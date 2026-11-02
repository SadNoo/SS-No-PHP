-- +goose Up
-- panel xcat repairDisconnectIP 的修复记录：每次修复的编号（repair_id）与实际修改了的用户。
-- 与 disconnect_ip 的修改在同一个事务中写入，只有提交成功的修改才有记录；回滚文件中的语句只恢复有记录的用户，
-- 修复时跳过的用户、失败的事务都不会被回滚覆盖。只新增本表，不修改旧表。
CREATE TABLE disconnect_ip_repair (
    repair_id CHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    userid INT UNSIGNED NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (repair_id, userid)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE disconnect_ip_repair;
