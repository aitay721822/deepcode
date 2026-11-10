package chat

import (
	"context"
	"errors"
	"log"
	"math"
	"strings"
	"unicode/utf8"

	"deepcode/internal/knowledge/chat/background"
	"deepcode/internal/knowledge/chat/harness"
	"deepcode/internal/knowledge/chat/store"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/catalog"
)

const historyTurns = 6

type (
	Message      = store.Message      // one message of a conversation, with its checked citations
	Conversation = store.Conversation // a user's conversation about a repository, with its messages
)

// Adopt gives user the conversations started before there were accounts.
// It runs on every start, so it also catches up after a first start that
// stopped before it.
func (s *Service) Adopt(ctx context.Context, user int64) error {
	return s.Store.Adopt(ctx, user)
}

// binding is the conversation a question belongs to and the snapshot it is
// answered at.
type binding struct {
	id   int64
	snap catalog.Ref
}

// snapshot is where a conversation bound to snapshot id is shown and
// answered: that snapshot while it is indexed, else (pruned) the published one.
func (s *Service) snapshot(ctx context.Context, repoID, id int64) (catalog.Ref, error) {
	ref, err := s.Catalog.Resolve(ctx, repoID, id)
	if errors.Is(err, catalog.ErrNotFound) {
		return s.Catalog.Resolve(ctx, repoID, 0)
	}
	return ref, err
}

// open starts a conversation (ConversationID 0) for the asking user at the
// published snapshot, or continues one of theirs at its snapshot, which it
// moves to when that changed.
func (s *Service) open(ctx context.Context, req Request, published catalog.Ref, question string) (binding, error) {
	b := binding{id: req.ConversationID, snap: published}
	if b.id == 0 {
		var err error
		b.id, err = s.Store.Start(ctx, req.RepoID, published.ID, title(question), req.UserID)
		return b, err
	}
	bound, err := s.Store.Bound(ctx, b.id, req.RepoID, req.UserID)
	if err != nil {
		return b, err
	}
	if b.snap, err = s.snapshot(ctx, req.RepoID, bound); err != nil || b.snap.ID == bound {
		return b, err
	}
	return b, s.Store.Rebind(ctx, b.id, b.snap.ID)
}

func title(q string) string {
	if utf8.RuneCountInString(q) > 120 {
		return string([]rune(q)[:120]) + "…"
	}
	return q
}

// history returns the conversation's last messages, oldest first, each cut to
// 3000 bytes: at least historyTurns of them, the oldest dropped historyTurns
// at a time, so what the model is sent before the new question stays the same
// for some turns and its server's prompt cache holds it.
func (s *Service) history(ctx context.Context, conv int64) ([]harness.Turn, error) {
	rows, err := s.Store.History(ctx, conv, historyTurns)
	out := make([]harness.Turn, len(rows))
	for i, t := range rows {
		if out[i] = harness.Turn(t); len(t.Content) > 3000 {
			out[i].Content = strings.ToValidUTF8(t.Content[:3000], "") + "…"
		}
	}
	return out, err
}

// asking is what an answer's request is fitted from (fit): the question's
// Fit, the tokens of the request's own lines, the conversation so far,
// the background page, and whether the evidence takes all the room the others
// leave (a deep answer's) rather than a fixed share.
type asking struct {
	conv  int64
	fit   *harness.Fit
	fixed int
	turns []harness.Turn
	page  background.Page
	rest  bool
	emit  Emit
}

// fitted are an answer's request's parts as fitted to the model's context:
// the conversation so far; what heads the new message, the conversation's
// summary when no turn of it is kept (harness.Recalled.Lead), then the
// background block; and the shares they come from, the evidence's its budget.
type fitted struct {
	history    []ai.Message
	background string
	shares     harness.Shares
}

// want is what a's parts want: evidence tokens, and the conversation and the
// background page whole.
func (a asking) want(evidence int) harness.Shares {
	return harness.Shares{Evidence: evidence, History: a.fit.History(a.turns), Background: a.fit.Count(a.page.Block(math.MaxInt, a.fit.Count))}
}

// tokens is an answer's request's tokens as its parts were fitted: its own
// lines (a.fixed), the conversation's messages, the background, and the
// evidence's evidence tokens, each piece counted alone, as Share and Format
// checked them; never the request whole, which may be megabytes.
func (a asking) tokens(parts fitted, evidence int) int {
	return a.fixed + a.fit.Prompt(ai.Prompt{Previous: parts.history}) + a.fit.Count(parts.background) + evidence
}

// fit shares the room the model's context leaves among a's parts
// (harness.Share), the evidence wanting evidence tokens, those it was
// gathered at, or with a.rest all the room left, when the context is known;
// and fits the conversation to its share (harness.Recall). A summary written
// is stored and told as a compact event; one that failed is logged, the turns
// that fit kept.
func (s *Service) fit(ctx context.Context, a asking, evidence int) (fitted, error) {
	sh, err := a.fit.Share(a.fixed, a.want(evidence), a.rest)
	if err != nil {
		return fitted{}, err
	}
	r, err := a.fit.Recall(ctx, a.turns, sh.History, func(ctx context.Context) (harness.Summary, error) {
		through, text, err := s.Store.Summary(ctx, a.conv)
		return harness.Summary{Through: through, Text: text}, err
	})
	switch {
	case err != nil:
		if ctx.Err() == nil {
			log.Printf("chat: the conversation's earlier turns are left out, not summed up: %v", err)
		}
	case r.Fresh != nil:
		if err := s.Store.SetSummary(ctx, a.conv, r.Fresh.Through, r.Fresh.Text); err != nil {
			return fitted{}, err
		}
		a.emit("compact", map[string]any{"summarized": r.Summed})
	}
	return fitted{history: r.Kept, background: r.Lead + a.page.Block(sh.Background, a.fit.Count), shares: sh}, nil
}

// Get loads conversation id of user userID with all its messages, at the
// snapshot its next question would be answered at.
func (s *Service) Get(ctx context.Context, id, userID int64) (Conversation, error) {
	c, err := s.Store.Conversation(ctx, id, userID)
	if err != nil {
		return c, err
	}
	ref, err := s.snapshot(ctx, c.RepoID, c.SnapshotID)
	if err != nil {
		return c, err
	}
	c.SnapshotID, c.CommitSHA = ref.ID, ref.CommitSHA
	c.Messages, err = s.Store.Messages(ctx, id)
	return c, err
}
