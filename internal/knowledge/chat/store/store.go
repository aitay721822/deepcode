// Package store keeps the conversations in SQLite: each one's repository,
// snapshot and owner, and its messages with their citations.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"deepcode/internal/knowledge/evidence"
	"deepcode/internal/platform/ai"
)

// Store runs the conversations' SQL.
type Store struct{ DB *sql.DB }

// ErrNotFound: the user has no such conversation.
var ErrNotFound = errors.New("找不到這個對話")

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

// Adopt gives user the conversations that have no owner.
func (s *Store) Adopt(ctx context.Context, user int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET user_id = ? WHERE user_id IS NULL`, user)
	return err
}

// Start adds a conversation of userID about repository repoID, bound to
// snapshot snap, and returns its id.
func (s *Store) Start(ctx context.Context, repoID, snap int64, title string, userID int64) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO conversations(repository_id, snapshot_id, title, user_id) VALUES (?, ?, ?, ?) RETURNING id`,
		repoID, snap, title, userID).Scan(&id)
	return id, err
}

// Bound returns the snapshot conversation id is bound to, or ErrNotFound
// unless it is one of userID's about repository repoID.
func (s *Store) Bound(ctx context.Context, id, repoID, userID int64) (int64, error) {
	var bound int64
	err := s.DB.QueryRowContext(ctx, `SELECT snapshot_id FROM conversations WHERE id = ? AND repository_id = ? AND user_id = ?`,
		id, repoID, userID).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return bound, err
}

// Rebind binds conversation id to snapshot snap.
func (s *Store) Rebind(ctx context.Context, id, snap int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET snapshot_id = ? WHERE id = ?`, snap, id)
	return err
}

// History returns the conversation's last messages, oldest first, as model
// messages: at least turns of them, the oldest dropped turns at a time.
func (s *Store) History(ctx context.Context, conv int64, turns int) ([]ai.Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT role, content FROM messages WHERE conversation_id = ?1 ORDER BY id
		LIMIT -1 OFFSET max(0, (SELECT count(*) FROM messages WHERE conversation_id = ?1) - ?2) / ?2 * ?2`, conv, turns)
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
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMessage stores one message and returns it as stored.
func (s *Store) AddMessage(ctx context.Context, conv int64, role, content string, cites []evidence.Citation) (Message, error) {
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

// Conversation returns conversation id of user userID without its messages,
// at the snapshot it is bound to, or ErrNotFound.
func (s *Store) Conversation(ctx context.Context, id, userID int64) (Conversation, error) {
	var c Conversation
	err := s.DB.QueryRowContext(ctx, `SELECT c.id, c.repository_id, r.public_id, c.snapshot_id, c.title
		FROM conversations c JOIN repositories r ON r.id = c.repository_id WHERE c.id = ? AND c.user_id = ?`, id, userID).
		Scan(&c.ID, &c.RepoID, &c.RepoPublicID, &c.SnapshotID, &c.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Messages returns all of the conversation's messages, oldest first.
func (s *Store) Messages(ctx context.Context, conv int64) ([]Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, role, content, citations, created_at FROM messages WHERE conversation_id = ? ORDER BY id`, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var cites string
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &cites, &m.CreatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(cites), &m.Citations) != nil || m.Citations == nil {
			m.Citations = []evidence.Citation{} // a damaged row reads as none, not null
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
