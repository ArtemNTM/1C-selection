CREATE TABLE IF NOT EXISTS clients (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 100),
    kind TEXT NOT NULL CHECK (kind IN ('staff', 'freelance')),
    role TEXT NOT NULL CHECK (role IN ('correspondent', 'editor')),
    trust DOUBLE PRECISION NOT NULL CHECK (trust >= 0 AND trust <= 1),
    key_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS stories (
    id BIGSERIAL PRIMARY KEY,
    rubric TEXT NOT NULL CHECK (length(rubric) BETWEEN 1 AND 100),
    topic TEXT NOT NULL CHECK (length(topic) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    published_at TIMESTAMPTZ,
    trust_at_publish DOUBLE PRECISION,
    UNIQUE (rubric, topic),
    CHECK ((published_at IS NULL) = (trust_at_publish IS NULL))
);

CREATE TABLE IF NOT EXISTS notes (
    id BIGSERIAL PRIMARY KEY,
    client_id BIGINT NOT NULL REFERENCES clients(id),
    story_id BIGINT NOT NULL REFERENCES stories(id),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    payload_hash TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    event_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    is_contribution BOOLEAN NOT NULL DEFAULT FALSE,
    rank INTEGER CHECK (rank > 0),
    UNIQUE (client_id, request_id),
    CHECK (rank IS NULL OR is_contribution)
);

CREATE UNIQUE INDEX IF NOT EXISTS notes_one_voice
    ON notes (story_id, client_id) WHERE is_contribution;
CREATE UNIQUE INDEX IF NOT EXISTS notes_publication_rank
    ON notes (story_id, rank) WHERE rank IS NOT NULL;
CREATE INDEX IF NOT EXISTS notes_story_order ON notes (story_id, id);

CREATE TABLE IF NOT EXISTS notifications (
    id BIGSERIAL PRIMARY KEY,
    client_id BIGINT NOT NULL REFERENCES clients(id),
    story_id BIGINT NOT NULL REFERENCES stories(id),
    rank INTEGER NOT NULL CHECK (rank > 0),
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    acknowledged_at TIMESTAMPTZ,
    UNIQUE (story_id, client_id)
);

CREATE INDEX IF NOT EXISTS notifications_pending
    ON notifications (client_id, id) WHERE acknowledged_at IS NULL;
