package sub

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/util"
)

// golden_php.json 由原版 SSPanel-UIM 的 URL.php / Tools.php 在 PHP 8.4 下实际运行生成，
// 本测试保证 Go 版的输出与原版逐字节一致。
type golden struct {
	V2 []struct {
		In  string `json:"in"`
		Out string `json:"out"`
		SS  string `json:"ss"`
	} `json:"v2"`
	Item []struct {
		In  map[string]any `json:"in"`
		SSR string         `json:"ssr"`
		SS1 string         `json:"ss1"`
		SS2 string         `json:"ss2"`
	} `json:"item"`
	Method   map[string]int `json:"method"`
	Protocol map[string]int `json:"protocol"`
	Obfs     map[string]int `json:"obfs"`
	NF       []struct {
		In  float64 `json:"in"`
		Out string  `json:"out"`
	} `json:"nf"`
	ClashJSON   string `json:"clash_json"`
	JSONDefault string `json:"json_default"`
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	b, err := os.ReadFile("testdata/golden_php.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGoldenV2Array(t *testing.T) {
	for _, c := range loadGolden(t).V2 {
		if got := EncodeJSON(V2Array(c.In), UnescapedUnicode); got != c.Out {
			t.Errorf("v2Array(%q)\n  得到 %s\n  期望 %s", c.In, got, c.Out)
		}
		if got := EncodeJSON(SSV2Array(c.In), UnescapedUnicode); got != c.SS {
			t.Errorf("ssv2Array(%q)\n  得到 %s\n  期望 %s", c.In, got, c.SS)
		}
	}
}

func TestGoldenItemURL(t *testing.T) {
	s := &Service{AppName: "牛牛面板"}
	for _, c := range loadGolden(t).Item {
		str := func(k string) string { v, _ := c.In[k].(string); return v }
		it := Item{
			Address: str("address"), Port: int(c.In["port"].(float64)), Passwd: str("passwd"), Method: str("method"),
			Protocol: str("protocol"), ProtocolParam: str("protocol_param"), Obfs: str("obfs"), ObfsParam: str("obfs_param"),
			Remark: str("remark"), Group: str("group"), Path: str("path"),
		}
		for isSS, want := range []string{c.SSR, c.SS1, c.SS2} {
			if got := s.ItemURL(it, isSS); got != want {
				t.Errorf("getItemUrl(%s, %d)\n  得到 %s\n  期望 %s", it.Remark, isSS, got, want)
			}
		}
	}
}

func TestGoldenCanConnect(t *testing.T) {
	g := loadGolden(t)
	for k, v := range g.Method {
		if CanMethodConnect(k) != v {
			t.Errorf("CanMethodConnect(%s) != %d", k, v)
		}
	}
	for k, v := range g.Protocol {
		if CanProtocolConnect(k) != v {
			t.Errorf("CanProtocolConnect(%s) != %d", k, v)
		}
	}
	for k, v := range g.Obfs {
		if CanObfsConnect(k) != v {
			t.Errorf("CanObfsConnect(%s) != %d", k, v)
		}
	}
}

func TestGoldenNumberFormatAndJSON(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.NF {
		if got := util.NumberFormat(c.In, 2); got != c.Out {
			t.Errorf("number_format(%v) = %s，期望 %s", c.In, got, c.Out)
		}
	}
	conf := NewOMap().Set("name", `香港/01 "q"`).Set("type", "ss").Set("server", "a.com").Set("port", 443).
		Set("cipher", "aes-128-gcm").Set("password", `p\w`).Set("plugin", "obfs").
		Set("plugin-opts", NewOMap().Set("mode", "tls").Set("host", "x.com"))
	if got := EncodeJSON(conf, UnescapedSlashes|UnescapedUnicode); got != g.ClashJSON {
		t.Errorf("json_encode(320)\n  得到 %s\n  期望 %s", got, g.ClashJSON)
	}
	def := NewOMap().Set("a", "x/y").Set("b", "中文").Set("c", PHPFloat(1)).Set("d", PHPFloat(1.5)).
		Set("e", []any{1, 2}).Set("f", true).Set("g", nil).Set("h", "\t\x01")
	if got := EncodeJSON(def, 0); got != g.JSONDefault {
		t.Errorf("json_encode 默认\n  得到 %s\n  期望 %s", got, g.JSONDefault)
	}
}

func TestPHPFloatString(t *testing.T) {
	cases := map[float64]string{1: "1", 1.5: "1.5", 1e-8: "1.0e-8", 1.1175870895385742e-8: "1.1175870895385742e-8", 0.0001: "0.0001", 1.5e20: "1.5e+20", 0: "0", 1e15: "1000000000000000", 0.00001234: "1.234e-5"}
	for in, want := range cases {
		if got := phpFloatString(in); got != want {
			t.Errorf("phpFloatString(%v) = %s，期望 %s", in, got, want)
		}
	}
}

func base64Decode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}
