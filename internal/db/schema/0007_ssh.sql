-- The public keys an account added in the panel, which the agent writes to its
-- ~/.ssh/authorized_keys. Same columns as the Laravel panel, so both can read
-- this table, and CREATE TABLE IF NOT EXISTS so a shared database that already
-- has it is left alone.
CREATE TABLE IF NOT EXISTS `ssh_keys` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id` BIGINT UNSIGNED NOT NULL,
    `name` VARCHAR(100) NOT NULL,
    `public_key` TEXT NOT NULL,
    `fingerprint` VARCHAR(64) NOT NULL,
    `created_at` TIMESTAMP NULL DEFAULT NULL,
    `updated_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `ssh_keys_user_id_fingerprint_unique` (`user_id`, `fingerprint`),
    CONSTRAINT `ssh_keys_user_id_foreign` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
