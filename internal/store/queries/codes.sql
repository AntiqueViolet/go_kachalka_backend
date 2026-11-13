-- name: CreateCode :one
INSERT INTO verification_codes (id, purpose, target, user_id, code_hash, expires_at, created_at)
VALUES (@id, @purpose, @target, @user_id, @code_hash, @expires_at, @now::timestamptz)
RETURNING *;

-- name: GetCode :one
SELECT * FROM verification_codes WHERE id = $1;

-- name: LatestActiveUserCode :one
SELECT * FROM verification_codes
WHERE purpose = @purpose AND user_id = @user_id::uuid AND consumed_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: InvalidateTargetCodes :exec
UPDATE verification_codes SET consumed_at = @now::timestamptz
WHERE purpose = @purpose AND target = @target AND user_id IS NULL AND consumed_at IS NULL;

-- name: InvalidateUserCodes :exec
UPDATE verification_codes SET consumed_at = @now::timestamptz
WHERE purpose = @purpose AND user_id = @user_id::uuid AND consumed_at IS NULL;

-- name: CodeSendStats :one
-- How many codes were sent to the address within the window, plus the oldest and latest send time.
SELECT count(*)::bigint AS sent,
       coalesce(min(created_at), @since)::timestamptz AS oldest,
       coalesce(max(created_at), @since)::timestamptz AS latest
FROM verification_codes
WHERE target = @target AND created_at > @since;

-- name: ClaimCodeAttempt :one
-- Burns one attempt before the code is compared. No row means the code is consumed, expired or
-- out of attempts; the check runs in the same statement, so parallel guesses can't exceed the limit.
UPDATE verification_codes SET attempts = attempts + 1
WHERE id = @id AND consumed_at IS NULL AND expires_at > @now::timestamptz AND attempts < @max_attempts::int
RETURNING attempts;

-- name: ConsumeCode :execrows
UPDATE verification_codes SET consumed_at = @now::timestamptz WHERE id = @id AND consumed_at IS NULL;

-- name: DeleteOldCodes :execrows
DELETE FROM verification_codes WHERE created_at < @before;

-- name: LockCodeTarget :exec
-- Serialises code issuing per address until the transaction ends, so parallel requests see each
-- other's codes when they check the send limits.
SELECT pg_advisory_xact_lock(hashtextextended(@target::text, 0));

-- name: DeleteCode :exec
-- Removes a code that could not be delivered, so it does not count against the send limits.
DELETE FROM verification_codes WHERE id = $1;
