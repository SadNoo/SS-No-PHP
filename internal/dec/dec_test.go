package dec

import "testing"

func TestParseAndString(t *testing.T) {
	cases := map[string]string{"12.34": "12.34", "3": "3.00", "0.5": "0.50", "-1.2": "-1.20", ".5": "0.50", "+2.05": "2.05"}
	for in, want := range cases {
		a, err := Parse(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if a.String() != want {
			t.Fatalf("%s: 得到 %s，期望 %s", in, a, want)
		}
	}
	for _, bad := range []string{"", "1.234", "abc", "1.", "1.-1"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("%q 应当报错", bad)
		}
	}
}

func TestMulPercent(t *testing.T) {
	if got := FromInt(10).MulPercent(20); got != FromInt(2) {
		t.Fatalf("10 元的 20%% 应为 2 元，得到 %s", got)
	}
	if got := D(333).MulPercent(50); got != D(167) {
		t.Fatalf("四舍五入错误: %s", got)
	}
}
