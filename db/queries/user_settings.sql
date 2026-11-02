-- 用户资料设置（原版 UserController 的 update* 系列方法）。

-- name: UpdateUserIM :exec
UPDATE `user` SET im_type = ?, im_value = ? WHERE id = ?;

-- name: UpdateUserSSR :exec
UPDATE `user` SET protocol = ?, obfs = ?, obfs_param = ? WHERE id = ?;

-- name: UpdateUserMethod :exec
UPDATE `user` SET method = ? WHERE id = ?;

-- name: UpdateUserPasswd :exec
UPDATE `user` SET passwd = ? WHERE id = ?;

-- name: UpdateUserTheme :exec
UPDATE `user` SET theme = ? WHERE id = ?;

-- name: UpdateUserDailyMail :exec
UPDATE `user` SET sendDailyMail = ? WHERE id = ?;

-- name: UpdateUserPac :exec
UPDATE `user` SET pac = ? WHERE id = ?;

-- name: UpdateUserHide :exec
UPDATE `user` SET is_hide = ? WHERE id = ?;

-- name: UpdateUserGaEnable :exec
UPDATE `user` SET ga_enable = ? WHERE id = ?;

-- name: UpdateUserGaToken :exec
UPDATE `user` SET ga_token = ? WHERE id = ?;

-- name: ResetUserTelegram :exec
UPDATE `user` SET telegram_id = 0 WHERE id = ?;

-- name: ResetUserDiscord :exec
UPDATE `user` SET discord = 0 WHERE id = ?;

-- name: DeductUserMoney :execrows
-- 扣款：余额不足时不更新（原版先读后写，并发请求可透支）。
UPDATE `user` SET money = money - sqlc.arg(amount) WHERE id = sqlc.arg(id) AND money >= sqlc.arg(amount);

-- name: AddUserMoney :exec
UPDATE `user` SET money = money + sqlc.arg(amount) WHERE id = sqlc.arg(id);

-- name: UpdateUserPort :exec
UPDATE `user` SET port = ? WHERE id = ?;

-- name: UpdateRelayPortForUser :exec
UPDATE relay SET port = sqlc.arg(new_port) WHERE user_id = sqlc.arg(user_id) AND port = sqlc.arg(old_port);

-- name: GetBlockIP :one
SELECT * FROM blockip WHERE ip = ? LIMIT 1;

-- name: DeleteBlockIP :exec
DELETE FROM blockip WHERE ip = ?;

-- name: ListBlockIPIDs :many
-- 解封时按标准化后的 IP 比较（节点可能以 ::ffff: 等其他写法记录同一个 IP）。
SELECT id, ip FROM blockip;

-- name: DeleteBlockIPByID :exec
DELETE FROM blockip WHERE id = ?;

-- name: InsertUnblockIP :exec
INSERT INTO unblockip (userid, ip, `datetime`) VALUES (?, ?, ?);

-- name: GetTelegramBindSession :one
SELECT * FROM telegram_session WHERE type = 0 AND user_id = ? LIMIT 1;

-- name: UpdateTelegramSession :exec
UPDATE telegram_session SET session_content = ?, `datetime` = ? WHERE id = ?;

-- name: ListPaybacksByRef :many
SELECT * FROM payback WHERE ref_by = ? ORDER BY `datetime` DESC, id DESC LIMIT ? OFFSET ?;

-- name: CountPaybacksByRef :one
SELECT COUNT(*) FROM payback WHERE ref_by = ?;

-- name: ListRecentLoginIPs :many
SELECT * FROM login_ip WHERE userid = ? AND type = 0 ORDER BY `datetime` DESC, id DESC LIMIT 10;

-- 删除账户（原版 User::kill_user），在事务中依次执行。

-- name: KillRadiusBan :exec
DELETE FROM radius_ban WHERE userid = ?;
-- name: KillDisconnectIP :exec
DELETE FROM disconnect_ip WHERE userid = ?;
-- name: KillBought :exec
DELETE FROM bought WHERE userid = ?;
-- name: KillAliveIP :exec
DELETE FROM alive_ip WHERE userid = ?;
-- name: KillCode :exec
DELETE FROM code WHERE userid = ?;
-- name: KillDetectLog :exec
DELETE FROM detect_log WHERE user_id = ?;
-- name: KillLoginIP :exec
DELETE FROM login_ip WHERE userid = ?;
-- name: KillInviteCode :exec
DELETE FROM ss_invite_code WHERE user_id = ?;
-- name: KillTelegramSession :exec
DELETE FROM telegram_session WHERE user_id = ?;
-- name: KillUnblockIP :exec
DELETE FROM unblockip WHERE userid = ?;
-- name: KillTrafficLog :exec
DELETE FROM user_traffic_log WHERE user_id = ?;
-- name: KillUserToken :exec
DELETE FROM user_token WHERE user_id = ?;
-- name: KillPasswordReset :exec
DELETE FROM ss_password_reset WHERE email = ?;
