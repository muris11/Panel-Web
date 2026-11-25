-- What the collectors measure, in the same shape the Laravel panel wrote, so
-- either panel can read the rows.

-- Key/value settings, including where the last bandwidth count stopped.
CREATE TABLE IF NOT EXISTS `settings` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `key` VARCHAR(100) NOT NULL,
    `value` JSON NOT NULL,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `settings_key_unique` (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Traffic per hosting account per day, split by where it moved.
CREATE TABLE IF NOT EXISTS `bandwidth_usages` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `date` DATE NOT NULL,
    `web_bytes` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `mail_bytes` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `ftp_bytes` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY `bandwidth_usages_user_id_date_unique` (`user_id`, `date`),
    KEY `bandwidth_usages_date_index` (`date`),
    CONSTRAINT `bandwidth_usages_user_id_foreign` FOREIGN KEY (`user_id`)
        REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Traffic per website per day.
CREATE TABLE IF NOT EXISTS `domain_bandwidths` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `domain_id` BIGINT UNSIGNED NOT NULL,
    `date` DATE NOT NULL,
    `bytes` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY `domain_bandwidths_domain_id_date_unique` (`domain_id`, `date`),
    KEY `domain_bandwidths_date_index` (`date`),
    CONSTRAINT `domain_bandwidths_domain_id_foreign` FOREIGN KEY (`domain_id`)
        REFERENCES `domains` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- CPU and peak memory per hosting account per day.
CREATE TABLE IF NOT EXISTS `resource_usages` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `date` DATE NOT NULL,
    `cpu_ms` BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `memory_peak_mb` INT UNSIGNED NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY `resource_usages_user_id_date_unique` (`user_id`, `date`),
    KEY `resource_usages_date_index` (`date`),
    CONSTRAINT `resource_usages_user_id_foreign` FOREIGN KEY (`user_id`)
        REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- The CPU count each cgroup was at the last time it was read. A restarted
-- cgroup gets a new id and starts at zero, so the difference is only taken
-- when the id matches.
CREATE TABLE IF NOT EXISTS `resource_counters` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `cgroup_id` BIGINT UNSIGNED NOT NULL,
    `cpu_usec` BIGINT UNSIGNED NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `resource_counters_user_id_cgroup_id_unique` (`user_id`, `cgroup_id`),
    CONSTRAINT `resource_counters_user_id_foreign` FOREIGN KEY (`user_id`)
        REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- One row per collector run, so the panel can say when it last worked
-- instead of failing silently in a log nobody reads.
CREATE TABLE IF NOT EXISTS `collector_runs` (
    `job` VARCHAR(40) NOT NULL,
    `started_at` TIMESTAMP NULL,
    `finished_at` TIMESTAMP NULL,
    `ok` TINYINT(1) NOT NULL DEFAULT 0,
    `detail` VARCHAR(255) NULL,
    PRIMARY KEY (`job`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
