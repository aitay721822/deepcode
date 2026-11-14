package rival

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// Endpoint is DeepWiki's public MCP server, which needs no sign-in for a public repository.
	Endpoint = "https://mcp.deepwiki.com/mcp"
	// Searches is the API DeepWiki's own search page reads a kept search from, with no sign-in: the MCP
	// tool's answer holds no citations, the search does. It has no published contract, so a change of
	// it fails the capture; the page's links (github.com/<repo>/blob/<commit>/<path>#Lx-Ly) say the same.
	Searches = "https://api.devin.ai/ada/query/"
	maxReply = 64 << 20 // a search's reply holds the files DeepWiki read, whole
)

// slug names the search an answer is kept as: "View this search on DeepWiki: https://deepwiki.com/search/<slug>".
var slug = regexp.MustCompile(`deepwiki\.com/search/([A-Za-z0-9_-]+)`)

// Connect opens a session with the MCP server at endpoint, request and reply only.
func Connect(ctx context.Context, endpoint string) (*mcp.ClientSession, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "deepcode-bench", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, DisableStandaloneSSE: true}, nil)
}

// Asker asks DeepWiki through Session and reads what each answer cites from the search it is kept as,
// at Searches+<slug>, over HTTP.
type Asker struct {
	Session  *mcp.ClientSession
	HTTP     *http.Client
	Searches string
}

// Ask asks DeepWiki question about repo (owner/name), then reads the answer's citations from the
// search it names; an answer naming none fails, as what it cites cannot be read.
func (a Asker) Ask(ctx context.Context, repo, question string) (Answer, error) {
	res, err := a.Session.CallTool(ctx, &mcp.CallToolParams{Name: "ask_question", Arguments: map[string]any{"repoName": repo, "question": question}})
	if err != nil {
		return Answer{}, err
	}
	var text strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			text.WriteString(t.Text)
		}
	}
	if res.IsError {
		return Answer{}, fmt.Errorf("DeepWiki failed: %s", text.String())
	}
	ans := Answer{Repo: repo, Question: question, Captured: time.Now().UTC().Format(time.RFC3339), Text: text.String()}
	m := slug.FindStringSubmatch(ans.Text)
	if m == nil {
		return Answer{}, errors.New("DeepWiki's answer names no search to read its citations from")
	}
	ans.Slug = m[1]
	ans.Commit, ans.Refs, ans.Read, err = a.cites(ctx, ans.Slug)
	return ans, err
}

// search is what cites reads of a kept search: its last query's state, the snapshot it read
// ("v1.9.9.5/PUBLIC/go-chi/chi/a54874f0") and its events.
type search struct {
	Queries []struct {
		State    string   `json:"state"`
		Contexts []string `json:"repo_context_ids"`
		Response []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"response"`
	} `json:"queries"`
}

// reference is a reference event's data: "Repo go-chi/chi: context.go", its lines counted from 0.
type reference struct {
	Path  string `json:"file_path"`
	Start int    `json:"range_start"`
	End   int    `json:"range_end"`
}

// cites reads the search kept as slug: the commit DeepWiki read, the ranges its answer cites and the
// files it read (file_contents events: [repo, path, content]).
func (a Asker) cites(ctx context.Context, slug string) (string, []Ref, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Searches+url.PathEscape(slug), nil)
	if err != nil {
		return "", nil, nil, err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, nil, fmt.Errorf("DeepWiki's search %s: %s", slug, resp.Status)
	}
	var s search
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReply)).Decode(&s); err != nil {
		return "", nil, nil, fmt.Errorf("DeepWiki's search %s: %w", slug, err)
	}
	if len(s.Queries) == 0 || s.Queries[len(s.Queries)-1].State != "done" {
		return "", nil, nil, fmt.Errorf("DeepWiki's search %s is not done", slug)
	}
	q := s.Queries[len(s.Queries)-1]
	var commit string
	if len(q.Contexts) > 0 {
		commit = q.Contexts[0][strings.LastIndex(q.Contexts[0], "/")+1:]
	}
	var refs []Ref
	var read []string
	for _, e := range q.Response {
		switch e.Type {
		case "reference":
			var r reference
			if err := json.Unmarshal(e.Data, &r); err != nil {
				return "", nil, nil, fmt.Errorf("DeepWiki's search %s: a reference: %w", slug, err)
			}
			refs = append(refs, Ref{Path: inRepo(r.Path), Start: r.Start + 1, End: r.End + 1})
		case "file_contents":
			var f []string
			if err := json.Unmarshal(e.Data, &f); err == nil && len(f) > 1 && !slices.Contains(read, f[1]) {
				read = append(read, f[1])
			}
		}
	}
	return commit, refs, read, nil
}

// inRepo is a reference's path in its repository: "Repo go-chi/chi: context.go" is context.go.
func inRepo(p string) string {
	if rest, ok := strings.CutPrefix(p, "Repo "); ok {
		if _, path, ok := strings.Cut(rest, ": "); ok {
			return path
		}
	}
	return p
}
