ALTER TABLE products
    ADD COLUMN parent_product_id BIGINT UNSIGNED NULL AFTER id,
    ADD COLUMN variant_name VARCHAR(100) NOT NULL DEFAULT '' AFTER name_km,
    ADD KEY idx_products_parent_product_id (parent_product_id),
    ADD CONSTRAINT fk_products_parent FOREIGN KEY (parent_product_id) REFERENCES products (id) ON DELETE CASCADE;
