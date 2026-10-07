-- The demo seed created purchase orders and expenses after 000015 had run,
-- without a code, so they showed a blank number. Give any blank code the same
-- "PO-<id>" / "EXP-<id>" form 000015 gave older rows. Rows that already have
-- a code are untouched.
UPDATE purchase_orders SET code = CONCAT('PO-', id) WHERE code = '';
UPDATE expenses SET code = CONCAT('EXP-', id) WHERE code = '';
