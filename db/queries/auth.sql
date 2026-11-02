-- name: InsertLoginIP :exec
INSERT INTO login_ip (userid, ip, datetime, type) VALUES (?, ?, ?, ?);

-- name: CountUsersByIM :one
SELECT COUNT(*) FROM `user` WHERE im_value = ? AND im_type = ?;

-- name: GetUserByTelegramID :one
SELECT * FROM `user` WHERE telegram_id = ? LIMIT 1;

-- name: GetInviteCode :one
SELECT * FROM ss_invite_code WHERE code = ? LIMIT 1;

-- name: GrantInviteReward :execrows
-- 注册时给邀请人增加流量并扣减一次邀请次数（invite_num 为负数表示不限次数，原版同样会减 1）。
UPDATE `user` SET transfer_enable = transfer_enable + ?, invite_num = invite_num - 1
WHERE id = ? AND invite_num <> 0;

-- name: CountEmailVerifyByIP :one
SELECT COUNT(*) FROM email_verify WHERE ip = ? AND expire_in > ?;

-- name: CountEmailVerifyByEmail :one
SELECT COUNT(*) FROM email_verify WHERE email = ? AND expire_in > ?;

-- name: InsertEmailVerify :exec
INSERT INTO email_verify (email, ip, code, expire_in) VALUES (?, ?, ?, ?);

-- name: FindEmailVerify :one
SELECT * FROM email_verify WHERE email = ? AND code = ? AND expire_in > ? LIMIT 1;

-- name: DeleteEmailVerifyByEmail :exec
DELETE FROM email_verify WHERE email = ?;

-- name: InsertPasswordReset :exec
INSERT INTO ss_password_reset (email, token, init_time, expire_time) VALUES (?, ?, ?, ?);

-- name: GetPasswordReset :one
SELECT * FROM ss_password_reset WHERE token = ? ORDER BY id DESC LIMIT 1;

-- name: DeletePasswordResetByEmail :exec
DELETE FROM ss_password_reset WHERE email = ?;

-- name: ResetUserPassword :exec
-- 原版重置密码时同时关闭两步验证。
UPDATE `user` SET pass = ?, ga_enable = 0 WHERE id = ?;

-- name: DeleteLinksByUser :exec
DELETE FROM link WHERE userid = ?;

-- name: CountTelegramSessionToken :one
SELECT COUNT(*) FROM telegram_session WHERE session_content = ?;

-- name: CountTelegramLoginSessionConflict :one
SELECT COUNT(*) FROM telegram_session WHERE session_content LIKE CONCAT(sqlc.arg(token), '|%') OR session_content LIKE CONCAT('%|', sqlc.arg(number));

-- name: InsertTelegramSession :exec
INSERT INTO telegram_session (user_id, type, session_content, datetime) VALUES (?, ?, ?, ?);

-- name: GetTelegramLoginSession :one
SELECT * FROM telegram_session WHERE type = 1 AND session_content = ? ORDER BY datetime DESC LIMIT 1;

-- name: DeleteTelegramSession :exec
DELETE FROM telegram_session WHERE id = ?;

-- name: TakeTelegramBindSession :one
-- 机器人收到绑定二维码：10 分钟内有效，取出后删除（一次性）。
SELECT * FROM telegram_session WHERE type = 0 AND session_content = ? AND `datetime` > ? ORDER BY `datetime` DESC LIMIT 1 FOR UPDATE;

-- name: UnbindTelegramID :exec
UPDATE `user` SET telegram_id = 0 WHERE telegram_id = ?;

-- name: BindTelegram :exec
UPDATE `user` SET telegram_id = ?, im_type = 4, im_value = ? WHERE id = ?;

-- name: ConfirmTelegramLoginByToken :execrows
-- 机器人收到登录二维码（90 秒内有效）：把登录会话标记为该用户。
UPDATE telegram_session SET user_id = sqlc.arg(user_id)
WHERE type = 1 AND user_id = 0 AND session_content LIKE CONCAT(sqlc.arg(token), '|%') AND `datetime` > sqlc.arg(since)
ORDER BY `datetime` DESC LIMIT 1;

-- name: ConfirmTelegramLoginByNumber :execrows
UPDATE telegram_session SET user_id = sqlc.arg(user_id)
WHERE type = 1 AND user_id = 0 AND session_content LIKE CONCAT('%|', sqlc.arg(number)) AND `datetime` > sqlc.arg(since)
ORDER BY `datetime` DESC LIMIT 1;
