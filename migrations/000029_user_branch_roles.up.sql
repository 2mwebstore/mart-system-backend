-- The role a user holds at this branch when it isn't their main role
-- (users.role_id). NULL means the main role applies here.
ALTER TABLE user_branches
    ADD COLUMN role_id BIGINT UNSIGNED NULL AFTER branch_id,
    ADD KEY idx_user_branches_role_id (role_id),
    ADD CONSTRAINT fk_user_branches_role FOREIGN KEY (role_id) REFERENCES roles (id);
