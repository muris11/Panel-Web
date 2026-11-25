-- The tables the rest of the panel needs: DNS, mail, databases, FTP, cron,
-- backups and API tokens. Column names and types follow the Laravel panel's
-- migrations so both panels read the same rows; where Laravel keeps a column
-- this panel does not use yet (import_status, modsecurity_mode …) it is left
-- out rather than guessed at.
--
-- A MySQL foreign key needs its parent index, which the core migration already
-- creates, so nothing here has to think about order beyond parents first.

-- ---------------------------------------------------------------------- DNS
-- One zone per domain and pointer. A subdomain's records live in its parent's
-- zone, which is why dns_records points at a zone and not at a user.
CREATE TABLE IF NOT EXISTS `dns_zones` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `domain_id` BIGINT UNSIGNED NULL,
    `name` VARCHAR(253) NOT NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `dns_zones_name_unique` (`name`),
    KEY `dns_zones_user_id_index` (`user_id`),
    CONSTRAINT `dns_zones_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `dns_zones_domain_id_foreign` FOREIGN KEY (`domain_id`) REFERENCES `domains` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `dns_records` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `dns_zone_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(253) NOT NULL,
    `type` VARCHAR(10) NOT NULL,
    `content` TEXT NOT NULL,
    `ttl` INT UNSIGNED NOT NULL DEFAULT 3600,
    `priority` SMALLINT UNSIGNED NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    KEY `dns_records_dns_zone_id_name_index` (`dns_zone_id`, `name`),
    CONSTRAINT `dns_records_dns_zone_id_foreign` FOREIGN KEY (`dns_zone_id`) REFERENCES `dns_zones` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- --------------------------------------------------------------------- mail
CREATE TABLE IF NOT EXISTS `mail_domains` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `domain_id` BIGINT UNSIGNED NOT NULL,
    `dkim_selector` VARCHAR(63) NOT NULL DEFAULT 'rayxo',
    `dkim_public_key` TEXT NULL,
    `catchall_target` VARCHAR(254) NULL,
    `mode` VARCHAR(8) NOT NULL DEFAULT 'local',
    `provider` VARCHAR(16) NULL,
    `ssl_status` VARCHAR(16) NULL,
    `ssl_error` VARCHAR(500) NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `mail_domains_domain_id_unique` (`domain_id`),
    CONSTRAINT `mail_domains_domain_id_foreign` FOREIGN KEY (`domain_id`) REFERENCES `domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `mailboxes` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `mail_domain_id` BIGINT UNSIGNED NOT NULL,
    `local_part` VARCHAR(64) NOT NULL,
    `password_hash` VARCHAR(255) NOT NULL,
    `quota_mb` INT UNSIGNED NULL,
    `daily_send_limit` INT UNSIGNED NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'active',
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `mailboxes_mail_domain_id_local_part_unique` (`mail_domain_id`, `local_part`),
    CONSTRAINT `mailboxes_mail_domain_id_foreign` FOREIGN KEY (`mail_domain_id`) REFERENCES `mail_domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `mail_forwarders` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `mail_domain_id` BIGINT UNSIGNED NOT NULL,
    `local_part` VARCHAR(64) NOT NULL,
    `destinations` TEXT NOT NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `mail_forwarders_mail_domain_id_local_part_unique` (`mail_domain_id`, `local_part`),
    CONSTRAINT `mail_forwarders_mail_domain_id_foreign` FOREIGN KEY (`mail_domain_id`) REFERENCES `mail_domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------- databases
CREATE TABLE IF NOT EXISTS `databases` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `size_bytes` BIGINT UNSIGNED NULL,
    `measured_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `databases_name_unique` (`name`),
    KEY `databases_user_id_index` (`user_id`),
    CONSTRAINT `databases_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- password_hash is MySQL's own 41-byte native hash ("*" plus 40 hex digits),
-- not bcrypt: the agent hands it straight to CREATE USER.
CREATE TABLE IF NOT EXISTS `database_users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(32) NOT NULL,
    `password_hash` VARCHAR(41) NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `database_users_name_unique` (`name`),
    KEY `database_users_user_id_index` (`user_id`),
    CONSTRAINT `database_users_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `database_database_user` (
    `database_id` BIGINT UNSIGNED NOT NULL,
    `database_user_id` BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (`database_id`, `database_user_id`),
    KEY `database_database_user_database_user_id_index` (`database_user_id`),
    CONSTRAINT `database_database_user_database_id_foreign` FOREIGN KEY (`database_id`) REFERENCES `databases` (`id`) ON DELETE CASCADE,
    CONSTRAINT `database_database_user_user_id_foreign` FOREIGN KEY (`database_user_id`) REFERENCES `database_users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `database_remote_hosts` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `database_user_id` BIGINT UNSIGNED NOT NULL,
    `host` VARCHAR(64) NOT NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `database_remote_hosts_user_host_unique` (`database_user_id`, `host`),
    CONSTRAINT `database_remote_hosts_user_id_foreign` FOREIGN KEY (`database_user_id`) REFERENCES `database_users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `postgres_databases` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(63) NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `size_bytes` BIGINT UNSIGNED NULL,
    `measured_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `postgres_databases_name_unique` (`name`),
    KEY `postgres_databases_user_id_index` (`user_id`),
    CONSTRAINT `postgres_databases_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- PostgreSQL cannot take a pre-hashed password, so this one keeps the password
-- itself; it is never shown again after the account is made.
CREATE TABLE IF NOT EXISTS `postgres_users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(63) NOT NULL,
    `password` TEXT NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `postgres_users_name_unique` (`name`),
    KEY `postgres_users_user_id_index` (`user_id`),
    CONSTRAINT `postgres_users_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `postgres_database_postgres_user` (
    `postgres_database_id` BIGINT UNSIGNED NOT NULL,
    `postgres_user_id` BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (`postgres_database_id`, `postgres_user_id`),
    KEY `postgres_db_user_user_id_index` (`postgres_user_id`),
    CONSTRAINT `postgres_db_user_database_id_foreign` FOREIGN KEY (`postgres_database_id`) REFERENCES `postgres_databases` (`id`) ON DELETE CASCADE,
    CONSTRAINT `postgres_db_user_user_id_foreign` FOREIGN KEY (`postgres_user_id`) REFERENCES `postgres_users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------------- FTP
-- The login is "name@domain"; password_hash is the same crypt(3) form vsftpd
-- reads, so the panel writes it and the agent only has to place the file.
CREATE TABLE IF NOT EXISTS `ftp_accounts` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `domain_id` BIGINT UNSIGNED NOT NULL,
    `login` VARCHAR(128) NOT NULL,
    `password_hash` VARCHAR(255) NOT NULL,
    `directory` VARCHAR(200) NOT NULL DEFAULT '',
    `quota_mb` INT UNSIGNED NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `ftp_accounts_login_unique` (`login`),
    KEY `ftp_accounts_user_id_index` (`user_id`),
    CONSTRAINT `ftp_accounts_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `ftp_accounts_domain_id_foreign` FOREIGN KEY (`domain_id`) REFERENCES `domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- --------------------------------------------------------------------- cron
CREATE TABLE IF NOT EXISTS `cron_jobs` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `schedule` VARCHAR(100) NOT NULL,
    `command` VARCHAR(1024) NOT NULL,
    `description` VARCHAR(100) NULL,
    `enabled` TINYINT(1) NOT NULL DEFAULT 1,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    KEY `cron_jobs_user_id_index` (`user_id`),
    CONSTRAINT `cron_jobs_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ------------------------------------------------------------------ backups
CREATE TABLE IF NOT EXISTS `backup_runs` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `trigger` VARCHAR(16) NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'running',
    `accounts` INT UNSIGNED NOT NULL DEFAULT 0,
    `succeeded` INT UNSIGNED NOT NULL DEFAULT 0,
    `failed` INT UNSIGNED NOT NULL DEFAULT 0,
    `message` TEXT NULL,
    `started_at` TIMESTAMP NULL,
    `finished_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    KEY `backup_runs_status_index` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ---------------------------------------------------------------- API tokens
-- For the billing system: a token signs a request as the user that owns it,
-- so a WHMCS install can create accounts without a password or a session.
CREATE TABLE IF NOT EXISTS `api_tokens` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `token_hash` CHAR(64) NOT NULL,
    `last_used_at` TIMESTAMP NULL,
    `expires_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `api_tokens_token_hash_unique` (`token_hash`),
    KEY `api_tokens_user_id_index` (`user_id`),
    CONSTRAINT `api_tokens_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
