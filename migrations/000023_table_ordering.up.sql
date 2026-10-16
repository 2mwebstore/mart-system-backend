-- QR table ordering: a customer scans the QR on a table, orders from that
-- branch's menu, and the order shows up on the POS for staff to accept and
-- take payment for. See docs/DECISIONS.md.

CREATE TABLE dining_tables (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id   BIGINT UNSIGNED NOT NULL,
    name        VARCHAR(40)  NOT NULL,
    -- Random and unguessable: it's the only thing in the printed QR, and
    -- anyone holding it can order to this table. Rotating it voids old prints.
    token       VARCHAR(32)  NOT NULL,
    active      TINYINT(1)   NOT NULL DEFAULT 1,
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_dining_tables_token (token),
    UNIQUE KEY uk_dining_tables_branch_name (branch_id, name),
    CONSTRAINT fk_dining_tables_branch FOREIGN KEY (branch_id) REFERENCES branches (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE table_orders (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id     BIGINT UNSIGNED NOT NULL,
    -- Kept even if the table is deleted later; table_name is the snapshot.
    table_id      BIGINT UNSIGNED NULL,
    table_name    VARCHAR(40)  NOT NULL,
    -- The customer's own status link (their phone polls it); separate from
    -- the table token so one order's link can't read another's.
    public_token  VARCHAR(32)  NOT NULL,
    status        VARCHAR(12)  NOT NULL DEFAULT 'NEW', -- NEW | ACCEPTED | PAID | CANCELLED
    note          VARCHAR(255) NOT NULL DEFAULT '',
    total_cents   BIGINT       NOT NULL DEFAULT 0,     -- at order time; the sale re-prices
    sale_id       BIGINT UNSIGNED NULL,
    handled_by    BIGINT UNSIGNED NULL,                -- who accepted / cancelled
    cancel_reason VARCHAR(255) NOT NULL DEFAULT '',
    accepted_at   DATETIME     NULL,
    closed_at     DATETIME     NULL,                   -- paid or cancelled
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_table_orders_public_token (public_token),
    KEY idx_table_orders_branch_status (branch_id, status, created_at),
    KEY idx_table_orders_table (table_id),
    CONSTRAINT fk_table_orders_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_table_orders_table FOREIGN KEY (table_id) REFERENCES dining_tables (id) ON DELETE SET NULL,
    CONSTRAINT fk_table_orders_sale FOREIGN KEY (sale_id) REFERENCES sales (id) ON DELETE SET NULL,
    CONSTRAINT fk_table_orders_handled_by FOREIGN KEY (handled_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE table_order_items (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    table_order_id   BIGINT UNSIGNED NOT NULL,
    product_id       BIGINT UNSIGNED NOT NULL,
    name_en          VARCHAR(150) NOT NULL,
    name_km          VARCHAR(150) NOT NULL DEFAULT '',
    qty              INT          NOT NULL,
    unit_price_cents BIGINT       NOT NULL,
    note             VARCHAR(255) NOT NULL DEFAULT '',
    KEY idx_table_order_items_order (table_order_id),
    CONSTRAINT fk_table_order_items_order FOREIGN KEY (table_order_id) REFERENCES table_orders (id) ON DELETE CASCADE,
    CONSTRAINT fk_table_order_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
