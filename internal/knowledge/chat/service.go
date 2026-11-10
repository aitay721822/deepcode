// Package chat answers questions about a snapshot: retrieve evidence, stream
// the model's answer, validate its citations and store the conversation.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"deepcode/internal/knowledge/chat/background"
	"deepcode/internal/knowledge/chat/harness"
	"deepcode/internal/knowledge/chat/loop"
	"deepcode/internal/knowledge/chat/store"
	"deepcode/internal/knowledge/codemap"
	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

type Service struct {
	Store     *store.Store
	Catalog   *catalog.Catalog
	Retriever *retrieval.Retriever
	Chat      *ai.OpenAI      // nil: not configured
	Tokens    *harness.Tokens // counts Chat's tokens (harness.LoadTokens)

	mu     sync.Mutex
	asking map[int64]int // answers being written, by user
}

// What an asker can act on. Explain shows these, and only these, to them.
var (
	ErrNotFound = store.ErrNotFound
	errQuestion = errors.New("問題長度必須為 1 到 4000 個字元")
	errNoModel  = errors.New("尚未設定對話模型：請設定 DEEPCODE_CHAT_MODEL")
	errIndexing = errors.New("Repository 仍在建立索引中")
	errBusy     = fmt.Errorf("同時最多只能產生 %d 個回答；請等其中一個完成後再提問", maxAsking)
	errModel    = errors.New("語言模型沒有回應；請稍後再試")
	errMap      = errors.New("語言模型沒有畫出有效的 Codemap；請再試一次，或改用一般問答")
	errSchema   = errors.New("語言模型伺服器不支援 structured outputs（json_schema），無法畫出 Codemap；請管理員改用支援的端點")
	errContext  = errors.New("問題連同程式碼超出了語言模型的上下文；請管理員把 DEEPCODE_CHAT_CONTEXT 設為模型的上下文大小")
)

// Explain is what the asker is told about err from Ask: one of the errors
// above, a context too small for the question (harness.ErrSmall, its words
// naming the context it needs), or a generic line, since other errors may
// quote SQL or a server's answer. logIt reports whether err belongs in the
// server log: the model's failures and unexplained errors do; what the asker
// can act on, or the startup log already says (no chat model), does not.
func Explain(err error) (msg string, logIt bool) {
	var small *harness.ErrSmall
	switch {
	case errors.As(err, &small):
		return small.Error(), true
	case errors.As(err, new(*ai.ErrContext)):
		return errContext.Error(), true
	}
	for _, known := range []error{ErrNotFound, errQuestion, errNoModel, errIndexing, errBusy, errMap} {
		if errors.Is(err, known) {
			return known.Error(), false
		}
	}
	for _, logged := range []error{errSchema, errModel} {
		if errors.Is(err, logged) {
			return logged.Error(), true
		}
	}
	return "回答失敗；管理員可在伺服器記錄檔中查看詳細資訊", true
}

type Request struct {
	RepoID         int64  `json:"-"`              // the row id; the API takes the public id
	ConversationID int64  `json:"conversationId"` // 0 starts one; else one of UserID's
	Question       string `json:"question"`
	Deep           bool   `json:"deep"`    // search more rounds, read a wiki page first (deep.go), and with the model's context known gather all the evidence it holds
	Codemap        bool   `json:"codemap"` // answer with a code map (package codemap), from deep evidence
	UserID         int64  `json:"-"`       // who asks
}

// Emit sends one server-sent event.
type Emit func(event string, data any)

const (
	evidenceHits  = 10
	evidenceChars = 64000 // a deep answer's too while the context is unknown: at 40000 an eShop payment answer evicted the handler it found
	maxQuestion   = 4000  // runes
	maxAsking     = 2     // answers one user may have written at a time, in all their tabs
	noEvidence    = "無法從目前程式碼確認 — no indexed code matched this question."
)

// What gathering the evidence may take, its budget aside (limit). A deep
// answer, and a code map, may take twice the rounds and more time, and go on
// when a reply only reads files found before it: the convergence stop that
// ends a plain answer's gathering then (loop.StopConverged) was measured on
// plain answers alone, and a deep one is to reach as far as its room allows.
var (
	gathering     = loop.Limits{Rounds: 4, Time: 45 * time.Second, Converge: true}
	deepGathering = loop.Limits{Rounds: 8, Time: 120 * time.Second}
)

// limit is the evidence's budget for a's shares: evidenceChars bytes and, once
// the model's context is known, its share in tokens. A deep answer's evidence
// is held to evidenceChars only while the context is unknown: once it is
// known, it takes all the room the context leaves (asking.rest), in tokens.
// Its tokens are counted either way, as the answer's request is (asking.tokens).
func (a asking) limit(sh harness.Shares) evidence.Limit {
	l := evidence.Limit{Chars: evidenceChars, Tokens: sh.Evidence, Count: a.fit.Count}
	switch {
	case a.fit.Context() == 0:
		l.Tokens = math.MaxInt
	case a.rest:
		l.Chars = math.MaxInt
	}
	return l
}

// Ask runs one question end to end, emitting: conversation, search* (each
// call of the model's while it gathers the evidence, package loop), evidence,
// delta*, cited (the references resolved to a path that differs from the one
// written, evidence.LinkCitations), then done (the stored, citation-checked
// message) or error. A code
// map (drawMap) emits drawing* in place of delta*. The answer is written once,
// from what was gathered; nothing the model wrote while gathering reaches it.
func (s *Service) Ask(ctx context.Context, req Request, emit Emit) error {
	q := strings.TrimSpace(req.Question)
	if q == "" || utf8.RuneCountInString(q) > maxQuestion {
		return errQuestion
	}
	if !s.begin(req.UserID) {
		return errBusy
	}
	defer s.end(req.UserID)
	if s.Chat == nil {
		return errNoModel
	}
	r, err := s.Catalog.Get(ctx, req.RepoID)
	if err != nil {
		return err
	}
	if r.Snapshot == nil {
		return errIndexing
	}
	conv, err := s.open(ctx, req, catalog.Ref{ID: r.Snapshot.ID, CommitSHA: r.Snapshot.CommitSHA}, q)
	if err != nil {
		return err
	}
	emit("conversation", map[string]any{"id": conv.id, "snapshotId": conv.snap.ID, "commitSha": conv.snap.CommitSHA})

	turns, err := s.history(ctx, conv.id)
	if err != nil {
		return err
	}
	// Stored before the answer, so a question whose answer failed stays in the conversation.
	if _, err := s.Store.AddMessage(ctx, conv.id, "user", q, nil); err != nil {
		return err
	}
	past := harness.Messages(turns)
	repo, sha, asked, lang := r.Owner+"/"+r.Name, conv.snap.CommitSHA, retrievalQuery(past, q), answerLanguage(q, past)
	first, err := s.search(ctx, conv.snap.ID, repo, asked)
	if err != nil {
		return err
	}
	limits, bg := gathering, background.Page{}
	if req.Deep || req.Codemap {
		limits = deepGathering
		if bg, err = s.wikiPage(ctx, req.RepoID, conv.snap.ID, asked); err != nil {
			return err
		}
	}
	m := codemap.Request{Repo: repo, Commit: sha, Question: q, Language: lang}
	fit := harness.New(s.Chat, s.Tokens)
	a := asking{conv: conv.id, fit: fit, fixed: fit.Prompt(prompt(repo, sha, nil, "", "", q, lang)), turns: turns, page: bg, rest: req.Deep && !req.Codemap, emit: emit}
	if req.Codemap {
		a.fixed = codemap.Fixed(m, fit.Prompt)
	}
	// The gathering's budget: the evidence's share of the room, the conversation
	// fitted only to what the gathered evidence leaves of it.
	sh, err := a.fit.Share(a.fixed, a.want(evidenceChars), a.rest)
	if err != nil {
		return err
	}
	limits.Limit = a.limit(sh)
	got := loop.Gatherer{Model: s.Chat, Retriever: s.Retriever, Catalog: s.Catalog, Fit: a.fit}.Gather(ctx,
		loop.Request{Snapshot: conv.snap, Repo: repo, Question: asked, First: first, Limits: limits}, loop.Emit(emit))
	_, _, gathered := evidence.Format(got.Hits, limits.Limit) // within the budget, whole
	link := func(c evidence.Citation) string {
		return gitserver.SourceURL(r.Provider, r.URL, sha, c.Path, c.StartLine, c.EndLine)
	}
	// Fitted, and once more after a refusal as too long that said by how much
	// (harness.Learn).
	for retried := false; ; retried = true {
		parts, err := s.fit(ctx, a, gathered)
		if err != nil {
			return err
		}
		l := a.limit(parts.shares)
		sources, used, tokens := evidence.Format(got.Hits, l)
		emit("evidence", evidenceEvent(got, used, len(sources), l, parts, a.fit))
		if req.Codemap {
			m.History, m.Background, m.Sources, m.Hits = parts.history, parts.background, sources, used
			err = s.drawMap(ctx, conv.id, m, link, emit)
		} else {
			err = s.answer(ctx, conv.id, prompt(repo, sha, parts.history, parts.background, sources, q, lang), used, lang, link, emit)
		}
		if err == nil || retried || !a.fit.Learn(err, a.tokens(parts, tokens)) {
			return err
		}
	}
}

// answer streams the model's answer to p, written from used, and stores it
// with its citations linked; with no evidence it says so without the model.
func (s *Service) answer(ctx context.Context, conv int64, p ai.Prompt, used []retrieval.Hit, lang string, link func(evidence.Citation) string, emit Emit) error {
	answer := noEvidence
	if len(used) == 0 {
		emit("delta", noEvidence)
	} else if reply, err := s.Chat.Stream(ctx, p, func(d string) { emit("delta", d) }); err != nil {
		return fmt.Errorf("%w: %w", errModel, err)
	} else {
		answer = ai.StripThinking(reply)
	}
	content, cites, resolved := evidence.LinkCitations(answer, used, link)
	emit("cited", map[string]any{"resolved": resolved})
	if len(cites) == 0 && answer != noEvidence {
		content += "\n\n" + uncited(lang)
	}
	msg, err := s.Store.AddMessage(ctx, conv, "assistant", content, cites)
	if err != nil {
		return err
	}
	emit("done", msg)
	return nil
}

// drawMap answers r's question with a code map, telling how much of the
// model's replies has come as drawing events, about one every 500 bytes. The
// map is stored beside its text, which links each location's line as an
// answer's citations are linked; a map without traces (the model found nothing
// to map) is answered as having no evidence.
func (s *Service) drawMap(ctx context.Context, conv int64, r codemap.Request, link func(evidence.Citation) string, emit Emit) error {
	content, cites, drawn := noEvidence, []evidence.Citation{}, 0
	var stored json.RawMessage
	if len(r.Hits) > 0 {
		m, err := codemap.Draw(ctx, s.Chat, r, func(n int) {
			if n-drawn >= 500 {
				drawn = n
				emit("drawing", map[string]any{"chars": n})
			}
		})
		switch {
		case errors.Is(err, codemap.ErrInvalid):
			return errMap
		case errors.Is(err, ai.ErrSchema):
			return fmt.Errorf("%w: %w", errSchema, err)
		case err != nil:
			return fmt.Errorf("%w: %w", errModel, err)
		}
		if len(m.Traces) > 0 {
			content = m.Markdown(func(l codemap.Location) string {
				c := evidence.Citation{Path: l.Path, StartLine: l.Line, EndLine: l.Line}
				if !slices.Contains(cites, c) {
					cites = append(cites, c)
				}
				return evidence.Link(c, link(c))
			})
			if stored, err = json.Marshal(m); err != nil {
				return err
			}
		}
	}
	msg, err := s.Store.AddMap(ctx, conv, content, cites, stored)
	if err != nil {
		return err
	}
	emit("done", msg)
	return nil
}

// begin counts an answer being written for user, or reports false when they
// have maxAsking already: each answer costs model calls on a server everyone
// shares. ponytail: in memory, per process; count in SQLite if several
// processes ever serve one database.
func (s *Service) begin(user int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asking[user] >= maxAsking {
		return false
	}
	if s.asking == nil {
		s.asking = map[int64]int{}
	}
	s.asking[user]++
	return true
}

// end gives back the place begin counted.
func (s *Service) end(user int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asking[user]--; s.asking[user] <= 0 {
		delete(s.asking, user)
	}
}

// search retrieves the evidence for query. When none of its words match the
// code (symbol names are in the text index too), as with a question in Chinese,
// the model is asked for English search terms, and the question is searched
// again with them in front, since the text search keeps only a query's first
// words.
func (s *Service) search(ctx context.Context, snapshotID int64, repo, query string) (retrieval.Result, error) {
	res, err := s.Retriever.Search(ctx, snapshotID, query, evidenceHits)
	if err != nil || res.FTS > 0 {
		return res, err
	}
	reply, err := s.Chat.Generate(ctx, termsRequest(repo, query))
	if err != nil {
		return res, fmt.Errorf("%w: %w", errModel, err)
	}
	terms := strings.Fields(ai.StripThinking(reply)) // bounded: a rambling reply is not a search
	return s.Retriever.Search(ctx, snapshotID, strings.Join(terms[:min(len(terms), 24)], " ")+"\n"+query, evidenceHits)
}

// wikiPage is the written wiki page that shares the most words with the
// question (background.Nearest), or the zero page when none does. The pages
// are the conversation's snapshot's, else the newest of the repository's
// indexed snapshots that has written ones.
func (s *Service) wikiPage(ctx context.Context, repoID, snapshotID int64, question string) (background.Page, error) {
	titles, contents, err := s.Store.WikiPages(ctx, repoID, snapshotID)
	if err != nil {
		return background.Page{}, err
	}
	i := background.Nearest(question, titles, contents)
	if i < 0 {
		return background.Page{}, nil
	}
	return background.Of(titles[i], contents[i]), nil
}

// evidenceEvent is the evidence event's payload. used is what Format took of
// got.Hits, their head: each source block, with where it came from
// (loop.Origin); got.Hits that Format left out ("dropped"), in the order the
// answer would have read them, which the gathering's budget keeps empty; the
// hits that budget evicted and refused (loop.Out), with the round and tool of
// the call that did it; and the sources' bytes as rendered, against that
// budget in bytes, l's, none for a deep answer with the context known
// ("chars"). Then the gathering's counts, rounds (the first search's
// counting), calls and stop; the model's context and threshold in tokens (0:
// unknown) with the parts' shares in tokens, the evidence's its budget, or
// while the context is unknown the tokens each part wants ("window"), and the
// tool results the gathering masked to fit it.
func evidenceEvent(got loop.Result, used []retrieval.Hit, rendered int, l evidence.Limit, parts fitted, fit *harness.Fit) map[string]any {
	chars := map[string]int{"rendered": rendered}
	if l.Chars < math.MaxInt {
		chars["budget"] = l.Chars
	}
	source := func(h retrieval.Hit, round int, tool string) map[string]any {
		return map[string]any{"path": h.Path, "symbol": h.Symbol, "startLine": h.StartLine, "endLine": h.EndLine, "round": round, "tool": tool}
	}
	sources, dropped := make([]map[string]any, len(used)), []map[string]any{}
	for i, h := range used {
		sources[i] = source(h, got.Origins[i].Round, got.Origins[i].Tool)
	}
	for i, h := range got.Hits[len(used):] {
		o := got.Origins[len(used)+i]
		d := source(h, o.Round, o.Tool)
		d["chars"] = o.Chars
		dropped = append(dropped, d)
	}
	outs := func(list []loop.Out) []map[string]any {
		out := make([]map[string]any, len(list))
		for i, o := range list {
			if out[i] = source(o.Hit, o.Round, o.Tool); o.From != "" {
				out[i]["from"] = o.From
			}
		}
		return out
	}
	return map[string]any{"sources": sources, "dropped": dropped, "evicted": outs(got.Evicted), "refused": outs(got.Refused),
		"chars": chars,
		"fts":   got.FTS, "vector": got.Vector, "symbol": got.Symbol, "named": got.Named, "graph": got.Graph, "rounds": got.Rounds + 1, "calls": got.Calls, "stop": got.Stop,
		"window": map[string]int{"context": fit.Context(), "threshold": fit.Threshold(),
			"evidence": parts.shares.Evidence, "history": parts.shares.History, "background": parts.shares.Background},
		"masked": fit.Masked}
}
