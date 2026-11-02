package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// adminEnv 注册并登录一个管理员，写入基础数据。
func adminEnv(t *testing.T) (*testEnv, uint32) {
	e := newEnv(t)
	e.seedNodes()
	uid := e.login("admin@example.com")
	e.exec("UPDATE user SET is_admin = 1 WHERE id = ?", uid)
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES (1,'月付',10.00,'{"bandwidth":"100","class":"1","class_expire":"30","content_extra":"check-全部节点"}',30,1,1)`)
	e.exec(`INSERT INTO user_traffic_log (user_id,u,d,node_id,rate,traffic,log_time) VALUES (?,1024,2048,1,1,'3KB',UNIX_TIMESTAMP())`, uid)
	e.exec(`INSERT INTO login_ip (userid,ip,datetime,type) VALUES (?,'1.1.1.1',UNIX_TIMESTAMP(),0)`, uid)
	e.exec(`INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES (1,?,'2.2.2.2',UNIX_TIMESTAMP())`, uid)
	e.exec(`INSERT INTO blockip (nodeid,ip,datetime) VALUES (1,'3.3.3.3',UNIX_TIMESTAMP())`)
	e.exec(`INSERT INTO detect_list (name,text,regex,type) VALUES ('BT','<b>x</b>','x',1)`)
	e.exec(`INSERT INTO detect_log (user_id,list_id,datetime,node_id) VALUES (?,1,UNIX_TIMESTAMP(),1)`, uid)
	e.exec(`INSERT INTO ticket (title,content,rootid,userid,datetime,status) VALUES ('<script>x</script>','<p>hi</p>',0,?,UNIX_TIMESTAMP(),1)`, uid)
	e.exec(`INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('C1',-1,10,0,0,'1989-06-04 02:30:00')`)
	e.exec(`INSERT INTO coupon (code,onetime,expire,shop,credit) VALUES ('X',1,UNIX_TIMESTAMP(),'',10)`)
	e.exec(`INSERT INTO relay (user_id,source_node_id,dist_node_id,dist_ip,port,priority) VALUES (0,2,1,'1.1.1.1',0,1)`)
	e.exec(`INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,UNIX_TIMESTAMP(),UNIX_TIMESTAMP()+100,'',10)`, uid)
	e.exec(`INSERT INTO payback (total,userid,ref_by,ref_get,datetime) VALUES (10,?,?,2,UNIX_TIMESTAMP())`, uid, uid)
	e.exec(`INSERT INTO auto (type,value,sign,datetime) VALUES (1,'echo','',UNIX_TIMESTAMP())`)
	return e, uid
}

func dtForm(cols ...string) url.Values {
	f := url.Values{"draw": {"1"}, "start": {"0"}, "length": {"10"}, "search[value]": {""}, "order[0][column]": {"0"}, "order[0][dir]": {"desc"}}
	for i, c := range cols {
		f.Set("columns["+strconv.Itoa(i)+"][data]", c)
	}
	return f
}

func TestAdminPages(t *testing.T) {
	e, uid := adminEnv(t)
	id := strconv.Itoa(int(uid))
	for _, p := range []string{
		"/admin", "/admin/sys", "/admin/profile", "/admin/trafficlog", "/admin/node", "/admin/node/create", "/admin/node/1/edit",
		"/admin/ticket", "/admin/ticket/1/view", "/admin/relay", "/admin/relay/create", "/admin/relay/1/edit", "/admin/relay/path_search/" + id,
		"/admin/shop", "/admin/shop/create", "/admin/shop/1/edit", "/admin/bought", "/admin/announcement", "/admin/announcement/create",
		"/admin/announcement/1/edit", "/admin/detect", "/admin/detect/create", "/admin/detect/1/edit", "/admin/detect/log",
		"/admin/auto", "/admin/auto/create", "/admin/block", "/admin/unblock", "/admin/login", "/admin/alive", "/admin/code",
		"/admin/code/create", "/admin/donate/create", "/admin/user", "/admin/user/" + id + "/edit", "/admin/coupon", "/admin/invite",
	} {
		e.page(p)
	}
	for path, col := range map[string]string{
		"/admin/trafficlog/ajax": "id", "/admin/node/ajax": "op", "/admin/ticket/ajax": "op", "/admin/relay/ajax": "op",
		"/admin/shop/ajax": "op", "/admin/bought/ajax": "op", "/admin/announcement/ajax": "op", "/admin/detect/ajax": "op",
		"/admin/detect/log/ajax": "id", "/admin/auto/ajax": "id", "/admin/block/ajax": "id", "/admin/unblock/ajax": "id",
		"/admin/login/ajax": "id", "/admin/alive/ajax": "id", "/admin/code/ajax": "id", "/admin/user/ajax": "op",
		"/admin/coupon/ajax": "id", "/admin/payback/ajax": "id",
	} {
		body := e.raw("POST", path, "application/x-www-form-urlencoded", dtForm(col).Encode())
		var res struct {
			Draw            int              `json:"draw"`
			RecordsTotal    int              `json:"recordsTotal"`
			RecordsFiltered int              `json:"recordsFiltered"`
			Data            []map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &res); err != nil || res.Draw != 1 {
			t.Errorf("%s 返回不正确: %s", path, body)
			continue
		}
		if path != "/admin/unblock/ajax" && len(res.Data) == 0 {
			t.Errorf("%s 没有数据: %s", path, body)
		}
	}
	// 用户填写的内容在后台表格中被转义（K38）。
	if body := e.raw("POST", "/admin/ticket/ajax", "application/x-www-form-urlencoded", dtForm("op").Encode()); strings.Contains(body, "<script>") {
		t.Errorf("工单标题未转义: %s", body)
	}
	// 排序字段只接受已定义的列（K02）。
	f := dtForm("op")
	f.Set("columns[0][data]", "id;DROP TABLE user")
	if body := e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", f.Encode()); !strings.Contains(body, `"recordsTotal"`) {
		t.Errorf("非法排序字段应被忽略: %s", body)
	}
	// 搜索。
	f = dtForm("id")
	f.Set("search[value]", "admin@")
	if body := e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", f.Encode()); !strings.Contains(body, `"recordsFiltered":1`) {
		t.Errorf("用户搜索不正确: %s", body)
	}
}

func TestAdminActions(t *testing.T) {
	e, uid := adminEnv(t)
	ctx := context.Background()
	if r := e.post("/admin/announcement", url.Values{"content": {"<p>新公告</p>"}, "markdown": {"新公告"}, "page": {"1"}}); r["ret"] != float64(1) {
		t.Fatalf("发布公告失败: %v", r)
	}
	if r := e.post("/admin/node", url.Values{"name": {"新节点"}, "server": {"9.9.9.9;10086;0;tcp;;"}, "sort": {"11"}, "type": {"1"}, "rate": {"1"}, "class": {"0"}, "group": {"0"},
		"node_speedlimit": {"0"}, "node_bandwidth_limit": {"0"}, "bandwidthlimit_resetday": {"1"}, "custom_method": {"0"}, "custom_rss": {"1"}, "mu_only": {"0"}, "status": {"可用"}}); r["ret"] != float64(1) {
		t.Fatalf("添加节点失败: %v", r)
	}
	if r := e.post("/admin/shop", url.Values{"name": {"季付"}, "price": {"25"}, "auto_renew": {"0"}, "auto_reset_bandwidth": {"0"}, "bandwidth": {"300"}, "expire": {"0"},
		"class": {"2"}, "class_expire": {"90"}, "speedlimit": {"0"}, "connector": {"0"}}); r["ret"] != float64(1) {
		t.Fatalf("添加商品失败: %v", r)
	}
	var content string
	_ = e.app.DB.QueryRowContext(ctx, "SELECT content FROM shop WHERE name = '季付'").Scan(&content)
	if content != `{"bandwidth":"300","class":"2","class_expire":"90","speedlimit":"0","connector":"0"}` {
		t.Fatalf("商品内容与原版格式不一致: %s", content)
	}
	if r := e.post("/admin/code", url.Values{"amount": {"3"}, "number": {"10"}}); r["ret"] != float64(1) {
		t.Fatalf("生成充值码失败: %v", r)
	}
	if r := e.post("/admin/coupon", url.Values{"generate_type": {"2"}, "onetime": {"1"}, "expire": {"24"}, "shop": {""}, "credit": {"10"}}); r["ret"] != float64(1) {
		t.Fatalf("生成优惠码失败: %v", r)
	}
	if r := e.post("/admin/invite", url.Values{"uid": {"admin@example.com"}, "num": {"5"}}); r["ret"] != float64(1) {
		t.Fatalf("增加邀请次数失败: %v", r)
	}
	if r := e.post("/admin/invite", url.Values{"uid": {"0"}, "num": {"5"}}); r["ret"] != float64(0) {
		t.Fatalf("用户 0 应提示错误: %v", r)
	}
	if r := e.post("/admin/user/create", url.Values{"userEmail": {"New@Example.com"}}); r["ret"] != float64(1) {
		t.Fatalf("快速创建用户失败: %v", r)
	}
	nu, err := e.app.Q.GetUserByEmail(ctx, "new@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if r := e.post("/admin/user/buy", url.Values{"userEmail": {"new@example.com"}, "shopId": {"1"}, "autorenew": {"0"}}); r["ret"] != float64(1) {
		t.Fatalf("为用户添加套餐失败: %v", r)
	}
	nu2, _ := e.app.Q.GetUser(ctx, nu.ID)
	if nu2.Class != 1 {
		t.Fatalf("套餐未生效: class=%d", nu2.Class)
	}
	// 编辑用户。
	form := url.Values{"email": {"new@example.com"}, "pass": {""}, "auto_reset_day": {"0"}, "auto_reset_bandwidth": {"0"}, "is_multi_user": {"0"},
		"port": {"23456"}, "group": {"0"}, "passwd": {"pw"}, "transfer_enable": {"50"}, "invite_num": {"0"}, "node_speedlimit": {"0"},
		"method": {"rc4-md5"}, "remark": {"备注"}, "money": {"9.5"}, "enable": {"1"}, "is_admin": {"0"}, "ga_enable": {"0"}, "ref_by": {"0"},
		"forbidden_ip": {"1.1.1.1\n2.2.2.2"}, "forbidden_port": {""}, "class": {"3"}, "class_expire": {"2030-01-01 08:00:00"},
		"expire_in": {"2099-06-04 00:05:00"}, "node_connector": {"0"}, "protocol": {"origin"}, "protocol_param": {""}, "obfs": {"plain"}, "obfs_param": {""}}
	if body := e.raw("PUT", "/admin/user/"+strconv.Itoa(int(nu.ID)), "application/x-www-form-urlencoded", form.Encode()); !strings.Contains(body, "修改成功") {
		t.Fatalf("编辑用户失败: %s", body)
	}
	nu3, _ := e.app.Q.GetUser(ctx, nu.ID)
	if nu3.Port != 23456 || nu3.Money.String() != "9.50" || nu3.ForbiddenIp != "1.1.1.1,2.2.2.2" || nu3.TransferEnable != 50<<30 ||
		nu3.ClassExpire.UTC().Format("2006-01-02 15:04:05") != "2030-01-01 00:00:00" || nu3.ExpireIn.Year() != 2099 {
		t.Fatalf("用户字段未正确保存: %+v", nu3)
	}
	// 以用户身份登录后返回管理员。
	if r := e.post("/admin/user/changetouser", url.Values{"userid": {strconv.Itoa(int(nu.ID))}, "local": {"/admin/user"}}); r["ret"] != float64(1) {
		t.Fatalf("切换用户失败: %v", r)
	}
	if !strings.Contains(e.page("/user"), "返回管理员身份") {
		t.Fatal("切换后应显示返回管理员身份")
	}
	if resp := e.get("/admin"); resp.StatusCode == 200 {
		t.Fatal("切换为普通用户后不应能访问后台")
	}
	resp, _ := e.c.PostForm(e.ts.URL+"/user/backtoadmin", nil)
	_ = resp.Body.Close()
	if resp.Header.Get("Location") != "/admin/user" {
		t.Fatalf("应返回原后台页面: %s", resp.Header.Get("Location"))
	}
	e.page("/admin")
	// 删除用户。
	if body := e.raw("DELETE", "/admin/user", "application/x-www-form-urlencoded", "id="+strconv.Itoa(int(nu.ID))); !strings.Contains(body, "删除成功") {
		t.Fatalf("删除用户失败: %s", body)
	}
	_ = uid
}

func TestAdminRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	e.login("normal@example.com")
	if resp := e.get("/admin"); resp.StatusCode == 200 {
		t.Fatal("普通用户不应能访问后台")
	}
	if body := e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", dtForm("id").Encode()); strings.Contains(body, "recordsTotal") {
		t.Fatal("普通用户不应能读取用户列表")
	}
}

// TestAdminNodeSorts 确认节点类型只能选当前使用的四种；已停用类型的旧节点编辑时保留原类型。
func TestAdminNodeSorts(t *testing.T) {
	e, _ := adminEnv(t)
	ctx := context.Background()
	form := func(server, sort string) url.Values {
		return url.Values{"name": {"节点"}, "server": {server}, "sort": {sort}, "type": {"1"}, "rate": {"1"}, "class": {"0"}, "group": {"0"},
			"node_ip": {"9.9.9.9"}, "node_speedlimit": {"0"}, "node_bandwidth_limit": {"0"}, "bandwidthlimit_resetday": {"1"},
			"custom_method": {"0"}, "custom_rss": {"1"}, "mu_only": {"-1"}, "status": {"可用"}}
	}
	options := func(page string) []string {
		i := strings.Index(page, `id="sort"`)
		return regexp.MustCompile(`<option value="(\d+)"`).FindAllString(page[i:i+strings.Index(page[i:], "</select>")], -1)
	}

	create := e.page("/admin/node/create")
	if got := strings.Join(options(create), " "); got != `<option value="11" <option value="14" <option value="15" <option value="16"` {
		t.Fatalf("添加页面的节点类型不正确: %s", got)
	}
	for _, name := range []string{"VMess（V2Ray）", "SS2022", "VLESS REALITY", "AnyTLS"} {
		if !strings.Contains(create, ">"+name+"</option>") {
			t.Errorf("缺少 %s", name)
		}
	}
	for _, sort := range []string{"0", "1", "2", "5", "9", "10", "12", "13", "99"} {
		if r := e.post("/admin/node", form("9.9.9.9", sort)); r["ret"] != float64(0) || !strings.Contains(r["msg"].(string), "节点类型只能是") {
			t.Errorf("不应能新建类型 %s: %v", sort, r)
		}
	}
	if r := e.post("/admin/node", form("ss.example.com;8443;"+fixtureKey, "14")); r["ret"] != float64(1) {
		t.Fatalf("新建 SS2022 节点失败: %v", r)
	}

	// 节点 1 是旧版 Shadowsocks（类型 0）：编辑页面保留原类型并选中，保存后类型不变；不能改成其他已停用类型。
	edit := e.page("/admin/node/1/edit")
	if got := strings.Join(options(edit), " "); got != `<option value="11" <option value="14" <option value="15" <option value="16" <option value="0"` ||
		!strings.Contains(edit, `<option value="0" selected>Shadowsocks（已停用）</option>`) {
		t.Fatalf("旧节点编辑页面的节点类型不正确: %s", got)
	}
	put := func(id string, f url.Values) string {
		return e.raw("PUT", "/admin/node/"+id, "application/x-www-form-urlencoded", f.Encode())
	}
	if body := put("1", form("hk.example.com", "0")); !strings.Contains(body, "修改成功") {
		t.Fatalf("旧节点保持原类型应能保存: %s", body)
	}
	if body := put("1", form("hk.example.com", "10")); !strings.Contains(body, "节点类型只能是") {
		t.Fatalf("旧节点不能改成其他已停用类型: %s", body)
	}
	n, _ := e.app.Q.GetNode(ctx, 1)
	if n.Sort != 0 {
		t.Fatalf("节点类型被改动: %d", n.Sort)
	}
	if body := put("1", form("hk.example.com;8443;"+fixtureKey, "14")); !strings.Contains(body, "修改成功") {
		t.Fatalf("旧节点应能改成当前类型: %s", body)
	}
	// 当前类型的节点编辑页面没有已停用选项，且选中原类型。
	edit = e.page("/admin/node/3/edit")
	if strings.Contains(edit, "已停用") || !strings.Contains(edit, `<option value="11" selected>VMess（V2Ray）</option>`) {
		t.Fatal("VMess 节点编辑页面不正确")
	}
}

// TestAdminUserOnlineIPSort 确认用户列表的在线 IP 数可以升序 / 降序排序，且数值与用户中心的计算相同。
func TestAdminUserOnlineIPSort(t *testing.T) {
	e, admin := adminEnv(t)
	now := time.Now().Unix()
	u1, u2, u3 := e.login2("ip1@example.com"), e.login2("ip2@example.com"), e.login2("ip3@example.com")
	// u1：5.5.5.5 与 ::ffff:5.5.5.5 算同一个，节点 IP 1.1.1.1 与 90 秒前的记录不算，共 3 个；u2：1 个；u3：0 个。
	e.exec(`INSERT INTO alive_ip (nodeid,userid,ip,datetime) VALUES
		(1,?,'5.5.5.5',?),(2,?,'::ffff:5.5.5.5',?),(1,?,'7.7.7.7',?),(1,?,'2001:db8::1',?),(1,?,'::ffff:1.1.1.1',?),(1,?,'8.8.8.8',?),
		(1,?,'9.9.9.9',?)`,
		u1, now, u1, now-10, u1, now-30, u1, now-60, u1, now, u1, now-200,
		u2, now-5)
	query := func(dir string) []map[string]any {
		f := dtForm("op", "id", "online_ip_count")
		f.Set("order[0][column]", "2")
		f.Set("order[0][dir]", dir)
		f.Set("length", "100")
		var res struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", f.Encode())), &res); err != nil {
			t.Fatal(err)
		}
		return res.Data
	}
	// 管理员只有节点 IP 2.2.2.2 的记录（adminEnv），不计入。
	want := map[uint32]string{u1: "3", u2: "1", u3: "0", admin: "0"}
	for _, dir := range []string{"desc", "asc"} {
		rows := query(dir)
		prev := -1
		for i, row := range rows {
			n, _ := strconv.Atoi(row["online_ip_count"].(string))
			if i > 0 && (dir == "desc" && n > prev || dir == "asc" && n < prev) {
				t.Fatalf("%s 排序不正确: %v", dir, rows)
			}
			prev = n
			id, _ := strconv.ParseUint(row["id"].(string), 10, 32)
			if w, ok := want[uint32(id)]; ok && row["online_ip_count"] != w {
				t.Errorf("用户 %d 在线 IP 数为 %v，应为 %s", id, row["online_ip_count"], w)
			}
			// 与用户中心的计算一致。
			got, err := e.app.onlineIPCount(httptest.NewRequest(http.MethodGet, "/", nil), uint32(id), time.Unix(now, 0))
			if err != nil || strconv.Itoa(got) != row["online_ip_count"] {
				t.Errorf("用户 %d：列表 %v，用户中心 %d", id, row["online_ip_count"], got)
			}
		}
		first, _ := strconv.ParseUint(rows[0]["id"].(string), 10, 32)
		last, _ := strconv.ParseUint(rows[len(rows)-1]["id"].(string), 10, 32)
		if dir == "desc" && uint32(first) != u1 || dir == "asc" && uint32(last) != u1 {
			t.Errorf("%s：在线 IP 最多的用户位置不正确", dir)
		}
	}
}

// TestAdminUserSortColumns 确认上次使用时间、上次签到时间、累计充值、邀请人用户名可以排序，显示内容与原来相同。
func TestAdminUserSortColumns(t *testing.T) {
	e, admin := adminEnv(t)
	inv, u1, u2 := e.login2("inv@example.com"), e.login2("s1@example.com"), e.login2("s2@example.com")
	e.exec("UPDATE user SET user_name = 'zz邀请人', ref_by = 0, t = 0, last_check_in_time = 0 WHERE id = ?", inv)
	e.exec("UPDATE user SET ref_by = 0, t = 0, last_check_in_time = 0 WHERE id = ?", admin)
	e.exec("UPDATE user SET ref_by = ?, t = 1700000000, last_check_in_time = 1800000000 WHERE id = ?", inv, u1)
	e.exec("UPDATE user SET ref_by = 999999, t = 1600000000, last_check_in_time = 1500000000 WHERE id = ?", u2)
	e.exec(`INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('S1',-1,10,1,?,UTC_TIMESTAMP()),('S2',-1,20.5,1,?,UTC_TIMESTAMP()),('S3',-1,5,1,?,UTC_TIMESTAMP())`, u1, u1, u2)
	e.exec(`DELETE FROM code WHERE userid NOT IN (?, ?)`, u1, u2)
	query := func(col, dir string) map[uint32]map[string]any {
		f := dtForm("op", "id", col)
		f.Set("order[0][column]", "2")
		f.Set("order[0][dir]", dir)
		f.Set("length", "100")
		var res struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", f.Encode())), &res); err != nil {
			t.Fatal(err)
		}
		out := map[uint32]map[string]any{}
		for i, row := range res.Data {
			id, _ := strconv.ParseUint(row["id"].(string), 10, 32)
			row["_pos"] = i
			out[uint32(id)] = row
		}
		return out
	}
	rows := query("top_up", "desc")
	for id, want := range map[uint32]map[string]string{
		u1:  {"top_up": "30.5", "ref_by_user_name": "zz邀请人", "last_ss_time": time.Unix(1700000000, 0).In(e.app.Loc).Format("2006-01-02 15:04:05"), "last_checkin_time": time.Unix(1800000000, 0).In(e.app.Loc).Format("2006-01-02 15:04:05")},
		u2:  {"top_up": "5", "ref_by_user_name": "邀请人已经被删除"},
		inv: {"top_up": "0", "ref_by_user_name": "系统邀请", "last_ss_time": "从未使用喵", "last_checkin_time": "从未签到"},
	} {
		for k, v := range want {
			if rows[id][k] != v {
				t.Errorf("用户 %d 的 %s 为 %v，应为 %s", id, k, rows[id][k], v)
			}
		}
	}
	// 每列的升序 / 降序：比较这几个用户的相对位置。
	for col, order := range map[string][]uint32{
		"top_up":            {u1, u2, inv}, // 30.5 > 5 > 0
		"last_ss_time":      {u1, u2, inv}, // 1700000000 > 1600000000 > 0
		"last_checkin_time": {u1, u2, inv}, // 1800000000 > 1500000000 > 0
	} {
		for _, dir := range []string{"desc", "asc"} {
			rows := query(col, dir)
			seq := append([]uint32{}, order...)
			if dir == "asc" {
				seq = []uint32{order[2], order[1], order[0]}
			}
			if !inOrder(rows[seq[0]]["_pos"].(int), rows[seq[1]]["_pos"].(int), rows[seq[2]]["_pos"].(int)) {
				t.Errorf("%s %s 排序不正确：%v %v %v", col, dir, rows[seq[0]]["_pos"], rows[seq[1]]["_pos"], rows[seq[2]]["_pos"])
			}
		}
	}
	// 邀请人用户名的中文排序取决于数据库的排序规则，这里只确认升序与降序的相对顺序正好相反。
	desc, asc := query("ref_by_user_name", "desc"), query("ref_by_user_name", "asc")
	ids := []uint32{u1, u2, inv}
	sort.Slice(ids, func(i, j int) bool { return desc[ids[i]]["_pos"].(int) < desc[ids[j]]["_pos"].(int) })
	if !inOrder(asc[ids[2]]["_pos"].(int), asc[ids[1]]["_pos"].(int), asc[ids[0]]["_pos"].(int)) {
		t.Errorf("邀请人用户名的升序不是降序的反向")
	}
}

// inOrder 判断三个位置是否依次递增。
func inOrder(a, b, c int) bool { return a < b && b < c }

// 后台列表中的数据库时间显示为 "2006-01-02 15:04:05"（业务时区），不出现驱动的 RFC 3339 写法（T、Z）；
// 1989、2099 等特殊值原样显示。
func TestAdminTableTimeFormat(t *testing.T) {
	e, uid := adminEnv(t)
	e.exec("UPDATE user SET reg_date = '2026-01-01 16:00:00', class_expire = '2026-01-02 03:04:05', expire_in = '2099-02-19 04:38:03' WHERE id = ?", uid)
	e.exec(`INSERT INTO code (code,type,number,isused,userid,usedatetime) VALUES ('U1',-1,10,1,?,'2026-03-04 05:06:07')`, uid)
	local := func(s string) string {
		v, _ := time.ParseInLocation(time.DateTime, s, time.UTC)
		return v.In(e.app.Loc).Format(time.DateTime)
	}
	rfc := regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d`)
	rows := func(path, col string) []map[string]any {
		body := e.raw("POST", path, "application/x-www-form-urlencoded", dtForm(col).Encode())
		if rfc.MatchString(body) {
			t.Errorf("%s 出现 RFC 3339 时间: %s", path, body)
		}
		var res struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &res); err != nil {
			t.Fatalf("%s 返回不正确: %s", path, body)
		}
		return res.Data
	}
	found := false
	for _, r := range rows("/admin/user/ajax", "op") {
		if r["id"] != strconv.Itoa(int(uid)) {
			continue
		}
		found = true
		if r["reg_date"] != local("2026-01-01 16:00:00") || r["class_expire"] != local("2026-01-02 03:04:05") || r["expire_in"] != "2099-02-19 04:38:03" {
			t.Errorf("用户列表时间不正确: reg_date=%v class_expire=%v expire_in=%v", r["reg_date"], r["class_expire"], r["expire_in"])
		}
	}
	if !found {
		t.Fatal("用户列表中没有测试用户")
	}
	used := map[string]any{}
	for _, r := range rows("/admin/code/ajax", "id") {
		used[r["code"].(string)] = r["usedatetime"]
	}
	if used["U1"] != local("2026-03-04 05:06:07") || used["C1"] != "未使用" {
		t.Errorf("充值码使用时间不正确: %v", used)
	}
	rows("/admin/announcement/ajax", "id")
}
