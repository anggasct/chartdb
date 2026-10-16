ALTER TABLE diagram ADD COLUMN IF NOT EXISTS epoch int NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS sync_update (
    diagram_id uuid NOT NULL REFERENCES diagram (id) ON DELETE CASCADE,
    seq BIGSERIAL PRIMARY KEY,
    update BYTEA NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sync_update_diagram_seq_idx ON sync_update (diagram_id, seq);

CREATE TABLE IF NOT EXISTS sync_snapshot (
    diagram_id uuid PRIMARY KEY REFERENCES diagram (id) ON DELETE CASCADE,
    update BYTEA NOT NULL,
    at_seq BIGINT NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
