DELETE FROM settings WHERE `key` = 'company_logo';
ALTER TABLE settings MODIFY value TEXT NOT NULL;
