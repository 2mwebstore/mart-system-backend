-- The most active branches and active user accounts a company may have, set
-- by the super admin. NULL is no limit, which is every company until one is set.
ALTER TABLE companies
    ADD COLUMN max_branches INT UNSIGNED NULL AFTER active,
    ADD COLUMN max_users INT UNSIGNED NULL AFTER max_branches;
