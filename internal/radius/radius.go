// Package radius 维护 FreeRADIUS 数据库中的帐号与 NAS（对应原版 Utils\Radius 与 Command\SyncRadius）。
//
// FreeRADIUS 使用自己的数据库（标准 SQL 模块表结构：radcheck、radusergroup、nas、radpostauth、radacct），
// 面板只写入帐号与 NAS、读取认证与计费记录。
package radius

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Client 是 Radius 数据库客户端。
type Client struct {
	DB     *sql.DB
	Secret string // 写入 NAS 表的共享密钥（radius_secret）
}

// UserName 对应 Radius::GetUserName：邮箱去掉 @ 和 . 作为 Radius 用户名。
func UserName(email string) string {
	return strings.NewReplacer("@", "", ".", "").Replace(email)
}

// Exists 该邮箱是否已有 Radius 帐号。
func (c *Client) Exists(ctx context.Context, email string) (bool, error) {
	var id int64
	err := c.DB.QueryRowContext(ctx, "SELECT id FROM radcheck WHERE username = ? LIMIT 1", UserName(email)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Create 新建帐号（Cleartext-Password，归入 user 组）。
func (c *Client) Create(ctx context.Context, email, pwd string) error {
	name := UserName(email)
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "INSERT INTO radcheck (username, attribute, op, value) VALUES (?, 'Cleartext-Password', ':=', ?)", name, pwd); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO radusergroup (username, groupname, priority) VALUES (?, 'user', 0)", name); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPassword 修改已有帐号的密码。
func (c *Client) SetPassword(ctx context.Context, email, pwd string) error {
	_, err := c.DB.ExecContext(ctx, "UPDATE radcheck SET value = ? WHERE username = ?", pwd, UserName(email))
	return err
}

// Rename 修改已有帐号的用户名与密码（Radius::ChangeUserName 中帐号已存在的情况）。
func (c *Client) Rename(ctx context.Context, oldEmail, newEmail, pwd string) error {
	from, to := UserName(oldEmail), UserName(newEmail)
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE radcheck SET username = ?, value = ? WHERE username = ?", to, pwd, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE radusergroup SET username = ? WHERE username = ?", to, from); err != nil {
		return err
	}
	return tx.Commit()
}

// Delete 对应 Radius::Delete。
func (c *Client) Delete(ctx context.Context, email string) error {
	name := UserName(email)
	if _, err := c.DB.ExecContext(ctx, "DELETE FROM radcheck WHERE username = ?", name); err != nil {
		return err
	}
	_, err := c.DB.ExecContext(ctx, "DELETE FROM radusergroup WHERE username = ?", name)
	return err
}

// AddNas 对应 Radius::AddNas：以 IP 作为 NAS 名称，已存在时不重复添加。
func (c *Client) AddNas(ctx context.Context, ip string) error {
	if ip == "" {
		return nil
	}
	var id int64
	err := c.DB.QueryRowContext(ctx, "SELECT id FROM nas WHERE shortname = ? LIMIT 1", ip).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = c.DB.ExecContext(ctx, "INSERT INTO nas (nasname, shortname, type, secret, description) VALUES (?, ?, 'other', ?, ?)", ip, ip, c.Secret, ip)
	return err
}

// DelNas 对应 Radius::DelNas。
func (c *Client) DelNas(ctx context.Context, ip string) error {
	_, err := c.DB.ExecContext(ctx, "DELETE FROM nas WHERE shortname = ?", ip)
	return err
}

// Now 返回 Radius 数据库服务器的当前时间（与 FreeRADIUS 写入记录时使用的时钟一致）。
func (c *Client) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := c.DB.QueryRowContext(ctx, "SELECT NOW()").Scan(&t)
	return t, err
}

// AcceptedLogins 返回 (from, to] 内认证成功的用户名（每次认证一条）。
func (c *Client) AcceptedLogins(ctx context.Context, from, to time.Time) ([]string, error) {
	rows, err := c.DB.QueryContext(ctx, "SELECT username FROM radpostauth WHERE authdate > ? AND authdate <= ? AND reply = 'Access-Accept'", from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Acct 是一条已结束会话的计费记录。
type Acct struct {
	UserName string
	Input    int64 // acctinputoctets：用户上传
	Output   int64 // acctoutputoctets：用户下载
}

// FinishedSessions 返回 (from, to] 内结束的会话。
func (c *Client) FinishedSessions(ctx context.Context, from, to time.Time) ([]Acct, error) {
	rows, err := c.DB.QueryContext(ctx, "SELECT username, COALESCE(acctinputoctets, 0), COALESCE(acctoutputoctets, 0) FROM radacct WHERE acctstoptime > ? AND acctstoptime <= ?", from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Acct
	for rows.Next() {
		var a Acct
		if err := rows.Scan(&a.UserName, &a.Input, &a.Output); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
