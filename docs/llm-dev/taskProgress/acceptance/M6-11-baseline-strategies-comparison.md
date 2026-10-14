# M6-11 验收记录：两个固定规则基线策略 + 对照报告（Q24=A）

**状态**：✅ 基线策略与对照运行（真实历史数据）；🟡「与 LLM 决策对照」列待 SHADOW/PAPER 决策数据（与 M6-03/04 同一数据依赖）。
**日期**：2026-10-11（session-26）· **依据**：`OPEN-QUESTIONS` Q24=A、`11-实施路线图.md` §1.1 M6-11 行。

## 1. 交付物

| 交付物 | 位置 | 说明 |
| --- | --- | --- |
| MACD 交叉基线 | `backtester/eventhandlers/strategies/macdcross/`（含 README + 测试） | 金叉（MACD 上穿信号线）开多、死叉平；固定仓位、无止损；默认 12/26/9，可经 `custom-settings` 覆盖；`macd-fast ≥ macd-slow` 直接拒绝 |
| SMA200 趋势基线 | `backtester/eventhandlers/strategies/sma200trend/`（含 README + 测试） | 收盘上穿 SMA 开多、下穿平；固定仓位、无止损；默认 200，可配置 |
| 注册 | `backtester/eventhandlers/strategies/strategies.go` | 两策略加入 `supportedStrategies`，`LoadStrategyByName` 可见 |
| 可跑配置 ×2 | `config/strategyexamples/macd-cross-baseline.strat`、`sma200-trend-baseline.strat` | CSV 数据源（仓库 `testdata/binance_BTCUSDT_24h_2019_01_01_2020_01_01.csv`，365 根日线）；示例索引 README 已登记 |
| 单测 | 各包 `*_test.go` | 名称/并发支持/默认值/自定义设置校验（含 fast≥slow 拒绝）/坏数据拒绝/暖机静默/金叉买入-死叉卖出与 SMA 上下穿的**逐 bar 定向断言** |

## 2. 对照运行（可复跑）

```bash
cd backtester
go run . -singlerunstrategypath=config/strategyexamples/macd-cross-baseline.strat -printlogo=false -colouroutput=false
go run . -singlerunstrategypath=config/strategyexamples/sma200-trend-baseline.strat -printlogo=false -colouroutput=false
```

**同一段数据**：Binance BTC/USDT spot 日线 2019-01-01 ~ 2019-12-31（365 根；首收 3,797.14 → 末收 7,195.23）。初始资金 $100,000、现货、无杠杆、maker/taker 0.02%/0.07%、无滑点。

| 对照 | 区间收益 | 跑赢买入持有？ | 手续费 | 期末总值 | 备注 |
| --- | --- | --- | --- | --- | --- |
| **MACD 交叉基线**（12,26,9） | **+3.73%** | ✗（市场 +89.49%） | $238.68 | $103,732.71 | 期末仍持仓 2.000 BTC（死叉未在年内出现） |
| **SMA200 趋势基线** | **−0.94%** | ✗ | $25.18 | $99,060.71 | 一年内仅少数几次穿越 |
| **Buy & hold（市场自身）** | **+89.49%** | — | — | ≈$189,490 | 回测器内置「Did it beat the market」对照 |
| **LLM 决策** | 🟡 待补 | — | — | — | 需 SHADOW/PAPER 决策留痕（生产装配后按 M6-03/04 数据面补记） |

**解读（重要）**：这组数字本身不是「LLM 无用」的证据——2019 是单边大牛市，**任何频繁进出的固定规则基线都会跑输买入持有**；它的价值在于给后续 LLM 对照提供**同一口径的基准刻度**：LLM 若连这两条朴素规则（尤其 SMA200 的 +几十 bp 级）都显著不如，才说明系统有问题。逐笔报告（HTML）落在 `backtester/results/`（gitignored）：`m6-11baselinemacdcross-*.html`、`m6-11baselinesma200trend-*.html`。

## 3. 验证证据

- `go test ./backtester/eventhandlers/strategies/... ./backtester/config/... -count 1` 全绿（含两新包）。
- `golangci-lint run`（全树）= **0 issues**；`go build ./...`、`go vet ./backtester/...` 无输出；`gofmt -l` 无输出。
- 两策略端到端运行退出码 0，日志含完整统计块（上文数字逐项摘自运行日志 `/tmp/bt_macd.log`、`/tmp/bt_sma.log`）。

## 4. 诚实边界

- 回测以**固定仓位（min-size 0.005 / max-size 2）**成交，未用杠杆、未计永续资金费（现货口径）——与 Q24=A 的「固定仓位无止损」一致；与 LLM 系统的真实执行环境（永续）在资金费与合约规格上有差异，对照时需在报告注明口径。
- SMA200 在 365 根日线上暖机 200 根，实际比价窗口为后 165 根；跨更长时间窗对照（如 2020-2024）时同一命令换 CSV 即可，结论可能变化，报告须带数据窗口。
- 「LLM 对照列」在决策留痕可用前不填数——不放估计值、不放占位假数。
