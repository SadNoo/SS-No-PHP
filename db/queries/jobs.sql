-- 定时任务（原版 Command\Job、DailyMail、FinanceMail、XCat）。

-- name: GetJobState :one
SELECT * FROM job_state WHERE name = ?;

-- name: SetJobState :exec
INSERT INTO job_state (name, value, updated_at) VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at);

-- name: DeleteJobState :exec
DELETE FROM job_state WHERE name = ?;

-- name: ListJobStateNamesByPrefix :many
-- 每分钟用户检查一次读出全部用户标记（按主键前缀范围扫描），不再每个用户各查一次。
SELECT name FROM job_state WHERE name LIKE CONCAT(sqlc.arg(prefix), '%');

-- name: DeleteJobStatesByPrefix :exec
DELETE FROM job_state WHERE name LIKE CONCAT(sqlc.arg(prefix), '%');

-- name: ResetNodeBandwidthOnDay :exec
UPDATE ss_node SET node_bandwidth = 0 WHERE bandwidthlimit_resetday = ? AND sort IN (0, 10, 11, 12, 13);

-- name: CleanNodeInfo :exec
DELETE FROM ss_node_info WHERE log_time < ?;
-- name: CleanNodeOnlineLog :exec
DELETE FROM ss_node_online_log WHERE log_time < ?;
-- name: CleanTrafficLog :exec
DELETE FROM user_traffic_log WHERE log_time < ?;
-- name: CleanDetectLog :exec
DELETE FROM detect_log WHERE `datetime` < ?;
-- name: CleanSpeedtest :exec
DELETE FROM speedtest WHERE `datetime` < ?;
-- name: CleanEmailVerify :exec
DELETE FROM email_verify WHERE expire_in < ?;
-- name: CleanAliveIP :exec
DELETE FROM alive_ip WHERE `datetime` < ?;
-- name: CleanUnblockIP :exec
DELETE FROM unblockip WHERE `datetime` < ?;
-- name: CleanBlockIP :exec
DELETE FROM blockip WHERE `datetime` < ?;
-- name: CleanTelegramSession :exec
DELETE FROM telegram_session WHERE `datetime` < ?;

-- name: ListAllBought :many
SELECT * FROM bought ORDER BY id;

-- name: DeleteBought :exec
DELETE FROM bought WHERE id = ?;

-- name: ResetUserTraffic :exec
UPDATE `user` SET transfer_enable = ?, u = 0, d = 0, last_day_t = 0 WHERE id = ?;

-- name: SnapshotLastDayTraffic :exec
-- 每日结算：记录截至今天的已用流量（用于计算“今日已用”）。
UPDATE `user` SET last_day_t = u + d;

-- name: ListUsersAutoResetOnDay :many
SELECT * FROM `user` WHERE auto_reset_day = ?;

-- name: ListAllUsers :many
SELECT * FROM `user` ORDER BY id;

-- name: ListUserIDs :many
SELECT id FROM `user` ORDER BY id;

-- name: ListUsersWithConnector :many
-- 有同时在线 IP 数限制、且最近有在线 IP 的用户（每分钟检查只处理这些用户）。
SELECT * FROM `user` WHERE node_connector > 0
    AND id IN (SELECT DISTINCT userid FROM alive_ip WHERE `datetime` >= sqlc.arg(since));

-- name: UpsertIPOnline :exec
-- 更新在线状态：已有记录只更新最后上报时间；上次上报早于 reset_before（离线过久）时重新计算开始时间。
INSERT INTO ip_online (userid, ip, first_seen, last_seen) VALUES (sqlc.arg(userid), sqlc.arg(ip), sqlc.arg(first_seen), sqlc.arg(last_seen))
ON DUPLICATE KEY UPDATE
    first_seen = IF(last_seen < sqlc.arg(reset_before), VALUES(first_seen), first_seen),
    last_seen = GREATEST(last_seen, VALUES(last_seen));

-- name: ListIPOnlineSince :many
-- 最近上报过的在线 IP，按本次连续在线的开始时间排序（同一秒按 IP）。
SELECT * FROM ip_online WHERE last_seen >= ? ORDER BY first_seen, ip;

-- name: CleanIPOnline :exec
DELETE FROM ip_online WHERE last_seen < ?;

-- name: KillIPOnline :exec
DELETE FROM ip_online WHERE userid = ?;

-- name: ListAliveIPSpans :many
-- 每个用户每个 IP 在窗口内首次与最后一次上报的时间（同时在线 IP 数限制使用）。
SELECT userid, ip, CAST(MIN(`datetime`) AS SIGNED) AS first_seen, CAST(MAX(`datetime`) AS SIGNED) AS last_seen
FROM alive_ip WHERE `datetime` >= ? GROUP BY userid, ip;

-- disconnect_ip 的写入（每分钟检查新增、到期解除、xcat repairDisconnectIP、删除用户）都在事务中先锁住用户行，
-- 再读写该用户的 disconnect_ip 表记录，最后更新 user.disconnect_ip：字段在锁内重新读取，不用事务外读到的旧值覆盖。

-- name: LockUserDisconnect :one
-- 锁住用户行并读取最新的 disconnect_ip 与同时在线 IP 数限制（每分钟检查新增限制）。
SELECT disconnect_ip, node_connector FROM `user` WHERE id = ? FOR UPDATE;

-- name: GetDisconnectForUpdate :one
-- 到期解除：锁住用户行后锁住这条记录，确认它仍然存在。
SELECT * FROM disconnect_ip WHERE id = ? FOR UPDATE;

-- name: LockUserRow :one
-- 删除用户前先锁住用户行（与其他写入 disconnect_ip 的事务保持相同的加锁顺序）。
SELECT id FROM `user` WHERE id = ? FOR UPDATE;

-- name: InsertDisconnect :exec
INSERT INTO disconnect_ip (userid, ip, `datetime`) VALUES (?, ?, ?);

-- name: ListDisconnectsBefore :many
SELECT * FROM disconnect_ip WHERE `datetime` < ?;

-- name: DeleteDisconnect :exec
DELETE FROM disconnect_ip WHERE id = ?;

-- name: UpdateUserDisconnectIP :exec
UPDATE `user` SET disconnect_ip = ? WHERE id = ?;

-- name: ListRenewDue :many
SELECT * FROM bought WHERE renew < ? AND renew <> 0 ORDER BY id;

-- name: GetLatestBoughtID :one
-- 用户最新的订单（不论是否开启续费、是否到期）：只有它可以自动续费。
SELECT id FROM bought WHERE userid = ? ORDER BY id DESC LIMIT 1;

-- name: ClaimBoughtRenew :execrows
-- 在续费事务中确认订单仍开启自动续费且已到期，并关闭它（之后由新订单接续）。影响 0 行时不续费、不扣款。
UPDATE bought SET renew = 0 WHERE id = ? AND renew <> 0 AND renew < ?;

-- name: ListRecentLogins :many
SELECT * FROM login_ip WHERE `datetime` > ? ORDER BY id;

-- name: ListLastTwoLogins :many
SELECT * FROM login_ip WHERE userid = ? ORDER BY id DESC LIMIT 2;

-- name: SaveUserJobFields :exec
-- 每分钟检查后写回用户的流量与等级字段。
UPDATE `user` SET transfer_enable = ?, u = ?, d = ?, last_day_t = ?, class = ? WHERE id = ?;

-- name: UpdateNodeIP :exec
UPDATE ss_node SET node_ip = ? WHERE id = ?;

-- name: ListUsedCodesBetween :many
-- 财务报表：时间范围内已使用的余额充值码（时间为 UTC）。
SELECT * FROM code WHERE type = -1 AND isused = 1 AND usedatetime >= ? AND usedatetime < ? ORDER BY id;

-- name: ListUsedCodesAnyTypeBetween :many
SELECT * FROM code WHERE isused = 1 AND usedatetime >= ? AND usedatetime < ? ORDER BY id;

-- name: CountTodayCheckin :one
SELECT COUNT(*) FROM `user` WHERE last_check_in_time > ?;

-- name: SumTodayTraffic :one
SELECT CAST(COALESCE(SUM(u + d - last_day_t), 0) AS SIGNED) FROM `user`;

-- name: ListDailyMailUsers :many
SELECT * FROM `user` WHERE sendDailyMail = 1 ORDER BY id;

-- name: ResetAllEnabledTraffic :exec
UPDATE `user` SET u = 0, d = 0, last_day_t = 0 WHERE enable = 1;

-- name: ListUsersWithTelegram :many
SELECT * FROM `user` WHERE telegram_id > 0;

-- name: CountRadiusBan :one
SELECT COUNT(*) FROM radius_ban WHERE userid = ?;

-- name: InsertRadiusBan :exec
INSERT INTO radius_ban (userid) VALUES (?);

-- name: ListRadiusBans :many
SELECT * FROM radius_ban ORDER BY id;

-- name: DeleteRadiusBan :exec
DELETE FROM radius_ban WHERE id = ?;

-- name: ListUserEmails :many
SELECT id, email FROM `user`;

-- name: ListInvalidClassUsers :many
-- 等级大于 0 却没有等级到期时间（1989 哨兵）的异常用户。
SELECT id, email, class, class_expire FROM `user` WHERE class > 0 AND class_expire < '1990-01-01' ORDER BY id;

-- name: CountAccountsToExtend :one
-- 帐号有效期调整为 100 年：统计仍有效、尚未达到目标且不是 2099 永久哨兵的帐号；expired 为已过期帐号数。
SELECT
    CAST(COALESCE(SUM(expire_in > sqlc.arg(now) AND expire_in < sqlc.arg(target) AND expire_in <> '2099-06-04 00:05:00'), 0) AS SIGNED) AS valid,
    CAST(COALESCE(SUM(expire_in <= sqlc.arg(now)), 0) AS SIGNED) AS expired
FROM `user`;

-- name: ExtendValidAccounts :execrows
UPDATE `user` SET expire_in = sqlc.arg(target)
WHERE expire_in > sqlc.arg(now) AND expire_in < sqlc.arg(target) AND expire_in <> '2099-06-04 00:05:00';

-- name: ExtendExpiredAccounts :execrows
UPDATE `user` SET expire_in = sqlc.arg(target) WHERE expire_in <= sqlc.arg(now);

-- name: ReplaceUserDisconnectIP :execrows
-- 修复 disconnect_ip：只有字段仍是读取时的原文才更新。按字节比较（不按字符排序规则：全角 / 半角、大小写、
-- 尾随空格都视为不同），读取后被改动过的跳过。
UPDATE `user` SET disconnect_ip = sqlc.arg(new_value)
WHERE id = sqlc.arg(id) AND CAST(disconnect_ip AS BINARY) = CAST(sqlc.arg(old_value) AS BINARY);

-- name: GetUserDisconnectIPForUpdate :one
-- 修复前锁住用户行并读取当前字段（每分钟任务写字段时会等待修复事务结束）。
SELECT disconnect_ip FROM `user` WHERE id = ? FOR UPDATE;

-- name: ListUserDisconnectRecordsForShare :many
-- 读取该用户当前的 disconnect_ip 表记录（在持有该用户行锁的事务中调用：新增记录也要先锁用户行，读到的就是最新数据；
-- READ COMMITTED 下只锁存在的记录，不锁间隙，见 internal/app/app.go 的 disconnectTx）。
SELECT ip FROM disconnect_ip WHERE userid = ? FOR SHARE;

-- name: InsertDisconnectRepair :exec
-- 记录本次修复实际修改了的用户（与修改在同一个事务中）。
INSERT INTO disconnect_ip_repair (repair_id, userid, created_at) VALUES (?, ?, ?);
