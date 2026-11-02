package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestAdminUserBought 后台「查套餐」：用户列表的按钮与付费用户数；购买明细页；添加（不扣余额、订单价格 0）、
// 购买（扣余额、余额不足提示且不改动）；关闭其他套餐的自动续费；列表字段；删除记录（只删除本用户的记录，
// 删除最新订单时关闭其余订单的续费）；非管理员不能访问。
func TestAdminUserBought(t *testing.T) {
	e, admin := adminEnv(t)
	ctx := context.Background()
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES
		(2,'周期<b>',20.00,'{"bandwidth":"200","class":"2","class_expire":"30","reset":"7","reset_value":"50","reset_exp":"30"}',30,1,1),
		(3,'下架',5.00,'{"bandwidth":"1"}',0,0,0)`)
	var uid uint32
	if r := e.post("/admin/user/create", url.Values{"userEmail": {"buyer@example.com"}}); r["ret"] != float64(1) {
		t.Fatalf("创建用户失败: %v", r)
	}
	_ = e.app.DB.QueryRow("SELECT id FROM user WHERE email = 'buyer@example.com'").Scan(&uid)
	id := strconv.Itoa(int(uid))
	e.exec("UPDATE user SET money = 15, class = 0 WHERE id = ?", uid)

	// 用户列表：查套餐按钮、付费用户数（adminEnv 的管理员与新用户都没有有效等级）。
	if page := e.page("/admin/user"); !strings.Contains(page, "付费用户：0") {
		t.Fatalf("用户列表应显示付费用户数")
	}
	var list struct {
		Data []map[string]string `json:"data"`
	}
	_ = json.Unmarshal([]byte(e.raw("POST", "/admin/user/ajax", "application/x-www-form-urlencoded", "draw=1&start=0&length=10")), &list)
	found := false
	for _, row := range list.Data {
		if row["id"] == id {
			found = strings.Contains(row["op"], `href="/admin/user/`+id+`/bought">查套餐</a>`)
		}
	}
	if !found {
		t.Fatalf("操作列应有查套餐按钮: %v", list.Data)
	}

	page := e.page("/admin/user/" + id + "/bought")
	if !strings.Contains(page, "#"+id+" [") || !strings.Contains(page, "用户购买明细") || !strings.Contains(page, "周期&lt;b&gt;（20.00 元）") || strings.Contains(page, "下架") {
		t.Fatalf("购买明细页不正确")
	}
	for _, col := range []string{"是否有效期内", "自动续费时间", "流量重置时间", "套餐购买时间", "套餐过期时间", "商品详细内容"} {
		if !strings.Contains(page, col) {
			t.Errorf("缺少表项 %s", col)
		}
	}
	if e.get("/admin/user/999999/bought").StatusCode != 404 {
		t.Error("不存在的用户应 404")
	}

	post := func(shop, typ string) map[string]any {
		return e.post("/admin/user/"+id+"/bought", url.Values{"shop": {shop}, "type": {typ}})
	}
	// 购买：余额不足时提示且不改动。
	if r := post("2", "1"); r["ret"] != float64(0) || !strings.Contains(r["msg"].(string), "余额不足") {
		t.Fatalf("余额不足应提示: %v", r)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.Money.String() != "15.00" || u.Class != 0 {
		t.Fatalf("余额不足时不应改动: %s %d", u.Money, u.Class)
	}
	// 下架商品、未选择套餐、未知类型都拒绝。
	for _, c := range [][2]string{{"3", "0"}, {"", "0"}, {"1", "9"}} {
		if r := post(c[0], c[1]); r["ret"] != float64(0) {
			t.Errorf("%v 应拒绝: %v", c, r)
		}
	}
	// 一条旧订单开启了续费（旧数据）。
	e.exec("INSERT INTO bought (userid,shopid,datetime,renew,coupon,price) VALUES (?,1,UNIX_TIMESTAMP()-100,UNIX_TIMESTAMP()+100,'',10)", uid)
	// 添加：不扣余额，订单价格记为 0，套餐生效，关闭旧订单的续费。
	if r := post("1", "0"); r["ret"] != float64(1) || r["msg"] != "套餐添加成功" {
		t.Fatalf("添加失败: %v", r)
	}
	u, _ := e.app.Q.GetUser(ctx, uid)
	if u.Money.String() != "15.00" || u.Class != 1 {
		t.Fatalf("添加不应扣余额且套餐应生效: %s %d", u.Money, u.Class)
	}
	var price string
	var renewing int
	_ = e.app.DB.QueryRow("SELECT price FROM bought WHERE userid = ? ORDER BY id DESC LIMIT 1", uid).Scan(&price)
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&renewing)
	if price != "0.00" || renewing != 0 {
		t.Fatalf("添加的订单价格应为 0，旧订单的续费应关闭: %s %d", price, renewing)
	}
	// 购买：扣余额，订单价格为套餐价格。
	e.exec("UPDATE user SET money = 25 WHERE id = ?", uid)
	if r := post("2", "1"); r["ret"] != float64(1) || !strings.Contains(r["msg"].(string), "扣除 20.00 元") {
		t.Fatalf("购买失败: %v", r)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.Money.String() != "5.00" || u.Class != 2 {
		t.Fatalf("购买应扣余额且套餐生效: %s %d", u.Money, u.Class)
	}
	if page := e.page("/admin/user"); !strings.Contains(page, "付费用户：1") {
		t.Error("购买后付费用户应为 1")
	}

	// 列表：新的在前；周期重置套餐显示有效、过期时间与下次重置日期，普通套餐显示“-”。
	body := e.raw("POST", "/admin/user/"+id+"/bought/ajax", "application/x-www-form-urlencoded", "draw=1&start=0&length=10&order[0][column]=0&order[0][dir]=desc&columns[0][data]=op")
	var resp struct {
		Total int                 `json:"recordsTotal"`
		Data  []map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Total != 3 || len(resp.Data) != 3 {
		t.Fatalf("列表应有 3 条记录: %v %s", err, body)
	}
	cyc, plain := resp.Data[0], resp.Data[1]
	if cyc["name"] != "周期&lt;b&gt;" || cyc["valid"] != "有效" || cyc["exp_time"] == "-" || len(cyc["reset_time"]) != 10 || cyc["auto_renew"] != "不自动续费" || !strings.Contains(cyc["op"], "删除") {
		t.Errorf("周期重置套餐的行不正确: %v", cyc)
	}
	if plain["valid"] != "-" || plain["exp_time"] != "-" || plain["reset_time"] != "-" || !strings.Contains(plain["content"], "100") {
		t.Errorf("普通套餐的行不正确: %v", plain)
	}

	// 删除：其他用户的记录不能删；删除最新订单时关闭其余订单的续费。
	var adminBought, latest, older int
	_ = e.app.DB.QueryRow("SELECT id FROM bought WHERE userid = ? LIMIT 1", admin).Scan(&adminBought)
	if body := e.raw("DELETE", "/admin/user/"+id+"/bought", "application/x-www-form-urlencoded", "id="+strconv.Itoa(adminBought)); !strings.Contains(body, "购买记录不存在") {
		t.Fatalf("不应删除其他用户的记录: %s", body)
	}
	_ = e.app.DB.QueryRow("SELECT MAX(id), MIN(id) FROM bought WHERE userid = ?", uid).Scan(&latest, &older)
	e.exec("UPDATE bought SET renew = UNIX_TIMESTAMP() + 100 WHERE id = ?", older)
	if body := e.raw("DELETE", "/admin/user/"+id+"/bought", "application/x-www-form-urlencoded", "id="+strconv.Itoa(latest)); !strings.Contains(body, "删除成功") {
		t.Fatalf("删除失败: %s", body)
	}
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&renewing)
	if _, total := e.renewCountAll(uid); total != 2 || renewing != 0 {
		t.Fatalf("删除最新订单后应剩 2 条且都不续费: %d %d", total, renewing)
	}
	if u, _ := e.app.Q.GetUser(ctx, uid); u.Class != 2 || u.Money.String() != "5.00" {
		t.Errorf("删除记录不应撤销已生效的套餐: %d %s", u.Class, u.Money)
	}

	// 非管理员不能访问。
	e.get("/user/logout")
	e.login("normal@example.com")
	if r := e.get("/admin/user/" + id + "/bought"); r.StatusCode == 200 {
		t.Error("非管理员不应访问购买明细")
	}
	if status, _, _ := e.response("POST", "/admin/user/"+id+"/bought", "shop=1&type=0"); status == 200 {
		t.Error("非管理员不应添加套餐")
	}
	if _, total := e.renewCountAll(uid); total != 2 {
		t.Errorf("非管理员的请求不应写入订单: %d", total)
	}
}

func (e *testEnv) renewCountAll(uid uint32) (enabled, total int) {
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ? AND renew <> 0", uid).Scan(&enabled)
	_ = e.app.DB.QueryRow("SELECT COUNT(*) FROM bought WHERE userid = ?", uid).Scan(&total)
	return
}

// TestAdminUserBoughtSort 购买明细的排序：是否有效期内、流量重置时间、套餐过期时间、商品详细内容由程序计算，
// 页面不提供排序（orderable: false），服务端也不按它们排序；其余列按数据库中的真实值排序，
// 订单 ID 与购买时间顺序相反时，套餐购买时间的升序、降序与跨页都按日期。（第三方 P2）
func TestAdminUserBoughtSort(t *testing.T) {
	e, _ := adminEnv(t)
	e.exec(`INSERT INTO shop (id,name,price,content,auto_renew,auto_reset_bandwidth,status) VALUES
		(2,'周期',20.00,'{"bandwidth":"200","class":"2","class_expire":"30","reset":"7","reset_value":"50","reset_exp":"30"}',30,1,1)`)
	if r := e.post("/admin/user/create", url.Values{"userEmail": {"sort@example.com"}}); r["ret"] != float64(1) {
		t.Fatal(r)
	}
	var uid uint32
	_ = e.app.DB.QueryRow("SELECT id FROM user WHERE email = 'sort@example.com'").Scan(&uid)
	id := strconv.Itoa(int(uid))
	// ID 与购买时间顺序相反：101 一天前、102 四天前（周期套餐）；103 两天前（普通套餐，日期列为“-”）。
	e.exec(`INSERT INTO bought (id,userid,shopid,datetime,renew,coupon,price) VALUES
		(101,?,2,UNIX_TIMESTAMP()-86400,0,'',20),(102,?,2,UNIX_TIMESTAMP()-4*86400,0,'',20),(103,?,1,UNIX_TIMESTAMP()-2*86400,0,'',10)`, uid, uid, uid)

	page := e.page("/admin/user/" + id + "/bought")
	if !strings.Contains(page, `var noSortKeys = ["valid","reset_time","exp_time","content"];`) || !strings.Contains(page, `"orderable": noSortKeys.indexOf(k) < 0`) {
		t.Fatal("页面应把计算列设为不能排序")
	}
	cols := []string{"op", "id", "name", "valid", "auto_renew", "reset_time", "buy_time", "exp_time", "content"}
	query := func(col, dir string, start, length int) []map[string]string {
		t.Helper()
		v := url.Values{"draw": {"1"}, "start": {strconv.Itoa(start)}, "length": {strconv.Itoa(length)}, "order[0][dir]": {dir}}
		for i, c := range cols {
			v.Set("columns["+strconv.Itoa(i)+"][data]", c)
			if c == col {
				v.Set("order[0][column]", strconv.Itoa(i))
			}
		}
		var resp struct {
			Total int                 `json:"recordsTotal"`
			Data  []map[string]string `json:"data"`
		}
		if err := json.Unmarshal([]byte(e.raw("POST", "/admin/user/"+id+"/bought/ajax", "application/x-www-form-urlencoded", v.Encode())), &resp); err != nil || resp.Total != 3 {
			t.Fatalf("列表: %v %d", err, resp.Total)
		}
		return resp.Data
	}
	ids := func(rows []map[string]string) string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r["id"])
		}
		return strings.Join(out, ",")
	}
	// 套餐购买时间按真实日期：升序 102（4 天前）、103、101；降序相反；跨页（每页 1 条）顺序一致。
	if got := ids(query("buy_time", "asc", 0, 10)); got != "102,103,101" {
		t.Errorf("购买时间升序: %s", got)
	}
	if got := ids(query("buy_time", "desc", 0, 10)); got != "101,103,102" {
		t.Errorf("购买时间降序: %s", got)
	}
	var pages []string
	for start := 0; start < 3; start++ {
		pages = append(pages, ids(query("buy_time", "asc", start, 1)))
	}
	if got := strings.Join(pages, ","); got != "102,103,101" {
		t.Errorf("购买时间升序跨页: %s", got)
	}
	if got := ids(query("id", "desc", 0, 10)); got != "103,102,101" {
		t.Errorf("ID 降序: %s", got)
	}
	// 显示：102 的重置日期与过期时间早于 101；普通套餐 103 为“-”。
	rows := map[string]map[string]string{}
	for _, r := range query("id", "asc", 0, 10) {
		rows[r["id"]] = r
	}
	if rows["102"]["reset_time"] >= rows["101"]["reset_time"] || rows["102"]["exp_time"] >= rows["101"]["exp_time"] ||
		rows["103"]["reset_time"] != "-" || rows["103"]["exp_time"] != "-" || rows["103"]["valid"] != "-" || rows["101"]["valid"] != "有效" {
		t.Errorf("日期列显示不正确: %v", rows)
	}
	// 计算列：即使请求按它们排序，服务端也不按计算结果或原始内容排序，按默认的订单 ID（与请求的方向）返回，
	// 页面不会发出这样的请求。
	for _, col := range []string{"valid", "reset_time", "exp_time", "content"} {
		for _, dir := range []string{"asc", "desc"} {
			want := ids(query("op", dir, 0, 10))
			if got := ids(query(col, dir, 0, 10)); got != want {
				t.Errorf("%s %s 不应按计算结果排序: %s（默认 %s）", col, dir, got, want)
			}
		}
	}
}
