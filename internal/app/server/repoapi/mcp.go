package repoapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/access/token"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/knowledge/chat"
	wikiview "deepcode/internal/knowledge/wiki/view"
	"deepcode/internal/repo/catalog"
)

// MountMCP adds /mcp, the MCP server that agents (Claude Code, Cursor,
// Codex, ...) reach with a bearer token: a personal access token, or one an
// app got through OAuth, acting with its user's roles. It is stateless
// Streamable HTTP: each POST is one call, answered as a short event stream;
// GET and DELETE get 405. Beside it, the protected resource metadata (RFC
// 9728) that sends an app without a token to OAuth.
func (a *API) MountMCP(r gin.IRouter) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "deepcode", Title: "deepcode", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Answers questions about the code repositories indexed on this deepcode server. " +
			"Start with list_repositories; read_wiki_structure and read_wiki_contents read a repository's wiki, " +
			"search_code and read_file read its code, and ask_question has deepcode answer with cited sources.",
	})
	a.addTools(srv)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// Guard stops DNS rebinding already, knowing whether a proxy is in front;
		// the SDK's own check would refuse every request a local proxy forwards.
		DisableLocalhostProtection: true,
	})
	r.GET("/.well-known/oauth-protected-resource", a.resourceMetadata)
	r.GET("/.well-known/oauth-protected-resource/mcp", a.resourceMetadata)
	r.Any("/mcp", func(c *gin.Context) {
		auth.RequireBearerToken(a.verify, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL:    httpx.BaseURL(c) + "/.well-known/oauth-protected-resource/mcp",
			AllowMissingExpiration: true, // a personal token may last until revoked
		})(handler).ServeHTTP(c.Writer, c.Request)
	})
}

// resourceMetadata names /mcp and the authorization server for it: this
// server, at the address the app used.
func (a *API) resourceMetadata(c *gin.Context) {
	base := httpx.BaseURL(c)
	auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource: base + "/mcp", AuthorizationServers: []string{base}, BearerMethodsSupported: []string{"header"}, ResourceName: "deepcode",
	}).ServeHTTP(c.Writer, c.Request)
}

// verify finds who a bearer token signs in. Why a check failed stays in the
// log: the answer goes to whoever sent the token.
func (a *API) verify(ctx context.Context, secret string, _ *http.Request) (*auth.TokenInfo, error) {
	p, expires, err := a.Tokens.Authenticate(ctx, secret)
	if errors.Is(err, token.ErrNoToken) {
		return nil, auth.ErrInvalidToken
	}
	if err != nil {
		log.Printf("mcp: checking a token: %v", err)
		return nil, errors.New("checking the token failed")
	}
	return &auth.TokenInfo{UserID: strconv.FormatInt(p.UserID, 10), Expiration: expires,
		Extra: map[string]any{"principal": p, "secret": secret}}, nil // watch checks the token again while answering
}

func principal(req *mcp.CallToolRequest) *session.Principal {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	p, _ := req.Extra.TokenInfo.Extra["principal"].(*session.Principal)
	return p
}

// repo finds the repository name names (owner/name, its id or its web URL)
// among those the caller may read, which they must also be allowed perm on.
func (a *API) repo(ctx context.Context, req *mcp.CallToolRequest, name string, perm role.Permission) (catalog.Repo, error) {
	p := principal(req)
	if p == nil {
		return catalog.Repo{}, errors.New("not signed in")
	}
	list, err := a.Catalog.List(ctx, func(id int64) bool { return p.CanOn(role.RepoRead, id) })
	if err != nil {
		return catalog.Repo{}, a.failed(err)
	}
	name = strings.TrimSuffix(strings.TrimSpace(name), "/")
	var found []catalog.Repo
	for _, r := range list {
		if r.PublicID == name || strings.EqualFold(r.Owner+"/"+r.Name, name) || strings.EqualFold(strings.TrimSuffix(r.URL, ".git"), name) {
			found = append(found, r)
		}
	}
	switch {
	case len(found) == 0:
		return catalog.Repo{}, fmt.Errorf("no repository %q that you can read; list_repositories names them", name)
	case len(found) > 1:
		return catalog.Repo{}, fmt.Errorf("more than one repository is called %q: name it by id (list_repositories)", name)
	case !p.CanOn(perm, found[0].ID):
		return catalog.Repo{}, fmt.Errorf("your roles do not allow %s on %s", perm, name)
	case found[0].Snapshot == nil:
		return catalog.Repo{}, fmt.Errorf("%s is still being indexed; try again later", name)
	}
	return found[0], nil
}

// failed logs err and gives the agent a line that quotes no SQL or path.
func (a *API) failed(err error) error {
	log.Printf("mcp: %v", err)
	return errors.New("the server failed; its administrator can find why in the server log")
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// maxText bounds what one tool returns, which lands in an agent's context.
const maxText = 100_000

// cut is s within n bytes, marked where it was cut.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}

type (
	noArgs   struct{}
	repoArgs struct {
		Repo string `json:"repo" jsonschema:"the repository: owner/name as list_repositories gives it, or its id"`
	}
	pageArgs struct {
		Repo string `json:"repo" jsonschema:"the repository: owner/name as list_repositories gives it, or its id"`
		Page string `json:"page,omitempty" jsonschema:"a page's number (2.1), slug or title, as read_wiki_structure lists them; empty reads every page, which can be long"`
	}
	searchArgs struct {
		Repo  string `json:"repo" jsonschema:"the repository: owner/name as list_repositories gives it, or its id"`
		Query string `json:"query" jsonschema:"what to look for: identifiers, words or a question"`
		Limit int    `json:"limit,omitempty" jsonschema:"how many passages, 1 to 20; 10 when left out"`
	}
	fileArgs struct {
		Repo  string `json:"repo" jsonschema:"the repository: owner/name as list_repositories gives it, or its id"`
		Path  string `json:"path" jsonschema:"the file's path in the repository"`
		Start int    `json:"start_line,omitempty" jsonschema:"the first line to read; 1 when left out"`
		End   int    `json:"end_line,omitempty" jsonschema:"the last line to read; at most 1000 lines are read at a time"`
	}
	askArgs struct {
		Repo     string `json:"repo" jsonschema:"the repository: owner/name as list_repositories gives it, or its id"`
		Question string `json:"question" jsonschema:"the question, in any language"`
		Mode     string `json:"mode,omitempty" jsonschema:"fast (when left out); deep: more search rounds and the wiki read first, slower; or codemap: the code paths that answer it, as traces of numbered code locations"`
	}
)

func (a *API) addTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{Name: "list_repositories", Annotations: readOnly,
		Description: "Lists the repositories you can read on this deepcode server, with their ids and wiki status."}, a.listTool)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_wiki_structure", Annotations: readOnly,
		Description: "Lists a repository's wiki pages, numbered and nested as the wiki shows them."}, a.wikiStructureTool)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_wiki_contents", Annotations: readOnly,
		Description: "Reads a page of a repository's wiki (or all of them): Markdown whose citations link to the source lines."}, a.wikiContentsTool)
	mcp.AddTool(srv, &mcp.Tool{Name: "search_code", Annotations: readOnly,
		Description: "Searches a repository's indexed code by words, identifiers and meaning, and returns the best passages with their paths and lines."}, a.searchTool)
	mcp.AddTool(srv, &mcp.Tool{Name: "read_file", Annotations: readOnly,
		Description: "Reads lines of a file of a repository, as indexed, numbered."}, a.fileTool)
	mcp.AddTool(srv, &mcp.Tool{Name: "ask_question", Annotations: &mcp.ToolAnnotations{OpenWorldHint: new(false), DestructiveHint: new(false)},
		Description: "Asks deepcode a question about a repository: it searches the code and answers with citations it checked against the " +
			"sources. Takes from half a minute to several minutes; the answer is kept in your deepcode conversations."}, a.askTool)
}

func (a *API) listTool(ctx context.Context, req *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
	p := principal(req)
	if p == nil {
		return nil, nil, errors.New("not signed in")
	}
	list, err := a.Catalog.List(ctx, func(id int64) bool { return p.CanOn(role.RepoRead, id) })
	if err != nil {
		return nil, nil, a.failed(err)
	}
	if len(list) == 0 {
		return text("You can read no repository on this server."), nil, nil
	}
	var b strings.Builder
	for _, r := range list {
		fmt.Fprintf(&b, "- %s/%s (id %s)", r.Owner, r.Name, r.PublicID)
		if r.Description != "" {
			fmt.Fprintf(&b, ": %s", r.Description)
		}
		if s := r.Snapshot; s == nil {
			b.WriteString(". Not indexed yet.\n")
		} else {
			fmt.Fprintf(&b, ". Indexed at commit %.7s; wiki: %s.\n", s.CommitSHA, s.WikiStatus)
		}
	}
	return text(b.String()), nil, nil
}

// numbered gives each wiki page its number as the wiki shows it: 2, and 2.1
// for the first page under it.
func numbered(pages []wikiview.Page) []string {
	nums, at, under, top := make([]string, len(pages)), map[string]int{}, map[string]int{}, 0
	for i, p := range pages {
		if j, ok := at[p.Parent]; ok && p.Parent != "" {
			under[p.Parent]++
			nums[i] = fmt.Sprintf("%s.%d", nums[j], under[p.Parent])
		} else {
			top++
			nums[i] = strconv.Itoa(top)
		}
		at[p.Slug] = i
	}
	return nums
}

// wikiOf loads the repository's wiki, or says why it has none.
func (a *API) wikiOf(ctx context.Context, req *mcp.CallToolRequest, name string) (wikiview.Wiki, error) {
	r, err := a.repo(ctx, req, name, role.RepoRead)
	if err != nil {
		return wikiview.Wiki{}, err
	}
	w, err := a.Wiki.Load(ctx, r.ID)
	if err != nil {
		return w, a.failed(err)
	}
	if len(w.Pages) == 0 {
		return w, fmt.Errorf("%s has no wiki yet (status: %s); search_code and ask_question work without one", name, w.Status)
	}
	return w, nil
}

func (a *API) wikiStructureTool(ctx context.Context, req *mcp.CallToolRequest, in repoArgs) (*mcp.CallToolResult, any, error) {
	w, err := a.wikiOf(ctx, req, in.Repo)
	if err != nil {
		return nil, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Wiki of %s at commit %.7s:\n", in.Repo, w.CommitSHA)
	for i, n := range numbered(w.Pages) {
		indent := ""
		if strings.Contains(n, ".") {
			indent = "  "
		}
		fmt.Fprintf(&b, "%s- %s %s\n", indent, n, w.Pages[i].Title)
	}
	return text(b.String()), nil, nil
}

func (a *API) wikiContentsTool(ctx context.Context, req *mcp.CallToolRequest, in pageArgs) (*mcp.CallToolResult, any, error) {
	w, err := a.wikiOf(ctx, req, in.Repo)
	if err != nil {
		return nil, nil, err
	}
	want := strings.TrimSpace(in.Page)
	var b strings.Builder
	var rest []string // the pages that would not fit
	for i, n := range numbered(w.Pages) {
		p := w.Pages[i]
		if want != "" && want != n && !strings.EqualFold(want, p.Slug) && !strings.EqualFold(want, p.Title) {
			continue
		}
		part := fmt.Sprintf("# %s %s\n\n%s", n, p.Title, p.Content)
		if b.Len() > 0 && b.Len()+len(part) > maxText {
			rest = append(rest, n)
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(part)
	}
	if b.Len() == 0 {
		return nil, nil, fmt.Errorf("no page %q; read_wiki_structure lists them", want)
	}
	out := cut(b.String(), maxText)
	if b.Len() > maxText {
		out += fmt.Sprintf("\n\n(the page was cut at %d KB; read_file and search_code read the code it cites)", maxText/1000)
	}
	if len(rest) > 0 {
		out += fmt.Sprintf("\n\n---\n\n%d more pages did not fit; read them one at a time by number: %s", len(rest), strings.Join(rest, ", "))
	}
	return text(out), nil, nil
}

func (a *API) searchTool(ctx context.Context, req *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, any, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" || len(q) > maxQuery {
		return nil, nil, fmt.Errorf("query must be 1 to %d bytes", maxQuery)
	}
	r, err := a.repo(ctx, req, in.Repo, role.RepoRead)
	if err != nil {
		return nil, nil, err
	}
	limit := in.Limit
	if limit == 0 {
		limit = 10
	}
	res, err := a.Retriever.Search(ctx, r.Snapshot.ID, q, min(max(limit, 1), 20))
	if err != nil {
		return nil, nil, a.failed(err)
	}
	if len(res.Hits) == 0 {
		return text("Nothing in the indexed code matches."), nil, nil
	}
	var b strings.Builder
	for _, h := range res.Hits {
		fmt.Fprintf(&b, "## %s:%d-%d", h.Path, h.StartLine, h.EndLine)
		if h.Symbol != "" {
			fmt.Fprintf(&b, " (%s)", h.Symbol)
		}
		fmt.Fprintf(&b, "\n```\n%s\n```\n\n", cut(h.Content, 1500))
	}
	return text(b.String()), nil, nil
}

// maxLines bounds the lines read_file returns at once, maxLine each line
// (a minified bundle is one line).
const (
	maxLines = 1000
	maxLine  = 2000
)

func (a *API) fileTool(ctx context.Context, req *mcp.CallToolRequest, in fileArgs) (*mcp.CallToolResult, any, error) {
	r, err := a.repo(ctx, req, in.Repo, role.RepoRead)
	if err != nil {
		return nil, nil, err
	}
	path := strings.TrimPrefix(strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(in.Path), `\`, "/"), "./"), "/")
	f, err := a.Catalog.File(ctx, catalog.Ref{ID: r.Snapshot.ID, CommitSHA: r.Snapshot.CommitSHA}, path)
	if errors.Is(err, catalog.ErrNotFound) {
		return nil, nil, fmt.Errorf("no file %q in the indexed snapshot; search_code finds paths", in.Path)
	} else if err != nil {
		return nil, nil, a.failed(err)
	}
	lines := strings.Split(strings.TrimSuffix(f.Content, "\n"), "\n")
	start := max(in.Start, 1)
	end := in.End
	if end == 0 || end > len(lines) {
		end = len(lines)
	}
	end = min(end, start+maxLines-1)
	if start > len(lines) || end < start {
		return nil, nil, fmt.Errorf("lines %d-%d are outside %s, which has %d", start, end, in.Path, len(lines))
	}
	var b strings.Builder
	width := len(strconv.Itoa(end))
	for i := start; i <= end; i++ {
		if b.Len() > maxText {
			end = i - 1
			break
		}
		fmt.Fprintf(&b, "%*d  %s\n", width, i, cut(strings.TrimSuffix(lines[i-1], "\r"), maxLine))
	}
	head := fmt.Sprintf("%s, lines %d-%d of %d, at commit %.7s:\n", f.Path, start, end, len(lines), f.CommitSHA)
	if end < len(lines) && (in.End == 0 || end < in.End) { // cut short of what was asked
		fmt.Fprintf(&b, "(read on from start_line %d)\n", end+1)
	}
	return text(head + b.String()), nil, nil
}

// asking tells an agent's user what a long answer is doing, by the chat's
// events: once per step, not per drawing event.
var asking = map[string]string{"conversation": "searching the code", "search": "reading the code",
	"evidence": "writing the answer", "drawing": "drawing the code map", "done": "done"}

// errRevoked: the token stopped working, or allowing the question, while it
// was being answered.
var errRevoked = errors.New("the token was revoked, or no longer allows asking about this repository")

// watch cancels ctx, with errRevoked as its cause, once the caller's token
// stops signing them in or allowed rejects them: checked every 20 seconds, as
// a web answer's session is. Call stop before returning.
func (a *API) watch(ctx context.Context, req *mcp.CallToolRequest, allowed func(*session.Principal) bool) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	secret, _ := req.Extra.TokenInfo.Extra["secret"].(string)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			p, _, err := a.Tokens.Authenticate(ctx, secret)
			if errors.Is(err, token.ErrNoToken) || err == nil && !allowed(p) {
				cancel(errRevoked)
				return
			} else if err != nil && ctx.Err() == nil {
				log.Printf("mcp: checking a token again: %v", err) // the answer goes on: the next check may succeed
			}
		}
	}()
	return ctx, func() { cancel(nil); <-done }
}

func (a *API) askTool(ctx context.Context, req *mcp.CallToolRequest, in askArgs) (*mcp.CallToolResult, any, error) {
	mode := strings.ToLower(strings.TrimSpace(in.Mode))
	if mode != "" && mode != "fast" && mode != "deep" && mode != "codemap" {
		return nil, nil, fmt.Errorf("mode must be fast, deep or codemap, not %q", in.Mode)
	}
	r, err := a.repo(ctx, req, in.Repo, role.RepoChat)
	if err != nil {
		return nil, nil, err
	}
	ctx, stop := a.watch(ctx, req, func(p *session.Principal) bool { return p.CanOn(role.RepoRead, r.ID) && p.CanOn(role.RepoChat, r.ID) })
	defer stop()
	var answer *chat.Message
	step, last, progress := 0.0, "", req.Params.GetProgressToken()
	emit := func(event string, data any) {
		if m, ok := data.(chat.Message); ok && event == "done" {
			answer = &m
		}
		msg, ok := asking[event]
		if event == "evidence" && mode == "codemap" {
			msg = asking["drawing"] // a map is drawn, not written, from the evidence on
		}
		if ok && progress != nil && msg != last {
			step, last = step+1, msg
			req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: progress, Progress: step, Message: msg})
		}
	}
	err = a.Chat.Ask(ctx, chat.Request{RepoID: r.ID, Question: in.Question, Deep: mode == "deep", Codemap: mode == "codemap",
		UserID: principal(req).UserID}, emit)
	if errors.Is(context.Cause(ctx), errRevoked) {
		return nil, nil, errRevoked
	}
	if err != nil {
		msg, logIt := chat.Explain(err)
		if logIt {
			log.Printf("mcp: ask_question: repository %d: %v", r.ID, err)
		}
		return nil, nil, errors.New(msg)
	}
	if answer == nil {
		return nil, nil, a.failed(errors.New("ask_question: the chat ended without an answer"))
	}
	return text(cut(answer.Content, maxText)), nil, nil
}
