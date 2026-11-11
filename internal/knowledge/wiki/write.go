package wiki

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/knowledge/wiki/outline/contract"
	"deepcode/internal/knowledge/wiki/outline/skeleton"
	"deepcode/internal/platform/ai"
	aicontract "deepcode/internal/platform/ai/contract"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/repo/gitserver"
)

// drawnNote tells the model writing the overview that the page already shows
// a diagram, drawn from the import graph.
const drawnNote = "This page is the overview, and a diagram of the imports between the repository's directories is shown with it: draw no diagram of your own.\n\n"

// Writing is how writing a page went: the headings it was held to, those
// left out before writing as no source of theirs fit (skeleton.unsourced),
// those it lacks after the repair and code's fixes, whether a repair was
// asked, its time, and each rule's counts. The bench reads it.
type Writing struct {
	Page      string                               `json:"page"`
	Headings  []string                             `json:"headings"`
	Unsourced []string                             `json:"unsourced"`
	Lacking   []string                             `json:"lacking"`
	Repaired  bool                                 `json:"repaired"`
	Seconds   float64                              `json:"seconds"`
	Contracts map[aicontract.Rule]aicontract.Count `json:"contracts"`
}

// write drafts page p, one of pages, from its sources, held to its skeleton:
// the overview to the stack heading, a section's page to none, any other to
// the headings its plan gives (skeleton.Build). A heading none of whose
// source fits the evidence is left out first. The page is written as
// Markdown, which streams into the job's output. Each heading missing, empty
// or citing nothing that checks out is asked for again once; a page with no
// skeleton, when it cites nothing.
//
// The prompt runs from what every call shares to what only this page has, so
// the model server's prompt cache reads the start once: the rules, the wiki's
// own lines, the sources its section's pages share, then this page's request
// and its own sources.
func (g *Generator) write(ctx context.Context, snap int64, repo string, src source, p page, pages []page, home map[string]string, progress jobs.Progress) (string, []evidence.Citation, error) {
	start := time.Now()
	head, inSection := section(p, pages)
	sk, err := g.skeleton(ctx, snap, p, inSection && head.Position == p.Position)
	if err != nil {
		return "", nil, err
	}
	shared, own, err := g.sources(ctx, snap, p, head, inSection, pages, anchors(sk))
	if err != nil {
		return "", nil, err
	}
	common, used, _ := evidence.Format(shared, evidence.Limit{Chars: sectionChars})
	own = slices.DeleteFunc(own, func(h retrieval.Hit) bool { // what the shared blocks hold already
		return slices.ContainsFunc(used, func(u retrieval.Hit) bool { return u.ChunkID == h.ChunkID })
	})
	sources, mine, _ := evidence.Format(own, evidence.Limit{Chars: evidenceChars - len(common)})
	used = append(used, mine...)
	if len(used) == 0 {
		return "No indexed code matched this topic, so it cannot be confirmed from the current code.", nil, nil
	}
	var counts aicontract.Counts
	sk, unsourced := sourced(sk, used)
	counts.Residual("skeleton.unsourced", len(unsourced))
	progress.Logf("%d source blocks (%d shared with the section) from %d search hits; %d headings, %d left out unsourced", len(used), len(used)-len(mine), len(shared)+len(own), len(sk), len(unsourced))
	if common != "" {
		common = fmt.Sprintf("Sources the pages of the section %q share:\n\n%s", head.Title, common)
	}
	overview := ""
	if p.Position == 0 {
		if d, err := g.Graph.Diagram(ctx, snap); err != nil {
			return "", nil, err
		} else if d != "" {
			overview = drawnNote
		}
	}
	titles, parts, section := make([]string, len(pages)), []string{}, ""
	for i, q := range pages {
		titles[i] = q.Title
		if q.Parent == p.Position && q.Position != p.Position {
			parts = append(parts, q.Title)
		} else if q.Position == p.Parent {
			section = q.Title
		}
	}
	place := ""
	if len(parts) > 0 {
		place = fmt.Sprintf("This page opens a section whose pages are: %s. Give the section's overview: what its part of the code does and how those pages' subjects fit together, leaving each one's details to its page.\n\n", strings.Join(parts, " | "))
	} else if section != "" {
		place = fmt.Sprintf("This page is part of the section %q: keep to its own subject, which the section's other pages do not cover.\n\n", section)
	}
	var files []string
	if p.Files != nil {
		files = append(slices.Clone(p.Files.Chosen), p.Files.Placed...)
	}
	ask := ai.Prompt{Static: skeleton.Rules, Dynamic: fmt.Sprintf("Repository: %s at commit %s. Write in %s.\nPages of this wiki: %s\n\n%s%s%sWrite the page %q. %s\nSources:\n\n%s",
		repo, src.sha[:min(7, len(src.sha))], g.Language, strings.Join(titles, " | "), common, overview, place, p.Title, skeleton.Listed(sk, files), sources)}
	link := func(c evidence.Citation) string {
		return gitserver.SourceURL(src.kind, src.url, src.sha, c.Path, c.StartLine, c.EndLine)
	}
	cited := func(text string) int {
		_, cs, _ := evidence.LinkCitations(text, used, link)
		return len(cs)
	}
	turn := aicontract.Turn[skeleton.Draft]{Kind: "write", Check: skeleton.Check(sk, cited), Merge: skeleton.Merge(sk), Fix: skeleton.Fix,
		Note: skeleton.Note(sk, fmt.Sprintf("[%s:%d-%d]", used[0].Path, used[0].StartLine, used[0].EndLine)),
		Ask: func(ctx context.Context, p ai.Prompt) (skeleton.Draft, string, error) {
			write, flush := progress.Lines()
			out, err := g.Chat.Stream(ctx, p, write)
			flush()
			return skeleton.Parse(clean(out), sk), out, err
		}}
	d, err := aicontract.Run(ctx, ask, turn, &counts)
	if err != nil {
		return "", nil, err
	}
	content, cites, _ := evidence.LinkCitations(skeleton.Render(d), used, link)
	if p.Files != nil && !(inSection && head.Position == p.Position) {
		more, err := g.otherFiles(ctx, snap, src, p, sk, home)
		if err != nil {
			return "", nil, err
		}
		content += more
	}
	if g.Written != nil {
		w := Writing{Page: p.Title, Unsourced: unsourced, Lacking: skeleton.Lacking(sk, d), Seconds: time.Since(start).Seconds(), Contracts: counts.Snapshot()}
		for _, h := range sk {
			w.Headings = append(w.Headings, h.Name)
		}
		for r, c := range w.Contracts {
			w.Repaired = w.Repaired || r != "skeleton.unsourced" && c.First > 0 // every rule a first reply broke is asked again
		}
		g.Written(w)
	}
	return content, cites, nil
}

// otherFiles is what code adds last to page p, written to sk, under the
// wiki's label: each file and aside unit of p no heading covers
// (skeleton.Others, home the code each guide shows), a file linked with the
// first declarations it holds, a folder by its path; "" when every one has a
// heading. No model writes it.
func (g *Generator) otherFiles(ctx context.Context, snap int64, src source, p page, sk skeleton.Skeleton, home map[string]string) (string, error) {
	others := skeleton.Others(sk, append(slices.Clone(p.Files.Chosen), p.Files.Placed...), p.Files.Units, home)
	if len(others) == 0 {
		return "", nil
	}
	decls, err := g.Store.Decls(ctx, snap, others)
	if err != nil {
		return "", err
	}
	labels, err := g.Store.Labels(ctx, snap)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n## %s\n", cmp.Or(labels.OtherFiles, contract.English.OtherFiles))
	for _, o := range others {
		if strings.HasSuffix(o, "/") {
			fmt.Fprintf(&b, "\n- `%s`", o)
			continue
		}
		fmt.Fprintf(&b, "\n- [%s](%s)", o, gitserver.FileURL(src.kind, src.url, src.sha, o))
		var names []string
		for _, d := range decls[o][:min(maxDeclared, len(decls[o]))] {
			names = append(names, "`"+d.Name+"`")
		}
		if len(names) > 0 {
			b.WriteString(": " + strings.Join(names, ", "))
		}
	}
	return b.String(), nil
}

// maxDeclared is the most declarations the Other files list names a file by.
const maxDeclared = 3

// skeleton is the headings page p is held to: the overview's, the stack's
// under the wiki's label; a section's page (head), and a page planned before
// pages had files, none; any other's, those its plan gives.
func (g *Generator) skeleton(ctx context.Context, snap int64, p page, head bool) (skeleton.Skeleton, error) {
	if p.Position == 0 {
		labels, err := g.Store.Labels(ctx, snap)
		return skeleton.Skeleton{{Name: cmp.Or(labels.Stack, contract.English.Stack), Level: 2}}, err
	}
	if head || p.Files == nil {
		return nil, nil
	}
	texts, err := g.Store.Texts(ctx, snap, skeleton.Texts(p.Files.Units))
	if err != nil {
		return nil, err
	}
	decls, err := g.Store.Decls(ctx, snap, p.Files.Chosen)
	if err != nil {
		return nil, err
	}
	lines, err := g.Store.Lines(ctx, snap)
	if err != nil {
		return nil, err
	}
	files := append(slices.Clone(p.Files.Chosen), p.Files.Placed...)
	return skeleton.Build(p.Files.Units, p.Files.Chosen, files, skeleton.Source{Texts: texts, Decls: decls, Lines: lines}, pageHits), nil
}

// anchors are the lines sk's headings rest on, those that have one.
func anchors(sk skeleton.Skeleton) []retrieval.Anchor {
	var out []retrieval.Anchor
	for _, h := range sk {
		if h.Anchor.Path != "" {
			out = append(out, h.Anchor)
		}
	}
	return out
}

// sourced is sk without the headings whose line no source in used shows,
// those under a level-2 one left out going with it, and the names of those
// left out. A fixed heading, with no line, stays.
func sourced(sk skeleton.Skeleton, used []retrieval.Hit) (skeleton.Skeleton, []string) {
	var kept skeleton.Skeleton
	var out []string
	dropped := false // the level-2 heading above is left out
	for _, h := range sk {
		shown := h.Unit == "" && h.Anchor.Path == "" || h.Anchor.Path != "" && slices.ContainsFunc(used, func(u retrieval.Hit) bool {
			return u.Path == h.Anchor.Path && (h.Anchor.Line == 0 || u.StartLine <= h.Anchor.Line && h.Anchor.Line <= u.EndLine)
		})
		if h.Level == 2 {
			dropped = !shown
		}
		if !shown || dropped {
			out = append(out, h.Name)
			continue
		}
		kept = append(kept, h)
	}
	return kept, out
}

// sources is the evidence for page p: shared, the search hits of the section
// it is in (head, when inSection), on the head's title and terms and the
// first chosen file of each of its pages; and own, the hits for its own
// title, terms, files and the lines its headings rest on (anchors), after the
// README for the overview. A section's page searches once: its hits are both.
func (g *Generator) sources(ctx context.Context, snap int64, p, head page, inSection bool, pages []page, anchors []retrieval.Anchor) (shared, own []retrieval.Hit, err error) {
	if inSection {
		var firsts []string
		for _, q := range pages {
			if q.Parent == head.Position && q.Position != head.Position && q.Files != nil && len(q.Files.Chosen) > 0 {
				firsts = append(firsts, q.Files.Chosen[0])
			}
		}
		res, err := g.Retriever.SearchPage(ctx, snap, words(head), firsts, nil, pageHits)
		if err != nil {
			return nil, nil, err
		}
		shared = res.Hits
	}
	if inSection && head.Position == p.Position {
		own = slices.Clone(shared)
	} else {
		var files []string
		if p.Files != nil {
			files = append(slices.Clone(p.Files.Chosen), p.Files.Placed...)
		}
		res, err := g.Retriever.SearchPage(ctx, snap, words(p), files, anchors, pageHits)
		if err != nil {
			return nil, nil, err
		}
		own = res.Hits
	}
	if p.Position != 0 {
		return shared, own, nil
	}
	readme, err := g.Retriever.Readme(ctx, snap)
	if err != nil {
		return nil, nil, err
	}
	return shared, dedupe(append(readme, own...)), nil
}

// words are what page p's search looks for: its title and its terms.
func words(p page) string { return p.Title + "\n" + p.Query }

// section is the section page p is in: p itself when pages sit under it, or
// the page it sits under; false for a page in no section.
func section(p page, pages []page) (page, bool) {
	for _, q := range pages {
		if p.Parent >= 0 && q.Position == p.Parent {
			return q, true
		}
		if p.Parent < 0 && q.Parent == p.Position {
			return p, true
		}
	}
	return page{}, false
}

// clean drops reasoning blocks, a wrapping code fence and a leading "#" title.
func clean(s string) string {
	s = ai.StripThinking(s)
	if strings.HasPrefix(s, "```") && strings.HasSuffix(s, "```") {
		if _, body, ok := strings.Cut(s, "\n"); ok {
			s = strings.TrimSpace(strings.TrimSuffix(body, "```"))
		}
	}
	if strings.HasPrefix(s, "# ") {
		_, s, _ = strings.Cut(s, "\n")
	}
	return strings.TrimSpace(s)
}

func dedupe(hits []retrieval.Hit) []retrieval.Hit {
	seen := map[int64]bool{}
	var out []retrieval.Hit
	for _, h := range hits {
		if !seen[h.ChunkID] {
			seen[h.ChunkID] = true
			out = append(out, h)
		}
	}
	return out
}
