-- llm_usage: the tokens the model servers counted, summed per repository, UTC
-- day and model. repository_id is 0 for a request no repository asked for, and
-- has no foreign key: a deleted repository's spend stays counted.
-- input_tokens include cached_tokens, the part the server read from its cache.
CREATE TABLE llm_usage (
    repository_id INTEGER NOT NULL,
    day TEXT NOT NULL,
    model TEXT NOT NULL,
    calls INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repository_id, day, model)
);
