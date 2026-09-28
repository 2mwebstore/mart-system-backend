DROP TABLE payment_methods;
ALTER TABLE products MODIFY COLUMN image_url VARCHAR(500) NOT NULL DEFAULT '';
ALTER TABLE purchase_orders DROP COLUMN shipping_cents;
