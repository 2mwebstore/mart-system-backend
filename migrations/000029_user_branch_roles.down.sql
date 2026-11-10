ALTER TABLE user_branches
    DROP FOREIGN KEY fk_user_branches_role,
    DROP KEY idx_user_branches_role_id,
    DROP COLUMN role_id;
