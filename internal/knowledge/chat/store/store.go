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
	"deepcode/internal/platform/storage"
)

// Store runs the conversations' SQL.
type Store struct{ DB *sql.DB }

// ErrNotFound: the user has no such conversation.
var ErrNotFound = errors.New("找不到這個對話")

type Message struct {
	ID        int64               `json:"-"`  // the row id
	PublicID  string              `json:"id"` // the CUID2 the API shows
	Role      string              `json:"role"`
	Content   string              `json:"content"`
	Citations []evidence.Citation `json:"citations"`
	Codemap   json.RawMessage     `json:"codemap,omitempty"` // an answer drawn as a code map: the map
	CreatedAt string              `json:"createdAt"`
}

type Conversation struct {
	ID               int64     `json:"-"`      // the row id
	PublicID         string    `json:"id"`     // the CUID2 the API, the URLs and the UI show
	RepoID           int64     `json:"-"`      // the row id
	RepoPublicID     string    `json:"repoId"` // the repository's public id
	SnapshotID       int64     `json:"-"`
	SnapshotPublicID string    `json:"snapshotId"`
	CommitSHA        string    `json:"commitSha"`
	Title            string    `json:"title"`
	Messages         []Message `json:"messages"`
}

// Adopt gives user the conversations that have no owner.
func (s *Store) Adopt(ctx context.Context, user int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET user_id = ? WHERE user_id IS NULL`, user)
	return err
}

// Start adds a conversation of userID about repository repoID, bound to
// snapshot snap, and returns its row id and public id.
func (s *Store) Start(ctx context.Context, repoID, snap int64, title string, userID int64) (id int64, publicID string, err error) {
	err = s.DB.QueryRowContext(ctx, `INSERT INTO conversations(public_id, repository_id, snapshot_id, title, user_id) VALUES (cuid(), ?, ?, ?, ?)
		RETURNING id, public_id`, repoID, snap, title, userID).Scan(&id, &publicID)
	return id, publicID, err
}

// Bound returns the row id of the conversation whose public id is publicID
// and the snapshot it is bound to, or ErrNotFound unless it is one of
// userID's about repository repoID.
func (s *Store) Bound(ctx context.Context, publicID string, repoID, userID int64) (id, bound int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT id, snapshot_id FROM conversations WHERE public_id = ? AND repository_id = ? AND user_id = ?`,
		publicID, repoID, userID).Scan(&id, &bound)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return id, bound, err
}

// ID is the row id of the conversation whose public id is publicID, or ErrNotFound.
func (s *Store) ID(ctx context.Context, publicID string) (int64, error) {
	id, err := storage.RowID(ctx, s.DB, "conversations", publicID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// Rebind binds conversation id to snapshot snap.
func (s *Store) Rebind(ctx context.Context, id, snap int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE conversations SET snapshot_id = ? WHERE id = ?`, snap, id)
	return err
}

// Turn is a message of a conversation as the model is sent it, with its id.
type Turn struct {
	ID int64
	ai.Message
}

// History returns the conversation's last messages, oldest first, as model
// messages with their ids: at least turns of them, the oldest dropped turns at
// a time.
func (s *Store) History(ctx context.Context, conv int64, turns int) ([]Turn, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, role, content FROM messages WHERE conversation_id = ?1 ORDER BY id
		LIMIT -1 OFFSET max(0, (SELECT count(*) FROM messages WHERE conversation_id = ?1) - ?2) / ?2 * ?2`, conv, turns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.ID, &t.Role, &t.Content); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddMessage stores one message and returns it as stored.
func (s *Store) AddMessage(ctx context.Context, conv int64, role, content string, cites []evidence.Citation) (Message, error) {
	return s.add(ctx, Message{Role: role, Content: content, Citations: cites}, conv)
}

// AddMap stores an answer drawn as code map codemap, its text beside it.
func (s *Store) AddMap(ctx context.Context, conv int64, content string, cites []evidence.Citation, codemap json.RawMessage) (Message, error) {
	return s.add(ctx, Message{Role: "assistant", Content: content, Citations: cites, Codemap: codemap}, conv)
}

func (s *Store) add(ctx context.Context, m Message, conv int64) (Message, error) {
	if m.Citations == nil {
		m.Citations = []evidence.Citation{}
	}
	citesJSON, err := json.Marshal(m.Citations)
	if err != nil {
		return Message{}, err
	}
	var codemap *string // NULL for a message that is no map
	if m.Codemap != nil {
		codemap = new(string(m.Codemap))
	}
	err = s.DB.QueryRowContext(ctx, `INSERT INTO messages(public_id, conversation_id, role, content, citations, codemap) VALUES (cuid(), ?, ?, ?, ?, ?)
		RETURNING id, public_id, created_at`, conv, m.Role, m.Content, string(citesJSON), codemap).Scan(&m.ID, &m.PublicID, &m.CreatedAt)
	return m, err
}

// Conversation returns conversation id of user userID without its messages,
// at the snapshot it is bound to, or ErrNotFound.
func (s *Store) Conversation(ctx context.Context, id, userID int64) (Conversation, error) {
	var c Conversation
	err := s.DB.QueryRowContext(ctx, `SELECT c.id, c.public_id, c.repository_id, r.public_id, c.snapshot_id, c.title
		FROM conversations c JOIN repositories r ON r.id = c.repository_id WHERE c.id = ? AND c.user_id = ?`, id, userID).
		Scan(&c.ID, &c.PublicID, &c.RepoID, &c.RepoPublicID, &c.SnapshotID, &c.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// Messages returns all of the conversation's messages, oldest first.
func (s *Store) Messages(ctx context.Context, conv int64) ([]Message, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, public_id, role, content, citations, codemap, created_at FROM messages WHERE conversation_id = ? ORDER BY id`, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var cites string
		var codemap sql.NullString
		if err := rows.Scan(&m.ID, &m.PublicID, &m.Role, &m.Content, &cites, &codemap, &m.CreatedAt); err != nil {
			return nil, err
		}
		if codemap.Valid && json.Valid([]byte(codemap.String)) {
			m.Codemap = json.RawMessage(codemap.String)
		}
		if json.Unmarshal([]byte(cites), &m.Citations) != nil || m.Citations == nil {
			m.Citations = []evidence.Citation{} // a damaged row reads as none, not null
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
