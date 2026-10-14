# 02 GoCryptoTrader 现状能力盘点

> **实现状态（2026-10-11）**：本文档是设计原稿。已实现落点、两处实测偏差（588 合约口径 / KlineWeight 分档）与全部已知残余见 [实现后状态对照](实现后状态对照.md)；进度与缺口以 [`taskProgress/`](taskProgress/) 为准。

本文逐项核对当前工作副本（分支 `llm-dev`，HEAD `492e8f06`）里**已有什么、缺什么**，作为后续设计的依据。所有路径和签名都经过源码核对。

## 0. 先纠正一批常见误解

网上流传的 GoCryptoTrader 教程和本仓库当前状态有多处不符。**下表是设计方案前必须校准的事实**：

| 常见说法 | 本仓库实际情况 |
| --- | --- |
| 入口在 `cmd/gocryptotrader/main.go` | ❌ 不存在。入口是**仓库根目录 `main.go`** |
| K 线类型在 `types/candle.go` | ❌ 不存在。在 `exchanges/kline/kline_types.go` |
| 指标包是 `types/indicator` | ❌ 不存在。在 `exchanges/kline/technical_analysis.go` |
| 交易所接口在 `exchange/interface.go` | ❌ 是 `exchanges/interfaces.go`（`IBotExchange`） |
| 有 `engine/ticker.go`、`engine/orderbook.go`、`engine/trade.go`、`engine/funding.go` | ❌ 都不存在。ticker/orderbook/trade 是 `exchanges/` 下的**包级单例服务** |
| 订单类型在 `order/side.go`、`order/submit.go` | ❌ 合并到 `exchanges/order/order_types.go` + `orders.go` |
| proto 在 `pkg/rpcproto/` | ❌ 在 `gctrpc/rpc.proto`（单文件单服务） |
| 有 `engine/candles*.go` 内存 K 线缓存子系统 | ❌ **不存在**。K 线只有数据库持久化路径 |
| 主引擎有策略子系统（`engine/strategy/`、`strategies/`） | ❌ **不存在**。唯一的策略框架在 `backtester/` |
| 有 `futuresOrders` 配置段 | ❌ **不存在** |
| 币安永续资产标识是 `usdt_swap` | ❌ 是 `usdtmarginedfutures` |
| `useSandbox` 能让币安走测试网 | ❌ 只有 Gemini 读这个字段 |

## 1. K 线 / OHLCV 数据链路 ✅ 完整可用

### 1.1 核心类型（`exchanges/kline/kline_types.go`）

```go
type Item struct {                                    // l.130-140
    Exchange        string
    Pair            currency.Pair
    UnderlyingPair  currency.Pair
    Asset           asset.Item
    Interval        Interval
    Candles         []Candle
    SourceJobID     uuid.UUID
    ValidationJobID uuid.UUID
}

type Candle struct {                                  // l.142-152
    Time             time.Time
    Open, High, Low, Close, Volume, QuoteVolume float64
    ValidationIssues string
}

type Interval time.Duration                           // l.179
```

`Interval` 常量（l.13-51）从 `TenMilliseconds` 到 `OneYear`，含 `OneMin, ThreeMin, FiveMin, TenMin, FifteenMin, ThirtyMin, OneHour, TwoHour, ThreeHour, FourHour, SixHour, SevenHour, EightHour, TwelveHour, OneDay, TwoDay, ThreeDay, SevenDay, FifteenDay, FiveDay, OneWeek, TwoWeek, ThreeWeek, OneMonth(30d), ThreeMonth(90d), SixMonth, NineMonth, OneYear(365d)`，以及 `Raw = Interval(-1)`。另有 `SupportedIntervals []Interval`（l.95-127）和 `IntervalCapacity{Interval, Capacity uint64}`（l.214-217）。

`Interval` 的方法（`exchanges/kline/kline.go`）：`String()`(l.126)、`Word()`(l.131)、`Duration()`(l.136)、`Short()`(l.214)、`IntervalsPerYear()`(l.517)、`UnmarshalJSON`(l.235，接受纳秒整数或 `"1h"`/`"15m"`/`"1M"` 文本)、`MarshalText`(l.307)、`ParseInterval(text string) (Interval, error)`(l.313，拒绝 `Raw` 和非正值)。

### 1.2 数据清洗工具（`exchanges/kline/kline.go`）

| 方法 | 行号 | 作用 |
| --- | --- | --- |
| `RemoveDuplicates()` | 390 | 按 `Time.Unix()` 去重 |
| `RemoveOutsideRange(start, end)` | 407 | 裁掉区间外 |
| `SortCandlesByTimestamp(desc bool)` | 419 | `slices.SortFunc` on `Time.Compare` |
| `FormatDates()` | 428 | 强制 `.UTC()` |
| `addPadding(start, exclusiveEnd, purgeOnPartial)` | 330 | 缺口补零 K 线 |
| `ConvertToNewInterval(newInterval)` | 528 | **升采样**（1m→1h） |
| `GetClosePriceAtTime(t)` | 697 | 取某时刻收盘价 |
| `EqualSource(i *Item)` | 795 | 校验同源 |
| `TotalCandlesPerInterval(start, end, interval)` | 503 | 区间内应有根数 |
| `CalculateCandleDateRanges(start, end, interval, limit)` | 622 | 按 API 上限切批 |
| `CreateKline(trades, interval, pair, a, exchName)` | 21 | 从成交构建 K 线 |

时间处理：一律 UTC。`alignIntervalStart`(l.159) 按间隔截断；日历间隔（月/年）锚定真实 UTC 月界（`calendarIntervalMonths` l.140）。`IntervalTime{Time, Ticks int64}`(types l.208-211) 缓存 unix ticks 加速比较。

`ConvertToNewInterval` 约束：**只能升采样**、必须整数倍、输入必须已 padding（否则 `errCandleDataNotPadded`）、末尾不完整组丢弃、零值 K 线跳过。

交易所间隔能力注册表（l.812-874）：

```go
func DeployExchangeIntervals(enabled ...IntervalCapacity) ExchangeIntervals
func (e *ExchangeIntervals) ExchangeSupported(in Interval) bool
func (e *ExchangeIntervals) Construct(required Interval) (Interval, error)   // 例如用 2×30m 合成 1h
func (e *ExchangeCapabilitiesEnabled) GetIntervalResultLimit(interval Interval) (uint64, error)
```

### 1.3 交易所接口（`exchanges/interfaces.go:74-75`）

```go
GetHistoricCandles(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
GetHistoricCandlesExtended(ctx context.Context, pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time) (*kline.Item, error)
```

**没有** `GetHistoricCandlesRequest`/`Response` 类型，也**没有** `GetAvailableCandlePeriods`（已删除）。请求抽象是 `kline.Request` / `kline.ExtendedRequest`（`exchanges/kline/request.go:30-62`），字段：`Exchange, Pair, RequestFormatted, Asset, ExchangeInterval, ClientRequired, Start, End, PartialCandle, IsExtended, ProcessedCandles, RequestLimit`。

构造与处理：

```go
func CreateKlineRequest(name string, pair, formatted currency.Pair, a asset.Item, clientRequired, exchangeInterval Interval, start, end time.Time, specificEndpointLimit uint64) (*Request, error)   // l.66
func (r *Request) ProcessResponse(timeSeries []Candle) (*Item, error)          // l.146
func (r *ExtendedRequest) ProcessResponse(timeSeries []Candle) (*Item, error)  // l.215
```

`ProcessResponse` 是**原始数据 → 处理后数据**的地方：`RemoveDuplicates` → `RemoveOutsideRange` → `SortCandlesByTimestamp(false)` → `addPadding` → 可选 `ConvertToNewInterval` → 若最后一根窗口超过当前时间则标 `ValidationIssues = "Partial Candle"`（常量 `request.go:25`）。`ExtendedRequest` 版本额外跑 `RangeHolder.SetHasDataFromCandles` 并打 `DataSummary` 缺口日志。

基础层辅助（`exchanges/exchange.go`）：

```go
func (b *Base) GetKlineRequest(pair currency.Pair, a asset.Item, interval kline.Interval, start, end time.Time, fixedAPICandleLength bool) (*kline.Request, error)      // l.1482
func (b *Base) GetKlineExtendedRequest(...) (*kline.ExtendedRequest, error)                                                                                            // l.1555
```

`GetKlineRequest` 会解析可合成间隔、校验交易对已启用、格式化交易对、取结果上限，窗口放不下时报 `kline.ErrRequestExceedsExchangeLimits`（l.1535）。

### 1.4 币安实现

```go
func (e *Exchange) GetHistoricCandles(ctx, pair, a, interval, start, end) (*kline.Item, error)          // binance_wrapper.go:1569-1645
func (e *Exchange) GetHistoricCandlesExtended(...)                                                       // binance_wrapper.go:1649-1727
func (e *Exchange) FormatExchangeKlineInterval(interval kline.Interval) string                           // binance_wrapper.go:1553
func (e *Exchange) UKlineData(ctx, symbol currency.Pair, interval string, limit uint64, startTime, endTime time.Time) ([]FuturesCandleStick, error)  // binance_ufutures.go:183-222
```

USDT-M 走 `UKlineData`，端点 `/fapi/v1/klines?`（`binance_ufutures.go:32`）。按 limit 分档计费：

```go
case limit > 0 && limit <= 100:    rateBudget = uFuturesKline100Rate    // weight 1
case limit > 100 && limit <= 500:  rateBudget = uFuturesKline500Rate    // weight 2
case limit > 500 && limit <= 1000: rateBudget = uFuturesKline1000Rate   // weight 5
case limit > 1000:                 rateBudget = uFuturesKlineMaxRate    // weight 10
```

**单次上限 1000 根**（`GlobalResultLimit: 1000`，`binance_wrapper.go:167`；各间隔的 `IntervalCapacity` 都没设 `Capacity`，l.150-166，所以 `GetIntervalResultLimit` 对所有间隔返回 1000）。

启用间隔：`1m,3m,5m,15m,30m,1h,2h,4h,6h,8h,12h,1d,3d,1w,1M`（l.150-166）。

限频（`exchanges/binance/ratelimit.go`）：现货 6000 weight/min；**USDT-M 合约 2400 weight/min**；币本位 2400/min；**USDT-M 下单 300 次/10s**；现货下单 100 次/10s。

缺口处理：由通用的 `kline.ExtendedRequest`（批次切分 + `SetHasDataFromCandles` + `DataSummary`）和 `addPadding` 负责。**没有币安特有的回填逻辑**。

### 1.5 持久化

**没有内存 K 线缓存子系统**。唯一路径是数据库（`exchanges/kline/kline_datastorage.go`）：

```go
func LoadFromDatabase(exch string, pair currency.Pair, a asset.Item, interval Interval, start, end time.Time) (*Item, error)  // l.15
func StoreInDatabase(in *Item, force bool) (uint64, error)                                                                    // l.55
```

模型 `database/models/{postgres,sqlite3}/candle.go`，仓储 `database/repository/candle/candle.go`，seed 工具 `cmd/dbseed/candle.go`。

### 1.6 DataHistoryManager ✅ 存在且活跃

`engine/datahistory_manager.go`（注意文件名不是 `data_history_manager.go`）。文档 `engine/datahistory_manager.md` 定义为"负责确保你指定区间内的 K 线/成交历史同步到数据库"的引擎子系统。

```go
type DataHistoryManager struct   // datahistory_manager_types.go:128-144
// 持有 exchangeManager, databaseConnectionInstance, maxJobsPerCycle, maxResultInsertions
// 以及四个可注入函数：candleLoader / tradeLoader / tradeSaver / candleSaver
// 默认接线在 SetupDataHistoryManager (datahistory_manager.go:28-68)：
//   candleLoader → kline.LoadFromDatabase
//   tradeLoader  → trade.GetTradesInRange
//   tradeSaver   → trade.SaveTradesToDatabase
//   candleSaver  → kline.StoreInDatabase

type DataHistoryJob struct       // datahistory_manager_types.go:148-174
// ID, Nickname, Exchange, Asset, Pair, StartDate, EndDate, Interval,
// RunBatchLimit, RequestSizeLimit, DataType, MaxRetryAttempts, Status, Results,
// OverwriteExistingData, ConversionInterval, DecimalPlaceComparison,
// SecondaryExchangeSource, IssueTolerancePercentage, ReplaceOnIssue,
// PrerequisiteJobID, PrerequisiteJobNickname
```

公开 API：`Start(ctx)`(l.75)、`Stop()`(l.101)、`IsRunning()`(l.93)、`PrepareJobs()`(l.146)、`UpsertJob(job, insertOnly)`(l.1107)、`SetJobRelationship(prereq, job)`(l.1074)、`GetByID`(l.1277)、`GetByNickname(nickname, fullDetails)`(l.1301)、`GetAllJobStatusBetween(start, end)`(l.1336)、`SetJobStatus(nickname, id, status)`(l.1362)、`GetActiveJobs()`(l.1419)、`GenerateJobSummary(nickname)`(l.1448)。

任务类型（l.26-33）：candles、trades、trade→candle 转换、**candle→candle 转换**（用 `ConvertToNewInterval`）、candle 校验、次级来源校验。状态（l.36-43）：`active/failed/complete/removed/issues-found/paused`。

默认值（l.112-124）：`defaultDataHistoryMaxJobsPerCycle=5`、`defaultMaxResultInsertions=10000`、`defaultDataHistoryBatchLimit=3`、`defaultDataHistoryRetryAttempts=3`、`defaultDataHistoryRequestSizeLimit=500`，ticker 间隔 1 分钟。

配置 `config.DataHistoryManager`（`config/config_types.go:139-145`）：`enabled, checkInterval, maxJobsPerCycle, maxResultInsertions, verbose`。

**只走 REST 批量拉取，不消费 WebSocket K 线。数据在 SQL 里，不在内存。**

### 1.7 币安 WebSocket K 线 ⚠ 现货才有

`wsAddDataHandler`（`binance_websocket.go:369-400`）处理 `kline_1m … kline_1M` 频道，转成单根 K 线的 `kline.Item` 发到 `e.Websocket.DataHandler.Send(ctx, ...)`；未收盘的标 `ValidationIssues = kline.PartialCandle`（l.383-385）。

消息类型（`binance_types.go:179-205`）：`KlineStream{EventType "e", EventTime, Symbol, Kline KlineStreamData}`；`KlineStreamData{StartTime, CloseTime types.Time; Symbol, Interval string; FirstTradeID, LastTradeID int64; OpenPrice, ClosePrice, HighPrice, LowPrice, Volume types.Number; NumberOfTrades int64; KlineClosed bool; Quote, TakerBuyBaseAssetVolume, TakerBuyQuoteAssetVolume types.Number}`。

订阅机制：`GetSubscriptionTemplate`(l.510)、模板映射(l.1019-1030) `candles → kline_<interval>`、`Subscribe/Unsubscribe` 用 `WsPayload{Method:"SUBSCRIBE", Params: subs.QualifiedChannels()}`(l.545-590)。

**但是**：`SetDefaults()`（`binance_wrapper.go:75-79`）对 `asset.Margin`、`asset.CoinMarginedFutures`、`asset.USDTMarginedFutures` 调 `DisableAssetWebsocketSupport`。默认订阅(l.170-175)全是现货。

> **设计影响**：币安永续的实时数据只能靠 SyncManager 的 REST 轮询。这决定了 [04-数据层](04-数据层-K线与指标.md) 的时效性模型和"数据新鲜度"门禁阈值必须按轮询周期设定，不能按 WS 推送设定。

## 2. 技术指标 ✅ 完整可用（原项目缺失的能力）

包 `exchanges/kline`，文件 `technical_analysis.go` + `weighted_price.go`，数学委托给外部库 `github.com/thrasher-corp/gct-ta/indicators`（`go.mod:21`）。

**没有** `types/indicator` 包，**没有** `Indicator` 包装类型，**没有**间隔感知的指标缓存。指标是挂在 `*kline.Item`（便利版）和 `*kline.OHLC`（核心版）上的**无状态方法**，返回与输入序列等长对齐的 `[]float64`。

```go
type OHLC struct { Open, High, Low, Close, Volume []float64 }   // technical_analysis.go:19-46
func (k *Item) GetOHLC() *OHLC
```

完整清单（精确签名）：

| 方法（`*Item` 版 / 行号） | 签名 | 底层 |
| --- | --- | --- |
| `GetAverageTrueRange` :49 / `*OHLC` :54 | `(period int64) ([]float64, error)` | `indicators.ATR(H,L,C,period)` |
| `GetBollingerBands` :78 / :90 | `(period int64, nbDevUp, nbDevDown float64, m indicators.MaType) (*Bollinger, error)` | `indicators.BBANDS` |
| `GetCorrelationCoefficient` :121 / :127 | `(other *Item, period int64) ([]float64, error)` | — |
| **`GetSimpleMovingAverageOnClose`** :163 / `GetSimpleMovingAverage` :170 | `(period int64) ([]float64, error)` / `(option []float64, period int64)` | `indicators.SMA` |
| **`GetExponentialMovingAverageOnClose`** :189 / `GetExponentialMovingAverage` :196 | `(period int64) ([]float64, error)` / `(option []float64, period int64)` | `indicators.EMA` |
| **`GetMovingAverageConvergenceDivergenceOnClose`** :223 / `GetMovingAverageConvergenceDivergence` :231 | `(fast, slow, signal int64) (*MACD, error)` / `(option []float64, fast, slow, signal int64)` | `indicators.MACD` |
| `GetMoneyFlowIndex` :268 / :273 | `(period int64) ([]float64, error)` | `indicators.MFI` |
| `GetOnBalanceVolume` :311 / :316 | `() ([]float64, error)` | `indicators.OBV` |
| `GetRelativeStrengthIndexOnClose` :331 / `GetRelativeStrengthIndex` :338 | `(period int64) ([]float64, error)` | `indicators.RSI` |
| `GetTWAP` `weighted_price.go:38/50` | `() (float64, error)` | — |
| `GetVWAPs` `weighted_price.go:98/114` | `() ([]float64, error)` | — |
| `GetAveragePrice` / `GetTypicalPrice` `weighted_price.go:15/74/20/80` | `() float64` | — |

MACD 返回类型（`technical_analysis.go:214-218`）：

```go
type MACD struct {
    Results    []float64   // DIF
    SignalVals []float64   // DEA
    Histogram  []float64   // 柱
}
```

校验：`fast < slow`、都 > 0、`len(data) >= slow+signal-2`，否则 `errNotEnoughData`（l.251）。

`Bollinger`（l.83）：`struct { Upper, Middle, Lower []float64 }`。

**用户要求的 MACD 和均线全部已有，零新增开发量。**

RPC 暴露：`GetTechnicalAnalysis`（`engine/rpcserver.go:4612-4767`），先用 `GetHistoricCandlesExtended` 拉 K 线，再 switch `algorithmType` ∈ `TWAP, VWAP, ATR, BBANDS, COCO, SMA, EMA, MACD, MFI, OBV, RSI`（l.4638-4763），返回 `map<string, ListOfSignals>`。gctcli 侧是 `technicalanalysis` 命令的 11 个子命令（`cmd/gctcli/technical_analysis.go:123-197`）。

> **设计影响**：这个 RPC 就是 LLM 只读工具的现成模板——它已经做了"拉 K 线 + 算指标 + 结构化返回"的完整链路。LLM 工具层可以直接复用同一套代码路径，见 [05-LLM接入层](05-LLM接入层-OpenAI兼容.md) §4。

## 3. 实时行情 ✅ 可用（但无 WS）

### 3.1 Ticker（`exchanges/ticker/ticker.go`）

**包级单例** `service *Service`（`init()` 初始化，l.32）。**没有 engine ticker 子系统**——WS 处理已迁移到交易所 wrapper（commit `aee155d4`）：币安 WS handler 先调 `ticker.ProcessTicker(tickPrice)` 再转发 DataHandler（`binance_websocket.go:365-368`）；REST wrapper 在 `UpdateTicker`/`UpdateTickers` 里调 `ProcessTicker`/`ProcessBatch`。

```go
func SubscribeTicker(exchange string, p currency.Pair, a asset.Item) (dispatch.Pipe, error)   // l.41
func SubscribeToExchangeTickers(exchange string) (dispatch.Pipe, error)                        // l.56
func GetTicker(exchange string, p currency.Pair, a asset.Item) (*Price, error)                 // l.70
func GetExchangeTickers(exchange string) ([]*Price, error)                                     // l.92
func FindLast(p currency.Pair, a asset.Item) (float64, error)                                  // l.118
func ProcessTicker(p *Price) error                                                             // l.134
func ProcessBatch(p []Price) ([]Price, error)                                                  // l.189
```

**没有** `GetAllTickers`；全引擎聚合是 `Engine.GetAllActiveTickers()`（`engine/helpers.go:748-749`）。交易所级缓存读：`Base.GetCachedTicker(p, a)`（`exchanges/exchange.go:1891-1893`）。接口：`IBotExchange.UpdateTicker(ctx,p,a) (*ticker.Price, error)`、`UpdateTickers(ctx,a) error`（`interfaces.go:46-47`）。

`ticker.Price`（`ticker_types.go:28-57`）：`Last, LastSize, VolumeWeightedAveragePrice, High, Low, Bid, BidSize, Ask, AskSize, BaseVolume, QuoteVolume, Open, Open24Hour, PercentChange24Hour, Close, OpenInterest, OpenInterestValue, MarkPrice, IndexPrice float64; Pair currency.Pair; ExchangeName string; AssetType asset.Item; LastUpdated time.Time; FlashReturnRate, BidPeriod, AskPeriod, FlashReturnRateAmount float64`。校验会拒绝交叉/锁定市场（`ErrBidEqualsAsk`，l.20）。更新通过 `dispatch.Mux` 发布（每 ticker 一个 `Main` UUID + 每交易所一个 `Assoc` UUID）。

**永续有用的字段**：`MarkPrice`（标记价，算未实现盈亏和强平）、`IndexPrice`（指数价）、`OpenInterest`/`OpenInterestValue`（持仓量）。

### 3.2 Orderbook（`exchanges/orderbook/`）

中央 `store` 单例（`orderbook.go:46-63`）：

```go
func Get(exchange string, p currency.Pair, a asset.Item) (*Book, error)                  // l.18
func GetDepth(exchange string, p currency.Pair, a asset.Item) (*Depth, error)            // l.23
func DeployDepth(exchange string, p currency.Pair, a asset.Item) (*Depth, error)         // l.30
func SubscribeToExchangeOrderbooks(exchange string) (dispatch.Pipe, error)               // l.35
func (b *Book) Process() error                                                           // l.161
```

`IBotExchange.UpdateOrderbook(ctx,p,a) (*orderbook.Book, error)`（`interfaces.go:49`）；缓存读 `Base.GetCachedOrderbook`（`exchanges/exchange.go:1897-1899`）。`store.Update(b *Book)`(l.46) 校验+处理后发布给 dispatch 订阅者。增量更新在 `incremental_updates.go`，深度聚合在 `depth.go`，另有 `orderbook/simulator` 包（回测滑点用）。

`orderbook.Book`（`orderbook_types.go:88-137`）关键字段：`Bids, Asks Levels`；`Exchange, Pair, Asset`；`LastUpdated, LastPushed, InsertedAt time.Time`；`LastUpdateID int64`；`PriceDuplication, IsFundingRate, ValidateOrderbook, RestSnapshot, IDAlignment, ChecksumStringRequired bool`；`MaxDepth int`。`Level`(l.66-85)：`Amount float64; StrAmount string; Price float64; StrPrice string; ID int64; Period int64; LiquidationOrders, OrderCount int64`。`Movement`(l.158-183) 支持滑点/冲击查询。

**`StrPrice` / `StrAmount` 是原始字符串**，这对金融精度很重要——LLM 工具层应该优先用字符串而不是 float64，见 [05](05-LLM接入层-OpenAI兼容.md) §5。

### 3.3 Trade（`exchanges/trade/`）

**没有 engine trade 子系统**。包内有单例缓冲 `Processor`（`trade_types.go:20-27`，flush 间隔 `DefaultProcessorIntervalTime = 15s`，可用 `-tradeprocessinginterval` 调）。

```go
type Data struct { ID uuid.UUID; TID, Exchange string; CurrencyPair currency.Pair; AssetType asset.Item; Side order.Side; Price, Amount float64; Timestamp time.Time }  // trade_types.go:37-47
func (t *Trade) Setup(tradeFeedEnabled bool, c *stream.Relay)                                            // trade.go:36
func (t *Trade) Update(save bool, data ...Data) error                                                    // trade.go:43
func AddTradesToBuffer(data ...Data) error                                                               // trade.go:66
func SaveTradesToDatabase(trades ...Data) error                                                          // trade.go:147
func GetTradesInRange(exchangeName, assetType, base, quote string, startDate, endDate time.Time) ([]Data, error)  // trade.go:153
func ConvertTradesToCandles(interval kline.Interval, trades ...Data) (*kline.Item, error)                 // trade.go:230
func FilterTradesByTime(trades []Data, startTime, endTime time.Time) []Data                               // trade.go:289
```

**没有成交的 dispatch pipe**。WS 成交流：交易所 wrapper → `Trade.Update` → `stream.Relay`（`exchange/stream/relay.go:14-46`：`NewRelay(buffer uint)`、`Send(ctx, data any)`、`Close()`）→ `WebsocketRoutineManager.ProcessWSData`，而后者对 `case []trade.Data, trade.Data:` **目前只打日志**（`engine/websocketroutine_manager.go:370-373`）。

> **设计影响**：想要成交流，要么 hook `ProcessWSData`，要么开 `tradeFeedEnabled` 接 relay，要么从数据库轮询 `GetTradesInRange`。币安永续无 WS，所以实际只能走数据库轮询。低频策略下这不是问题。

### 3.4 资金费率（永续核心）✅

**没有 `engine/funding.go`**。类型在 `exchanges/fundingrate/fundingrate_types.go`：

```go
type HistoricalRatesRequest struct { Asset; Pair; PaymentCurrency; StartDate, EndDate; IncludePayments, IncludePredictedRate, RespectHistoryLimits bool }  // l.22-37
type HistoricalRates struct { Exchange; Asset; Pair; StartDate, EndDate; LatestRate, PredictedUpcomingRate Rate; FundingRates []Rate; PaymentSum decimal.Decimal; PaymentCurrency; TimeOfNextRate }  // l.40-52
type LatestRateRequest struct { Asset; Pair; IncludePredictedRate bool }   // l.55-59
type LatestRateResponse struct { ...; LatestRate, PredictedUpcomingRate; TimeOfNextRate, TimeChecked }  // l.62-70
type Rate struct { Time time.Time; Rate, Payment decimal.Decimal }         // l.73-77
```

接口（`interfaces.go:196-197`）：

```go
GetHistoricalFundingRates(context.Context, *fundingrate.HistoricalRatesRequest) (*fundingrate.HistoricalRates, error)
GetLatestFundingRates(context.Context, *fundingrate.LatestRateRequest) ([]fundingrate.LatestRateResponse, error)
```

**币安两个都实现了**：`binance_wrapper.go:1876`（latest，USDT-M + 币本位 + 批量）、`binance_wrapper.go:2014`（historical；内部 `UGetFundingHistory` `binance_ufutures.go:255`、`FuturesGetFundingHistory` `binance_cfutures.go:370`）。能力标志：REST `FundingRateFetching: true`(l.109)、WS `false`(l.122)、`SupportedFundingRateFrequencies: {4h, 8h}`(l.135-138)、`FundingRateBatching: {usdtmarginedfutures: true}`。

订单管理器已经消费 `GetHistoricalFundingRates` 来跟踪持仓的资金费（`engine/order_manager.go:824-831`）。RPC：`GetFundingRates`、`GetLatestFundingRate`（`rpc.proto:1950-1954`；handler `rpcserver.go:4206/4325`）。

### 3.5 合约仓位与保证金

接口 `FuturesManagement`（`interfaces.go:190-207`）和 `MarginManagement`（l.210-216）。**币安实现情况**：

| 接口方法（行号） | 币安 | 位置 |
| --- | --- | --- |
| `GetOpenInterest(ctx, ...key.PairAsset) ([]futures.OpenInterest, error)` (191) | ✅ | `binance_wrapper.go:2901` |
| `ScaleCollateral(ctx, *futures.CollateralCalculator)` (192) | ❌ | Base 默认 `exchange.go:1649` |
| `GetPositionSummary(ctx, *futures.PositionSummaryRequest)` (193) | ❌ | Base 默认 `exchange.go:1476` |
| `CalculateTotalCollateral(...)` (194) | ❌ | Base 默认 `exchange.go:1655` |
| `GetFuturesPositions(ctx, *futures.PositionsRequest)` (195) | ❌ | Base 默认 `exchange.go:1682` |
| `GetHistoricalFundingRates` / `GetLatestFundingRates` (196-197) | ✅ | 2014 / 1876 |
| `IsPerpetualFutureCurrency(asset.Item, currency.Pair)` (198) | ✅ | 2177 |
| `GetCollateralCurrencyForContract(...)` (199) | ❌ | Base 默认 |
| `GetFuturesPositionSummary(ctx, *futures.PositionSummaryRequest)` (201) | ✅ | 2297 |
| `GetFuturesPositionOrders(ctx, *futures.PositionsRequest)` (202) | ✅ | 2577 |
| `SetCollateralMode` / `GetCollateralMode` (203-204) | ✅ | 2188 / 2199 |
| `SetLeverage(ctx, item, pair, margin.Type, amount float64, order.Side) error` (205) | ✅ | 2745 |
| `GetLeverage(...)` (206) | ✅ | 2759 |
| `SetMarginType(ctx, item, pair, margin.Type) error` (211) | ✅ | 2214 |
| `ChangePositionMargin(ctx, *margin.PositionChangeRequest)` (212) | ✅ | 2239 |
| `GetMarginRatesHistory(...)` (213) | ❌ | Base 默认 |
| `GetFuturesContractDetails(ctx, asset.Item) ([]futures.Contract, error)` (215) | ✅ | 2790 |
| `futures.PNLCalculation`（内嵌，214） | ⚠ | 走 `Base.CalculatePNL` 默认 `exchange.go:1643` |

> **注意**：`GetFuturesPosition`、`GetFuturesTransactionHistory`、`GetFuturesCollateral`、`ModifyFuturesPositionLeverage`、`ModifyCollateral`、`GetMarginType`、`GetPositionsSummary` 这些名字在本仓库**都不存在**（可能来自上游旧版本文档）。用上表的真实名字。

本地仓位跟踪（`exchanges/futures/futures.go`）：`SetupPositionController() PositionController`(l.22)、`TrackNewOrder(*order.Detail)`(l.31)、`GetPositionsForExchange`(l.97)、`GetOpenPosition`(l.155)、`GetAllOpenPositions`(l.182)、`UpdateOpenPositionUnrealisedPNL(...)`(l.207)、`TrackFundingDetails(*fundingrate.HistoricalRates)`(l.117)、`Liquidate`(l.485/601)、`PositionTracker.GetStats() *Position`(l.498)。

OrderManager 暴露：`GetFuturesPositionsForExchange(exch, item, pair)`(`order_manager.go:260`)、`GetOpenFuturesPosition`(276)、`GetAllOpenFuturesPositions`(294)、`ClearFuturesTracking`(309)、`UpdateOpenPositionUnrealisedPNL`(326)。启用条件：配置 `orderManager.activelyTrackFuturesPositions` + `futuresTrackingSeekDuration`，且 `FuturesCapabilities.OrderManagerPositionTracking` 为真（`order_manager.go:735`）。

### 3.6 抵押品 ⚠ 无独立管理器

**没有 `engine/collateral.go`**。合约抵押品在 RPC `GetCollateral` 里按需计算（`engine/rpcserver.go:4394-4471`）：用 `GetCachedCurrencyBalances` 按 USD 价值缩放（交易所支持时走 `ScaleCollateral`，币安不支持）。`exchanges/collateral` 只提供 `Mode`（单向/双向）类型。

> **设计影响**：风控引擎算"可用保证金"时不能依赖现成的抵押品管理器，要么直接读 `GetCachedCurrencyBalances`，要么调 `GetFuturesPositionSummary`。这是 [07-策略与自动决策引擎](07-策略与自动决策引擎.md) 必须自己解决的一块。

### 3.7 投资组合管理器 ⚠ 现货导向

`engine/portfolio_manager.go`：`setupPortfolioManager(e *ExchangeManager, delay time.Duration, cfg *portfolio.Base)`(l.37)，周期性（`PortfolioSleepDelay = time.Minute`，l.22）调 `UpdateAccountBalances` 并镜像到 `portfolio.Base` 地址（`updateExchangeBalances` l.144、`updateExchangeAddressBalances` l.180）。API：`AddAddress`、`RemoveAddress`、`GetPortfolioSummary() portfolio.Summary`、`GetAddresses`、`GetPortfolio`、`IsWhiteListed`、`IsExchangeSupported`。

**它是现货/余额导向的，不跟踪合约可用保证金。**

## 4. 订单执行 ✅ 完整可用

### 4.1 OrderManager（`engine/order_manager.go`）

```go
func SetupOrderManager(exchangeManager iExchangeManager, communicationsManager iCommsManager, wg *sync.WaitGroup, cfg *config.OrderManager) (*OrderManager, error)  // l.31
func (m *OrderManager) Start(ctx context.Context) error / Stop() error / IsRunning() bool                                                                          // l.74/89/69
func (m *OrderManager) Submit(ctx context.Context, newOrder *order.Submit) (*OrderSubmitResponse, error)                                                            // l.478
func (m *OrderManager) SubmitFakeOrder(newOrder *order.Submit, resultingOrder *order.SubmitResponse, checkExchangeLimits bool) (*OrderSubmitResponse, error)         // l.529
func (m *OrderManager) Modify(ctx context.Context, mod *order.Modify) (*order.ModifyResponse, error)                                                                 // l.408
func (m *OrderManager) Cancel(ctx context.Context, cancel *order.Cancel) error                                                                                       // l.187
func (m *OrderManager) CancelAllOrders(ctx context.Context, exchanges []exchange.IBotExchange)                                                                       // l.148
func (m *OrderManager) GetOrderInfo(ctx, exchangeName, orderID string, cp currency.Pair, a asset.Item) (order.Detail, error)                                           // l.342
func (m *OrderManager) GetOrdersSnapshot(s order.Status) []order.Detail                                                                                              // l.567
func (m *OrderManager) GetOrdersFiltered(f *order.Filter) ([]order.Detail, error)                                                                                    // l.586
func (m *OrderManager) GetOrdersActive(f *order.Filter) ([]order.Detail, error)                                                                                      // l.598
func (m *OrderManager) FetchAndUpdateExchangeOrder(ctx, exch, ord *order.Detail, a asset.Item) error                                                                 // l.855
// Exists/Add/GetByExchangeAndID/UpdateExistingOrder/UpsertOrder  l.870/875/887/898/909
```

**没有** `SubmitOrders` 批量版，**没有** `GetOrdersInfo`。`OrderSubmitResponse{ *order.Detail; InternalOrderID string }`（`order_manager_types.go:68-71`）。

后台循环 `run` 每 `orderManagerInterval = 10s`（types l.28）跑一轮：`processOrders`(l.651) 同步各交易所挂单；启用时同步合约仓位 + 资金费 + 未实现盈亏（l.735-767、777-834）。提交/改单/撤单的成功与失败都会往 CommunicationsManager 推 `base.Event{Type:"order"}`（l.449-452、469-472）。

`Submit` 流程（l.478-525）：

```text
解析交易所 → m.validate → exch.CheckOrderExecutionLimits(asset, pair, price, amount, orderType)
           → exch.CanTradePair(pair, asset) → exch.SubmitOrder(ctx, newOrder)
           → processSubmittedOrder（存 Detail + TrackNewOrder 更新本地仓位）
```

> **这就是自动决策的落点。** 已经内建：限额校验、币种状态 halt、通信通知、仓位跟踪。LLM 层只需要产出一个合法的 `*order.Submit` 交给它。

### 4.2 order 包（`exchanges/order/order_types.go`）

**Side**（l.451-471）：`UnknownSide=0; Buy; Sell; Bid; Ask; AnySide; Long; Short; ClosePosition;` 加回测信号用的 `DoNothing; TransferredFunds; CouldNotBuy; CouldNotSell; CouldNotShort; CouldNotLong; CouldNotCloseShort; CouldNotCloseLong; MissingData`（`1 << iota` 位移）。

**Type**（l.377-402）：`UnknownType=0; Limit; Market; Stop; TakeProfit; TrailingStop; IOS; AnyType; Liquidation; Trigger; LimitMaker; OCO; ConditionalStop; TWAP; Chase; OptimalLimit; MarketMakerProtection`；组合 `StopLimit = Stop|Limit`、`StopMarket`、`TakeProfitMarket`、`TrailingStopLimit`、`Bracket = Stop|TakeProfit`；`func (t Type) Is(cmp Type) bool`(l.372)。

**Status**（l.343-366）：`UnknownStatus, AnyStatus, New, Active, PartiallyCancelled, PartiallyFilled, PartiallyFilledCancelled, Filled, Cancelled, PendingCancel, InsufficientBalance, MarketUnavailable, Rejected, Expired, Hidden, Open, AutoDeleverage, Closed, Pending, Cancelling, Liquidated, STP`。

**Submit**（l.46-117）：`Exchange, Type, Side, Pair, AssetType, TimeInForce, ReduceOnly bool, Leverage float64, Price, Amount, QuoteAmount, TriggerPrice float64, TriggerPriceType PriceType, ClientID, ClientOrderID string, AutoBorrow, AutoRepay bool, MarginType margin.Type, RetrieveFees bool, RetrieveFeeDelay time.Duration, RiskManagementModes, EndTime time.Time, StopDirection, TrackingMode/TrackingValue, LimitTrackingMode/LimitTrackingValue, RFQDisabled bool, SlippageTolerance float64`。

其它：`SubmitResponse`(l.120-150)、`Modify`(l.166-192)、`ModifyResponse`(l.195-217)、`Detail`(l.221-257，含 `InternalOrderID uuid.UUID`、`ExecutedAmount`、`RemainingAmount`、`AverageExecutedPrice`、`Fee/FeeAsset`、`MarginType`、`Trades []TradeHistory`)、`Cancel`(l.279-291)、`Filter`(l.261-273)、`MultiOrderRequest`(l.322-337)、`TradeHistory`(l.306-319)。

行为（`orders.go`）：`(s *Submit) Validate(requirements protocol.TradingRequirements, opt ...validate.Checker) error`(l.59，`protocol.TradingRequirements{SpotMarketBuyQuotation, SpotMarketSellBase, ClientOrderID bool}` 在 `exchanges/protocol/features.go:52-60`)、`DeriveSubmitResponse(orderID string) (*SubmitResponse, error)`(l.474)、`Detail.UpdateOrderFromDetail`(l.149)、`Detail.IsActive/IsInactive/WasOrderPlaced`(l.415/424/438)、`Detail.InferCostsAndTimes()`(l.863)、`Cancel.Validate`(l.1196)、`Modify.Validate`(l.1255)。TimeInForce 在 `timeinforce.go`。

### 4.3 币安永续下单的具体约束（`binance_wrapper.go:786-952`）

| 约束 | 细节 |
| --- | --- |
| Side 映射 | 只认 `order.Buy → "BUY"`、`order.Sell → "SELL"`（l.896-940）。**不传 `positionSide`** → **不支持双向持仓** |
| Type 映射 | `LIMIT, MARKET, STOP, TAKE_PROFIT, STOP_MARKET, TAKE_PROFIT_MARKET, TRAILING_STOP_MARKET` |
| TimeInForce | **硬编码 `"GTC"`**，不能配置 |
| ReduceOnly | 会转发（l.934） |
| Leverage | `s.Leverage` 必须是 0 或 1，否则 `order.ErrSubmitLeverageNotSupported`（l.793-795）。杠杆要单独 `SetLeverage` |
| ModifyOrder | 返回 `common.ErrFunctionNotSupported`（l.955-957） |
| 现货侧 | 用 `s.Side.IsLong()` 把 Long 折成 Buy |

### 4.4 纸面交易路径 ✅

- `-dryrun`（`main.go:35`）**只表示"不回写配置文件"**，不阻止下单。别当模拟交易用
- `OrderManager.SubmitFakeOrder`（l.529）：完整校验 + 入库 + 仓位跟踪，**不调交易所**。**只能从 Go 代码调用，没有 RPC 入口**
- RPC `SimulateOrder`（`rpcserver.go`）：**注意这不是纸面下单**。它硬编码 `asset.Spot`，且调的是 `orderbook.SimulateOrder()`，属于盘口冲击模拟器
- RPC `WhaleBomb` / `GetOrderbookAmountByNominal` / `GetOrderbookAmountByImpact`：盘口冲击与滑点估算
- 回测器就是这个二分：`useRealOrders ? orderManager.Submit : orderManager.SubmitFakeOrder`（`backtester/eventhandlers/exchange/exchange.go:360-375`）

## 5. 风控 ⚠ 关键缺口

### 5.1 已生效的约束

| 约束 | 位置 |
| --- | --- |
| 交易所侧最小/最大数量、价格精度 | `exch.CheckOrderExecutionLimits(a, cp, price, amount, orderType)`，由 `exchange/order/limits.MinMaxLevel` 实现，`UpdateOrderExecutionLimits` 刷新（`interfaces.go:87-89`） |
| 币种状态 halt | `exch.CanTradePair(pair, asset)`（`interfaces.go:136`），由 CurrencyStateManager 驱动 |

### 5.2 订单管理器限额：代码在但**没接线** ⚠

`orderManagerConfig`（`engine/order_manager_types.go:34-42`）：

```go
EnforceLimitConfig, AllowMarketOrders, CancelOrdersOnShutdown bool
LimitAmount float64
AllowedPairs currency.Pairs
AllowedExchanges []string
OrderSubmissionRetries int64
```

在 `validate`（`engine/order_manager.go:385-402`）里生效：拒绝市价单（若 `!AllowMarketOrders`）、拒绝 `Amount > LimitAmount`、检查交易所/交易对白名单。

**但是**：`SetupOrderManager` 只从 `config.OrderManager` 接了 `CancelOrdersOnShutdown` 一个字段（`engine/order_manager.go:61-63`）。`config.OrderManager`（`config/config_types.go:129-136`）在本 fork 里**没有 limit 配置段**，所以这些字段恒为零值、处于休眠状态。**也没有 `maxOrdersPerCycle` 这个配置键。**

> **这是本方案最重要的一个缺口。** 意味着：配置层面没有任何"单笔金额上限"、"每日亏损上限"、"频率限制"的现成实现。[07-策略与自动决策引擎](07-策略与自动决策引擎.md) §5 的硬风控引擎必须完全自建。

### 5.3 没有的东西

| 缺失 | 影响 |
| --- | --- |
| 单笔/累计名义额上限 | 必须自建 |
| 每日亏损上限（UTC 日界基线） | 必须自建 |
| 新增风险意图的滑动窗口频率限制 | 必须自建 |
| 滑点上限校验 | `order.Submit.SlippageTolerance` 字段存在，但币安 wrapper 不用它 |
| 最大并发仓位数 | 必须自建 |
| 紧急停机标志 | 必须自建（`CancelAllOrders` 可复用） |
| 逐市场交易授权 | 必须自建 |
| 订单对账 | 必须自建（`FetchAndUpdateExchangeOrder` 可作基础） |

## 6. 策略框架 ⚠ 主引擎没有

**主引擎（`engine/`）没有策略子系统。** 唯一的策略框架在**回测器**里，但回测器支持实盘数据 + 真实下单，是自然的接入点。

### 6.1 布局

`backtester/eventhandlers/strategies/`，注册表 + `Handler` 接口（`strategies_types.go:20-31`）：

```go
type Handler interface {
    Name() string
    Description() string
    OnSignal(data.Handler, funding.IFundingTransferer, portfolio.Handler) (signal.Event, error)
    OnSimultaneousSignals([]data.Handler, funding.IFundingTransferer, portfolio.Handler) ([]signal.Event, error)
    UsingSimultaneousProcessing() bool
    SupportsSimultaneousProcessing() bool
    SetSimultaneousProcessing(bool)
    SetCustomSettings(map[string]any) error
    SetDefaults()
    CloseAllPositions([]holdings.Holding, []data.Event) ([]signal.Event, error)
}
```

注册表（`strategies.go`）：`LoadStrategyByName(name string, useSimultaneousProcessing bool) (Handler, error)`(l.18)、`AddStrategy(strategy Handler) error`(l.70)。内置（l.88-93）：`dollarcostaverage`、`rsi`、`top2bottom2`、`binancecashandcarry`。

**没有 Lua，没有 YAML DSL**：策略是 Go 代码，内嵌或作为 **Go plugin** 加载。

基类模板：`backtester/eventhandlers/strategies/base`（`base.Strategy`，提供 `GetBaseData(d data.Handler)` 等）。

插件加载：`backtester/plugins/strategies/loader.go:16` — `func LoadCustomStrategies(strategyPluginPath string) error`，用 `plugin.Open(path)` + `p.Lookup("GetStrategies")` 断言成 `func() []strategies.Handler`。示例插件 `backtester/plugins/strategies/example/example-strategy.go`（`package main`，`func GetStrategies() []strategies.Handler { return []strategies.Handler{&CustomStrategy{}} }`，策略名 `"custom-strategy"`）。

### 6.2 策略拿到什么数据

`data.Handler` 覆盖 kline/trade 事件流（`Latest()`、`StreamClose()`、`HasDataAtTime(t)`、偏移量）——**不含 ticker/orderbook**（`backtester/README.md` 里列为 planned）。数据源（`backtester/config/strategyconfig_types.go:45-53`）：`APIData`、`DatabaseData`、`CSVData`、`LiveData`。

### 6.3 策略怎么产出订单

策略返回 `signal.Event`，`SetDirection(order.Side)`（`order.Buy/Sell/Long/Short/ClosePosition/DoNothing…`）；portfolio handler 决定规模；

```go
backtester/eventhandlers/exchange.Exchange.ExecuteOrder(o order.Event, dh data.Handler, om *engine.OrderManager, funds funding.IFundReleaser) (fill.Event, error)   // exchange.go:35
```

`placeOrder`（`exchange.go:338-380`）构造 `gctorder.Submit{Type: gctorder.Market, RetrieveFees: true, RetrieveFeeDelay: 500ms, …}`，然后 `RealOrders ? orderManager.Submit : SubmitFakeOrder`。

### 6.4 配置

`.strat` 文件（其实是 JSON）在 `backtester/config/strategyexamples/`。配置结构 `backtester/config/strategyconfig_types.go:32-41`：`strategy-settings{name, use-simultaneous-signal-processing, custom-settings{...}}`、`currency-settings`、`data-settings{interval (ns int64), data-type: "candle", api-data{start-date,end-date}}`、`portfolio-settings`、`statistic-settings`。

实盘配置：`LiveData{NewEventTimeout, DataCheckTimer, RealOrders bool, ClosePositionsOnStop bool, DataRequestRetryTolerance, DataRequestRetryWaitTime, ExchangeCredentials []Credentials}`（l.192-206）。

实盘 K 线加载器：`backtester/data/kline/live/live.go:21` — `func LoadData(ctx, timeToRetrieve time.Time, exch exchange.IBotExchange, dataType int64, interval time.Duration, currencyPair, underlyingPair currency.Pair, a asset.Item, verbose bool) (*kline.Item, error)`。

### 6.5 现成的指标策略范例

`backtester/eventhandlers/strategies/rsi/rsi.go`：`OnSignal`(l.50-103) 在 `d.StreamClose()` 上算 `indicators.RSI(backfilledData, period)`，RSI ≥ high 时 `order.Sell`，≤ low 时 `order.Buy`，`backfillMissingData`(l.177-196) 防缺口。`SetCustomSettings` 读 `rsi-period/rsi-low/rsi-high`(l.137-164)。

> **这就是"写死指标"的策略形态。本方案不采用这种模式**——按用户原项目的哲学，买卖假设由用户策略和当时数据驱动，指标只作为证据。但这段代码是**理解 backtester 策略接口的最好范例**。

### 6.6 回测器可作为常驻服务

回测器能作为长驻 gRPC 服务跑（`backtester/btrpc`，CLI `backtester/btcli`），外部服务可以通过 RPC 创建/运行策略。

## 7. 扩展点 ✅ 丰富

### 7.1 gRPC（`gctrpc/rpc.proto`）

单文件单服务 `service GoCryptoTraderService`（l.1505）。相关 RPC（proto 行号 → HTTP 网关路由）：

| 分类 | RPC |
| --- | --- |
| K 线 | `GetHistoricCandles`(1803, GET `/v1/gethistoriccandles`)、`ConvertTradesToCandles`(1855)、`GetTechnicalAnalysis`(1938)、`FindMissingSavedCandleIntervals`(1859) |
| Ticker | `GetTicker`(1560)、`GetTickers`(1567)、`GetTickerStream`(1791)、`GetExchangeTickerStream`(1795) |
| Orderbook | `GetOrderbook`(1571)、`GetOrderbooks`(1578)、`GetOrderbookStream`(1783)、`GetExchangeOrderbookStream`(1787)、`GetOrderbookMovement`(1956)、`GetOrderbookAmountByNominal`(1959)、`GetOrderbookAmountByImpact`(1962) |
| 账户 | `GetAccountBalances`(1582)、`UpdateAccountBalances`(1586)、`GetAccountBalancesStream`(1590) |
| 合约 | `GetFuturesPositionsSummary`(1926)、`GetFuturesPositionsOrders`(1929)、`GetManagedPosition`(1944)、`GetAllManagedPositions`(1947)、`GetCollateral`(1932)、`GetCollateralMode`/`SetCollateralMode`(1965/1971)、`GetLeverage`/`SetLeverage`(1968/1983)、`SetMarginType`(1977)、`ChangePositionMargin`(1989)、`GetOpenInterest`(1995)、`GetFundingRates`(1950)、`GetLatestFundingRate`(1953)、`GetMarginRatesHistory`(1941) |
| 订单 | `SubmitOrder`(1642)、`SimulateOrder`(1649)、`WhaleBomb`(1656)、`CancelOrder`(1663)、`CancelBatchOrders`(1670)、`CancelAllOrders`(1677)、`ModifyOrder`(1907)、`GetOrders`/`GetOrder`/`GetManagedOrders`(1628/1635/1901) |
| 配置/子系统/事件 | `GetConfig`(1594)、`GetSubsystems`/`EnableSubsystem`/`DisableSubsystem`(1510-1519)、`GetEvents`/`AddEvent`/`RemoveEvent`(1684-1696)、`Shutdown`(1935) |
| 历史数据任务 | `UpsertDataHistoryJob`(1870)、`GetDataHistoryJobDetails`(1877)、`GetActiveDataHistoryJobs`(1880)、`GetDataHistoryJobsBetween`(1883)、`SetDataHistoryJobStatus`(1889)、`UpdateDataHistoryJobPrerequisite`(1895) |
| 成交 | `GetRecentTrades`/`GetHistoricTrades`/`GetSavedTrades`(1843-1851) |
| 交易对 | `GetExchangePairs`(1769)、`SetExchangePair`(1776)、`SetExchangeAsset`(1807)、`SetAllExchangePairs`(1811)、`UpdateExchangeSupportedPairs`(1815)、`GetExchangeAssets`(1819) |
| WebSocket | `WebsocketGetInfo/SetEnabled/GetSubscriptions/SetProxy/SetURL`(1823-1840) |

**主 bot 没有策略管理 RPC**（只有 `backtester/btrpc` 有）。

关键消息：

```protobuf
message GetHistoricCandlesRequest {   // rpc.proto:714-726
  exchange, pair, asset_type, start, end,
  int64 time_interval,          // 纳秒
  bool ex_request, sync, use_db, fill_missing_with_trades, force
}
message Candle { string time; double low, high, open, close, volume; bool is_partial; }   // l.737-745
message SubmitOrderRequest { exchange, pair, side, order_type, amount, price, client_id, asset_type, margin_type }  // l.364-374
message GetTechnicalAnalysisRequest { algorithm_type, interval, period, fast_period, slow_period, ... }  // l.1325-1342
```

服务端：`StartRPCServer(ctx, engine)`（`engine/rpcserver.go:126`），TLS（证书 `<datadir>/tls`），默认 `localhost:9052`（`config/config.go:1601`）；网关 `startRPCRESTProxy`（l.191-241）默认 `localhost:9053`（l.1602）。认证：HTTP Basic（`authenticateClient` l.86-123）对 `remoteControl.username/password`，可选 per-request 交易所凭据走 gRPC metadata（`accounts.ParseCredentialsMetadata` l.114）、`verbose` metadata（l.119）、TOTP（l.1666+）。网关侧 `r.BasicAuth()`（l.245-246）。

### 7.2 通信 / 告警 ✅

`engine/communication_manager.go`：`SetupCommunicationManager(cfg *base.CommunicationsConfig)`(l.24)、`PushEvent(evt base.Event)`(l.87) → 转发到 `communications.Communications`。`base.Event{Type, Message string}`（`communications/base/base.go:17-20`）。

订单管理器已经在推 `"order"` 事件（`order_manager.go:28, 449, 469`）。LLM 层的告警可以直接复用 `iCommsManager.PushEvent`（接口定义 `engine/subsystem_types.go:50-52`）。

通道（`communications/`）：**Telegram**(`telegram/`)、**SMTP**(`smtpservice/`)、**Slack**(`slack/`)、**SMSGlobal**(`smsglobal/`)。配置 `communications` = `base.CommunicationsConfig{SlackConfig, SMSGlobalConfig, SMTPConfig, TelegramConfig}`（`communications/base/base.go:58-63`）。**没有通用 webhook**。

### 7.3 事件总线 / 发布订阅 ✅

**`dispatch` 包**（`dispatch/dispatch.go`、`mux.go`）是内部 pub/sub：

```go
dispatch.Start(workers, jobsLimit)                        // engine.go:322-326 启动
Mux.Subscribe(id uuid.UUID) (Pipe, error)
Mux.Publish(data any, ids ...uuid.UUID)
Mux.GetID()
Pipe.Channel() <-chan any
Pipe.Release()
```

现有订阅钩子：`ticker.SubscribeTicker`、`ticker.SubscribeToExchangeTickers`、`orderbook.SubscribeToExchangeOrderbooks`、`IBotExchange.SubscribeAccountBalances() (dispatch.Pipe, error)`（`interfaces.go:148`）。gRPC 流（`GetTickerStream` 等）就建在这些 pipe 上（`rpcserver.go:707-713`）。

**WebSocket DataHandler relay**：每个交易所都有 `Websocket.DataHandler`（`exchange/stream.Relay`）。所有解码后的 WS 消息（含 `kline.Item`、`ticker.Price`、`order.Detail`、`[]trade.Data`、`accounts.Change`、`[]futures.Position`）都汇入 `WebsocketRoutineManager.ProcessWSData`（`engine/websocketroutine_manager.go`，type switch 结尾 l.383-387）——**这是引擎内消费实时数据的最佳单点 hook**。注意 `[]futures.Position` 和 `accounts.SubAccounts` 目前只打日志带 TODO（l.354-382）。

**Event manager**（`engine/event_manager.go`，types l.44-77）：用户定义的条件事件（`Item: "PRICE"|"ORDERBOOK"`，条件 `>,>=,<,<=,==`，动作 `SMS`、`CONSOLE_PRINT`），通过 RPC `AddEvent/RemoveEvent/GetEvents` 管理。简单，但是"条件 → 通知"逻辑的先例。

**主引擎里没有通用的 plugin/hook 注册表**（唯一的插件机制是回测器的策略插件加载器）。

### 7.4 新增引擎子系统的标准模式 ✅

`Engine` 结构（`engine/engine.go:34-58`）把每个子系统作为字段持有（需要被 RPC/其它包访问的导出，如 `OrderManager *OrderManager`；否则私有，如 `dataHistoryManager *DataHistoryManager`）。

标准六步（以 DataHistoryManager 为范本）：

1. **Settings 标志**：`engine/engine_types.go` 的 `CoreSettings`（如 `EnableDataHistoryManager bool`，l.38）+ `main.go` 的 CLI flag（l.39）
2. **配置段**：`config/config_types.go`（如 `DataHistoryManager` 字段，JSON tag `dataHistoryManager`，l.103/139-145）+ `config/config.go` 里的默认值 + `config_example.json` 条目 + `config/versions/vN` 迁移
3. **flag/config 合并**：`validateSettings`（`engine.go:161-253`）里 `flagSet.WithBool("datahistorymanager", &b.Settings.EnableDataHistoryManager, b.Config.DataHistoryManager.Enabled)`（l.174）
4. **Setup + Start 块**（`engine.go:431-442`）：

   ```go
   if bot.Settings.EnableDataHistoryManager {
       if bot.dataHistoryManager == nil {
           if d, err := SetupDataHistoryManager(bot.ExchangeManager, bot.DatabaseManager, &bot.Config.DataHistoryManager); err != nil {
               gctlog.Errorf(gctlog.Global, "database history manager unable to setup: %s", err)
           } else {
               bot.dataHistoryManager = d
               if err := bot.dataHistoryManager.Start(runtimeCtx); err != nil { ... }
           }
       }
   }
   ```

   （OrderManager 版本额外传 `&bot.ServicesWG` 和 `runtimeCtx`，`engine.go:459-473`）
5. **子系统内部约定**：`started atomic.Bool` 配 `CompareAndSwap`、`shutdown chan struct{}`、错误 `ErrNilSubsystem`/`ErrSubSystemAlreadyStarted`/`ErrSubSystemNotStarted`、消息 `MsgSubSystemStarting/Started/ShuttingDown/Shutdown`（`engine/subsystem_types.go:18-40`）；依赖注入走窄接口 `iExchangeManager{GetExchanges(); GetExchangeByName(string)}` 和 `iCommsManager{PushEvent(base.Event)}`（l.44-52）；长驻循环注册到 `bot.ServicesWG`；名字常量如 `const OrderManagerName = "orders"`（`order_manager_types.go:15`）、`dataHistoryManagerName = "data_history_manager"`
6. **状态/RPC 集成**：加进 `Engine.GetSubsystemsStatus()` map（`engine/helpers.go:75-90`）和 `Engine.SetSubsystem`（l.116），让 `EnableSubsystem`/`DisableSubsystem` RPC 生效；`Engine.Stop`/`Shutdown` 里安排关闭顺序

**启动顺序**（`Engine.Start()`，`engine/engine.go:283-556`）：

```text
pprof Profiler (305) → DatabaseConnectionManager (311-320) → Dispatcher (322-326)
→ Connection manager (329-338) → NTP manager (340-353) → Exchange manager (940)
→ Communications manager → Currency storage/forex (399-412) → gRPC server
→ Portfolio manager → DataHistoryManager (431-442) → Withdraw manager (444-448)
→ Deposit address manager (450-457) → Order manager (459-473) → Sync manager (475-510)
→ Event manager (512-521) → Websocket routine manager (523-532)
→ Currency state manager (534-552)
```

> **LLM 子系统应该排在 Order manager 之后、Sync manager 附近**，因为它依赖 ExchangeManager、OrderManager、CommunicationsManager、DatabaseManager 都已就绪。

## 8. 编码规范约束（`docs/CODING_GUIDELINES.md`）

新增代码必须遵守的规则，与本方案直接相关的：

### 8.1 通用

- gofmt + Effective Go + Google Go Style
- **散文/注释/标识符用澳式/英式英语**（`normalise`、`serialise`）；外部 API 名保留原样；用 codespell 自定义词典 `contrib/spellcheck/codespell_custom_dictionary.txt`
- 所有 API 方法和公开类型都要 GoDoc 注释；解释 **why** 不是 what；不写自明的注释

### 8.2 安全

- **永不提交 API key、secret、client ID 或填好的 `config.json`**（l.23）
- 测试/示例里用占位符
- **永不在错误信息、RPC 响应、测试输出里记录或返回凭据**
- 漏洞按 SECURITY.md 私下报告

### 8.3 类型使用

- 裸 JSON 数字 → `float64`；带引号或歧义的数字 → `types.Number`（**绝不**用 `float64` + `json:",string"`）
- 时间戳 → `time.Time` 或 `types.Time`
- 字段名描述性；API 整数优先 `uint64`
- 有构造器就用（`currency.NewBTCUSDT()` 之类）
- 周期写全（`Volume24Hour` 不是 `Volume24H`）
- 嵌套对象用具名类型，**绝不用匿名 struct**
- 结构命名 `XRequest` / `XResponse`，作为指针使用
- 参数用指针 struct；用惯用 Go 类型（`time.Time`）在方法内部转换；**时间请求默认 UTC**

> **对本方案的影响**：LLM/Jev 的请求响应结构必须具名、用 `types.Number` 处理带引号的数字（Jev 的概率可能是字符串）、时间一律 UTC。

### 8.4 路径与错误

- 端点路径**内联在方法里**（不在别处定义常量）；字符串拼接（`"/api/v1/" + id`）不用 `fmt.Sprintf`；`strings.Builder` 只在基准测试证明后用；查询参数用 `common.EncodeURLValues(path, params)`
- 错误用 `fmt.Errorf("…: %w", err)` 包装
- 校验/解析路径需要稳定 `errors.Is` 的用包级哨兵错误 `var errX = errors.New(...)`
- 包装哨兵用 `%w`，不要替换
- 错误信息具体、有上下文
- **不许 panic**

### 8.5 配置迁移

- 迁移放 `config/versions/vN`，在 `config/versions/register.go` 注册
- 每次 schema 变更**加新版本**，绝不改写历史
- 迁移专用类型保持本地
- 评估已保存配置的升级行为
- 改默认值/示例**不会**迁移已有安装——要通过真实加载器测试
- 保留用户的显式选择
- 详细的 "Migration Guards" 规则（归一化匹配、通配符范围、显式 false/true/omitted/null、守卫两侧都要测、no-op 时也要推进版本号）

> **对本方案的影响**：新增 `llm` 配置段必须走 `config/versions/v18`，不能直接改 v17。

### 8.6 测试

- `go test ./... -race -count 1` 必须通过（l.179）
- 流程依赖结果时用 `require`，消息含 **"must"**；否则用 `assert`，消息含 **"should"**
- `f` 变体只在消息有格式化动词时用
- 每个哨兵错误路径都要有直接的 `assert.ErrorIs` 覆盖
- 优先完整覆盖，mock 外部依赖
- 区分 mock 验证和 live 验证
- 测试去重（对资产类型等循环）
- TestMain 不许发 API 请求（`sync.Once` bootstrap、`UpdatePairsOnce`）
- 期望值从**意图行为**推导，不是从当前代码
- 等价输入形式的回归矩阵

### 8.7 格式化 / lint / 文档

- 改完跑 `make gofumpt`；`golangci-lint run ./...`（或 `make lint`）；`make misc_checks`；完整流程 `make check`
- 所有 warning 都要解决；`//nolint:<linter>` 少用且必须带内联理由
- 文档改 `cmd/documentation` 下的模板然后 `go run .` 重新生成；生成文件 mode 0644；`make markdownlint`；模板改了要跑两遍生成器（第二遍应无 diff）；提交前 `git diff --check`
- `AGENTS.md`/`CLAUDE.md`/copilot-instructions 保持为指向本文件的薄指针

### 8.8 交易所实现规范（对 LLM 层间接相关）

- 端点按 API 文档顺序实现；按文档结构分组
- **默认导出交易所类型/函数/方法**（`func (e *Exchange) GetOrderBook(...)`），让仓库可当库用
- WS ticker handler 必须**先处理进共享 store 再转发 DataHandler**；绝不转发失败或空的批次

## 9. 能力总评

| 能力域 | 状态 | 新增工作量 |
| --- | --- | --- |
| K 线获取（REST，币安永续） | ✅ 完整 | 0 |
| K 线持久化 + 批量回填 | ✅ 完整 | 0（只需配置 + 建任务） |
| 技术指标（MACD/SMA/EMA/RSI/ATR/BBANDS） | ✅ 完整 | 0 |
| 实时 ticker / orderbook | ✅ 完整（REST 轮询） | 0 |
| 资金费率 | ✅ 完整 | 0 |
| 仓位跟踪 + 未实现盈亏 | ✅ 完整 | 0 |
| 订单提交（真实 / 纸面） | ✅ 完整 | 0 |
| 交易所侧限额校验 | ✅ 完整 | 0 |
| gRPC + REST 网关 | ✅ 完整 | 少量新增 RPC |
| 事件通知（Telegram 等） | ✅ 完整 | 0 |
| 内部 pub/sub（dispatch） | ✅ 完整 | 0 |
| 新增子系统模式 | ✅ 有清晰范例 | 0 |
| 配置加密 | ✅ 完整 | 0 |
| **运行时金额/频率/日损风控** | ❌ **代码在但未接线** | **必须自建** |
| **紧急停机 / halt 标志** | ❌ 缺 | 必须自建 |
| **逐市场交易授权** | ❌ 缺 | 必须自建 |
| **订单对账** | ⚠ 有基础方法 | 需自建流程 |
| **LLM 客户端（OpenAI 兼容）** | ❌ 缺 | 全新 |
| **Jev / System One 客户端** | ❌ 缺 | 全新 |
| **会话 / 策略版本 / 审批模型** | ❌ 缺 | 全新 |
| **决策审计链** | ❌ 缺 | 全新 |
| **主引擎策略框架** | ❌ 缺（回测器有） | 需决策：新建 or 复用回测器 |
| **模型成本预算 / 限流** | ❌ 缺 | 全新 |
| **Web UI** | ❌ 缺 | 需决策：CLI-first or Web |

**结论**：GoCryptoTrader 提供了**完整的交易基础设施**（数据、指标、订单、通知、接口），`hyperliquid-auto-trader` 提供了**完整的 AI 决策与安全边界设计**。两者互补性极强：本项目要新建的是"模型接入 + 决策治理 + 硬风控"这一层，而交易执行层几乎零改动。

## 10. 下一步

[03-总体架构设计](03-总体架构设计.md)。
