CREATE TABLE IF NOT EXISTS account (
    id uuid PRIMARY KEY,
    display_name text NOT NULL,
    primary_email text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS identity (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES account (id) ON DELETE CASCADE,
    provider text NOT NULL,
    subject text NOT NULL,
    email text,
    email_verified boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);

CREATE TABLE IF NOT EXISTS local_credential (
    identity_id uuid PRIMARY KEY REFERENCES identity (id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS session (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES account (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    user_agent text,
    ip inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS session_account_id_idx ON session (account_id);
