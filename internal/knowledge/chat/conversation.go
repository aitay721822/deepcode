package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/platform/ai"
	"deepcode/internal/repo/catalog"
)

const historyTurns = 6

type Message struct {
	ID        int64               `json:"id"`
	Role      string              `json:"role"`
	Content   string              `json:"content"`
	Citations []evidence.Citation `json:"citations"`
	CreatedAt string              `json:"createdAt"`
}

type Conversation struct {
	ID           int64     `json:"id"`
	RepoID       int64     `json:"-"`      // the row id
	RepoPublicID string    `json:"repoId"` // the repository's public id
	SnapshotID   int64     `json:"snapshotId"`
	CommitSHA    string    `json:"commitSha"`
	Title        string    `json:"title"`
	Messages     []Message `json:"messages"`
}

// Adopt gives user the conversations started before there were accounts.
// It runs on every start, so it also catches up after a first start that
// stopped before it.
func (s *Service) Adopt(ctx context.Context, user int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET user_id = ? WHERE user_id IS NULL`, user)
	return err
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
		err := s.DB.QueryRowContext(ctx, `INSERT INTO conversations(repository_id, snapshot_id, title, user_id) VALUES (?, ?, ?, ?) RETURNING id`,
			req.RepoID, published.ID, title(question), req.UserID).Scan(&b.id)
		return b, err
	}
	var bound int64
	err := s.DB.QueryRowContext(ctx, `SELECT snapshot_id FROM conversations WHERE id = ? AND repository_id = ? AND user_id = ?`,
		b.id, req.RepoID, req.UserID).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	} else if err != nil {
		return b, err
	}
	if b.snap, err = s.snapshot(ctx, req.RepoID, bound); err != nil || b.snap.ID == bound {
		return b, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE conversations SET snapshot_id = ? WHERE id = ?`, b.snap.ID, b.id)
	return b, err
}

func title(q string) string {
	if utf8.RuneCountInString(q) > 120 {
		return string([]rune(q)[:120]) + "…"
	}
	return q
}

// history returns the conversation's last messages, oldest first, each cut to
// 3000 bytes, as model messages.
func (s *Service) history(ctx context.Context, conv int64) ([]ai.Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT role, content FROM (
		SELECT id, role, content FROM messages WHERE conversation_id = ? ORDER BY id DESC LIMIT ?) ORDER BY id`, conv, historyTurns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ai.Message
	for rows.Next() {
		var m ai.Message
		if err := rows.Scan(&m.Role, &m.Content); err != nil {
			return nil, err
		}
		if len(m.Content) > 3000 {
			m.Content = strings.ToValidUTF8(m.Content[:3000], "") + "…"
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// addMessage stores one message and returns it as stored.
func (s *Service) addMessage(ctx context.Context, conv int64, role, content string, cites []evidence.Citation) (Message, error) {
	if cites == nil {
		cites = []evidence.Citation{}
	}
	citesJSON, err := json.Marshal(cites)
	if err != nil {
		return Message{}, err
	}
	m := Message{Role: role, Content: content, Citations: cites}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO messages(conversation_id, role, content, citations) VALUES (?, ?, ?, ?) RETURNING id, created_at`,
		conv, role, content, string(citesJSON)).Scan(&m.ID, &m.CreatedAt)
	return m, err
}

// Get loads conversation id of user userID with all its messages, at the
// snapshot its next question would be answered at.
func (s *Service) Get(ctx context.Context, id, userID int64) (Conversation, error) {
	var c Conversation
	err := s.DB.QueryRowContext(ctx, `SELECT c.id, c.repository_id, r.public_id, c.snapshot_id, c.title
		FROM conversations c JOIN repositories r ON r.id = c.repository_id WHERE c.id = ? AND c.user_id = ?`, id, userID).
		Scan(&c.ID, &c.RepoID, &c.RepoPublicID, &c.SnapshotID, &c.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	} else if err != nil {
		return c, err
	}
	ref, err := s.snapshot(ctx, c.RepoID, c.SnapshotID)
	if err != nil {
		return c, err
	}
	c.SnapshotID, c.CommitSHA = ref.ID, ref.CommitSHA
	rows, err := s.DB.QueryContext(ctx, `SELECT id, role, content, citations, created_at FROM messages WHERE conversation_id = ? ORDER BY id`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	c.Messages = []Message{}
	for rows.Next() {
		var m Message
		var cites string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &cites, &m.CreatedAt); err != nil {
			return c, err
		}
		if json.Unmarshal([]byte(cites), &m.Citations) != nil || m.Citations == nil {
			m.Citations = []evidence.Citation{} // a damaged row reads as none, not null
		}
		c.Messages = append(c.Messages, m)
	}
	return c, rows.Err()
}
