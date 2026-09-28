CREATE TABLE branches (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(120) NOT NULL,
    code            VARCHAR(20)  NOT NULL,
    address         VARCHAR(255) NOT NULL DEFAULT '',
    phone           VARCHAR(30)  NOT NULL DEFAULT '',
    receipt_footer  VARCHAR(500) NOT NULL DEFAULT '',
    active          TINYINT(1)   NOT NULL DEFAULT 1,
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at      DATETIME     NULL,
    UNIQUE KEY uk_branches_code (code),
    KEY idx_branches_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE devices (
    id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id      BIGINT UNSIGNED NOT NULL,
    name           VARCHAR(80)  NOT NULL,
    device_key     VARCHAR(64)  NOT NULL,
    last_seen_at   DATETIME     NULL,
    active         TINYINT(1)   NOT NULL DEFAULT 1,
    created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_devices_device_key (device_key),
    KEY idx_devices_branch_id (branch_id),
    CONSTRAINT fk_devices_branch FOREIGN KEY (branch_id) REFERENCES branches (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
