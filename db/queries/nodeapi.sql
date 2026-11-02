-- 节点后端接口（原版 Mod_Mu 与 Mu 控制器）。

-- name: TouchNodeHeartbeat :exec
UPDATE ss_node SET node_heartbeat = ? WHERE id = ?;

-- name: ListNodeUsers :many
-- 节点可用的用户：等级不低于节点（节点有分组时还需分组一致）或管理员，已启用且未过期。
SELECT * FROM `user`
WHERE (( class >= sqlc.arg(node_class) AND (sqlc.arg(node_group) = 0 OR node_group = sqlc.arg(node_group)) ) OR is_admin = 1)
  AND enable = 1 AND expire_in > sqlc.arg(now)
ORDER BY id;

-- name: AddNodeBandwidth :exec
UPDATE ss_node SET node_bandwidth = node_bandwidth + ? WHERE id = ?;

-- name: InsertTrafficLog :exec
INSERT INTO user_traffic_log (user_id, u, d, node_id, rate, traffic, log_time) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: InsertNodeOnlineLog :exec
INSERT INTO ss_node_online_log (node_id, online_user, log_time) VALUES (?, ?, ?);

-- name: InsertNodeInfo :exec
INSERT INTO ss_node_info (node_id, uptime, `load`, log_time) VALUES (?, ?, ?, ?);

-- name: InsertAliveIP :exec
INSERT INTO alive_ip (nodeid, userid, ip, `datetime`) VALUES (?, ?, ?, ?);

-- name: InsertDetectLog :exec
INSERT INTO detect_log (user_id, list_id, `datetime`, node_id) VALUES (?, ?, ?, ?);

-- name: ListAllDetectRules :many
SELECT * FROM detect_list ORDER BY id;

-- name: ListAPINodes :many
SELECT * FROM ss_node WHERE sort IN (0, 10, 12) ORDER BY id;

-- name: ListBlockIPsSince :many
SELECT * FROM blockip WHERE `datetime` > ? ORDER BY id;

-- name: ListUnblockIPsSince :many
SELECT * FROM unblockip WHERE `datetime` > ? ORDER BY id;

-- name: InsertBlockIP :exec
INSERT INTO blockip (nodeid, ip, `datetime`) VALUES (?, ?, ?);

-- name: CountBlockIP :one
SELECT COUNT(*) FROM blockip WHERE ip = ?;

-- name: InsertSpeedtest :exec
INSERT INTO speedtest (nodeid, `datetime`, telecomping, telecomeupload, telecomedownload, unicomping, unicomupload, unicomdownload, cmccping, cmccupload, cmccdownload)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAutoCommandsSince :many
SELECT * FROM auto WHERE `datetime` > ? AND type = 1 ORDER BY id;

-- name: ListAutoReceipts :many
SELECT sign FROM auto WHERE type = 2 AND sign IN (sqlc.slice('signs'));

-- name: InsertAuto :exec
INSERT INTO auto (type, value, sign, `datetime`) VALUES (?, ?, ?, ?);

-- name: AddUserTrafficRows :execrows
-- 节点上报流量：原子累加（K01），返回影响行数以跳过已删除的用户。
UPDATE `user` SET u = u + ?, d = d + ?, t = ? WHERE id = ?;

-- name: DeleteOldTrafficBatches :execrows
-- 清理过期的流量批次去重记录，每次最多 5000 条（分批执行，避免长时间锁表）。
DELETE FROM node_traffic_batch WHERE created_at < ? ORDER BY created_at LIMIT 5000;

-- name: ListUserNodeAccess :many
-- 流量批次一次读出相关用户的等级、分组与管理员标记，判断能否在本节点计费。
SELECT id, class, node_group, is_admin FROM `user` WHERE id IN (sqlc.slice('ids')) ORDER BY id;
