package view

import (
	"testing"

	"github.com/SadNoo/SS-No-PHP/internal/dec"
)

func TestLooseEqual(t *testing.T) {
	cases := []struct {
		a, b any
		want bool
	}{
		{nil, "", true}, {nil, 0, true}, {nil, "0", false}, {nil, "a", false}, {nil, nil, true},
		{"1", 1, true}, {"1.0", 1, true}, {"abc", "abc", true}, {"true", "true", true},
		{0, "0", true}, {int32(10), "10", true}, {dec.FromInt(12), "12", true}, {dec.D(150), 1.5, true},
		{true, "x", true}, {false, "", true}, {"a", "b", false},
	}
	for _, c := range cases {
		if got := LooseEqual(c.a, c.b); got != c.want {
			t.Errorf("LooseEqual(%#v, %#v) = %v，期望 %v", c.a, c.b, got, c.want)
		}
	}
}

func TestTruthy(t *testing.T) {
	for _, v := range []any{nil, "", "0", 0, 0.0, false, []int{}, dec.D(0)} {
		if Truthy(v) {
			t.Errorf("%#v 应为假", v)
		}
	}
	for _, v := range []any{"a", "00", 1, -1, true, []int{1}, dec.D(1)} {
		if !Truthy(v) {
			t.Errorf("%#v 应为真", v)
		}
	}
}

func TestToString(t *testing.T) {
	if ToString(dec.D(1250)) != "12.5" || ToString(3.0) != "3" || ToString(nil) != "" {
		t.Fatal(ToString(dec.D(1250)), ToString(3.0))
	}
}
