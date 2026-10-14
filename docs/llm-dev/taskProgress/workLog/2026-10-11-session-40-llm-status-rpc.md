# session-40：`LLMGetStatus` 落地（W7 运行控制读侧）+ 适配器缺口登记

**日期**：2026-10-11
**关联任务**：`W7-01`（读侧）/`W7-02`（读侧）/`W7-03`（读侧）——08 §3.6 运行控制读面
**账目**：不计分（基础设施 RPC；W7-01 写侧与 W7-02/03 的手动触发面仍待适配器装配）；**总完成度维持 84.8%**

## 1. 交付物

- **proto**：`LLMGetStatus` RPC（GET `/v1/llm/status`）+ `LLMGetStatusResponse`（17 字段）；`buf generate` 连跑幂等 `327a910f…`。
- **引擎** `engine/rpcserver_llm_control.go`：`LLMGetStatus` handler——**只填可诚实取数的字段**：
  - `runMode`（配置值，`normaliseLLMRunMode`）；`liveUnlocked` = 模式已为 LIVE（**三重闸是模式落定的原因，模式本身是结果——没有独立的可读解锁标志，不发明一个**）；
  - 配对：`pairReadiness`/`pairEpoch`/两侧 档案名+模型（pair→revision→profile）；从未激活 → `INCOMPLETE`（状态不是错误）；
  - 探针年龄：两侧最新 `llm_probe_results`；
  - 停机：`ActiveHalts` → `halted` + `haltLevel`（FULL>MARKET>SOFT，`highestHaltLevel` 纯函数）；
  - 对账：`LatestReconciliation` → `accountReconciled` + 年龄（`CompletedAt` 空则 0）；
  - 预算：`reservationsRemaining24h` = 配置上限 − 窗口内预约数（无配对/无上限则不填）；
  - `approvedStrategies` = `ListActiveStrategyApprovals` 计数；`checkedAt`。
  - **诚实缺席**：`clockDriftMs`/`oldestDataAgeSeconds`/`runningStrategies`/`decisionsToday`/`newRiskIntents*`/`tradeAuthorized`/`executionReady` **不进消息**——「诚实的『—』胜过伪造的 0」。
  - 测试 `TestLLMGetStatusStates`（fail-closed / 未激活=INCOMPLETE / 配对+探针年龄 / 预约余量 / `highestHaltLevel` 纯函数）。
- **前端**：站点头部徽章与健康页自动转 live（复用既有 `getLlmStatusQuery`）——徽章「SHADOW｜配对 READY · epoch 3｜对账 已对」、停机卡「当前级别 NONE」、对账预算卡「已对账 是 / 预约剩余 2」；**`tradeAuthorized` 字段缺席渲染『—』**（此前缺席时渲染 false——把「没报」伪装成「报了 false」）；`DecisionsPage` 统计卡 `decisionsToday` 缺席同样改『—』。
- **mock**：`GET /v1/llm/status` 离线形状。

## 2. 机器化约束（长期有效）

- **缺席字段不进消息**：状态读写模型只包含可溯源字段；任何「没数据但先给 0」都是把未知伪装成已知。
- **LIVE 解锁不设独立旗标**：能读的只有模式；三重闸过不去模式就永远不落——旗标会多出一个可以撒谎的位。
- **halt 级别取最严**：多个活跃停机按 FULL>MARKET>SOFT 报告。

## 3. 验证

- 引擎：`go build ./gctrpc/ ./engine/` 0；`TestLLMGetStatusStates` + `TestLLMGetRiskSwitchesCatalogue` 全绿。
- 浏览器（mock 栈）：`/health` 头部徽章 live；停机卡 NONE；对账预算卡「是 / 2」；时钟偏差与最旧数据年龄诚实『—』；风险开关面板不受影响。
- `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步（含新登记的适配器缺口）

- **W7-01 写侧（halt 三级控制 + 清除）待生产适配器**：`HaltController` 需要 `iRunningStrategyPauser`（SOFT 即需要）、`iBotOrderCanceller`（MARKET/FULL）、`iPositionFlattener`（FULL+flatten）的**生产实现**——目前仅测试侧实现（与 M6-01 A14「撤单适配器属装配」同源）。读侧本日已 live。
- **W7-02 手动触发对账**待 `LLMReconcile` RPC + 账户/订单快照适配器（reconcile runner 的输入面）。
- **W7-03 token 计数/自估成本**待 accounting 读盘（`llm_accounting_*` 表尚无仓储读面）与口径确认；预约余量显示本日已 live。
- 其余队列不变：W4-06（vault RPC + UI）、W6-06、W7-06/07、SHADOW `RiskFacts`、`M2-13` 末项；`M6-03/04/05`、`M1-15` 等凭据（decisions/01）。
