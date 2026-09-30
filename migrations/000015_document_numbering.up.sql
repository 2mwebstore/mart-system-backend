-- Configurable "PREFIX-000001" reference numbers for sales, expenses and
-- purchase orders. One row per document type; next_number is read-and-
-- incremented under a row lock at creation time (see nextDocNumber in
-- internal/handlers/api.go), so concurrent creates never collide.
CREATE TABLE number_sequences (
    doc_type    VARCHAR(20) NOT NULL PRIMARY KEY,
    prefix      VARCHAR(20) NOT NULL DEFAULT '',
    next_number BIGINT UNSIGNED NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO number_sequences (doc_type, prefix, next_number) VALUES
    ('SALE', 'INV', 1),
    ('EXPENSE', 'EXP', 1),
    ('PURCHASE_ORDER', 'PO', 1);

-- Purchase orders never stored a code — it was computed as "PO-<id>" on
-- every read. Give it a real, stored column so new POs can get a proper
-- sequence number; existing rows keep displaying the same "PO-<id>" they
-- always have, so no historical reference number changes.
ALTER TABLE purchase_orders ADD COLUMN code VARCHAR(30) NOT NULL DEFAULT '' AFTER id;
UPDATE purchase_orders SET code = CONCAT('PO-', id) WHERE code = '';

-- Expenses never had a reference number at all.
ALTER TABLE expenses ADD COLUMN code VARCHAR(30) NOT NULL DEFAULT '' AFTER id;
UPDATE expenses SET code = CONCAT('EXP-', id) WHERE code = '';
