-- 商店、购买记录、充值码、邀请（原版 UserController 中的 shop / buy / code / invite 等方法）。

-- name: ListShopsOnSale :many
SELECT * FROM shop WHERE status = 1 ORDER BY name, id;

-- name: GetShopOnSale :one
SELECT * FROM shop WHERE id = ? AND status = 1 LIMIT 1;

-- name: ListShopsByIDs :many
SELECT * FROM shop WHERE id IN (sqlc.slice('ids'));

-- name: GetCouponByCode :one
SELECT * FROM coupon WHERE code = ? ORDER BY id LIMIT 1;

-- name: CountBoughtWithCoupon :one
SELECT COUNT(*) FROM bought WHERE userid = ? AND coupon = ?;

-- name: DisableUserRenew :exec
UPDATE bought SET renew = 0 WHERE userid = ?;

-- name: InsertBought :exec
INSERT INTO bought (userid, shopid, `datetime`, renew, coupon, price) VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateUserAfterBuy :exec
-- 购买或续费套餐后写回用户的套餐相关字段（原版 Shop::buy 末尾的 $user->save()）。
UPDATE `user` SET transfer_enable = ?, u = ?, d = ?, last_day_t = ?, expire_in = ?, class = ?, class_expire = ?,
    node_speedlimit = ?, node_connector = ?
WHERE id = ?;

-- name: ListBoughtByUser :many
SELECT * FROM bought WHERE userid = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountBoughtByUser :one
SELECT COUNT(*) FROM bought WHERE userid = ?;

-- name: DisableBoughtRenew :execrows
UPDATE bought SET renew = 0 WHERE id = ? AND userid = ?;

-- name: GetBoughtForUser :one
SELECT * FROM bought WHERE id = ? AND userid = ? LIMIT 1;

-- name: ListUserCodes :many
SELECT * FROM code WHERE type <> -2 AND userid = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: CountUserCodes :one
SELECT COUNT(*) FROM code WHERE type <> -2 AND userid = ?;

-- name: GetUnusedCodeForUpdate :one
SELECT * FROM code WHERE code = ? AND isused = 0 ORDER BY id LIMIT 1 FOR UPDATE;

-- name: MarkCodeUsed :exec
UPDATE code SET isused = 1, usedatetime = ?, userid = ? WHERE id = ?;

-- name: InsertPayback :exec
INSERT INTO payback (total, userid, ref_by, ref_get, `datetime`) VALUES (?, ?, ?, ?, ?);

-- name: AddUserTransferEnable :exec
UPDATE `user` SET transfer_enable = transfer_enable + ? WHERE id = ?;

-- name: UpdateUserExpireIn :exec
UPDATE `user` SET expire_in = ? WHERE id = ?;

-- name: UpdateUserClass :exec
UPDATE `user` SET class = ?, class_expire = ? WHERE id = ?;

-- name: FindRechargeCodeAfter :one
-- 原版 code_check：支付完成后轮询，是否已有新的充值记录。
SELECT * FROM code WHERE userid = ? AND usedatetime > ? ORDER BY id LIMIT 1;

-- name: GetUserInviteCode :one
SELECT * FROM ss_invite_code WHERE user_id = ? ORDER BY id LIMIT 1;

-- name: CountInviteCode :one
SELECT COUNT(*) FROM ss_invite_code WHERE code = ?;

-- name: InsertInviteCode :exec
INSERT INTO ss_invite_code (code, user_id) VALUES (?, ?);

-- name: UpdateInviteCode :exec
UPDATE ss_invite_code SET code = ? WHERE id = ?;

-- name: DeleteUserInviteCodes :exec
DELETE FROM ss_invite_code WHERE user_id = ?;

-- name: SumPaybackByRef :one
SELECT CAST(COALESCE(SUM(ref_get), 0) AS DECIMAL(12, 2)) AS total FROM payback WHERE ref_by = ?;

-- name: ListPaybacksByRefID :many
SELECT * FROM payback WHERE ref_by = ? ORDER BY id DESC LIMIT ? OFFSET ?;

-- name: AddUserInviteNum :exec
UPDATE `user` SET invite_num = invite_num + ? WHERE id = ?;

-- name: InsertPaylist :exec
INSERT INTO paylist (userid, total, status, tradeno, `datetime`, type, url) VALUES (?, ?, 0, ?, ?, 0, '');

-- name: GetPaylistByTradeno :one
SELECT * FROM paylist WHERE tradeno = ? LIMIT 1;

-- name: CountPaylistTradeno :one
SELECT COUNT(*) FROM paylist WHERE tradeno = ?;

-- name: MarkPaylistPaid :execrows
-- 支付回调：只有未支付的订单会被标记，重复通知不会重复入账（K45）。
UPDATE paylist SET status = 1 WHERE tradeno = ? AND status = 0;

-- name: DeleteBoughtForUser :execrows
DELETE FROM bought WHERE id = ? AND userid = ?;
