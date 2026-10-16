DELETE rp FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id WHERE p.`key` = 'invoice.manage';
DELETE FROM permissions WHERE `key` = 'invoice.manage';
DROP TABLE IF EXISTS invoice_payments;
DROP TABLE IF EXISTS invoice_items;
DROP TABLE IF EXISTS invoices;
DELETE FROM number_sequences WHERE doc_type = 'INVOICE';
