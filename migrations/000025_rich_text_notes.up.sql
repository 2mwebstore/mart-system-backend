-- Invoice and purchase-order notes are rich text now (bold, lists, links —
-- see the RichTextEditor component), stored as sanitised HTML; the old
-- VARCHAR limits are too short for that markup.
ALTER TABLE invoices MODIFY note TEXT NOT NULL;
ALTER TABLE purchase_orders MODIFY note TEXT NOT NULL;
