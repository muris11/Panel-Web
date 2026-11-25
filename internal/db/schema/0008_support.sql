-- Reseller packages, tickets and announcements.
--
-- Column for column the same as the Laravel migrations so both panels can read
-- one database. Every statement is IF NOT EXISTS: on an existing install this
-- file changes nothing.

CREATE TABLE IF NOT EXISTS `reseller_packages` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `name` VARCHAR(64) NOT NULL,
    `description` VARCHAR(255) NULL,
    `accounts` INT UNSIGNED NULL,
    `disk_mb` INT UNSIGNED NULL,
    `inodes` INT UNSIGNED NULL,
    `bandwidth_mb` INT UNSIGNED NULL,
    `domains` INT UNSIGNED NULL,
    `subdomains` INT UNSIGNED NULL,
    `pointers` INT UNSIGNED NULL,
    `email_accounts` INT UNSIGNED NULL,
    `email_forwarders` INT UNSIGNED NULL,
    `databases` INT UNSIGNED NULL,
    `ftp_accounts` INT UNSIGNED NULL,
    `cron_jobs` INT UNSIGNED NULL,
    `ssl` TINYINT(1) NOT NULL DEFAULT 1,
    `oversell` TINYINT(1) NOT NULL DEFAULT 0,
    `cpu_percent` INT UNSIGNED NULL,
    `memory_mb` INT UNSIGNED NULL,
    `max_processes` INT UNSIGNED NULL,
    `io_weight` SMALLINT UNSIGNED NULL,
    `ssh` TINYINT(1) NOT NULL DEFAULT 0,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `reseller_packages_name_unique` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `tickets` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `reseller_id` BIGINT UNSIGNED NULL,
    `subject` VARCHAR(150) NOT NULL,
    `status` VARCHAR(16) NOT NULL DEFAULT 'open',
    `priority` VARCHAR(16) NOT NULL DEFAULT 'normal',
    `last_message_at` TIMESTAMP NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    KEY `tickets_status_index` (`status`),
    KEY `tickets_last_message_at_index` (`last_message_at`),
    CONSTRAINT `tickets_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE,
    CONSTRAINT `tickets_reseller_id_foreign` FOREIGN KEY (`reseller_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `ticket_messages` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `ticket_id` BIGINT UNSIGNED NOT NULL,
    `author_id` BIGINT UNSIGNED NULL,
    `body` TEXT NOT NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `ticket_messages_ticket_id_foreign` FOREIGN KEY (`ticket_id`) REFERENCES `tickets` (`id`) ON DELETE CASCADE,
    CONSTRAINT `ticket_messages_author_id_foreign` FOREIGN KEY (`author_id`) REFERENCES `users` (`id`) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `announcements` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `sender_id` BIGINT UNSIGNED NOT NULL,
    `audience` JSON NOT NULL,
    `subject` VARCHAR(150) NOT NULL,
    `body` TEXT NOT NULL,
    `emailed` TINYINT(1) NOT NULL DEFAULT 0,
    `recipient_count` INT UNSIGNED NOT NULL DEFAULT 0,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT `announcements_sender_id_foreign` FOREIGN KEY (`sender_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
