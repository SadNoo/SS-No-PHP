// Package cloudflare 更新 Cloudflare DNS 的 A 记录（对应原版 Utils\CloudflareDriver）。
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client 是 Cloudflare API 客户端。
//
// 认证方式：设置 Email 时使用旧的 Global API Key（X-Auth-Email / X-Auth-Key，与原版一致）；
// Email 为空时把 Key 当作 API Token（推荐，只需授予该域名的 DNS 编辑权限）。
type Client struct {
	Email   string
	Key     string
	Zone    string // 域名，例如 example.com（原版 cloudflare_name）
	BaseURL string // 默认 https://api.cloudflare.com/client/v4，测试时替换
	HTTP    *http.Client
}

// New 创建客户端。
func New(email, key, zone string) *Client {
	return &Client{Email: email, Key: key, Zone: zone, BaseURL: "https://api.cloudflare.com/client/v4", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

type response struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Email != "" {
		req.Header.Set("X-Auth-Email", c.Email)
		req.Header.Set("X-Auth-Key", c.Key)
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return fmt.Errorf("cloudflare 返回格式错误（%d）", resp.StatusCode)
	}
	if !r.Success {
		msg := ""
		for _, e := range r.Errors {
			msg += fmt.Sprintf(" %d %s", e.Code, e.Message)
		}
		return fmt.Errorf("cloudflare 返回错误:%s", msg)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

type record struct {
	ID string `json:"id"`
}

// UpdateA 把 fqdn 的 A 记录指向 ip：不存在时添加（TTL 120、不代理），存在时逐条修改（对应 updateRecord）。
func (c *Client) UpdateA(ctx context.Context, fqdn, ip string) error {
	var zones []record
	if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(c.Zone), nil, &zones); err != nil {
		return err
	}
	if len(zones) == 0 {
		return errors.New("cloudflare 中没有找到域名 " + c.Zone)
	}
	zone := zones[0].ID
	var recs []record
	q := url.Values{"type": {"A"}, "name": {fqdn}}
	if err := c.do(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?"+q.Encode(), nil, &recs); err != nil {
		return err
	}
	if len(recs) == 0 {
		return c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records",
			map[string]any{"type": "A", "name": fqdn, "content": ip, "ttl": 120, "proxied": false}, nil)
	}
	for _, r := range recs {
		if err := c.do(ctx, http.MethodPatch, "/zones/"+zone+"/dns_records/"+r.ID,
			map[string]any{"type": "A", "name": fqdn, "content": ip, "proxied": false}, nil); err != nil {
			return err
		}
	}
	return nil
}
