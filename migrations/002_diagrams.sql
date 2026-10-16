CREATE TABLE IF NOT EXISTS diagram (
    id uuid PRIMARY KEY,
    owner_account_id uuid NOT NULL REFERENCES account (id),
    title text NOT NULL DEFAULT 'Untitled diagram',
    document jsonb NOT NULL,
    head_version int NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS diagram_owner_updated_idx ON diagram (owner_account_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS diagram_version (
    id uuid PRIMARY KEY,
    diagram_id uuid NOT NULL REFERENCES diagram (id) ON DELETE CASCADE,
    version int NOT NULL,
    document jsonb NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (diagram_id, version)
);

CREATE INDEX IF NOT EXISTS diagram_version_diagram_idx ON diagram_version (diagram_id, version DESC);

CREATE TABLE IF NOT EXISTS share_link (
    id uuid PRIMARY KEY,
    diagram_id uuid NOT NULL REFERENCES diagram (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_by text NOT NULL,
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
