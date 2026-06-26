# promo 兑换码总开关(rank5,Owner 选 A 方案)— 计划

状态:✅ 已实现待审查合并
日期:2026-06-26
作者:Claude(Owner 明确「选 A」授权)

## 背景
`promo_enabled` 是个**死开关**:全仓唯一消费者是 sitepublichttp(只回显给站点配置),redeem 链路从不读它。
开关默认 "false",但因为没人消费,兑换照常可用。Owner 选 **A 方案**:把它接成真门控 + 把默认翻成 "true"
(**行为保持**——现在啥都不变,但运营者从此能真正一键关闭兑换)。

## 改动(money + 默认翻转,Owner 已授权)
- **默认翻转**:`platformsettings/types.go` `KeyPromoEnabled: "false" → "true"`。这是 Owner 授权的默认行为翻转;
  因开关原本是死的、兑换默认可用,翻成 true 后**对现有部署零行为变更**(兑换仍默认可用)。
- **后端门控**:`gatewayhttp/voucher_handler.go` 加 `promoRedeemEnabled`(读 promo_enabled)+ 在
  newVoucherRedeemHandler 调 Redeem **之前**拦截:运营者显式设 "false" → 403 promo_disabled。
  语义:nil 设置 / 读取出错 / 默认 "true" → 放行(行为保持);**仅显式 "false" 才拦**。
- **接线**:routes.go VoucherUserDeps 注入 d.platformSettings。
- **前端**:RedeemPage 据 siteConfig.promoEnabled 隐藏/禁用兑换表单 + 通知;siteConfig 加 promoEnabled
  (行为保持解析 `!== false`,同 passwordLoginEnabled 思路,与后端门控一致)。
- **测试同步**:sitepublichttp 默认断言 false→true(预期更新)。

## 安全/正确性
- 门控在 Redeem(真金:扣码 + credit 余额 + billing ledger)**之前**短路 → 关闭时码不被消费(变异测试证:
  去门控则关闭时兑换变 200 扣码加余额 → 转红)。
- 前端隐藏只是 UX,**后端门控是最终防线**(前端取配置失败也不影响后端拦截)。
- 行为保持:默认/缺省/出错一律放行,只有运营者主动关才拦——绝不因门控读取失败误伤正常兑换。

## 验证
- 后端:promo 门控测试(建有效码→promo="false" 拦 403 且码未消费→promo nil 兑成功)+ 变异转红;
  **全后端 go test ./... 绿**(默认翻转仅破 sitepublichttp 一个预期断言,已更新)。quality-gate PASS。
- 前端:siteConfig promoEnabled 解析测试 + 变异(===true 致缺省变 false→红);tsc 净;vite build 成功。
