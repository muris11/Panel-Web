-- Dovecot and Postfix read these views instead of the tables: a domain or a
-- mailbox disappears from the mail server the moment its website or account is
-- not active, without the panel having to write anything.
CREATE OR REPLACE VIEW `v_mail_domains` AS
SELECT d.`name` AS `name`
FROM `mail_domains` md
JOIN `domains` d ON d.`id` = md.`domain_id`
JOIN `users` u ON u.`id` = d.`user_id`
WHERE md.`status` = 'active'
  AND u.`status` = 'active'
  AND u.`role` = 'user'
  AND u.`uid` IS NOT NULL
  AND u.`gid` IS NOT NULL;

CREATE OR REPLACE VIEW `v_mail_mailboxes` AS
SELECT
    CONCAT(m.`local_part`, '@', d.`name`) AS `address`,
    m.`password_hash` AS `password`,
    u.`uid` AS `uid`,
    u.`gid` AS `gid`,
    CONCAT('/home/', u.`username`, '/imap/', d.`name`, '/', m.`local_part`) AS `home`,
    CASE WHEN m.`quota_mb` IS NULL THEN NULL ELSE CONCAT('*:storage=', m.`quota_mb`, 'M') END AS `quota_rule`
FROM `mailboxes` m
JOIN `mail_domains` md ON md.`id` = m.`mail_domain_id`
JOIN `domains` d ON d.`id` = md.`domain_id`
JOIN `users` u ON u.`id` = d.`user_id`
WHERE m.`status` = 'active'
  AND md.`status` = 'active'
  AND u.`status` = 'active'
  AND u.`role` = 'user'
  AND u.`uid` IS NOT NULL
  AND u.`gid` IS NOT NULL;
