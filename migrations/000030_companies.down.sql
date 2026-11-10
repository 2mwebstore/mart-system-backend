-- Going back to one business only works while there is at most one company:
-- two companies' products, customers and numbers can't be merged safely, so
-- this stops with "Check constraint 'only_one_company_can_be_rolled_back' is
-- violated" instead.
CREATE TEMPORARY TABLE companies_rollback_check (
    companies INT NOT NULL,
    CONSTRAINT only_one_company_can_be_rolled_back CHECK (companies <= 1)
);
INSERT INTO companies_rollback_check SELECT COUNT(*) FROM companies;
DROP TEMPORARY TABLE companies_rollback_check;

-- The company's settings go back to the shared table.
REPLACE INTO settings (`key`, value)
SELECT `key`, value FROM company_settings;
DROP TABLE company_settings;

-- Super admins don't exist without companies.
DELETE FROM activity_logs WHERE user_id IN (SELECT id FROM (SELECT id FROM users WHERE company_id IS NULL) super_admins);
DELETE FROM users WHERE company_id IS NULL;
DELETE FROM roles WHERE company_id IS NULL;

ALTER TABLE number_sequences
    DROP FOREIGN KEY fk_number_sequences_company,
    DROP PRIMARY KEY,
    ADD PRIMARY KEY (doc_type),
    DROP COLUMN company_id;
ALTER TABLE invoices
    DROP INDEX uk_invoices_branch_code,
    ADD UNIQUE KEY uk_invoices_code (code);
ALTER TABLE devices
    DROP FOREIGN KEY fk_devices_company,
    DROP INDEX uk_devices_company_key,
    ADD UNIQUE KEY uk_devices_device_key (device_key),
    DROP COLUMN company_id;
ALTER TABLE activity_logs
    DROP FOREIGN KEY fk_activity_logs_company,
    DROP INDEX idx_activity_logs_company_id,
    DROP COLUMN company_id;
ALTER TABLE exchange_rates
    DROP FOREIGN KEY fk_exchange_rates_company,
    DROP INDEX idx_exchange_rates_company_effective,
    DROP COLUMN company_id;
ALTER TABLE payment_methods
    DROP FOREIGN KEY fk_payment_methods_company,
    DROP INDEX idx_payment_methods_company_id,
    DROP COLUMN company_id;
ALTER TABLE customers
    DROP FOREIGN KEY fk_customers_company,
    DROP INDEX uk_customers_company_phone,
    ADD UNIQUE KEY uk_customers_phone (phone),
    DROP COLUMN company_id;
ALTER TABLE suppliers
    DROP FOREIGN KEY fk_suppliers_company,
    DROP INDEX idx_suppliers_company_id,
    DROP COLUMN company_id;
ALTER TABLE categories
    DROP FOREIGN KEY fk_categories_company,
    DROP INDEX idx_categories_company_id,
    DROP COLUMN company_id;
ALTER TABLE products
    DROP FOREIGN KEY fk_products_company,
    DROP INDEX uk_products_company_sku,
    ADD UNIQUE KEY uk_products_sku (sku),
    DROP COLUMN company_id;
ALTER TABLE roles
    DROP FOREIGN KEY fk_roles_company,
    DROP INDEX uk_roles_company_name,
    ADD UNIQUE KEY uk_roles_name (name),
    DROP COLUMN company_id;
ALTER TABLE users
    DROP FOREIGN KEY fk_users_company,
    DROP INDEX idx_users_company_id,
    DROP COLUMN company_id;
ALTER TABLE branches
    DROP FOREIGN KEY fk_branches_company,
    DROP INDEX uk_branches_company_code,
    ADD UNIQUE KEY uk_branches_code (code),
    DROP COLUMN company_id;

-- A database that had no company before going up gets back its shared
-- numbering rows.
INSERT IGNORE INTO number_sequences (doc_type, prefix, next_number) VALUES
    ('SALE', 'INV', 1), ('EXPENSE', 'EXP', 1), ('PURCHASE_ORDER', 'PO', 1), ('INVOICE', 'SI', 1);

DROP TABLE companies;
