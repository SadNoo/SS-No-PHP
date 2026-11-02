-- +goose Up
-- 定时任务的状态记录。
-- 原版在 storage/ 目录下用空文件记录“已发过提醒”等状态（如 5.offline、12.renew、traffic_notified/3.userid），
-- 多台服务器或重新部署时会丢失；新版统一存放在数据库中。
-- name 示例：offline:node:5、gfw:node:5、renew:bought:12、expire:user:3、traffic:user:3、daily:2026-10-21
CREATE TABLE `job_state` (
    `name`       VARCHAR(191) NOT NULL,
    `value`      VARCHAR(255) NOT NULL DEFAULT '',
    `updated_at` BIGINT       NOT NULL,
    PRIMARY KEY (`name`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE `job_state`;
