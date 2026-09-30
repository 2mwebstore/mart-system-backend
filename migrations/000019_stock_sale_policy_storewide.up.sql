-- allow_out_of_stock_sale moved from a per-product column (migration 000018)
-- to one store-wide setting (see docs/DECISIONS.md) — hide_when_out_of_stock
-- stays per-product, unaffected.
ALTER TABLE products DROP COLUMN allow_out_of_stock_sale;
