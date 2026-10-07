-- Product image URLs pasted into the product form kept leading/trailing
-- whitespace (a tab, in practice). The form now trims; clean existing rows.
UPDATE products
SET image_url = REGEXP_REPLACE(image_url, '^[[:space:]]+|[[:space:]]+$', '')
WHERE image_url REGEXP '^[[:space:]]|[[:space:]]$';
