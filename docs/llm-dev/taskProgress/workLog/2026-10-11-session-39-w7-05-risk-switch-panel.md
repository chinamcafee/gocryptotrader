# session-39：`W7-05` 风险开关目录面板（live + 翻转闭环）+ 阻塞项台账（测试网凭据）

**日期**：2026-10-11
**关联任务**：`W7-05`（07 §5.6、Q8d）；阻塞项 `decisions/01-testnet-credentials.md`
**账目**：（66）W7 1/7 → **2/7**（1.00% → 2.00%）；**总完成度 83.8% → 84.8%**

## 1. 交付物

- **阻塞项台账**：`taskProgress/decisions/01-testnet-credentials.md`（测试网凭据与真实供应商口令——阻塞 `M6-03/04/05`、`M1-15`；三选项 + 建议 + 不决策后果）；`decisions/README.md`「当前阻塞项」更新；`TASKS.md` 四条对应行标 `[!]` 并指向该文件。
- **proto**：新增 `LLMGetRiskSwitches` RPC（GET `/v1/llm/riskswitches`）+ `LLMRiskSwitch`/`LLMGetRiskSwitchesRequest/Response` 消息；`buf generate` 连跑幂等 `3e387899…`。
- **引擎**：`engine/rpcserver_llm_strategy.go` 新增 `LLMGetRiskSwitches` handler——目录 18 行逐一映射（live `enabled`（默认值已应用）+ `defaultOn` + **`locked`（红线 4/5 三把锁）** + `reason_code`）；空装配 fail-closed。测试 `TestLLMGetRiskSwitchesCatalogue`（fail-closed / 18 行 / 三把锁全在 / 极端资金默认关 + 拒绝码）。
- **前端**：`HealthPage` 风险开关卡从静态表升级为 **live 面板**——18 项 live 状态（含项数徽标校验 18）、锁定项禁用开关 + Tooltip、未锁定项可翻转（`setLlmRiskSwitch`，每次成功 toast「已写 CONFIG_CHANGED 审计」）、中文语义注解与中英对照保留；`gctApi.ts` 新增 `getLlmRiskSwitches`/`setLlmRiskSwitch` + `LlmRiskSwitches` tag（写后失效重取）。
- **mock**：`GET /v1/llm/riskswitches`（18 行同锁）与 `POST /v1/llm/riskswitch`（未锁定翻转 ok；**锁定项关闭 403**——与后端同语义）。

## 2. 机器化约束（长期有效）

- **锁在目录里声明、由服务强制执行、在 UI 禁用**——三处一致（`haltOnDailyLoss` / `blockOnUnknownOrders` / `blockOnClockDrift`）；静态时代的五个「锁定」猜测已由 live 数据纠正为三个。
- **翻转必留痕**：每次成功翻转 toast 明示写审计；后端审计失败会回滚翻转（M5-17 既定，未变）。
- **面板数据永不装到达**：`llm/status` 与 `llm/coverage` 不可达时各自降级提示（既有约定），风险开关只有 live 一途（无静态冒充）。

## 3. 验证（浏览器实测：mock 9443 → 代理 8788（写开）→ vite 5173）

- 代理链 curl：`GET riskswitches` 18 行；`POST riskswitch blockOnExtremeFunding=true → ok`；`POST riskswitch haltOnDailyLoss=false → 403 "this switch cannot be turned off over RPC"`。
- 浏览器（/health）：面板 18 项绿徽标；三把锁 disabled（haltOnDailyLoss / blockOnUnknownOrders / blockOnClockDrift）；`blockOnExtremeFunding` 因 curl 翻转显示 checked=true（**live 回读**）；UI 翻回关 → toast「blockOnExtremeFunding → 关（已写 CONFIG_CHANGED 审计）」+ 状态刷新 checked=false。
- 引擎：`go build ./gctrpc/ ./engine/` 0 · 聚焦测试全绿 · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W7 组余：`W7-02`（halt 控制——后端 RPC 属 M5 已落，本卡待接活）、`W7-03`（对账/预算卡片接活）、`W7-06`（通知通道配置）、`W7-07`（覆盖轮询运行面 + `LLMGetCoverageStatus` 出口）。
- 队列：W4-06（vault UI，需新 RPC）、W6-06（REVIEW 过期）、SHADOW `RiskFacts` 生产装配、`M2-13` 末项。
- **阻塞**：`M6-03/04/05`、`M1-15` 已标 `[!]` 并入 `decisions/01-testnet-credentials.md`，等用户凭据；其余队列不受阻。
