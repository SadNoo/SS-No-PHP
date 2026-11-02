package sub

// siriClientRules is the domain-only projection of the approved SiriAI snapshot.
// Snapshot SHA-256: e6b226cdb0cde29e52850c91dd88ec5e7eb3bc77106448560b21499ef94ccf58.
// Kept in this repository so non-sing-box clients do not need a format converter.
var siriClientRules = []string{
	"DOMAIN,api-siri-prod.apple.com,🫥SiriAI",
	"DOMAIN,apple-carry-relay.cloudflare.com,🫥SiriAI",
	"DOMAIN,apple-relay.akamaized.net,🫥SiriAI",
	"DOMAIN,guzzoni.apple.com,🫥SiriAI",
	"DOMAIN,mask-api.fe.apple-dns.net,🫥SiriAI",
	"DOMAIN,mask-api.fe2.apple-dns.net,🫥SiriAI",
	"DOMAIN,mask-api.icloud.com,🫥SiriAI",
	"DOMAIN,mask-t.apple-dns.net,🫥SiriAI",
	"DOMAIN,mask.apple-dns.net,🫥SiriAI",
	"DOMAIN,metrics.icloud.com,🫥SiriAI",
	"DOMAIN,probe.icloud.com,🫥SiriAI",
	"DOMAIN,seed.siri.apple.com,🫥SiriAI",
	"DOMAIN-SUFFIX,apple-relay.apple.com,🫥SiriAI",
	"DOMAIN-SUFFIX,apple-relay.cloudflare.com,🫥SiriAI",
	"DOMAIN-SUFFIX,apple-relay.fastly-edge.com,🫥SiriAI",
	"DOMAIN-SUFFIX,apple-relay.mask.apple-dns.net,🫥SiriAI",
	"DOMAIN-SUFFIX,apps.mzstatic.com,🫥SiriAI",
	"DOMAIN-SUFFIX,cp4.cloudflare.com,🫥SiriAI",
	"DOMAIN-SUFFIX,gateway.icloud.com,🫥SiriAI",
	"DOMAIN-SUFFIX,gspe1-ssl.ls.apple.com,🫥SiriAI",
	"DOMAIN-SUFFIX,ls.apple.com,🫥SiriAI",
	"DOMAIN-SUFFIX,mask-h2.icloud.com,🫥SiriAI",
	"DOMAIN-SUFFIX,mask.icloud.com,🫥SiriAI",
	"DOMAIN-SUFFIX,smoot.apple.com,🫥SiriAI",
	"DOMAIN-KEYWORD,siri,🫥SiriAI",
	"DOMAIN,android.chat.openai.com,🫥SiriAI",
	"DOMAIN,auth0.openai.com,🫥SiriAI",
	"DOMAIN,challenges.cloudflare.com,🫥SiriAI",
	"DOMAIN,chat.openai.com,🫥SiriAI",
	"DOMAIN,chat.openai.com.cdn.cloudflare.net,🫥SiriAI",
	"DOMAIN,desktop.chat.openai.com,🫥SiriAI",
	"DOMAIN,forwarder.workos.com,🫥SiriAI",
	"DOMAIN,images.workoscdn.com,🫥SiriAI",
	"DOMAIN,ios.chat.openai.com,🫥SiriAI",
	"DOMAIN,js.intercomcdn.com,🫥SiriAI",
	"DOMAIN,js.stripe.com,🫥SiriAI",
	"DOMAIN,o207216.ingest.sentry.io,🫥SiriAI",
	"DOMAIN,o33249.ingest.sentry.io,🫥SiriAI",
	"DOMAIN,openai-api.arkoselabs.com,🫥SiriAI",
	"DOMAIN,openaicom-api-bdcpf8c6d2e9atf6.z01.azurefd.net,🫥SiriAI",
	"DOMAIN,openaicomproductionae4b.blob.core.windows.net,🫥SiriAI",
	"DOMAIN,production-openaicom-storage.azureedge.net,🫥SiriAI",
	"DOMAIN,rum.browser-intake-datadoghq.com,🫥SiriAI",
	"DOMAIN,setup.auth.openai.com,🫥SiriAI",
	"DOMAIN,setup.workos.com,🫥SiriAI",
	"DOMAIN,tcr9i.chat.openai.com,🫥SiriAI",
	"DOMAIN,ws.chatgpt.com,🫥SiriAI",
	"DOMAIN-SUFFIX,auth.openai.com,🫥SiriAI",
	"DOMAIN-SUFFIX,cdn.openaimerge.com,🫥SiriAI",
	"DOMAIN-SUFFIX,cdn.workos.com,🫥SiriAI",
	"DOMAIN-SUFFIX,chatgpt.com,🫥SiriAI",
	"DOMAIN-SUFFIX,chatgpt.livekit.cloud,🫥SiriAI",
	"DOMAIN-SUFFIX,client-api.arkoselabs.com,🫥SiriAI",
	"DOMAIN-SUFFIX,ct.sendgrid.net,🫥SiriAI",
	"DOMAIN-SUFFIX,host.livekit.cloud,🫥SiriAI",
	"DOMAIN-SUFFIX,humb.apple.com,🫥SiriAI",
	"DOMAIN-SUFFIX,oaistatic.com,🫥SiriAI",
	"DOMAIN-SUFFIX,oaistatsig.com,🫥SiriAI",
	"DOMAIN-SUFFIX,oaiusercontent.com,🫥SiriAI",
	"DOMAIN-SUFFIX,openai.com,🫥SiriAI",
	"DOMAIN-SUFFIX,openaiapi-site.azureedge.net,🫥SiriAI",
	"DOMAIN-SUFFIX,openaicom.imgix.net,🫥SiriAI",
	"DOMAIN-SUFFIX,sora.com,🫥SiriAI",
	"DOMAIN-SUFFIX,turn.livekit.cloud,🫥SiriAI",
	"DOMAIN-SUFFIX,workos.imgix.net,🫥SiriAI",
}

// proxyDomainRules 是 Surge 风格（Clash / Surge / Loon / Surfboard / 旧版 Quantumult 通用）的强制代理规则，
// 每个域名后缀一行；域名已由配置校验，只含字母、数字、连字符与点。
func proxyDomainRules(domains []string) []string {
	rules := make([]string, 0, len(domains))
	for _, d := range domains {
		rules = append(rules, "DOMAIN-SUFFIX,"+d+",🍃 Proxy")
	}
	return rules
}
