# session-11：MarketSnapshot 契约与派生事实（M2-03）

**日期**：2026-10-10
**范围**：`M2-03` MarketSnapshotBuilder（后端 `llm/snapshot/`）
**前置**：session-10（M2-12 轮询规划层与权重闸）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/snapshot/snapshot.go` | `MarketSnapshot` + 9 类事实（`CandleWindow`/`CandleFact`/`CandleSummary`/`IndicatorSet`/`MACDFact`/`MovingAverageFact`/`SeriesFact`/`BollingerFact`/`TickerFacts`/`OrderBookFacts`/`FundingFacts`/`AccountFacts`/`PositionFact`）+ 6 个质量类目 + `Validate` + 深度 `Clone` + `SetQuality` + `DegradedCategories` |
| `llm/snapshot/derive.go` | `DeriveMACDState` / `DeriveMAState` / `DeriveSpreadBPS` / `DeriveMidPrice` / `DeriveExecutablePrice` / `DeriveAnnualisedFundingPct` / `SummariseCandles` / `Interval` 枚举 |
| `llm/snapshot/snapshot_test.go` | 29 个测试函数 |

## 2. 设计要点（为什么这样做）

### 2.1 `llm/` 的纯离线部分到本会话为止做完了

这是个值得写下来的边界判断。`llm/` 现在有七个包：

| 包 | 依赖 | 状态 |
| --- | --- | --- |
| `candidate` | 无 | ✅ |
| `coverage` | 无 | ✅ |
| `httpclient` | 无 | ✅ |
| `jev` | `httpclient` | ✅ |
| `secrets` | 无 | ✅ |
| `openai` | `httpclient` + `candidate` | ✅ |
| `snapshot` | `candidate` + `types/decimal` | ✅ |

**没有一个包需要 DB、proto、provider key 或 exchange manager。** 接下来无论做什么，都要接一个外部依赖。这对接手的模型是重要信息：不能再用"挑一个能离线做的"来选任务了。

### 2.2 拒绝 `kline.Interval`——一个真实的依赖污染

文档 `04` §3.1 把 `Interval` 写成 `kline.Interval`。照做会怎样：`exchanges/kline` import `exchanges/order`（因为 `CreateKline` 要收 `[]order.TradeHistory`），而 `exchanges/order` 是订单管理器的核心类型。

于是 `llm/snapshot` → `exchanges/kline` → `exchanges/order`，`llm/` 就再也抽不出去了，直接违背 Q22 决议里那句"`llm/` 包不依赖 `engine/`，可以整体搬走"。

**代码改用本地 `Interval` 字符串枚举**（`"1m"`/`"5m"`/`"15m"`/`"1h"`/`"4h"`/`"1d"`）。额外好处：序列化给模型和审计时是人能读的字符串，而不是一个纳秒数。`Duration()` 用手写映射而非解析字符串，这样未知区间**报错**而不是被当成某个纳秒数。

同理，`Pair currency.Pair` / `Asset asset.Item` 也改成朴素字符串（`"BTC_USDT"` / `"usdtmarginedfutures"`）——契约的序列化形式必须是稳定的、与仓库内部类型解耦的。

**建议 `M0-13` 架构边界测试把这条钉死。**

### 2.3 全十进制字符串

价格/数量/金额一律字符串，用 `ValidateDecimal` 校验：

```text
拒绝：""  "-1"  "+1"  "1e5"  "1."  ".1"  " 1"  "1,000"
接受："0"  "1"  "0.001"  "12345678901234567890.123456789012345678"
```

**为什么拒指数**：`1e5` 和 `100000` 是同一个数但两个字符串。如果允许两种写法，那么"模型看到的模型 JSON"和"审计记录里的字符串"就无法用哈希比对——而 `M1-08` 的 `InputManifest` 正是靠哈希比对做的。

签名值（涨跌幅、MACD 柱、资金费率）走 `ValidateSignedDecimal`，允许一个前导 `-`，仍然拒指数。这样不必为签名值单开一套更宽松的规则。

### 2.4 派生事实只算"读数"，不算"结论"

`derive.go` 里能算出的最大胆的东西是 `DirectionScore`（窗口内收盘 ≥ 开盘的 K 线占比）。**没有任何 `SHOULD_BUY` 类字段**，这是文档 §3.3 的硬性要求，代码用"只提供这类函数"来保证——没有函数可以产出一个建议。

理由写在 `SummariseCandles` 的注释里：在 Go 里派生一个建议，等于把决策从审计路径里挪到一个没人按决策标准审查的代码路径里。

### 2.5 `SummariseCandles` 给"形状"而不是"50 行原始数据"

模型不擅长数值计算。给它 50 根 K 线让它自己算涨跌幅，既浪费 token 又多一个算错的机会。摘要给 `HighestHigh`/`LowestLow`/`TotalVolume`/`ChangePct`/`DirectionScore` + 最近 50 根明细用于核对。

**`formatFloat` 保证不用指数写法**：一个 0.00000001 涨到 0.000000012 的场景会算出 `2e-07`，那会被 `ValidateDecimal` 拒掉，于是"快照带摘要就永远校验不过"。测试 `TestSummariseCandlesDoesNotUseExponentNotation` 专门守这个。

### 2.6 可成交均价：吃不下就报错

```go
func DeriveExecutablePrice(levels []OrderBookLevel, notional decimal.Decimal) (price, size decimal.Decimal, err error)
```

逐档累加，在能覆盖目标名义额的那一档**只吃剩余部分**（否则均价会被拖向订单根本没成交的价格），返回 `spent/filled`。

**吃不下时返回错误而不是部分成交价**：把部分成交当全额报告会**低估滑点**，而滑点正是风控 `RISK_SLIPPAGE_EXCEEDED` 要拦的东西。低估它等于放过一笔实际会亏更多的单。

测试断言了这个性质：`TestExecutablePriceIsWorseThanTheMidpoint` 用 1 → 10 → 100 三档名义额，断言**每一档都不优于中价**且**大单不比小单成交得更好**。这是滑点检查真正依赖的不变量。

### 2.7 `DegradedCategories`：区分"缺失"与"未知"

这是本会话做出的一处**设计判断**，值得单独说明。

第一版实现是"遍历 6 个类目，`QualityOf(category).IsDegraded()` 就报"。但 `QualityOf` 对**不在 map 里**的类目返回 `UNKNOWN`，而 `UNKNOWN` 是降级状态。后果：一个只带 K 线的快照会报出 `[indicators ticker orderbook funding account]` 五个降级类目——**快照永远不可用**。

正确的规则是三分：

| 情况 | 判定 | 理由 |
| --- | --- | --- |
| 有数据，无标签 | 阻断（UNKNOWN） | 没人评估过它。这正是标签体系存在的意义 |
| 有降级标签，**即使没有数据** | 阻断 | 一个"抓取失败后标 STALE"的组装器是在断言它需要的东西缺了 |
| 无数据，无标签 | **忽略** | 它根本不是这个快照的一部分。只算 K 线的快照不该因为没有 ticker 而被拒 |

第二条是容易漏的：`s.OrderBook = &OrderBookFacts{}`（空盘口）+ 标 `UNKNOWN`，这是"盘口拉取失败"的正常表达，必须阻断。

第三条是容易过度保守的：把"缺失"当"未知"，会让每一个**更窄用途的快照**（例如只做只读对话、或只做触发器扫描）都无法通过检查。真正的"必需事实清单"是另一个关注点，属 `M2-06` 的新鲜度门禁。

### 2.8 `Clone` 必须是深拷贝

`Clone` 的用途是"组装器交付一个调用方改不动别人输入的快照"。浅拷贝会留下共享的嵌套 slice——两个快照共用一条 `Values` 序列，改一个动两个。

第一版 `Clone` 只拷贝了顶层 slice（`append([]MovingAverageFact(nil), ...)`），但每个 `MovingAverageFact` 内部的 `Values []string` 仍然共享。测试 `TestCloneIsDeep` 直接把这个抓出来：改 clone 的每个嵌套 slice，断言原快照全部不变。

现在 `Clone` 逐个深拷贝：`Candles.Closed`、`MovingAverages[].Values`、`RSI/ATR.Values`、`Bollinger.{Upper,Middle,Lower}`、`MACD.{DIF,DEA,Histogram}`、`OrderBook.{Bids,Asks}`、`Funding.History`、`Account.Positions`、`Quality` map、`LiveCandle`。

### 2.9 质量标签只降不升

`SetQuality` 复用 `candidate` 已有的 `DataQuality.Downgrade`，升级返回 `candidate.ErrQualityUpgrade`。

这是整个 fail-closed 体系的核心不变量：**后续任何阶段都不能把前一个阶段已经否掉的事实的可信度改回去**。测试里直接模拟了"先标 STALE，再试标 EXCHANGE_REST"，断言被拒且标签保持不变。

### 2.10 `DeriveMACDState` 的最近交叉优先

一个窗口里可能有两次交叉。报**最近的那次**（从末尾往前扫，第一个符号翻转即停），因为旧的交叉描述的是一个已经不成立的状态。

`BarsSinceCross` 的计算：交叉在 `i-1` 与 `i` 之间，从最新一根往回数是 `len-1-i` 根。测试覆盖了"最新一根交叉"（0 根）与"三根前交叉"（2 根）。

### 2.11 均线斜率的平阈

`Slope` 用 `averages[last] - averages[last-4]`（回看 3 个点）与 `|reference| × 0.01%` 比较。

**没有阈值会怎样**：50000 美元的价格上漂 1 美分会被读成一次方向变化。测试里 `["50000.00","50000.00","50000.00","50000.01"]` 必须读成 `FLAT`。

序列短于 4 个点时返回 `FLAT` 而不是猜一个方向——位置事实仍然给出，方向事实标为"看不出来"。

### 2.12 比率与它除的数用同一套算术

`DeriveSpreadBPS` 第一版用 float 算，而 `DeriveMidPrice` 用 decimal。在这个不一致下，(100, 100.1) 的价差在**第 12 位**就开始漂（float 给 `9.995002498750056`，正确值 `9.995002498750624`）。

两个派生数字落在两套算术上，是那种"平时看不出、对账时才发现"的问题。现在两者都用 decimal。

**测试用容差而不是字面量**：`types/decimal` 有两套实现（shopspring 默认 / udecimal 构建标签），两者的除法精度不同，确切尾数是一个实现细节而不是答案。`assertDecimalNear` 就是这个用途，注释里写明了原因。

## 3. 本会话修的两个真实缺陷与三个测试错误

| # | 现象 | 判定 | 处理 |
| --- | --- | --- | --- |
| 1 | `TestCloneIsDeep`：改 clone 的 `MovingAverages[0].Values[0]` 后原快照也变了 | **代码缺陷**：`Clone` 只拷了顶层 slice，嵌套 slice 共享 | 逐个深拷贝所有嵌套 slice |
| 2 | `TestDegradedCategories`：只带 K 线的快照报出 5 个降级类目 | **设计缺陷**：把"缺失"当"未知" | 改三分规则（§2.7），并补两条测试锁住新语义 |
| 3 | `TestDeriveSpreadAndMid`：价差期望 `...0624` 实得 `...0056` | **代码缺陷**（float/decimal 混用）+ 测试期望同样要改 | 代码改 decimal；测试改容差断言 |
| 4 | `TestDeriveMACDState` "down through zero" 期望 `CONTRACTING` | **测试错了**：柱从 0 到 −0.5 是**幅度增大** = `EXPANDING` | 改期望值 |
| 5 | `TestDeriveMAState`：`closes[0]="abc"` 没被拒 | **测试过了头**：函数只读末尾的值，`closes[0]` 根本用不到 | 把垃圾放到真正被读的位置，并在 GoDoc 写明"只校验读取到的值" |

第 5 条值得留意：`DeriveMAState` 不校验整条序列，是因为 `MovingAverageFact.validate()` / `CandleFact.Validate()` 在契约层已经把整条序列校验过了。重复劳动不是额外安全。这个边界写进了 GoDoc，否则下一个人会以为是漏了。

## 4. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **组装器**（从 exchange manager 的 `kline.Item`/ticker/orderbook/账户组装成本契约，含指标计算与质量标注） | **M2-15**（新拆出） | 本次**没有**任何 I/O。快照的**契约与派生逻辑**做完了，但把交易所数据填进去需要 exchange manager |
| 指标计算本身（MACD/均线/RSI/ATR/布林） | **M2-04** | 本次只做了**从指标序列派生事实**（叉态/斜率/趋势）。指标数值由 `exchanges/kline/technical_analysis.go` 提供，接线属 M2-04 |
| 新鲜度门禁（7 类事实阈值） | **M2-06** | `FreshnessMS` 字段已在契约里，但**谁来计算和判定**属 M2-06。本次没有把它接进 `Validate`（`Validate` 刻意不做时间相关判断，理由见下） |
| 数据质量标签的**应用**（8 个标签各自的触发条件） | **M2-05** | 契约与"只降不升"规则做完了；"什么条件下降级"属 M2-05 |
| `DirectionScore` 的语义定案 | **M2-04 或 M2-13** | 本次给它的是"收盘 ≥ 开盘的 K 线占比"。这是个**读数**，但如果 M2-04 想要别的口径（例如按成交量加权），需要在这里改 |
| 资金费率批量权重 = 10 | **M1-15 / 实机冒烟** | 沿用 session-10 的估计值，仍未对真实端点验证 |
| 真实交易所数据往返 | **M2-15** | 全部测试用构造的 fixture，**未接任何真实数据** |

**`Validate` 刻意不检查新鲜度**：新鲜度依赖当前时间，而一个"已经过期"的快照仍然是关于"当时是什么"的合法记录。把它塞进 `Validate` 会让重放和审计读不了历史快照。新鲜度判定属 `M2-06`，应在构造时做。

## 5. 校验证据

```text
$ gofmt -l -w llm/            # 无残留
$ go build ./llm/...          # 通过
$ go vet  ./llm/...           # 无输出
$ go test ./llm/... -count 1
?   github.com/thrasher-corp/gocryptotrader/llm            [no test files]
ok  github.com/thrasher-corp/gocryptotrader/llm/candidate   0.704s
ok  github.com/thrasher-corp/gocryptotrader/llm/coverage    1.731s
ok  github.com/thrasher-corp/gocryptotrader/llm/httpclient  1.741s
ok  github.com/thrasher-corp/gocryptotrader/llm/jev         1.938s
ok  github.com/thrasher-corp/gocryptotrader/llm/openai      1.221s
ok  github.com/thrasher-corp/gocryptotrader/llm/secrets     2.799s
ok  github.com/thrasher-corp/gocryptotrader/llm/snapshot    1.723s

$ go test ./llm/snapshot/ -count 1 -race
ok  github.com/thrasher-corp/gocryptotrader/llm/snapshot    1.946s

测试函数计数：candidate 31 · coverage 22 · httpclient 39 · jev 89 · openai 41 · secrets 20 · snapshot 29  → 271
```

**全部离线**：无网络、无数据库、无 provider key、无 exchange manager。所有 fixture 用 `testTime()` 的固定时刻，不依赖真实时钟。

## 6. 给接手模型的具体建议

**现在 `llm/` 里已经没有任何能纯离线完成的包了。** 下一步必然要接一个外部依赖，请按"哪个依赖最容易在本地验证"来选：

1. **`M0-04` 8 个 PostgreSQL 迁移**（**最高杠杆点**）：DDL 可以用 `dbmigrate` 在本地验证，不需要 provider 和交易所；做完解锁 `M1-09`~`M1-13`。表结构在 `docs/llm-dev/09-数据存储与配置.md` §2-§5，`provider_probe_results` 的列已在 `05` §3。
2. **`M1-13` 三个生产提示词常量**：`llm/openai/prompt.go`，纯字符串，`docs/llm-dev/05` §6 已给出全文。零风险。
3. **`M0-13` 架构边界测试**：本会话刚发现 `kline.Interval` 会经 `exchanges/order` 污染 `llm/`。这个测试能把"`llm/` 可整体抽出"变成机器化断言，而不是靠人记得。
4. **`M2-15` + `M2-14`**：需要 exchange manager 接线，本地有跑起来的 bot（`PROGRESS.md` §7 有启动命令），可以真实往返。
5. 前端 `W5-01`、`W7-04`、`W8-04/06`：不依赖后端。

**注意**：`llm/openai/prompt.go` 是 `M1-13` 的落点，本次**没有**创建。`probe.go` 里的 `probeSchemaSystemPrompt` / `probeSchemaUserPrompt` 是探针专用，**不要**当作生产提示词使用。
