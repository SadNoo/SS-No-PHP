-- 工单、中转规则、审计（原版 UserController 的 ticket* / detect* 与 RelayController）。

-- name: ListUserTickets :many
SELECT * FROM ticket WHERE userid = ? AND rootid = 0 ORDER BY `datetime` DESC, id DESC LIMIT ? OFFSET ?;

-- name: CountUserTickets :one
SELECT COUNT(*) FROM ticket WHERE userid = ? AND rootid = 0;

-- name: GetRootTicket :one
SELECT * FROM ticket WHERE id = ? AND rootid = 0 LIMIT 1;

-- name: ListTicketThread :many
SELECT * FROM ticket WHERE id = sqlc.arg(id) OR rootid = sqlc.arg(id) ORDER BY `datetime` DESC, id DESC LIMIT ? OFFSET ?;

-- name: CountTicketThread :one
SELECT COUNT(*) FROM ticket WHERE id = sqlc.arg(id) OR rootid = sqlc.arg(id);

-- name: InsertTicket :exec
INSERT INTO ticket (title, content, rootid, userid, `datetime`, status) VALUES (?, ?, ?, ?, ?, 1);

-- name: UpdateTicketStatus :exec
UPDATE ticket SET status = ? WHERE id = ?;

-- name: ListAdmins :many
SELECT * FROM `user` WHERE is_admin = 1;

-- name: ListRelayRulesPage :many
SELECT * FROM relay WHERE user_id = ? OR user_id = 0 ORDER BY id LIMIT ? OFFSET ?;

-- name: CountRelayRulesVisible :one
SELECT COUNT(*) FROM relay WHERE user_id = ? OR user_id = 0;

-- name: GetUserRelayRule :one
SELECT * FROM relay WHERE id = ? AND user_id = ? LIMIT 1;

-- name: InsertRelayRule :exec
INSERT INTO relay (user_id, source_node_id, dist_node_id, dist_ip, port, priority) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateRelayRule :exec
UPDATE relay SET source_node_id = ?, dist_node_id = ?, dist_ip = ?, port = ?, priority = ? WHERE id = ? AND user_id = ?;

-- name: DeleteUserRelayRule :execrows
DELETE FROM relay WHERE id = ? AND user_id = ?;

-- name: ListRelayBySource :many
SELECT * FROM relay WHERE source_node_id = ?;

-- name: ListDetectRules :many
SELECT * FROM detect_list ORDER BY id LIMIT ? OFFSET ?;

-- name: CountDetectRules :one
SELECT COUNT(*) FROM detect_list;

-- name: ListUserDetectLogs :many
SELECT * FROM detect_log WHERE user_id = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountUserDetectLogs :one
SELECT COUNT(*) FROM detect_log WHERE user_id = ?;

-- name: ListDetectRulesByIDs :many
SELECT * FROM detect_list WHERE id IN (sqlc.slice('ids'));
