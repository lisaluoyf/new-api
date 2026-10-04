# 委托钱包 ERC-20 充值修复与审计

日期：2026-10-04。范围：EVM ERC-20 充值校验；不修改生产余额、历史订单或后台补账。

## 根因与修复

旧版 `verifyIntentOnChain` 在读取 ERC-20 Transfer 事件之前要求外层 `tx.from` 等于订单的钱包地址、`tx.to` 等于代币合约地址。MetaMask 委托交易可由其他地址发起并调用 DelegationManager，即使代币实际从订单钱包转入平台，也会被这两个条件拦下。

修复保留原生币的外层发送地址、收款地址与金额校验；ERC-20 则以成功且确认数足够的链上 receipt 中的 Transfer 事件校验代币合约、实际付款钱包、平台收款地址与准确金额。未改动签名授权、账号归属、交易哈希去重、结算事务或失败订单状态机。

## 审计结果

本次补丁范围内未发现阻断发布的新增问题。逐项检查如下：

- 授权：仍需订单钱包的有效 challenge 签名；缺少签名、签名无效、另一个钱包的有效签名和其他账号的订单均不能冒领。
- 合约与付款方：仅接受订单指定代币合约发出的 Transfer；实际付款方必须是订单钱包，不使用外层交易发送者替代。
- 收款与金额：平台收款地址与 base units 必须匹配。其他合约、钱包、收款地址、事件、金额及缺少日志的情形均有拒绝测试。
- 交易完成：链上交易失败或确认数不足仍被拒绝。
- 委托手续费：同一 receipt 中前后出现的手续费 Transfer 不会覆盖匹配的平台收款事件。
- 原生币：保留外层发送者、收款方和金额检查；本补丁不支持原生币内部委托转账。
- 重复记账：保留 `(chain, tx_hash)` 唯一约束、恢复绑定的去重与结算事务；已有的只入账一次测试通过。
- 人工补账：失败充值单不会被重新校验或结算；新增测试确认失败单保持 failed，已人工增加的额度不再增加。

## 验证

新增 5 个顶层测试，覆盖 25 个叶级测试场景；RPC 使用本地模拟服务，持久化使用临时内存数据库，不发起真实充值或生产结算。

- `go test ./...`：通过。
- `go test ./controller ./model -count=1`：通过。
- `go test -race ./controller ./model -run 'Crypto|VerifyEVM|VerifyTron|VerifySolana' -count=1`：通过。
- `go test -race ./controller ./model -count=1`：首次通过，最后复跑时 model 包触发既有推荐奖励测试的异步通知竞态；controller 包通过。堆栈为 `NotifyReferralGPTRewardToFeishu` 读取全局数据库与 `setupReferralGPTRewardTestDB` 清理恢复数据库之间的冲突，触发测试为 `TestOnTopupSucceededBackfillsMissingCompleteTimeForReferralRewards`。这些文件本次均未修改，未扩展补丁修复该测试问题。
- `go vet ./controller ./model`：通过。
- `gofmt` 与 `git diff --check`：通过。

## 发布与历史订单边界

当前仅本地修改，未 commit、push 或部署。生产发布必须先成功提交并推送 GitHub，再从推送版本部署并核对生产版本。

本次修复不会把历史 failed 订单改回 pending，也不会触发人工补账订单再次结算。已人工补账的历史单应保留补账凭据并单独对账；不应为了显示成功而重开自动结算流程。
