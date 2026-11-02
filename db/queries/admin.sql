-- 管理后台（原版 Admin* 控制器、Services\Analytics）。

-- name: AdminUserStats :one
SELECT COUNT(*) AS total,
       CAST(COALESCE(SUM(last_check_in_time > 0), 0) AS SIGNED) AS checkin,
       CAST(COALESCE(SUM(last_check_in_time > sqlc.arg(today)), 0) AS SIGNED) AS today_checkin,
       CAST(COALESCE(SUM(u), 0) AS SIGNED) AS sum_u,
       CAST(COALESCE(SUM(d), 0) AS SIGNED) AS sum_d,
       CAST(COALESCE(SUM(last_day_t), 0) AS SIGNED) AS sum_last_day,
       CAST(COALESCE(SUM(transfer_enable), 0) AS SIGNED) AS sum_transfer,
       CAST(COALESCE(SUM(t = 0), 0) AS SIGNED) AS unused,
       CAST(COALESCE(SUM(t > sqlc.arg(online_60)), 0) AS SIGNED) AS online_60,
       CAST(COALESCE(SUM(t > sqlc.arg(online_3600)), 0) AS SIGNED) AS online_3600,
       CAST(COALESCE(SUM(t > sqlc.arg(online_86400)), 0) AS SIGNED) AS online_86400
FROM `user`;

-- name: AdminNodeStats :one
-- 后台首页「节点在线情况」：统计启用（type = 1）且属于当前支持类型（sorts，即后台可选的节点类型）的节点，
-- 心跳晚于 alive_after 的为在线，其余（心跳过期、从未上报）为离线。停用节点与已停用的旧类型不计入。
-- 原版统计 sort IN (0, 10, 11, 12, 13) 且心跳 > 0 的全部节点（含停用），新类型 SS2022 / VLESS / AnyTLS 不计入。
SELECT CAST(COUNT(*) AS SIGNED) AS total,
       CAST(COALESCE(SUM(node_heartbeat > sqlc.arg(alive_after)), 0) AS SIGNED) AS alive
FROM ss_node WHERE type = 1 AND sort IN (sqlc.slice('sorts'));

-- name: CountCouponCode :one
SELECT COUNT(*) FROM coupon WHERE code = ?;

-- name: InsertCoupon :exec
INSERT INTO coupon (code, onetime, expire, shop, credit) VALUES (?, ?, ?, ?, ?);

-- name: InsertCode :exec
INSERT INTO code (code, type, number, isused, userid, usedatetime) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetAnnouncement :one
SELECT * FROM announcement WHERE id = ?;

-- name: InsertAnnouncement :exec
INSERT INTO announcement (`date`, content, markdown) VALUES (?, ?, ?);

-- name: UpdateAnnouncement :execrows
UPDATE announcement SET content = ?, markdown = ?, `date` = ? WHERE id = ?;

-- name: DeleteAnnouncement :execrows
DELETE FROM announcement WHERE id = ?;

-- name: ListUsersForMail :many
SELECT * FROM `user` WHERE class >= ? ORDER BY id LIMIT ? OFFSET ?;

-- name: GetDetectRule :one
SELECT * FROM detect_list WHERE id = ?;

-- name: InsertDetectRule :exec
INSERT INTO detect_list (name, text, regex, type) VALUES (?, ?, ?, ?);

-- name: UpdateDetectRule :execrows
UPDATE detect_list SET name = ?, text = ?, regex = ?, type = ? WHERE id = ?;

-- name: DeleteDetectRule :execrows
DELETE FROM detect_list WHERE id = ?;

-- name: DeleteAuto :execrows
DELETE FROM auto WHERE id = ?;

-- name: GetShop :one
SELECT * FROM shop WHERE id = ?;

-- name: InsertShop :exec
INSERT INTO shop (name, price, content, auto_renew, auto_reset_bandwidth, status) VALUES (?, ?, ?, ?, ?, 1);

-- name: UpdateShop :execrows
UPDATE shop SET name = ?, price = ?, content = ?, auto_renew = ?, auto_reset_bandwidth = ?, status = 1 WHERE id = ?;

-- name: SetShopStatus :execrows
UPDATE shop SET status = ? WHERE id = ?;

-- name: DisableShopRenew :exec
UPDATE bought SET renew = 0 WHERE shopid = ?;

-- name: CountShopSalesSince :one
SELECT COUNT(*) FROM bought WHERE shopid = ? AND `datetime` > ?;

-- name: DisableBoughtRenewByID :execrows
UPDATE bought SET renew = 0 WHERE id = ?;

-- name: GetRelayRule :one
SELECT * FROM relay WHERE id = ?;

-- name: ListAllRelayRules :many
SELECT * FROM relay ORDER BY id;

-- name: AdminInsertRelayRule :exec
INSERT INTO relay (user_id, source_node_id, dist_node_id, dist_ip, port, priority) VALUES (?, ?, ?, ?, ?, ?);

-- name: AdminUpdateRelayRule :execrows
UPDATE relay SET user_id = ?, source_node_id = ?, dist_node_id = ?, dist_ip = ?, port = ?, priority = ? WHERE id = ?;

-- name: DeleteRelayRule :execrows
DELETE FROM relay WHERE id = ?;

-- name: ListRelayByDist :many
SELECT * FROM relay WHERE dist_node_id = ?;

-- name: UpdateRelayDistIP :exec
UPDATE relay SET dist_ip = ? WHERE id = ?;

-- name: InsertNode :execlastid
INSERT INTO ss_node (name, type, server, method, info, status, sort, custom_method, traffic_rate, node_class, node_speedlimit,
    node_connector, node_bandwidth, node_bandwidth_limit, bandwidthlimit_resetday, node_heartbeat, node_ip, node_group, custom_rss, mu_only)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, 0, ?, ?, ?, ?);

-- name: UpdateNode :execrows
UPDATE ss_node SET name = ?, type = ?, server = ?, method = ?, info = ?, status = ?, sort = ?, custom_method = ?, traffic_rate = ?,
    node_class = ?, node_speedlimit = ?, node_bandwidth_limit = ?, bandwidthlimit_resetday = ?, node_ip = ?, node_group = ?,
    custom_rss = ?, mu_only = ?
WHERE id = ?;

-- name: DeleteNode :execrows
DELETE FROM ss_node WHERE id = ?;

-- name: AdminUpdateUser :exec
UPDATE `user` SET email = ?, pass = ?, auto_reset_day = ?, auto_reset_bandwidth = ?, port = ?, passwd = ?, protocol = ?,
    protocol_param = ?, obfs = ?, obfs_param = ?, is_multi_user = ?, transfer_enable = ?, invite_num = ?, method = ?,
    node_speedlimit = ?, node_connector = ?, enable = ?, is_admin = ?, ga_enable = ?, node_group = ?, ref_by = ?, remark = ?,
    money = ?, class = ?, class_expire = ?, expire_in = ?, forbidden_ip = ?, forbidden_port = ?
WHERE id = ?;

-- name: SetUserInviteNum :exec
UPDATE `user` SET invite_num = invite_num + ? WHERE id = ?;

-- name: CountPaidUsers :one
-- 付费用户：等级大于 0 且等级未过期（购买套餐会设置等级与等级到期时间）。时间按 UTC 存储。
SELECT COUNT(*) FROM `user` WHERE class > 0 AND class_expire > sqlc.arg(now);
