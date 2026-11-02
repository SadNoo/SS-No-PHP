package app

import "strconv"

// subscriptionClient describes the migrated clients; subscription tokens remain unchanged.
// CopyLabel 为空时不显示复制按钮，ImportLabel 为空时不显示一键导入（按钮文字沿用旧正式版）。
type subscriptionClient struct {
	Name, Format, Query, Note string
	CopyLabel, ImportLabel    string
}

// subscriptionClients 是首页「客户端订阅」中 sing-box 之后的客户端，前 6 个的顺序与按钮沿用旧正式版。
var subscriptionClients = []subscriptionClient{
	{"v2rayN / v2rayNG", "v2ray", "?v2ray=1", "VMess + SS2022 + VLESS 共用链接", "复制混合订阅", "导入 v2rayNG"},
	{"Clash", "clash", "?clash=1", "VMess + SS2022 + VLESS + AnyTLS 配置；请使用较新的 Mihomo 内核", "复制订阅", "一键导入"},
	{"Surge v5", "surge5", "?surge=5", "VMess + SS2022 + AnyTLS 配置；AnyTLS 需 iOS 5.17.0+ / Mac 6.4.3+", "复制订阅", "一键导入"},
	{"Shadowrocket", "shadowrocket", "?shadowrocket=1", "VMess + SS2022 + VLESS + AnyTLS 配置", "", "一键导入"},
	{"Loon", "loon", "?loon=1", "VMess + SS2022 + VLESS + AnyTLS 配置；手动添加时请选择导入配置", "复制订阅", "一键导入"},
	{"Quantumult X", "quantumultx", "?quantumultx=1", "VMess + SS2022 + VLESS 配置（不含 AnyTLS）", "复制混合订阅", "一键导入"},
	{"Surge v4", "surge4", "?surge=4", "VMess + SS2022 配置（不含 VLESS、AnyTLS）", "复制订阅", "一键导入"},
	{"Surfboard", "surfboard", "?surfboard=1", "VMess + SS2022 配置（SS2022 需支持该协议的版本）", "复制订阅", ""},
	{"Quantumult（旧版）", "quantumult", "?quantumult=3", "原生完整配置：VMess；与 Quantumult X 格式不同", "复制订阅", ""},
	{"Quantumult 来源配置", "quantumult-source", "?quantumult=2", "完整配置及可更新的 VMess 来源，保留筛选参数", "复制订阅", ""},
	{"Kitsunebi", "kitsunebi", "?kitsunebi=1", "Kitsunebi 专用 VMess URI 订阅", "复制订阅", ""},
}

// protocolSubscription 是首页「按协议订阅」的一项：Format 为订阅链接（data-sub-copy），AllURL 为页面数据中「全部 URL」文本的键。
type protocolSubscription struct {
	Name, Format, CopyLabel, AllURL string
}

// protocolSubscriptions 沿用旧正式版的「按协议订阅」（不含已停用的 SSR），加上 AnyTLS。
var protocolSubscriptions = []protocolSubscription{
	{"SS / SS2022", "ss2022", "SS2022 订阅", "ss_url_all"},
	{"VMess", "vmess", "复制订阅", "v2_url_all"},
	{"VLESS REALITY", "vless", "独立订阅", ""},
	{"AnyTLS", "anytls", "独立订阅", ""},
}

// subscriptionLine 是订阅区「订阅线路」的一项：Base 为该线路的订阅地址加订阅码。
type subscriptionLine struct {
	Name, Base string
}

// subscriptionLines 返回主线路与备用订阅地址（app.sub_url_backup）；没有备用地址时返回 nil，页面不显示切换。
func (a *App) subscriptionLines(token string) []subscriptionLine {
	if len(a.Cfg.App.SubURLBackup) == 0 {
		return nil
	}
	lines := []subscriptionLine{{"主线路", a.Cfg.App.SubURL + token}}
	for i, s := range a.Cfg.App.SubURLBackup {
		lines = append(lines, subscriptionLine{"备用线路 " + strconv.Itoa(i+1), s + token})
	}
	return lines
}
