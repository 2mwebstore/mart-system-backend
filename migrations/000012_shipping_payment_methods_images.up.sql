ALTER TABLE purchase_orders
    ADD COLUMN shipping_cents BIGINT NOT NULL DEFAULT 0 AFTER note;

-- Product images can be an https link or (until R2 upload lands) an inline
-- data URL, which is far longer than 500 chars.
ALTER TABLE products
    MODIFY COLUMN image_url MEDIUMTEXT NOT NULL;

CREATE TABLE payment_methods (
    id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name         VARCHAR(80)  NOT NULL,
    type         VARCHAR(10)  NOT NULL,
    fee_percent  DECIMAL(5,2) NOT NULL DEFAULT 0,
    enabled      TINYINT(1)   NOT NULL DEFAULT 1,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at   DATETIME NULL,
    KEY idx_payment_methods_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
