-- Back to the all-in-one keys: each comes back for any role holding one of
-- the keys that replaced it, then the separate keys go.

INSERT IGNORE INTO permissions (`key`, `group`, description) VALUES
    ('inventory.edit_price', 'inventory', 'Edit product price/cost'),
    ('inventory.product_create', 'inventory', 'Create new products (and add variants)'),
    ('inventory.product_edit', 'inventory', 'Edit or delete existing products'),
    ('inventory.purchase_order', 'inventory', 'Manage purchase orders'),
    ('inventory.receive', 'inventory', 'Receive stock'),
    ('customer.manage', 'customer', 'Create/edit customers'),
    ('invoice.manage', 'invoice', 'Create invoices and record their payments'),
    ('expense.manage', 'user', 'Manage expenses'),
    ('user.manage', 'user', 'Manage users'),
    ('role.manage', 'user', 'Manage roles & permissions'),
    ('branch.manage', 'user', 'Manage branches');

CREATE TEMPORARY TABLE permission_map (old_key VARCHAR(60) NOT NULL, new_key VARCHAR(60) NOT NULL)
    DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
INSERT INTO permission_map (old_key, new_key) VALUES
    ('inventory.edit_price', 'category.create'),
    ('inventory.edit_price', 'category.update'),
    ('inventory.edit_price', 'category.delete'),
    ('inventory.edit_price', 'supplier.create'),
    ('inventory.edit_price', 'supplier.update'),
    ('inventory.edit_price', 'supplier.delete'),
    ('inventory.product_create', 'product.create'),
    ('inventory.product_edit', 'product.update'),
    ('inventory.product_edit', 'product.delete'),
    ('inventory.purchase_order', 'purchase_order.create'),
    ('inventory.purchase_order', 'purchase_order.update'),
    ('inventory.purchase_order', 'purchase_order.delete'),
    ('inventory.receive', 'purchase_order.receive'),
    ('customer.manage', 'customer.create'),
    ('customer.manage', 'customer.update'),
    ('customer.manage', 'customer.delete'),
    ('invoice.manage', 'invoice.create'),
    ('invoice.manage', 'invoice.update'),
    ('invoice.manage', 'invoice.delete'),
    ('expense.manage', 'expense.view'),
    ('expense.manage', 'expense.create'),
    ('expense.manage', 'expense.update'),
    ('expense.manage', 'expense.delete'),
    ('user.manage', 'user.create'),
    ('user.manage', 'user.update'),
    ('user.manage', 'user.delete'),
    ('role.manage', 'role.create'),
    ('role.manage', 'role.update'),
    ('role.manage', 'role.delete'),
    ('branch.manage', 'branch.create'),
    ('branch.manage', 'branch.update'),
    ('branch.manage', 'branch.delete'),
    ('branch.manage', 'table.create'),
    ('branch.manage', 'table.update'),
    ('branch.manage', 'table.delete'),
    ('branch.manage', 'payment_method.create'),
    ('branch.manage', 'payment_method.update'),
    ('branch.manage', 'payment_method.delete'),
    ('branch.manage', 'settings.company'),
    ('inventory.view', 'product.view'),
    ('inventory.view', 'category.view'),
    ('inventory.view', 'supplier.view'),
    ('inventory.view', 'purchase_order.view');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, op.id
FROM role_permissions rp
JOIN permissions np ON np.id = rp.permission_id
JOIN permission_map m ON m.new_key = np.`key`
JOIN permissions op ON op.`key` = m.old_key;

DROP TEMPORARY TABLE permission_map;

DELETE FROM permissions WHERE `key` IN ('product.view', 'product.create', 'product.update', 'product.delete', 'category.view', 'category.create', 'category.update', 'category.delete', 'supplier.view', 'supplier.create', 'supplier.update', 'supplier.delete', 'purchase_order.view', 'purchase_order.create', 'purchase_order.update', 'purchase_order.delete', 'purchase_order.receive', 'customer.create', 'customer.update', 'customer.delete', 'invoice.view', 'invoice.create', 'invoice.update', 'invoice.delete', 'expense.view', 'expense.create', 'expense.update', 'expense.delete', 'user.view', 'user.create', 'user.update', 'user.delete', 'role.view', 'role.create', 'role.update', 'role.delete', 'branch.view', 'branch.create', 'branch.update', 'branch.delete', 'table.view', 'table.create', 'table.update', 'table.delete', 'payment_method.create', 'payment_method.update', 'payment_method.delete', 'settings.company');

UPDATE permissions SET description = 'View inventory' WHERE `key` = 'inventory.view';
UPDATE permissions SET `group` = 'user', description = 'Manage the company''s alerts, numbering and activity-log retention' WHERE `key` = 'system.manage';
UPDATE permissions SET `group` = 'user' WHERE `key` IN ('settings.rate', 'system.reset');
