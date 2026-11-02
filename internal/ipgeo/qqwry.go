// Package ipgeo 查询 IP 归属地，读取纯真 IP 库 qqwry.dat（对应原版 App\Utils\QQWry）。
//
// 原版从 storage/qqwry.dat 读取，文件需要管理员自行下载（原版 xcat initQQWry）；
// 文件不存在时查询结果为空字符串，与原版一致。
package ipgeo

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DB 是加载到内存中的 qqwry.dat。零值可用，所有查询返回空字符串。
type DB struct {
	data    []byte
	firstIP uint32
	lastIP  uint32
}

// Open 读取 qqwry.dat；文件不存在时返回空的 DB（不报错）。
func Open(path string) (*DB, error) {
	if path == "" {
		return &DB{}, nil
	}
	b, err := os.ReadFile(path) //nolint:gosec // 路径来自配置
	if errors.Is(err, os.ErrNotExist) {
		return &DB{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parse(b)
}

func parse(b []byte) (*DB, error) {
	if len(b) < 8 {
		return nil, errors.New("qqwry.dat 文件不完整")
	}
	db := &DB{data: b, firstIP: binary.LittleEndian.Uint32(b), lastIP: binary.LittleEndian.Uint32(b[4:])}
	if db.firstIP > db.lastIP || int(db.lastIP)+7 > len(b) {
		return nil, errors.New("qqwry.dat 索引无效")
	}
	return db, nil
}

// Loaded 是否已加载 IP 库。
func (db *DB) Loaded() bool { return db != nil && len(db.data) > 0 }

func (db *DB) u32(off uint32) uint32 {
	if int(off)+4 > len(db.data) {
		return 0
	}
	return binary.LittleEndian.Uint32(db.data[off:])
}

func (db *DB) u24(off uint32) uint32 {
	if int(off)+3 > len(db.data) {
		return 0
	}
	d := db.data[off:]
	return uint32(d[0]) | uint32(d[1])<<8 | uint32(d[2])<<16
}

func (db *DB) byteAt(off uint32) byte {
	if int(off) >= len(db.data) {
		return 0
	}
	return db.data[off]
}

// cstr 读取以 0 结尾的字符串，返回字符串和结尾之后的位置。
func (db *DB) cstr(off uint32) (string, uint32) {
	end := off
	for int(end) < len(db.data) && db.data[end] != 0 {
		end++
	}
	if int(off) > len(db.data) {
		return "", off
	}
	return string(db.data[off:end]), end + 1
}

func (db *DB) area(off uint32) string {
	switch db.byteAt(off) {
	case 0:
		return ""
	case 1, 2:
		s, _ := db.cstr(db.u24(off + 1))
		return s
	default:
		s, _ := db.cstr(off)
		return s
	}
}

// Lookup 返回 IP 的归属地（国家 + 地区），对应原版 getlocation 后拼接 country 与 area。
func (db *DB) Lookup(ipStr string) string {
	country, area := db.lookup(ipStr)
	return country + area
}

// Country 只返回国家（原版异地登录检测只比较 country）。
func (db *DB) Country(ipStr string) string {
	country, _ := db.lookup(ipStr)
	return country
}

func (db *DB) lookup(ipStr string) (string, string) {
	if !db.Loaded() {
		return "", ""
	}
	var ip uint32 // 与原版 ip2long 一致：非 IPv4 地址按 0.0.0.0 查询
	if v4 := net.ParseIP(strings.TrimSpace(ipStr)).To4(); v4 != nil {
		ip = binary.BigEndian.Uint32(v4)
	}
	lo, hi := uint32(0), (db.lastIP-db.firstIP)/7
	found := db.lastIP
	for lo <= hi {
		mid := (lo + hi) / 2
		idx := db.firstIP + mid*7
		if ip < db.u32(idx) {
			if mid == 0 {
				break
			}
			hi = mid - 1
			continue
		}
		if ip > db.u32(db.u24(idx+4)) {
			lo = mid + 1
			continue
		}
		found = idx
		break
	}
	off := db.u24(found + 4)
	var country, area string
	switch db.byteAt(off + 4) {
	case 1:
		cOff := db.u24(off + 5)
		if db.byteAt(cOff) == 2 {
			country, _ = db.cstr(db.u24(cOff + 1))
			area = db.area(cOff + 4)
		} else {
			var next uint32
			country, next = db.cstr(cOff)
			area = db.area(next)
		}
	case 2:
		country, _ = db.cstr(db.u24(off + 5))
		area = db.area(off + 8)
	default:
		var next uint32
		country, next = db.cstr(off + 4)
		area = db.area(next)
	}
	if country == " CZ88.NET" {
		country = "未知"
	}
	if area == " CZ88.NET" {
		area = ""
	}
	return gbk(country), gbk(area)
}

// gbk 把 GBK 编码转换为 UTF-8，无法转换的字节忽略（原版 iconv('gbk', 'utf-8//IGNORE')）。
func gbk(s string) string {
	if s == "" {
		return ""
	}
	out, err := simplifiedchinese.GBK.NewDecoder().String(s)
	if err != nil {
		return ""
	}
	return strings.ReplaceAll(strings.ToValidUTF8(out, ""), "\uFFFD", "")
}
