-- Their grants go with them (ON DELETE CASCADE).
DELETE FROM permissions WHERE `key` IN ('tax_rate.create', 'tax_rate.update', 'tax_rate.delete');

DELETE FROM company_settings WHERE `key` IN ('tax_default_pos', 'tax_default_invoice', 'tax_default_purchase', 'tax_default_expense');

-- Notes longer than the old column are cut to fit rather than failing.
UPDATE expenses SET note = LEFT(note, 255) WHERE CHAR_LENGTH(note) > 255;
ALTER TABLE expenses
    DROP FOREIGN KEY fk_expenses_tax_rate,
    DROP COLUMN tax_rate_id,
    DROP COLUMN tax_name,
    DROP COLUMN tax_percent,
    DROP COLUMN tax_cents,
    MODIFY COLUMN note VARCHAR(255) NOT NULL DEFAULT '';

ALTER TABLE purchase_orders
    DROP FOREIGN KEY fk_purchase_orders_tax_rate,
    DROP COLUMN tax_rate_id,
    DROP COLUMN tax_name,
    DROP COLUMN tax_percent,
    DROP COLUMN tax_cents;

ALTER TABLE invoices
    DROP FOREIGN KEY fk_invoices_tax_rate,
    DROP COLUMN tax_rate_id,
    DROP COLUMN tax_name,
    DROP COLUMN tax_percent,
    DROP COLUMN tax_cents;

UPDATE sales SET note = LEFT(note, 255) WHERE CHAR_LENGTH(note) > 255;
ALTER TABLE sales
    DROP FOREIGN KEY fk_sales_tax_rate,
    DROP COLUMN tax_rate_id,
    DROP COLUMN tax_name,
    DROP COLUMN tax_percent,
    DROP COLUMN tax_cents,
    MODIFY COLUMN note VARCHAR(255) NOT NULL DEFAULT '';

DROP TABLE tax_rates;
