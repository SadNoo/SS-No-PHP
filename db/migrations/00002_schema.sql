-- 初始表结构：对应 SSPanel-UIM 2020-01 版（sql/glzjin_all.sql、sql/config.sql 及其后的全部 ALTER）。
--
-- 与原版的差异（数据迁移时按此转换）：
--   1. 全部表使用 utf8mb4 / utf8mb4_unicode_ci（原版混用 latin1、utf8、utf8mb4）。
--   2. DATETIME 字段一律存 UTC；原版的哨兵值（1989-06-04 00:05:00 表示“无等级期限”，
--      2099-06-04 00:05:00 表示“账户永不过期”）原样保留，不做时区换算。
--   3. 补齐常用查询的索引；user.email、link.token、ss_invite_code.code 改为唯一索引。
--   4. user.email 由 varchar(32) 放宽为 varchar(255)（原版长邮箱会被截断）。
--   5. 计数类整数字段统一为 BIGINT，避免溢出（如 user_traffic_log.u/d、各 log_time）。
--   6. 删除代码中从未使用的字段：user.last_get_gift_time、user.last_rest_pass_time、
--      user.expire_time、user.is_email_verify、link.isp。
--   7. Radius 相关表（radcheck 等）在独立的 Radius 数据库中，不在此创建。
--   8. 原版允许 NULL 的文本/数字列一律改为 NOT NULL：导入时 NULL 转为空字符串或 0
--      （原版 PHP 中 null == '' 和 null == 0 均成立，业务判断结果不变）。
--      TEXT 类列没有默认值，插入时必须显式提供。

-- +goose Up
CREATE TABLE `user` (
    `id`                   INT UNSIGNED    NOT NULL AUTO_INCREMENT,
    `user_name`            VARCHAR(128)    NOT NULL,
    `email`                VARCHAR(255)    NOT NULL,
    `pass`                 VARCHAR(255)    NOT NULL COMMENT '登录密码哈希',
    `passwd`               VARCHAR(64)     NOT NULL COMMENT '节点连接密码',
    `t`                    BIGINT          NOT NULL DEFAULT 0 COMMENT '最后使用时间（Unix 秒）',
    `u`                    BIGINT          NOT NULL DEFAULT 0 COMMENT '已用上行（字节，已乘倍率）',
    `d`                    BIGINT          NOT NULL DEFAULT 0 COMMENT '已用下行（字节，已乘倍率）',
    `plan`                 VARCHAR(2)      NOT NULL DEFAULT 'A',
    `transfer_enable`      BIGINT          NOT NULL DEFAULT 0 COMMENT '流量额度（字节）',
    `port`                 INT             NOT NULL DEFAULT 0,
    `switch`               TINYINT         NOT NULL DEFAULT 1,
    `enable`               TINYINT         NOT NULL DEFAULT 1,
    `type`                 TINYINT         NOT NULL DEFAULT 1,
    `last_check_in_time`   BIGINT          NOT NULL DEFAULT 0,
    `reg_date`             DATETIME        NOT NULL,
    `invite_num`           INT             NOT NULL DEFAULT 0,
    `money`                DECIMAL(12, 2)  NOT NULL DEFAULT 0.00,
    `ref_by`               INT UNSIGNED    NOT NULL DEFAULT 0,
    `method`               VARCHAR(64)     NOT NULL DEFAULT 'rc4-md5',
    `reg_ip`               VARCHAR(128)    NOT NULL DEFAULT '127.0.0.1',
    `node_speedlimit`      DECIMAL(12, 2)  NOT NULL DEFAULT 0.00,
    `node_connector`       INT             NOT NULL DEFAULT 0,
    `is_admin`             TINYINT         NOT NULL DEFAULT 0,
    `im_type`              INT             NOT NULL DEFAULT 1,
    `im_value`             TEXT            NOT NULL,
    `last_day_t`           BIGINT          NOT NULL DEFAULT 0,
    `sendDailyMail`        INT             NOT NULL DEFAULT 0,
    `class`                INT             NOT NULL DEFAULT 0,
    `class_expire`         DATETIME        NOT NULL DEFAULT '1989-06-04 00:05:00',
    `expire_in`            DATETIME        NOT NULL DEFAULT '2099-06-04 00:05:00',
    `theme`                VARCHAR(64)     NOT NULL DEFAULT 'material',
    `ga_token`             VARCHAR(64)     NOT NULL DEFAULT '',
    `ga_enable`            INT             NOT NULL DEFAULT 0,
    `pac`                  LONGTEXT        NOT NULL,
    `remark`               TEXT            NOT NULL,
    `node_group`           INT             NOT NULL DEFAULT 0,
    `auto_reset_day`       INT             NOT NULL DEFAULT 0,
    `auto_reset_bandwidth` DECIMAL(12, 2)  NOT NULL DEFAULT 0.00,
    `protocol`             VARCHAR(128)    NOT NULL DEFAULT 'origin',
    `protocol_param`       VARCHAR(128)    NOT NULL DEFAULT '',
    `obfs`                 VARCHAR(128)    NOT NULL DEFAULT 'plain',
    `obfs_param`           VARCHAR(128)    NOT NULL DEFAULT '',
    `forbidden_ip`         LONGTEXT        NOT NULL,
    `forbidden_port`       LONGTEXT        NOT NULL,
    `disconnect_ip`        LONGTEXT        NOT NULL,
    `is_hide`              INT             NOT NULL DEFAULT 0,
    `is_multi_user`        INT             NOT NULL DEFAULT 0,
    `telegram_id`          BIGINT          NOT NULL DEFAULT 0,
    `discord`              BIGINT          NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_user_email` (`email`),
    KEY `idx_user_user_name` (`user_name`),
    KEY `idx_user_port` (`port`),
    KEY `idx_user_ref_by` (`ref_by`),
    KEY `idx_user_telegram_id` (`telegram_id`),
    KEY `idx_user_class_expire` (`class_expire`),
    KEY `idx_user_expire_in` (`expire_in`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ss_node` (
    `id`                      INT UNSIGNED   NOT NULL AUTO_INCREMENT,
    `name`                    VARCHAR(128)   NOT NULL,
    `type`                    INT            NOT NULL DEFAULT 1,
    `server`                  VARCHAR(512)   NOT NULL,
    `method`                  VARCHAR(64)    NOT NULL,
    `info`                    VARCHAR(512)   NOT NULL DEFAULT '',
    `status`                  VARCHAR(128)   NOT NULL DEFAULT '',
    `sort`                    INT            NOT NULL DEFAULT 0,
    `custom_method`           TINYINT        NOT NULL DEFAULT 0,
    `traffic_rate`            FLOAT          NOT NULL DEFAULT 1,
    `node_class`              INT            NOT NULL DEFAULT 0,
    `node_speedlimit`         DECIMAL(12, 2) NOT NULL DEFAULT 0.00,
    `node_connector`          INT            NOT NULL DEFAULT 0,
    `node_bandwidth`          BIGINT         NOT NULL DEFAULT 0,
    `node_bandwidth_limit`    BIGINT         NOT NULL DEFAULT 0,
    `bandwidthlimit_resetday` INT            NOT NULL DEFAULT 0,
    `node_heartbeat`          BIGINT         NOT NULL DEFAULT 0,
    `node_ip`                 TEXT           NOT NULL,
    `node_group`              INT            NOT NULL DEFAULT 0,
    `custom_rss`              INT            NOT NULL DEFAULT 0,
    `mu_only`                 INT            NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    KEY `idx_ss_node_sort` (`sort`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ss_node_info` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `node_id`  INT UNSIGNED    NOT NULL,
    `uptime`   FLOAT           NOT NULL,
    `load`     VARCHAR(32)     NOT NULL,
    `log_time` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_ss_node_info_node_time` (`node_id`, `log_time`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ss_node_online_log` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `node_id`     INT UNSIGNED    NOT NULL,
    `online_user` INT             NOT NULL,
    `log_time`    BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_ss_node_online_log_node_time` (`node_id`, `log_time`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `user_traffic_log` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id`  INT UNSIGNED    NOT NULL,
    `u`        BIGINT          NOT NULL,
    `d`        BIGINT          NOT NULL,
    `node_id`  INT UNSIGNED    NOT NULL,
    `rate`     FLOAT           NOT NULL,
    `traffic`  VARCHAR(32)     NOT NULL,
    `log_time` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_user_traffic_log_user_time` (`user_id`, `log_time`),
    KEY `idx_user_traffic_log_node_time` (`node_id`, `log_time`),
    KEY `idx_user_traffic_log_time` (`log_time`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `alive_ip` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `nodeid`   INT UNSIGNED    NOT NULL,
    `userid`   INT UNSIGNED    NOT NULL,
    `ip`       VARCHAR(255)    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_alive_ip_datetime` (`datetime`),
    KEY `idx_alive_ip_userid` (`userid`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `blockip` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `nodeid`   INT UNSIGNED    NOT NULL,
    `ip`       VARCHAR(255)    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_blockip_datetime` (`datetime`),
    KEY `idx_blockip_ip` (`ip`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `unblockip` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `ip`       VARCHAR(255)    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    `userid`   INT UNSIGNED    NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_unblockip_datetime` (`datetime`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `disconnect_ip` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `userid`   INT UNSIGNED    NOT NULL,
    `ip`       VARCHAR(255)    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_disconnect_ip_userid` (`userid`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `login_ip` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `userid`   INT UNSIGNED    NOT NULL,
    `ip`       VARCHAR(255)    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    `type`     INT             NOT NULL COMMENT '0 成功，1 失败',
    PRIMARY KEY (`id`),
    KEY `idx_login_ip_userid` (`userid`, `datetime`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `announcement` (
    `id`       INT UNSIGNED NOT NULL AUTO_INCREMENT,
    `date`     DATETIME     NOT NULL,
    `content`  LONGTEXT     NOT NULL,
    `markdown` LONGTEXT     NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `code` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `code`        VARCHAR(255)    NOT NULL,
    `type`        INT             NOT NULL,
    `number`      DECIMAL(11, 2)  NOT NULL,
    `isused`      INT             NOT NULL DEFAULT 0,
    `userid`      INT UNSIGNED    NOT NULL DEFAULT 0,
    `usedatetime` DATETIME        NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_code_code` (`code`),
    KEY `idx_code_userid` (`userid`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `link` (
    `id`      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `type`    INT             NOT NULL,
    `address` TEXT            NOT NULL,
    `port`    INT             NOT NULL,
    `token`   VARCHAR(255)    NOT NULL,
    `ios`     INT             NOT NULL DEFAULT 0,
    `userid`  INT UNSIGNED    NOT NULL,
    `geo`     INT             NOT NULL DEFAULT 0,
    `method`  TEXT            NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_link_token` (`token`),
    KEY `idx_link_userid` (`userid`, `type`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `radius_ban` (
    `id`     INT UNSIGNED NOT NULL AUTO_INCREMENT,
    `userid` INT UNSIGNED NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_radius_ban_userid` (`userid`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `speedtest` (
    `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `nodeid`           INT UNSIGNED    NOT NULL,
    `datetime`         BIGINT          NOT NULL,
    `telecomping`      VARCHAR(255)    NOT NULL,
    `telecomeupload`   VARCHAR(255)    NOT NULL,
    `telecomedownload` VARCHAR(255)    NOT NULL,
    `unicomping`       VARCHAR(255)    NOT NULL,
    `unicomupload`     VARCHAR(255)    NOT NULL,
    `unicomdownload`   VARCHAR(255)    NOT NULL,
    `cmccping`         VARCHAR(255)    NOT NULL,
    `cmccupload`       VARCHAR(255)    NOT NULL,
    `cmccdownload`     VARCHAR(255)    NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_speedtest_node_time` (`nodeid`, `datetime`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ss_invite_code` (
    `id`         INT UNSIGNED NOT NULL AUTO_INCREMENT,
    `code`       VARCHAR(128) NOT NULL,
    `user_id`    INT UNSIGNED NOT NULL,
    `created_at` TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_ss_invite_code_code` (`code`),
    KEY `idx_ss_invite_code_user_id` (`user_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ss_password_reset` (
    `id`          INT UNSIGNED NOT NULL AUTO_INCREMENT,
    `email`       VARCHAR(255) NOT NULL,
    `token`       VARCHAR(128) NOT NULL,
    `init_time`   BIGINT       NOT NULL,
    `expire_time` BIGINT       NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_ss_password_reset_token` (`token`),
    KEY `idx_ss_password_reset_email` (`email`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `user_token` (
    `id`          INT UNSIGNED NOT NULL AUTO_INCREMENT,
    `token`       VARCHAR(255) NOT NULL,
    `user_id`     INT UNSIGNED NOT NULL,
    `create_time` BIGINT       NOT NULL,
    `expire_time` BIGINT       NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_user_token_token` (`token`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `payback` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `total`    DECIMAL(12, 2)  NOT NULL,
    `userid`   INT UNSIGNED    NOT NULL,
    `ref_by`   INT UNSIGNED    NOT NULL,
    `ref_get`  DECIMAL(12, 2)  NOT NULL,
    `datetime` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_payback_ref_by` (`ref_by`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `shop` (
    `id`                   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `name`                 VARCHAR(255)    NOT NULL,
    `price`                DECIMAL(12, 2)  NOT NULL,
    `content`              TEXT            NOT NULL COMMENT 'JSON：商品内容',
    `auto_renew`           INT             NOT NULL DEFAULT 0,
    `auto_reset_bandwidth` INT             NOT NULL DEFAULT 0,
    `status`               INT             NOT NULL DEFAULT 1,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `coupon` (
    `id`      BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `code`    VARCHAR(255)    NOT NULL,
    `onetime` INT             NOT NULL,
    `expire`  BIGINT          NOT NULL,
    `shop`    TEXT            NOT NULL,
    `credit`  INT             NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_coupon_code` (`code`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `bought` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `userid`   INT UNSIGNED    NOT NULL,
    `shopid`   BIGINT UNSIGNED NOT NULL,
    `datetime` BIGINT          NOT NULL,
    `renew`    BIGINT          NOT NULL DEFAULT 0,
    `coupon`   VARCHAR(255)    NOT NULL DEFAULT '',
    `price`    DECIMAL(12, 2)  NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_bought_userid` (`userid`),
    KEY `idx_bought_renew` (`renew`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `ticket` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `title`    LONGTEXT        NOT NULL,
    `content`  LONGTEXT        NOT NULL,
    `rootid`   BIGINT UNSIGNED NOT NULL DEFAULT 0,
    `userid`   INT UNSIGNED    NOT NULL,
    `datetime` BIGINT          NOT NULL,
    `status`   INT             NOT NULL DEFAULT 1,
    PRIMARY KEY (`id`),
    KEY `idx_ticket_rootid` (`rootid`),
    KEY `idx_ticket_userid` (`userid`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `auto` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `type`     INT             NOT NULL,
    `value`    LONGTEXT        NOT NULL,
    `sign`     LONGTEXT        NOT NULL,
    `datetime` BIGINT          NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `email_verify` (
    `id`        BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `email`     VARCHAR(255)    NOT NULL,
    `ip`        VARCHAR(255)    NOT NULL,
    `code`      VARCHAR(64)     NOT NULL,
    `expire_in` BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_email_verify_email` (`email`),
    KEY `idx_email_verify_ip` (`ip`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `detect_list` (
    `id`    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `name`  LONGTEXT        NOT NULL,
    `text`  LONGTEXT        NOT NULL,
    `regex` LONGTEXT        NOT NULL,
    `type`  INT             NOT NULL,
    PRIMARY KEY (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `detect_log` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id`  INT UNSIGNED    NOT NULL,
    `list_id`  BIGINT UNSIGNED NOT NULL,
    `datetime` BIGINT          NOT NULL,
    `node_id`  INT UNSIGNED    NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_detect_log_user_id` (`user_id`),
    KEY `idx_detect_log_datetime` (`datetime`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `relay` (
    `id`             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id`        INT UNSIGNED    NOT NULL COMMENT '0 表示全部用户',
    `source_node_id` INT             NOT NULL COMMENT '0 表示全部节点',
    `dist_node_id`   INT             NOT NULL COMMENT '-1 表示阻断',
    `dist_ip`        VARCHAR(255)    NOT NULL,
    `port`           INT             NOT NULL,
    `priority`       INT             NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_relay_user_id` (`user_id`),
    KEY `idx_relay_source_node_id` (`source_node_id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `telegram_session` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `user_id`         INT UNSIGNED    NOT NULL,
    `type`            INT             NOT NULL,
    `session_content` VARCHAR(255)    NOT NULL,
    `datetime`        BIGINT          NOT NULL,
    PRIMARY KEY (`id`),
    KEY `idx_telegram_session_content` (`session_content`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

CREATE TABLE `paylist` (
    `id`       BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `userid`   INT UNSIGNED    NOT NULL,
    `total`    DECIMAL(12, 2)  NOT NULL,
    `status`   INT             NOT NULL DEFAULT 0,
    `tradeno`  VARCHAR(255)    NOT NULL DEFAULT '',
    `datetime` BIGINT          NOT NULL DEFAULT 0,
    `type`     INT             NOT NULL DEFAULT 0,
    `url`      VARCHAR(255)    NOT NULL DEFAULT '',
    PRIMARY KEY (`id`),
    KEY `idx_paylist_userid` (`userid`),
    KEY `idx_paylist_tradeno` (`tradeno`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- 原版 sql/config.sql：ChenPay 等功能使用的键值配置。
CREATE TABLE `config` (
    `name`  VARCHAR(255) NOT NULL,
    `value` TEXT         NOT NULL,
    PRIMARY KEY (`name`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

INSERT INTO `config` (`name`, `value`) VALUES
    ('AliPay_Cookie', ''),
    ('AliPay_QRcode', ''),
    ('AliPay_Status', '1'),
    ('Notice_EMail', ''),
    ('WxPay_Cookie', ''),
    ('WxPay_QRcode', ''),
    ('WxPay_Status', '1'),
    ('WxPay_Url', 'wx.qq.com'),
    ('WxPay_SyncKey', ''),
    ('Pay_Price', ''),
    ('Pay_Xposed', '0');

-- +goose Down
DROP TABLE `config`;
DROP TABLE `paylist`;
DROP TABLE `telegram_session`;
DROP TABLE `relay`;
DROP TABLE `detect_log`;
DROP TABLE `detect_list`;
DROP TABLE `email_verify`;
DROP TABLE `auto`;
DROP TABLE `ticket`;
DROP TABLE `bought`;
DROP TABLE `coupon`;
DROP TABLE `shop`;
DROP TABLE `payback`;
DROP TABLE `user_token`;
DROP TABLE `ss_password_reset`;
DROP TABLE `ss_invite_code`;
DROP TABLE `speedtest`;
DROP TABLE `radius_ban`;
DROP TABLE `link`;
DROP TABLE `code`;
DROP TABLE `announcement`;
DROP TABLE `login_ip`;
DROP TABLE `disconnect_ip`;
DROP TABLE `unblockip`;
DROP TABLE `blockip`;
DROP TABLE `alive_ip`;
DROP TABLE `user_traffic_log`;
DROP TABLE `ss_node_online_log`;
DROP TABLE `ss_node_info`;
DROP TABLE `ss_node`;
DROP TABLE `user`;
