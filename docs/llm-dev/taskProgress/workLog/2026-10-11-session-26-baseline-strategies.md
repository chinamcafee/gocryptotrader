# Session-26 工作日志：M6-11 两个固定规则基线策略 + 对照运行（同日续 session-25）

**日期**：2026-10-11 · **任务**：`M6-11`（Q24=A）· **结果**：✅ 基线策略落地并对照运行；LLM 对照列 🟡 待决策数据。

## 1. 交付物清单

- `backtester/eventhandlers/strategies/macdcross/`：`macdcross.go`（金叉开多/死叉平，固定仓位无止损；默认 12/26/9；`macd-fast ≥ macd-slow` 拒绝；暖机 = slow+signal；坏数据流 > 暖机长度 → `ErrTooMuchBadData`）+ `macdcross_test.go`（9 个测试，含逐 bar 金叉唯一买/死叉唯一卖断言）+ README（照 rsi 模板）。
- `backtester/eventhandlers/strategies/sma200trend/`：`sma200trend.go`（收盘上穿 SMA 开多/下穿平；默认 200 可配置）+ 测试（8 个）+ README。
- `backtester/eventhandlers/strategies/strategies.go`：注册两策略（`New` 列表 + import）。
- `backtester/config/strategyexamples/macd-cross-baseline.strat`、`sma200-trend-baseline.strat`（CSV 数据源，正斜杠路径 `../testdata/binance_BTCUSDT_24h_2019_01_01_2020_01_01.csv`）；`strategyexamples/README.md` 索引表补两行。
- 验收记录：`taskProgress/acceptance/M6-11-baseline-strategies-comparison.md`（含数字与复跑命令）。

## 2. 对照运行（真实历史数据，可复跑）

数据：BTC/USDT 现货日线 2019 全年（365 根，首收 3,797.14 → 末收 7,195.23）；$100,000 初始、无杠杆、费 0.02%/0.07%。

| 对照 | 收益 | 跑赢买入持有 (+89.49%)？ | 期末总值 |
| --- | --- | --- | --- |
| MACD 交叉基线 | +3.73% | ✗ | $103,732.71（费 $238.68） |
| SMA200 趋势基线 | −0.94% | ✗ | $99,060.71（费 $25.18） |
| Buy & hold | +89.49% | — | ≈$189,490（回测器内置对照） |
| LLM 决策 | 🟡 待 SHADOW/PAPER 留痕 | — | — |

命令：`cd backtester && go run . -singlerunstrategypath=config/strategyexamples/<name>.strat -printlogo=false -colouroutput=false`（HTML 报告落 `backtester/results/`，gitignored）。

**解读纪律**：2019 单边牛市，"频繁进出的固定规则都会输给买入持有"是**预期现象**；该表的作用是为将来 LLM 对照提供同口径刻度（若 LLM 连朴素规则都显著不如才指问题），**不可**把此表读成「LLM 有用/无用」的结论。

## 3. 本次踩坑（写给接力者）

1. **`offset` 是 int64**：`latest.GetOffset()` 与 `IntPart()` 比较不能混 `int` 字面量（编译错误在 macdcross 第一次 build 暴露）。
2. **`SetCustomSettings` 的跨字段校验要容忍未赋值**：初版 `fast ≥ slow` 无条件拒绝，导致 `SetCustomSettings(nil)` 在零值上就报错；改为「两者都非零才校验」（rsi 原版无跨字段校验可参照）。
3. **CSV 路径大小写/分隔符**：示例配置用正斜杠相对路径（Windows 反斜杠在 macOS 是字面字符）；数据在**仓库根** `testdata/`（不是 backtester/testdata）。
4. **`cmd/documentation` 的 README 不是自动生成**：新策略包要手写 README（模板照抄 rsi 并改包名），测试不强制但仓库惯例如此；`strategyexamples/README.md` 的示例表要手动补行。
5. **results/ 报告是 gitignored**：验收证据要把关键数字摘进记录文件，别指望 HTML 进 git。
6. 单测里构造逐 bar 数据流：`DataFromKline.Load()` 从 candles 建流（offset 1..N），`Next()` 推进，`RangeHolder.SetHasDataFromCandles` 保证 `HasDataAtTime` 为真——照 rsi_test 的骨架但改成循环推进以覆盖整段。

## 3b. M6-06 崩溃恢复演练（kill -9，同日续段）

**交付**：`engine/llm_crash_drill_test.go`——不是模拟：父测试起**真实第二进程**（re-exec 同一测试二进制，`-test.run=TestM6CrashDrillChild`，env 传共享 SQLite 目录/文件名/意图 ID），子进程用**真实仓储** `ClaimOrderIntent` 认领成 SUBMITTING 后打印 `CLAIMED` 并永久阻塞；父进程确认落盘后 **`Process.Kill()`（SIGKILL）**；随后重连数据库跑**真实 `CrashRecovery.Recover`**（真实 `LLMIntentStatusLister`/`LLMIntentLifecycle`/`LLMDecisionStore` 接缝）。

**断言链（全过）**：① 被 kill 后行仍停在 SUBMITTING（"认领了没结果"）② 重启扫描 → `UnknownIntents=[drill-claimed]`、`ReconciliationRequired=true`、过期 PREPARED 同批 `ExpiredIntents=[drill-stale-prepared]` ③ 库里恰好一条 UNKNOWN ④ **`ClaimOrderIntent` 对 UNKNOWN 返回 false**（不盲重试，机器化保证）⑤ `MetricUnknownOrders` gauge = 1 ⑥ **恰好一条 `CRASH_RECOVERY` 审计**且详情点名两意图。测试 0.25s。

**套件复跑注意**：本战役把引擎整套复跑两次，期间 `TestGetOrdersFiltered` 有一次打 Bitstamp 实网 EOF（与既有两个 flaky 同类，单跑复验 6.9s 即过；本地验收 skip 列表可一并带上）；带演练的完整套件 **113.9s 全绿**。

**要点（接力用）**：`testhelpers.TempDir` + `MigrationDir` 必须在 `ConnectToDatabase` **之前**设好（父进程走 `setupProviderAdminDB`，子进程从 env 恢复）；`goose` 对已迁移库幂等，子进程重复跑迁移无副作用；SQLite 单文件跨进程共享是这套演练能离线复现的关键。

## 4. 验证

- `go test ./backtester/eventhandlers/strategies/... ./backtester/config/... -count 1` 全绿。
- `golangci-lint run`（全树）0 issues；`go build ./...` / `go vet ./backtester/...` / `gofmt -l` 无输出。
- 两条 `go run . -singlerunstrategypath=...` 退出码 0；统计块完整。

## 7. M6-09 文档定稿 + M0-13 残余半句关闭（同日续段）

**交付**：

1. `docs/llm-dev/实现后状态对照.md`（Doc-to-code map）：13 篇设计稿逐篇给代码落点 + **两处实测偏差**（588 合约口径替代文档 525、`KlineWeight` 半开区间修正）+ **诚实残余清单**（生产装配 / M0-09 配置 v18 / M2-13 两残余 / M6 数据依赖行）。
2. 13 篇设计稿 + OPEN-QUESTIONS 头部加「实现状态」引导块（指向对照表与 `taskProgress/`）——每篇文档自我声明，不再需要读者猜哪段已实现。
3. `engine/llm_manager.md` **重写补齐**（原文停在上线前的「lifecycle-only」骨架）：当前状态（M5 落地 + M6 进行中、生产装配未接线的 fail-closed 语义）、代码分布表（llm/ 十一包 / engine/llm_* / 仓储 / 迁移 / RPC / CLI）、准入命令十一条门、崩溃恢复路径（含 kill -9 演练指向）与残余清单。
4. **M0-13 残余半句关闭**：`engine/llm_risk_boundary_test.go` 用 `go/parser` 解析风控三文件（`llm_risk.go`/`llm_sizing.go`/`llm_pregate.go`）的 import，断言不含 openai/jev/httpclient/tools/chat/sql/net-http——「风控拿不到模型」从文档承诺变成机器事实（与 `llm/boundary_test.go` 的反向边界合围完整）。

**验证**：`go test ./engine/ -run 'TestRiskEngineImportsNoModelClients'` 过（2.5s）· `golangci-lint run ./engine/...` 0 · markdownlint（llm-dev 全树 49 文件）0 issues。

## 5. 未完成与下一步

- **LLM 对照列**：待生产装配后以 SHADOW/PAPER 决策留痕按同口径补记（与 M6-03/04 同数据门）。
- M6 其余可做项：`M6-06`（kill -9 演练，复用 `CrashRecovery.Recover`）、`M6-09`（文档定稿）。
- 跨长时间窗对照（2020-2024 等）换 CSV 即跑，报告须标注数据窗口与现货口径。
