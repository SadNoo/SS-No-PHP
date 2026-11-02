package sub

import (
	"errors"
	"strconv"
	"strings"
)

// 外部 NAT 中转：节点地址末尾的 “;||中转地址;中转端口” 后缀（沿用私有版格式）。
//
//	客户端 → 中转地址:中转端口 → 真实节点；真实节点 → 面板 WebAPI
//
// 订阅与节点详情使用中转地址和端口；WebAPI 下发给节点后端的配置、节点认证、节点 IP 同步、
// Cloudflare 同步使用真实节点地址（后缀之前的部分）。密钥、UUID、SNI、REALITY 参数等不变。
//
// 读取：只认最后一个 “||”，且其后严格为 “地址;端口” 才算中转后缀；兼容不带前导分号的 “||” 写法。
// 保存：统一写成 “原节点配置;||地址;端口”，原节点配置原样保留（不改动其中的分号和空字段）。
// 不带前导分号的旧写法若原节点配置本身以分号结尾，会被视为 “;||” 写法（该分号属于分隔符）。

// RelaySeparator 是保存时使用的中转分隔符。
const RelaySeparator = ";||"

// MaxServerLen 是节点地址的总长度上限（ss_node.server 为 VARCHAR(512)，节点地址只允许 ASCII，字节数等于字符数）。
const MaxServerLen = 512

// Relay 是外部中转入口。
type Relay struct {
	Host string
	Port int
}

var errRelayFormat = errors.New("中转格式无效：应为 原节点配置;||中转地址;中转端口")

// SplitRelay 拆出中转后缀。没有中转时 relay 为 nil、base 为原字符串。
// strict 为 true 时，含有 “||” 但后缀不合法返回错误；为 false 时把整个字符串视为原节点配置（兼容旧数据）。
func SplitRelay(server string, strict bool) (base string, relay *Relay, err error) {
	i := strings.LastIndex(server, "||")
	if i < 0 {
		return server, nil, nil
	}
	r, ok := parseRelaySuffix(server[i+2:])
	if !ok {
		if strict {
			return "", nil, errRelayFormat
		}
		return server, nil, nil
	}
	base = server[:i]
	base = strings.TrimSuffix(base, ";")
	if strings.TrimSpace(base) == "" {
		if strict {
			return "", nil, errRelayFormat
		}
		return server, nil, nil
	}
	return base, r, nil
}

func parseRelaySuffix(s string) (*Relay, bool) {
	host, portStr, ok := strings.Cut(s, ";")
	if !ok || strings.Contains(portStr, ";") {
		return nil, false
	}
	host = strings.TrimSpace(host)
	if !validHost(host) {
		return nil, false
	}
	port, err := portNumber(strings.TrimSpace(portStr), false)
	if err != nil {
		return nil, false
	}
	return &Relay{Host: host, Port: port}, true
}

// RealServer 返回去掉中转后缀的原节点配置（节点后端、认证、同步使用）。
func RealServer(server string) string {
	base, _, _ := SplitRelay(server, false)
	return base
}

// JoinRelay 把中转地址拼接到原节点配置之后。relay 为 nil 时原样返回。
func JoinRelay(base string, relay *Relay) string {
	if relay == nil {
		return base
	}
	return base + RelaySeparator + relay.Host + ";" + strconv.Itoa(relay.Port)
}

// ParseRelayInput 校验后台“中转地址/中转端口”两个输入框：必须同时填写或同时留空。
func ParseRelayInput(host, port string) (*Relay, error) {
	host, port = strings.TrimSpace(host), strings.TrimSpace(port)
	if host == "" && port == "" {
		return nil, nil
	}
	if host == "" || port == "" {
		return nil, errors.New("中转地址和中转端口必须同时填写或同时留空")
	}
	if !validHost(host) || strings.Contains(host, ";") {
		return nil, errors.New("中转地址无效")
	}
	p, err := portNumber(port, false)
	if err != nil {
		return nil, errors.New("中转端口无效：" + err.Error())
	}
	return &Relay{Host: host, Port: p}, nil
}

// ValidateServerText 检查节点地址只含可见 ASCII 且不超过总长度上限。
func ValidateServerText(server string) error {
	if len(server) > MaxServerLen {
		return errors.New("节点地址过长：含中转后缀最多 " + strconv.Itoa(MaxServerLen) + " 个字符")
	}
	for i := 0; i < len(server); i++ {
		if server[i] < 0x21 || server[i] > 0x7e {
			return errors.New("节点地址只能包含英文字母、数字和符号，不能有空格或中文")
		}
	}
	return nil
}

// applyRelay 把 VMess JSON（v2rayN 格式）的连接地址换成中转入口。替换之前先把中转前实际生效的名称写成显式值，
// 证书校验与伪装 Host 不因中转改变（必须在解析完 server= 等附加参数之后调用）：
//   - TLS 校验名称：sni 为空时按客户端的回退顺序取 host，再取原地址（原地址是 IP 时就是该 IP）；
//   - 传输层 Host：ws / h2 / http / httpupgrade 的 host 为空时，中转前客户端使用连接地址，固定为原地址。
//
// 不开启或关闭 TLS、不改加密方式，纯 TCP 节点只替换地址和端口。原地址记录在 item.Origin。
// 由 Proxy 导出的其他格式不使用这里写入的值，而是按各自原有的回退规则使用 Proxy.Origin（见 Proxy.origin）。
func applyRelay(item *V2, relay *Relay) {
	if relay == nil {
		return
	}
	orig := item.Str("add")
	if item.Str("tls") == "tls" && item.Str("sni") == "" {
		item.Set("sni", firstNonempty(item.Str("host"), orig))
	}
	if n := item.Str("net"); (n == "ws" || n == "h2" || n == "http" || n == "httpupgrade") && item.Str("host") == "" {
		item.Set("host", orig)
	}
	item.Set("add", relay.Host)
	item.Set("port", relay.Port)
	item.Origin = orig
}
