# 2026-10-10 session-02 — 交易台 W3 全部完成（图表 + 指标 + 合约列表 + 滑点 + 派生事实）

## 会话目标

在 session-01 的脚手架与数据层之上，完成交易台（W3）的全部 8 个子任务，使平台成为**可用的可视化交易台**。

## 完成的子任务

`W3-01` `W3-02` `W3-03` `W3-04` `W3-05` `W3-06` `W3-07` `W3-08`（W3 组 8/8，权重 12% 全部计入）

## 具体做了什么

| 文件 | 内容 |
| --- | --- |
| `src/components/MarketList.tsx` | 合约列表：`getexchangepairs`(POST) → 可用 789 / 已启用 5；搜索框；分段切换（已启用/全部/前 50）；点击行切换全局 `selectedMarketAtom`；当前合约高亮（`.gct-row-active`） |
| `src/components/PriceChart.tsx` | K 线图：Lightweight Charts v5 `addSeries` API；蜡烛 + 成交量（独立价格轴 `vol`，`scaleMargins.top=0.82`）+ EMA20/SMA200/布林上下轨叠加；MACD 与 RSI 独立副图；5 个指标开关 |
| `src/components/OrderbookPanel.tsx` | 盘口：1000 档聚合，5/15/30 档切换，买卖分表 + 深度条 + 累计量 + 价差 bps |
| `src/components/AccountPanel.tsx` | 账户与仓位：可用/冻结/总额/总抵押/可用抵押/已用抵押/杠杆/**单向持仓确认**（绿标）/当前仓位/开仓均价/标记价/盈亏平衡/预估强平/近期盈亏/名义杠杆/保证金率 |
| `src/components/IndicatorFacts.tsx` | 指标派生事实：MACD 交叉状态与根数、柱趋势、DIF/DEA、柱值、收盘 vs EMA20/SMA200 与斜率、ATR14 |
| `src/components/SlippageSimulator.tsx` | 滑点模拟：纯前端按盘口逐档累加，输出可成交数量/均价/起始→结束价/滑点 bps/吃档数，并与风控上限 20bps 对比给出通过/拒绝 |
| `src/pages/TerminalPage.tsx` | 重写为组件化布局：价格头（含资金费率）+ 三栏（合约列表 / 图表+账户 / 盘口+派生事实+滑点）+ 周期与 K 线数切换 |
| `src/styles/global.css` | 新增 `.gct-row-active` 选中行样式 |

派生事实的计算规则**刻意与 `docs/llm-dev/04-数据层-K线与指标.md` §3.3 完全一致**（CrossState / HistogramTrend / PriceVsMA / Slope 及容差），以便前后端对同一份数据给出同一结论，未来 M2-04 落地时可直接对照。

## 验证证据（真实跑出）

### 三连校验

```text
pnpm typecheck  →  0 错误
pnpm lint       →  oxlint: Found 0 warnings and 0 errors（24 files, 96 rules）
pnpm build      →  ✓ 3148 modules transformed；index-*.js 1,360.05 kB (gzip 434.33 kB)
```

### 浏览器实测（截图 `/tmp/webui-4.png`）

**K 线图已正常渲染**：蜡烛覆盖 80000–89000 区间、时间轴 10/3–10/12、成交量柱、EMA20 黄线与 SMA200 蓝线叠加、最新价线 82760.00；
**MACD 副图**正常（DIF/DEA 双线 + 柱状图 + 零轴）。

其余面板实测值：

| 面板 | 实测 |
| --- | --- |
| 价格头 | 82,760.00 · 24h 高/低 83,269.40 / 82,241.50 · 价差 0.10 (0.01 bps) · 24h 量 53,363.78 · 成交额 4,412,648,131 |
| 资金费率 | -0.0012%，年化 -1.36%，下次结算 2026-10-11 00:00 CST |
| 合约列表 | 已启用 5 / 全部 789；BTC 行高亮；点击可切换 |
| 盘口 | 买 1000 档 / 卖 1000 档，前 15 档含深度条与累计量 |
| 账户 | 可用 68.69 · 冻结 1.32 · 总额 70.01 · 总抵押 70.01 · 杠杆 20× · **单向 one-way（绿标）** · 近期盈亏 -0.2280 |
| 指标派生事实 | **MACD 交叉：金叉 GOLDEN 9 根前** · 柱趋势：柱收缩 · DIF/DEA 72.63/69.92 · 柱值 2.71 · 收盘 82703.62 在 EMA20 上方（斜率 UP）· 在 SMA200（84271.73）下方（斜率 DOWN）· ATR(14) 198.40 |
| 滑点模拟 | 1000 USDT → 0.0120825 BTC，均价 82764.80，滑点 0.01 bps，吃 1/1000 档 → **通过滑点检查** |

## 遇到的坑与解决（**重要，接力必读**）

### 坑 1：图表容器 0 个 canvas —— 什么都不画

**现象**：`createChart` 无报错，但图表区空白；DOM 查询显示容器有尺寸（709×300）却 `canvases: 0`、`innerHTML: ""`。

**根因**：原实现里 `candles.length === 0` 时组件**提前 return** 了 Empty 卡片，图表 div 根本没挂载；而 `createChart` 放在 `[]` 依赖的 effect 里只在首屏跑一次，此刻 `ref.current` 还是 `null` → 直接 `return`，之后数据到位也不会重建。

**解决**：改为**「单个 effect 完整重建」**模式——把 `createChart` + `addSeries` + `setData` 全部放进同一个 effect，依赖 `[candles, ind.*, ta*]`，清理函数 `chart.remove()`；并且**图表 div 无条件渲染**（空数据用绝对定位的 Empty 覆盖层提示）。

**顺带修掉的隐患**：原设计「创建一次 + 后续 addSeries」会在 TA 查询逐个 resolve 时**重复 addSeries**，造成序列叠加与内存泄漏。重写后不复存在。

### 坑 2：BBANDS 端点 400

```text
{"code":2, "message":"get bollinger bands invalid deviation multiplier upper limit"}
```

**根因**：`GetTechnicalAnalysis` 的 BBANDS 分支**必须同时传 `standardDeviationUp` 与 `standardDeviationDown`**；只传一个会报 `invalid deviation multiplier lower limit`，一个都不传报 upper limit。

**解决**：`gctApi.ts` 的 TA 端点新增 `stdUp`/`stdDown` 参数并成对下发；`PriceChart` 传 `2 / 2`。已在代码注释与本文件记录。

### 坑 3：MACD 端点必须显式传 fast/slow

```text
{"code":2, "message":"get macd invalid period fast"}
```

单独传 `period` 不够，必须同时给 `fastPeriod`/`slowPeriod`。已在前端固定为 12/26/9（Q16 定案）。

### 其他 API 事实（已复核）

| 端点 | 结论 |
| --- | --- |
| `ATR` / `RSI` / `EMA` / `SMA` | 只需 `period`，正常返回 `["ATR"]` 等键 |
| `BBANDS` | 返回 `UPPER` / `LOWER`（可能还有 `MIDDLE`），键名大写 |
| `gettechnicalanalysis` 的 `start`/`end` | **RFC3339**（`2026-09-30T00:00:00Z`）—— 与 `gethistoriccandles` 的 `2006-01-02 15:04:05 MST` 不同 |

### TS 类型坑（已修）

- `Table` 的 `onRow`/`rowClassName` 会从 `columns` 反推窄类型 → 需显式写 `<Table<MarketRow>>`
- `vite.config.ts` 用 `node:url`/`node:path` 需要 `@types/node`（已加入 devDependencies）
- antd 6 的 `Statistic.valueStyle` 已弃用 → 改用 `styles={{ content: {...} }}`

## 未完成 / 下一步建议

**W3 已全部完成。下一个模型从 `W7-04`（全量覆盖轮询状态，纯前端可做骨架）或 `W5-01`（对话工作区，消费真流式）接手**，也可按 `TASKS.md` 顺序推进 `W4`/`W6` 的骨架。

仍存在的已知缺口（非阻塞，记录备查）：

1. **持仓量 / 标记价 / 指数价**：币安合约 REST ticker 不填这三个字段（恒 0）。持仓量需后端 `M2` 补（或修正 `getopeninterest` 的参数结构 —— 实测该端点报 `unsupported wrapper function requires pair`）；标记价可从 `getfuturespositionssummary` 的 `positionStats.markPrice` 取，但无仓位时为 0。对应 `W3-05` 的余项。
2. **前端 bundle 1.36 MB**：未做按路由懒加载，非阻塞。
3. 图表的 `ResizeObserver` 依赖 `autoSize`；切换路由回来会重建，行为正常但会多一次 API 拉取（RTK Query 有缓存，实测无明显延迟）。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 运行中，PID 56291，9052/9053 |
| webUI 代理 | 运行中，8788（`/tmp/webui-proxy.pid`） |
| Vite dev | 运行中，5173（`/tmp/webui-vite.pid`） |
| 重启 | `cd webUI && pnpm dev:all` |
