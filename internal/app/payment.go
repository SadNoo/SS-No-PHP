package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SadNoo/SS-No-PHP/internal/auth"
	"github.com/SadNoo/SS-No-PHP/internal/dec"
	"github.com/SadNoo/SS-No-PHP/internal/payment/alipay"
	"github.com/SadNoo/SS-No-PHP/internal/server"
	"github.com/SadNoo/SS-No-PHP/internal/store"
)

// maxPayAmount 是单笔在线充值的上限，防止误输入超大金额（原版没有任何校验）。
const maxPayAmount = dec.D(100000 * 100)

// userPaymentRoutes 注册 /user 下的支付路由（对应原版 Payment::purchase / returnHTML）。
func (a *App) userPaymentRoutes(r chi.Router) {
	r.Post("/payment/purchase", a.purchase)
	r.Post("/code/f2fpay", a.purchase)
	r.Get("/payment/return", a.paymentReturn)
}

// paymentRoutes 注册 /payment 下的回调与状态查询。
//
// 回调由支付网关的服务器发起，不带浏览器的 Origin / Sec-Fetch-Site 头，不受跨站拦截影响；
// 其可信性完全由签名校验保证。
func (a *App) paymentRoutes(r chi.Router) {
	r.Post("/payment/notify", a.paymentNotify)
	r.Post("/payment/notify/{type}", a.paymentNotify)
	// 原版的状态查询不需要登录，任何人都能按订单号查询任意订单是否已支付（K46）。
	r.With(auth.RequireLogin).Post("/payment/status", a.paymentStatus)
}

// initPayment 按 payment_system 初始化支付渠道。配置错误只记录日志，充值页面不显示在线支付。
func (a *App) initPayment() {
	p := a.Cfg.Payment
	if p.System != "f2fpay" {
		if p.System != "" && p.System != "none" {
			a.Log.Warn("该支付渠道尚未移植，在线充值不可用", "payment_system", p.System)
		}
		return
	}
	notifyURL := p.F2FNotifyURL
	if notifyURL == "" {
		notifyURL = a.Cfg.App.BaseURL + "/payment/notify"
	}
	c, err := alipay.New(p.F2FAppID, p.F2FMerchantPrivateKey, p.F2FAlipayPublicKey, p.F2FGateway, notifyURL)
	if err != nil {
		a.Log.Error("支付宝当面付配置错误，在线充值不可用", "err", err)
		return
	}
	a.F2F = c
}

// purchaseEnabled 当前是否有可用的在线支付渠道。
func (a *App) purchaseEnabled() bool { return a.F2F != nil }

// newTradeNo 生成 8 位订单号（与原版 generateGuid 的长度、字符集一致，但使用安全随机数并保证不重复）。
func (a *App) newTradeNo(r *http.Request) (string, error) {
	for range 10 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		no := strings.ToUpper(hex.EncodeToString(b[:]))
		n, err := a.Q.CountPaylistTradeno(r.Context(), no)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return no, nil
		}
	}
	return "", errors.New("无法生成不重复的订单号")
}

// purchase 对应 AopF2F::purchase：创建订单并返回收款二维码。
//
// 原版只检查金额非空，负数、非数字都会写入订单并提交给支付宝（K47）。
func (a *App) purchase(w http.ResponseWriter, r *http.Request) {
	if !a.purchaseEnabled() {
		a.json(w, fail("在线充值未开启"))
		return
	}
	raw := trimmed(r, "amount")
	amount, err := dec.Parse(raw)
	if err != nil || amount <= 0 || amount > maxPayAmount {
		a.json(w, fail("订单金额错误："+raw))
		return
	}
	tradeNo, err := a.newTradeNo(r)
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	u := auth.User(r)
	if err := a.Q.InsertPaylist(r.Context(), store.InsertPaylistParams{Userid: u.ID, Total: amount, Tradeno: tradeNo, Datetime: time.Now().Unix()}); err != nil {
		a.jsonError(w, r, err)
		return
	}
	qr, err := a.F2F.Precreate(r.Context(), tradeNo, tradeNo, amount.String())
	if err != nil {
		// 原版在支付宝返回错误时仍返回 ret=1 和空二维码。
		a.Log.Warn("支付宝下单失败", "tradeno", tradeNo, "err", err)
		a.json(w, fail("连接支付网关失败，请稍后再试"))
		return
	}
	a.json(w, Ret{"ret": 1, "qrcode": qr, "amount": amount.Float(), "pid": tradeNo})
}

// paymentNotify 对应 AopF2F::notify：支付宝异步通知。
//
// 原版未核对通知金额与订单金额；这里金额不符时不入账（K47）。
func (a *App) paymentNotify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if a.F2F == nil {
		_, _ = w.Write([]byte("fail"))
		return
	}
	if err := r.ParseForm(); err != nil {
		_, _ = w.Write([]byte("fail"))
		return
	}
	n, err := a.F2F.VerifyNotify(r.PostForm)
	if err != nil {
		a.Log.Warn("支付宝通知校验失败", "ip", server.ClientIP(r), "err", err)
		_, _ = w.Write([]byte("fail"))
		return
	}
	if !n.Paid() {
		_, _ = w.Write([]byte("success"))
		return
	}
	paid, err := dec.Parse(n.TotalAmount)
	if err != nil {
		_, _ = w.Write([]byte("fail"))
		return
	}
	if err := a.postPayment(r, n.OutTradeNo, paid, "支付宝当面付 "+n.OutTradeNo); err != nil {
		a.Log.Warn("支付入账失败", "tradeno", n.OutTradeNo, "err", err)
		_, _ = w.Write([]byte("fail"))
		return
	}
	_, _ = w.Write([]byte("success"))
}

var errPayAmount = errors.New("通知金额与订单金额不符")

// postPayment 对应 AbstractPayment::postPayment：标记订单已支付并为用户入账。
//
// 原版先读状态再保存，支付网关重复通知并发到达时会重复入账（K45）。这里在一个事务中
// 用条件更新标记订单，只有真正把订单从未支付改为已支付的那次通知会入账。
func (a *App) postPayment(r *http.Request, tradeNo string, paid dec.D, method string) error {
	ctx := r.Context()
	now := time.Now()
	var donor *store.User
	var total dec.D
	err := a.tx(ctx, func(q *store.Queries) error {
		p, err := q.GetPaylistByTradeno(ctx, tradeNo)
		if err != nil {
			return err
		}
		if p.Total != paid {
			return errPayAmount
		}
		rows, err := q.MarkPaylistPaid(ctx, tradeNo)
		if err != nil || rows == 0 {
			return err // rows == 0：已经处理过的重复通知
		}
		u, err := q.GetUserForUpdate(ctx, p.Userid)
		if err != nil {
			return err
		}
		if err := q.AddUserMoney(ctx, store.AddUserMoneyParams{Amount: p.Total, ID: u.ID}); err != nil {
			return err
		}
		if err := q.InsertCode(ctx, store.InsertCodeParams{Code: method, Type: -1, Number: p.Total, Isused: 1, Userid: u.ID, Usedatetime: now}); err != nil {
			return err
		}
		// code_payback 为 0 时不返利，也不写返利记录。
		if u.RefBy >= 1 && a.Cfg.Users.CodePayback > 0 {
			get := p.Total.MulPercent(a.Cfg.Users.CodePayback)
			if err := q.AddUserMoney(ctx, store.AddUserMoneyParams{Amount: get, ID: u.RefBy}); err != nil {
				return err
			}
			if err := q.InsertPayback(ctx, store.InsertPaybackParams{Total: p.Total, Userid: u.ID, RefBy: u.RefBy, RefGet: get, Datetime: now.Unix()}); err != nil {
				return err
			}
		}
		donor, total = &u, p.Total
		return nil
	})
	if err != nil {
		return err
	}
	if donor != nil && a.Cfg.Display.EnableDonate {
		if donor.IsHide == 1 {
			a.tg("一位不愿透露姓名的大老爷给我们捐了 "+total.String()+" 元!", false)
		} else {
			a.tg(donor.UserName+" 大老爷给我们捐了 "+total.String()+" 元！", false)
		}
	}
	return nil
}

// paymentStatus 对应 AopF2F::getStatus：充值页面轮询订单是否已支付。只能查询自己的订单（K46）。
func (a *App) paymentStatus(w http.ResponseWriter, r *http.Request) {
	p, err := a.Q.GetPaylistByTradeno(r.Context(), trimmed(r, "pid"))
	if isNoRows(err) || (err == nil && p.Userid != auth.User(r).ID) {
		a.json(w, Ret{"ret": 0, "result": 0})
		return
	}
	if err != nil {
		a.jsonError(w, r, err)
		return
	}
	a.json(w, Ret{"ret": 1, "result": p.Status})
}

// paymentReturn 对应 Payment::returnHTML。当面付没有同步跳转页（原版返回 0），这里回到充值页面。
func (a *App) paymentReturn(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/user/code", http.StatusFound)
}

// purchaseNotice 返回购买说明的各行（payment.notice），模板逐行显示并转义。
func (a *App) purchaseNotice() []string {
	var lines []string
	for _, l := range strings.Split(a.Cfg.Payment.Notice, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
