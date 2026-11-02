-- name: ListSubNodes :many
-- 订阅使用的 SS/SSR 节点（sort 0/10/13），管理员不受等级、分组限制（原版 URL::getAllItems）。
SELECT * FROM ss_node
WHERE sort IN (0, 10, 13) AND type = 1
  AND (sqlc.arg(is_admin) = 1 OR ((node_group = sqlc.arg(node_group) OR node_group = 0) AND node_class <= sqlc.arg(class)))
ORDER BY name;

-- name: ListMuPortNodes :many
-- 单端口多用户承载节点（sort 9）。
SELECT * FROM ss_node
WHERE sort = 9 AND type = 1
  AND (sqlc.arg(is_admin) = 1 OR ((node_group = sqlc.arg(node_group) OR node_group = 0) AND node_class <= sqlc.arg(class)))
ORDER BY id;

-- name: ListVMessNodes :many
-- V2Ray 节点（sort 11/12），原版对管理员同样按等级、分组过滤。
SELECT * FROM ss_node
WHERE sort IN (11, 12) AND type = 1 AND (node_group = ? OR node_group = 0) AND node_class <= ?
ORDER BY name;

-- name: ListRelayRulesForUser :many
SELECT * FROM relay WHERE user_id = ? OR user_id = 0 ORDER BY id;

-- name: GetMuUserByPort :one
SELECT * FROM `user` WHERE port = ? AND is_multi_user <> 0 ORDER BY id LIMIT 1;

-- name: GetUserByPort :one
SELECT * FROM `user` WHERE port = ? ORDER BY id LIMIT 1;

-- name: GetSubLink :one
SELECT * FROM link WHERE type = 11 AND userid = ? AND geo = ? ORDER BY id LIMIT 1;

-- name: GetLinkByToken :one
SELECT * FROM link WHERE token = ? LIMIT 1;

-- name: CreateSubLink :exec
INSERT INTO link (type, address, port, token, ios, userid, geo, method) VALUES (11, '', 0, ?, 0, ?, ?, '');

-- name: CountLinkToken :one
SELECT COUNT(*) FROM link WHERE token = ?;
