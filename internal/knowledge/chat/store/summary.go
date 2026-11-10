package store

import (
	"context"
	"database/sql"
	"errors"
)

// Summary returns the summary of conversation conv's earlier turns and the
// last message it covers, or 0 and "" when it has none.
func (s *Store) Summary(ctx context.Context, conv int64) (through int64, content string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT through_message_id, content FROM conversation_summaries WHERE conversation_id = ?`, conv).Scan(&through, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return through, content, err
}

// SetSummary stores the summary of conversation conv's turns through message
// through, in place of the one before unless that one covers as much: two
// questions asked at once may store theirs in either order.
func (s *Store) SetSummary(ctx context.Context, conv, through int64, content string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO conversation_summaries(conversation_id, through_message_id, content) VALUES (?, ?, ?)
		ON CONFLICT(conversation_id) DO UPDATE SET through_message_id = excluded.through_message_id, content = excluded.content
		WHERE excluded.through_message_id > conversation_summaries.through_message_id`, conv, through, content)
	return err
}
