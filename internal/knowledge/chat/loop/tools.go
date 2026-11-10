package loop

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"

	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/retrieval/twins"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/catalog"
)

const (
	searchHits   = 8   // chunks a search_code call returns
	prefixPool   = 25  // chunks searched for one under a folder, which it keeps searchHits of
	previewLines = 12  // lines of each chunk a search_code or users result shows
	maxRead      = 200 // lines one read_file call reads
	userHits     = 4   // chunks a users call returns
	maxLine      = 400 // bytes of a line a tool result shows
)

type (
	searchArgs struct {
		Query      string `json:"query" jsonschema_description:"English words and code identifiers (types, functions, files) to search for"`
		PathPrefix string `json:"path_prefix" jsonschema_description:"a folder to search under, such as src/Ordering.API/, or an empty string for the whole repository"`
	}
	readArgs struct {
		Path      string `json:"path" jsonschema_description:"the file's path, as a search result gives it"`
		StartLine int    `json:"start_line" jsonschema_description:"the first line to read, or 0 for the first of the file"`
		EndLine   int    `json:"end_line" jsonschema_description:"the last line to read, or 0 for as many as a call reads"`
	}
	usersArgs struct {
		Name string `json:"name" jsonschema_description:"a type, function or event, as the code spells it"`
	}
)

var (
	searchFormat = ai.Schema[searchArgs]("search_code")
	readFormat   = ai.Schema[readArgs]("read_file")
	usersFormat  = ai.Schema[usersArgs]("users")
	tools        = []ai.Tool{
		searchFormat.Tool(fmt.Sprintf("Search the repository's code by words and identifiers. Returns up to %d chunks: their path, lines and symbol, and their first %d lines.", searchHits, previewLines)),
		readFormat.Tool(fmt.Sprintf("Read lines of one of the repository's files, numbered: at most %d a call.", maxRead)),
		usersFormat.Tool(fmt.Sprintf("Find the code that uses a type, function or event: up to %d chunks of the files that import its definition and name it, such as its callers, handlers or publishers.", userHits)),
		ai.Schema[struct{}]("done").Tool("Say that the code gathered answers the question."),
	}
)

// run runs call, a tool's but done's, and returns what the model is told, with
// what the evidence's budget evicted or refused for it, and whether it added
// code not held before. Arguments that are not the tool's, a tool that does
// not exist, a call repeated or a tool that failed are told as such: they end
// nothing, but add nothing either. A call that failed may be made again.
func (s *state) run(ctx context.Context, call ai.ToolCall, emit Emit) (string, bool) {
	var (
		args any
		do   func() (string, []retrieval.Hit, error)
		err  error
	)
	switch call.Name {
	case searchFormat.Name:
		var a searchArgs
		a, err = ai.Decode(searchFormat, call.Arguments)
		args, do = a, func() (string, []retrieval.Hit, error) { return s.search(ctx, a) }
	case readFormat.Name:
		var a readArgs
		a, err = ai.Decode(readFormat, call.Arguments)
		args, do = a, func() (string, []retrieval.Hit, error) { return s.read(ctx, a) }
	case usersFormat.Name:
		var a usersArgs
		a, err = ai.Decode(usersFormat, call.Arguments)
		args, do = a, func() (string, []retrieval.Hit, error) { return s.usersOf(ctx, a) }
	default:
		s.res.Calls["unknown"]++ // the model's word, which no report should carry
		return fmt.Sprintf("Error: there is no tool %q. The tools are search_code, read_file, users and done.", call.Name), false
	}
	s.res.Calls[call.Name]++
	if err != nil {
		return fmt.Sprintf("Error: the arguments are %v. Call %s again with arguments of its schema.", err, call.Name), false
	}
	key, _ := json.Marshal(args)
	if s.seen[call.Name+string(key)] {
		return "Already called with these arguments: its result is above.", false
	}
	emit("search", map[string]any{"round": s.res.Rounds + 1, "tool": call.Name, "args": args})
	text, hits, err := do()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("chat: the tool %s failed: %v", call.Name, err)
		}
		return "Error: the tool failed.", false
	}
	s.seen[call.Name+string(key)] = true
	grew, note := s.add(call.Name, hits)
	return text + note + s.follow(ctx, hits, follows), grew
}

// follows is how far follow goes from a call's code: to the code using a
// message type it names, then on from each step's finds, three steps in all:
// eShop's payment handler (OrderPaymentSucceededIntegrationEventHandler) is
// the third step from the order-status code one of its payment questions
// finds first.
const follows = 3

// ident is an identifier, which follow looks up among the message types.
var ident = regexp.MustCompile(`[\p{L}_][\p{L}\p{N}_]*`)

// follow runs users itself, once a gathering, for each message type
// (twins.Names) that the code of hits the evidence holds declares or names:
// code refused, or evicted since, is not followed, nor is code past the type
// whose finds evicted it. What it finds joins the evidence as users' finds do,
// with the tool "follow"; then it follows from what it found, depth steps in
// all. It returns what the model is told: each type followed, with the ranges
// found, and what the budget evicted and refused for them. A type no code
// uses, or whose users fail, tells nothing.
func (s *state) follow(ctx context.Context, hits []retrieval.Hit, depth int) string {
	var b strings.Builder
	var next []retrieval.Hit
	for _, h := range hits {
		for _, n := range ident.FindAllString(h.Content, -1) {
			if !s.messages[n] || s.followed[n] {
				continue
			}
			if _, held := s.held[h.ChunkID]; !held && !s.covered(h) {
				break
			}
			s.followed[n] = true
			s.res.Calls["follow"]++
			using, err := s.g.Retriever.Users(ctx, s.req.Snapshot.ID, []string{n}, userHits)
			if err != nil && ctx.Err() == nil {
				log.Printf("chat: following %s failed: %v", n, err)
			}
			if len(using) == 0 {
				continue
			}
			s.res.Graph += len(using)
			at := make([]string, len(using))
			for i, u := range using {
				at[i] = fmt.Sprintf("%s:%d-%d", u.Path, u.StartLine, u.EndLine)
			}
			_, note := s.add("follow", using)
			fmt.Fprintf(&b, "Followed %s to: %s.\n%s", n, strings.Join(at, ", "), note)
			next = append(next, using...)
		}
	}
	if depth > 1 && len(next) > 0 {
		b.WriteString(s.follow(ctx, next, depth-1))
	}
	return b.String()
}

// search runs search_code: the hybrid search, under a folder when a's says.
// ponytail: the folder filters a search of prefixPool chunks; filter inside
// each search if a folder's code ranks below that.
func (s *state) search(ctx context.Context, a searchArgs) (string, []retrieval.Hit, error) {
	prefix, limit := strings.Trim(a.PathPrefix, "/"), searchHits
	if prefix != "" {
		limit = prefixPool
	}
	res, err := s.g.Retriever.Search(ctx, s.req.Snapshot.ID, a.Query, limit)
	if err != nil {
		return "", nil, err
	}
	s.res.FTS, s.res.Vector, s.res.Symbol, s.res.Named = s.res.FTS+res.FTS, s.res.Vector+res.Vector, s.res.Symbol+res.Symbol, s.res.Named+res.Named
	s.res.Graph += res.Graph
	hits := slices.DeleteFunc(res.Hits, func(h retrieval.Hit) bool {
		return prefix != "" && !strings.HasPrefix(h.Path, prefix+"/") && !strings.Contains(h.Path, "/"+prefix+"/")
	})
	hits = hits[:min(len(hits), searchHits)]
	if len(hits) == 0 {
		return "No code matched.", nil, nil
	}
	return list(hits), hits, nil
}

// read runs read_file: lines of the snapshot's file at a's path, or the one
// file whose path ends with it, as indexed, but those read before. Nothing
// outside the index is read: a path of the disk is no file of it.
func (s *state) read(ctx context.Context, a readArgs) (string, []retrieval.Hit, error) {
	missing := fmt.Sprintf("Not in this snapshot: no one indexed file is at %q. Use a path a search gave.", a.Path)
	paths, err := s.g.Retriever.Store.Files(ctx, s.req.Snapshot.ID, []string{a.Path}, 1)
	if err != nil || len(paths[0]) != 1 || (paths[0][0] != a.Path && !strings.HasSuffix(paths[0][0], "/"+a.Path)) {
		return missing, nil, err // a folder or a glob names no one file, though it holds one
	}
	f, err := s.g.Catalog.File(ctx, s.req.Snapshot, paths[0][0])
	if errors.Is(err, catalog.ErrNotFound) {
		return missing, nil, nil
	} else if err != nil {
		return "", nil, err
	}
	lines := strings.Split(strings.TrimSuffix(f.Content, "\n"), "\n")
	start, end := max(a.StartLine, 1), a.EndLine
	if end < start || end >= start+maxRead {
		end = start + maxRead - 1
	}
	if end = min(end, len(lines)); start > end {
		return fmt.Sprintf("%s has %d lines.", f.Path, len(lines)), nil, nil
	}
	from, to := start, end
	if start, end = s.unread(f.Path, start, end); start > end {
		return fmt.Sprintf("%s, lines %d-%d: read above.", f.Path, from, to), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, lines %d-%d of %d", f.Path, start, end, len(lines))
	if start != from || end != to {
		fmt.Fprintf(&b, ", the rest of lines %d-%d read above", from, to)
	}
	b.WriteString(":\n")
	for i, l := range lines[start-1 : end] {
		fmt.Fprintf(&b, "%d: %s\n", start+i, clip(l))
	}
	return b.String(), []retrieval.Hit{{Path: f.Path, StartLine: start, EndLine: end, Content: strings.Join(lines[start-1:end], "\n")}}, nil
}

// usersOf runs users: the code using a's name, which spans every declaration
// of the name, and a note when those are twins (package twins): one message
// type, sent in one project and received in another.
func (s *state) usersOf(ctx context.Context, a usersArgs) (string, []retrieval.Hit, error) {
	hits, err := s.g.Retriever.Users(ctx, s.req.Snapshot.ID, []string{a.Name}, userHits)
	if err != nil {
		return "", nil, err
	}
	twin, err := twins.Of(ctx, s.g.Retriever.Store, s.req.Snapshot.ID, a.Name)
	if err != nil {
		return "", nil, err
	}
	s.res.Graph += len(hits)
	text := fmt.Sprintf("No indexed file imports a definition of %s and names it.\n", a.Name)
	if len(hits) > 0 {
		text = list(hits)
	}
	if len(twin) > 0 {
		var where []string
		for _, t := range twin {
			where = append(where, fmt.Sprintf("%s (project %s)", t.Path, cmp.Or(t.Project, "at the root")))
		}
		text += fmt.Sprintf("Note: %s is one message type, each project declaring its own copy deriving from %s: %s. Code using any copy is about the same message: one project sends it, another receives it.\n",
			a.Name, twin[0].Base, strings.Join(where, ", "))
	}
	return text, hits, nil
}

// list shows hits as a search result: each one's path, lines and symbol,
// then its first lines, numbered.
func list(hits []retrieval.Hit) string {
	var b strings.Builder
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s:%d-%d", i+1, h.Path, h.StartLine, h.EndLine)
		if h.Symbol != "" {
			b.WriteString(" " + h.Symbol)
		}
		b.WriteString("\n")
		lines := strings.Split(h.Content, "\n")
		for j, l := range lines[:min(len(lines), previewLines)] {
			fmt.Fprintf(&b, "   %d: %s\n", h.StartLine+j, clip(l))
		}
	}
	return b.String()
}
