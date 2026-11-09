// Package chat answers questions about a snapshot: retrieve evidence, stream
// the model's answer, validate its citations and store the conversation.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

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
	Chat      *ai.OpenAI // nil: not configured

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
)

// Explain is what the asker is told about err from Ask: one of the errors
// above, or a generic line, since other errors may quote SQL or a server's
// answer. logIt reports whether err belongs in the server log: the model's
// failures and unexplained errors do; what the asker can act on, or the
// startup log already says (no chat model), does not.
func Explain(err error) (msg string, logIt bool) {
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
	Deep           bool   `json:"deep"`    // search more rounds and read a wiki page first (deep.go)
	Codemap        bool   `json:"codemap"` // answer with a code map (package codemap), from deep evidence
	UserID         int64  `json:"-"`       // who asks
}

// Emit sends one server-sent event.
type Emit func(event string, data any)

const (
	evidenceHits  = 10
	evidenceChars = 40000
	maxQuestion   = 4000 // runes
	maxAsking     = 2    // answers one user may have written at a time, in all their tabs
	noEvidence    = "無法從目前程式碼確認 — no indexed code matched this question."
)

// Ask runs one question end to end, emitting: conversation, search* (a deep
// answer's further rounds), evidence, delta*, then done (the stored,
// citation-checked message) or error. A draft that recheck turns down, as
// may the draft written in its place, is followed by search (its one more
// round, if it searched), evidence and delta* again: the answer written anew;
// a rewrite that fails is followed by evidence once more, the standing draft's.
// A code map (drawMap) emits drawing* in place of delta*.
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

	history, err := s.history(ctx, conv.id)
	if err != nil {
		return err
	}
	// Stored before the answer, so a question whose answer failed stays in the conversation.
	if _, err := s.Store.AddMessage(ctx, conv.id, "user", q, nil); err != nil {
		return err
	}
	repo, asked := r.Owner+"/"+r.Name, retrievalQuery(history, q)
	res, err := s.search(ctx, conv.snap.ID, repo, asked)
	if err != nil {
		return err
	}
	budget, background, rounds := evidenceChars, "", 1
	if req.Deep || req.Codemap {
		if res, rounds, err = s.deepen(ctx, conv.snap.ID, repo, asked, res, emit); err != nil {
			return err
		}
		if background, err = s.background(ctx, req.RepoID, conv.snap.ID, asked); err != nil {
			return err
		}
		budget = deepEvidenceChars
	}
	// Each evidence event starts the answer: a second one, after a draft that recheck turned down, starts it anew.
	show := func(used []retrieval.Hit, res retrieval.Result, rounds int) {
		emit("evidence", map[string]any{"sources": sourceList(used), "fts": res.FTS, "vector": res.Vector, "symbol": res.Symbol,
			"named": res.Named, "graph": res.Graph, "rounds": rounds})
	}
	link := func(c evidence.Citation) string {
		return gitserver.SourceURL(r.Provider, r.URL, conv.snap.CommitSHA, c.Path, c.StartLine, c.EndLine)
	}
	if req.Codemap {
		sources, used := evidence.Format(res.Hits, budget)
		show(used, res, rounds)
		return s.drawMap(ctx, conv.id, codemap.Request{Repo: repo, Commit: conv.snap.CommitSHA, History: history, Background: background,
			Sources: sources, Question: q, Language: answerLanguage(q, history), Hits: used}, link, emit)
	}
	write := func(res retrieval.Result, rounds int, note string) (answer, sources string, used []retrieval.Hit, err error) {
		sources, used = evidence.Format(res.Hits, budget)
		show(used, res, rounds)
		if len(used) == 0 {
			emit("delta", noEvidence)
			return noEvidence, sources, used, nil
		}
		answer, err = s.Chat.Stream(ctx, prompt(repo, conv.snap.CommitSHA, history, background, sources, q, note), func(d string) { emit("delta", d) })
		if err != nil {
			return "", "", nil, fmt.Errorf("%w: %w", errModel, err)
		}
		return ai.StripThinking(answer), sources, used, nil
	}
	answer, sources, used, err := write(res, rounds, "")
	if err != nil {
		return err
	}
	// The check only improves a sound draft: when it fails, the draft stands
	// (done replaces whatever the rewrite had streamed), unless the asker left.
	for i := 0; i < maxRechecks && len(used) > 0; i++ {
		more, n, again, err := s.recheck(ctx, conv.snap.ID, repo, asked, answer, sources, res, rounds, emit)
		if err == nil && again {
			var a, src string
			var u []retrieval.Hit
			if a, src, u, err = write(more, n, rewriteNote); err == nil {
				answer, sources, used, res, rounds = a, src, u, more, n
				continue
			}
			show(used, res, rounds) // the rewrite's sources were listed: list the standing draft's again
		}
		if err != nil && ctx.Err() != nil {
			return err
		} else if err != nil {
			log.Printf("chat: the draft stands, as checking it failed: %v", err)
		}
		break
	}
	content, cites := evidence.LinkCitations(answer, used, link)
	if len(cites) == 0 && answer != noEvidence {
		content += "\n\n" + uncited(answerLanguage(q, history))
	}
	msg, err := s.Store.AddMessage(ctx, conv.id, "assistant", content, cites)
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

// sourceList is the evidence event payload: where each source block came from.
func sourceList(used []retrieval.Hit) []map[string]any {
	out := make([]map[string]any, len(used))
	for i, h := range used {
		out[i] = map[string]any{"path": h.Path, "symbol": h.Symbol, "startLine": h.StartLine, "endLine": h.EndLine}
	}
	return out
}
