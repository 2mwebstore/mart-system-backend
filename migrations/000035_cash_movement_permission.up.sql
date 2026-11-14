-- Cash pay-ins and pay-outs at the till get a permission of their own: until
-- now anyone who could run their own shift could record them. Every role that
-- runs a shift today keeps them (each role holding shift.own, every Owner
-- among them, gets the new key); a company can then take it from a role.
INSERT IGNORE INTO permissions (`key`, `group`, description) VALUES
    ('pos.cash_movement', 'pos', 'Record cash pay-ins and pay-outs');

INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT DISTINCT rp.role_id, np.id
FROM role_permissions rp
JOIN permissions op ON op.id = rp.permission_id AND op.`key` = 'shift.own'
JOIN permissions np ON np.`key` = 'pos.cash_movement';
