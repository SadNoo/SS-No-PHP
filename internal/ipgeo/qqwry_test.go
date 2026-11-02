package ipgeo

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func gbkBytes(s string) []byte {
	b, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	return append(b, 0)
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le24(v uint32) []byte { return le32(v)[:3] }

// buildDB 构造一个包含三段 IP 的小型 qqwry.dat，覆盖直接存储、模式 2 和模式 1 三种记录格式。
func buildDB() []byte {
	var buf bytes.Buffer
	buf.Write(make([]byte, 8))
	// 共享的国家字符串
	shared := uint32(buf.Len())
	buf.Write(gbkBytes("美国"))

	r1 := uint32(buf.Len()) // 1.0.0.0 - 1.0.0.255：国家、地区直接存储
	buf.Write(le32(0x010000FF))
	buf.Write(gbkBytes("中国"))
	buf.Write(gbkBytes("电信"))

	r2 := uint32(buf.Len()) // 2.0.0.0 - 2.0.0.255：模式 2，国家重定向，地区紧随其后
	buf.Write(le32(0x020000FF))
	buf.WriteByte(2)
	buf.Write(le24(shared))
	buf.Write(gbkBytes("加州"))

	r3c := uint32(buf.Len()) // 模式 1 指向的位置：国家直接存储 + 地区为 CZ88.NET
	buf.Write(gbkBytes("日本"))
	buf.Write(gbkBytes(" CZ88.NET"))
	r3 := uint32(buf.Len()) // 3.0.0.0 - 3.0.0.255：模式 1
	buf.Write(le32(0x030000FF))
	buf.WriteByte(1)
	buf.Write(le24(r3c))

	first := uint32(buf.Len())
	for _, e := range []struct{ start, off uint32 }{{0x01000000, r1}, {0x02000000, r2}, {0x03000000, r3}} {
		buf.Write(le32(e.start))
		buf.Write(le24(e.off))
	}
	last := uint32(buf.Len()) - 7
	b := buf.Bytes()
	copy(b, le32(first))
	copy(b[4:], le32(last))
	return b
}

func TestLookup(t *testing.T) {
	db, err := parse(buildDB())
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]string{
		"1.0.0.8": "中国电信", "2.0.0.1": "美国加州", "3.0.0.200": "日本",
	} {
		if got := db.Lookup(ip); got != want {
			t.Errorf("%s: 得到 %q，应为 %q", ip, got, want)
		}
	}
	if got := (&DB{}).Lookup("1.0.0.8"); got != "" {
		t.Errorf("未加载时应返回空字符串，得到 %q", got)
	}
	empty, err := Open("/nonexistent/qqwry.dat")
	if err != nil || empty.Loaded() {
		t.Fatalf("文件不存在时应返回空库: %v", err)
	}
}
