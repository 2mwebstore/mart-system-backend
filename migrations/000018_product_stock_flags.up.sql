ALTER TABLE products
    ADD COLUMN allow_out_of_stock_sale TINYINT(1) NOT NULL DEFAULT 0 AFTER active,
    ADD COLUMN hide_when_out_of_stock TINYINT(1) NOT NULL DEFAULT 0 AFTER allow_out_of_stock_sale;
