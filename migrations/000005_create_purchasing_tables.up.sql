CREATE TABLE purchase_orders (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id     BIGINT UNSIGNED NOT NULL,
    supplier_id   BIGINT UNSIGNED NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'DRAFT',
    note          VARCHAR(255) NOT NULL DEFAULT '',
    user_id       BIGINT UNSIGNED NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_purchase_orders_branch_id (branch_id),
    KEY idx_purchase_orders_supplier_id (supplier_id),
    KEY idx_purchase_orders_status (status),
    CONSTRAINT fk_purchase_orders_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_purchase_orders_supplier FOREIGN KEY (supplier_id) REFERENCES suppliers (id),
    CONSTRAINT fk_purchase_orders_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE purchase_order_items (
    id                  BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    purchase_order_id   BIGINT UNSIGNED NOT NULL,
    product_id          BIGINT UNSIGNED NOT NULL,
    qty_ordered         BIGINT NOT NULL,
    qty_received        BIGINT NOT NULL DEFAULT 0,
    unit_cost_cents     BIGINT NOT NULL,
    KEY idx_poi_purchase_order_id (purchase_order_id),
    KEY idx_poi_product_id (product_id),
    CONSTRAINT fk_poi_purchase_order FOREIGN KEY (purchase_order_id) REFERENCES purchase_orders (id) ON DELETE CASCADE,
    CONSTRAINT fk_poi_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_transfers (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    from_branch_id  BIGINT UNSIGNED NOT NULL,
    to_branch_id    BIGINT UNSIGNED NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'DRAFT',
    note            VARCHAR(255) NOT NULL DEFAULT '',
    user_id         BIGINT UNSIGNED NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_stock_transfers_from_branch_id (from_branch_id),
    KEY idx_stock_transfers_to_branch_id (to_branch_id),
    KEY idx_stock_transfers_status (status),
    CONSTRAINT fk_stock_transfers_from_branch FOREIGN KEY (from_branch_id) REFERENCES branches (id),
    CONSTRAINT fk_stock_transfers_to_branch FOREIGN KEY (to_branch_id) REFERENCES branches (id),
    CONSTRAINT fk_stock_transfers_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_transfer_items (
    id                  BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    stock_transfer_id   BIGINT UNSIGNED NOT NULL,
    product_id          BIGINT UNSIGNED NOT NULL,
    qty                 BIGINT NOT NULL,
    KEY idx_sti_stock_transfer_id (stock_transfer_id),
    KEY idx_sti_product_id (product_id),
    CONSTRAINT fk_sti_stock_transfer FOREIGN KEY (stock_transfer_id) REFERENCES stock_transfers (id) ON DELETE CASCADE,
    CONSTRAINT fk_sti_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_counts (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id   BIGINT UNSIGNED NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'OPEN',
    note        VARCHAR(255) NOT NULL DEFAULT '',
    user_id     BIGINT UNSIGNED NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_stock_counts_branch_id (branch_id),
    KEY idx_stock_counts_status (status),
    CONSTRAINT fk_stock_counts_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_stock_counts_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_count_items (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    stock_count_id   BIGINT UNSIGNED NOT NULL,
    product_id       BIGINT UNSIGNED NOT NULL,
    expected_qty     BIGINT NOT NULL,
    counted_qty      BIGINT NOT NULL,
    KEY idx_sci_stock_count_id (stock_count_id),
    KEY idx_sci_product_id (product_id),
    CONSTRAINT fk_sci_stock_count FOREIGN KEY (stock_count_id) REFERENCES stock_counts (id) ON DELETE CASCADE,
    CONSTRAINT fk_sci_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
