CREATE TABLE sales (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id         BIGINT UNSIGNED NOT NULL,
    shift_id          BIGINT UNSIGNED NOT NULL,
    device_id         BIGINT UNSIGNED NOT NULL,
    cashier_id        BIGINT UNSIGNED NOT NULL,
    customer_id       BIGINT UNSIGNED NULL,
    receipt_no        VARCHAR(20) NOT NULL,
    -- Asia/Phnom_Penh calendar date, computed by the service layer (not by
    -- MySQL from sold_at) — see build spec §3 and models/sale.go.
    business_date     DATE NOT NULL,
    status            VARCHAR(20) NOT NULL DEFAULT 'PAID',
    subtotal_cents    BIGINT NOT NULL,
    discount_cents    BIGINT NOT NULL DEFAULT 0,
    total_cents       BIGINT NOT NULL,
    total_riel        BIGINT NOT NULL,
    exchange_rate     BIGINT NOT NULL,
    cost_total_cents  BIGINT NOT NULL,
    note              VARCHAR(255) NOT NULL DEFAULT '',
    idempotency_key   VARCHAR(80) NOT NULL,
    sold_at           DATETIME NOT NULL,
    UNIQUE KEY uk_sales_branch_receipt_date (branch_id, receipt_no, business_date),
    UNIQUE KEY uk_sales_idempotency_key (idempotency_key),
    KEY idx_sales_branch_sold_at (branch_id, sold_at),
    KEY idx_sales_shift_id (shift_id),
    KEY idx_sales_device_id (device_id),
    KEY idx_sales_cashier_id (cashier_id),
    KEY idx_sales_customer_id (customer_id),
    KEY idx_sales_status (status),
    CONSTRAINT fk_sales_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_sales_shift FOREIGN KEY (shift_id) REFERENCES shifts (id),
    CONSTRAINT fk_sales_device FOREIGN KEY (device_id) REFERENCES devices (id),
    CONSTRAINT fk_sales_cashier FOREIGN KEY (cashier_id) REFERENCES users (id),
    CONSTRAINT fk_sales_customer FOREIGN KEY (customer_id) REFERENCES customers (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE sale_items (
    id                 BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sale_id            BIGINT UNSIGNED NOT NULL,
    product_id         BIGINT UNSIGNED NOT NULL,
    name_snapshot      VARCHAR(150) NOT NULL,
    qty                BIGINT NOT NULL,
    unit_price_cents   BIGINT NOT NULL,
    unit_cost_cents    BIGINT NOT NULL,
    discount_cents     BIGINT NOT NULL DEFAULT 0,
    line_total_cents   BIGINT NOT NULL,
    KEY idx_sale_items_sale_id (sale_id),
    KEY idx_sale_items_product_id (product_id),
    CONSTRAINT fk_sale_items_sale FOREIGN KEY (sale_id) REFERENCES sales (id) ON DELETE CASCADE,
    CONSTRAINT fk_sale_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE payments (
    id                    BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sale_id               BIGINT UNSIGNED NOT NULL,
    method                VARCHAR(10) NOT NULL,
    currency              VARCHAR(5)  NOT NULL,
    amount_cents          BIGINT NOT NULL DEFAULT 0,
    amount_riel           BIGINT NOT NULL DEFAULT 0,
    received_usd_cents    BIGINT NOT NULL DEFAULT 0,
    received_khr_riel     BIGINT NOT NULL DEFAULT 0,
    change_usd_cents      BIGINT NOT NULL DEFAULT 0,
    change_khr_riel       BIGINT NOT NULL DEFAULT 0,
    reference             VARCHAR(120) NOT NULL DEFAULT '',
    status                VARCHAR(12) NOT NULL DEFAULT 'CONFIRMED',
    created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_payments_sale_id (sale_id),
    KEY idx_payments_method (method),
    CONSTRAINT fk_payments_sale FOREIGN KEY (sale_id) REFERENCES sales (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE voids_refunds (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sale_id           BIGINT UNSIGNED NOT NULL,
    sale_item_id      BIGINT UNSIGNED NULL,
    type              VARCHAR(10) NOT NULL,
    amount_cents      BIGINT NOT NULL,
    reason            VARCHAR(255) NOT NULL DEFAULT '',
    cashier_id        BIGINT UNSIGNED NOT NULL,
    approved_by_id    BIGINT UNSIGNED NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_voids_refunds_sale_id (sale_id),
    KEY idx_voids_refunds_type (type),
    KEY idx_voids_refunds_created_at (created_at),
    CONSTRAINT fk_voids_refunds_sale FOREIGN KEY (sale_id) REFERENCES sales (id) ON DELETE CASCADE,
    CONSTRAINT fk_voids_refunds_sale_item FOREIGN KEY (sale_item_id) REFERENCES sale_items (id),
    CONSTRAINT fk_voids_refunds_cashier FOREIGN KEY (cashier_id) REFERENCES users (id),
    CONSTRAINT fk_voids_refunds_approved_by FOREIGN KEY (approved_by_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
