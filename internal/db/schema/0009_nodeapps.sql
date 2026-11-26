-- Node.js applications: one per domain, reverse-proxied by nginx. Column for
-- column with the Laravel panel's node_apps table so both panels can read the
-- same database. The env column is plaintext JSON here; the Laravel panel
-- stores it encrypted, so a database written by this panel reads as garbage
-- there and the other way round -- a documented difference, not an accident.
CREATE TABLE IF NOT EXISTS node_apps (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    domain_id BIGINT UNSIGNED NOT NULL,
    entry VARCHAR(255) NOT NULL,
    env TEXT NULL,
    port INT UNSIGNED NOT NULL,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    status VARCHAR(255) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NULL DEFAULT NULL,
    updated_at TIMESTAMP NULL DEFAULT NULL,
    UNIQUE KEY node_apps_domain_id_unique (domain_id),
    UNIQUE KEY node_apps_port_unique (port),
    CONSTRAINT node_apps_domain_id_foreign FOREIGN KEY (domain_id) REFERENCES domains (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
