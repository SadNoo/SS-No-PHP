package blockedip

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1.2.3.4", "1.2.3.4"},
		{" 203.0.113.9 ", "203.0.113.9"},
		{"0.0.0.0", "0.0.0.0"},
		{"255.255.255.255", "255.255.255.255"},
		{"2001:DB8::1", "2001:db8::1"},
		{"2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},
		{"::ffff:198.51.100.7", "198.51.100.7"},
		{"::1", "::1"},
	} {
		if got, ok := Parse(c.in); !ok || got != c.want {
			t.Errorf("%q：得到 %q %v，应为 %q", c.in, got, ok, c.want)
		}
	}
	for _, c := range []struct{ in, kind string }{
		{"256.1.1.1", "数字段超过 255"},
		{"1.2.3.999", "数字段超过 255"},
		{"1.2.3", "IPv4 不完整"},
		{"10", "IPv4 不完整"},
		{"1.2.3.4.5", "IPv4 超过四段"},
		{"1..2.3", "IPv4 有空段"},
		{"01.2.3.4", "前导零"},
		{"1.2.3.04", "前导零"},
		{"1.2.3.4:443", "带端口或混入冒号"},
		{"1.2.3.4;5.6.7.8", "混入分号"},
		{"1.2.3.0/24", "CIDR 网段"},
		{"2001:db8::/32", "CIDR 网段"},
		{"fe80::1%eth0", "IPv6 zone"},
		{"[2001:db8::1]:443", "带方括号或端口"},
		{"2001:db8:::1", "IPv6 格式错误或混入冒号"},
		{"1.2.3.4 5.6.7.8", "含空白"},
		{"abc", "其他格式"},
		{"１.２.３.４", "其他格式"},
	} {
		if got, ok := Parse(c.in); ok {
			t.Errorf("%q 不应合法，得到 %q", c.in, got)
		}
		if k := Kind(c.in); k != c.kind {
			t.Errorf("%q 分类为 %q，应为 %q", c.in, k, c.kind)
		}
	}
}

func TestSplit(t *testing.T) {
	l := Split(" 1.2.3.4,,256.1.1.1, 2001:DB8::1 ,1.2.3.4,1.2.3.4:80,::ffff:1.2.3.4,01.2.3.4,")
	if want := []string{"1.2.3.4", "2001:db8::1"}; !slices.Equal(l.Valid, want) {
		t.Errorf("合法项 %v，应为 %v", l.Valid, want)
	}
	if want := []string{"256.1.1.1", "1.2.3.4:80", "01.2.3.4"}; !slices.Equal(l.Invalid, want) {
		t.Errorf("非法项 %v，应为 %v", l.Invalid, want)
	}
	if l := Split(""); l.Valid != nil || l.Invalid != nil {
		t.Errorf("空字段应没有任何项: %+v", l)
	}
	if m := Masked("1.2.3.4:80"); m != "带端口或混入冒号（10 字符）" {
		t.Errorf("脱敏描述: %q", m)
	}
}
