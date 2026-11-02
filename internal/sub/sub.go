// Package sub 生成节点连接信息和订阅内容，对应原版 App\Utils\URL、Tools 中的节点解析函数和 LinkController。
//
// 输出格式（ssr://、ss://、vmess://、ssd://、Clash 配置）与原版逐字节保持一致，
// 唯一的差异是原版在循环中反复查询数据库，这里改为一次性读取后在内存中计算。
package sub

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// 节点类型（ss_node.sort）。
const (
	SortSS         = 0
	SortMuPort     = 9
	SortRelay      = 10
	SortV2Ray      = 11
	SortV2RayRelay = 12
	SortSSV2Ray    = 13
)

var ssAEADMethods = []string{"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305"}
var ssObfs = []string{"simple_obfs_http", "simple_obfs_http_compatible", "simple_obfs_tls", "simple_obfs_tls_compatible"}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// CanMethodConnect 对应 URL::CanMethodConnect：1 仅 SSR，2 仅 SS，3 都可以。
func CanMethodConnect(method string) int {
	if contains(ssAEADMethods, method) {
		return 2
	}
	return 3
}

// CanProtocolConnect 对应 URL::CanProtocolConnect。
func CanProtocolConnect(protocol string) int {
	if protocol != "origin" && !strings.Contains(protocol, "_compatible") {
		return 1
	}
	return 3
}

// CanObfsConnect 对应 URL::CanObfsConnect：4 表示 SSR 需改为 plain，5 表示 SS 需改为 plain。
func CanObfsConnect(obfs string) int {
	if obfs == "plain" {
		return 3
	}
	if contains(ssObfs, obfs) {
		if !strings.Contains(obfs, "_compatible") {
			return 2
		}
		return 4
	}
	if !strings.Contains(obfs, "_compatible") {
		return 1
	}
	return 5
}

// Conn 是生成连接信息所需的用户字段（单端口多用户时会替换为承载用户的参数）。
type Conn struct {
	ID            uint32
	Port          int32
	Passwd        string
	Method        string
	Protocol      string
	ProtocolParam string
	Obfs          string
	ObfsParam     string
}

func connOf(u store.User) Conn {
	return Conn{ID: u.ID, Port: u.Port, Passwd: u.Passwd, Method: u.Method, Protocol: u.Protocol, ProtocolParam: u.ProtocolParam, Obfs: u.Obfs, ObfsParam: u.ObfsParam}
}

// SSCanConnect 对应 URL::SSCanConnect（不含单端口分支）。
func (c Conn) SSCanConnect() bool {
	return CanMethodConnect(c.Method) >= 2 && CanProtocolConnect(c.Protocol) >= 2 && CanObfsConnect(c.Obfs) >= 2
}

// SSRCanConnect 对应 URL::SSRCanConnect（不含单端口分支）。
func (c Conn) SSRCanConnect() bool {
	return CanMethodConnect(c.Method) != 2 && CanProtocolConnect(c.Protocol) != 2 && CanObfsConnect(c.Obfs) != 2
}

// SSConnectInfo 对应 URL::getSSConnectInfo。
func (c Conn) SSConnectInfo() Conn {
	if CanObfsConnect(c.Obfs) == 5 {
		c.Obfs, c.ObfsParam = "plain", ""
	}
	if CanProtocolConnect(c.Protocol) == 3 {
		c.Protocol, c.ProtocolParam = "origin", ""
	}
	c.Obfs = strings.ReplaceAll(c.Obfs, "_compatible", "")
	c.Protocol = strings.ReplaceAll(c.Protocol, "_compatible", "")
	return c
}

// SSRConnectInfo 对应 URL::getSSRConnectInfo。
func (c Conn) SSRConnectInfo() Conn {
	if CanObfsConnect(c.Obfs) == 4 {
		c.Obfs, c.ObfsParam = "plain", ""
	}
	c.Obfs = strings.ReplaceAll(c.Obfs, "_compatible", "")
	c.Protocol = strings.ReplaceAll(c.Protocol, "_compatible", "")
	return c
}

// Item 是一个节点的连接信息（原版 getItem 返回的数组）。
type Item struct {
	Class         int32
	Address       string
	Port          int
	Passwd        string
	Method        string
	Protocol      string
	ProtocolParam string
	Obfs          string
	ObfsParam     string
	Remark        string
	Group         string
	Path          string
}

// ParseArgs 对应 URL::parse_args：把 "a=1|b=2" 解析为 map。
func ParseArgs(origin string) map[string]string {
	out := map[string]string{}
	for _, arg := range strings.Split(origin, "|") {
		k, v, _ := strings.Cut(arg, "=")
		out[k] = v
	}
	return out
}

// V2 是 V2Ray 节点 server 字段解析结果（Tools::v2Array / ssv2Array）。键序与原版 JSON 输出一致。
type V2 struct {
	Keys   []string
	Values map[string]any
	// Origin 是配置外部中转时的原节点地址（add 已换成中转入口）；没有中转时为空。不输出到 JSON。
	Origin string
}

func newV2() *V2 { return &V2{Values: map[string]any{}} }

// clone 返回副本，修改副本不影响原值。
func (v *V2) clone() *V2 {
	c := &V2{Keys: append([]string(nil), v.Keys...), Values: make(map[string]any, len(v.Values)), Origin: v.Origin}
	for k, val := range v.Values {
		c.Values[k] = val
	}
	return c
}

// Set 设置键值，保持首次出现的顺序（与 PHP 关联数组一致）。
func (v *V2) Set(k string, val any) {
	if _, ok := v.Values[k]; !ok {
		v.Keys = append(v.Keys, k)
	}
	v.Values[k] = val
}

// Del 删除键。
func (v *V2) Del(k string) {
	if _, ok := v.Values[k]; !ok {
		return
	}
	delete(v.Values, k)
	for i, x := range v.Keys {
		if x == k {
			v.Keys = append(v.Keys[:i], v.Keys[i+1:]...)
			break
		}
	}
}

// Str 返回字符串值。
func (v *V2) Str(k string) string {
	switch x := v.Values[k].(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// Int 返回整数值。
func (v *V2) Int(k string) int {
	switch x := v.Values[k].(type) {
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}

func phpIntval(s string) int {
	// PHP (int)"443abc" = 443
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || (end == 0 && (s[end] == '-' || s[end] == '+'))) {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}

func mergeArgs(item *V2, extra string) {
	for _, val := range orderedArgs(extra) {
		item.Set(val.k, val.v)
	}
	if hasKey(item, "server") {
		item.Set("add", item.Values["server"])
		item.Del("server")
	}
	if hasKey(item, "outside_port") {
		item.Set("port", phpIntval(item.Str("outside_port")))
		item.Del("outside_port")
	}
}

func hasKey(v *V2, k string) bool { _, ok := v.Values[k]; return ok }

type kv struct{ k, v string }

// orderedArgs 按出现顺序解析 parse_args 格式的参数。
func orderedArgs(origin string) []kv {
	var out []kv
	seen := map[string]int{}
	for _, arg := range strings.Split(origin, "|") {
		k, v, _ := strings.Cut(arg, "=")
		if i, ok := seen[k]; ok {
			out[i].v = v
			continue
		}
		seen[k] = len(out)
		out = append(out, kv{k, v})
	}
	return out
}

// V2Array 对应 Tools::v2Array：解析 V2Ray 节点的 server 字段
// 格式：地址;端口;alterId;传输协议;额外类型;参数（a=b|c=d）
//
// 节点配置了外部中转（;||地址;端口）时，连接地址与端口换成中转入口，SNI / Host 固定为原地址（见 relay.go）。
func V2Array(server string) *V2 {
	base, relay, _ := SplitRelay(server, false)
	item := v2ArrayBase(base)
	applyRelay(item, relay)
	return item
}

// v2ArrayBase 解析不含中转后缀的 VMess 节点配置。
func v2ArrayBase(server string) *V2 {
	parts := strings.Split(server, ";")
	item := newV2()
	item.Set("host", "")
	item.Set("path", "")
	item.Set("tls", "")
	item.Set("add", parts[0])
	if len(parts) < 2 || parts[1] == "0" || parts[1] == "" {
		item.Set("port", 443)
	} else {
		item.Set("port", phpIntval(parts[1]))
	}
	aid := 0
	if len(parts) > 2 {
		aid = phpIntval(parts[2])
	}
	item.Set("aid", aid)
	item.Set("net", "tcp")
	item.Set("type", "none")
	if len(parts) >= 4 {
		item.Set("net", parts[3])
		switch parts[3] {
		case "ws":
			item.Set("path", "/")
		case "tls":
			item.Set("tls", "tls")
		}
	}
	if len(parts) >= 5 {
		if net := item.Str("net"); net == "kcp" || net == "http" {
			item.Set("type", parts[4])
		} else if parts[4] == "ws" {
			item.Set("net", "ws")
		}
	}
	if len(parts) >= 6 {
		mergeArgs(item, parts[5])
	}
	return item
}

// SSV2Array 对应 Tools::ssv2Array：解析 SS + v2ray-plugin 节点（sort 13）的 server 字段。
// 传统 SS 已从订阅中退役，这里不支持外部中转，只解析中转后缀之前的部分。
func SSV2Array(server string) *V2 {
	parts := strings.Split(RealServer(server), ";")
	item := newV2()
	item.Set("host", "")
	item.Set("path", "")
	item.Set("net", "ws")
	item.Set("tls", "")
	item.Set("add", parts[0])
	if len(parts) < 2 || parts[1] == "0" || parts[1] == "" {
		item.Set("port", 443)
	} else {
		item.Set("port", phpIntval(parts[1]))
	}
	if len(parts) >= 4 {
		item.Set("net", parts[3])
		switch parts[3] {
		case "ws":
			item.Set("path", "/")
		case "tls":
			item.Set("tls", "tls")
		}
	}
	if len(parts) >= 5 && parts[4] == "ws" {
		item.Set("net", "ws")
	} else if len(parts) >= 5 && parts[4] == "tls" {
		item.Set("tls", "tls")
	}
	if len(parts) >= 6 {
		mergeArgs(item, parts[5])
	}
	return item
}

// Service 生成订阅内容。
type Service struct {
	Q        *store.Queries
	AppName  string
	SubURL   string
	MergeSub bool
	MuRegex  string
	MuSuffix string
	Loc      *time.Location
	// ProxyDomains 是完整配置中强制走「🍃 Proxy」的域名后缀（配置 app.sub_proxy_domains，已校验）。
	ProxyDomains []string
	// TokenFunc 可替换取得订阅令牌的方法（测试用），为空时使用 SubToken。
	TokenFunc func(ctx context.Context, uid uint32, withoutMu int32) (string, error)
}

// env 是一次订阅计算中预先读取的数据，避免在循环中反复查询数据库。
type env struct {
	nodes    map[uint32]store.SsNode
	relays   []store.Relay
	muUsers  map[int32]*store.User
	plainPrt map[int32]*store.User
}

func (s *Service) load(ctx context.Context, u store.User) (*env, error) {
	all, err := s.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	e := &env{nodes: map[uint32]store.SsNode{}, muUsers: map[int32]*store.User{}, plainPrt: map[int32]*store.User{}}
	for _, n := range all {
		e.nodes[n.ID] = n
	}
	if e.relays, err = s.Q.ListRelayRulesForUser(ctx, u.ID); err != nil {
		return nil, err
	}
	return e, nil
}

// muUser 对应 User::where('port', $mu_port)->where('is_multi_user', '<>', 0)->first()。
func (s *Service) muUser(ctx context.Context, e *env, port int32) (*store.User, error) {
	if u, ok := e.muUsers[port]; ok {
		return u, nil
	}
	u, err := s.Q.GetMuUserByPort(ctx, port)
	if errors.Is(err, sql.ErrNoRows) {
		e.muUsers[port] = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.muUsers[port] = &u
	return &u, nil
}

// SSCanConnectMu 对应 URL::SSCanConnect($user, $mu_port)，模板中使用。
func (s *Service) SSCanConnectMu(ctx context.Context, u store.User, muPort int32) (bool, error) {
	if muPort == 0 {
		return connOf(u).SSCanConnect(), nil
	}
	mu, err := s.Q.GetMuUserByPort(ctx, muPort)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return connOf(mu).SSCanConnect(), nil
}

// SSRCanConnectMu 对应 URL::SSRCanConnect($user, $mu_port)。
func (s *Service) SSRCanConnectMu(ctx context.Context, u store.User, muPort int32) (bool, error) {
	if muPort == 0 {
		return connOf(u).SSRCanConnect(), nil
	}
	mu, err := s.Q.GetMuUserByPort(ctx, muPort)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return connOf(mu).SSRCanConnect(), nil
}

// PickOutRelayRule 对应 Tools::pick_out_relay_rule：为 (节点, 端口) 选出优先级最高的中转规则；阻断规则返回 nil。
func PickOutRelayRule(relayNodeID uint32, port int32, rules []store.Relay) *store.Relay {
	var match *store.Relay
	applies := func(r store.Relay) bool {
		return (r.Port == port || r.Port == 0) && (r.SourceNodeID == 0 || r.SourceNodeID == int32(relayNodeID)) //nolint:gosec // 节点 ID 范围很小
	}
	for i := range rules {
		single := rules[i]
		if !applies(single) {
			continue
		}
		higher := false
		for _, p := range rules {
			if !applies(p) {
				continue
			}
			if (p.Priority > single.Priority && p.ID != single.ID) || (p.Priority == single.Priority && p.ID < single.ID) {
				higher = true
			}
		}
		if higher {
			continue
		}
		match = &rules[i]
	}
	if match != nil && match.DistNodeID == -1 {
		return nil
	}
	return match
}

// relayByID 对应 getItem 中按 ID 取用户可用的中转规则。
func relayByID(rules []store.Relay, id uint64) *store.Relay {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

func (e *env) distNode(r *store.Relay) (store.SsNode, bool) {
	if r == nil || r.DistNodeID <= 0 {
		return store.SsNode{}, false
	}
	n, ok := e.nodes[uint32(r.DistNodeID)]
	return n, ok
}

// getItem 对应 URL::getItem。返回 nil 表示该用户无法连接此节点。
func (s *Service) getItem(ctx context.Context, e *env, u store.User, node store.SsNode, muPort int32, relayRuleID uint64, isSS bool) (*Item, error) {
	nodeName := node.Name
	if r := relayByID(e.relays, relayRuleID); r != nil {
		if dn, ok := e.distNode(r); ok {
			nodeName += " - " + dn.Name
		}
	}
	mu := model.NewUser(u, s.Loc)
	c := connOf(u)
	if muPort != 0 {
		muUser, err := s.muUser(ctx, e, muPort)
		if err != nil || muUser == nil {
			return nil, err
		}
		c = connOf(*muUser)
		c.ObfsParam = mu.MuMd5(s.MuRegex, s.MuSuffix)
		c.ProtocolParam = fmt.Sprintf("%d:%s", u.ID, u.Passwd)
		if !s.MergeSub {
			nodeName += fmt.Sprintf(" - %d 单端口", muPort)
		}
	}
	if isSS {
		if !c.SSCanConnect() {
			return nil, nil
		}
		c = c.SSConnectInfo()
	} else {
		if !c.SSRCanConnect() {
			return nil, nil
		}
		c = c.SSRConnectInfo()
	}
	it := &Item{Class: node.NodeClass, Passwd: c.Passwd, Method: c.Method, Remark: nodeName, Group: s.AppName}
	if node.Sort == SortSSV2Ray {
		v := SSV2Array(node.Server)
		it.Address = v.Str("add")
		it.Port = v.Int("port")
		it.Protocol, it.ProtocolParam = "origin", ""
		it.Path = v.Str("path")
		it.Obfs = "v2ray"
		// 原版此处使用的是“当前连接用户”的 getMuMd5（单端口时为承载用户）。
		host := model.NewUser(s.connUser(u, c), s.Loc).MuMd5(s.MuRegex, s.MuSuffix)
		security := "none"
		if v.Str("tls") == "tls" && v.Str("net") == "ws" {
			security = "tls"
		}
		it.ObfsParam = "mode=ws;security=" + security + ";path=" + v.Str("path") + ";host=" + host
	} else {
		it.Address = node.Server
		it.Port = int(c.Port)
		it.Protocol, it.ProtocolParam = c.Protocol, c.ProtocolParam
		it.Obfs, it.ObfsParam = c.Obfs, c.ObfsParam
	}
	if muPort != 0 && !s.MergeSub {
		it.Group += " - 单端口"
	}
	return it, nil
}

// connUser 把连接参数套回用户记录，用于计算 getMuMd5（原版在替换为承载用户后调用）。
func (s *Service) connUser(u store.User, c Conn) store.User {
	u.ID, u.Passwd, u.Method, u.Obfs, u.Protocol = c.ID, c.Passwd, c.Method, c.Obfs, c.Protocol
	return u
}

// AllItems 对应 URL::getAllItems。isMu：0 普通端口，1 全部单端口，其他为指定单端口端口号。
func (s *Service) AllItems(ctx context.Context, u store.User, isMu int32, isSS bool) ([]Item, error) {
	e, err := s.load(ctx, u)
	if err != nil {
		return nil, err
	}
	return s.allItems(ctx, e, u, isMu, isSS)
}

func (s *Service) allItems(ctx context.Context, e *env, u store.User, isMu int32, isSS bool) ([]Item, error) {
	admin := 0
	if u.IsAdmin != 0 {
		admin = 1
	}
	nodes, err := s.Q.ListSubNodes(ctx, store.ListSubNodesParams{IsAdmin: admin, NodeGroup: u.NodeGroup, Class: u.Class})
	if err != nil {
		return nil, err
	}
	var muNodes []store.SsNode
	if isMu != 0 {
		all, err := s.Q.ListMuPortNodes(ctx, store.ListMuPortNodesParams{IsAdmin: admin, NodeGroup: u.NodeGroup, Class: u.Class})
		if err != nil {
			return nil, err
		}
		for _, n := range all {
			if isMu == 1 || n.Server == strconv.Itoa(int(isMu)) {
				muNodes = append(muNodes, n)
			}
		}
	}
	var out []Item
	add := func(it *Item, err error) error {
		if err != nil {
			return err
		}
		if it != nil {
			out = append(out, *it)
		}
		return nil
	}
	for _, node := range nodes {
		if node.MuOnly != 1 && isMu == 0 {
			var ruleID uint64
			if node.Sort == SortRelay {
				if r := PickOutRelayRule(node.ID, u.Port, e.relays); r != nil {
					if _, ok := e.distNode(r); ok {
						ruleID = r.ID
					}
				}
			}
			if err := add(s.getItem(ctx, e, u, node, 0, ruleID, isSS)); err != nil {
				return nil, err
			}
		}
		if node.CustomRss == 1 && node.MuOnly != -1 && isMu != 0 {
			for _, mn := range muNodes {
				port := int32(phpIntval(mn.Server)) //nolint:gosec // 端口号
				var ruleID uint64
				if node.Sort == SortRelay {
					if r := PickOutRelayRule(node.ID, port, e.relays); r != nil {
						if _, ok := e.distNode(r); ok {
							ruleID = r.ID
						}
					}
				}
				if err := add(s.getItem(ctx, e, u, node, port, ruleID, isSS)); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// B64 对应 Tools::base64_url_encode。
func B64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// rawURLEncode 对应 PHP rawurlencode（RFC 3986，空格编码为 %20）。
func rawURLEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// ItemURL 对应 URL::getItemUrl。isSS：0 生成 ssr://，1 生成 SIP002 ss://，2 生成旧版 ss://。
func (s *Service) ItemURL(it Item, isSS int) string {
	if isSS == 0 {
		ssurl := it.Address + ":" + strconv.Itoa(it.Port) + ":" + it.Protocol + ":" + it.Method + ":" + it.Obfs + ":" + B64(it.Passwd) +
			"/?obfsparam=" + B64(it.ObfsParam) + "&protoparam=" + B64(it.ProtocolParam) + "&remarks=" + B64(it.Remark) + "&group=" + B64(it.Group)
		return "ssr://" + B64(ssurl)
	}
	if isSS == 2 {
		info := it.Method + ":" + it.Passwd + "@" + it.Address + ":" + strconv.Itoa(it.Port)
		return "ss://" + B64(info) + "#" + rawURLEncode(s.AppName+" - "+it.Remark)
	}
	ssurl := "ss://" + B64(it.Method+":"+it.Passwd) + "@" + it.Address + ":" + strconv.Itoa(it.Port)
	if it.Obfs == "v2ray" || contains(ssObfs, it.Obfs) {
		plugin := ""
		switch {
		case strings.Contains(it.Obfs, "http"):
			plugin = "obfs-local;obfs=http"
		case strings.Contains(it.Obfs, "tls"):
			plugin = "obfs-local;obfs=tls"
		default:
			plugin = "v2ray;" + it.ObfsParam
		}
		if it.ObfsParam != "" && it.Obfs != "v2ray" {
			plugin += ";obfs-host=" + it.ObfsParam
		}
		ssurl += "?plugin=" + rawURLEncode(plugin)
	}
	return ssurl + "#" + rawURLEncode(s.AppName+" - "+it.Remark)
}

// AllURL 对应 URL::getAllUrl。
func (s *Service) AllURL(ctx context.Context, u store.User, isMu int32, isSS int, now time.Time) (string, error) {
	var b strings.Builder
	if isSS == 0 {
		b.WriteString(s.userTraffic(u, isMu, now) + "\n")
		b.WriteString(s.userClassExpiration(u, isMu, now) + "\n")
	}
	if u.ExpireIn.Before(now) {
		return b.String(), nil
	}
	e, err := s.load(ctx, u)
	if err != nil {
		return "", err
	}
	var items []Item
	if s.MergeSub {
		mu, err := s.allItems(ctx, e, u, 1, isSS != 0)
		if err != nil {
			return "", err
		}
		normal, err := s.allItems(ctx, e, u, 0, isSS != 0)
		if err != nil {
			return "", err
		}
		items = append(mu, normal...)
	} else if items, err = s.allItems(ctx, e, u, isMu, isSS != 0); err != nil {
		return "", err
	}
	for _, it := range items {
		b.WriteString(s.ItemURL(it, isSS) + "\n")
	}
	return b.String(), nil
}

const fakeSSR = "www.google.com:%d:auth_chain_a:chacha20:tls1.2_ticket_auth:YnJlYWt3YWxs/?obfsparam=&protoparam=&remarks=%s&group=%s"

func (s *Service) groupName(isMu int32) string {
	if isMu == 1 && !s.MergeSub {
		return s.AppName + " - 单端口"
	}
	return s.AppName
}

// userTraffic 对应 URL::getUserTraffic：订阅第一行显示剩余流量。
func (s *Service) userTraffic(u store.User, isMu int32, now time.Time) string {
	var remark string
	if u.ExpireIn.After(now) {
		percent := "0.00%"
		if u.TransferEnable != 0 {
			percent = util.NumberFormat(float64(u.TransferEnable-u.U-u.D)/float64(u.TransferEnable)*100, 2) + "%"
		}
		remark = "剩余流量：" + percent + " " + model.NewUser(u, s.Loc).UnusedTraffic()
	} else {
		remark = "账户已过期，请续费后使用"
	}
	return "ssr://" + B64(fmt.Sprintf(fakeSSR, 1, B64(remark), B64(s.groupName(isMu))))
}

// userClassExpiration 对应 URL::getUserClassExpiration：订阅第二行显示等级到期时间。
func (s *Service) userClassExpiration(u store.User, isMu int32, now time.Time) string {
	var remark string
	if u.ExpireIn.After(now) {
		remark = "过期时间：" + model.NewUser(u, s.Loc).ClassExpireStr()
	} else {
		remark = "账户已过期，请续费后使用"
	}
	return "ssr://" + B64(fmt.Sprintf(fakeSSR, 2, B64(remark), B64(s.groupName(isMu))))
}

// SubToken 对应 LinkController::GenerateSSRSubCode：取得或创建用户的订阅令牌。
func (s *Service) SubToken(ctx context.Context, uid uint32, withoutMu int32) (string, error) {
	l, err := s.Q.GetSubLink(ctx, store.GetSubLinkParams{Userid: uid, Geo: withoutMu})
	if err == nil {
		return l.Token, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	for i := 0; i < 10; i++ {
		token := util.RandomChar(16)
		if n, err := s.Q.CountLinkToken(ctx, token); err != nil {
			return "", err
		} else if n > 0 {
			continue
		}
		if err := s.Q.CreateSubLink(ctx, store.CreateSubLinkParams{Token: token, Userid: uid, Geo: withoutMu}); err != nil {
			return "", err
		}
		return token, nil
	}
	return "", errors.New("无法分配订阅令牌")
}

// GetItem 对应 URL::getItem，用于节点详情页。返回 nil 表示该用户无法连接此节点。
func (s *Service) GetItem(ctx context.Context, u store.User, node store.SsNode, muPort int32, relayRuleID uint64, isSS bool) (*Item, error) {
	e, err := s.load(ctx, u)
	if err != nil {
		return nil, err
	}
	return s.getItem(ctx, e, u, node, muPort, relayRuleID, isSS)
}

// JSONObfs 对应 URL::getJsonObfs：节点详情页 JSON 配置中的 plugin 字段。
// 原版在 obfs 与 --obfs-host 之间漏了空格（生成 "obfs-local --obfs http--obfs-host ..."），这里补上（K29）。
func JSONObfs(it Item) string {
	if !contains(ssObfs, it.Obfs) {
		return ""
	}
	plugin := "obfs-local --obfs tls"
	if strings.Contains(it.Obfs, "http") {
		plugin = "obfs-local --obfs http"
	}
	if it.ObfsParam != "" {
		plugin += " --obfs-host " + it.ObfsParam
	}
	return plugin
}

// WithConn 返回套用了连接参数（协议、混淆）的用户副本，对应模板中 $user = URL::getSSConnectInfo($pre_user)。
func WithConn(u store.User, c Conn) store.User {
	u.Protocol, u.ProtocolParam, u.Obfs, u.ObfsParam = c.Protocol, c.ProtocolParam, c.Obfs, c.ObfsParam
	return u
}

// ConnOf 取得用户的连接参数。
func ConnOf(u store.User) Conn { return connOf(u) }
