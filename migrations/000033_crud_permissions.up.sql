-- Separate view, create, update and delete permissions for every kind of
-- record (products, purchase orders, users, roles…), replacing the older
-- all-in-one keys (user.manage, inventory.product_edit, branch.manage…).
--
-- Every role keeps exactly what it could do: each old key is swapped for the
-- new keys covering the same routes (the map below), then the old keys go.
-- inventory.view used to open products, categories, suppliers and purchase
-- orders as well as stock moves, so it also brings their .view keys;
-- report.sales used to show invoices, so it brings invoice.view.

INSERT INTO permissions (`key`, `group`, description) VALUES
    ('pos.sell', 'pos', 'Sell at the till'),
    ('pos.discount', 'pos', 'Apply a discount'),
    ('pos.void', 'pos', 'Void a sale'),
    ('pos.edit_sale', 'pos', 'Edit a sale''s items (still-open shift only)'),
    ('pos.refund', 'pos', 'Refund a sale'),
    ('pos.open_drawer', 'pos', 'Open the cash drawer without a sale'),
    ('shift.own', 'shift', 'Open/close own shift'),
    ('shift.close_others', 'shift', 'Close another cashier''s shift'),
    ('shift.see_expected', 'shift', 'See expected cash before counting'),
    ('product.view', 'product', 'View products'),
    ('product.create', 'product', 'Create products'),
    ('product.update', 'product', 'Edit products'),
    ('product.delete', 'product', 'Delete products'),
    ('category.view', 'category', 'View categories'),
    ('category.create', 'category', 'Create categories'),
    ('category.update', 'category', 'Edit categories'),
    ('category.delete', 'category', 'Delete categories'),
    ('supplier.view', 'supplier', 'View suppliers'),
    ('supplier.create', 'supplier', 'Create suppliers'),
    ('supplier.update', 'supplier', 'Edit suppliers'),
    ('supplier.delete', 'supplier', 'Delete suppliers'),
    ('inventory.view', 'inventory', 'View stock movements'),
    ('inventory.adjust', 'inventory', 'Adjust/write off/count stock'),
    ('inventory.transfer', 'inventory', 'Transfer stock between branches'),
    ('purchase_order.view', 'purchase_order', 'View purchase orders'),
    ('purchase_order.create', 'purchase_order', 'Create purchase orders'),
    ('purchase_order.update', 'purchase_order', 'Edit purchase orders'),
    ('purchase_order.delete', 'purchase_order', 'Delete purchase orders'),
    ('purchase_order.receive', 'purchase_order', 'Receive stock on a purchase order'),
    ('customer.view', 'customer', 'View customers'),
    ('customer.create', 'customer', 'Create customers'),
    ('customer.update', 'customer', 'Edit customers'),
    ('customer.delete', 'customer', 'Delete customers'),
    ('invoice.view', 'invoice', 'View invoices'),
    ('invoice.create', 'invoice', 'Create invoices'),
    ('invoice.update', 'invoice', 'Edit invoices and record payments'),
    ('invoice.delete', 'invoice', 'Cancel invoices and remove payments'),
    ('expense.view', 'expense', 'View expenses'),
    ('expense.create', 'expense', 'Create expenses'),
    ('expense.update', 'expense', 'Edit expenses'),
    ('expense.delete', 'expense', 'Delete expenses'),
    ('report.sales', 'report', 'View sales reports'),
    ('report.profit_loss', 'report', 'View profit & loss'),
    ('report.export', 'report', 'Export reports'),
    ('report.activity_log', 'report', 'View the activity log'),
    ('user.view', 'user', 'View users'),
    ('user.create', 'user', 'Create users'),
    ('user.update', 'user', 'Edit users and reset their PINs'),
    ('user.delete', 'user', 'Delete users'),
    ('role.view', 'role', 'View roles & permissions'),
    ('role.create', 'role', 'Create roles'),
    ('role.update', 'role', 'Edit roles and their permissions'),
    ('role.delete', 'role', 'Delete roles'),
    ('branch.view', 'branch', 'View every branch and its tills'),
    ('branch.create', 'branch', 'Create branches and register tills'),
    ('branch.update', 'branch', 'Edit branches and tills'),
    ('branch.delete', 'branch', 'Delete branches and tills'),
    ('table.view', 'table', 'View dining tables'),
    ('table.create', 'table', 'Create dining tables'),
    ('table.update', 'table', 'Edit dining tables and their QR codes'),
    ('table.delete', 'table', 'Delete dining tables'),
    ('payment_method.create', 'payment_method', 'Create payment methods'),
    ('payment_method.update', 'payment_method', 'Edit payment methods'),
    ('payment_method.delete', 'payment_method', 'Delete payment methods'),
    ('settings.rate', 'settings', 'Change the exchange rate'),
    ('settings.company', 'settings', 'Change company, receipt and loyalty settings'),
    ('system.manage', 'system', 'Manage the company''s alerts, numbering and activity-log retention'),
    ('system.reset', 'system', 'Wipe all data for a fresh production setup')
ON DUPLICATE KEY UPDATE `group` = VALUES(`group`), description = VALUES(description);

CREATE TEMPORARY TABLE permission_map (old_key VARCHAR(60) NOT NULL, new_key VARCHAR(60) NOT NULL)
    DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
INSERT INTO permission_map (old_key, new_key) VALUES
    ('inventory.view', 'product.view'),
    ('inventory.view', 'category.view'),
    ('inventory.view', 'supplier.view'),
    ('inventory.view', 'purchase_order.view'),
    ('inventory.adjust', 'category.create'),
    ('inventory.adjust', 'category.update'),
    ('inventory.adjust', 'category.delete'),
    ('inventory.adjust', 'supplier.create'),
    ('inventory.adjust', 'supplier.update'),
    ('inventory.adjust', 'supplier.delete'),
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
    ('invoice.manage', 'invoice.view'),
    ('invoice.manage', 'invoice.create'),
    ('invoice.manage', 'invoice.update'),
    ('invoice.manage', 'invoice.delete'),
    ('report.sales', 'invoice.view'),
    ('expense.manage', 'expense.view'),
    ('expense.manage', 'expense.create'),
    ('expense.manage', 'expense.update'),
    ('expense.manage', 'expense.delete'),
    ('user.manage', 'user.view'),
    ('user.manage', 'user.create'),
    ('user.manage', 'user.update'),
    ('user.manage', 'user.delete'),
    ('role.manage', 'role.view'),
    ('role.manage', 'role.create'),
    ('role.manage', 'role.update'),
    ('role.manage', 'role.delete'),
    ('branch.manage', 'branch.view'),
    ('branch.manage', 'branch.create'),
    ('branch.manage', 'branch.update'),
    ('branch.manage', 'branch.delete'),
    ('branch.manage', 'table.view'),
    ('branch.manage', 'table.create'),
    ('branch.manage', 'table.update'),
    ('branch.manage', 'table.delete'),
    ('branch.manage', 'payment_method.create'),
    ('branch.manage', 'payment_method.update'),
    ('branch.manage', 'payment_method.delete'),
    ('branch.manage', 'settings.company');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions op ON op.id = rp.permission_id
JOIN permission_map m ON m.old_key = op.`key`
JOIN permissions np ON np.`key` = m.new_key;

DROP TEMPORARY TABLE permission_map;

-- Their grants go with them (ON DELETE CASCADE).
DELETE FROM permissions WHERE `key` IN ('inventory.edit_price', 'inventory.product_create', 'inventory.product_edit', 'inventory.purchase_order', 'inventory.receive', 'customer.manage', 'invoice.manage', 'expense.manage', 'user.manage', 'role.manage', 'branch.manage');
