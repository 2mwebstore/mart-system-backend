-- Nightly/manual database backup runs. Telegram alert settings and the
-- backup schedule live in the existing key-value `settings` table (no new
-- table needed for those) — see internal/handlers/notify.go.
CREATE TABLE backups (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    filename      VARCHAR(255) NOT NULL,
    size_bytes    BIGINT UNSIGNED NOT NULL DEFAULT 0,
    status        VARCHAR(20)  NOT NULL, -- RUNNING | SUCCESS | FAILED
    trigger_type  VARCHAR(20)  NOT NULL, -- SCHEDULED | MANUAL
    triggered_by  BIGINT UNSIGNED NULL,  -- user id; null for the scheduled run
    error         TEXT NULL,
    started_at    DATETIME NOT NULL,
    finished_at   DATETIME NULL,
    KEY idx_backups_started_at (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
