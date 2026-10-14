# GoCryptoTrader 本地部署指南（币安 USDT 本位永续合约）

本目录是一套**面向本地 dev 环境**的完整部署文档，目标只有一个：把当前仓库的源码在本机跑起来，并且能够**查询行情、拉取 K 线、计算指标、设置杠杆、下真实永续合约单**。

现货、币本位合约、杠杆、期权、充值提现等其它交易类型全部**不在本指南范围内**，配置里会显式关掉。

## 阅读顺序

| 序号 | 文档 | 内容 | 什么时候读 |
| --- | --- | --- | --- |
| 01 | [环境准备](01-环境准备.md) | Go 1.27、构建工具、网络与代理、系统时钟 | 第一次动手前 |
| 02 | [构建与安装](02-构建与安装.md) | 编译主程序与 gctcli、可选 build tag、验证产物 | 拉完代码 |
| 03 | [配置文件详解](03-配置文件详解.md) | `config.json` 逐字段说明 + 币安 API Key 申请步骤 | 配置阶段（最重要） |
| 04 | [首次启动与验证](04-首次启动与验证.md) | 启动命令、日志判读、gRPC 证书、子系统状态自检 | 第一次跑起来 |
| 05 | [gctcli 永续合约操作手册](05-gctcli-永续合约操作手册.md) | 每一条 gctcli 命令：行情 / K 线 / 指标 / 杠杆 / 仓位 / 下单 | 日常操作 |
| 06 | [数据库与历史 K 线](06-数据库与历史K线.md) | SQLite 初始化、迁移、种子数据、DataHistoryManager 任务 | 需要历史数据落库 |
| 07 | [测试网与模拟交易](07-测试网与模拟交易.md) | 币安合约测试网切换、`SimulateOrder`、`SubmitFakeOrder`、dryrun 语义 | 上真实资金前 |
| 08 | [故障排查](08-故障排查.md) | 20 个高频坑位与对应解法 | 出问题时 |

## 五分钟速览（已熟悉本项目的人看这里）

```bash
# 1. 构建（仓库根目录，Go 1.27）
go build
go build ./cmd/gctcli

# 2. 准备配置
mkdir -p ~/.gocryptotrader
cp docs/localDeploy/config.binance-usdtm-futures.json ~/.gocryptotrader/config.json

# 3. 填入币安合约 API Key（见 03-配置文件详解.md）
$EDITOR ~/.gocryptotrader/config.json

# 4. 启动（关掉 portfolio manager —— 它只服务现货，纯合约部署会每分钟刷 ERROR）
./gocryptotrader -portfoliomanager=false

# 5. 另开一个终端验证
./gctcli --rpcuser admin --rpcpassword 'ChangeMeNow_2026' getinfo
./gctcli --rpcuser admin --rpcpassword 'ChangeMeNow_2026' getticker \
  --exchange Binance --pair BTC-USDT --asset usdtmarginedfutures
```

## 本目录附带的文件

- [`config.binance-usdtm-futures.json`](config.binance-usdtm-futures.json) —— 可直接使用的完整配置模板，已按"仅币安 USDT 本位永续"裁剪。复制到 `~/.gocryptotrader/config.json` 后只需改三处：API Key、API Secret、gRPC 密码。

## 关键事实速查（源码实证）

下表是本指南所有步骤的依据，均来自当前仓库源码，不是上游文档的转述。

| 事项 | 实际值 | 源码位置 |
| --- | --- | --- |
| 主程序入口 | 仓库根目录 `main.go`（**不是** `cmd/gocryptotrader/`） | `main.go` |
| Go 版本要求 | `go 1.27` | `go.mod:3` |
| 模块名 | `github.com/thrasher-corp/gocryptotrader` | `go.mod:1` |
| 配置版本号 | `17` | `config_example.json:3` |
| 默认配置目录（macOS） | `~/.gocryptotrader` | `common/common.go:366-385` |
| 配置文件名 | `config.json`（未加密）/ `config.dat`（加密） | `config/config_types.go:21` |
| 永续合约资产标识 | `usdtmarginedfutures`（**不是** `usdt_swap`） | `exchanges/asset/asset.go:26-75` |
| 配置内交易对格式 | `BTC_USDT`（下划线） | `exchanges/binance/binance_wrapper.go:38-59` |
| 交易所请求格式 | `BTCUSDT`（无分隔符） | 同上 |
| gctcli 输入格式 | `BTC-USDT`（`--delimiter` 默认 `-`） | `cmd/gctcli/main.go:132-210` |
| 合约 REST 端点 | `https://fapi.binance.com` | `exchanges/binance/binance.go:36` |
| 合约测试网端点 | `https://testnet.binancefuture.com` | `exchanges/binance/binance.go:41` |
| gRPC 监听 | `localhost:9052`（TLS + Basic Auth） | `config/config.go:1601` |
| gRPC REST 网关 | `localhost:9053`（HTTPS + Basic Auth） | `config/config.go:1602` |
| K 线单次上限 | 1000 根 | `exchanges/binance/binance_wrapper.go:167` |
| 合约权重限额 | 2400 weight/min，下单 300 次/10s | `exchanges/binance/ratelimit.go:23-26` |
| 币安合约 WebSocket | **不支持**（仅现货有 WS） | `exchanges/binance/binance_wrapper.go:75-79` |

## 三条必须先知道的红线

1. **不会自动生成配置**。首次运行如果找不到 `config.json`，程序直接报错退出，报错信息是 `config.json file not found in %s, please follow README.md in root dir for config generation`（`config/config.go:1390`）。必须自己复制。
2. **`useSandbox` 对币安无效**。全仓库只有 Gemini 读这个字段（`exchanges/gemini/gemini_wrapper.go:130`）。要上测试网必须改 `api.urlEndpoints.RestUSDTMarginedFuturesURL`，详见 [07-测试网与模拟交易](07-测试网与模拟交易.md)。
3. **下单时不能带杠杆**。`order.Submit.Leverage` 必须是 0 或 1，否则报 `order.ErrSubmitLeverageNotSupported`（`exchanges/binance/binance_wrapper.go:793-795`）。杠杆要用 `gctcli futures setleverage` 单独设置一次。

## 实测验证记录（2026-10-08，macOS arm64 / Go 1.27.0）

本指南的命令都在真实环境跑过一遍。下面是**实测结果**，包括几处与直觉不符、必须记住的结论。

### 环境与网络

| 项 | 实测值 |
| --- | --- |
| Go | `go1.27.0 darwin/arm64`（首次运行自动下载 toolchain） |
| 构建耗时 | 主程序 73s（首次，含依赖下载），gctcli 8.5s |
| 产物体积 | `gocryptotrader` 49 MB，`gctcli` 21 MB |
| 币安连通性 | **`fapi.binance.com` 直连可达，HTTP 200，1.4s**，无需代理 |
| 时钟偏差 | `-1058 ms`（本地慢 1 秒），在 `recvWindow=5000ms` 内可用 |
| 数据库迁移 | 23 个迁移全部 `OK`，`current version: 20210709133512` |
| 启动耗时 | 约 8 秒到全部子系统就绪 |
| 内存占用 | 约 94 MB RSS |

### 启动后的子系统状态（`gctcli getsubsystems`）

```json
{"communications": false, "currency_state_manager": true, "data_history_manager": false,
 "database": true, "dispatch": true, "exchange_syncer": true, "grpc": true,
 "grpc_proxy": true, "internet_monitor": true, "ntp_timekeeper": false,
 "orders": true, "portfolio": true}
```

`communications`（未配通知通道）、`data_history_manager`（配置里关着）、`ntp_timekeeper`（设成 `-1`）为 `false` 是**预期的**。

### 币安永续数据链路：实测全部打通

| 命令 | 结果 |
| --- | --- |
| `getexchanges` | `Binance` |
| `getticker --pair BTC-USDT --asset usdtmarginedfutures` | ✅ `last: 82091.9`，含 high/low/volume/quote_volume |
| `orderbook getorderbook --pair BTC-USDT --asset usdtmarginedfutures` | ✅ 返回真实买卖档位 |
| `gethistoriccandles --granularity 3600 --rangesize 5` | ✅ 5 根 1h K 线，时间戳 UTC |
| `technicalanalysis macd --granularity 3600` | ✅ **688 个有效点**，返回 MACD / SIGNAL / HISTOGRAM 三序列 |
| `technicalanalysis ema --period 20` | ✅ 702 个有效点 |
| `pair get --exchange Binance --asset usdtmarginedfutures` | ✅ `autoPairUpdates` 拉到 **789 个** USDT-M 合约 |
| `pair getassets --exchange Binance` | ✅ `margin,coinmarginedfutures,usdtmarginedfutures,spot` |
| `futures getlatestfundingrate` | ✅ **无需 API Key**（公共端点） |
| `futures getopeninterest` | ✅ **无需 API Key**（公共端点） |
| `futures getallmanagedpositions` | ✅ 返回 `no positions`（无持仓，正常） |
| REST 网关 `/v1/getinfo`、`/v1/getticker`、`/v1/gethistoriccandles` | ✅（注意下方三条坑） |
| REST 网关错误密码 | ✅ 返回 HTTP 401 |

启动日志里这一行**印证了"币安永续没有 WebSocket"**：

```text
[WARN] | SYNC | Binance asset type usdtmarginedfutures websocket functionality is unsupported, REST fetching only.
```

同步管理器实际配置也被日志确认：

```text
[DEBUG] | SYNC | Exchange currency pair syncer config: continuous: true ticker: true orderbook: true trades: false workers: 4 ...
[DEBUG] | SYNC | Exchange CurrencyPairSyncer initial sync took 5.410239916s [10 sync items].
```

### 三个必须记住的实测结论

**1. `futures getcollateral` 在币安上不可用**

```text
rpc error: code = Unknown desc = unsupported wrapper function Get Collateral for exchange Binance
```

加 `--calculateoffline` 也一样。根因：RPC `GetCollateral`（`engine/rpcserver.go:4394-4471`）依赖 `ScaleCollateral` / `CalculateTotalCollateral`，而币安 wrapper 没实现这两个方法（落到 `exchanges/exchange.go:1649` / `:1655` 的基类默认）。

> **查合约可用资金一律用 `getaccountbalances --exchange Binance --asset usdtmarginedfutures`。**

**2. `GetFuturesContractDetails` 没有 RPC / gctcli 入口**

它只是 Go wrapper 方法（`exchanges/interfaces.go:215`、`binance_wrapper.go:2790`）。查合约精度（step size / tick size / 最小名义额）只能直接问币安：

```bash
curl -s "https://fapi.binance.com/fapi/v1/exchangeInfo?symbol=BTCUSDT" | jq '.symbols[0].filters'
```

**3. REST 网关与 gctcli 的调用方式差别很大。**

| 差异点 | gctcli | REST 网关（9053） |
| --- | --- | --- |
| 字段命名 | snake_case 输出 | **camelCase** 输出（`enabledExchanges`、`subsystemStatus`） |
| `pair` 参数 | `--pair BTC-USDT` 字符串 | **必须是对象** `{"delimiter":"-","base":"BTC","quote":"USDT"}`，传字符串报 `proto: syntax error` |
| HTTP 方法 | 无关 | **固定**：`getticker`/`getorderbook`/`submitorder` 是 POST；`gethistoriccandles`/`gettechnicalanalysis`/`getallmanagedpositions` 是 GET。用错返回 `{"code":12,"message":"Method Not Allowed"}` |
| K 线响应结构 | — | 字段在**顶层**（`.candle`），不是 `.data.candle` |
| `start`/`end` | 有默认值 | **必填**，格式 `2006-01-02 15:04:05 MST`，否则报 `invalid start and end times` |

查任意端点的准确方法和参数名：

```bash
jq -r '.paths | to_entries[] | .key as $p | .value | keys[] | select(.=="get" or .=="post") | "\(.|ascii_upcase)  \($p)"' gctrpc/rpc.swagger.json | sort -k2
jq -r '.paths."/v1/gethistoriccandles".get.parameters[].name' gctrpc/rpc.swagger.json
```

### 配置文件的两处实测教训

**1. `currencyConfig.forexProviders[].apiKeyLvl` 必须是整数**

手写配置时给了空字符串 `""`，结果 `dbmigrate` 直接失败：

```text
fatal error opening file (~/.gocryptotrader/config.json):
json: cannot unmarshal string into Go struct field Config.currencyConfig.forexProviders.0.apiKeyLvl of type int
```

字段类型见 `currency/currency_types.go:60`（`APIKeyLvl int`）。官方 `config_example.json` 用 `-1`。

> **结论：不要手写整个 config.json，从 `config_example.json` 派生。** 本目录的模板现在就是这么生成的。

**2. `available` 不能为空**

把 `usdtmarginedfutures.available` 设成空字符串、只填 `enabled`，启动时报：

```text
fatal error checking config values: no exchanges enabled
```

根因是 `CheckPairConsistency`（`config/config.go:1005-1016`）发现 `enabled` 不是 `available` 的子集，于是把整个交易所禁用（`config.go:1032` 的 `errNoEnabledExchanges`）。

> **`available` 至少要包含 `enabled` 里的所有交易对。** 启动后 `autoPairUpdates` 会自动把 `available` 刷新成全量 789 个（运行时生效；写回配置文件发生在正常关闭或配置变更时）。

### 尚未实测（需要 API Key）

以下命令已确认**存在且参数正确**，但当前无凭据，返回鉴权错误，等你提供币安合约 API Key 后验证：

- `getaccountbalances` / `updateaccountbalances`
- `futures getleverage` / `setleverage`
- `futures getfuturespositionsummary` / `getfuturepositionorders`
- `futures getcollateralmode` / `setcollateralmode`
- `futures setmargintype` / `changepositionmargin`
- `futures getmanagedposition`
- `submitorder` / `simulateorder` / `cancelorder`

**`futures changepositionmargin` 的真实参数**（与直觉不同，注意不是 `--amount`/`--type`）：

```text
--margintype  --originalallocatedmargin  --newallocatedmargin  --marginside
```

### 鉴权链路：实测通过（2026-10-10，真实主网 API Key）

填入真实 Key 后重启，启动日志**没有**出现 `authenticated API support disabled due to default/empty APIKey/Secret/ClientID values`，说明凭据被正确加载。实测结果：

| 命令 | 实测结果 |
| --- | --- |
| `getaccountbalances --asset usdtmarginedfutures` | ✅ 返回真实账户（id `TiFzoCTimYAuSg`），USDT `free: 68.687`、`hold: 1.321` |
| `futures getcollateralmode` | ✅ **`single`（单向持仓）** —— 正是本项目需要的模式 |
| `futures getleverage --pair BTC-USDT` | ✅ `20`（账户侧已设 20 倍） |
| `futures getfuturespositionsummary` | ✅ `total_collateral: 70.0076`、`free_collateral: 68.7274`、`collateral_used: 69.7700`、`recent_pnl: -0.2375`、`contract_settlement_type: linear` |
| `futures getallmanagedpositions` | ✅ `no positions found`（机器人未下过单，正常） |
| `orderbook buy quote --amount 1000` | ✅ 1000 USDT 可买 `0.012075836` BTC，均价 82810，`no_slippage_occurred: true`，`update_protocol: "REST"` |
| `orderbook whalebomb --side BUY --price 90000` | ✅ 返回逐档吃单明细 |

`orderbook buy quote` 的返回里 **`update_protocol: "REST"`** 再次印证了"币安永续无 WebSocket、行情靠 REST 轮询"这个结论。

BTC-USDT 永续的实测合约规格（`curl https://fapi.binance.com/fapi/v1/exchangeInfo?symbol=BTCUSDT`）：

| filter | 值 | 含义 |
| --- | --- | --- |
| `contractType` | `PERPETUAL` | 永续 |
| `PRICE_FILTER.tickSize` | `0.10` | 价格精度 |
| `LOT_SIZE.stepSize` / `minQty` / `maxQty` | `0.001` / `0.001` / `1000` | 数量步长与上下限 |
| `MIN_NOTIONAL.notional` | **`50`** | 最小名义额 50 USDT |

> 按 8 万价位算，最小可下单量 0.001 BTC ≈ 80 USDT，刚好越过 50 USDT 门槛。

### 第四个必须记住的实测结论：`simulateorder` 不是纸面下单

```text
rpc error: code = Unknown desc = spot asset type not enabled
```

两个原因叠加：

1. `SimulateOrder` 的 handler **硬编码 `asset.Spot`** 两次（`checkParams(..., asset.Spot, p)` 和 `GetCachedOrderbook(p, asset.Spot)`），而 `SimulateOrderRequest` proto 里**没有 `asset_type` 字段**，所以永续合约根本传不进去
2. 它调的是 `orderbook.SimulateOrder(amount, buy)`，返回盘口成交档位和最高/最低价 —— 本质是**盘口冲击模拟器**，从来不调 `SubmitFakeOrder`

> **真正的纸面下单 `OrderManager.SubmitFakeOrder`（`engine/order_manager.go:529`）没有 RPC / gctcli / REST 入口**，只能从 Go 代码调用。现成的调用方只有回测器（`backtester/eventhandlers/exchange/exchange.go:360-375`）。
>
> 这条实测结论直接影响 `docs/llm-dev` 方案：`PAPER` 模式**必须**由新增的 LLMManager 在进程内直接调 `SubmitFakeOrder`，不能指望复用 RPC。方案里的 ExecutionGateway 设计正是如此，实测验证了它是必需的而非可选的。

命令行想做"不花钱的下单演练"，可用替代见 [07-测试网与模拟交易](07-测试网与模拟交易.md) §3.1。

### 推荐启动命令（实测最优）

```bash
./gocryptotrader -portfoliomanager=false
```

加 `-portfoliomanager=false` 的理由：portfolio manager 是**现货/余额导向**的（`engine/portfolio_manager.go`），每分钟尝试给每种币找价格提供方，纯合约部署下会持续刷：

```text
[ERROR] | PORTFOLIO | Portfolio manager: UpdatePortfolio error: provider not enabled: CryptoID for currency BTC
```

实测 ERROR 从 **30 条降到 1 条**（只剩"未配通知通道"，属预期），且该 flag **不在 `dryRunParamInteraction` 列表里，不会触发 dry-run**（`engine/engine.go:927-937`）。

### 尚未实测（涉及真实资金或写入）

以下命令会改变交易所状态或动用真实资金，**未在本次验证中执行**：

- `submitorder` / `cancelorder` / `cancelallorders`（真实下单）
- `futures setleverage` / `setmargintype` / `setcollateralmode` / `changepositionmargin`（改账户设置）
- `datahistory addjob`（需要先 `dbseed exchange`，且 DataHistoryManager 当前是关闭的）

建议先在**测试网**跑完 [07-测试网与模拟交易](07-测试网与模拟交易.md) §4.5 的 13 步全链路，再动主网。

## 免责声明

GoCryptoTrader 上游 README 明确写着 "this bot is under development and is not ready for production!"。本指南描述的是**本地开发环境**的跑通方式。真实资金下单前请务必先读完 [07-测试网与模拟交易](07-测试网与模拟交易.md)，并从小额开始。
