-- +goose Up
-- 同时在线 IP 数限制使用的在线状态：每个用户每个 IP 本次连续在线的开始时间（first_seen）与最后一次上报时间（last_seen）。
-- 由每分钟检查根据 alive_ip 维护；离线超过 10 分钟的记录删除，再次出现时重新计算开始时间。只新增本表，不修改旧表。
CREATE TABLE ip_online (
    userid INT UNSIGNED NOT NULL,
    ip VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    first_seen BIGINT NOT NULL,
    last_seen BIGINT NOT NULL,
    PRIMARY KEY (userid, ip),
    KEY idx_ip_online_last_seen (last_seen)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE ip_online;
