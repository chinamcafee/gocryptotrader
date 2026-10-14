# session-20：snapshot 组装器（M2-15）

**日期**：2026-10-10
**范围**：`M2-15` snapshot 组装器（`kline.Item` / ticker / orderbook / 账户 → M2-03 契约）
**前置**：session-19（M2-04 指标组装）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `engine/llm_snapshot.go` | `iMarketDataSource`（5 方法窄接口）· `BuildMarketSnapshot` · `SnapshotRequest` / `SnapshotResult` · `snapshotInterval` · 各映射函数 |
| `engine/llm_snapshot_test.go` | 12 个测试函数 + 1 条编译期接口断言 |

**这是整条链里第一个能把"模型层契约"和"交易所"接上的东西。** 在此之前 `llm/` 里全是零件。

## 2. 窄接口：为什么不需要 bot 就能测

```go
type iMarketDataSource interface {
	GetHistoricCandles(ctx, pair, a, interval, start, end) (*kline.Item, error)
	GetCachedTicker(p, a) (*ticker.Price, error)
	GetCachedOrderbook(p, a) (*orderbook.Book, error)
	GetCachedSubAccounts(ctx, a) (accounts.SubAccounts, error)
	GetFuturesPositions(ctx, *futures.PositionsRequest) ([]futures.PositionDetails, error)
}
```

只声明组装器真正用到的 5 个方法（而不是 `iExchangeManager`）。

**收益**：测试传一个假 source 就能跑全部 12 个用例——**不需要跑起来的 bot、不需要网络、不需要密钥**。这是本会话能在没有交易所的情况下交付可用组装器的唯一原因。

**`exchange.IBotExchange` 直接满足它**，用编译期断言钉住：

```go
var _ iMarketDataSource = (exchange.IBotExchange)(nil)
```

于是生产接线就是"把 exchange 传进去"，不需要适配器；而交易所哪天丢了这 5 个方法里的任何一个，**包直接编不过**——比运行时报错早得多。

> 这一条踩过一次：我第一版写的是"用我自己的 stub 证明接口满足"——那证明的是 stub 满足 stub，什么也没证明。改成对 `exchange.IBotExchange` 本身断言才有意义。

## 3. 组装过程的六个判断

### 3.1 `now` 由调用方注入，从不读墙上时钟

```go
if req.Now.IsZero() { return nil, ErrSnapshotClockMissing }
```

年龄必须可复现：如果组装器自己读时钟，那么"这个快照当时是不是新鲜"在事后无法重算，测试也无法稳定。**能注入的时间就不要自己取。**

### 3.2 K 线窗口按指标反推，而不是拍一个数

```go
bars := int(params.LongestPeriod()) + CandleFetchMargin   // 200 + 30
lookback := time.Duration(bars+1) * req.Interval.Duration()
start := req.Now.Add(-lookback)
```

多要 30 根是给"部分交付"留余量；多要 1 根是为了让窗口里有那根**还没收盘**的。测试直接断言 `start == now - 231h`，所以窗口是被检查的而不是被相信的。

### 3.3 未收盘的那根单独归入 `LiveCandle`

```go
if candle.Time.Add(seconds).After(now) { live = &fact; continue }
closed = append(closed, fact)
```

**它绝不进指标**（数值还在变），但必须被携带——它回答了"价格现在在哪"，而那正是模型会问的第一个问题。

`SourceTime` 取**最新已收盘那根**的开盘时刻，不是 `BuiltAt`，也不是"最新那根"。测试对此有显式断言。

### 3.4 盘口优先用交易所的十进制字符串

```go
price, amount := level.StrPrice, level.StrAmount
if price == "" || amount == "" { /* 回退到浮点渲染 */ }
```

`orderbook.Level` 同时带 `Price float64` 与 `StrPrice string`。**字符串是交易所真正说的那个值**；浮点副本已经丢掉了二进制表示装不下的部分，而**过了浮点的价格是另一个价格**。

测试断言 `"203.90000000"` 原样保留。浮点回退保留是为了不丢档位（不是每个数据源都填字符串形式）。

### 3.5 缺失的缓存读数不致命

ticker / orderbook 走 `GetCached*`：缓存由引擎自己的轮询填充，**冷缓存是启动时的正常状态**。取不到就不带这个事实，**不发明**：

```go
assert.Nil(t, result.Snapshot.Ticker, "an absent ticker must stay absent, not be invented")
assert.NotContains(t, result.Freshness.BlockingStale, snapshot.CategoryTicker,
    "an absent fact is not a stale fact")
```

"没有" 与 "旧了" 由新鲜度门禁区分（session-18 的三分规则），组装器不插手。

### 3.6 位置事实由调用方提供，不在组装器里编

引擎的 `futures.PositionSummary` / `PositionDetails` **不带持仓方向**（方向只在 `order.Detail.Side` 或币安原始字段里）。而契约的 `PositionFact.Side` 是必需的。

**在这里编一个方向出来，就是给风控塞伪造数据**——风控随后会按它算敞口和反向开仓判定。所以：

```go
// Account, when set, is recorded as the account facts... Assembling it is
// M5-08's job, alongside reconciliation.
Account *snapshot.AccountFacts
```

`BalanceFrom: true` 时从缓存余额汇总 **USDT** 权益与可用额（信封以 USD 计价，混入其它币种需要汇率，而快照没有资格发明汇率）；位置仍由调用方给。**这是一个明确记录的缺口，不是遗漏。**

## 4. 修掉的两个真实缺陷（都是测试逼出来的）

| # | 现象 | 判定 | 修法 |
| --- | --- | --- | --- |
| 1 | `s.Validate()` 报 `snapshot is incomplete: id` | **代码缺陷**：组装器从不设置 `ID`，而它是必需的（候选必须把它当第一条证据引用） | `SnapshotRequest.ID` 由调用方提供，空则生成 UUID。**这同时解决了一个冲突**：ID 是"每次组装"的属性而非"内容"的属性，所以两次组装不该有同一 ID——确定性测试改为传入固定 ID 后断言字节一致，另加一条断言"不传 ID 时两次组装 ID 不同" |
| 2 | `Pair` 记成 `BTCUSDT`（没有分隔符） | **代码缺陷**：`pair.String()` 用 pair 自带的分隔符，而那来自配置——**操作员改一次配置，快照里的市场标识就换一种写法**，历史记录没法比 | 固定用 `_` 格式化（`snapshotPairDelimiter`），因为设计文档、`llm_*` 表、市场列表用的都是 `BTC_USDT` |

第 2 条值得单独记：**市场标识的写法不能依赖配置**。快照是审计对象，它的键必须跨配置变更稳定。

## 5. 12 个测试覆盖了什么

| 测试 | 断言的核心性质 |
| --- | --- |
| `TestBuildMarketSnapshotHappyPath` | 五类事实齐备、`SourceTime` = 最新已收盘、`LiveCandle` 更新、7 条均线、质量标签（EXCHANGE_REST / DERIVED）、契约校验通过、门禁放行 |
| `TestFetchWindowCoversTheIndicators` | 窗口 = `now - 231h`；`seenEnd == now`（不是墙上时钟）；pair/asset/interval 原样透传；**一次组装只取一轮 K 线** |
| `TestGeneratedSnapshotIDAndSuppliedID` | 不传 ID 则生成且两次不同；传了就原样用 |
| `TestBuildRefusesMisuse` | nil source · 缺 `now` · 不支持的间隔 · 不可能的指标参数 |
| `TestProviderErrorsAreNotSwallowed` | "currency not enabled" 必须透传（引擎已经做过的判断，组装器不能替它兜成一张快照）；空窗口 / nil 窗口都拒 |
| `TestMissingCachedReadingsDoNotFailTheAssembly` | 冷缓存仍可组装；缺席 ≠ 不可信（`QualityUnknown`）；缺席 ≠ 陈旧 |
| `TestOrderBookKeepsTheExchangesDecimalStrings` | `"203.90000000"` 原样保留；价差与中价已派生；盘口自己的读取时间被记录 |
| `TestFreshnessGateIsAppliedBeforeReturning` | 5 分钟前的 ticker → 门禁拒绝 + `RISK_SNAPSHOT_STALE` + 快照被标 `STALE` + **快照仍返回** |
| `TestSuppliedAccountAndBalances` | 按 USDT 汇总（忽略 BTC）；余额读时间被用；调用方提供的位置优先且余额只补它缺的；两者都无则类目缺席 |
| `TestCandleWindowIsBoundedAndTheLiveBarIsSeparated` | 400 根 → 收成 50；`Count` 反映取到的总数而非携带数；裁剪后仍能通过契约校验 |
| `TestAssemblyIsDeterministic` | 同输入两次组装 **JSON 字节级一致**（否则存档快照的哈希无法与重算结果比对） |
| `TestEverySupportedIntervalMaps` | 6 个间隔双向映射；**契约的间隔列表与映射表大小相同**（任一侧多支持一个都会失败） |

## 6. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **组装器没有任何调用方** | **M2-14 / M4** | 本次交付的是**可用的组件**，不是生效的链路。它要跑起来需要有人按周期调它（轮询执行器）或决策流水线调它 |
| **位置事实的装配**（方向、强平价、杠杆） | **M5-08** | 见 §3.6。缺口是刻意的：引擎类型不带方向，编一个出来会给风控塞伪造数据 |
| **资金费率事实** | **M2-07 / M2-15 后续** | 本次**没有**装配 `FundingFacts`。`getlatestfundingrate` 存在于网关，但装配它需要决定"批量缓存 vs 按需拉"（文档 `04` §4 提到资金费率批量端点）。当前类目缺席 |
| **`onDemand` 路径**（可用未启用的合约） | **M2-01** | `GetHistoricCandles` 会因 `currency not enabled` 拒绝。这是刻意的：**未被追踪的市场没有值得构建的快照**。Q4=B 的按需只读需要绕过该校验的路径，属 M2-01（见 `session-15` §4 的实测） |
| **K 线价格的精度来源** | — | `kline.Candle` **只带 float64**，不带交易所的十进制字符串（与盘口不同）。所以 K 线价格是平台浮点经 12 位渲染的结果，**不是交易所的精确字符串**。从 `kline` 表读（那里存字符串）可以消除这个限制。已在代码注释里写明 |
| **指标缓存**（`04` §2.4） | **M2-02** | 每次组装重算 250 根的指标。文档说"微秒级，不需要缓存"，**本次未实测这个断言** |
| **ticker / orderbook 的刷新** | **M2-14** | 组装器只读缓存。缓存由谁刷新、多久刷新一次，属轮询执行器 |
| **真实交易所往返** | **M2-15 后续 / M2-14** | 全部测试用假 source。**没有对真实交易所跑过一次组装**——那需要 M2-14 或一个手动触发入口 |

## 7. 校验证据

```text
$ gofmt -l engine/ llm/          # 无残留
$ go build ./...                 # 通过
$ go vet ./engine/ ./llm/...     # 无输出
$ go test ./llm/... -count 1     # 八包全绿（329 个测试函数）
$ go test ./engine/ -run '<组装器与指标测试>' -count 1 -v
--- PASS  × 17（含 3 个既有引擎测试被宽匹配命中）
其中本次新增 12 个 · session-19 的 3 个跨层交叉验证仍通过

编译期断言：engine/llm_snapshot_test.go:448  var _ iMarketDataSource = (exchange.IBotExchange)(nil)

文档：markdownlint docs/llm-dev 37 files → 0 issues
```

**全部离线**：无网络、无数据库、无时钟依赖、**不需要运行中的 bot**。

## 8. 给接手模型的具体建议

**`llm/` + 组装器这一侧现在是完整的**，从契约到派生到新鲜度再到真实交易所类型的转换都齐了。缺口在两端：

1. **上游：`M2-14` 轮询执行器**——它是唯一会按周期调 `BuildMarketSnapshot` 的东西，而且缓存（ticker/orderbook）要由它刷新，否则组装器读到的永远是冷缓存。做完它，这套东西第一次对着真实币安数据跑。
2. **下游：`M0-06` → `M0-07` → `M1-09`~`M1-13`**——决策记录要落库。前置（pg）已就位（`docker compose up -d postgres`）。

**另外两个仍然挡着一整片的**：proto 工具链校准（`TASKS.md` §0.1，挡 `M1-14`/`W7-07`/W5-W6 接口层）。

**做 `M2-14` 时请沿用本会话的两条做法**：

- **窄接口而不是大接口**。`iMarketDataSource` 只有 5 个方法，所以组装器能在没有 bot 的情况下被完整测试。轮询执行器应该照做——它需要的是"读一个市场的 K 线/ticker/盘口"和"发一个请求"，不是整个 `iExchangeManager`。
- **时间从外面注入**。组装器不读墙上时钟，所以它的年龄断言是确定的。执行器的调度逻辑同理（`coverage.Gate` 的 `Allow(weight, now)` 也是这么设计的）——**能注入的时间不要自己取**。
