-- Redis: one cache instance per hosting account.
--
-- The password is stored the way Laravel's model stores it: encrypted with the
-- panel APP_KEY when one is configured. The agent needs the plain text, so the
-- panel decrypts on the way out and never sends it to a browser again.
--
-- There is deliberately no status column: the table only exists once the
-- account asked for a cache, and "enabled" is the only state the agent cares
-- about. Rows created by the Laravel panel are therefore readable as-is.
CREATE TABLE IF NOT EXISTS `redis_instances` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `password` TEXT NOT NULL,
    `max_memory_mb` INT UNSIGNED NOT NULL DEFAULT 256,
    `enabled` TINYINT(1) NOT NULL DEFAULT 1,
    `created_at` TIMESTAMP NULL,
    `updated_at` TIMESTAMP NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `redis_instances_user_id_unique` (`user_id`),
    CONSTRAINT `redis_instances_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
