-- A role belongs to one branch, or is shared by every branch (NULL). Every
-- role that already exists is shared.
ALTER TABLE roles
    ADD COLUMN branch_id BIGINT UNSIGNED NULL AFTER description,
    ADD KEY idx_roles_branch_id (branch_id),
    ADD CONSTRAINT fk_roles_branch FOREIGN KEY (branch_id) REFERENCES branches (id);
