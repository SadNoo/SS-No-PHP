package util

import (
	"testing"
	"time"
)

func TestFlowAutoShow(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0B"},
		{1000, "1000B"},
		{1536, "1.5KB"},
		{MB * 2, "2MB"},
		{GB, "1024MB"}, // 原版用 > 比较，恰好 1GB 时显示 1024MB
		{GB * 1.256, "1.26GB"},
		{-GB * 2, "-2GB"},
		{TB * 3.333, "3.33TB"},
	}
	for _, c := range cases {
		if got := FlowAutoShow(c.in); got != c.want {
			t.Errorf("FlowAutoShow(%v) = %s，期望 %s", c.in, got, c.want)
		}
	}
}

func TestPHPRound(t *testing.T) {
	if got := PHPRound(1.005, 2); got != 1.01 {
		t.Errorf("PHPRound(1.005) = %v", got)
	}
	if got := PHPRound(-2.5, 0); got != -3 {
		t.Errorf("PHPRound(-2.5) = %v", got)
	}
}

func TestRandom(t *testing.T) {
	a, b := RandomChar(16), RandomChar(16)
	if len(a) != 16 || a == b {
		t.Fatal("随机字符串长度或唯一性错误")
	}
	if !IsValidate(a) {
		t.Fatal("随机字符串包含非法字符")
	}
	n := RandomNum(6)
	if len(n) != 6 || !IsInt(n) {
		t.Fatalf("随机数字错误: %s", n)
	}
}

func TestSecondsToTime(t *testing.T) {
	if got := SecondsToTime(90061); got != "1 天, 1 小时, 1 分 + 1 秒" {
		t.Fatal(got)
	}
}

func TestToDateTime(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if got := ToDateTime(0, loc); got != "1970-01-01 08:00:00" {
		t.Fatal(got)
	}
}

func TestMiddleText(t *testing.T) {
	if s, ok := MiddleText("a[x]b", "[", "]"); !ok || s != "x" {
		t.Fatal(s, ok)
	}
	if _, ok := MiddleText("[x]", "[", "]"); ok {
		t.Fatal("开头位置为 0 时原版视为找不到")
	}
	if s, ok := MiddleText("a[]b", "[", "]"); !ok || s != "" {
		t.Fatal("中间为空也应找到")
	}
}

func TestIsInt(t *testing.T) {
	for _, s := range []string{"1", "-23", "007"} {
		if !IsInt(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"", "-", "1.2", "a"} {
		if IsInt(s) {
			t.Fatal(s)
		}
	}
}
