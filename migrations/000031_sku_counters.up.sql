-- Each company numbers its products' SKUs on its own, per prefix: a
-- company's first product in a "Drinks" category is DRI-001, whatever other
-- companies have. A SKU used to be the prefix and the product's row id, which
-- every company shared.
--
-- A row appears the first time a company uses a prefix, starting after the
-- highest SKU it already has under that prefix (see nextSKU in catalog.go),
-- so existing SKUs are never handed out again.
CREATE TABLE sku_counters (
    company_id   BIGINT UNSIGNED NOT NULL,
    prefix       VARCHAR(10) NOT NULL,
    last_number  INT UNSIGNED NOT NULL,
    PRIMARY KEY (company_id, prefix),
    CONSTRAINT fk_sku_counters_company FOREIGN KEY (company_id) REFERENCES companies (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
