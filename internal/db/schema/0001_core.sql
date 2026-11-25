-- Core tables the panel needs, with the same names and columns the Laravel
-- panel uses for them, so the Go panel can be pointed at an existing MURISCORE
-- database and keep the same accounts, packages and websites.
--
-- Every statement is IF NOT EXISTS: these migrations are layered onto an
-- existing schema instead of replacing it, the same way a later MURISCORE
-- release adds a table.
--
-- Identifiers are all backticked. MySQL 8 reserves several of these words
-- (`databases`, `ssl`, `role`, `status`), so unquoted they parse as syntax
-- rather than as column names.

CREATE TABLE IF NOT EXISTS `users` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `username` VARCHAR(16) NOT NULL,
    `uid` INT UNSIGNED NULL,
    `gid` INT UNSIGNED NULL,
    `name` VARCHAR(255) NOT NULL,
    `email` VARCHAR(255) NOT NULL,
    `email_verified_at` TIMESTAMP NULL,
    `password` VARCHAR(255) NOT NULL,
    `two_factor_secret` TEXT NULL,
    `two_factor_recovery_codes` TEXT NULL,
    `two_factor_confirmed_at` TIMESTAMP NULL,
    `role` VARCHAR(16) NOT NULL DEFAULT 'user',
    `owner_id` BIGINT UNSIGNED NULL,
    `package_id` BIGINT UNSIGNED NULL,
    `reseller_package_id` BIGINT UNSIGNED NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'active',
    `phpmyadmin_expires_at` TIMESTAMP NULL,
    `sftp_password_set_at` TIMESTAMP NULL,
    `cron_email` VARCHAR(254) NULL,
    `suspend_reason` VARCHAR(255) NULL,
    `bandwidth_suspended_at` TIMESTAMP NULL,
    `suspended_by` VARCHAR(255) NULL,
    `ssh_enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `ipv4_address_id` BIGINT UNSIGNED NULL,
    `ipv6_address_id` BIGINT UNSIGNED NULL,
    `last_login_at` TIMESTAMP NULL,
    `remember_token` VARCHAR(100) NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `users_username_unique` (`username`),
    UNIQUE KEY `users_uid_unique` (`uid`),
    KEY `users_email_index` (`email`),
    KEY `users_role_index` (`role`),
    KEY `users_status_index` (`status`),
    KEY `users_owner_id_index` (`owner_id`),
    KEY `users_package_id_index` (`package_id`),
    CONSTRAINT `users_owner_id_foreign` FOREIGN KEY (`owner_id`) REFERENCES `users` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `packages` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `owner_id` BIGINT UNSIGNED NULL,
    `name` VARCHAR(64) NOT NULL,
    `description` VARCHAR(255) NULL,
    `disk_mb` INT UNSIGNED NULL,
    `bandwidth_mb` INT UNSIGNED NULL,
    `domains` INT UNSIGNED NULL,
    `subdomains` INT UNSIGNED NULL,
    `pointers` INT UNSIGNED NULL,
    `databases` INT UNSIGNED NULL,
    `email_accounts` INT UNSIGNED NULL,
    `email_forwarders` INT UNSIGNED NULL,
    `ftp_accounts` INT UNSIGNED NULL,
    `cron_jobs` INT UNSIGNED NULL,
    `inodes` INT UNSIGNED NULL,
    `ssl` TINYINT(1) NOT NULL DEFAULT 1,
    `cgi` TINYINT(1) NOT NULL DEFAULT 0,
    `ssh` TINYINT(1) NOT NULL DEFAULT 0,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `packages_owner_id_name_unique` (`owner_id`, `name`),
    CONSTRAINT `packages_owner_id_foreign` FOREIGN KEY (`owner_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `domains` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `parent_id` BIGINT UNSIGNED NULL,
    `name` VARCHAR(253) NOT NULL,
    `type` VARCHAR(16) NOT NULL DEFAULT 'domain',
    `pointer_mode` VARCHAR(16) NULL,
    `php_version` VARCHAR(8) NOT NULL DEFAULT '8.4',
    `php_enabled` TINYINT(1) NOT NULL DEFAULT 1,
    `is_primary` TINYINT(1) NOT NULL DEFAULT 0,
    `document_root` VARCHAR(200) NULL,
    `www_mode` VARCHAR(8) NOT NULL DEFAULT 'both',
    `cgi_enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `status` VARCHAR(16) NOT NULL DEFAULT 'pending',
    `ssl_status` VARCHAR(16) NULL,
    `ssl_source` VARCHAR(16) NULL,
    `ssl_names` JSON NULL,
    `ssl_expires_at` TIMESTAMP NULL,
    `ssl_error` TEXT NULL,
    `force_https` TINYINT(1) NOT NULL DEFAULT 0,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `domains_name_unique` (`name`),
    KEY `domains_user_id_index` (`user_id`),
    KEY `domains_status_index` (`status`),
    KEY `domains_parent_id_index` (`parent_id`),
    CONSTRAINT `domains_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `domains_parent_id_foreign` FOREIGN KEY (`parent_id`) REFERENCES `domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- One row per panel sign-in. The token itself is never stored: only its
-- SHA-256, so a database leak cannot be replayed as a session.
CREATE TABLE IF NOT EXISTS `panel_sessions` (
    `id` CHAR(36) NOT NULL,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `token_hash` CHAR(64) NOT NULL,
    `csrf_token` CHAR(64) NOT NULL,
    `ip_address` VARCHAR(45) NULL,
    `user_agent` VARCHAR(255) NULL,
    `created_at` TIMESTAMP NOT NULL,
    `last_seen_at` TIMESTAMP NOT NULL,
    `expires_at` TIMESTAMP NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `panel_sessions_token_hash_unique` (`token_hash`),
    KEY `panel_sessions_user_id_index` (`user_id`),
    KEY `panel_sessions_expires_at_index` (`expires_at`),
    CONSTRAINT `panel_sessions_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Work handed to muriscored. Same shape as the Laravel panel's tasks table; the
-- Go panel writes the payload as plain JSON, the Laravel panel encrypted it,
-- so only one panel should be writing tasks at a time.
CREATE TABLE IF NOT EXISTS `tasks` (
    `id` CHAR(36) NOT NULL,
    `user_id` BIGINT UNSIGNED NULL,
    `actor_id` BIGINT UNSIGNED NULL,
    `parent_id` CHAR(36) NULL,
    `action` VARCHAR(64) NOT NULL,
    `payload` LONGTEXT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'queued',
    `progress` TINYINT UNSIGNED NOT NULL DEFAULT 0,
    `attempts` SMALLINT UNSIGNED NOT NULL DEFAULT 0,
    `output` JSON NULL,
    `error` TEXT NULL,
    `started_at` TIMESTAMP NULL,
    `finished_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    KEY `tasks_status_index` (`status`),
    KEY `tasks_user_created_index` (`user_id`, `created_at`),
    CONSTRAINT `tasks_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `audit_logs` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `actor_id` BIGINT UNSIGNED NULL,
    `impersonator_id` BIGINT UNSIGNED NULL,
    `action` VARCHAR(64) NOT NULL,
    `subject_type` VARCHAR(255) NULL,
    `subject_id` VARCHAR(36) NULL,
    `ip_address` VARCHAR(45) NULL,
    `user_agent` VARCHAR(255) NULL,
    `changes` JSON NULL,
    `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    KEY `audit_logs_action_index` (`action`),
    KEY `audit_logs_subject_index` (`subject_type`, `subject_id`),
    KEY `audit_logs_created_at_index` (`created_at`),
    CONSTRAINT `audit_logs_actor_id_foreign` FOREIGN KEY (`actor_id`) REFERENCES `users` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
