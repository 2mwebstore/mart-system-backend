ALTER TABLE refresh_tokens
    DROP KEY idx_refresh_tokens_family_id,
    DROP COLUMN family_id;
ALTER TABLE users
    DROP COLUMN token_version;
