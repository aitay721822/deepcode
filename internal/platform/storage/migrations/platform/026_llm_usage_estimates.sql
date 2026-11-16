-- llm_usage counts every model request from 2.0 on, not only those the server
-- counted tokens for: unreported_calls are the calls it counted none for (a
-- subscription agent's answer, a refused request), est_input_tokens and
-- est_output_tokens the tokens of those answered, as a local tokenizer
-- estimated them, and milliseconds every call's time, summed.
ALTER TABLE llm_usage ADD COLUMN unreported_calls INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_usage ADD COLUMN est_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_usage ADD COLUMN est_output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_usage ADD COLUMN milliseconds INTEGER NOT NULL DEFAULT 0;
