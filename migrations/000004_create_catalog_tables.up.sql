CREATE TABLE categories (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name_en     VARCHAR(100) NOT NULL,
    name_km     VARCHAR(100) NOT NULL DEFAULT '',
    sort        INT NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME NULL,
    KEY idx_categories_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE suppliers (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name            VARCHAR(150) NOT NULL,
    phone           VARCHAR(30)  NOT NULL DEFAULT '',
    contact         VARCHAR(150) NOT NULL DEFAULT '',
    payment_terms   VARCHAR(150) NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at      DATETIME NULL,
    KEY idx_suppliers_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE products (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sku           VARCHAR(60)  NOT NULL,
    barcode       VARCHAR(60)  NULL,
    name_en       VARCHAR(150) NOT NULL,
    name_km       VARCHAR(150) NOT NULL DEFAULT '',
    category_id   BIGINT UNSIGNED NULL,
    supplier_id   BIGINT UNSIGNED NULL,
    unit          VARCHAR(30)  NOT NULL DEFAULT 'pcs',
    image_url     VARCHAR(500) NOT NULL DEFAULT '',
    active        TINYINT(1)   NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at    DATETIME NULL,
    UNIQUE KEY uk_products_sku (sku),
    KEY idx_products_barcode (barcode),
    KEY idx_products_category_id (category_id),
    KEY idx_products_supplier_id (supplier_id),
    KEY idx_products_deleted_at (deleted_at),
    CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories (id),
    CONSTRAINT fk_products_supplier FOREIGN KEY (supplier_id) REFERENCES suppliers (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE product_prices (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    product_id    BIGINT UNSIGNED NOT NULL,
    branch_id     BIGINT UNSIGNED NULL,
    price_cents   BIGINT NOT NULL,
    cost_cents    BIGINT NOT NULL,
    effective_at  DATETIME NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_product_prices_product_id (product_id),
    KEY idx_product_prices_branch_id (branch_id),
    KEY idx_product_prices_effective_at (effective_at),
    CONSTRAINT fk_product_prices_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE,
    CONSTRAINT fk_product_prices_branch FOREIGN KEY (branch_id) REFERENCES branches (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE branch_stock (
    branch_id       BIGINT UNSIGNED NOT NULL,
    product_id      BIGINT UNSIGNED NOT NULL,
    qty             BIGINT NOT NULL DEFAULT 0,
    reorder_point   BIGINT NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (branch_id, product_id),
    KEY idx_branch_stock_product_id (product_id),
    CONSTRAINT fk_branch_stock_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_branch_stock_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_movements (
    id                BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    branch_id         BIGINT UNSIGNED NOT NULL,
    product_id        BIGINT UNSIGNED NOT NULL,
    type              VARCHAR(20) NOT NULL,
    qty_change        BIGINT NOT NULL,
    balance_after     BIGINT NOT NULL,
    unit_cost_cents   BIGINT NOT NULL,
    reference_type    VARCHAR(40) NOT NULL DEFAULT '',
    reference_id      BIGINT UNSIGNED NULL,
    user_id           BIGINT UNSIGNED NOT NULL,
    note              VARCHAR(255) NOT NULL DEFAULT '',
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    KEY idx_stock_movements_branch_product (branch_id, product_id),
    KEY idx_stock_movements_type (type),
    KEY idx_stock_movements_user_id (user_id),
    KEY idx_stock_movements_created_at (created_at),
    CONSTRAINT fk_stock_movements_branch FOREIGN KEY (branch_id) REFERENCES branches (id),
    CONSTRAINT fk_stock_movements_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT fk_stock_movements_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
