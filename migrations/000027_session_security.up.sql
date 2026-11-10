-- token_version is compared against the access token on every request, so
-- bumping it ends a user's access tokens at once (logout, password change,
-- account disabled, refresh token replayed).
ALTER TABLE users
    ADD COLUMN token_version BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER active;

-- family_id groups a login's chain of rotated refresh tokens, so a replayed
-- old token can end just that session.
ALTER TABLE refresh_tokens
    ADD COLUMN family_id CHAR(36) NULL AFTER token_hash;
UPDATE refresh_tokens SET family_id = UUID() WHERE family_id IS NULL;
ALTER TABLE refresh_tokens
    MODIFY family_id CHAR(36) NOT NULL,
    ADD KEY idx_refresh_tokens_family_id (family_id);
