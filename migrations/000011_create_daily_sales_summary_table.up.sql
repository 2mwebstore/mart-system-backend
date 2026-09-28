CREATE TABLE daily_sales_summary (
    branch_id           BIGINT UNSIGNED NOT NULL,
    summary_date        DATE NOT NULL,
    transactions_count  BIGINT NOT NULL DEFAULT 0,
    gross_sales_cents   BIGINT NOT NULL DEFAULT 0,
    discount_cents      BIGINT NOT NULL DEFAULT 0,
    refund_cents        BIGINT NOT NULL DEFAULT 0,
    net_sales_cents     BIGINT NOT NULL DEFAULT 0,
    cost_total_cents    BIGINT NOT NULL DEFAULT 0,
    items_sold_qty      BIGINT NOT NULL DEFAULT 0,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (branch_id, summary_date),
    CONSTRAINT fk_daily_sales_summary_branch FOREIGN KEY (branch_id) REFERENCES branches (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
