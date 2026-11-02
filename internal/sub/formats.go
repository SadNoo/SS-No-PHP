package sub

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/SadNoo/SS-No-PHP/internal/model"
	"github.com/SadNoo/SS-No-PHP/internal/store"
	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// 订阅类型（链接参数 ?mu=）。
const (
	MuNormal = 0
	MuSingle = 1
	MuV2Ray  = 2
	MuSSD    = 3
	MuClash  = 4
)

// V2Item 对应 URL::getV2Url($user, $node, 1) 的数组形式。
func (s *Service) V2Item(u store.User, node store.SsNode) *V2 {
	item := V2Array(node.Server)
	item.Set("v", "2")
	item.Set("ps", node.Name)
	item.Set("id", model.NewUser(u, s.Loc).UUID())
	return item
}

// V2URL 对应 URL::getV2Url($user, $node, 0)。
func (s *Service) V2URL(u store.User, node store.SsNode) string {
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(EncodeJSON(s.V2Item(u, node), UnescapedUnicode)))
}

// VMessNodes 返回用户可用的 V2Ray 节点（原版对管理员同样按等级、分组过滤）。
func (s *Service) VMessNodes(ctx context.Context, u store.User) ([]store.SsNode, error) {
	return s.Q.ListVMessNodes(ctx, store.ListVMessNodesParams{NodeGroup: u.NodeGroup, NodeClass: u.Class})
}

// AllVMessURL 对应 URL::getAllVMessUrl($user, 0)。
func (s *Service) AllVMessURL(ctx context.Context, u store.User) (string, error) {
	return s.allVMessURL(ctx, u, Filter{})
}

func (s *Service) allVMessURL(ctx context.Context, u store.User, filter Filter) (string, error) {
	nodes, err := s.VMessNodes(ctx, u)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, n := range nodes {
		if filter.match(Proxy{Class: n.NodeClass, Name: n.Name, Kind: "vmess"}) {
			n.Name = filter.name(n.Name)
			b.WriteString(s.V2URL(u, n) + "\n")
		}
	}
	return b.String(), nil
}

// AllSSDURL 对应 URL::getAllSSDUrl。用户无法使用 SS 时返回空字符串（原版返回 null）。
func (s *Service) AllSSDURL(ctx context.Context, u store.User) (string, error) {
	if !connOf(u).SSCanConnect() {
		return "", nil
	}
	mu := model.NewUser(u, s.Loc)
	all := NewOMap()
	all.Set("airport", s.AppName)
	all.Set("port", u.Port)
	all.Set("encryption", u.Method)
	all.Set("password", u.Passwd)
	all.Set("traffic_used", gbValue(u.U+u.D))
	all.Set("traffic_total", gbValue(u.TransferEnable))
	all.Set("expiry", mu.ClassExpireStr())
	tokenFn := s.SubToken
	if s.TokenFunc != nil {
		tokenFn = s.TokenFunc
	}
	token, err := tokenFn(ctx, u.ID, 0)
	if err != nil {
		return "", err
	}
	all.Set("url", s.SubURL+token+"?mu=3")
	plugin := ""
	// 原版用 strpos(...) != false，位置为 0 时视为不包含。
	if strings.Index(u.Obfs, "http") > 0 {
		plugin = "obfs=http"
	}
	if strings.Index(u.Obfs, "tls") > 0 {
		plugin = "obfs=tls"
	}
	if plugin != "" {
		all.Set("plugin", "simple-obfs")
		if u.ObfsParam != "" {
			plugin += ";obfs-host=" + u.ObfsParam
		}
		all.Set("plugin_options", plugin)
	}

	nodes, err := s.Q.ListSubNodes(ctx, store.ListSubNodesParams{IsAdmin: 0, NodeGroup: u.NodeGroup, Class: u.Class})
	if err != nil {
		return "", err
	}
	allNodes, err := s.Q.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	byID := map[uint32]store.SsNode{}
	for _, n := range allNodes {
		byID[n.ID] = n
	}
	relays, err := s.Q.ListRelayRulesForUser(ctx, u.ID)
	if err != nil {
		return "", err
	}
	muHost := mu.MuMd5(s.MuRegex, s.MuSuffix)
	var servers []any
	index := 1
	for _, node := range nodes {
		if node.Sort == SortSSV2Ray {
			if CanMethodConnect(u.Method) != 2 {
				continue
			}
			v := SSV2Array(node.Server)
			srv := NewOMap()
			srv.Set("server", v.Str("add"))
			srv.Set("id", index)
			srv.Set("remarks", node.Name+" - 单多"+v.Str("port")+"端口")
			srv.Set("port", v.Int("port"))
			srv.Set("encryption", u.Method)
			srv.Set("password", u.Passwd)
			srv.Set("plugin", "v2ray")
			security := "none"
			if v.Str("tls") == "tls" && v.Str("net") == "ws" {
				security = "tls"
			}
			srv.Set("plugin_options", "mode=ws;security="+security+";path="+v.Str("path")+";host="+muHost)
			servers = append(servers, srv)
			index++
			continue
		}
		base := NewOMap()
		base.Set("id", index)
		base.Set("server", node.Server)
		if r := firstRelayForSource(relays, node.ID); r != nil {
			dn := byID[uint32(r.DistNodeID)] //nolint:gosec // 已排除 -1
			base.Set("remarks", node.Name+" => "+dn.Name)
			base.Set("ratio", PHPFloat(float64(node.TrafficRate)+float64(dn.TrafficRate)))
			servers = append(servers, base)
			index++
			continue
		}
		base.Set("ratio", PHPFloat(float64(node.TrafficRate)))
		if node.MuOnly == 0 || node.MuOnly == -1 {
			base.Set("remarks", node.Name)
			servers = append(servers, base)
			index++
		}
		if node.MuOnly == 0 || node.MuOnly == 1 {
			// 原版此处用 $user->group（不存在的属性，恒为 null），等价于只取公共分组 0 的单端口节点。
			muNodes, err := s.Q.ListMuPortNodes(ctx, store.ListMuPortNodesParams{IsAdmin: 0, NodeGroup: 0, Class: u.Class})
			if err != nil {
				return "", err
			}
			sortByServer(muNodes)
			for _, mn := range muNodes {
				muUser, err := s.Q.GetUserByPort(ctx, int32(phpIntval(mn.Server))) //nolint:gosec // 端口号
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return "", err
				}
				if !connOf(muUser).SSCanConnect() {
					continue
				}
				// 原版复用同一个 $server 数组继续修改，键序沿用之前的顺序。
				srv := cloneOMap(base)
				srv.Set("id", index)
				srv.Set("remarks", node.Name+" - 单多"+mn.Server+"端口")
				srv.Set("port", mn.Server)
				srv.Set("encryption", muUser.Method)
				srv.Set("password", muUser.Passwd)
				srv.Set("plugin", "simple-obfs")
				po := ""
				if strings.Index(muUser.Obfs, "http") > 0 {
					po = "obfs=http"
				}
				if strings.Index(muUser.Obfs, "tls") > 0 {
					po = "obfs=tls"
				}
				srv.Set("plugin_options", po+";obfs-host="+muHost)
				servers = append(servers, srv)
				base = srv
				index++
			}
		}
	}
	if servers == nil {
		servers = []any{}
	}
	all.Set("servers", servers)
	return "ssd://" + B64(EncodeJSON(all, 0)), nil
}

func cloneOMap(m *OMap) *OMap {
	c := NewOMap()
	for _, k := range m.keys {
		c.Set(k, m.vals[k])
	}
	return c
}

func sortByServer(nodes []store.SsNode) {
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j].Server < nodes[j-1].Server; j-- {
			nodes[j], nodes[j-1] = nodes[j-1], nodes[j]
		}
	}
}

// firstRelayForSource 对应 getAllSSDUrl 中按优先级取源节点的第一条中转规则。
func firstRelayForSource(rules []store.Relay, nodeID uint32) *store.Relay {
	var best *store.Relay
	for i := range rules {
		r := &rules[i]
		if r.SourceNodeID != int32(nodeID) { //nolint:gosec // 节点 ID 范围很小
			continue
		}
		if best == nil || r.Priority > best.Priority || (r.Priority == best.Priority && r.ID < best.ID) {
			best = r
		}
	}
	return best
}

// gbValue 模拟 PHP 中 $bytes / GB 的结果。
func gbValue(b int64) any { return PHPFloat(float64(b) / util.GB) }

// Content 对应 LinkController::GetSSRSub：按 mu 参数生成订阅内容。
func (s *Service) Content(ctx context.Context, u store.User, mu int, now time.Time) (string, error) {
	switch mu {
	case MuNormal, MuSingle:
		all, err := s.AllURL(ctx, u, int32(mu), 0, now) //nolint:gosec // mu 只有 0/1
		if err != nil {
			return "", err
		}
		return B64(all), nil
	case MuV2Ray:
		all, err := s.AllVMessURL(ctx, u)
		if err != nil {
			return "", err
		}
		return B64(all), nil
	case MuSSD:
		return s.AllSSDURL(ctx, u)
	case MuClash:
		return s.Clash(ctx, u, now)
	}
	return "", nil
}

// Clash 对应 LinkController::GetClash。
func (s *Service) Clash(ctx context.Context, u store.User, now time.Time) (string, error) {
	return s.clash(ctx, u, now, Filter{})
}

func (s *Service) clash(ctx context.Context, u store.User, now time.Time, filter Filter) (string, error) {
	var confs []*OMap
	var names []string
	e, err := s.load(ctx, u)
	if err != nil {
		return "", err
	}
	normal, err := s.allItems(ctx, e, u, 0, true)
	if err != nil {
		return "", err
	}
	single, err := s.allItems(ctx, e, u, 1, true)
	if err != nil {
		return "", err
	}
	mu := model.NewUser(u, s.Loc)
	for _, it := range append(normal, single...) {
		if !filter.match(Proxy{Class: it.Class, Name: it.Remark, Kind: "ss", Method: it.Method}) {
			continue
		}
		it.Remark = filter.name(it.Remark)
		c := NewOMap()
		c.Set("name", it.Remark)
		c.Set("type", "ss")
		c.Set("server", it.Address)
		c.Set("port", it.Port)
		c.Set("cipher", it.Method)
		c.Set("password", it.Passwd)
		if it.Obfs != "plain" {
			opts := NewOMap()
			switch it.Obfs {
			case "simple_obfs_http":
				c.Set("plugin", "obfs")
				opts.Set("mode", "http")
			case "simple_obfs_tls":
				c.Set("plugin", "obfs")
				opts.Set("mode", "tls")
			case "v2ray":
				c.Set("plugin", "v2ray-plugin")
				opts.Set("mode", "websocket")
				// 原版 strpos(...) 为 0 时视为不包含。
				if strings.Index(it.ObfsParam, "security=tls") > 0 {
					opts.Set("tls", true)
				}
				opts.Set("host", mu.MuMd5(s.MuRegex, s.MuSuffix))
				opts.Set("path", it.Path)
			}
			if it.Obfs != "v2ray" {
				switch {
				case it.ObfsParam != "":
					opts.Set("host", it.ObfsParam)
				case u.ObfsParam != "":
					opts.Set("host", u.ObfsParam)
				default:
					opts.Set("host", "wns.windows.com")
				}
			}
			c.Set("plugin-opts", opts)
		}
		confs = append(confs, c)
		names = append(names, it.Remark)
	}
	vnodes, err := s.VMessNodes(ctx, u)
	if err != nil {
		return "", err
	}
	for _, n := range vnodes {
		if !filter.match(Proxy{Class: n.NodeClass, Name: n.Name, Kind: "vmess"}) {
			continue
		}
		n.Name = filter.name(n.Name)
		it := s.V2Item(u, n)
		net := it.Str("net")
		if net == "kcp" || net == "http" || net == "quic" {
			continue
		}
		if it.Origin != "" && it.Str("tls") == "tls" {
			continue // 旧 Clash 模板没有 SNI 字段，配置了外部中转的 TLS 节点无法校验原证书
		}
		c := NewOMap()
		c.Set("name", it.Str("ps"))
		c.Set("type", "vmess")
		c.Set("server", it.Values["add"])
		c.Set("port", it.Values["port"])
		c.Set("uuid", it.Str("id"))
		c.Set("alterId", it.Values["aid"])
		c.Set("cipher", "auto")
		switch net {
		case "ws":
			c.Set("network", "ws")
			c.Set("ws-path", it.Values["path"])
			if it.Str("tls") == "tls" {
				c.Set("tls", true)
			}
			if it.Str("host") != "" {
				c.Set("ws-headers", NewOMap().Set("Host", it.Values["host"]))
			}
		case "tls":
			c.Set("tls", true)
		}
		confs = append(confs, c)
		names = append(names, it.Str("ps"))
	}
	if len(confs) == 0 && filter.active() {
		return "", errors.New("没有兼容旧 Clash 格式的节点")
	}
	return renderClash(confs, names, now.In(s.Loc)), nil
}

// renderClash 对应 resources/conf/clash.tpl。
func renderClash(confs []*OMap, names []string, now time.Time) string {
	const flags = UnescapedSlashes | UnescapedUnicode
	var b strings.Builder
	b.WriteString(strings.Replace(clashHead, "{UPDATED}", now.Format("2006-01-02 03:04:05"), 1))
	for _, c := range confs {
		b.WriteString("    - " + EncodeJSON(c, flags) + "\n")
	}
	if names == nil {
		names = []string{}
	}
	proxies := EncodeJSON(names, flags)
	withAuto := EncodeJSON(append([]string{"Auto"}, names...), flags)
	b.WriteString(strings.NewReplacer("{PROXIES}", proxies, "{PROXIES_AUTO}", withAuto).Replace(clashGroups))
	b.WriteString(clashRules)
	return b.String()
}
