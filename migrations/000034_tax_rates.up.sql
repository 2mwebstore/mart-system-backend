-- Tax rates a company can add on top of a POS sale, invoice, purchase order or
-- expense. Each of those keeps a snapshot of the rate it used (id, name,
-- percent) and the tax it came to, so changing or deleting a rate never
-- changes a document already made.
CREATE TABLE tax_rates (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    company_id  BIGINT UNSIGNED NOT NULL,
    name        VARCHAR(60)  NOT NULL,
    percent     DECIMAL(5,2) NOT NULL,
    active      TINYINT(1)   NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME NULL,
    KEY idx_tax_rates_company (company_id),
    KEY idx_tax_rates_deleted_at (deleted_at),
    CONSTRAINT fk_tax_rates_company FOREIGN KEY (company_id) REFERENCES companies (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE sales
    ADD COLUMN tax_rate_id BIGINT UNSIGNED NULL AFTER discount_cents,
    ADD COLUMN tax_name    VARCHAR(60)  NOT NULL DEFAULT '' AFTER tax_rate_id,
    ADD COLUMN tax_percent DECIMAL(5,2) NOT NULL DEFAULT 0 AFTER tax_name,
    ADD COLUMN tax_cents   BIGINT       NOT NULL DEFAULT 0 AFTER tax_percent,
    -- The sale's own note, rich text from the till.
    MODIFY COLUMN note TEXT NOT NULL DEFAULT (''),
    ADD CONSTRAINT fk_sales_tax_rate FOREIGN KEY (tax_rate_id) REFERENCES tax_rates (id);

ALTER TABLE invoices
    ADD COLUMN tax_rate_id BIGINT UNSIGNED NULL AFTER shipping_cents,
    ADD COLUMN tax_name    VARCHAR(60)  NOT NULL DEFAULT '' AFTER tax_rate_id,
    ADD COLUMN tax_percent DECIMAL(5,2) NOT NULL DEFAULT 0 AFTER tax_name,
    ADD COLUMN tax_cents   BIGINT       NOT NULL DEFAULT 0 AFTER tax_percent,
    ADD CONSTRAINT fk_invoices_tax_rate FOREIGN KEY (tax_rate_id) REFERENCES tax_rates (id);

ALTER TABLE purchase_orders
    ADD COLUMN tax_rate_id BIGINT UNSIGNED NULL AFTER shipping_cents,
    ADD COLUMN tax_name    VARCHAR(60)  NOT NULL DEFAULT '' AFTER tax_rate_id,
    ADD COLUMN tax_percent DECIMAL(5,2) NOT NULL DEFAULT 0 AFTER tax_name,
    ADD COLUMN tax_cents   BIGINT       NOT NULL DEFAULT 0 AFTER tax_percent,
    ADD CONSTRAINT fk_purchase_orders_tax_rate FOREIGN KEY (tax_rate_id) REFERENCES tax_rates (id);

ALTER TABLE expenses
    ADD COLUMN tax_rate_id BIGINT UNSIGNED NULL AFTER amount_cents,
    ADD COLUMN tax_name    VARCHAR(60)  NOT NULL DEFAULT '' AFTER tax_rate_id,
    ADD COLUMN tax_percent DECIMAL(5,2) NOT NULL DEFAULT 0 AFTER tax_name,
    ADD COLUMN tax_cents   BIGINT       NOT NULL DEFAULT 0 AFTER tax_percent,
    -- Rich text now, like invoice and purchase order notes.
    MODIFY COLUMN note TEXT NOT NULL DEFAULT (''),
    ADD CONSTRAINT fk_expenses_tax_rate FOREIGN KEY (tax_rate_id) REFERENCES tax_rates (id);

-- Listing rates is open to everyone signed in (the till and the forms need
-- them); changing them is these. Whoever could change the company's
-- settings gets them, which is every Owner.
INSERT IGNORE INTO permissions (`key`, `group`, description) VALUES
    ('tax_rate.create', 'tax_rate', 'Create tax rates'),
    ('tax_rate.update', 'tax_rate', 'Edit tax rates and where they start'),
    ('tax_rate.delete', 'tax_rate', 'Delete tax rates');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions op ON op.id = rp.permission_id AND op.`key` = 'settings.company'
JOIN permissions np ON np.`key` IN ('tax_rate.create', 'tax_rate.update', 'tax_rate.delete');
