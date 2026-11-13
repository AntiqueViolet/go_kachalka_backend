-- name: CreateUser :one
INSERT INTO users (id, phone, password_hash, email, created_at, updated_at)
VALUES (@id, @phone, @password_hash, @email, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByPhone :one
SELECT * FROM users WHERE phone = $1;

-- name: GetUserByVerifiedEmail :one
SELECT * FROM users WHERE email = $1 AND email_verified_at IS NOT NULL;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = @password_hash, updated_at = @now::timestamptz WHERE id = @id;

-- name: SetEmail :one
UPDATE users SET email = @email, email_verified_at = NULL, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: MarkEmailVerified :one
UPDATE users SET email_verified_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: LockUser :one
-- Locks the user row until the transaction ends and returns the current password hash. Logins
-- (session creation) serialise with password changes and resets through this lock.
SELECT password_hash FROM users WHERE id = $1 FOR UPDATE;

-- name: RehashPassword :execrows
-- Upgrades a hash made with outdated parameters, unless the password changed in the meantime.
UPDATE users SET password_hash = @new_hash, updated_at = @now::timestamptz
WHERE id = @id AND password_hash = @old_hash;
