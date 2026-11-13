-- name: CreateSession :one
INSERT INTO sessions (id, user_id, device_name, generation, created_at, last_used_at, expires_at)
VALUES (@id, @user_id, @device_name, 1, @now::timestamptz, @now::timestamptz, @expires_at)
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE id = $1;

-- name: GetSessionForUpdate :one
SELECT * FROM sessions WHERE id = $1 FOR UPDATE;

-- name: RotateSession :one
UPDATE sessions
SET generation = generation + 1, rotated_at = @now::timestamptz, last_used_at = @now::timestamptz, expires_at = @expires_at
WHERE id = @id
RETURNING *;

-- name: TouchSession :exec
-- GREATEST: a slow request must not move last_used_at backwards (the session limit orders by it).
UPDATE sessions SET last_used_at = GREATEST(last_used_at, @now::timestamptz) WHERE id = @id;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = @now::timestamptz, revoke_reason = @reason::text
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = @now::timestamptz, revoke_reason = @reason::text
WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: RevokeUserSessionsExcept :exec
UPDATE sessions SET revoked_at = @now::timestamptz, revoke_reason = @reason::text
WHERE user_id = @user_id AND id <> @keep_id AND revoked_at IS NULL;

-- name: RevokeExcessSessions :exec
-- Keeps the most recently used live sessions of the user (up to keep) and revokes the rest.
UPDATE sessions SET revoked_at = @now::timestamptz, revoke_reason = 'session_limit'
WHERE id IN (
    SELECT s.id FROM sessions s
    WHERE s.user_id = @user_id AND s.revoked_at IS NULL AND s.expires_at > @now::timestamptz
    ORDER BY s.last_used_at DESC, s.id DESC
    OFFSET @keep::int
);

-- name: DeleteStaleSessions :execrows
DELETE FROM sessions
WHERE (revoked_at IS NOT NULL AND revoked_at < @before::timestamptz) OR expires_at < @before::timestamptz;
