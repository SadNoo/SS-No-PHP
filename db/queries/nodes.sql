-- name: GetNode :one
SELECT * FROM ss_node WHERE id = ? LIMIT 1;

-- name: ListNodes :many
SELECT * FROM ss_node ORDER BY id;

-- name: ListNodesOrdered :many
SELECT * FROM ss_node ORDER BY node_class, name;

-- name: GetNodeByExactIP :one
-- 节点 IP 精确匹配（K04：原版用 LIKE 'IP%' 前缀匹配）。node_ip 可能是逗号分隔的多个地址，取第一个比较。
SELECT * FROM ss_node WHERE SUBSTRING_INDEX(node_ip, ',', 1) = ? ORDER BY id LIMIT 1;

-- name: ListNodesByName :many
SELECT * FROM ss_node ORDER BY name, id;
