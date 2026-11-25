-- vsftpd and ProFTPD read the virtual login list from this view, not from the
-- table: the view only shows logins whose account is active and has a uid, so
-- suspending an account also closes its FTP. The Laravel panel creates the
-- same view; this one adds nothing to it.
CREATE OR REPLACE VIEW `v_ftp_users` AS
SELECT
    f.`login` AS `login`,
    f.`password_hash` AS `password`,
    u.`uid` AS `uid`,
    u.`gid` AS `gid`,
    CONCAT('/home/', u.`username`, CASE WHEN f.`directory` = '' THEN '' ELSE CONCAT('/', f.`directory`) END) AS `dir`,
    f.`quota_mb` AS `quota_size`
FROM `ftp_accounts` f
JOIN `users` u ON u.`id` = f.`user_id`
WHERE f.`status` = 'active'
  AND u.`status` = 'active'
  AND u.`role` = 'user'
  AND u.`uid` IS NOT NULL
  AND u.`gid` IS NOT NULL;
