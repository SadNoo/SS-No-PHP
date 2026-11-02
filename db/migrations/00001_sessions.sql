-- 会话表：由 alexedwards/scs 的 mysqlstore 使用。
-- 新面板使用服务端会话，Cookie 中只保存随机令牌，不保存任何用户信息。

-- +goose Up
CREATE TABLE sessions (
    token  CHAR(43)     NOT NULL,
    data   BLOB         NOT NULL,
    expiry TIMESTAMP(6) NOT NULL,
    PRIMARY KEY (token),
    KEY sessions_expiry_idx (expiry)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE sessions;
