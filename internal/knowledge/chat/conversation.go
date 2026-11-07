package chat

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

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
// 3000 bytes, as model messages: at least historyTurns of them, the oldest
// dropped historyTurns at a time, so what the model is sent before the new
// question stays the same for some turns and its server's prompt cache holds it.
func (s *Service) history(ctx context.Context, conv int64) ([]ai.Message, error) {
	out, err := s.Store.History(ctx, conv, historyTurns)
	for i, m := range out {
		if len(m.Content) > 3000 {
			out[i].Content = strings.ToValidUTF8(m.Content[:3000], "") + "…"
		}
	}
	return out, err
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
