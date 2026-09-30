ALTER TABLE products
    DROP FOREIGN KEY fk_products_parent,
    DROP KEY idx_products_parent_product_id,
    DROP COLUMN variant_name,
    DROP COLUMN parent_product_id;
