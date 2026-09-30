-- Products gain a type: STANDARD (physical, stock-tracked — the existing
-- behavior) or SERVICE (no stock: a fee, a delivery charge, an install job —
-- always sellable, never shows on a stock report, never blocks a sale for
-- "insufficient stock").
ALTER TABLE products
    ADD COLUMN product_type VARCHAR(20) NOT NULL DEFAULT 'STANDARD' AFTER unit,
    ADD KEY idx_products_type (product_type);
