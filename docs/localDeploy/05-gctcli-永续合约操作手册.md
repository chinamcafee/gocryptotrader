# 05 gctcli 永续合约操作手册

本文是币安 USDT 本位永续合约的**全命令操作手册**。所有命令都在 `cmd/gctcli` 里实现，通过 gRPC 调用主程序。

## 0. 通用约定

### 0.1 前缀

后文所有 `gctcli` 都假设你已经配好别名：

```bash
alias gctcli='/Users/changzechuan/TradeCryptoCoinProjects/gocryptotrader/gctcli --rpcuser admin --rpcpassword ChangeMeNow_2026'
```

没配别名就在每条命令前加 `--rpcuser admin --rpcpassword <你的密码>`，且必须放在子命令**前面**。

### 0.2 三个参数的固定写法

| 参数 | 币安永续的取值 | 说明 |
| --- | --- | --- |
| `--exchange` | `Binance` | 大小写不敏感 |
| `--asset` | `usdtmarginedfutures` | 会被 `strings.ToLower` 处理，写 `USDTMarginedFutures` 也行 |
| `--pair` | `BTC-USDT` | gctcli 用 `--delimiter`（默认 `-`）解析 |

**再次强调**：gctcli 里用 `BTC-USDT`（连字符），配置文件里用 `BTC_USDT`（下划线），发给币安的是 `BTCUSDT`（无分隔符）。

### 0.3 查看任意命令的帮助

```bash
gctcli futures --help
gctcli futures setleverage --help
gctcli submitorder --help
```

---

## 1. 行情查询

### 1.1 最新价（Ticker）

```bash
# 单个
gctcli getticker --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 该资产下所有已启用交易对
gctcli gettickers --exchange Binance --asset usdtmarginedfutures
```

返回字段来自 `ticker.Price`（`exchanges/ticker/ticker_types.go:28-57`），永续合约特有的两个：

- `mark_price` —— 标记价格，用于计算未实现盈亏和强平
- `index_price` —— 指数价格
- `open_interest` / `open_interest_value` —— 持仓量

### 1.2 盘口（Orderbook）

```bash
gctcli orderbook getorderbook --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures
```

`orderbook.Book` 的关键字段（`exchanges/orderbook/orderbook_types.go:88-137`）：`bids` / `asks`（每档含 `price`、`amount`、`id`）、`last_updated`、`last_update_id`、`max_depth`。

盘口相关的进阶查询（用来估算滑点和冲击成本）：

```bash
# 从当前盘口价到指定深度的价格变动
gctcli orderbook buy nominal --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --percent 0.001

# 按名义金额查能成交到的价位
gctcli orderbook buy quote --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --amount 10000

# 按冲击比例查
gctcli orderbook buy impact --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --percent 0.001
```

对应 RPC：`GetOrderbookMovement`、`GetOrderbookAmountByNominal`、`GetOrderbookAmountByImpact`（`gctrpc/rpc.proto:1956-1962`）。

### 1.3 实时流

```bash
# 单个交易对的 ticker 流（长连接，Ctrl+C 退出）
gctcli gettickerstream --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 整个交易所的 ticker 流
gctcli getexchangetickerstream --exchange Binance

# 盘口流
gctcli orderbook getorderbookstream --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures
gctcli orderbook getexchangeorderbookstream --exchange Binance
```

底层是 `dispatch` 包发布的管道（`ticker.SubscribeTicker`、`orderbook.SubscribeToExchangeOrderbooks`）。**因为币安合约没有 WebSocket，这些流的数据源是 SyncManager 的 REST 轮询**，更新频率是轮询周期，不是逐笔推送。

### 1.4 资金费率（永续核心）

```bash
# 最新一期 + 预测下一期
gctcli futures getlatestfundingrate --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 历史资金费率
gctcli futures getfundingrates --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT \
  --startdate "2026-01-01 00:00:00" --enddate "2026-02-01 00:00:00"

# 历史利率/资金费（另一个端点）
gctcli getmarginrateshistory --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT
```

币安合约的资金费率结算周期是 4h 或 8h（`binance_wrapper.go:135-138` 的 `SupportedFundingRateFrequencies`）。

返回结构 `fundingrate.LatestRateResponse`（`exchanges/fundingrate/fundingrate_types.go:62-70`）：`latest_rate`、`predicted_upcoming_rate`、`time_of_next_rate`、`time_checked`。

### 1.5 持仓量

```bash
gctcli futures getopeninterest --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT
```

### 1.6 合约详情

```bash
# gctcli 未暴露该接口，直接查币安（filterType LOT_SIZE=数量步长, PRICE_FILTER=价格精度, MIN_NOTIONAL=最小名义额）
curl -s "https://fapi.binance.com/fapi/v1/exchangeInfo?symbol=BTCUSDT" | jq '.symbols[0].filters'
```

对应 `GetFuturesContractDetails`（`binance_wrapper.go:2790`），返回每个合约的 tick size、最小数量、合约乘数等。**下单前确认精度用这个。**

---

## 2. K 线与技术指标

### 2.1 简单 K 线

```bash
gctcli gethistoriccandles --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --rangesize 10
```

| flag | 默认 | 说明 |
| --- | --- | --- |
| `--granularity` | `86400` | **秒**。支持的取值见下表 |
| `--rangesize` | `10` | 往回数几个周期 |

支持的 granularity（`cmd/gctcli/commands.go:3132-3134` 的帮助文本原文）：

```text
15, 60(1min), 180(3min), 300(5min), 600(10min), 900(15min) 1800(30min),
3600(1h), 7200(2h), 14400(4h), 21600(6h), 28800(8h), 43200(12h),
86400(1d), 259200(3d) 604800(1w), 1209600(2w), 1296000(15d),
2592000(1M), 31536000(1Y)
```

币安实际启用的间隔（`binance_wrapper.go:150-166`）：`1m, 3m, 5m, 15m, 30m, 1h, 2h, 4h, 6h, 8h, 12h, 1d, 3d, 1w, 1M`。请求别的间隔会由 `kline.ExchangeIntervals.Construct` 尝试用更小的间隔合成（`exchanges/kline/kline.go:812-874`），合成不了就报错。

**单次请求最多 1000 根**（`GlobalResultLimit: 1000`，`binance_wrapper.go:167`）。

### 2.2 扩展 K 线（跨大范围）

```bash
gctcli gethistoriccandlesextended --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --interval 3600 \
  --start "2026-01-01 00:00:00" --end "2026-02-01 00:00:00"
```

| flag | 默认 | 说明 |
| --- | --- | --- |
| `--interval` | — | 秒，同上表 |
| `--start` | 一个月前 | `time.DateTime` 格式：`2006-01-02 15:04:05` |
| `--end` | 现在 | 同上 |
| `--sync` | `false` | 拉完写入数据库 |
| `--force` | `false` | 覆盖已有数据 |
| `--db` | `false` | 直接从数据库读，不打交易所 API |
| `--fill` / `--fillmissingdatawithtrades` | `false` | 用已存的成交数据补缺口 |

`Extended` 版本会自动把大区间**拆成多个请求**（`kline.CalculateCandleDateRanges`，`exchanges/kline/kline.go:622`），每批 1000 根，然后合并、去重、排序、补零填充缺口（`Request.ProcessResponse`，`exchanges/kline/request.go:146`）。缺失区间会在日志里打 `DataSummary` 告警。

布尔 flag 要用等号：`--sync=true`。

`--db=true` 需要先跑过 `--sync=true` 或者用 DataHistoryManager 灌过数据，见 [06-数据库与历史K线](06-数据库与历史K线.md)。

### 2.3 最后一根 K 线是"未收盘"的

返回结果里最后一根如果 `is_partial: true`，说明它的时间窗口还没走完（`ValidationIssues` 被标成 `"Partial Candle"`，常量在 `exchanges/kline/request.go:25`）。**算指标时要不要包含它，取决于你的策略**——包含会让 MACD/均线随实时价格抖动。

### 2.4 技术指标

11 个子命令，全部走 RPC `GetTechnicalAnalysis`（`engine/rpcserver.go:4612-4767`），服务端先拉 K 线再算。

#### MACD

```bash
gctcli technicalanalysis macd --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 \
  --fastperiod 12 --slowperiod 26 --period 9
```

- `--fastperiod`：快线 EMA 周期，默认 12
- `--slowperiod`：慢线 EMA 周期，默认 26
- `--period`：**信号线**周期，默认 9

返回三组序列，对应 `kline.MACD` 结构（`exchanges/kline/technical_analysis.go:214-218`）：

```go
type MACD struct {
    Results    []float64   // DIF / MACD 线
    SignalVals []float64   // DEA / 信号线
    Histogram  []float64   // 柱状图 = Results - SignalVals
}
```

约束：`fast < slow`，两者都 > 0，且数据量必须 `>= slow + signal - 2`，否则报 `errNotEnoughData`（`technical_analysis.go:251`）。26+9-2 = 33，所以**至少要 33 根 K 线**。默认 `--start` 是一个月前，1 小时线足够。

#### 均线

```bash
# 简单移动平均（SMA），20 周期
gctcli technicalanalysis sma --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 20

# 指数移动平均（EMA），20 周期
gctcli technicalanalysis ema --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 20
```

#### 其它

```bash
# RSI（14 周期）
gctcli technicalanalysis rsi --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 14

# 布林带
gctcli technicalanalysis bbands --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 20 \
  --stddevup 2 --stddevdown 2 --movingaveragetype SMA

# ATR（波动率）
gctcli technicalanalysis atr --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 14

# VWAP / TWAP
gctcli technicalanalysis vwap --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --granularity 3600
gctcli technicalanalysis twap --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --granularity 3600

# OBV（量能）
gctcli technicalanalysis obv --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --granularity 3600

# MFI（资金流量）
gctcli technicalanalysis mfi --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --granularity 3600 --period 14

# 相关系数（比如 BTC 和 ETH 的走势相关性）
gctcli technicalanalysis coco --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --granularity 3600 --period 30 \
  --comparisonexchange Binance --comparisonpair ETH-USDT --comparisonasset usdtmarginedfutures
```

服务端支持的 `algorithmType` 全集（`engine/rpcserver.go:4638-4763`）：`TWAP, VWAP, ATR, BBANDS, COCO, SMA, EMA, MACD, MFI, OBV, RSI`。

底层实现是 `exchanges/kline/technical_analysis.go`，方法挂在 `*kline.Item` 上（便利版）和 `*kline.OHLC` 上（核心版），数学计算委托给外部库 `github.com/thrasher-corp/gct-ta/indicators`（`go.mod:21`）。完整清单：

| 方法（`*kline.Item` 版） | 签名 | 位置 |
| --- | --- | --- |
| `GetAverageTrueRange` | `(period int64) ([]float64, error)` | :49 |
| `GetBollingerBands` | `(period int64, nbDevUp, nbDevDown float64, m indicators.MaType) (*Bollinger, error)` | :78 |
| `GetCorrelationCoefficient` | `(other *Item, period int64) ([]float64, error)` | :121 |
| `GetSimpleMovingAverageOnClose` | `(period int64) ([]float64, error)` | :163 |
| `GetExponentialMovingAverageOnClose` | `(period int64) ([]float64, error)` | :189 |
| `GetMovingAverageConvergenceDivergenceOnClose` | `(fast, slow, signal int64) (*MACD, error)` | :223 |
| `GetMoneyFlowIndex` | `(period int64) ([]float64, error)` | :268 |
| `GetOnBalanceVolume` | `() ([]float64, error)` | :311 |
| `GetRelativeStrengthIndexOnClose` | `(period int64) ([]float64, error)` | :331 |
| `GetTWAP` | `() (float64, error)` | `weighted_price.go:38` |
| `GetVWAPs` | `() ([]float64, error)` | `weighted_price.go:98` |

这些方法**无状态、无缓存**，每次调用都重算。返回值是跟输入 K 线序列**等长对齐**的 `[]float64`。

### 2.5 成交数据

```bash
gctcli trade getrecent --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --limit 50
gctcli gethistoriccandles --help    # 对比
gctcli trade getsaved --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures
```

`getsavedtrades` 读数据库，需要 `features.enabled.saveTradeData: true` 且数据库开启。

---

## 3. 账户与仓位

### 3.1 保证金 / 余额

```bash
# 账户余额 / 可用保证金（按资产）—— 币安 USDT-M 用这个
gctcli getaccountbalances --exchange Binance --asset usdtmarginedfutures

# 强制刷新后再读
gctcli updateaccountbalances --exchange Binance --asset usdtmarginedfutures
```

> ⚠ **实测：`gctcli futures getcollateral` 在币安上不可用**，返回
> `rpc error: code = Unknown desc = unsupported wrapper function Get Collateral for exchange Binance`。
> 加 `--calculateoffline` 也一样。原因：RPC `GetCollateral`（`engine/rpcserver.go:4394-4471`）
> 依赖 `ScaleCollateral` / `CalculateTotalCollateral`，而币安 wrapper **没有实现**这两个方法
> （走 `exchanges/exchange.go:1649` / `:1655` 的基类默认，返回 `ErrNotYetImplemented`）。
> **查合约可用资金一律用 `getaccountbalances`。**

`GetCollateral`（RPC，`engine/rpcserver.go:4394-4471`）用 `GetCachedCurrencyBalances` 算，返回 `Total`、`Free`、`Locked`、`UnrealisedPNL`。

### 3.2 持仓模式（单向 / 双向）

```bash
# 查询当前模式
gctcli futures getcollateralmode --exchange Binance --asset usdtmarginedfutures

# 设置成单向持仓
gctcli futures setcollateralmode --exchange Binance --asset usdtmarginedfutures --collateralmode one_way
```

**本项目必须用单向持仓（One-way）**。原因：币安 wrapper 的 `SubmitOrder` 在 `asset.USDTMarginedFutures` 分支里**只映射 `order.Buy` → `"BUY"` 和 `order.Sell` → `"SELL"`，完全不传 `positionSide` 字段**（`binance_wrapper.go:896-940`）。如果币安账户开了双向持仓（Hedge Mode），下单会因为缺 `positionSide` 被拒。

对应端点 `POST/GET /fapi/v1/multiAssetsMargin`（`binance_ufutures.go:1028-1036`）。**只有 `asset.USDTMarginedFutures` 支持**这两个方法（`binance_wrapper.go:2188-2211`）。

### 3.3 杠杆

```bash
# 查询
gctcli futures getleverage --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 设置（5 倍，逐仓）
gctcli futures setleverage --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT \
  --leverage 5 --margintype isolated
```

`setleverage`（别名 `sl`）的 flag（`cmd/gctcli/futures.go:272`）：

| flag | 必填 | 说明 |
| --- | --- | --- |
| `--exchange` / `-e` | ✅ | |
| `--asset` / `-a` | ✅ | |
| `--pair` / `-p` | ✅ | |
| `--leverage` / `-l` | ✅ | 倍数 |
| `--margintype` | ❌ | `isolated`、`multi` 或 `cross` |
| `--orderside` | ❌ | 双向持仓时才需要 |

对应 `POST /fapi/v1/leverage`（`binance_ufutures.go:761`）。

**杠杆是账户侧的持久设置，设一次就生效**，不用每次下单前设。而且下单时**不能**带杠杆——`order.Submit.Leverage` 必须是 0 或 1，否则 `order.ErrSubmitLeverageNotSupported`（`binance_wrapper.go:793-795`）。

### 3.4 保证金模式（逐仓 / 全仓）

```bash
gctcli futures setmargintype --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT \
  --margintype isolated
```

映射（`marginTypeToString`，`binance_wrapper.go:2285-2293`）：`margin.Isolated → "ISOLATED"`，`margin.Multi → "CROSSED"`。对应 `POST /fapi/v1/marginType`（`binance_ufutures.go:777`）。

**注意**：币安在有持仓时改保证金模式会报错，需要先平仓。

### 3.5 调整逐仓保证金

```bash
gctcli futures changepositionmargin --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT \
  --margintype isolated --originalallocatedmargin 100 --newallocatedmargin 200 --marginside long
```

**只支持逐仓**。全仓（`margin.Multi`）会返回 `ErrMarginTypeUnsupported`（`binance_wrapper.go:2239-2282`）。

### 3.6 仓位查询

```bash
# 交易所侧的仓位汇总
gctcli futures getfuturespositionsummary --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 交易所侧该仓位相关的订单
gctcli futures getfuturepositionorders --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 本地跟踪的单个仓位（需要 orderManager.activelyTrackFuturesPositions=true）
gctcli futures getmanagedposition --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 本地跟踪的全部仓位
gctcli futures getallmanagedpositions
```

**两类仓位的区别很重要**：

| 命令 | 数据源 | 说明 |
| --- | --- | --- |
| `getfuturespositionsummary` | **币安 API 实时查** | 权威，但有请求延迟和权重消耗 |
| `getmanagedposition` / `getallmanagedpositions` | **本地 PositionController** | 由订单管理器根据成交持续更新，含未实现盈亏和资金费累计 |

本地跟踪的实现在 `exchanges/futures/futures.go`：`SetupPositionController()`（l.22）、`TrackNewOrder(*order.Detail)`（l.31）、`GetOpenPosition`（l.155）、`GetAllOpenPositions`（l.182）、`UpdateOpenPositionUnrealisedPNL`（l.207）、`TrackFundingDetails`（l.117）。订单管理器每 10 秒跑一轮同步（`orderManagerInterval = 10s`，`engine/order_manager_types.go:28`；`processOrders` l.651；仓位/资金费/未实现盈亏 l.735-767、777-834）。

订单管理器暴露的方法（`engine/order_manager.go`）：`GetFuturesPositionsForExchange`（l.260）、`GetOpenFuturesPosition`（l.276）、`GetAllOpenFuturesPositions`（l.294）、`ClearFuturesTracking`（l.309）、`UpdateOpenPositionUnrealisedPNL`（l.326）。

**币安在本仓库里未实现的合约接口**（会返回 `common.ErrFunctionNotSupported` / `ErrNotYetImplemented`，默认实现在 `exchanges/exchange.go`）：

| 接口 | 默认实现位置 |
| --- | --- |
| `GetFuturesPositions` | `exchange.go:1682` |
| `GetPositionSummary` | `exchange.go:1476` |
| `ScaleCollateral` | `exchange.go:1649` |
| `CalculateTotalCollateral` | `exchange.go:1655` |
| `GetMarginRatesHistory` | 基类默认 |
| `GetCollateralCurrencyForContract` | 基类默认 |

**已实现**的：`GetOpenInterest`（`binance_wrapper.go:2901`）、`GetHistoricalFundingRates`（l.2014）、`GetLatestFundingRates`（l.1876）、`IsPerpetualFutureCurrency`（l.2177）、`GetFuturesPositionSummary`（l.2297）、`GetFuturesPositionOrders`（l.2577）、`SetCollateralMode`/`GetCollateralMode`（l.2188/2199）、`SetLeverage`/`GetLeverage`（l.2745/2759）、`SetMarginType`（l.2214）、`ChangePositionMargin`（l.2239）、`GetFuturesContractDetails`（l.2790）。

---

## 4. 下单

### 4.1 限价单

```bash
gctcli submitorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --side BUY --type LIMIT --amount 0.001 --price 60000
```

### 4.2 市价单

```bash
gctcli submitorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --side BUY --type MARKET --amount 0.001
```

`submitorder` 的完整 flag（`cmd/gctcli/commands.go:1298-1345`）：

| flag | 必填 | 说明 |
| --- | --- | --- |
| `--exchange` | ✅ | |
| `--pair` | ✅ | |
| `--asset` | ✅ | |
| `--side` | ✅ | `BUY` 或 `SELL` |
| `--type` | ✅ | `MARKET` 或 `LIMIT`（服务端还支持更多，见下） |
| `--amount` | ✅ | 数量（币安合约是**合约张数/币数**，不是名义金额） |
| `--price` | LIMIT 时必填 | |
| `--client_id` | ❌ | 自定义订单 ID，方便自己追踪 |
| `--margintype` | ❌ | |

### 4.3 订单类型映射

`order.Type` → 币安字符串（`binance_wrapper.go:896-940`）：

| gocryptotrader | 币安 | 用途 |
| --- | --- | --- |
| `order.Limit` | `LIMIT` | 限价 |
| `order.Market` | `MARKET` | 市价 |
| `order.Stop` | `STOP` | 止损限价 |
| `order.TakeProfit` | `TAKE_PROFIT` | 止盈限价 |
| `order.StopMarket` | `STOP_MARKET` | 止损市价（**做保护性止损用这个**） |
| `order.TakeProfitMarket` | `TAKE_PROFIT_MARKET` | 止盈市价 |
| `order.TrailingStop` | `TRAILING_STOP_MARKET` | 跟踪止损 |

常量定义在 `exchanges/order/order_types.go:377-402`。

**`TimeInForce` 被硬编码成 `"GTC"`**（`binance_wrapper.go`），不能通过 `submitorder` 指定。需要 IOC/FOK 得走代码层。

### 4.4 只减仓（Reduce-Only）

gctcli 的 `submitorder` **没有** `--reduceonly` flag，REST 网关的 `SubmitOrderRequest` **也没有** `reduce_only` 字段。但 `order.Submit` 结构体有 `ReduceOnly bool`（`exchanges/order/order_types.go:46-117`），且 wrapper 会把它传给币安（`binance_wrapper.go:934`）。**所以下 reduce-only 单只能走 Go 代码层。**

REST 下单的正确请求体（`pair` 是对象）：

```bash
curl -sk -u admin:'ChangeMeNow_2026' -X POST https://127.0.0.1:9053/v1/submitorder \
  -H 'Content-Type: application/json' \
  -d '{
    "exchange": "Binance",
    "pair": {"delimiter":"-","base":"BTC","quote":"USDT"},
    "assetType": "usdtmarginedfutures",
    "side": "SELL",
    "orderType": "STOP_MARKET",
    "amount": 0.001,
    "price": 55000
  }' | jq .
```

（`SubmitOrderRequest` 的字段见 `gctrpc/rpc.proto:364-374`：`exchange, pair, side, order_type, amount, price, client_id, asset_type, margin_type`。**实测确认：`SubmitOrderRequest`（`gctrpc/rpc.proto`）只有 9 个字段 —— `exchange, pair, side, order_type, amount, price, client_id, asset_type, margin_type`，没有 `reduce_only`。所以 reduce-only 单只能走 Go 代码层（直接构造 `order.Submit{ReduceOnly: true}` 交给 `OrderManager.Submit`）。**）

### 4.5 下单前必做的三件事

1. **设杠杆**（一次性）：`gctcli futures setleverage ...`
2. **确认单向持仓**：`gctcli futures getcollateralmode ...`
3. **确认精度**：`# gctcli 未暴露该接口，直接查币安（filterType LOT_SIZE=数量步长, PRICE_FILTER=价格精度, MIN_NOTIONAL=最小名义额）
curl -s "https://fapi.binance.com/fapi/v1/exchangeInfo?symbol=BTCUSDT" | jq '.symbols[0].filters'`，看 BTC-USDT 的最小数量步长和价格 tick。数量不合规会被 `CheckOrderExecutionLimits` 拦下（`engine/order_manager.go:478-525` 的 `Submit` 流程里）。

### 4.6 下单链路

`OrderManager.Submit`（`engine/order_manager.go:478`）的完整流程：

```text
解析交易所
  → m.validate(newOrder)                     订单管理器自身的限额/白名单校验
  → exch.CheckOrderExecutionLimits(...)      交易所侧最小/最大数量、价格精度
  → exch.CanTradePair(pair, asset)           币种状态是否被 halt
  → exch.SubmitOrder(ctx, newOrder)          真正打币安 API
  → processSubmittedOrder(...)               存 order.Detail + TrackNewOrder 更新本地仓位
```

成功后会往通信管理器推 `base.Event{Type: "order"}`（l.449-452），开了 Telegram/SMTP 就会收到通知。

### 4.7 盘口冲击模拟（`simulateorder` 的真实用途）

> ⚠ **实测更正：`simulateorder` 不能用于永续合约，也不是纸面下单。**
> `SimulateOrder` 的 handler（`engine/rpcserver.go`）里**硬编码了 `asset.Spot`** 两次
> （`checkParams(r.Exchange, exch, asset.Spot, p)` 和 `GetCachedOrderbook(p, asset.Spot)`），
> 而 `SimulateOrderRequest` proto 里根本没有 `asset_type` 字段。关掉现货后调用会直接报：
> `rpc error: code = Unknown desc = spot asset type not enabled`。
> 而且它调的是 `orderbook.SimulateOrder(amount, buy)`，返回的是**盘口成交档位和最高/最低价**，
> 本质是**盘口冲击模拟器**（等价于 `orderbook buy/sell`），从来不调 `SubmitFakeOrder`。

**正确的盘口冲击/滑点估算命令**（`orderbook` 命令组下，支持 `--asset`）：

```bash
# 按名义金额估算买入的成交均价与滑点
gctcli orderbook buy quote --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --amount 1000

# 按冲击比例估算
gctcli orderbook buy impact --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --percent 0.001

# 估算打到某个价位需要多少量
gctcli orderbook whalebomb --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --side BUY --price 90000
```

对应 RPC `GetOrderbookAmountByNominal` / `GetOrderbookAmountByImpact` / `WhaleBomb`（`gctrpc/rpc.proto:1956-1962`、`1656`）。

> **真正的纸面下单**（`OrderManager.SubmitFakeOrder`，`engine/order_manager.go:529`）**没有 RPC / gctcli 入口**，
> 只能从 Go 代码里调（回测器就是这么用的，见 [07-测试网与模拟交易](07-测试网与模拟交易.md) §3）。

### 4.8 查询与撤单

```bash
# 全部订单
gctcli getorders --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 单个订单
gctcli getorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --orderid 123456789

# 本地跟踪的订单
gctcli getmanagedorders --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 撤单
gctcli cancelorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --orderid 123456789 --side BUY --type LIMIT --amount 0.001 --price 60000

# 批量撤单
gctcli cancelbatchorders --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --ordersid 123456789,123456790

# 撤掉该交易所全部挂单
gctcli cancelallorders --exchange Binance --asset usdtmarginedfutures
```

**`modifyorder` 在币安上不可用**——`ModifyOrder` 直接返回 `common.ErrFunctionNotSupported`（`binance_wrapper.go:955-957`）。要改单只能撤了重下。

`order.Detail` 的关键字段（`exchanges/order/order_types.go:221-257`）：`InternalOrderID uuid.UUID`（本地 ID）、`ExchangeOrderID`、`Status`、`ExecutedAmount`、`RemainingAmount`、`AverageExecutedPrice`、`Fee`/`FeeAsset`、`MarginType`、`Trades []TradeHistory`。

订单状态常量（`order_types.go:343-366`）：`New, Active, PartiallyFilled, Filled, Cancelled, PendingCancel, InsufficientBalance, Rejected, Expired, Open, Closed, Liquidated` 等。

---

## 5. 交易对与运行时管理

### 5.1 动态增删交易对

```bash
# 查当前配置的交易对
gctcli pair get --exchange Binance --asset usdtmarginedfutures

# 启用一个新交易对（不用改配置文件重启）
gctcli pair enable --exchange Binance --asset usdtmarginedfutures --pairs SOL-USDT

# 停用
gctcli pair disable --exchange Binance --asset usdtmarginedfutures --pairs SOL-USDT

# 刷新交易所支持的全部交易对
gctcli pair update --exchange Binance

# 查资产类型
gctcli pair getassets --exchange Binance
```

`SetExchangePair` 会写回配置（除非 dry-run）。**"追踪任意一个永续合约"这个需求就靠这条命令实现**——不用重启就能加新合约。

### 5.2 子系统控制

```bash
gctcli getsubsystems
gctcli enablesubsystem --subsystem data_history_manager
gctcli disablesubsystem --subsystem sync_manager
```

子系统名对应 `Engine.GetSubsystemsStatus()` 返回的 map（`engine/helpers.go:75-90`），例如 `orders`、`data_history_manager`、`sync_manager`、`portfolio_manager`、`database_connection`、`event_manager`、`websocket_routine_manager`、`currency_state_manager`。

### 5.3 日志级别

```bash
gctcli getloggerdetails --logger EXCHANGE
gctcli setloggerdetails --logger EXCHANGE --level DEBUG
```

### 5.4 配置读写

```bash
gctcli getconfig
```

**注意**：`GetConfig` 会返回完整配置。确认你的 RPC 密码够强，且 9052/9053 只绑 `127.0.0.1`。

### 5.5 WebSocket 管理（币安永续用不上）

```bash
gctcli websocket getinfo --exchange Binance
gctcli websocket disable --exchange Binance
gctcli websocket getsubs --exchange Binance
gctcli websocket setproxy --exchange Binance --proxy socks5://127.0.0.1:7890
```

币安合约的 WS 资产支持被代码显式关闭，这些命令对永续没有实际效果。

---

## 6. 完整操作演练：从零开一个 BTC 永续多单

```bash
# 1. 确认服务和交易所在线
gctcli getinfo
gctcli getexchangeinfo --exchange Binance

# 2. 看当前价格
gctcli getticker --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 3. 看盘口，确认可成交价
gctcli orderbook getorderbook --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 4. 看资金费率（做多要付还是收）
gctcli futures getlatestfundingrate --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 5. 看指标：1 小时 MACD 和 20 期 EMA
gctcli technicalanalysis macd --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --fastperiod 12 --slowperiod 26 --period 9
gctcli technicalanalysis ema --exchange Binance --pair BTC-USDT \
  --asset usdtmarginedfutures --granularity 3600 --period 20

# 6. 确认账户有钱
gctcli getaccountbalances --exchange Binance --asset usdtmarginedfutures

# 7. 确认单向持仓模式
gctcli futures getcollateralmode --exchange Binance --asset usdtmarginedfutures
# 如果是双向：gctcli futures setcollateralmode --exchange Binance --asset usdtmarginedfutures --collateralmode one_way

# 8. 设逐仓 + 3 倍杠杆（一次性）
gctcli futures setmargintype --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT --margintype isolated
gctcli futures setleverage --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT --leverage 3 --margintype isolated

# 9. 确认合约精度
# gctcli 未暴露该接口，直接查币安（filterType LOT_SIZE=数量步长, PRICE_FILTER=价格精度, MIN_NOTIONAL=最小名义额）
curl -s "https://fapi.binance.com/fapi/v1/exchangeInfo?symbol=BTCUSDT" | jq '.symbols[0].filters'

# 10. 估算滑点（simulateorder 硬编码现货、永续不可用，改用 orderbook buy）
gctcli orderbook buy quote --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures --amount 82

# 11. 真实下限价单
gctcli submitorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --side BUY --type LIMIT --amount 0.001 --price 60000 --client_id dev-test-001

# 12. 查订单状态
gctcli getorders --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures

# 13. 成交后查仓位
gctcli futures getmanagedposition --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT
gctcli futures getfuturespositionsummary --exchange Binance --asset usdtmarginedfutures --pair BTC-USDT

# 14. 平仓（反向市价单）
gctcli submitorder --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures \
  --side SELL --type MARKET --amount 0.001

# 15. 确认已平
gctcli futures getallmanagedpositions
```

## 7. 下一步

- 要存历史 K 线做回测/分析：[06-数据库与历史K线](06-数据库与历史K线.md)
- 要用测试网而不是真钱：[07-测试网与模拟交易](07-测试网与模拟交易.md)
- 报错了：[08-故障排查](08-故障排查.md)
