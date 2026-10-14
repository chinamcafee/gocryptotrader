# 12 与 hyperliquid-auto-trader 的对照

> **实现状态（2026-10-11）**：本文档是设计原稿。已实现落点、两处实测偏差（588 合约口径 / KlineWeight 分档）与全部已知残余见 [实现后状态对照](实现后状态对照.md)；进度与缺口以 [`taskProgress/`](taskProgress/) 为准。

本文说明本方案对原项目的**复用 / 丢弃 / 新增**三类决策，以及术语对照。目的是让用户能一眼看出"哪些是我熟悉的东西被保留了，哪些因为平台不同而必须改"。

**参考项目**：`/Users/changzechuan/TradeCryptoCoinProjects/hyperliquid-auto-trader`
TypeScript（Node ≥22.12）pnpm monorepo，React 19 + Fastify + Postgres/Redis/ClickHouse + 10 个独立进程。自评状态：自主范围 78/78 = 100%，完整目标 78/100，真实交易 `NOT_READY / executionAuthorized=false`。git 仓库已初始化但**零提交**。

## 1. 完整复用（设计直接搬过来）

这些是原项目最有价值的部分，**逐字或近乎逐字继承**：

### 1.1 五条设计底线

`docs/README.md` 的"设计底线"完整继承，见 [01-需求与产品目标](01-需求与产品目标.md) §3。这是整个方案的安全基石，一个字都不改。

### 1.2 角色权限表

`docs/04-strategy-ai.md` §1 的"用户 / LLM / Jev / Risk Engine / Executor 各可以决定什么、不可以决定什么"表格，在 [06-Jev接入层](06-Jev接入层-SystemOne.md) §3.1 逐字引用。

### 1.3 半自动的定义

> 用户逐版、限时审批策略；授权期内，符合范围和硬风控的订单自动执行；越权、约束变化或模型不确定时进入人工复核。可交易市场须逐个审批，市场发现本身不授予交易权限。

### 1.4 Jev / System One 契约

`docs/22-jev-factual-input.md` + `packages/ai-adapters/src/jev.ts` + `jev-assessment.ts` 的全部内容：

- `POST {base}/v1/systemone`，Bearer，`{model, state, questions}` → `{model, answers, usage}`
- 三种题型 Choice / Score / Noul 及其响应形态差异（Noul 无 confidence）
- 固定模型版本 `jev-1.13.0`，`jev-latest` 仅探索
- 模型漂移检测正则 `^jev-\d+\.\d+\.\d+$`
- 响应校验规则（分布求和 1±0.03、选中项 ≥max−0.03、加权分 ±0.06、legend 3 项）
- **同源事实输入**不变量 + `readAnalysisFactsForReview` 的 11 项重校验
- 纯函数请求构造器（`jev-factual-input-v1` / `jev-review-v2`）
- 64 KiB 请求体上限，超限整体拒绝不截断
- `inputEvidence` 清单结构 + `MANIFEST_BOUND / LEGACY_MISSING / INVALID / NO_ASSESSMENT` 四态
- 订单准备和发送认领都要求 `MANIFEST_BOUND`

### 1.5 OpenAI 兼容适配范围

`docs/09-model-config-and-strategy-chat.md` §2 逐字引用：只用 `chat/completions` + `messages` + `tools/tool_choice` + `tool_calls`；不要求 Responses API；流式延后。

以及：

- 四层探针（auth → tool_call → schema → stream）
- `tool_choice: auto`（DeepSeek thinking 模式强制工具选择会 400）
- JSON mode + 服务端严格校验（不假设 `json_schema` 严格模式）
- 循环上限：4 轮 / 4 次每轮 / 8 次总计 / 24000 token / 30s 单请求 / 256KB body / 12000 字符内容
- `max_tokens` 默认 1024（512 会 TRUNCATED，T039 实测）
- 共同截止时间 90s（手动/影子）

### 1.6 系统提示词

`packages/ai-adapters/src/analysis.ts` 的 `analysis-readonly-v1` 和 `policy-proposal.ts` 的提取提示词，在 [05-LLM接入层](05-LLM接入层-OpenAI兼容.md) §6 里做了币安永续适配后使用。新增两段（指标地位、单向持仓），其余保持。

### 1.7 双模型配对治理

- `provider_profiles` + `provider_profile_revisions` + 单行 `active_model_pair` + 单调 `pair_epoch`
- 各自最多启用一个；`READY / INCOMPLETE / DEGRADED / STALE_PAIR`
- 编辑活跃档案 → 新不可变修订版 → 清 active 指针 → `pair_epoch++` → 暂停策略 → 撤销审批
- CAS 事务 + `If-Match: pair_epoch` + 幂等键 + 重新认证

### 1.8 策略生命周期与授权粒度

`DRAFT → NEEDS_CLARIFICATION → READY_FOR_REVIEW → APPROVED → RUNNING → PAUSED/EXPIRED/REVOKED`

授权粒度：`strategy_version_id + account_id + network + execution_mode + approved_market_ids + valid_from/to + immutable risk_envelope_hash + approved_model_pair_epoch`

`reviewable-v1` 冻结契约（`tradeAuthorized: literal(false)`、`maxNewRiskIntents ≤ 5`、≤7 天、perp 必须有杠杆上限）

### 1.9 硬风控清单

`docs/04-strategy-ai.md` §6 的八大类（授权 / 数据 / 资金 / 账户资金模式 / 成交保护 / 频率 / 仓位与委托 / 系统），在 [07-策略与自动决策引擎](07-策略与自动决策引擎.md) §5.2 逐条具体化为约 45 个检查码。

统一敞口公式、UTC 日损基线、"reaches cap or source failure → preflight rejects"。

### 1.10 决策合成规则

`docs/04-strategy-ai.md` §5 的伪代码，在 [06](06-Jev接入层-SystemOne.md) §7 具体化。特别是"即使全部一致也不直接 ALLOW，而是 REVIEW/AWAITING_HARD_RISK_AND_APPROVAL"。

### 1.11 订单状态机与 UNKNOWN

`CANDIDATE → BLOCKED/REVIEW/PREPARED → SUBMITTING → ACKED/REJECTED/UNKNOWN → RESTING/PARTIALLY_FILLED/FILLED/CANCELED`

`UNKNOWN` 永不盲目重试；崩溃 → `SUBMITTING` 转 `UNKNOWN` + 账户阻断。

### 1.12 成本与预算

`dailyBudgetUsd` 三态（null 未设 / **恰好 0 = 禁止一切请求** / 正数 = 计划值非硬闸）；24h 任务预约上限（LLM 默认 100，Jev = min(配置,100)）；四处门禁（探针/预约/发送前/DB）。

以及诚实的限制说明："没有供应商价格表，无法做每请求 USD 记账"。

### 1.13 工具证据四道闸

作用域 / 时间（不许未来，`asOf > now+2s` 拒）/ 体积（12KB、32KB）/ 敏感键（深度 ≤8，正则匹配 secret/api_key/private_key/mnemonic/seed_phrase）。

工具转录留存（≤8 条 × 12KB，每条 SHA-256，含原始参数）。

### 1.14 上下文管理规则

256 条用户消息 / 16000 字符上限；**超出时要求在新会话写明经确认的汇总策略，避免静默截断**；矛盾必须产生澄清问题，绝不静默选择。

### 1.15 密钥保管设计

`docs/14-local-https-model-secrets.md`：RSA 3072 密钥对；每 key 用随机 AES-256-GCM 数据密钥加密，数据密钥用 RSA-OAEP-SHA256 包裹；文件 0600/0700；DB 只存 `secret://model/<UUID>` + 截断 SHA-256 指纹；会话 vault 单独密钥，ID 作 AAD，标题不加密。

### 1.16 URL 安全规则

https only、无嵌凭据、无重定向、DNS 解析后重查 IP、阻断 link-local/元数据地址、合成数据探针。

### 1.17 错误码词表

`ProbeError` 的约 60 个码全部继承（见 [05](05-LLM接入层-OpenAI兼容.md) §9），只新增数据层/策略层/风控层/系统层的码。

### 1.18 可观测性指标与失效矩阵

`docs/06-security-operations.md` 的指标表和 §7 失效矩阵，在 [10-安全与风控](10-安全与风控.md) §6-7 具体化。

### 1.19 诚实的验收提醒

`docs/07-ui-delivery.md` 的"容易被误判为'通过'的情况"，在 [01](01-需求与产品目标.md) §10 和 [10](10-安全与风控.md) §8.2 引用。

### 1.20 评估方法

两层评估（安全违规率/证据缺失率/人工标注一致性/校准度 → 含费滑点资金费的纸面表现）。

## 2. 明确丢弃（原项目有但本方案不需要）

### 2.1 全深度节点采集（原项目 22 个未完成点里的 12 个）

| 丢弃项 | 原项目文档 | 丢弃理由 |
| --- | --- | --- |
| Hyperliquid 非验证节点 | `docs/08-full-depth-capture.md`、`docs/13-node-feasibility.md` | 币安是 CEX，REST `/fapi/v1/depth` 直接给完整深度 |
| Ubuntu 24.04 / 16 vCPU / 128GB / 500GB SSD 主机 | `docs/13` | 不需要 |
| 节点 flags（`--write-trades --write-raw-book-diffs --write-order-statuses --batch-by-block --serve-info`） | `docs/08` | 不需要 |
| L4 检查点 + 原始盘口 diff 有序重建 | `docs/08`、`apps/book-rebuilder` | 不需要 |
| 小时级不可变归档 + SHA-256/行数/区块范围清单 | `docs/08`、`apps/market-capture`、`apps/ops` | 不需要 |
| 当前小时实时 spool + 原子游标 + SSH 分页传输 + 成对审计 | `docs/08` | 不需要 |
| ClickHouse 投影（`market_trades`、`book_diffs`、`book_checkpoints_index`、`minute_bars_projection`） | `docs/05` | 不需要，K 线走现有 `candle` 表 |
| 双通道小时发布凭据 | `docs/05` | 不需要 |
| 容量规划（~100GB/天 → ~36.5TB/年） | `docs/05` §6 | 不需要 |
| `FULL / TOP20_ONLY / MID_ONLY` 质量分级 | `docs/04`、`docs/08` | **重新定义**为 `EXCHANGE_REST / LOCAL_TRACKED / DATABASE_CACHED / DERIVED / PARTIAL / GAP / STALE / UNKNOWN`（见 [04](04-数据层-K线与指标.md) §5） |

> 原项目的 `FULL 0/1487 markets`、"all live model analysis runs on TOP20_ONLY"、"every non-HOLD path is fail-closed" 这个死结，在本方案里**根本不存在**。这是移植到 gocryptotrader 最大的收益。

### 2.2 链上钱包与签名（原项目 22 个未完成点里的 10 个的核心）

| 丢弃项 | 原项目文档 | 丢弃理由 |
| --- | --- | --- |
| 主账户地址 / agent 钱包 / 助记词 | `docs/11-wallet-security-design.md` | 币安用 API Key + HMAC，不是私钥签名 |
| Agent 钱包生命周期（创建/平台授权/启用/轮换/吊销） | `docs/11` | 同上 |
| 独立 signer 进程 + Unix socket + 短期密钥读取 | `apps/signer`、`docs/06` | 同上 |
| 单一执行器约束 + 单调 nonce | `docs/03` | 同上 |
| signer 允许列表（恰好一个 `order` 或一个 `cancelByCloid`） | `docs/03` | 同上 |
| `cloid` 格式 `/^0x[0-9a-fA-F]{32}$/` | `docs/05` | 币安用 `newClientOrderId`，格式自由（本方案用 `gct-llm-` 前缀） |
| 账户抽象模式（`disabled/unifiedAccount/portfolioMargin/default/dexAbstraction`） | `docs/11`、`docs/04` §6 | 币安对应的是**持仓模式**（单向/双向）和**保证金模式**（逐仓/全仓），检查项已改 |
| 测试网 faucet 前置条件（同地址需有主网充值历史） | `docs/todo` | 币安测试网直接领 |
| `HAT_AGENT_KEY_REF` / `HAT_SIGNER_ENABLE_MAINNET_ORDER/CANCEL` | `docs/06` | 不需要 |
| 金融证明合同 A/B（`PREZERO_OFFICIAL_BOUNDED` / `EXACT_UTC_ZERO_INDEPENDENT`） | `docs/43`-`docs/46` | 币安直接给 USDT 权益，不需要外部汇率源和独立历史源认证 |
| USDC/USD 参考（Kraken 公共 WS v2 10 档） | `docs/17` | 不需要，USDT 就是计价单位 |
| 外部本金中性化 | `docs/18` | 大幅简化：只需识别币安内部的划转（`UAccountIncomeHistory` 的 TRANSFER 类型） |
| `new-risk-v2` 金融证书 + 同事务证书 | `docs/45`、`docs/46` | 简化为 `new-risk-v1` 检查码 + `llm_risk_evaluations` 表 |
| 三差额审计（仓位 / 现货余额 / 永续原始 USDC） | `docs/26`-`docs/32` | 简化为单一仓位 + 余额对账 |
| 原生签名器金融检查 + 保护锁 `lockPgNewRiskProtectionForSend` | `docs/38`-`docs/42` | 不需要（无签名器） |
| 财务源事务栅栏 `account_financial_source_epochs` | `docs/34` | 简化 |
| 封存窗口（sealed FILL/FUNDING/LEDGER） | `docs/19` | 简化为每日 UTC 00:00 一次快照 |

> 原项目为这块写了 `docs/17` 到 `docs/46` 共 30 篇文档、migrations 0102-0118，是它工作量最大且**最未完成**的部分（"no real testnet fill yet"）。本方案省掉约 **60% 的工作量**。

### 2.3 其它丢弃项

| 丢弃项 | 理由 |
| --- | --- |
| Redis | 频率计数直接查 SQL（低频，每分钟几次） |
| ClickHouse | 不采集逐笔 |
| 对象归档服务 | 无节点数据 |
| 现货支持（`BUY` / `SELL_HELD`、`PURR/USDC` 特例、spot asset ID = 10000+index） | 只做永续 |
| HIP-3 和其它 perp DEX | 只做币安 |
| 市场身份 `network + marketType + dex + assetId` | 改为 `Exchange:asset:Pair`（gocryptotrader 格式） |
| `getTradesWindow` 工具（逐笔成交） | 币安永续无 WS 成交流；原项目自己也标 `UNKNOWN/UNVERIFIED` |
| 564 市场 × 2 订阅 > 1000 订阅上限的问题 | 不存在（无 WS） |
| `apps/wallet`、`apps/ops`、`apps/market-capture`、`apps/book-rebuilder` | 不需要 |
| Drizzle ORM | gocryptotrader 用 sqlboiler |
| Fastify + OpenAPI | gocryptotrader 用 grpc-gateway，自动出 `rpc.swagger.json` |
| Ant Design X 对话组件（M7 阶段再考虑） | 首期 gctcli REPL |
| `HAT_*` 环境变量体系 | 改为 `config.json` 的 `llm` 段 |

## 3. 新增（原项目没有，本方案新加）

### 3.1 技术指标层 ⭐

**这是本方案相对原项目最大的能力增量。**

原项目**全仓库没有任何指标计算**（grep `MACD|均线|moving average|\bEMA\b|rsi|indicator` 零真命中），这是刻意的："系统不是'写死买卖指标'；买卖假设由用户策略和当时数据驱动"。

本方案因为用户明确要求 MACD 和均线，且 gocryptotrader 已有完整实现（`exchanges/kline/technical_analysis.go`）：

| 新增 | 内容 |
| --- | --- |
| `getIndicators` 工具 | 从原项目的 `getMarketWindow` 拆出，独立暴露 MACD/SMA/EMA/RSI/ATR/BBANDS |
| `IndicatorSet` / `MACDFact` / `MovingAverageFact` | 结构化指标事实 + 派生事实 |
| 派生事实计算 | `CrossState`（GOLDEN/DEATH/NONE）、`BarsSinceCross`、`HistogramTrend`、`PriceVsMA`、`Slope` |
| `indicatorPolicy` 策略字段 | 指标参数固化到策略版本，保证可重放 |
| `state.indicatorParams`（Jev 输入） | Jev 必须知道指标参数才能判断 |
| 提示词新增段落 | "Technical indicators … are evidence about past prices, not predictions and not trade instructions. Never treat a crossover by itself as sufficient grounds for a non-HOLD action." |
| 指标触发器 | `triggerOnMacdCross` / `triggerOnMaCross` / `triggerOnVolatilitySpike`（触发 ≠ 决策） |

**哲学上的处理**：见 [04-数据层](04-数据层-K线与指标.md) §8。保留"指标不是权威"，但允许指标作为证据和触发器。

### 3.2 对话分析模式 ⭐

原项目有策略对话（`docs/09` §4 的工作区规格），但**没有独立的"只读问答"模式**——它的对话目的是产出策略条款。

用户明确要求"对话分析实时数据"，所以新增：

- 第三个提示词版本 `chat-analysis-v1`
- 独立的代码路径（不产 `decision_run`，产 `chat_message`）
- 明确的安全边界：这个模式**永不**产出候选、订单、审批或执行
- 用户要求下单时，模型必须拒绝并指向审批流程
- 强制区分"交易所事实 / 派生指标值 / 我的解读"三类表述

### 3.3 复用 gocryptotrader 既有能力（零新增开发）

| 能力 | 来源 | 原项目对应 |
| --- | --- | --- |
| 多交易所抽象 | `exchanges/interfaces.go` `IBotExchange` | `packages/hyperliquid-adapter`（单交易所） |
| K 线获取 + 清洗 + 分批 | `exchanges/kline` | `minute_bars` + `candleSnapshot` |
| K 线持久化 + 批量回填 | `kline_datastorage.go` + `DataHistoryManager` | `apps/market-capture`（复杂得多） |
| ticker / orderbook 单例存储 | `exchanges/ticker`、`exchanges/orderbook` | `Ingestor` + Redis |
| 订单管理器（限额、halt、通知、仓位跟踪） | `engine/order_manager.go` | `apps/executor`（40 个模块） |
| 本地仓位 + 未实现盈亏 + 资金费跟踪 | `exchanges/futures` `PositionController` | `position_snapshots` + delta 审计 |
| gRPC + REST 网关 + Basic Auth + TLS | `engine/rpcserver.go` + `gctrpc` | `apps/api`（Fastify，60 个模块） |
| CLI 客户端 | `cmd/gctcli` | 无（原项目只有 Web） |
| 通信通知（Telegram/SMTP/Slack/SMS） | `engine/communication_manager.go` | 无（原项目只有 Web 展示） |
| 内部 pub/sub | `dispatch` | 无（用 PG outbox + Redis） |
| 配置加密 | `config/config_encryption.go` | `docs/14` 的 vault（更复杂） |
| 回测器（含实盘模式） | `backtester/` | 无（只有 `paper_replays` 反事实估算） |
| 数据库迁移体系 | `database/migrations` + `cmd/dbmigrate` | 118 个 SQL 迁移（自建体系） |
| 限频器 | `exchanges/binance/ratelimit.go` | 自建 |

### 3.4 币安永续特有的新增检查

| 新增检查码 | 理由 |
| --- | --- |
| `RISK_COLLATERAL_MODE_HEDGE` | 币安 wrapper 不传 `positionSide`，双向持仓会导致下单被拒 |
| `RISK_OPPOSING_POSITION` | 单向持仓下反向开仓 = 减仓，必须明确意图 |
| `RISK_NETWORK_MISMATCH` | runMode ↔ endpoint ↔ approval.network 三方一致（防主网/测试网错配） |
| `RISK_CLOCK_DRIFT` | 币安 `recvWindow=5000ms`，时钟偏差会导致 `-1021` |
| `RISK_RATE_LIMIT_DEGRADED` | 币安 2400 weight/min 限额 |
| `RISK_FUNDING_SETTLEMENT_IMMINENT` | 4h/8h 结算周期，策略可能要求避开 |
| `RISK_MARK_PRICE_DEVIATION` / `RISK_INDEX_DEVIATION` | 币安有标记价和指数价，偏离过大说明异常 |
| `RISK_MARGIN_TYPE_UNEXPECTED` | 逐仓/全仓影响保证金计算 |
| `RISK_BELOW_MIN_NOTIONAL` | 币安有最小名义额（约 5-20 USDT，按合约不同） |

### 3.5 新增的治理机制

| 新增 | 理由 |
| --- | --- |
| `llm readiness` 准入命令 | 借鉴原项目的 `readiness:testnet`（退出码 2 = NOT_READY），但原项目**没有**把它做成硬门禁。本方案要求 M6 之前不能进 TESTNET |
| 架构测试（import 边界） | 原项目靠进程边界隔离，本方案在单进程内必须靠编译期检查 |
| halt 三级（SOFT/MARKET/FULL） | 原项目只有 per-network halt，且**没有 un-halt API**（缺陷）。本方案补齐 |
| 保护性退出 fail-safe | 币安原生支持 `STOP_MARKET`，所以简化为"挂交易所条件单 + 挂失败则市价平仓"，不需要原项目那套 0090-0099 + 0104 + 0113 的分配账本 |
| 崩溃恢复扫描 | 重启时扫 `SUBMITTING` 中的意图 → `UNKNOWN` |

## 4. 术语对照表

| 原项目术语 | 本方案 | 说明 |
| --- | --- | --- |
| `network: mainnet / testnet` | `binance-mainnet / binance-testnet` | 加交易所前缀 |
| `marketType: PERP / SPOT` | 固定 `PERP`，字段保留 | 只做永续 |
| `marketId: perp:default:BTC` | `Binance:usdtmarginedfutures:BTC_USDT` | gocryptotrader 格式 |
| `assetId` / `dex` / `spotMeta.universe.index` | 不需要 | CEX 无这些概念 |
| `accountId: acct_demo` | `binance-usdtm-main` | |
| `agent wallet` / `owner address` | 币安 API Key | |
| `cloid` | `ClientOrderID`（前缀 `gct-llm-`） | 幂等键 |
| `szDecimals` | 币安 `stepSize`（从 `GetFuturesContractDetails`） | 数量精度 |
| `tick` | 币安 `tickSize` | 价格精度 |
| `DataQuality.FULL` | `EXCHANGE_REST` | 币安 REST 就是权威 |
| `DataQuality.TOP20_ONLY` | 不需要 | REST 给完整深度 |
| `DataQuality.MID_ONLY` | 不需要 | |
| `DataQuality.PUBLIC_AGGREGATE` | `DATABASE_CACHED` / `DERIVED` | |
| `capture_coverage` / `capture_gaps` / `archive_manifests` | `GAP` 质量标签 | 大幅简化 |
| `minute_bars` | 现有 `candle` 表 | 复用 |
| `market_snapshots` | `llm_market_snapshots` | |
| `decision_runs` | `llm_decision_runs` | 列几乎一致 |
| `order_intents` / `order_attempts` | `llm_order_intents` | 合并（无签名器，不需要 attempt 分离） |
| `risk_evaluations` | `llm_risk_evaluations` | |
| `risk_budget_reservations` | `llm_budget_reservations` | |
| `strategy_approvals` | `llm_strategy_approvals` | |
| `market_trade_grants` | `llm_market_trade_grants` | |
| `active_model_pair` | `llm_active_model_pair` | |
| `provider_profiles` / `provider_profile_revisions` | `llm_provider_profiles` / `_revisions` | |
| `provider_probe_results` | `llm_provider_probe_results` | |
| `conversations` / `chat_messages` | `llm_conversations` / `llm_chat_messages` | |
| `policy_proposal_requests` | 合并进 `llm_strategy_versions.proposal_json` | 简化 |
| `auto_candidate_slots` / `shadow_analysis_slots` | `llm_budget_reservations` + `llm_new_risk_counters` | 简化 |
| `analysis_preview_requests` | `trigger = MANUAL_PREVIEW` | 简化 |
| `paper_replays` | `run_mode = PAPER` + `SubmitFakeOrder` | gocryptotrader 原生支持 |
| `network_new_risk_halts` | `llm_halts`（三级） | 增强 |
| `outbox_events` | 直接调 `CommunicationsManager.PushEvent` | 单进程不需要 outbox |
| `audit_events` | `llm_audit_events` | |
| `usdc_usd_reference_observations` | 不需要 | USDT 就是计价单位 |
| `perp_mark_context_observations` | `llm_market_snapshots.ticker_json`（含 MarkPrice） | 复用 |
| `observedDayStartInventory` | `llm_day_start_inventory` | 大幅简化 |
| `observedUtcDayUsdLoss` | 由 `llm_day_start_inventory` + `llm_external_flows` 计算 | |
| `risk_financial_certificates` | 不需要 | |
| `account_financial_source_epochs` | 不需要 | |
| `new-risk-v1` / `new-risk-v2` | `new-risk-v1` | 只需一版 |
| `readiness-v1` | `llm readiness` 命令 | |
| `analysis-readonly-v1` | 同名 | 提示词版本 |
| `policy-proposal-v1` | 同名 | 新增编号（原项目未编号） |
| `chat-analysis-v1` | 同名 | **本方案新增** |
| `jev-factual-input-v1` / `jev-review-v2` | 同名 | |
| `reviewable-v1` | 同名 | |
| `MANIFEST_BOUND` | 同名 | |
| `STALE_PAIR` | 同名 | |
| `pair_epoch` | 同名 | |
| `tradeAuthorized` / `executionReady` / `accountReconciled` | 同名 | 三个 fail-closed 标志 |
| `AUTO_SHADOW` / `AUTO_CANDIDATE` / `AUTO_EXECUTION` / `MANUAL_PREVIEW` | 同名 + 新增 `MANUAL_CHAT` | |
| `COUNTERFACTUAL_ONLY` | `run_mode = PAPER` | |
| `HAT_*` 环境变量 | `config.json` 的 `llm` 段 | |

## 5. 架构差异总结

| 维度 | 原项目 | 本方案 |
| --- | --- | --- |
| 语言 | TypeScript (Node ≥22.12) | Go 1.27 |
| 仓库形态 | pnpm monorepo，10 个 app + 7 个 package | 单体仓库，新增 1 个顶层包 + engine 子系统 |
| 交易所 | Hyperliquid（去中心化永续 DEX） | 币安 USDT 本位永续（CEX） |
| 签名方式 | 链上私钥签名（agent wallet + nonce） | API Key + HMAC-SHA256 |
| 行情来源 | 自建非验证节点（全深度）+ 公共 WS（20 档） | 交易所 REST 轮询（SyncManager） |
| WebSocket | 有（公共 + 节点） | **币安永续无 WS**（代码显式禁用） |
| 数据库 | PostgreSQL（104 表 / 118 迁移） | SQLite 或 PostgreSQL（现有 23 迁移 + 新增 8 个，约 22 张新表） |
| 缓存 | Redis | 进程内 LRU |
| 列式存储 | ClickHouse（8 DDL） | 无 |
| 对象归档 | 内容寻址不可变文件 + 清单 | 无 |
| 进程隔离 | ai-worker / signer / executor / wallet 独立进程 + Unix socket | 单进程 + 包边界 + 架构测试 |
| 密钥保管 | RSA-OAEP 包裹 AES-GCM，独立 ai-worker 解密 | 同样算法，但**在进程内**由 `llm/secrets` 解密 |
| UI | React 19 + AntD 6/X + Lightweight Charts，5 路由 30 面板 | gctcli REPL（M7 可选 Web UI） |
| API | Fastify + OpenAPI，`/v1/*`，WS `/v1/stream` | gRPC + grpc-gateway（`/v1/*`），TLS + Basic Auth |
| 命令信封 | `{commandId, status:"ACCEPTED", resourceId, requestId, asOf}` | gRPC 原生响应 + 错误码 |
| 策略框架 | Strategy Orchestrator（自建） | 新增 LLMManager 子系统；回测器保留作历史验证 |
| 表数量 | 104 | ~22 新增 |
| 文档数量 | 46 篇 + ~150 篇工作日志 | 13 篇（本目录） |
| 完成度 | 自主范围 100%，完整目标 78%，真实交易 NOT_READY | 方案阶段 |
| 最大死结 | 全深度节点从未跑起来（`FULL 0/1487`），所有非 HOLD 路径 fail-closed | **不存在**（币安 REST 即权威） |
| 第二大死结 | 无资金测试网账户，零真实成交 | 币安测试网可直接领取模拟资金 |

## 6. 从原项目的未完成中吸取的教训

原项目自评"完整目标 78/100"，缺的 22 分全在**真实世界验证**：

| 缺口 | 教训 | 本方案的对策 |
| --- | --- | --- |
| 真实节点从未运行（12 分） | **架构野心超出了必要范围**。全深度采集对低频策略是过度设计 | 不引入任何需要额外基础设施的组件；只用币安 REST |
| 无资金测试网账户，零真实成交（10 分） | **验证链路被外部依赖阻塞**。整个执行链只有合成验证 | 币安测试网可自助领取资金；M6 明确要求 TESTNET 全链路演练，`llm readiness` 作为硬门禁 |
| 生产 ALLOW 只在合同 A 下可生成 | 过度复杂的金融证明契约 | 简化为 `new-risk-v1` 检查码；币安直接给 USDT 权益 |
| Jev 交易语境质量未评估 | **模型质量评估被推迟到最后**，结果一直没做 | M4 就要求 SHADOW 模式，M6-03 要求 ≥100 条人工标注 |
| 硬 USD 成本闸未实现 | 成本控制在没有供应商定价表时无法闭环 | 诚实保留这个限制（`dailyBudgetUsd` 正数只是计划值），用 token 计数 + 预约次数做硬闸 |
| 完整账户对账未做 | 对账是"最后一公里"，容易被推迟 | M5-08 是必做项，`accountReconciled` 是硬门禁 |
| 紧急停机不完整（无 un-halt API） | 安全机制的"退出路径"和"进入路径"同样重要 | M5-10 明确包含 `ClearHalt`（带重新认证 + 不自动恢复） |
| 真实浏览器验收未做 | UI 验收成本高 | 首期不做 UI，用 gctcli（可脚本化验收） |
| LLM 流式延后 | 正确的取舍 | 同样延后，且明确"决策路径不用流式" |

**最重要的一条教训**：原项目在**架构和安全设计上极其严谨**（46 篇文档、118 个迁移、662 个测试），但**从未在真实环境完成一笔交易**。严谨的设计如果没有真实验证闭环，就只是纸面严谨。

本方案的对策是把 M6（测试网验收）设为**硬门禁**：`llm readiness` 命令返回非 0 就不能进 LIVE，且 M6 的 10 项验收全部要**演示**而不是"代码写完了"。

## 7. 参考工程溯源

原项目 `docs/10-reference-project-audit.md` 审查了三个固定版本的参考仓库，本方案继承其结论：

| 参考工程 | 许可 | 借鉴 | 不借鉴 |
| --- | --- | --- | --- |
| **Jev Trade**（`github.com/aowang-ai/jev-trade`，`a3f2f834a1b9`） | MIT | `src/model.ts` 的有界 Jev Choice 问题、市场状态摘要、无效答案转 HOLD；`src/feed.ts` 的快照/WS/用户成交分流；`src/account.ts` 的成交去重与账户对账 | 2 秒自动报价、每市场独立钱包、**模型概率 → 杠杆** |
| **Trading Terminal**（`f0a2f81c51f5`） | Apache-2.0 | 图表上下文 → 结构化计划 → 确定性校验 → 授权 → 执行 → 复盘的交互链 | — |
| **Purro Extension**（`9dfcaac2077d`） | CC BY-NC-ND | 仅产品需求，**不复用代码** | 钱包安全缺陷：postMessage 桥转发任意类型、`isDevMode` 来自调用方数据、`sendToken` 无确认 = 未授权资金转移风险 |

**"模型概率 → 杠杆"这条明确不借鉴**，与本方案底线 5 一致。

## 8. 下一步

**[OPEN-QUESTIONS-待确认事项](OPEN-QUESTIONS-待确认事项.md)** —— 开工前必读，含 6 项阻塞性决策。
