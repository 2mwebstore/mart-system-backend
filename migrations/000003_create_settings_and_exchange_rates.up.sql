CREATE TABLE settings (
    `key`       VARCHAR(100) PRIMARY KEY,
    value       TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE exchange_rates (
    id                 BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    rate_riel_per_usd  BIGINT   NOT NULL,
    set_by_user_id     BIGINT UNSIGNED NOT NULL,
    effective_at       DATETIME NOT NULL,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_exchange_rates_effective_at (effective_at),
    KEY idx_exchange_rates_set_by_user_id (set_by_user_id),
    CONSTRAINT fk_exchange_rates_user FOREIGN KEY (set_by_user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
