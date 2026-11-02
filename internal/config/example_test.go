package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestExampleConfig 保证 config.example.toml 与代码同步：能被正常加载、写出了全部配置项，
// 且业务配置的示例值就是内置默认值（示例文件同时充当默认值说明）。
func TestExampleConfig(t *testing.T) {
	t.Setenv("NIUNIU_APP_KEY", "0123456789abcdef0123")
	const path = "../../config.example.toml"
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	ex := Default()
	md, err := toml.DecodeFile(path, &ex)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	var walk func(rt reflect.Type, prefix []string)
	walk = func(rt reflect.Type, prefix []string) {
		for i := 0; i < rt.NumField(); i++ {
			tag := strings.Split(rt.Field(i).Tag.Get("toml"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			key := append(append([]string{}, prefix...), tag)
			if ft := rt.Field(i).Type; ft.Kind() == reflect.Struct && ft != reflect.TypeFor[Duration]() {
				walk(ft, key)
				continue
			}
			// [radius.db] 只写出常用项，其余与 [database] 相同。
			if len(key) == 3 && key[0] == "radius" {
				continue
			}
			// [[contact.group_links]] 默认为空，示例以注释给出（写成空数组会与 [[...]] 写法冲突）。
			if strings.Join(key, ".") == "contact.group_links" {
				continue
			}
			if !md.IsDefined(key...) {
				missing = append(missing, strings.Join(key, "."))
			}
		}
	}
	walk(reflect.TypeFor[Config](), nil)
	if len(missing) > 0 {
		t.Errorf("config.example.toml 缺少配置项: %s", strings.Join(missing, ", "))
	}
	def := Default()
	for name, pair := range map[string][2]any{
		"register": {ex.Register, def.Register}, "users": {ex.Users, def.Users}, "mail": {ex.Mail, def.Mail},
		"captcha": {ex.Captcha, def.Captcha}, "telegram": {ex.Telegram, def.Telegram}, "contact": {ex.Contact, def.Contact},
		"display": {ex.Display, def.Display}, "jobs": {ex.Jobs, def.Jobs}, "detect": {ex.Detect, def.Detect},
		"dns": {ex.DNS, def.DNS}, "payment": {ex.Payment, def.Payment},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("[%s] 示例值与默认值不一致:\n示例 %+v\n默认 %+v", name, pair[0], pair[1])
		}
	}
}
