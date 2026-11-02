-- name: GetUser :one
SELECT * FROM `user` WHERE id = ? LIMIT 1;

-- name: GetUserForUpdate :one
SELECT * FROM `user` WHERE id = ? LIMIT 1 FOR UPDATE;

-- name: GetUserByEmail :one
SELECT * FROM `user` WHERE email = ? LIMIT 1;

-- name: CountUsersByEmail :one
SELECT COUNT(*) FROM `user` WHERE email = ?;

-- name: ListUsedPorts :many
SELECT port FROM `user`;

-- name: CountUsersByPort :one
SELECT COUNT(*) FROM `user` WHERE port = ?;

-- name: CreateUser :execlastid
INSERT INTO `user` (
    user_name, email, pass, passwd, port, t, u, d, transfer_enable, invite_num, money, ref_by,
    reg_date, reg_ip, expire_in, class, class_expire, node_connector, node_speedlimit, node_group,
    method, protocol, protocol_param, obfs, obfs_param, forbidden_ip, forbidden_port,
    auto_reset_day, auto_reset_bandwidth, im_type, im_value, theme, ga_token, ga_enable,
    is_admin, plan, enable, telegram_id, pac, remark, disconnect_ip
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, '', '', ''
);

-- name: UpdateUserPass :exec
UPDATE `user` SET pass = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM `user` WHERE id = ?;

-- name: AddUserTraffic :exec
-- 原子累加流量（K01：原版先读后写，并发时会丢失流量）。
UPDATE `user` SET u = u + ?, d = d + ?, t = ? WHERE id = ?;
