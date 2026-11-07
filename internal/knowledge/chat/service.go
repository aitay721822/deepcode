// Package chat answers questions about a snapshot: retrieve evidence, stream
// the model's answer, validate its citations and store the conversation.
package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

type Service struct {
	DB        *sql.DB
	Catalog   *catalog.Catalog
	Retriever *retrieval.Retriever
	Chat      *ai.OpenAI // nil: not configured

	mu     sync.Mutex
	asking map[int64]int // answers being written, by user
}

// What an asker can act on. Explain shows these, and only these, to them.
var (
	ErrNotFound = errors.New("conversation not found")
	errQuestion = errors.New("a question of 1 to 4000 characters is required")
	errNoModel  = errors.New("no chat model is configured: set DEEPCODE_CHAT_MODEL")
	errIndexing = errors.New("the repository is still being indexed")
	errBusy     = fmt.Errorf("at most %d of your answers are written at a time; ask again once one has finished", maxAsking)
	errModel    = errors.New("the language model did not answer; try again later")
)

// Explain is what the asker is told about err from Ask: one of the errors
// above, or a generic line, since other errors may quote SQL or a server's
// answer. logIt reports whether err belongs in the server log: the model's
// failures and unexplained errors do; what the asker can act on, or the
// startup log already says (no chat model), does not.
func Explain(err error) (msg string, logIt bool) {
	for _, known := range []error{ErrNotFound, errQuestion, errNoModel, errIndexing, errBusy} {
		if errors.Is(err, known) {
			return known.Error(), false
		}
	}
	if errors.Is(err, errModel) {
		return errModel.Error(), true
	}
	return "the answer failed; an administrator can find the details in the server log", true
}

type Request struct {
	RepoID         int64  `json:"repoId"`
	ConversationID int64  `json:"conversationId"` // 0 starts one; else one of UserID's
	Question       string `json:"question"`
	Deep           bool   `json:"deep"` // search more rounds and read a wiki page first (deep.go)
	UserID         int64  `json:"-"`    // who asks
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
// citation-checked message) or error.
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
	if _, err := s.addMessage(ctx, conv.id, "user", q, nil); err != nil {
		return err
	}
	repo, asked := r.Owner+"/"+r.Name, retrievalQuery(history, q)
	res, err := s.search(ctx, conv.snap.ID, repo, asked)
	if err != nil {
		return err
	}
	budget, background, rounds := evidenceChars, "", 1
	if req.Deep {
		if res, rounds, err = s.deepen(ctx, conv.snap.ID, repo, asked, res, emit); err != nil {
			return err
		}
		if background, err = s.background(ctx, req.RepoID, conv.snap.ID, asked); err != nil {
			return err
		}
		budget = deepEvidenceChars
	}
	sources, used := evidence.Format(res.Hits, budget)
	emit("evidence", map[string]any{"sources": sourceList(used), "fts": res.FTS, "vector": res.Vector, "symbol": res.Symbol,
		"graph": res.Graph, "rounds": rounds})

	answer := noEvidence
	if len(used) == 0 {
		emit("delta", answer)
	} else if answer, err = s.Chat.Stream(ctx, prompt(repo, conv.snap.CommitSHA, history, background, sources, q), func(d string) { emit("delta", d) }); err != nil {
		return fmt.Errorf("%w: %w", errModel, err)
	}
	content, cites := evidence.LinkCitations(ai.StripThinking(answer), used, func(c evidence.Citation) string {
		return gitserver.SourceURL(r.Provider, r.URL, conv.snap.CommitSHA, c.Path, c.StartLine, c.EndLine)
	})
	msg, err := s.addMessage(ctx, conv.id, "assistant", content, cites)
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
