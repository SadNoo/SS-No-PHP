package model

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// ShopItem 是商品 content 字段（JSON 对象）中的一项，保留原有顺序（原版按顺序生成说明文字并逐项生效）。
type ShopItem struct {
	Key   string
	Value json.RawMessage
}

// Shop 包装商品记录，提供原版 Models\Shop 中的方法。
type Shop struct {
	store.Shop
	Items []ShopItem
}

// NewShop 解析商品内容。content 不是合法 JSON 对象时视为空。
func NewShop(s store.Shop) *Shop {
	return &Shop{Shop: s, Items: parseObject(s.Content)}
}

func parseObject(s string) []ShopItem {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var out []ShopItem
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return out
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out
		}
		// 原版 json_decode 为数组，重复的键以最后一次为准。
		replaced := false
		for i := range out {
			if out[i].Key == key {
				out[i].Value, replaced = raw, true
			}
		}
		if !replaced {
			out = append(out, ShopItem{key, raw})
		}
	}
	return out
}

// get 取得某一项，不存在时返回 nil。
func (s *Shop) get(key string) json.RawMessage {
	for _, it := range s.Items {
		if it.Key == key {
			return it.Value
		}
	}
	return nil
}

// PHPString 按 PHP 输出规则把 JSON 值转为字符串（字符串原样，数字按 PHP 格式，true 为 "1"）。
func PHPString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return ""
	}
	if string(raw) == "true" {
		return "1"
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return strconv.FormatInt(n, 10)
	}
	if f, err := strconv.ParseFloat(string(raw), 64); err == nil {
		return util.PHPFloat(f)
	}
	return string(raw)
}

// PHPNumber 按 PHP 宽松规则把 JSON 值转为数字（字符串取前导数字部分）。
func PHPNumber(raw json.RawMessage) float64 {
	s := strings.TrimSpace(PHPString(raw))
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || (end == 0 && (s[end] == '-' || s[end] == '+'))) {
		end++
	}
	f, _ := strconv.ParseFloat(s[:end], 64)
	return f
}

// value 对应原版 $content->key ?? 0。
func (s *Shop) value(key string) string {
	if v := s.get(key); v != nil {
		return PHPString(v)
	}
	return "0"
}

// Num 取某一项的数值（不存在为 0）。
func (s *Shop) Num(key string) float64 { return PHPNumber(s.get(key)) }

// Has 是否包含某一项。
func (s *Shop) Has(key string) bool { return s.get(key) != nil }

// 以下方法对应原版 Shop 模型中同名的方法，模板中使用。

func (s *Shop) Bandwidth() string   { return s.value("bandwidth") }
func (s *Shop) Expire() string      { return s.value("expire") }
func (s *Shop) Reset() string       { return s.value("reset") }
func (s *Shop) ResetValue() string  { return s.value("reset_value") }
func (s *Shop) ResetExp() string    { return s.value("reset_exp") }
func (s *Shop) UserClass() string   { return s.value("class") }
func (s *Shop) ClassExpire() string { return s.value("class_expire") }
func (s *Shop) Speedlimit() string  { return s.value("speedlimit") }
func (s *Shop) Connector() string   { return s.value("connector") }

// ContentExtra 对应 Shop::content_extra：以 ; 分隔的附加说明，每项为 [图标, 文字]，没有图标时为 check。
func (s *Shop) ContentExtra() [][]string {
	v := s.get("content_extra")
	if v == nil {
		return nil
	}
	var out [][]string
	for _, part := range strings.Split(PHPString(v), ";") {
		if !strings.Contains(part, "-") {
			part = "check-" + part
		}
		out = append(out, strings.Split(part, "-"))
	}
	return out
}

// ContentExtraText 后台编辑页显示的附加说明（原版模板把 content_extra 各项用 - 与 ; 重新拼接）。
func (s *Shop) ContentExtraText() string {
	items := s.ContentExtra()
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, strings.Join(it, "-"))
	}
	return strings.Join(parts, ";")
}

// Content 对应 Shop::content：商品说明文字。
func (s *Shop) Content() string {
	var b strings.Builder
	for _, it := range s.Items {
		v := PHPString(it.Value)
		switch it.Key {
		case "bandwidth":
			b.WriteString("添加流量 " + v + " G ")
		case "expire":
			b.WriteString(", 为账号的有效期添加 " + v + " 天 ")
		case "class":
			b.WriteString(", 为账号升级为等级 " + v + " , 有效期 " + PHPString(s.get("class_expire")) + " 天 ")
		case "reset":
			b.WriteString(", 在 " + PHPString(s.get("reset_exp")) + " 天内 ，每 " + v + " 天重置流量为 " + PHPString(s.get("reset_value")) + " G ")
		case "speedlimit":
			if PHPNumber(it.Value) == 0 {
				b.WriteString(", 用户端口不限速 ")
			} else {
				b.WriteString(", 用户端口限速变为" + v + " Mbps ")
			}
		case "connector":
			if PHPNumber(it.Value) != 0 { // 只作说明，购买不修改用户的 IP 数（见 Apply）
				b.WriteString(", 同时在线 IP " + v + " 个")
			}
		}
	}
	return strings.TrimRight(b.String(), ",")
}

// Apply 对应 Shop::buy：把商品内容（流量、有效期、等级、限速、设备数）按顺序作用到用户上。
// renew 为 true 表示自动续费（原版 $is_renew）。boughtReset / boughtExtend 对应配置 enable_bought_reset / enable_bought_extend。
func (s *Shop) Apply(u *store.User, renew, boughtReset, boughtExtend bool, now time.Time) {
	days := func(key string) time.Duration { return time.Duration(s.Num(key) * 86400 * float64(time.Second)) }
	for _, it := range s.Items {
		v := PHPNumber(it.Value)
		switch it.Key {
		case "bandwidth":
			bytes := int64(v * util.GB)
			reset := (!renew && boughtReset) || (renew && s.AutoResetBandwidth == 1)
			if reset {
				u.TransferEnable, u.U, u.D, u.LastDayT = bytes, 0, 0, 0
			} else {
				u.TransferEnable += bytes
			}
		case "expire":
			add := days("expire")
			if now.After(u.ExpireIn) {
				u.ExpireIn = now.Add(add)
			} else {
				u.ExpireIn = u.ExpireIn.Add(add)
			}
		case "class":
			class := int32(v)
			if boughtExtend && u.Class == class {
				u.ClassExpire = u.ClassExpire.Add(days("class_expire"))
			} else {
				u.ClassExpire = now.Add(days("class_expire"))
			}
			u.Class = class
			// 原版开启 enable_bought_extend 时此处缺少 break，会继续执行 speedlimit 分支，
			// 把用户限速改成等级数值（K26），这里不再照搬。
		case "speedlimit":
			u.NodeSpeedlimit = dec.FromFloat(v)
		case "connector":
			// 购买套餐不修改用户的同时在线 IP 数：默认值由注册配置决定，管理员给个别用户设置的值（含 0 不限）不被套餐覆盖。
			// 套餐中的“IP限制”只用于展示。
		}
	}
}
