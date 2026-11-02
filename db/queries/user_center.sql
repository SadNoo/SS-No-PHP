-- 用户中心（原版 UserController）使用的查询。

-- name: LatestAnnouncement :one
SELECT * FROM announcement ORDER BY `date` DESC, id DESC LIMIT 1;

-- name: ListAnnouncements :many
SELECT * FROM announcement ORDER BY `date` DESC, id DESC;

-- name: ListAliveIPsSince :many
SELECT * FROM alive_ip WHERE userid = ? AND `datetime` >= ? ORDER BY id DESC;

-- name: ListNodeIPs :many
-- 节点自身的 IP（原版 Node::where('node_ip', $ip)：在线 IP 中排除节点之间的中转连接）。
SELECT node_ip FROM ss_node WHERE node_ip <> '';

-- name: SumUserCodeNumber :one
-- 原版 User::get_top_up：该用户名下所有充值码金额之和。
SELECT CAST(COALESCE(SUM(number), 0) AS DECIMAL(12, 2)) AS total FROM code WHERE userid = ?;

-- name: CheckinUser :execrows
-- 签到：在同一条语句中判断“今天未签到”，防止并发重复签到（K05）。
UPDATE `user` SET transfer_enable = transfer_enable + ?, last_check_in_time = ?
WHERE id = ? AND last_check_in_time < ?;

-- name: ListLatestNodeInfo :many
-- 每个节点最近 5 分钟内最新的一条负载记录（原版依赖 MySQL 5.6 的 GROUP BY 行为，8.x 下结果不确定）。
SELECT i.node_id, i.`load` FROM ss_node_info i
JOIN (SELECT s.node_id, MAX(s.id) AS id FROM ss_node_info s WHERE s.log_time > ? GROUP BY s.node_id) t ON t.id = i.id;

-- name: ListLatestNodeOnline :many
SELECT o.node_id, o.online_user FROM ss_node_online_log o
JOIN (SELECT s.node_id, MAX(s.id) AS id FROM ss_node_online_log s WHERE s.log_time > ? GROUP BY s.node_id) t ON t.id = o.id;

-- name: ListUserNodes :many
SELECT * FROM ss_node WHERE type = 1 ORDER BY node_class, name;

-- name: ListRelayRulesVisible :many
SELECT * FROM relay WHERE user_id = ? OR user_id = 0 ORDER BY id;

-- name: ListUsersByPorts :many
SELECT * FROM `user` WHERE port IN (sqlc.slice('ports'));

-- name: ListSpeedtestsSince :many
SELECT * FROM speedtest WHERE `datetime` > ? ORDER BY `datetime` DESC;

-- name: ListUserTrafficLogsSince :many
SELECT * FROM user_traffic_log WHERE user_id = ? AND log_time > ? ORDER BY id DESC;

-- name: ListDonateCodes :many
SELECT * FROM code WHERE (type = -1 OR type = -2) AND isused = 1 ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountDonateCodes :one
SELECT COUNT(*) FROM code WHERE (type = -1 OR type = -2) AND isused = 1;

-- name: SumUsedCodesByType :one
SELECT CAST(COALESCE(SUM(number), 0) AS DECIMAL(12, 2)) AS total FROM code WHERE isused = 1 AND type = ?;

-- name: ListUsersByIDs :many
SELECT * FROM `user` WHERE id IN (sqlc.slice('ids'));

-- name: ListNodeLoadSamples :many
-- 原版 Node::getNodeLoad：每 30 分钟取一个点，最近 48 个。
SELECT * FROM ss_node_info WHERE node_id = ? AND log_time % 1800 < 60 ORDER BY id DESC LIMIT 48;

-- name: ListNodeAliveSamples :many
SELECT * FROM ss_node_online_log WHERE node_id = ? AND log_time % 1800 < 60 ORDER BY id DESC LIMIT 48;

-- name: ListNodeSpeedtests :many
SELECT * FROM speedtest WHERE nodeid = ? ORDER BY id DESC LIMIT 48;

-- name: LastNodeInfo :one
SELECT * FROM ss_node_info WHERE node_id = ? ORDER BY id DESC LIMIT 1;

-- name: CountNodeOnlineLogsSince :one
SELECT COUNT(*) FROM ss_node_online_log WHERE node_id = ? AND log_time >= ?;

-- name: ListUserBoughtShops :many
-- 用户的购买记录与对应商品（用户中心计算下次流量重置时间；商品已删除的记录不计，与每日任务相同）。
SELECT bought.id, bought.datetime, shop.content FROM bought JOIN shop ON shop.id = bought.shopid WHERE bought.userid = ? ORDER BY bought.id;
