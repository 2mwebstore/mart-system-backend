-- Companies: several fully separate businesses in one install. Each has its
-- own branches, staff, roles, catalog, customers, settings, exchange rate and
-- document numbers. A super admin (company_id NULL) creates and manages them.
CREATE TABLE companies (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(120) NOT NULL,
    code        VARCHAR(12)  NOT NULL,
    active      TINYINT(1)   NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME NULL,
    UNIQUE KEY uk_companies_code (code),
    KEY idx_companies_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- An install that already has data becomes company 1. A new, empty one has no
-- company until the super admin creates one.
INSERT INTO companies (id, name, code)
SELECT 1, COALESCE(NULLIF((SELECT value FROM settings WHERE `key` = 'company_name'), ''), 'Main company'), 'MAIN'
FROM DUAL
WHERE EXISTS (SELECT 1 FROM users) OR EXISTS (SELECT 1 FROM branches);

-- Every table that isn't reached through a branch gets the company it
-- belongs to.
ALTER TABLE branches        ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE users           ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE roles           ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE products        ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE categories      ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE suppliers       ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE customers       ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE payment_methods ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE exchange_rates  ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE activity_logs   ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE devices         ADD COLUMN company_id BIGINT UNSIGNED NULL AFTER id;
ALTER TABLE number_sequences ADD COLUMN company_id BIGINT UNSIGNED NULL FIRST;

UPDATE branches        SET company_id = 1;
UPDATE users           SET company_id = 1;
UPDATE roles           SET company_id = 1;
UPDATE products        SET company_id = 1;
UPDATE categories      SET company_id = 1;
UPDATE suppliers       SET company_id = 1;
UPDATE customers       SET company_id = 1;
UPDATE payment_methods SET company_id = 1;
UPDATE exchange_rates  SET company_id = 1;
UPDATE activity_logs   SET company_id = 1;
UPDATE devices         SET company_id = 1;
UPDATE number_sequences SET company_id = 1;
-- With no company, the global sequence rows have nobody to belong to: each new
-- company gets its own when it's created.
DELETE FROM number_sequences WHERE company_id NOT IN (SELECT id FROM companies);

-- users.company_id and roles.company_id stay nullable: NULL is the super
-- admin and their role. activity_logs.company_id is NULL for what the super
-- admin did.
ALTER TABLE branches
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    DROP INDEX uk_branches_code,
    ADD UNIQUE KEY uk_branches_company_code (company_id, code),
    ADD CONSTRAINT fk_branches_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE users
    ADD KEY idx_users_company_id (company_id),
    ADD CONSTRAINT fk_users_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE roles
    DROP INDEX uk_roles_name,
    ADD UNIQUE KEY uk_roles_company_name (company_id, name),
    ADD CONSTRAINT fk_roles_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE products
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    DROP INDEX uk_products_sku,
    ADD UNIQUE KEY uk_products_company_sku (company_id, sku),
    ADD CONSTRAINT fk_products_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE categories
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    ADD KEY idx_categories_company_id (company_id),
    ADD CONSTRAINT fk_categories_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE suppliers
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    ADD KEY idx_suppliers_company_id (company_id),
    ADD CONSTRAINT fk_suppliers_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE customers
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    DROP INDEX uk_customers_phone,
    ADD UNIQUE KEY uk_customers_company_phone (company_id, phone),
    ADD CONSTRAINT fk_customers_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE payment_methods
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    ADD KEY idx_payment_methods_company_id (company_id),
    ADD CONSTRAINT fk_payment_methods_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE exchange_rates
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    ADD KEY idx_exchange_rates_company_effective (company_id, effective_at),
    ADD CONSTRAINT fk_exchange_rates_company FOREIGN KEY (company_id) REFERENCES companies (id);
ALTER TABLE activity_logs
    ADD KEY idx_activity_logs_company_id (company_id),
    ADD CONSTRAINT fk_activity_logs_company FOREIGN KEY (company_id) REFERENCES companies (id);
-- A till's company is its branch's, kept here so each company can name its
-- own tills (two companies can both have "TILL-1").
ALTER TABLE devices
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    DROP INDEX uk_devices_device_key,
    ADD UNIQUE KEY uk_devices_company_key (company_id, device_key),
    ADD CONSTRAINT fk_devices_company FOREIGN KEY (company_id) REFERENCES companies (id);
-- Numbering is per company, so the same invoice code can turn up in two
-- companies. It's still unique within a branch, which is always one company's.
ALTER TABLE invoices
    DROP INDEX uk_invoices_code,
    ADD UNIQUE KEY uk_invoices_branch_code (branch_id, code);
ALTER TABLE number_sequences
    MODIFY company_id BIGINT UNSIGNED NOT NULL,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (company_id, doc_type),
    ADD CONSTRAINT fk_number_sequences_company FOREIGN KEY (company_id) REFERENCES companies (id);

-- Each company's own settings. `settings` keeps only what's for the whole
-- install: backups and the alerts about them.
CREATE TABLE company_settings (
    company_id  BIGINT UNSIGNED NOT NULL,
    `key`       VARCHAR(100) NOT NULL,
    value       MEDIUMTEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (company_id, `key`),
    CONSTRAINT fk_company_settings_company FOREIGN KEY (company_id) REFERENCES companies (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO company_settings (company_id, `key`, value)
SELECT c.id, s.`key`, s.value FROM settings s JOIN companies c ON c.id = 1
WHERE s.`key` IN ('company_name', 'company_logo', 'receipt_header', 'receipt_footer', 'loyalty_points_per_usd',
                  'allow_out_of_stock_sale', 'telegram_enabled', 'telegram_bot_token', 'telegram_chat_id',
                  'telegram_alert_types', 'activity_log_retention_months');
DELETE FROM settings
WHERE `key` IN ('company_name', 'company_logo', 'receipt_header', 'receipt_footer', 'loyalty_points_per_usd',
                'allow_out_of_stock_sale', 'activity_log_retention_months');
-- The Telegram keys stay in settings too, as the install's own alerts (backups),
-- so an upgraded install keeps getting them where it did.

-- The super admin's role: no company, no permissions, can't be edited.
INSERT INTO roles (company_id, name, description, is_locked, created_at, updated_at)
VALUES (NULL, 'Super admin', 'Manages companies, backups and resets. Sees no company data.', 1, NOW(), NOW());
