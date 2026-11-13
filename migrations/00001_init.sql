-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                uuid PRIMARY KEY,
    phone             text NOT NULL UNIQUE,
    password_hash     text NOT NULL,
    email             citext NOT NULL,
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
-- Unique only among verified addresses: a typo can't lock someone else out of their email.
CREATE UNIQUE INDEX users_verified_email_uq ON users (email) WHERE email_verified_at IS NOT NULL;

CREATE TABLE sessions (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_name   text NOT NULL,
    generation    bigint NOT NULL DEFAULT 1,
    rotated_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoke_reason text
);
CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;

CREATE TABLE verification_codes (
    id          uuid PRIMARY KEY,
    purpose     text NOT NULL CHECK (purpose IN ('phone_register', 'email_verify', 'password_reset')),
    target      text NOT NULL,
    user_id     uuid REFERENCES users (id) ON DELETE CASCADE,
    code_hash   bytea NOT NULL,
    attempts    int NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
-- Send limits are counted per address across purposes.
CREATE INDEX verification_codes_target_idx ON verification_codes (target, created_at DESC);
CREATE INDEX verification_codes_user_idx ON verification_codes (user_id, purpose, created_at DESC) WHERE user_id IS NOT NULL;

-- +goose Down
DROP TABLE verification_codes;
DROP TABLE sessions;
DROP TABLE users;
