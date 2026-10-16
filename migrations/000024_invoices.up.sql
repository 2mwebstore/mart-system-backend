-- Back-office sales ("invoices"), separate from POS receipts: a customer,
-- searched-in items with per-line discounts, an invoice discount, shipping
-- and a note, paid over time in any number of payments (each optionally with
-- a proof image or link). payment_status is DUE (nothing paid), PENDING
-- (partly paid) or PAID, kept in step with paid_cents. See docs/DECISIONS.md.

INSERT INTO number_sequences (doc_type, prefix, next_number) VALUES ('INVOICE', 'SI', 1)
ON DUPLICATE KEY UPDATE doc_type = doc_type;

CREATE TABLE invoices (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    code             VARCHAR(30)  NOT NULL,
    branch_id        BIGINT UNSIGNED NOT NULL,
    customer_id      BIGINT UNSIGNED NOT NULL,
    invoice_date     DATE         NOT NULL,
    due_date         DATE         NULL,
    status           VARCHAR(12)  NOT NULL DEFAULT 'OPEN',  -- OPEN | CANCELLED
    payment_status   VARCHAR(12)  NOT NULL DEFAULT 'DUE',   -- DUE | PENDING | PAID
    subtotal_cents   BIGINT       NOT NULL DEFAULT 0,       -- qty × unit price, before any discount
    line_discount_cents BIGINT    NOT NULL DEFAULT 0,
    discount_cents   BIGINT       NOT NULL DEFAULT 0,       -- invoice-level discount
    shipping_cents   BIGINT       NOT NULL DEFAULT 0,
    total_cents      BIGINT       NOT NULL DEFAULT 0,
    paid_cents       BIGINT       NOT NULL DEFAULT 0,
    cost_total_cents BIGINT       NOT NULL DEFAULT 0,
    exchange_rate    INT          NOT NULL DEFAULT 0,       -- riel per USD on the invoice date
    note             VARCHAR(500) NOT NULL DEFAULT '',
    cancel_reason    VARCHAR(255) NOT NULL DEFAULT '',
    cancelled_at     DATETIME     NULL,
    user_id          BIGINT UNSIGNED NOT NULL,
    created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_invoices_code (code),
    KEY idx_invoices_branch_date (branch_id, invoice_date),
    KEY idx_invoices_branch_payment (branch_id, payment_status),
    KEY idx_invoices_customer (customer_id),
    CONSTRAINT fk_invoices_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_invoices_customer FOREIGN KEY (customer_id) REFERENCES customers (id),
    CONSTRAINT fk_invoices_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE invoice_items (
    id               BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    invoice_id       BIGINT UNSIGNED NOT NULL,
    product_id       BIGINT UNSIGNED NOT NULL,
    name_en          VARCHAR(150) NOT NULL,
    name_km          VARCHAR(150) NOT NULL DEFAULT '',
    sku              VARCHAR(60)  NOT NULL DEFAULT '',
    qty              INT          NOT NULL,
    unit_price_cents BIGINT       NOT NULL,
    discount_cents   BIGINT       NOT NULL DEFAULT 0,       -- on the whole line
    line_total_cents BIGINT       NOT NULL,
    unit_cost_cents  BIGINT       NOT NULL DEFAULT 0,
    note             VARCHAR(255) NOT NULL DEFAULT '',
    KEY idx_invoice_items_invoice (invoice_id),
    CONSTRAINT fk_invoice_items_invoice FOREIGN KEY (invoice_id) REFERENCES invoices (id) ON DELETE CASCADE,
    CONSTRAINT fk_invoice_items_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE invoice_payments (
    id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    invoice_id   BIGINT UNSIGNED NOT NULL,
    paid_on      DATE         NOT NULL,
    method_name  VARCHAR(60)  NOT NULL,                     -- as configured in Settings at the time
    method_type  VARCHAR(10)  NOT NULL,                     -- CASH | KHQR | CARD | OTHER
    amount_cents BIGINT       NOT NULL,
    reference    VARCHAR(120) NOT NULL DEFAULT '',
    note         VARCHAR(255) NOT NULL DEFAULT '',
    -- Proof of payment: an https link, or an image as a data URL (shrunk in the
    -- browser first) — the same storage rule as product images until object
    -- storage exists. Only the invoice detail endpoint returns it.
    attachment   MEDIUMTEXT   NULL,
    user_id      BIGINT UNSIGNED NOT NULL,
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_invoice_payments_invoice (invoice_id),
    CONSTRAINT fk_invoice_payments_invoice FOREIGN KEY (invoice_id) REFERENCES invoices (id) ON DELETE CASCADE,
    CONSTRAINT fk_invoice_payments_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Who can create invoices and take their payments. Added here (not only in
-- the seed) so databases that are already set up get it too.
INSERT IGNORE INTO permissions (`key`, `group`, description) VALUES ('invoice.manage', 'invoice', 'Create invoices and record their payments');
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.`key` = 'invoice.manage' WHERE r.name IN ('Owner', 'Manager');
