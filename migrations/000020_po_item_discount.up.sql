ALTER TABLE purchase_order_items ADD COLUMN discount_cents BIGINT NOT NULL DEFAULT 0 AFTER unit_cost_cents;
