CREATE TABLE expenses (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id       BIGINT UNSIGNED NOT NULL,
    category        VARCHAR(20) NOT NULL,
    amount_cents    BIGINT NOT NULL,
    expense_date    DATE NOT NULL,
    note            VARCHAR(255) NOT NULL DEFAULT '',
    user_id         BIGINT UNSIGNED NOT NULL,
    attachment_url  VARCHAR(500) NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at      DATETIME NULL,
    KEY idx_expenses_branch_id (branch_id),
    KEY idx_expenses_category (category),
    KEY idx_expenses_expense_date (expense_date),
    KEY idx_expenses_deleted_at (deleted_at),
    CONSTRAINT fk_expenses_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_expenses_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
