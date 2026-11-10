ALTER TABLE roles
    DROP FOREIGN KEY fk_roles_branch,
    DROP KEY idx_roles_branch_id,
    DROP COLUMN branch_id;
