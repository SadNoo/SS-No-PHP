// Package db 负责建立 MySQL 连接，并提供基于 MySQL GET_LOCK 的分布式锁。
package db

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/SadNoo/SS-No-PHP/internal/config"
)

const tlsConfigName = "niuniu"

// Open 根据配置建立连接池并验证连通性。
//
// 连接统一使用 utf8mb4 字符集、UTC 会话时区，时间字段由程序按业务时区换算。
func Open(ctx context.Context, c config.Database) (*sql.DB, error) {
	mc := mysql.NewConfig()
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	mc.User = c.User
	mc.Passwd = c.Password
	mc.DBName = c.Name
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.Collation = "utf8mb4_unicode_ci"
	mc.Timeout = c.ConnectTimeout.D()
	mc.ReadTimeout = 60 * time.Second
	mc.WriteTimeout = 60 * time.Second
	mc.MultiStatements = false
	// 由驱动把参数安全转义后拼入语句（字符集固定为 utf8mb4，驱动会按服务器的 NO_BACKSLASH_ESCAPES 状态转义），
	// 每条带参数的查询只需一次往返；关闭时驱动对每条查询分别执行 Prepare、Execute、Close，共三次往返。
	mc.InterpolateParams = true
	if !c.ServerTimeZone {
		mc.Params = map[string]string{"time_zone": "'+00:00'"}
	}

	switch c.TLSMode {
	case "verify":
		tc, err := buildTLS(c)
		if err != nil {
			return nil, err
		}
		// 每个数据库（面板库、Radius 库）使用各自的 TLS 配置。
		name := tlsConfigName + "-" + mc.Addr + "/" + c.Name
		if err := mysql.RegisterTLSConfig(name, tc); err != nil {
			return nil, fmt.Errorf("注册数据库 TLS 配置: %w", err)
		}
		mc.TLSConfig = name
	case "disabled":
		mc.TLSConfig = "false"
	default:
		return nil, fmt.Errorf("未知的 tls_mode: %q", c.TLSMode)
	}

	connector, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接器: %w", err)
	}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(c.MaxOpenConns)
	pool.SetMaxIdleConns(c.MaxIdleConns)
	pool.SetConnMaxLifetime(c.ConnMaxLifetime.D())

	pingCtx, cancel := context.WithTimeout(ctx, c.ConnectTimeout.D())
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("连接数据库 %s: %w", mc.Addr, err)
	}
	return pool, nil
}

// buildTLS 构造强制校验服务器证书和主机名的 TLS 配置。不提供跳过校验的选项。
func buildTLS(c config.Database) (*tls.Config, error) {
	tc := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.Host,
	}
	if c.TLSServerName != "" {
		tc.ServerName = c.TLSServerName
	}
	if c.TLSCA != "" {
		pem, err := os.ReadFile(c.TLSCA)
		if err != nil {
			return nil, fmt.Errorf("读取数据库 CA 证书: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("数据库 CA 证书文件中没有有效证书")
		}
		tc.RootCAs = pool
	}
	if c.TLSClientCert != "" || c.TLSClientKey != "" {
		cert, err := tls.LoadX509KeyPair(c.TLSClientCert, c.TLSClientKey)
		if err != nil {
			return nil, fmt.Errorf("读取数据库客户端证书: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}

// ErrLockNotAcquired 表示锁已被其他进程持有。
var ErrLockNotAcquired = errors.New("锁已被其他进程持有")

// Lock 是一把基于 MySQL GET_LOCK 的命名锁。锁绑定在一条专用连接上，
// 进程退出或连接断开时 MySQL 会自动释放，不会出现死锁残留。
type Lock struct {
	conn *sql.Conn
	name string
}

// AcquireLock 尝试在 wait 时间内获取命名锁。
func AcquireLock(ctx context.Context, pool *sql.DB, name string, wait time.Duration) (*Lock, error) {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", name, int(wait.Seconds())).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("获取锁 %s: %w", name, err)
	}
	if !got.Valid || got.Int64 != 1 {
		_ = conn.Close()
		return nil, ErrLockNotAcquired
	}
	return &Lock{conn: conn, name: name}, nil
}

// Release 释放锁并归还连接。
func (l *Lock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	_, err := l.conn.ExecContext(ctx, "SELECT RELEASE_LOCK(?)", l.name)
	closeErr := l.conn.Close()
	l.conn = nil
	return errors.Join(err, closeErr)
}
