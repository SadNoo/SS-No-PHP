# 待移植模板

这里是已从原版机械转换、但所属功能尚未移植的模板（不会打包进程序）。
对应功能移植时，人工复核后移回 `web/templates/`。

- `pages/user/*pay*.html`、`chenPay.html`、`doiam.html`、`pay_success.html`：其他支付渠道（支付宝当面付已移植为 `partials/payment/aopf2f.html`）
- 其余用户中心页面（资料编辑、邀请、商店、充值、工单、中转规则、审计等）：M3 后续批次
- `pages/admin/payEdit.html`：ChenPay 支付配置（M8）
