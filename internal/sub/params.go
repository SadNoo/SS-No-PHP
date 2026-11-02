package sub

// 原版 Config::getSupportParam 中的可选参数列表（顺序与原版一致，资料编辑页按此顺序显示）。
var (
	methodList = []string{
		"rc4-md5", "rc4-md5-6", "aes-128-cfb", "aes-192-cfb", "aes-256-cfb", "aes-128-ctr", "aes-192-ctr", "aes-256-ctr",
		"camellia-128-cfb", "camellia-192-cfb", "camellia-256-cfb", "bf-cfb", "cast5-cfb", "des-cfb", "des-ede3-cfb",
		"idea-cfb", "rc2-cfb", "seed-cfb", "salsa20", "chacha20", "xsalsa20", "chacha20-ietf",
		"aes-128-gcm", "aes-192-gcm", "none", "aes-256-gcm", "chacha20-ietf-poly1305", "xchacha20-ietf-poly1305",
	}
	protocolList = []string{
		"origin", "verify_deflate", "auth_sha1_v4", "auth_sha1_v4_compatible", "auth_aes128_sha1", "auth_aes128_md5",
		"auth_chain_a", "auth_chain_b", "auth_chain_c", "auth_chain_d", "auth_chain_e", "auth_chain_f",
	}
	obfsList = []string{
		"plain", "http_simple", "http_simple_compatible", "http_post", "http_post_compatible",
		"tls1.2_ticket_auth", "tls1.2_ticket_auth_compatible", "tls1.2_ticket_fastauth", "tls1.2_ticket_fastauth_compatible",
		"simple_obfs_http", "simple_obfs_http_compatible", "simple_obfs_tls", "simple_obfs_tls_compatible",
	}
	allowNoneProtocol = []string{"auth_chain_a", "auth_chain_b", "auth_chain_c", "auth_chain_d", "auth_chain_e", "auth_chain_f"}
)

// SupportParam 对应 Config::getSupportParam：method / protocol / obfs / allow_none_protocol。
func SupportParam(kind string) []string {
	switch kind {
	case "protocol":
		return protocolList
	case "obfs":
		return obfsList
	case "allow_none_protocol":
		return allowNoneProtocol
	default:
		return methodList
	}
}

// IsParamValid 对应 Tools::is_param_validate。
func IsParamValid(kind, v string) bool { return contains(SupportParam(kind), v) }

// CheckNoneProtocol 对应 Tools::checkNoneProtocol：加密方式为 none 时，协议必须是 auth_chain 系列。
func CheckNoneProtocol(method, protocol string) bool {
	return method != "none" || contains(allowNoneProtocol, protocol)
}
