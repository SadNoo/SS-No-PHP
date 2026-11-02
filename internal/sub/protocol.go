package sub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// maxProtocolBaseLen 是 VLESS / AnyTLS 原节点配置（不含中转后缀）的长度上限：
// 原为 128 字节，放宽到 448，留出中转后缀（最长约 60 个字符）后总长仍不超过 MaxServerLen。
const maxProtocolBaseLen = 448

const (
	SortSS2022   = 14
	SortVLESS    = 15
	SortAnyTLS   = 16
	SS2022Method = "2022-blake3-aes-256-gcm"
)

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
var shortIDRE = regexp.MustCompile(`^(?:[0-9a-fA-F]{2}){0,8}$`)
var versionRE = regexp.MustCompile(`^[0-9.]{0,32}$`)

// ProtocolConfig contains public node settings. REALITY private keys never belong here.
type ProtocolConfig struct {
	Protocol   string `json:"protocol"`
	Server     string `json:"server"`
	Port       int    `json:"server_port"`
	Method     string `json:"method,omitempty"`
	ServerKey  string `json:"server_key,omitempty"`
	SNI        string `json:"server_name,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
	ShortID    string `json:"short_id,omitempty"`
	Flow       string `json:"flow,omitempty"`
	Transport  string `json:"transport,omitempty"`
	MinVersion string `json:"min_client_version,omitempty"`
	// Relay 是客户端连接的外部中转入口，只用于订阅与节点详情，不下发给节点后端。
	Relay *Relay `json:"-"`
}

func validHost(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	if len(s) == 0 || len(s) > 253 || !hostnameRE.MatchString(s) {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

func portNumber(s string, defaultPort bool) (int, error) {
	if defaultPort && (s == "" || s == "0") {
		return 443, nil
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("端口必须为数字")
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, errors.New("端口必须在 1 到 65535 之间")
	}
	return n, nil
}

// ParseProtocol validates the established sort=14/15 storage formats without changing credentials.
//
// server 可以带外部中转后缀（;||地址;端口），返回的 Server/Port 为真实节点，中转入口在 Relay 中。
func ParseProtocol(sortID int32, server string) (ProtocolConfig, error) {
	c, err := parseProtocolBase(sortID, server)
	if err != nil {
		return c, err
	}
	_, c.Relay, _ = SplitRelay(strings.TrimSpace(server), true)
	return c, nil
}

func parseProtocolBase(sortID int32, server string) (ProtocolConfig, error) {
	c := ProtocolConfig{}
	server = strings.TrimSpace(server)
	if err := ValidateServerText(server); err != nil {
		return c, err
	}
	server, _, err := SplitRelay(server, true)
	if err != nil {
		return c, err
	}
	p := strings.Split(server, ";")
	if sortID == SortSS2022 {
		if len(p) != 3 {
			return c, errors.New("SS2022 格式应为 host;port;server_key_base64")
		}
		for i := range p {
			p[i] = strings.TrimSpace(p[i])
		}
		key, err := base64.StdEncoding.DecodeString(p[2])
		if err != nil || len(key) != 32 {
			return c, errors.New("SS2022 server key 必须为 32 字节标准 Base64")
		}
		port, err := portNumber(p[1], true)
		if err != nil {
			return c, err
		}
		if !validHost(p[0]) {
			return c, errors.New("SS2022 host 无效")
		}
		return ProtocolConfig{Protocol: "shadowsocks", Server: p[0], Port: port, Method: SS2022Method, ServerKey: p[2]}, nil
	}
	if sortID == SortAnyTLS {
		if len(server) > maxProtocolBaseLen || len(p) != 6 || p[2] != "0" || p[3] != "tcp" || p[4] != "anytls" || !strings.HasPrefix(p[5], "sni=") {
			return c, errors.New("AnyTLS 格式应为 host;port;0;tcp;anytls;sni=hostname，原节点配置最长 448 个字符")
		}
		port, err := portNumber(p[1], false)
		if err != nil {
			return c, err
		}
		sni := strings.TrimPrefix(p[5], "sni=")
		if !validHost(p[0]) || port == 80 || !validHost(sni) || net.ParseIP(sni) != nil {
			return c, errors.New("AnyTLS host、SNI 或端口无效")
		}
		return ProtocolConfig{Protocol: "anytls", Server: p[0], Port: port, SNI: sni, Transport: "tcp"}, nil
	}
	if sortID != SortVLESS {
		return c, errors.New("不是定制协议节点")
	}
	if len(server) > maxProtocolBaseLen || len(p) != 6 || p[2] != "0" || strings.ToLower(p[3]) != "tcp" || strings.ToLower(p[4]) != "reality" {
		return c, errors.New("VLESS REALITY 格式应为 host;port;0;tcp;reality;sni=...|pbk=...|sid=...，原节点配置最长 448 个字符")
	}
	port, err := portNumber(p[1], false)
	if err != nil {
		return c, err
	}
	if !validHost(p[0]) || port == 80 {
		return c, errors.New("REALITY 地址无效或使用了保留的 80 端口")
	}
	opts := map[string]string{}
	for _, part := range strings.Split(p[5], "|") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return c, errors.New("REALITY 选项格式无效")
		}
		if _, exists := opts[key]; exists {
			return c, errors.New("REALITY 选项重复")
		}
		switch key {
		case "sni", "pbk", "sid", "target", "minver":
		default:
			return c, errors.New("REALITY 选项未知")
		}
		opts[key] = value
	}
	for _, key := range []string{"sni", "pbk", "sid"} {
		if _, ok := opts[key]; !ok {
			return c, fmt.Errorf("缺少 REALITY %s", key)
		}
	}
	pub, err := base64.RawURLEncoding.DecodeString(opts["pbk"])
	if err != nil || len(pub) != 32 || len(opts["pbk"]) != 43 {
		return c, errors.New("REALITY 公钥无效")
	}
	if net.ParseIP(opts["sni"]) != nil || !validHost(opts["sni"]) || !shortIDRE.MatchString(opts["sid"]) || !versionRE.MatchString(opts["minver"]) {
		return c, errors.New("REALITY SNI、short ID 或版本无效")
	}
	if target, ok := opts["target"]; ok {
		host, port, err := net.SplitHostPort(target)
		if err != nil || !validHost(host) {
			return c, errors.New("REALITY target 无效")
		}
		if _, err := portNumber(port, false); err != nil {
			return c, err
		}
	}
	return ProtocolConfig{Protocol: "vless", Server: p[0], Port: port, SNI: opts["sni"], PublicKey: opts["pbk"], ShortID: strings.ToLower(opts["sid"]), Flow: "xtls-rprx-vision", Transport: "tcp", MinVersion: opts["minver"]}, nil
}

func SS2022UserKey(u store.User, nodeID uint32, serverKey string) string {
	material := fmt.Sprintf("%d|%s|%s|%d|%s", u.ID, u.Passwd, SS2022Method, nodeID, serverKey)
	sum := sha256.Sum256([]byte(material))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// AnyTLSPassword is stable across nodes and does not expose the account password.
func AnyTLSPassword(u store.User) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("sspanel-anytls-v1\x00%d\x00%s", u.ID, u.Passwd)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VMessConfig accepts both legacy tls;ws and explicit ws;tls forms for modern exports.
// The historical V2Array/mu output remains untouched for compatibility.
// 外部中转在 TLS 标记修正之后应用，SNI 才能正确固定为原地址。
func VMessConfig(server string) *V2 {
	v, relay := vmessConfigBase(server)
	applyRelay(v, relay)
	return v
}

// vmessConfigBase 解析 VMess 节点配置但不应用外部中转，同时返回中转入口。
func vmessConfigBase(server string) (*V2, *Relay) {
	base, relay, _ := SplitRelay(server, false)
	v := v2ArrayBase(base)
	parts := strings.Split(base, ";")
	if len(parts) > 4 && parts[4] == "tls" {
		v.Set("tls", "tls")
	}
	return v, relay
}

// Proxy is the common client representation; it is never persisted to the database.
type Proxy struct {
	Class                                                              int32
	Name, Kind, Server, Password, UUID, Method                         string
	Port                                                               int
	TLS                                                                bool
	Transport, Host, Path, SNI, PublicKey, ShortID, Plugin, PluginOpts string
	AlterID                                                            int
	V2                                                                 *V2
	// Origin 是配置外部中转时的原节点地址，Server / Port 为中转入口；没有中转时为空。
	Origin string
}

// origin 返回中转前的节点地址。各格式中 SNI / Host 的回退（原来回退到连接地址）使用它，
// 中转后实际生效的证书校验名称与传输 Host 与中转前相同。
func (p Proxy) origin() string { return firstNonempty(p.Origin, p.Server) }

func ProtocolProxy(u store.User, n store.SsNode) (Proxy, error) {
	c, err := ParseProtocol(n.Sort, n.Server)
	if err != nil {
		return Proxy{}, err
	}
	p := Proxy{Class: n.NodeClass, Name: n.Name, Kind: c.Protocol, Server: c.Server, Port: c.Port, Method: c.Method, SNI: c.SNI, PublicKey: c.PublicKey, ShortID: c.ShortID, TLS: c.Protocol == "vless", Transport: c.Transport}
	if c.Relay != nil { // 客户端连接外部中转入口；SNI、REALITY 参数、凭据不变
		p.Origin, p.Server, p.Port = c.Server, c.Relay.Host, c.Relay.Port
	}
	switch n.Sort {
	case SortSS2022:
		p.Kind = "ss"
		p.Password = c.ServerKey + ":" + SS2022UserKey(u, n.ID, c.ServerKey)
	case SortAnyTLS:
		p.Password = AnyTLSPassword(u)
		p.TLS = true
	default:
		p.UUID = model.NewUser(u, time.UTC).UUID()
	}
	return p, nil
}

// vmessProxy 生成 VMess 节点的 Proxy。Proxy 保留中转前的原始 SNI / Host，各格式按原有回退规则使用 Origin；
// v2rayN JSON（Proxy.V2）使用把实际生效名称写成显式值的副本（见 applyRelay）。
func vmessProxy(n store.SsNode, uuid string) (Proxy, bool) {
	v, relay := vmessConfigBase(n.Server)
	v.Set("v", "2")
	v.Set("ps", n.Name)
	v.Set("id", uuid)
	port := v.Int("port")
	if !validHost(v.Str("add")) || port < 1 || port > 65535 {
		return Proxy{}, false
	}
	p := Proxy{Class: n.NodeClass, Name: n.Name, Kind: "vmess", Server: v.Str("add"), Port: port, UUID: uuid, AlterID: v.Int("aid"), Transport: v.Str("net"), TLS: v.Str("tls") == "tls", Host: v.Str("host"), Path: v.Str("path"), SNI: v.Str("sni"), V2: v}
	if relay != nil {
		p.V2 = v.clone()
		applyRelay(p.V2, relay)
		p.Origin, p.Server, p.Port = p.Server, relay.Host, relay.Port
	}
	return p, true
}

func activeUser(u store.User, now time.Time) bool {
	return u.Enable == 1 && u.ExpireIn.After(now) && u.TransferEnable > u.U+u.D
}

func NodeAllowed(u store.User, n store.SsNode) bool {
	return n.Type == 1 && (n.NodeBandwidthLimit == 0 || n.NodeBandwidth < n.NodeBandwidthLimit) && (u.IsAdmin != 0 || (n.NodeClass <= u.Class && (n.NodeGroup == 0 || n.NodeGroup == u.NodeGroup)))
}

// Proxies exposes only SS2022, VMess, VLESS and AnyTLS subscriptions.
func (s *Service) Proxies(ctx context.Context, u store.User, now time.Time) ([]Proxy, error) {
	return s.proxies(ctx, u, now, Filter{})
}

func (s *Service) proxies(ctx context.Context, u store.User, now time.Time, filter Filter) ([]Proxy, error) {
	if !activeUser(u, now) {
		return nil, nil
	}
	var out []Proxy
	nodes, err := s.Q.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if !NodeAllowed(u, n) {
			continue
		}
		if n.Sort == SortSS2022 || n.Sort == SortVLESS || n.Sort == SortAnyTLS {
			p, err := ProtocolProxy(u, n)
			if err == nil {
				out = append(out, p)
			}
			continue
		}
		if n.Sort != SortV2Ray && n.Sort != SortV2RayRelay {
			continue
		}
		// VMess retains the original class/group policy, including administrators.
		if n.NodeClass > u.Class || (n.NodeGroup != 0 && n.NodeGroup != u.NodeGroup) {
			continue
		}
		if p, ok := vmessProxy(n, model.NewUser(u, s.Loc).UUID()); ok {
			out = append(out, p)
		}
	}
	out = filter.proxies(out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	used := map[string]bool{"DIRECT": true, "REJECT": true, "Auto": true, "direct": true, "tailscale": true, "🍃 Proxy": true, "🫥SiriAI": true, "🍎 Only": true, "🏖 Netflix": true, "🍂 Domestic": true, "☁️ Others": true}
	for i := range out {
		name := strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ", ",", " ", "=", " ", "#", " ", ";", " ", "[", "(", "]", ")", "\\", "-", "\"", "'").Replace(out[i].Name))
		if name == "" {
			name = "Proxy"
		}
		base := name
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		used[name] = true
		out[i].Name = name
	}
	return out, nil
}

func (p Proxy) URI() string {
	endpoint := net.JoinHostPort(p.Server, strconv.Itoa(p.Port))
	switch p.Kind {
	case "ss":
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(p.Method + ":" + p.Password))
		if strings.HasPrefix(p.Method, "2022-") {
			userinfo = rawURLEncode(p.Method) + ":" + rawURLEncode(p.Password)
		}
		q := ""
		if p.Plugin != "" {
			q = "?plugin=" + rawURLEncode(p.Plugin+";"+p.PluginOpts)
		}
		return "ss://" + userinfo + "@" + endpoint + q + "#" + rawURLEncode(p.Name)
	case "vmess":
		if p.V2 == nil {
			return ""
		}
		v := newV2()
		for _, key := range p.V2.Keys {
			v.Set(key, p.V2.Values[key])
		}
		v.Set("ps", p.Name)
		return "vmess://" + base64.StdEncoding.EncodeToString([]byte(EncodeJSON(v, UnescapedUnicode)))
	case "anytls":
		return "anytls://" + rawURLEncode(p.Password) + "@" + endpoint + "/?sni=" + rawURLEncode(p.SNI) + "&insecure=0#" + rawURLEncode(p.Name)
	case "vless":
		q := url.Values{"encryption": {"none"}, "flow": {"xtls-rprx-vision"}, "security": {"reality"}, "sni": {p.SNI}, "fp": {"chrome"}, "pbk": {p.PublicKey}, "sid": {p.ShortID}, "type": {"tcp"}, "packetEncoding": {"xudp"}, "udp": {"3"}, "xtls": {"2"}}
		return "vless://" + p.UUID + "@" + endpoint + "?" + q.Encode() + "#" + rawURLEncode(p.Name)
	}
	return ""
}
