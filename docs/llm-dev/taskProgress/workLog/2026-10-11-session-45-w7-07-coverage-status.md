# 2026-10-11 session-45（同日续段）：W7-07 覆盖轮询运行面（LLMGetCoverageStatus RPC + 健康页运行面卡）

## 会话目标

补齐 W7-07：把 M2-14 旋转器的**实测运行态**搬上 RPC（周期进度、最旧合约年龄、实际权重占用、上次周期完成时刻），并让健康页覆盖卡从「计划值」升级为「运行值」。

## 完成的子任务

- `W7-07` 覆盖轮询运行面 + `LLMGetCoverageStatus` RPC 出口

## 具体做了什么

### 后端

1. `engine/llm_rotation.go`：`RotationStats` +`LastCycleCompletedAt`（sweep 收尾时记 `now`）——**完成时刻是运行证据，不只是计数**（「它上次跑完是什么时候」是操作员判断 sweep 是否活着的问题）。
2. `gctrpc/rpc.proto` +`LLMGetCoverageStatusRequest/Response` + RPC（GET `/v1/llm/coverage`）——**套在既有的 501 计划路径上**：前端 `getCoverageStatus`（gctApi 早已定义、HealthPage 早已消费）零改动转 live。`buf generate` 连跑两次同哈希 `25fde8b89cc6648531465e34f90817488a65d605`。
3. `engine/rpcserver_llm_coverage.go`（新）：**计划值与实测值同响应分列**——`scope`/`rotationCycleMinutes` 来自配置映射（系统配置成什么样），`visitedThisCycle`/`cycleProgressPercent`/`worstContractAgeSeconds`/`weightUsedPerMinute`/`lastCycleCompletedAt`/`failures`/… 来自旋转器实测（它此刻在做什么）。两态语义：coverage 未启用 = `enabled:false`（合法配置不是错误）；LLM 子系统未装配 = fail-closed 拒——「读不到」与「读到未启用」不许长得一样。
4. `engine/rpcserver_llm_coverage_test.go`（新）：unassembled 拒 / 未启用态 / **真旋转器**（sweep 中途：ticker 批次后一次访问，visited=1、进度 50%、最冷-market 年龄为正；收尾后 `cyclesCompleted=1` + `lastCycleCompletedAt` 非空）。**测试坑**：夹具时钟必须避开整点重载窗（`hour` 后 `fullReloadSpreadMinutes` 内 Step 走重载分支而不计 visited）——首次选 12:00 连撞四次断言，改 12:30 即顺。

### 前端

1. `types.ts` `CoverageStatus` +9 运行面字段（visitedThisCycle/cyclesCompleted/failures/reloadsThisHour/reloadMarkets/lastTickerRefresh/lastReload/incrementSpacingMs/checkedAt）。
2. `HealthPage` 覆盖卡升级「运行面 W7-07」：范围·合约 / **周期进度 visited/总数（%）** / 最旧合约年龄 / **实际权重占用 x / y /min** / **周期完成次数 + 上次完成时刻** / **失败计数**（>0 红显）+ 脚注（最近 ticker 刷新 / 最近整点重载 / 增量间距）；未启用显示合法态文案；「计划值见合约覆盖页」保留指路。
3. `mock-gateway.mjs` +GET `/v1/llm/coverage`（sweep 进行到一半的样本：214/588、36.4%、失败 2）。

## 验证证据

- `go build ./...` 绿 · `go test ./engine/ -count 1 -run 'TestLLMGetCoverageStatus'` **ok**（1.008s）· `gofumpt` 无输出。
- `buf generate` ×2 同哈希 `25fde8b8…`。
- `pnpm typecheck` 0 · `pnpm lint` 0/0 · `pnpm build` 成功（1.71s）。
- **代理链 curl**：`/api/llm/coverage` → `{"enabled":true,"scope":"ALL_USDTM_PERPETUAL","contractCount":588,…}`。
- **浏览器实测**（/health，mock 栈）：卡逐行断言——`ALL_USDTM_PERPETUAL · 588` / `214 / 588 合约（36%） · 10 分钟/周期` / `最旧合约年龄 331s` / `权重占用 22 / 1440 /min` / `周期完成 412 次 · 上次 2026-10-11 12:50:03` / `失败计数 2` + 脚注文本（最近 ticker 刷新 / 增量间距）。

## 遇到的坑与解决

1. **测试时钟撞重载窗**（见上 §后端 4）——`Step` 的分支优先级：ticker 批次 → 整点重载（窗内优先）→ 增量访问。夹具时间必须选在窗外的整点后余量里。
2. **代理进程在 mock 重启窗口内死亡**（上游拒连时未兜住导致退出？——复现：mock 重启后 `/api/llm/coverage` 空响应、`lsof :8788` 无进程）。恢复动作：按端口重启代理（`GCT_GATEWAY_BASE=https://127.0.0.1:9443/v1/`，writes 默认 false）。**给接力者**：重启 mock 后顺手看一眼 8788 是否还在。
3. 「收尾步同时开启下一轮的首个访问」——`visitedThisCycle` 收尾后回到 1 而非 0（源注释明示行为），断言按真实语义写。

## 未完成 / 下一步建议

- **W7-06**（通知通道配置：Telegram + webhook；`communications/webhook` M5-16 已就位——配置读取面与 UI 待定）。
- **适配器缺口三件套**（解锁 W7-01/02/03 写侧）：halt 写侧 `iRunningStrategyPauser`/`iBotOrderCanceller`/`iPositionFlattener` 生产实现、`LLMReconcile` + 账户快照、accounting 读面。
- `M2-13` 末项（上游账户/下架事件生产者）、SHADOW `RiskFacts`；**M6-07/08 可离线演练**（vault/sqlite 本地文件，与 M6-06 同法）。
- 凭据门：M6-03/04/05、M1-15（decisions/01）。

## 环境状态快照

- mock 9443 / 代理 8788（allowWrites:false）/ vite 5173 在位；真实 bot 9053 未启动。
- 测试产物：`/tmp/mock-gw.log`、`/tmp/gct-proxy.log`、`/tmp/llm-w5-draft.json`（可删）。
