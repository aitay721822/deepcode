-- A conversation whose earlier turns no longer fit the model's context keeps
-- them summed up by the model (package chat/harness), through the message
-- through_message_id, so the next question reuses the summary or adds to it.
CREATE TABLE conversation_summaries (
  conversation_id INTEGER PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
  through_message_id INTEGER NOT NULL,
  content TEXT NOT NULL
);
