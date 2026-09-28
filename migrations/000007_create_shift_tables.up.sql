CREATE TABLE shifts (
    id                    BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id             BIGINT UNSIGNED NOT NULL,
    device_id             BIGINT UNSIGNED NOT NULL,
    user_id               BIGINT UNSIGNED NOT NULL,
    status                VARCHAR(10) NOT NULL DEFAULT 'OPEN',
    opened_at             DATETIME NOT NULL,
    closed_at             DATETIME NULL,
    exchange_rate         BIGINT NOT NULL,
    opening_usd_cents     BIGINT NOT NULL DEFAULT 0,
    opening_khr_riel      BIGINT NOT NULL DEFAULT 0,
    expected_usd_cents    BIGINT NOT NULL DEFAULT 0,
    expected_khr_riel     BIGINT NOT NULL DEFAULT 0,
    counted_usd_cents     BIGINT NOT NULL DEFAULT 0,
    counted_khr_riel      BIGINT NOT NULL DEFAULT 0,
    diff_usd_cents        BIGINT NOT NULL DEFAULT 0,
    diff_khr_riel         BIGINT NOT NULL DEFAULT 0,
    note                  VARCHAR(255) NOT NULL DEFAULT '',
    KEY idx_shifts_branch_id (branch_id),
    KEY idx_shifts_device_id (device_id),
    KEY idx_shifts_user_id (user_id),
    KEY idx_shifts_status (status),
    KEY idx_shifts_opened_at (opened_at),
    CONSTRAINT fk_shifts_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_shifts_device FOREIGN KEY (device_id) REFERENCES devices (id),
    CONSTRAINT fk_shifts_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE shift_counts (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    shift_id      BIGINT UNSIGNED NOT NULL,
    type          VARCHAR(10) NOT NULL,
    currency      VARCHAR(5)  NOT NULL,
    denomination  BIGINT NOT NULL,
    quantity      BIGINT NOT NULL,
    KEY idx_shift_counts_shift_id (shift_id),
    CONSTRAINT fk_shift_counts_shift FOREIGN KEY (shift_id) REFERENCES shifts (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE cash_movements (
    id                 BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    shift_id           BIGINT UNSIGNED NOT NULL,
    type               VARCHAR(10) NOT NULL,
    amount_usd_cents   BIGINT NOT NULL DEFAULT 0,
    amount_khr_riel    BIGINT NOT NULL DEFAULT 0,
    reason             VARCHAR(255) NOT NULL DEFAULT '',
    user_id            BIGINT UNSIGNED NOT NULL,
    approved_by_id     BIGINT UNSIGNED NULL,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_cash_movements_shift_id (shift_id),
    KEY idx_cash_movements_user_id (user_id),
    CONSTRAINT fk_cash_movements_shift FOREIGN KEY (shift_id) REFERENCES shifts (id) ON DELETE CASCADE,
    CONSTRAINT fk_cash_movements_user FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT fk_cash_movements_approved_by FOREIGN KEY (approved_by_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
