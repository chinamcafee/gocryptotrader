# 2026-10-11 session-51（同日续段）：M6-08 备份恢复演练（三件套两跳恢复进新进程）

## 会话目标

补齐 M6-08：「从备份完整恢复到另一台机器，验证 vault + 数据库 + 配置」。三件套都是本地文件（vault 目录 / sqlite 文件 / config.json），与 M6-06/M6-07 同法做全机器可验证的离线演练；生产差异（PostgreSQL、真实双机）写成手册并如实标注边界。

## 完成的子任务

- `M6-08` 备份恢复演练
- `M2-13` 残余「下架事件生产者」收官（第二段，不计分；含 M6-02 #18 转 🟡）
- `M2-13` 收官第三段：触发队列生产消费者 + engine lint 清零（不计分）
- `M2-13` 收官第四段：账户事件生产者 seam（不计分，随桥激活）
- `M2-13` 收官第五段：数量链盘口走价装配件（不计分，RiskFacts 前置）
- `M2-13` 收官第六段：决策→订单桥第一件 `BuildIntentPlan`（不计分）
- `M2-13` 收官第七段：`RiskFacts` 链头——M5-08 账户事实生产者（不计分）
- `M2-13` 收官第八段：`RiskFacts` B/C 组映射（不计分）
- `M2-13` 收官第九段：账户事实进快照接缝 + `worstSnapshotQuality` 折叠修正（不计分）
- `M2-13` 收官第十段：`RiskFacts` A 组（授权）映射（不计分）
- `M2-13` 收官第十一段：`RiskFacts` D 组（市场上下文）映射（不计分）
- `M2-13` 收官第十二段：`RiskFacts` G 组（系统）映射（不计分）

## 具体做了什么

1. `engine/llm_backup_restore_drill_test.go`（新）——`TestM6BackupRestoreDrill`（父）+ `TestM6BackupRestoreChild`（子进程）：
   - 机器 A：真 vault（RSA 包裹钥匙对 + 两条密钥 + `conversation-key.bin`）+ 真库（**生产路径**写入：档案/修订版（含指纹）、活跃配对 READY、订单意图、审计行、已结算预算预约）+ 双版本配置（v18 显式 LLM 段 / v17 无 LLM 段）；
   - 备份**两跳**：A → 备份介质 → B（文件级复制；库先干净关闭再拷——journal 收进单文件；断言备份集含 `vault-private.pem` 与 `conversation-key.bin`）；
   - 机器 B = **全新进程**（re-exec 测试二进制，同 M6-06 手法）+ 全新目录树；子进程只从 B 的文件重开一切（新 vault、新连库、密器由恢复的钥匙文件重建），**10 步断言**：库迁移干净空跑 → 从库里引用揭密钥两半咬合（LLM+Jev、指纹一致）→ 会话密文逐字节重开（含中文）→ 意图/审计行在 + `LLMGetUsage` 跨机器出数（token/延迟）→ v18 配置原样加载（`CheckLLMConfig` 不覆写显式值）/ **v17 配置在 B 上迁移到 18**（缺席 llm 段 disabled + READ_ONLY）→ **负例①只有库没有 vault = `ErrVaultNotInitialised`**（不凭空铸新钥匙）→ **负例②漏拷 `conversation-key.bin` = 重铸新钥匙、旧密文 `ErrDecrypt` 永久不可读** → **泄密机器证明**（恢复库原始字节不含任何明文 Key/消息）。
2. `taskProgress/acceptance/M6-08-backup-restore-drill.md`（新）——演练记录：10 步断言表（逐项“为什么值钱”）+ **备份集定义**（vault 四件套 / 库 / 配置，缺一不可）+ 生产备份恢复手册（含「vault 与库分开保管传输」红线）+ 边界如实记录（同机新进程非真双机；SQLite 口径——pg 的 `pg_dump`/`pg_restore` 单列**未执行**；机器 B 只读+迁移，不声称写路径）。

## 第二段：`M2-13` 残余「下架事件生产者」收官（同日续段）

1. `engine/llm_market_delist.go`（新）——`MarketDelistWatcher`：`Check` 把旋转器每 10 分钟刷新的市场列表与上轮做差，消失的市场走 **10 §7 行 18 完整响应链**：撤配置账户上覆盖该市场的活跃 grant → 级联撤引用它的有效审批（坏清单炸错不跳过）→ 对应 FROZEN 版本移 PAUSED（同 halt / pair-switch 先例）→ `MARKET_DELISTED` 审计（kind/actor/明细全量）→ `LLMNotifier.MarketDelisted` 高优推送（点名「确认下架并清理配置」）。纪律：**首见只登记不响应**（priming）、失败留集合内下轮重试且全链幂等、恰好一次；外部下架与运营者移除同一响应（安全方向一致，重启用走重新授权）。
2. 装配：`Rotator.attachDelistWatcher`（`w.Markets = r.Markets`，Start 前挂入不落锁）← `SetupRotation`（读 `m.marketWatch`）← `LLMManager.SetMarketWatch` ← `Engine.Start` 在 `Start` 前用 `newMarketDelistWatch()`（真适配器：`LLMMarketGrants` / `LLMPairSwitchStore` / `LLMDecisionStore` / `bot.LLMNotifier()`）装配；`rotationLoop` 在 `RefreshMarkets` 成功后调用（刷新失败不调用——旧列表做差是空读）。
3. `engine/llm_events.go` +`EventMarketDelisted` 与 `MarketDelisted(...)`（高优）。
4. **侦察遗留登记（新）**：`RaiseAccountEvent` 与 `Scanner().Drain` 在生产中均无调用方——账户事件生产者与**触发队列的生产消费者**（扫描事件尚不自动驱动决策）为仅存数据面接线项，已写进 M2-13 行 / §1。

## 第三段：触发队列生产消费者（`M2-13` 收官）+ engine lint 清零（同日续段尾）

1. `engine/llm_trigger_consume.go`（新）——`TriggerConsumer`：`Engine.Start` 在决策运行器装配后起 `triggerConsumeLoop`（每 30s 一轮，`runtimeCtx` 收尾）。每轮：**READ_ONLY 不排空**（`modePermitsDecisions` 为假直接返回——只读下把成百上千个触发落成 MODE_NOT_PERMITTED 行是噪音；队列有界丢最旧，不会积压）→ `Scanner().Drain(50)` → 一次 `ListActiveApprovals` + 逐条版本状态读，按「市场 → 活跃审批覆盖**且版本 FROZEN**」建路由表 → 逐 (事件, 版本) `buildShadowRunRequest` + `ShadowRunner.RunOnce`（去抖→门禁→分析→Jev→合成→落账**全在既有链里**）。**路由不是门禁**（预算/readiness/halt/对账闸全保留在 RunOnce）；**单版本失败不拖垮一批**（计数 + 限长错误收集）；结果 `TriggerConsumeResult` 供循环按需打点。
2. `LLMManager.EffectiveRunMode()`（新 getter）；`Engine.Start` 装配位（runner 成功才起循环）。
3. **lint 清零**（`golangci-lint run ./engine/...` 14 → **0**）：存量 5 条（session-40 `rpcserver_llm_control.go`：unnamedResult、minmax×2）+ 本会话新文件 9 条（goconst：`jsonKeyReason` 常量收口 6 处 `"reason"` map 键；iface：删除与 `iAuditEvents` 重复的 `iDelistAudit`；gosec G122：备份演练 copyTree 由 WalkDir 回调改手写递归；testifylint：4 处 `assert.True(strings.Contains)` → `Contains/NotContainsf/ErrorContainsf` 并移除 4 个 `strings` 导入）。

## 第四段：账户事件生产者 seam（`M2-13` 收官 · 随桥激活；同日续段尾二）

1. `engine/llm_dispatch.go`：`OrderDispatcher` 新 seam `Events func(marketID, detail string, at time.Time)`——**受理成功**（生命周期 ACKED 落库 + `ORDER_SUBMITTED` 审计落账之后）即 `Events(市场, "order acked: <交易所单号>", now)`。市场取自 `intent.Pair`（扫描器/审批同一拼写 `BASE_QUOTE`）；**UNKNOWN / 拒绝 / Shadow 路径不响**——模糊结果由对账旗标响应，不是「再看一眼」信号；nil seam 静默丢弃（装配前/测试）。桥装配时 `Events` 指向 `scanner.RaiseAccountEvent`。
2. `engine/llm_dispatch_test.go`：harness 捕获事件；新测试 `TestDispatchRaisesAccountEventOnAck` 钉四路径（ack→1 条含市场与单号、时刻=注入钟；提交失败=UNKNOWN→0 条；Shadow→0 条；PAPER 受理→1 条；nil seam→不炸）。
3. **诚实登记**：`Dispatch` 目前无生产调用方（PAPER/LIVE 决策→订单桥未装），seam 就位即待桥一并接线——已在 TASKS M2-13 行 / PROGRESS §1 写明，不假装数据面全活。

## 第五段：数量链盘口走价装配件（`M2-13` 收官 · RiskFacts 前置；同日续段尾三）

1. `engine/llm_sizing_walk.go`（新）——`sizingWalkFromDepth(depth)` 产出 `SizingInput.Walk`：**买卖两侧按 07 §5 复用 `orderbook` 的 quotation 遍历**（"ask"=投报价额到卖盘 `LiftTheAsksFromBest(notional,false)`；"bid"=从买盘换回报价额 `HitTheBidsFromBest(notional,true)`），返回跨档 `AverageOrderCost`（平均成交价，报价/基础）。**纪律：盘口填不满就拒绝**——`FullBookSideConsumed` 兼指"流动性吃穿"与"恰好停在最后一档"，两者都不可定价，拒绝而不是拿残缺平均价冒充可成交价；nil 盘/非法边/非法名义额各自拒绝。
2. `engine/llm_sizing_walk_test.go`：真 `orderbook.Depth` 快照——档内价（50→100）、跨档均值（150→150/1.4950495）、卖盘侧（100→9800/99）、填不满拒绝、恰停最后一档同拒绝、三坏输入、nil 盘不炸；5 组断言全过（0.992s）。

## 第六段：决策→订单桥第一件——`BuildIntentPlan`（同日续段尾四）

1. `engine/llm_intent_plan.go`（新）——`BuildIntentPlan(in *IntentPlanInput) (*IntentPlan, error)`：把**已落库决策记录 + 候选 + 行情材料 + 审批绑定**组装成 `IntentPlan`（`IntentRecorder.Record` 的入参）。急切校验：
   - **三重 ALLOW 生成期再核**：`Risk.Verdict == ALLOW`、`Sizing.Verdict == ALLOW`、动作 ∈ {OPEN_LONG, OPEN_SHORT, REDUCE}——ALLOW 唯一产地是硬风控，生产者不解释不修补；`Record.DecisionRunID`/`RiskEvaluationID` 必填（记录缺风险行＝链条不完整）。
   - **OrderType 由执行策略（调用方）给定**：`LIMIT` 要求 §5.4 材料四件齐（suggestedEntry/tick/bestBid/bestAsk——`limitPrice` 落库期做 tick 取整与盘口区间）；`MARKET` 免价材；`STOP_MARKET` 拒（保护性止损针对父意图另行准备）；其余拒。
   - **MarketID 生成期三段校验**（`splitIntentMarketID`），坏市场号不走到落库才炸；平仓点名现有仓位方向并**统一大写**（LONG/SHORT）。
2. `engine/llm_intent_plan_test.go`：LIMIT 开多端到端（plan 字段 → `BuildIntent` → `Price="50000"` 断言）、MARKET 无价 / REDUCE 归一化 + side/reduceOnly 断言、20 例拒绝表（含 nil 输入、二段市场号、缺 tick/bid/ask、STOP_MARKET、HOLD 动作）。
3. **常量收口（lint 驱动）**：新文件把 `"LONG"/"SHORT"` 顶破 goconst 阈值（6 处），顺手把 `"LIMIT"/"MARKET"/"STOP_MARKET"` 与 `"LONG"/"SHORT"` 收口为 `OrderType*` / `PositionSide*` 常量（跨 5 文件 12 处替换）——单一拼写口径，engine lint 回 0。

## 第七段：`RiskFacts` 链头——M5-08 账户事实生产者（同日续段尾五）

1. `engine/llm_account_facts.go`（新）——`buildSnapshotAccountFacts(ctx, src, item, now)`：把交易所真值读成 `snapshot.AccountFacts`（即 `llm_snapshot.go` 107-110 行所指"另一件"——快照的 `Account` 字段由调用方供给，因为引擎 futures 仓位类型没有 side，装配器不得发明方向）。逐启用合约单次摘 `GetFuturesPositionSummary`——**同一次读**同时给出账户级权益（EquityUSD/AvailableEquity 取自首个成功读数）与逐仓明细（AverageOpenPrice/MarkPrice/EstimatedLiquidationPrice/UnrealisedPNL/Leverage），账户与仓位不存在对不齐窗口。纪律：**读不到任一合约或空摘要 → 整体拒**（「读不到」不是「没有仓位」）；零仓位跳过；方向从 `CurrentSize` 符号推（正 LONG/负 SHORT）、数量取绝对；市场拼写 `snapshotPairDelimiter`（`BASE_QUOTE`，与旋转/审批同口径）；零启用合约 = 账户读不出 → 拒（不给零值）；`ReadAt`=注入钟。
2. `engine/llm_account_facts_test.go`：复用既有 `fakeSummarySource`——正例断言权益/无实现 PNL 求和/拼写/绝对数量与全字段；四拒绝子例（单对读失败整体拒、零合约拒绝、空摘要拒绝、缺钟与缺源）。
3. lint 一则 `unparam`（item 当前仅测试调用方传常值）——以带理由的定向 `nolint` 记录"装配器将按配置传值"。

## 第八段：`RiskFacts` B/C 组映射（同日续段尾六）

1. `engine/llm_risk_facts.go`（新）——快照 → 硬风控事实：
   - `riskDataFactsFromSnapshot`：**质量=全量已标注类别折叠最坏值**（`worstQuality` 借 `Downgrade` 的拒绝升级语义取两两 min——`rank` 是全序故折叠可交换；无标注快照=UNKNOWN——"未评估的事实不是可信事实"；给最坏值而非"是否降级"是因为 risk 检查的放行集（EXCHANGE_REST/DATABASE_CACHED）比 `IsDegraded` 更严，`PARTIAL` 不降级也不放行）；年龄=`FreshnessMS`；`CandleGap`=`Gaps` 非空；`OrderbookValid`=盘口非空（构建期 `Validate` 已拒交叉/空盘）；两龄上限=**政策显式输入**（≤0 拒——"报不出自己上限的事实集不是事实集"）；快照缺账户→拒。
   - `riskAccountFactsFromSnapshot`：快照账户（权益/可用）直映射 + venue 材料（保证金模式/预期/单向/日界基线/外部划转/两计数）**显式输入**；仓位逐条校验 `Side` ∈ LONG/SHORT、`MarketID`=candidate 拼写（`Symbol`）、数量非空；`ReadAt` 缺失/未来钟→拒（复用 `ErFactFromTheFuture` 哨兵）。
2. `engine/llm_risk_facts_test.go`：B 组正例（最坏值 DATABASE_CACHED / STALE 中选 / 空标注=UNKNOWN）+ 6 拒绝；C 组正例（字段逐个断言）+ 6 拒绝；共 3 函数。

## 第九段：账户事实进快照接缝 + 折叠修正（同日续段尾七）

1. **接缝接线**：`buildShadowRunRequest` 的 `Assemble` 闭包在取回交易所实例后，先过 `iPositionSummarySource` 守卫（不暴露则拒——`ErrRiskFactsInput`），再 `buildSnapshotAccountFacts(ctx, accountSource, assetItem, now)`（时钟=运行钟）并以 `SnapshotRequest{Account: …}` 交给构建器——**影子运行的快照自此携带交易所账户真值**（此前 `Account` 为 nil）。测试波及面经核为零：既有闭包测试只走「无交易所管理器 fail-closed」路径，新增读取在其后。
2. **折叠修正（真实缺陷）**：核查构建器发现其对调用方供给的账户**恒标 `LOCAL_TRACKED`**（`llm_snapshot.go` 503 行原注释）——原「全类别折叠」会把该标签纳入 → `RISK_SNAPSHOT_QUALITY`（放行集仅 EXCHANGE_REST/DATABASE_CACHED）**必拒每一次评估**。修正：`worstSnapshotQuality` **排除 `account`**（有专属检查 INVENTORY_STALE/NOT_RECONCILED）与 **`funding`**（专属检查 + 新鲜度刻意非阻断——慢资金费读取不该停掉大半个结算周期的交易）；仅排除类别被标注 → UNKNOWN（未评估市场事实不是可信事实）。测试同步：正例改期望 EXCHANGE_REST + 新增「LOCAL_TRACKED 账户与 STALE 资金费不得污染」与「仅排除类别被标注→UNKNOWN」断言。

## 第十段：`RiskFacts` A 组（授权）映射（同日续段尾八）

1. `engine/llm_risk_authorization.go`（新）——`riskAuthorizationFromState`：全部来源都是 `buildShadowRunRequest` 已经取到/构建过的材料（批准行、冻结策略 JSON、授权列表、运行世代、Jev manifest 绑定），**本层不新读任何东西**。要点：**缺批准 = `HasApproval=false` 如实申报**（其余字段留零，A 组各检查照 fail-closed 拒——「没有批准」是事实不是错误）；`MarketGranted` 复用 `hasActiveGrant`（llm_approval 既有单一口径）；`EnvelopeHashMatches` 用 `RiskEnvelopeHashOf` **对冻结原文重算**并与批准携带的哈希比对——审批后被换策略在此现形，而不是被当作「哈希自洽」；`MarketInScope`/`ActionAllowed` 从冻结策略 `MarketScopes` 查；`PairEpochMatches` 对运行世代。材料不全（无候选/冻结 JSON 不可解）**报错不猜**。
2. `engine/llm_risk_authorization_test.go`：一切为真正例 + 缺席（nil 批准）+ 六种字段级分歧（动作出范围/市场出范围/过期授权/已撤授权/哈希被换/世代不符）+ 四拒绝（nil 输入/无候选/坏 JSON/缺钟），1 函数多断言。

## 第十一段：`RiskFacts` D 组（市场上下文）映射（同日续段尾九）

1. `engine/llm_risk_market_facts.go`（新）——`riskMarketFactsFromSnapshot`：**盘口最优档优先、行情 Ticker 回落**（两个都是市场自身读数；都缺→字段留空，spread/滑点检查各自 fail-closed）；`LastPrice`=Ticker.Last；**`ATRPercentOfPrice` 从指标派生**（`ATR.Latest ÷ LastPrice`）——**读不到 ATR 或锚价即报错**：波动率闸开着时 0 会静默放行，那是 fail-open；资金费 `FundingRate`/`MinutesToFundingSettlement` 取自 Funding 事实（缺读数留 nil，`RiskFundingExtreme` 自己按 unreadable 拒）；`DepthAvailable`=盘口事实存在。**显式输入（库中/快照中无来源，禁发明）**：`MarkPrice`/`IndexPrice`（交易所侧读取属装配）、`EstimatedSlippageBPS`（走价+名义额上界链属装配——此处再算一份就是第二拼写）、`QuoteVolumeRank`（全宇宙排名面未有）、`FundingRequired`（策略意图）。
2. `engine/llm_risk_market_facts_test.go`：正例全字段 + 盘口缺→行情回落 + 资金费缺→nil + 六拒绝（nil 快照/缺输入/ATR 缺/ATR 坏/锚价缺/锚价零）。

## 第十二段：`RiskFacts` G 组（系统）映射（同日续段尾十）

1. `engine/llm_risk_system_facts.go`（新）——`riskSystemFactsFromState`：**能派生的派生、其余显式输入**。`GlobalHalt`/`MarketHalt` 从**活跃 halt 行**派生（`ClearedAt == nil`；SOFT/FULL=全局、MARKET 命中被裁决市场才成立——两个布尔一起进 `RiskHalted`，与检查语义一一对应）；`Mode`/`LiveAcknowledged`/`PairReadiness` 由调用方各自读一次后传入；`ModelBudgetExhausted`/`DatabaseHealthy`/`ClockDriftMs`/`RateLimitDegraded`/`OrderManagerUp`/`ExchangeManagerUp` **库中无统一现成读面 → 显式输入**（预算可由预约窗口推导、DB 可由连接探测、漂移可由交易所校时——各有归属，装配层只申报不发明）。纪律：**报不出自己限额即拒**（`MaxClockDriftMs` 必须为正）、负漂移=读不可信报错、模式/就绪缺=材料不全。
2. `engine/llm_risk_system_facts_test.go`：MARKET 命中/他人市场/两种全局 halt/已清除不计 + 显式透传 + 5 拒绝。

## 第十三段：`RiskFacts` E 组（频率）映射 + 窗口计数仓库查询（同日续段尾十一）

1. `database/repository/llm/order.go` 新增 `CountOrderIntentsInWindow(ctx, strategyVersionID, action, since)`——`llm_order_intents` 按窗口计数，版本/动作两个**可选等值过滤**（空串=不筛）、两方言单形状（pg 占位符改写 + `since.UTC()`、sqlite `tsToText`）；文档注明**候选单不计入**（「would this order breach」由装配侧折入）。测试 `TestCountOrderIntentsInWindow`（真库 4 组合：全量 3 / 版本 2 / 版本+动作 1 / 纯动作 1）。
2. `engine/llm_risk_frequency_facts.go`（新）——`riskFrequencyFactsFromState`：**E 组不再纯映射**——四个计数就地读库（仓库层聚合查询），其余显式输入、禁发明：
   - `NewRiskIntentsInWindow`=`llm_new_risk_counters` **未释放**行（M1-11 预算闸的账；撤单释放即出窗）；版本/全部/平仓通道三计数读 intents 表——**表无账户列**，全表即诚实口径里最大的「账户级」近似；
   - **候选订单折进计数**：检查是 `count <= max` 判过而候选行尚未落库——不折放行的就是第 max+1 条。折入口径=「候选若成单会出现在哪个计数里」：任何订单折版本+全部；开仓再折新风险计数；REDUCE 再折平仓通道计数（07 §5.2 平仓不耗新风险预算但有独立防风暴限额）；
   - 上限/冷却/连亏**无配置或读取面→显式输入**：`MaxVersionWindow`/`MaxOrdersPerWindow`/`MaxReduceOrdersPerWindow`（检查对 `<=0` 即「无闸」——0 照传不是发明）、`MarketCooldownElapsed`（上次开仓与冷却时长两个来源都没有）、`ConsecutiveLosses`/`MaxConsecutiveLosses`（成交结果账无读面；检查对 `<=0` 上限回退 `RiskMaxConsecutiveLosses`）。拒绝径：nil 输入/账本或版本缺/无 as-of/窗口非正/非出单动作（HOLD 无频率语义）/负数上限/负数连亏。
3. `engine/llm_risk_frequency_facts_test.go`：**计数与折入口径**（1 未释放 counter + 1 已释放 + 窗内开仓×1 + 窗内 REDUCE×1 + 窗外×1；开仓候选→新风险 2/版本 2/全部 3/平仓 1；REDUCE 候选→新风险 1/版本 2/全部 3/平仓 2，政策字段直通）+ **10 拒绝径**。两测试函数。

## 第十四段：`RiskContractSpec` 生产者 + 开关目录读面澄清（同日续段尾十二）

1. `engine/llm_contract_spec.go`（新）——`buildContractSpec(exchangeName, item, pair)`：§5.4 合约规格从**进程内执行限额仓库**（`limits.GetOrderExecutionLimits`，键 `key.NewExchangeAssetPair`）取档——**出处更正**：侦察单第 3 条写的 `GetFuturesContractDetails` 不对（`futures.Contract` 只有乘数/结算信息，无 tick/step/最小名义额）；限额仓库由各交易所 `UpdateOrderExecutionLimits` 灌入，是这三件的事实源。**tick/step 非正即拒**（§5.4 取整与数量对齐会静默变 no-op=fail-open）；**最小量/最小名义额 0 → 空串缺席**（仓库把「未声明」折叠成 0；检查对空串的公开语义=不过闸，报 "0" 会被读成「要求大于零」=伪造）；规格值 float64→十进制串用最短往返（`FormatFloat(v,'f',-1,64)`，与走价/回读同法）。
2. `engine/llm_contract_spec_test.go`：真实限额仓库五径——正例（0.1/0.001/0.002/5 精确往返）、缺席（未声明→空串）、tick 缺拒、step 缺拒（各自 `ErrRiskFactsInput`）、未装载键拒（`limits.ErrOrderLimitNotFound`）。测试用独占 venue（`ContractSpecVenue`）避免与其它测试的全局限额键冲突。
3. **开关目录读面澄清**：`RiskSwitchService.Current()` 已返回填好默认的 `RiskSwitches`——RiskFacts 装配侧一行消费即可，「开关目录映射」作为独立装配件不成立；仅余清单收敛为**桥的接线件**。

## 第十五段：24h quoteVolume 排名取数件（同日续段尾十四）

1. `engine/llm_volume_rank.go`（新）——`quoteVolumeRanks(exchangeName, item, tickers)`：§5.6 `liquidityTopNOnly` 的取数件。背景：检查 `RiskMarketLowLiquidity` 只在 `rank > 0 && rank <= 50` 时放行，而开关**默认 ON**——排名缺席不是「没闸」而是闭闸（前哨更新第 10 条 ② 的实锤缺口）。语义：过滤本场所本资产（`strings.EqualFold` 宽松）→ quoteVolume 降序、**同量按 `llmMarketID` 对名升序**去随机（同批 ticker 两次调用必须给出同一张表——审计里的排名要可复现）；零/负面量照排（票数不足 50 的市场「进前 50」是文档语义，量小由检查上限说话、不由排名隐藏）；键 = `llmMarketID` 全仓同拼写；**空宇宙（该场所该资产一个 ticker 都没有）→ 报错**（报 0 会让「没读到」与「排名很差」长得一样）。
2. `engine/llm_volume_rank_test.go`：排序（BTC 100 > ETH/SOL 50 > DOGE 0）；平手确定性（ETH_USDT < SOL_USDT 按对名）；异场所（Kraken）与异资产（spot）排除；nil 元素跳过；场所名大小写宽松；乱序输入同表；空宇宙拒（`ErrRiskFactsInput`）——首跑自撞一处夹具错：负例误用了**含** ticker 的场所（Kraken 在夹具里数量 999），改真正缺席的场所以后即好。
3. **装配期遗留一验**：数据面 = 引擎 ticker 缓存（`GetAllActiveTickers` → `GetCachedTicker` 路径）；USDT-M 的 ticker 是否实际进缓存取决于 bot 的 ticker 更新任务是否覆盖该资产——闭包装配时先验证，未覆盖则为排名件补喂数路径。

## 第十六段：§5.2 D 滑点估算件（同日续段尾十五）

1. `engine/llm_slippage_bps.go`（新）——`estimatedSlippageBPS(walk, side, notionalUSD, bestQuote)`：`RiskSlippageExceeded` 的 `EstimatedSlippageBPS` 装配件（`llm_risk_market_facts.go:111` 原注「估算属装配——它持有走价与名义额上界链」）。口径：按**审批名义额上界**走价（§5.3 第 4 步参考价语义）与最优档比——**ask=(均价−最优)÷最优×10000、bid 反向**（卖出吃买盘，成交均价天然低于最优买价；首跑正因把 bid 也按 ask 方向比而失败——方向是侧的属性，已分支修正）；**两侧各有「错误一侧」即拒**（ask 低于最优卖价 / bid 高于最优买价 = 盘口不一致——报 0 会静默通过检查）；**向上取整**（检查「超过阈值即拒」，高报是 fail-closed 方向）；最优档缺失/非正、走价不可读、未知侧各自拒。
2. `engine/llm_slippage_bps_test.go`：ask 5bps / bid 10bps 两个正例、任何零头向上取整（0.01bps→1）、平坦 0、双侧错误一侧各自拒、未知侧拒、nil walk / 空最优档 / 非正最优档 / 走价报错（透传非哨兵）——首跑两自撞：①bid 正例撞方向错（见上，实现修正）；②「未知侧」用例喂了会在 unknown side 上报错的脚本 walk——改宽容 walk 让本函数的侧校验成为被证对象；unparam 撞 `notionalUSD 恒为 50`——用例改双值（50/75）以自然消警。
3. **前哨第 10 条 ③ 兑现**：装配先决三件（两龄常量/排名取数件/滑点估算）自此全部落地。

## 第十七段：premiumIndex 事实件——先决 ④ 兑现（同日续段尾十七）

1. `engine/llm_funding_facts.go`（新）——`iPremiumIndexSource`（`GetName`+`UGetMarkPrice`，binance 结构性满足）+ `buildFundingMarkIndexFacts(ctx, src, pair, now)`：**一次 premiumIndex 读同给两件**——①`snapshot.FundingFacts`（`LatestRate`=带符号费率最短十进制、`NextFundingTime`、`ReadAt`=now；`History` 留空——premiumIndex 无历史且 `validate` 允许空，有界性靠 `MaxFundingRates`）②`MarkPrice`/`IndexPrice` 十进制串（D 组两个显式输入的来源）。**读不到即拒**：nil 源/空回包/非正 mark/index（零值会让偏差检查把可读事实当异常行情拒掉——错误方向也在撒谎）；venue 错误透传非哨兵。类型坑：`types.Number`=float64 命名类型（`.Float64()`）、`types.Time`=`type Time time.Time` + `.Time()` 方法（不是内嵌字段——首跑编译错暴露，用 `types.Time(settles)` 转换构造）。
2. **快照接缝**：`SnapshotRequest.Funding`（新字段）+ builder `applyFunding`（复制上快照 + `SetQuality(CategoryFunding, EXCHANGE_REST)`）——**质量语义与 account 块刻意不同**：account 是本地缓存视图（LOCAL_TRACKED、风控前须复核），funding 是场地直读（EXCHANGE_REST）。挂在 `applyAccount` 之后（llm_snapshot.go:217 区）。
3. `engine/llm_funding_facts_test.go`：正例（符号保留/结算时刻/读时刻/mark/index 串）+ 5 拒绝（nil 源/venue 错误非哨兵/空回包/零 mark/零 index）+ `TestApplyFunding`（落快照+EXCHANGE_REST；**缺席读不标注=UNKNOWN**——不把「没读」伪装成评级）。首跑自撞：收尾时在文件尾误留占位垃圾（假 helper）——立即清除重写；`types.Time` 形状错一处——见上。
4. **装配先决四件自此全部到位**（①Q9 常量 ②排名取数件 ③滑点估算 ④premiumIndex 件）——闭包装配与桥只剩接线本体。

## 第十八段：仓储侧账户态事实件（同日续段尾十九）

1. `engine/llm_account_state_facts.go`（新）——`buildRiskAccountStateFacts(ctx, accountID, now)`：`RiskAccountFactsInput`（llm_risk_facts.go:53-63 的九字段）中**仓储可读的三件切片**：①`DayStartEquityUSD`=当日（`UtcDayKey`）已封基线（`GetDayStartInventory`；**`ErrNotFound`/nil 都如实留 nil**——「未封基线」是法律状态，检查按 `RISK_DAILY_LOSS_UNKNOWN` 拒；本层发明 0 才是伪造）②`UnresolvedUnknownOrders`=`ListOrderIntentsByStatus(IntentStatusUnknown)` 计数（intents 表无账户列——与 E 组频率计数同一诚实口径的全表近似）③`ExternalFlowsUSD` **刻意不在此件**（净额 ± 加减属 `DayStartService.DailyLoss` 既有公式：llm_day_start.go:220-239；装配侧再算一遍就是第二拼写）。拒绝径：空账户/零 as-of。
2. `engine/llm_account_state_facts_test.go`：真库两段——未封基线（nil 非 0 + 0 未知）+ 封基线/两条 UNKNOWN/一条 PREPARED 后（基线指针值 + 只数 UNKNOWN）+ 两拒绝径。
3. **闭包装配的前件又添一块**：账户态（仓储侧）从「显式输入句柄」变成有生产者；其余 `RiskAccountFactsInput` 字段（MarginType/ExpectedMarginType/HedgeMode/TargetAccountID/UnreconciledPartials）仍属调用方/场地材料。

## 第十九段：决策门（02）——自动执行单型（同日续段尾二十）

切闭包装配前把侦察第 6 条点名的顺序问题摆上桌：`RiskOrderFacts.OrderType/Price/NotionalUSD` 的定型压在执行计划之前（shadow 评估先于计划生成），而「自动执行到底用 LIMIT 还是 MARKET」**设计文档两处都保留两种可能**——07 §5.4 价格表 `LIMIT`/`MARKET` 两行并存、§7.1 状态机只画 `→ RESTING（挂单中，LIMIT）` 一条路径；24 项已定案里没有这一条。这属「钱怎么花」的执行语义、且会进审批语义（suggestedEntry=入场计划价），模型自行选默认 = 替用户把钱放进没选过的路径——按流程**不自行拍板**：写入 `decisions/02-auto-execution-order-type.md`（三选项：全 LIMIT / 开仓 LIMIT+平仓 MARKET（建议 B）/ 全 MARKET；含取舍、建议理由与不决策后果——SHADOW 停 awaiting-verdict 合法、PAPER/LIVE 桥待拍板，**其余一切不受阻**）。`decisions/README.md` 当前阻塞项登记 02，TASKS M2-13 尾与 §1 指针同步标注。

## 第二十段：SizingInput 装配件——闭包元组第二件先行（同日续段尾二十四）

1. `engine/llm_shadow_sizing.go`（新）——`buildShadowSizingInput(*ShadowSizingMaterial)`：闭包返回元组第二件的装配本体，**不依赖单型决策 02**（sizing 链与 OrderType 无关）。三块材料单一口径、零第二拼写：①敞口=`existingExposure(positions)`（保守名义额 max(entry,mark)×qty——与 C 组敞口检查同一句式，账实相加只有一份）②日损=`DayStartService.DailyLoss` 的产物（调用方传入——服务实例挂 bot 上，本件不持有）③止损距离=`ProtectiveStopPrice`（Q8b 公式 + 向远离市场取整）再取 |entry−stop|（与真实挂出的保护性止损同一句话）。只装开仓——非 OPEN_LONG/OPEN_SHORT 直接拒（退出数量归退出策略 07 §7，闭包对 REDUCE 传 nil 即可）；缺锚价/ATR/tick/日损、仓位行读不清各自拒。
2. `engine/llm_shadow_sizing_test.go`：正例 LONG（entry 100、ATR 2 → 距离 3；仓 0.5@110 → 敞口 55；walk 透传并可用）+ SHORT（止损在入场上方）+ 8 拒绝径（nil 材料/nil walk/REDUCE/空锚价/ATR 缺/ATR 坏（非哨兵）/日损空/tick 空/仓位读不清）。首跑即绿；唯一磕绊=misc 抓出 assert 消息里 `2%=2` 的百分号触发 f 变体检查——改写措辞即好（**教训续：写 assert 消息别带 `%`**）。
3. **闭包元组第二件就位**；第一件（`RiskInput`）的 Order 三字段仍受 02 门，其余全部已核可接线（第 11 条清单）。

## 第二十一段：RiskInput 组装件——元组第一件的可决部分（同日续段尾二十五）

1. `engine/llm_shadow_risk_input.go`（新）——`buildShadowRiskInput(*ShadowRiskInputMaterial)`：闭包元组第一件的组装本体。**十四个字段：十三路直取已落映射件**（Authorization/Data/Account/Positions/Frequency/Contract/Market/System/Switches/Candidate/Envelope/Now——各自映射件负责非零，本件守「材料齐」一道：候选/时钟/合约规格缺一即拒）；**两件组装期决定单点化**：①`CheckVersion` 由 `RiskCheckVersion` 常量盖章——检查表版本是编译期事实，不留任何外部输入面；②`Order` **以参数进入**——`RiskOrderFacts` 定型压在单型决策 02 上（侦察第 6 条不许编造），拍板后由调用方构造、本件只合流。
2. `engine/llm_shadow_risk_input_test.go`：全字段落位正例（含 `RiskCheckVersion` 盖章与 `Order` 透传）+ 4 拒绝径（nil/无候选/无时钟/空合约规格）。首跑一处 gofmt 修形。
3. **闭包两件（`buildShadowRiskInput` + `buildShadowSizingInput`）自此均为「材料齐→即插」形态**——拍板 02 后闭包的剩余工作压缩为：raise 材料（按第 11 条清单）、构造 `RiskOrderFacts`（02 定的单型）、塞进 `llm_decision_facts.go:213` 的闭包位。

## 第二十二段：premiumIndex 读面进 Assemble 闭包（同日续段尾二十六）

1. `engine/llm_decision_facts.go` 的 `Assemble` 闭包接线：`source.(iPremiumIndexSource)` 守卫（**同 `iPositionSummarySource` 同式**——交易所不暴露即拒，fail-closed）→ `buildFundingMarkIndexFacts(ctx, premiumSource, targetPair, now)` 一次读 → 两消费者：①`SnapshotRequest.Funding` 上快照（**影子快照自此真正携带 funding 事实**——D 组资金费检查与快照 funding 类别此前恒空挂；`applyFunding` 标 EXCHANGE_REST）②`shadowRunFacts.premium` 存 mark/index（02 拍板后的 RiskFacts 闭包直取 D 组两输入）。
2. 测试策略：成功路径需真交易所（既有约定——`TestBuildShadowRunRequest*` 两测试以「无交易所必拒」为界并复跑全绿）；本接线与账户接缝（尾七）同式，类型断言编译期即契约。
3. **非 02 门的最后接线缺口已清**：拍板 02 后闭包剩余 = raise 材料（第 11 条清单）+ 构造 `RiskOrderFacts` + 塞 `llm_decision_facts.go:213` 闭包位；桥的接线件为后续件。

## 第二十三段：冷却检查的数据侧——`LastEntryIntentAt`（同日续段尾二十七）

1. `database/repository/llm/order.go` 新增 `LastEntryIntentAt(ctx, exchange, pair, asset)`——`llm_order_intents` 按市场取最近**开仓**意图时刻：`action IN (OPEN_LONG, OPEN_SHORT)`（REDUCE 不是开仓、不计）；无行 → `ok=false`（从未开过仓=过了任何冷却）；两方言共用 `MAX(created_at)`（sqlite `sql.NullString`+`tsFromText` / pg `sql.NullTime`）。
2. `database/repository/llm/llm_domains_test.go` 追加 `TestLastEntryIntentAt`：真库五种子（10 分钟前 OPEN_LONG / 1 分钟前 REDUCE / 5 分钟前 OPEN_SHORT / 他场所 / 他市场）→ 断言取到 5 分钟前那笔（**更近的 REDUCE 与他场所/他市场都不干扰**）+ 从未开过仓 → `ok=false`。首跑一处 gofmt 修形。
3. **`MarketCooldownElapsed` 的「上次开仓」半自此可读**（映射件注释同步更新：时长半仍为政策输入——装配期定值并记录理由）。**闭包装配前清单再添一孔：E 组冷却数据侧已闭**。

## 第二十四段：账户模式出口——`venueAccountModes`（同日续段尾二十九）

1. C 组地形侦察（尾二十九前半）钉死四件事：`PositionSummary` 同读自带 `MarginType`；`collateral.Mode` 是**多资产保证金**（≠对冲模式，禁作 HedgeMode）；**全包无 dualSide 读面**（HedgeMode 二选一收口）；`ExpectedMarginType` 审批与 cfg 均无来源（补配置/显式输入）。
2. 兑现其中可直取的一件：`buildSnapshotAccountFacts` **增返回 `*venueAccountModes`**（`MarginType`=`summary.MarginType.String()`——与权益同读、首个成功读支付，注释与权益句式并列；**`HedgeMode` 刻意留在结构外**：无读面、装配会话二选一收口）+ `shadowRunFacts.accountModes` 存出口。签名更替仅动三处：生产者、`Assemble` 闭包、本件测试（5 处调用点 + CROSS 值断言）。
3. **C 组四字段的现状**：`MarginType` **有出口**（本段）；`HedgeMode`/`ExpectedMarginType` 二选一/补配置（地形已钉，装配会话照办）；`TargetAccountID`（取 approval.AccountID 的对照）/`UnreconciledPartials`（对账侧读面）仍属装配期定源。

## 第二十五段：对冲模式读面——HedgeMode 二选一取 a（同日续段尾三十）

1. 地形（尾二十九 ③）的二选一**取 a：真实事实胜过静默假设**。binance `binance_ufutures.go`：端点常量 `uFuturesDualSidePosition = "/fapi/v1/positionSide/dual"` + `UGetDualSidePosition(ctx)`——**同 `GetAssetsMode` 形制**（认证 GET + 匿名响应体 `dualSidePosition`；**写侧刻意不暴露**：模式切换不经本层）。
2. 引擎侧 `llm_account_facts.go`：seam `iDualSideSource`（`GetName`+`UGetDualSidePosition`，binance 结构性满足）+ `readVenueHedgeMode`（**nil 源拒；venue 错误透传非哨兵**——读失败不是坏输入）+ `venueAccountModes.HedgeMode` 兑现（「刻意缺席」注释同批更替为「由独立读填充」）。`Assemble` 闭包（同 premiumIndex 式守卫）：`source.(iDualSideSource)` → 读 → `f.accountModes.HedgeMode`。
3. `engine/llm_account_facts_test.go` 追加 `TestReadVenueHedgeMode`（true/false/nil/venue 错 4 例）。binance 侧无独立测试（同形镜像既有已测模式；**测试包编译核查** + vet + lint 全过——纯方法新增、未动能力位/IBotExchange）。
4. **C 组四字段现状**：`MarginType`/`HedgeMode` **双出口就位**；`ExpectedMarginType`（补配置或显式输入——装配决策）与 `TargetAccountID`/`UnreconciledPartials` 定源留装配。

## 第二十六段：未对账计数件——`CountOrderIntentsByStatus`（同日续段尾三十二）

1. `database/repository/llm/order.go` 新增 `CountOrderIntentsByStatus(ctx, status)`：生命周期状态计数（两方言 `COUNT(*)`）；测试 `TestCountOrderIntentsByStatus`（真库：UNKNOWN×2 → 2、缺状态 → **0 而非全量**）。
2. `engine/llm_account_state_facts.go`：`riskAccountState` 补第三孔 `UnreconciledPartials`——**`PARTIALLY_FILLED` 状态即「未对账部分成交」的诚实对应**（状态自身语义；全表范围同前）；同时把 UNKNOWN 从「列举后取 len」改走同一计数（少一次全行读）；引擎真库测试加 partial 断言。
3. **C 组四字段现状**：`MarginType`（同读直取）/`HedgeMode`（dualSide 读）/`UnreconciledPartials`（本件）**三出口就位**；仅 `ExpectedMarginType`（03 门）与 `TargetAccountID`（取 `approval.AccountID` 的对照——装配一行）留装配。

## 第二十七段：时钟漂移读面——G 组「需补读面」两件之一（同日续段尾三十四）

1. `engine/llm_clock_drift.go`（新）——`iServerTimeSource`（binance `UServerTime` 结构性满足）+ `readClockDriftMs(ctx, src, now)`：**漂移=|venue 时间 − 本地时间| 毫秒幅值**——G 组映射对负值报「不可读」，检查侧 `±limit` 双边界只等幅值来填，故幅值即唯一合法形状（取相位丢失是刻意的，理由在注释）；nil 源/零 as-of/零回包/venue 错误全拒——**报 0 会静默过漂移闸=fail-open**。
2. `engine/llm_clock_drift_test.go`：正负两向幅值（+1500/−250）、零漂移、四拒绝径（nil/零 as-of/零回包/venue 错非哨兵）。首跑即绿。
3. **取值表⑤同步**：`ClockDriftMs` 由「需补读面」改为**本件直取**；G 组「需补读面」仅余 `RateLimitDegraded`（venue 限频状态——装配期定源或 fail-closed 值并记录理由）。

## 第二十八段：dualSide venue 侧测试补缺（同日续段尾三十五）

1. `exchanges/binance/binance_test.go` 增 `TestUGetDualSidePosition`——`sharedtestvalues.SkipTestIfCredentialsUnset(t, e)` + `assert.NoError`（**同 `TestGetAssetsMode` 惯例**：凭证在位跑活体、不在位即跳过）。尾三十一「同形镜像、无独立测试」的口子闭合。
2. 顺带侦察结论（限频）：`LLMRuntimeFacts` 仅四读面（Config/Rotation/Switches/Now）、`binance/ratelimit.go` 只给限额定义——**`RateLimitDegraded` 的利用率读面在 requester 层，属独立件**——维持取值表⑤「装配期定源或 fail-closed 显式值」的登记。

## 验证证据

- `go test ./engine/ -count 1 -run 'TestM6BackupRestoreDrill'` → **ok**（1.573s）；`-count 2` 复跑 1.441s / 1.495s。
- 与 `TestM6CrashDrillKill9MidSubmitting` 同跑不互扰（全局 `testhelpers.TempDir` 纪律：父进程不碰、子进程自设）。
- `go vet ./engine/` 0 · `gofmt -l` 无输出 · engine 整套（-skip 网络类）**全绿（90.8s）** · `make misc_checks` All passed · markdownlint 0 issues。
- 第二段：`go test ./engine/ -count 1 -run 'TestMarketDelistWatch|TestSetupRotationAttachesTheDelistWatch'` → **ok**（1.134s）；`gofmt -l` 无输出；`go build ./...` BUILD_OK；`make misc_checks` All passed（首跑抓掉本段自撞的两处：assert 消息里的 must ×4 与 workLog 里一个 U+3000——已修）；**engine 整套（-skip 网络类）引入下架监视后复跑全绿（93.2s，较第一段 90.8s +2.4s）**。
- 第三段：`go test ./engine/ -count 1 -run 'TestTriggerConsumer'` → **ok**（2.425s）；四家族连跑 `ok 2.929s`；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l`/`go build ./...` 无输出/BUILD_OK；engine 整套（-skip 网络类）含消费者**全绿（92.1s）**。
- 第四段：`go test ./engine/ -count 1 -run 'TestDispatch'` → **ok**（2.261s，含新 seam 测试）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（89.7s）**。
- **会话级（竞态）**：`go test -race ./engine/ -count 1 -skip '<网络类>'` → **ok（109.9s）**——本会话全部新增并发路径（下架监视预装配读、消费循环×扫描器队列、去抖钟）在竞态检测器下全绿；跨 goroutine 队列契约（`Scan`/`Drain` 同一把 `s.mu`、`Drain` 复制不共享底层数组）经代码核对。
- 第五段：`go test ./engine/ -count 1 -run 'TestSizingWalkFromDepth'` → **ok**（0.992s）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed（首跑抓掉 assert 消息里一个 must——已修）；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（93.0s）**。
- 第六段：`go test ./engine/ -count 1 -run 'TestBuildIntentPlan'` → **ok**（2.261s，3 函数 20+ 子例）；受影响家族（Intent/Dispatch/ProtectiveExit/Reconcile/Risk）连跑 **ok**（1.298s）；`golangci-lint run ./engine/...` → **0 issues**（含常量收口后）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（92.2s）**。
- 第七段：`go test ./engine/ -count 1 -run 'TestBuildSnapshotAccountFacts'` → **ok**（2.085s）；`golangci-lint run ./engine/...` → **0 issues**（一处带理由 nolint）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（91.7s）**。
- 第八段：`go test ./engine/ -count 1 -run 'TestRiskDataFactsFromSnapshot|TestRiskAccountFactsFromSnapshot'` → **ok**（2.297s）；`golangci-lint run ./engine/...` → **0 issues**（gofumpt 单 var 块修形）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（89.2s）**。
- 第九段：四家族连跑（ShadowRunRequest/RiskDataFacts/RiskAccountFacts/AccountFacts）→ **ok**（2.391s；折叠修正后复跑 2.340s）；`golangci-lint run ./engine/...` → **0 issues**（过户后 nolint 变冗余被 nolintlint 抓出——已移除）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（90.5s）**。
- 第十段：`go test ./engine/ -count 1 -run 'TestRiskAuthorizationFromState'` → **ok**（2.613s）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed（首跑抓掉 assert 消息里一个 must——已修）；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（93.5s）**。
- 第十一段：`go test ./engine/ -count 1 -run 'TestRiskMarketFactsFromSnapshot'` → **ok**（1.008s；lint 修形后复跑 1.031s）；`golangci-lint run ./engine/...` → **0 issues**（modernize 一处 max 修形）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（90.5s）**。
- 第十二段：`go test ./engine/ -count 1 -run 'TestRiskSystemFactsFromState'` → **ok**（2.045s）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./...` BUILD_OK；engine 整套（-skip 网络类）**全绿（94.1s）**。
- 第十三段：仓库 `go test ./database/repository/llm/ -count 1` → **ok**（2.416s，含 `TestCountOrderIntentsInWindow` 4 组合口径）；`go test ./engine/ -count 1 -run 'TestRiskFrequencyFacts'` → **ok**（2.648s，2 函数 12 子例）；`golangci-lint run ./engine/... ./database/repository/llm/...`（单命令双包）→ **0 issues**；`make misc_checks` All passed（webUI node_modules/dist 暂存后跑再还原）；`gofmt -l` 无输出；`go build ./engine/... ./database/repository/llm/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（94.1s，实测 94.091s）**。
- 第十四段：`go test ./engine/ -count 1 -run 'TestBuildContractSpec'` → **ok**（2.288s，5 径）；`golangci-lint run ./engine/...` → **0 issues**（首跑 3 条自撞：testifylint `Equal("",…)`×2 → `Empty`、unparam venue 常量 → 带理由 `//nolint:unparam`）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（91.3s，实测 91.278s）**。
- 尾十三（装配前哨 + Q9 常量）：`go test ./engine/ -count 1 -run 'TestRiskThresholdsMatchTheDocs'` → **ok**（2.366s）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK（断言追加进既有测试函数——engine 测试函数数不变 359）。
- 第十五段：`go test ./engine/ -count 1 -run 'TestQuoteVolumeRanks'` → **ok**（1.018s；首跑一处夹具自误——负例场所恰在夹具里含 ticker——换真正缺席场所后复跑即好）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（89.2s，实测 89.155s）**。
- 第十六段：`go test ./engine/ -count 1 -run 'TestEstimatedSlippageBPS'` → **ok**（1.058s；首跑两自撞——bid 方向错已修实现、unknown-side 用例改宽容 walk；unparam 以双 notional 值自然消警）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（77.0s，实测 77.034s）**。
- 第十七段：`go test ./engine/ -count 1 -run 'TestBuildFundingMarkIndexFacts|TestApplyFunding'` → **ok**（2.407s，2 函数 10 断言；首跑一处编译错——`types.Time` 是命名类型+方法非内嵌字段，修法见上）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（93.7s，实测 93.670s）**。
- 第十八段：`go test ./engine/ -count 1 -run 'TestBuildRiskAccountStateFacts'` → **ok**（2.454s，真库两段 + 两拒绝径）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（97.7s，实测 97.684s）**。
- 尾二十二/二十三（清单核毕 + **完成度审计快照**，session 收口实机验证）：`webUI`: `pnpm exec tsc --noEmit` → RC=0；`pnpm lint`（oxlint）→ **0 warnings / 0 errors**（30 文件 × 96 规则）；全仓 `go build ./...` → **OK**；`markdownlint-cli2` taskProgress → **62 文件 0 issues**。**审计结论（对照原始目标）**：前端平台 = W 组 8/8 全交付且实机全绿；决策系统 = M0/M2/M3/M4/M5 全组 + M6 8/11 + engine 364 测试/整套绿——**未完成项恰为两扇用户决策门**：(02) 自动执行单型（闭包 `RiskOrderFacts` 定型 + PAPER/LIVE 桥）、(01) 测试网凭据（M6-03/04/05 + M1-15）——两者均在 `decisions/` 备好选项与后果，**不满足「全部交付」判据，目标保持进行中**。
- 第二十段：`go test ./engine/ -count 1 -run 'TestBuildShadowSizingInput'` → **ok**（2.098s，1 函数 12 例；首跑即绿）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` 首跑抓一处（assert 消息 `2%=2` 的 `%` 触发 f 变体检查——改写措辞）后 All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（87.2s，实测 87.188s）**。
- 第二十一段：`go test ./engine/ -count 1 -run 'TestBuildShadowRiskInput'` → **ok**（0.800s，7 例；首跑一处 gofmt 修形）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（89.7s，实测 89.708s）**。
- 第二十二段：`go test ./engine/ -count 1 -run 'TestBuildShadowRunRequest|TestBuildFundingMarkIndexFacts'` → **ok**（2.433s，既有装配族 + funding 件全过）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（86.7s，实测 86.699s）**。
- 第二十三段：仓库 `go test ./database/repository/llm/ -count=1` → **ok**（1.549s，全包含 `TestLastEntryIntentAt` 新例）；`golangci-lint run ./database/repository/llm/...` → **0 issues**（首跑 gofmt 修形后清）；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./database/repository/llm/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（86.7s，实测 86.747s）**。
- 第二十四段：`go test ./engine/ -count 1 -run 'TestBuildSnapshotAccountFacts|TestBuildShadowRunRequest'` → **ok**（2.467s；签名更替后全族复跑；首跑一处 `margin.Cross` 常量名错——实为 `margin.Multi`）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（84.5s，实测 84.495s）**。
- 第二十五段：`go test ./engine/ -count 1 -run 'TestReadVenueHedgeMode|TestBuildSnapshotAccountFacts|TestBuildShadowRunRequest'` → **ok**（2.699s，含新 4 例）；binance 测试包编译核查 → **ok**（[no tests to run]）；`go vet ./exchanges/binance/` 0；`golangci-lint run ./engine/... ./exchanges/binance/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./exchanges/binance/ ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（85.5s，实测 85.466s）**。
- 第二十六段：`go test ./database/repository/llm/ -count=1` → **ok**（1.984s，含 `TestCountOrderIntentsByStatus` 新例）；`go test ./engine/ -count 1 -run 'TestBuildRiskAccountStateFacts'` → **ok**（1.005s，含 partial 断言）；`golangci-lint run ./engine/... ./database/repository/llm/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/... ./database/repository/llm/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（83.5s，实测 83.522s）**。
- 第二十七段：`go test ./engine/ -count 1 -run 'TestReadClockDriftMs'` → **ok**（2.213s，7 例；首跑即绿）；`golangci-lint run ./engine/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；`go build ./engine/...` BUILD_OK；engine 整套（-skip 网络类）**全绿（88.2s，实测 88.210s）**；markdownlint 首跑抓一处（TASKS 表格内 `|…|` 绝对值符号致列数错——改文字表述后 63 文件 0 issues）。
- 第二十八段：binance `go test -run 'TestUGetDualSidePosition' -count=1` → **ok**（2.269s，无凭证按惯例跳过）；`golangci-lint run ./exchanges/binance/...` → **0 issues**；`make misc_checks` All passed；`gofmt -l` 无输出；engine 整套（-skip 网络类）**全绿（85.9s，实测 85.857s）**；markdownlint 63 文件 0 issues。
- 尾三十六（**审计刷新**，尾二十三后累计 12 件增量的收口复验）：`webUI`: `pnpm exec tsc --noEmit` → RC=0；`pnpm lint`（oxlint）→ **0 warnings / 0 errors**（30 文件 × 96 规则）；全仓 `go build ./...` → **OK**；`go vet ./engine/ ./database/repository/llm/ ./exchanges/binance/` → 0；engine 整套（-skip 网络类，同刻）**85.9s 全绿**。**结论**：前端平台与全部已落 Go 件实机复核无损；剩余仍唯三扇用户决策门（02/03/01）。

## 遇到的坑与解决

1. **vault kind 字面量**：`vault.Add` 的 kind 必须是 `llm`（生产 `vaultSecretKind`）——传 `ProviderKindLLM`（`LLM_OPENAI_COMPAT`）或 `jev` 都被 `AddProviderRevision` 拒（`ErrInvalidSecretRef`）；**两类 provider 的密钥都进 `llm` 命名空间**。
2. **配置布局与复制目标对齐**：`copyTree(configDirA, medium)`（而不是 `medium/config`）——子进程按 datadir 根布局读 `config.json`；copyTree 对单文件 src 同样工作（WalkDir 单文件 + rel="."）。
3. **变量名撞车**：`out` 已被预算结算的 token 变量占用（`in, out, latency`），子进程输出改名 `childOut`。
4. 类型细节：`llmstore.AuditEvent.Subject` 是 `*string`（`new("m6-08")`）；`AppendChatMessage` 返回 `(int64, error)`。
5. **`make misc_checks` 首跑 rc=2**——本会话首跑发现三类存量违规（session-42~49 写下的）：①6 处「format 参数但缺 `f` 变体」（`assert.True/False/NotContains` → `Truef/Falsef/NotContainsf`，含本会话新测试 1 处）；②9 处「assert 消息里含 must」（应为 should；require 才用 must）；③session-48 workLog 3 行里的 **U+3000 全角空格**（invisible-Unicode 检查拒——`Jev · SYSTEMONE 6 次` 这类排版空格）。全部修复后 **All passed**。**教训：M6-10 的绿是 session-25 测的——之后每个新增测试文件/新增台账的会话都要跑一遍 misc_checks，别信任「上次绿过」**。

## 未完成 / 下一步建议

- **SHADOW `RiskFacts` 生产装配**（把已就位的全部件装进 `ShadowRunRequest.RiskFacts` 闭包——链头（尾五）、B/C（尾六）、接缝+折叠修正（尾七）、A（尾八）、D（尾九）、G（尾十）、E（尾十一）、合约规格（尾十二）、排名取数件（尾十四）与数量链 `Walk`（尾三）悉数就位；**仅余：闭包装配 + 桥的接线件**——开关目录读面已备（`RiskSwitchService.Current()` 填好默认返回）；排名装配剩一验：USDT-M ticker 是否实际进缓存）——决策链路唯一剩余前置。
- **PAPER/LIVE 决策→订单桥**（决策行→`BuildIntentPlan`→`IntentRecorder.Record`→`OrderDispatcher.Dispatch`——`Dispatch` 尚无生产调用方；**账户事件 seam（`OrderDispatcher.Events`）随桥激活**；F 组订单侧字段（`RiskOrderFacts.OrderType/Price/NotionalUSD`）溯源到该计划，桥之前不许编造）。
- 凭据门：M6-03/04/05、M1-15（decisions/01）。

## RiskFacts 装配的下轮侦察结论（写给下一会话）

`RiskFacts` 闭包（`ShadowRunRequest.RiskFacts`）需产出 `(*RiskInput, *SizingInput, error)`；侦察结论（**进度：第 1/2 条与第 3 条的账户/授权/市场/系统/频率映射 + `RiskContractSpec` 均已兑现——尾五~尾十二（`RiskContractSpec` 的出处经尾十二更正为限额仓库，非 `GetFuturesContractDetails`）；`RiskOrderFacts` 随桥**）：

1. **已就位的零件**：`SizingInput.Walk`（`sizingWalkFromDepth`，本会话）——装配时从决策用盘口快照建 Depth 传入。
2. **快照已带大半事实**：`SnapshotResult.Snapshot`（`snapshot.MarketSnapshot`）已含 `Ticker/OrderBook/Funding/Account` 四组事实 + `Quality/FreshnessMS/Gaps/Indicators`——`RiskMarketFacts`/`RiskDataFacts` 的多数字段可直接映射；**账户侧 `Account *AccountFacts` 的装配明确是 M5-08 的活**（`llm_snapshot.go` 107-110 行原注释：引擎的 futures 仓位类型不带 side，"invented side is a fabrication the risk engine then trusts"——不要在装配器里补 side）。**（尾十六更正：其中「Funding」组**不是**构建器产物——`engine/llm_snapshot.go` 零 funding 引用、全仓非测试代码无 `FundingFacts` 构造点；D 组映射对 `snap.Funding` 的消费目前恒 nil（nil-safe 留空）。Account 侧的「可直映射」已由尾五（生产）+尾七（接缝）兑现；Funding 的缺口见第 10 条 ④。）**
3. **需新写的读取**：`RiskAccountFacts`（交易所真值：equity/available/marginType/day-start 基线——day-start 已在 DB）、`RiskFrequencyFacts`（滑窗计数：仓库已有 24h 预约/新险滑窗查询）、`RiskContractSpec`（tick/step/minNotional 来自交易所 instrument）、`RiskSystemFacts`（halt/mode/readiness/预算/时钟漂移的现成读面）、`RiskAuthorization`（来自 `buildShadowRunRequest` 已取的批准行）、`RiskOrderFacts`（候选 + 结算价）。
4. **语义雷区（本轮核出，勿踩）**：`Candidate.SuggestedEntry/SuggestedExit` 是**顾问价**（candidate.go 96-98 行原注释："the risk engine derives the executable price from the live order book"）——`RiskOrderFacts.Price` **不得**取自 SuggestedEntry；`RiskEnvelope`（policy.go）**没有 OrderType 字段**——两者都要先读 07 §5 的取价/取型条文再落，凭空补就是伪造。十进制一律 `shopspring/decimal`（`llm_sizing.go` 先例；`decSub` 在 llm_sizing.go:226）。
5. **07 §5.3/§5.4 条文要点（本轮核出）**：§5.3 第 4 步"参考价 = 当前盘口可成交价（买入 ask 侧按 notionalCap 的成交均价 / 卖出 bid 侧）"——**正是 `sizingWalkFromDepth` 的语义**（本会话落地件即为此步而生）；`ContractSpec` 来源 = `GetFuturesContractDetails`（`binance_wrapper.go:2790`，含 tick/step/最小名义额/乘数）；§5.4 价格表**全表已核**：`LIMIT` → `candidate.suggestedEntry`（tick 取整 + 盘口区间校验：买入 ≤ bestAsk×(1+maxSlippage)、卖出 ≥ bestBid×(1−maxSlippage)）；`MARKET` → **不传价**（币安市价单不收 price）；`STOP_MARKET`（保护性止损）→ `exitPolicy` 计算（`entry − k×ATR` 或 `entry×(1−stopPct)`，tick 取整）；`suggestedEntry` 为 null 不允许开仓（合成层已拦）。**关键区分**：订单**下单价**（`RiskOrderFacts.Price`）与**参考价**（§5.3 第 4 步、滑点/名义额算术用）是两个字段两个来源——前者按上表、后者是盘口走价（`Walk`），不可混用；统一敞口公式与 UTC 日损基线公式在 §5 "C. 资金类" 内（`max(开仓价, 标记价)×数量` 保守名义额；`dayStartEquity ± 外部划转 − 当前权益`）。
6. **桥的既有零件（本轮核出，比预想多）**：`BuildIntent`（`llm_execution.go:112`，纯函数）已含：ALLOW 双闸（risk+sizing）、市场号三段拆分、`intentSide` 映射、**OrderType 白名单（LIMIT/MARKET）**、LIMIT 经 `limitPrice(in, side)` 取价（tick 取整 + 盘口区间——§5.4 条文已在其内）。**桥要补的不是这些，而是"决策行 + 候选 → IntentPlan"的组装**（`OrderType` 由计划侧定——即桥的决策）；`RiskOrderFacts.OrderType/Price/NotionalUSD` 在 risk 阶段的值同样溯源到这份尚不存在的计划——**在桥之前不要给 RiskOrderFacts 编造这三个字段**，这正是"不可半套放行"的具体落点。
7. **B/C 组侦察增量（本轮核出）**：`snapshot.AccountFacts{TotalEquityUSD/AvailableBalanceUSD/UnrealisedPNLUSD/Positions[]/ReadAt}` 与 `PositionFact{Symbol/Side/Size/EntryPrice/MarkPrice/LiquidationPrice/UnrealisedPNLUSD/Leverage}` **可直映射**到 `RiskAccountFacts`/`RiskPositionFact`（年龄=now−ReadAt）；质量机件在快照包内已有：`QualityOf(category)`（缺标签默认 UNKNOWN、不信任）、`DegradedCategories()`（有数据或打过标签才算数——蜡烛型快照不因无 ticker 被拒）、`SetQuality` 单向 `Downgrade`——**`RiskDataFacts.SnapshotQuality` 的归约应复用它**。**尚未钉**：`MaxSnapshotAgeMs` 的配置来源（先读 04 §新鲜度条文）与"关键事实"类别子集（疑似=带数据或带标签的类别全集，最坏值胜）；`RiskSystemFacts`/`RiskFrequencyFacts` 部分计数无现成查询（库存量见 §RiskFacts 侦察结论第 2、3 条）——**照着钉子逐个落，不许抢跑**。
8. **链头再钉（本轮核出，下一会话从这里开工）**：全仓 grep `PositionFact{` 无生产构造点——`snapshot.AccountFacts` 的生产者（M5-08 所指）**尚不存在**，而它是 B/C 组映射（`RiskAccountFacts`/`RiskPositionFact`）的输入。**正确开工序**：① **M5-08 账户事实生产者**（引擎 futures 仓位 + 余额 → `snapshot.AccountFacts`，**市场拼写必须用 `BTC_USDT`**——与 rotator/审批同口径；side 从 `CurrentSize` 符号推、禁补侧；`ReadAt`=注入钟）→ ② B 组映射（`SnapshotQuality`=带数据或带标签类别的**最坏值**（`Downgrade` 折叠，无类别→UNKNOWN——恰好使 `RISK_SNAPSHOT_QUALITY` 的"仅 EXCHANGE_REST/DATABASE_CACHED 放行"成立）；`SnapshotAgeMs`=`FreshnessMS`；`CandleGap`=`len(Gaps)>0`；`OrderbookValid`=非空（构建期 `Validate` 已拒交叉盘——snapshot.go:697-702 原注释））→ ③ `MaxSnapshotAgeMs`/`MaxAccountInventoryAgeMs`/`AccountReconciled` 等**政策旋钮按显式输入**（当前代码库无此配置字段，勿发明默认——同 `OrderType` 的处理）。④ `LimitPrice` 已含 §5.4 校验（`llm_execution.go:280`）；`FreshnessReport` 有 `WorstAge()/BlockingStale/MayDecide` 但**无单一 MaxAge 字段**。
9. 纪律不变：**不可半套放行**——RiskFacts 要么完整可读、要么保持 nil（awaiting-verdict 是文档化合法形态）；每个组的读不到要显式错误，不许零值冒充。
10. **装配前哨更新（尾十三侦察，2026-10-11）**——闭包装配（`llm_decision_facts.go:213` 的 nil 位）开工前先把三类缺口的来源钉死，本轮核出：
    - **两龄上限有设计来源**：04 §6「新鲜度阈值」（Q9 定案）——Ticker/盘口 60 秒、最新已收盘 K 线 2×间隔+60s、资金费 10 分钟（不阻断）、本地仓位 60 秒。所以 `MaxSnapshotAgeMs`/`MaxAccountInventoryAgeMs` **不用发明也不用等用户**：按 Q9 落成文档常量——**已随本轮落地**（`engine/llm_risk.go` 常量块 + `RiskMaxSnapshotAgeMs`/`RiskMaxAccountInventoryAgeMs`=60_000；钉子=既有 `TestRiskThresholdsMatchTheDocs` 追加两断言）。注：年龄闸对 `<=0` 直接拒（llm_risk.go:509/523），所以装配必须给出正值——常量就是那个值。
    - **mark/index 价来源**：`FundingFacts`（snapshot.go:354）**不带** mark/index（只有费率/结算）；binance 端点 `UGetMarkPrice`（`binance_ufutures.go:224`，`/fapi/v1/premiumIndex`）可取两者。装配需（a）新 seam 接口（类似 `iPositionSummarySource`，binance wrapper 满足它是**编译期契约**）或（b）扩展 FundingFacts——**取舍属装配决策，届时二选一并记录理由**；D 组映射中 `MarkPrice`/`IndexPrice` 的「显式输入」位置就是接入点。
    - **`QuoteVolumeRank` 全仓无来源实锤**：`llm/coverage` 无排名面、`QuoteVolumeRank` 只出现在 risk 引擎（llm_risk.go:301/961）；而 `RiskMarketLowLiquidity` 的开关 `liquidityTopNOnly` **默认 ON**——`rank<=0` 即拒。**这是装配期必须先解决的问题**（候选：币安全 symbol 24h quoteVolume 排名读一次缓存 / 或语义讨论进 decisions/）——注意：该默认 ON + 无来源的组合会让「每个开仓」被拒，别在装配完成后才发现。→ **尾十四已落取数件**（`engine/llm_volume_rank.go` `quoteVolumeRanks`，数据面=引擎 ticker 缓存路径）。→ **尾十八验证闭合**：`Rotator.Step` **每周期批量** `UpdateTickers(ctx, r.cfg.Asset)`（llm_rotation.go:322-339——`WeightTickerBatch` 计权、失败不记 `lastTicker` 下轮重试）；binance 的 `USDTMarginedFutures` 分支用 `U24HTickerPriceChangeStats(EMPTYPAIR)` 全符号 → `ProcessTicker` 灌缓存——**排名数据面确有生产者（LLM 旋转器自身，周期级刷新）**。唯一残余如实记录：批间最多一周期（默认 1h）的陈旧——top-50 流动性分类是慢变量，且检查消费的是排名而非带新鲜度戳的事实，可接受；`sync_manager.go:575` 的通用路径按配置另加覆盖。
    - **`EstimatedSlippageBPS` 可装配**：装配侧盘口走价链（`sizingWalkFromDepth` 跨档均价 vs 最优档）即得，无外部依赖。→ **尾十五已落**（`engine/llm_slippage_bps.go` `estimatedSlippageBPS`——ask/bid 双侧方向分支、错误一侧即拒、向上取整；装配先决三件自此全部到位）。
    - **④（尾十六侦察）funding + mark/index 生产者（新发现，装配前必须补）**：`FundingFacts` 无生产者（见第 2 条更正）+ mark/index 无来源——两者**恰好同源**：binance `UGetMarkPrice`（`binance_ufutures.go:224`，`/fapi/v1/premiumIndex`）**一次返回 markPrice + indexPrice + lastFundingRate + nextFundingTime**。所以不做「FundingFacts 扩展 vs 新 seam」的二选一，而是**合并为一个新件**：一个 premiumIndex 读面同时喂 ①`snapshot` 的 Funding 事实（`LatestRate`/`NextFundingTime`）②D 组 `MarkPrice`/`IndexPrice` 两个显式输入。形态照 `iPositionSummarySource` 先例（engine 侧小接口 + wrapper 满足）；`snap.Funding` 消费侧（llm_risk_market_facts.go:69-73）已 nil-safe 并就位。→ **尾十七已落**（`engine/llm_funding_facts.go` + `SnapshotRequest.Funding` 接缝，EXCHANGE_REST；**先决四件全部到位**）。
    - **开关目录**：`RiskSwitchService.Current()` 直接可用（尾十二已澄清）。
11. **闭包装配接线清单（尾二十一补钉，拍板 02 后照此开工——免侦察）**：闭包位于 `llm_decision_facts.go:213` 的 nil 位，入参 `(ctx, analysis, jev)`；在作用域内已有 `bot/cfg/version/policy/approval/runtimeFacts/targetPair/assetItem/f.built/now`。
    - **已核（直接接线）**：`Envelope` ← `policy.Risk`（`ReviewablePolicy.Risk RiskEnvelope` 字段自带——审批行 `RiskEnvelopeJSON` 同源可交叉验证）；`Candidate` ← `analysis.Candidate`；`Now` ← 捕获的 `now`；`Switches` ← `RiskSwitchService.Current()`；`Contract` ← `buildContractSpec(cfg.Exchange, assetItem, targetPair)`（尾十二）；Funding/mark/index ← `buildFundingMarkIndexFacts` **已接线进 `Assemble`（尾二十六）：Funding 上快照、mark/index 存 `f.premium` 供本闭包直取**；两龄上限 ← `RiskMaxSnapshotAgeMs`/`RiskMaxAccountInventoryAgeMs`（尾十三）；账户态 ← `buildRiskAccountStateFacts(ctx, approval.AccountID, now)`（尾十九）；`AccountReconciled` ← `runtimeFacts.Reconciled`；`MaxNewRiskIntents`/`RiskWindowMinutes` ← `policy.Risk`；E 组窗口 ← `riskFrequencyFactsFromState(ctx, …)`（尾十一；上限/冷却/连亏显式输入——`RiskMaxConsecutiveLosses` 常量在；**冷却「上次开仓」半 ← `LastEntryIntentAt`（尾二十七）、时长半政策输入**）；滑点 ← `estimatedSlippageBPS(walk, side, envelope.MaxOrderNotionalUSD, bestFromOrderBook)`（尾十五）；数量链 ← `sizingWalkFromDepth(f.built.Snapshot.OrderBook…)` + `ComputeQuantity`；**组装两件 `buildShadowRiskInput`（尾二十五）/`buildShadowSizingInput`（尾二十四）已即插化**。**→ 尾四十一勘（session-52 同段尾二）：System 组读面逐字段点名（`ListActiveHalts`/`EffectiveRunMode`/`cfg.AcknowledgeLiveTrading == LiveAcknowledgment`/预算位已在作用域/`llmRiskSwitches.Current()`）；新增未钉两件（`ConflictsWithResting`/`RestingOrder` 来源）；市场号拼写矩阵与 F 组裸相等警报（仓位 `BTC_USDT` vs 候选三段式——反向仓位闸生产下沉睡）——闭包开工前先读。**
    - **待核（3 小件）**——**尾二十二已核两件半**：① 授权列表读面 = `(LLMApprovals{}).ListGrants(ctx, approval.AccountID)`（`llm_approval.go:78`，仓储 `ListMarketTradeGrants` 的引擎适配——**直接接线**）② reconciled 字段名 = `RiskSnapshotFactsInput.AccountReconciled`（`llm_risk_facts.go:48`）← `runtimeFacts.Reconciled`（**直接接线**）③ C 组场地材料：`MarginType`/`ExpectedMarginType` **二者映射层要求非空**（`llm_risk_facts.go:100`），检查层 `ExpectedMarginType==""` 反而不过闸（`llm_risk.go:677`）——而**审批行不含保证金模式、场地读面未建**：**装配期必须给这两个字段定来源**（若 `cfg` 无对应字段，属显式输入或补配置的装配决策；`HedgeMode`/`TargetAccountID`/`UnreconciledPartials` 同理逐一定源）。
    - **C 组场地材料地形（尾二十九侦察钉死，装配会话照此收口）**：① `futures.PositionSummary`（账户事实生产者的同一次调用即持有）**自带 `MarginType margin.Type` 与 `CollateralMode collateral.Mode` 字段**（futures_types.go:350-351）——**`MarginType` 出口已兑现（尾二十九：`venueAccountModes` 随账户事实生产者返回、闭包存 `f.accountModes`）**；② **`collateral.Mode` 是「多资产保证金」不是对冲模式**（wrapper `GetCollateralMode`=assetsMode：Single/Multi——`exchanges/collateral` 语义，**不得**拿来当 HedgeMode）；③ **全 binance 包无 dualSide/positionSide-dual 读面**（仅有写侧 `UChangeInitialMarginType` 与 `GetCollateralMode`）——`HedgeMode` 事实**二选一**：补 `UGetDualSidePosition`（GET `/fapi/v1/positionSide/dual`，须同步 wrapper 能力位 `binance_wrapper.go:133` 一带）／或把「单向持仓」定为运营前置条件（装配时记录理由，类似钱包设置）——**已选 a（尾三十）：`UGetDualSidePosition` 读面已落、`venueAccountModes.HedgeMode` 兑现、闭包读入 `f.accountModes.HedgeMode`——未动能力位（纯方法新增，IBotExchange 无改）**；④ `ExpectedMarginType` 审批不含、cfg 无字段——**需补配置或显式输入（装配决策）**。→ **尾三十一：升级为决策门 03**——细读后确认这是**契约缺口**而非装配小项：检查定义源自 07 §5.2 C 表「实际保证金模式与**审批假设**一致」，但审批 17 列与冻结 `RiskEnvelope` 均无该字段（「假设」无处安放）；检查侧对缺席宽容（`ExpectedMarginType==""`→不触发）而映射侧成对非空（我取严）——两侧不一致即设计从未闭环的证据。已立 `decisions/03-expected-margin-type-source.md`（A 配置默认 CROSS（建议）/ B 审批录入 schema+前端 / C 暂不声明检查空转）。
    - **受 02 门（拍板后再定）**：`RiskOrderFacts.OrderType/Price/NotionalUSD`（订单三字段的正确定型）+ `SizingInput` 的装配外壳——SizingInput 其余输入（`Candidate`/`Envelope`/`Contract`/`Walk` 同上一栏；`ExistingExposureUSD` ← 账户事实仓位保守名义额、`DailyLossUSD` ← `DayStartService.DailyLoss(equity)`、`StopDistancePerUnitUSD` ← §5.6 止损公式（`max(1.5×ATR, 2%)` 取较紧，ATR 在快照指标里））——**这些不依赖 02，可先行**；三字段与单型见 `decisions/02`。
    - **装配期取值表（尾三十三固化——每项显式输入的建议取值与理由，照抄即装）**：① 三个窗口上限（`MaxVersionWindow`/`MaxOrdersPerWindow`/`MaxReduceOrdersPerWindow`）→ **0 照传**（检查 `<=0` 即「无闸」是既有语义，尾十一已判「0 照传不是发明」）；② `ConsecutiveLosses` → **0**（成交结果账无读面；检查 `limit<=0` 回退 `RiskMaxConsecutiveLosses=3`，闸活着而计数暂为 0——**待成交结果账读面到位后补**）；③ `MarketCooldownElapsed` → **由 `LastEntryIntentAt`+时长算出：无参（时长未配置）时按「0=无闸」同一模式翻译为 elapsed=true（从未开仓或时长 0）**——dormant 可查、配置面到位自动激活（登记为待补政策项，非用户门）；④ `FundingRequired` → **false**（无任何面能声明「本策略需要资金费率」——缺席即未声明，检查 dormant；与「缺席字段不进消息」同族）；⑤ G 组显式位——`Mode`/`LiveAcknowledged`/`PairReadiness` ← `runtimeFacts`+cfg 已有面；`ModelBudgetExhausted` ← 24h 预约窗口（`ListReservationsInWindow`，buildShadowRunRequest 既有算式）；`DatabaseHealthy` ← 装配期一次 `Ping`（归属装配）；`OrderManagerUp`/`ExchangeManagerUp` ← bot 两管理器非 nil；`ClockDriftMs` ← **`readClockDriftMs`（尾三十四已落——venue `UServerTime` 幅度值）**；`RateLimitDegraded` ← **需补读面**（venue 限频状态——装配期定源或按 fail-closed 显式值并记录理由）**→ 尾三十九已勘（session-52，requester 层实勘）：无持久降级状态可读（429 退避=请求内瞬态、屏障=per-request 上下文、限速器内部不可达）；真读面=共享包新增状态，取法留拍板会话按尾三十七定形**；⑥ `QuoteVolumeRank` ← `quoteVolumeRanks(exchange, item, bot.GetAllActiveTickers() 的展开)` 取 `marketID` 键（尾十四/十八）**→ 尾四十五勘（session-52 同段尾六）：查表键必须 `llmMarketID(exchange, item, pair)`——小写 exchange/asset 三段式（`quoteVolumeRanks` 的键生成函数，函数注释自述「与全仓同拼写」）；拿运行市场号（大写交易所）直接查会 MISS，rank=0 撞 `liquidityTopNOnly` 默认 ON 即每个开仓被拒**。**每项在装配时落注释理由——此表即理由蓝本。**
    - **闭包合成器的设计归属（尾三十七侦察结论）**：把「材料→七个映射调用→两组装件」合成为一函数的做法**已在纸面推演**——其材料 API 约 28 字段、绝大多数直通入各映射的输入结构体；形状与 02/03 的落地选择相连（谁 raise `AccountState`/契约/走价、`RateLimitDegraded` 的取法），**预锁这份 API 得不偿失**——**在拍板会话按最终选择一次设计并测试**（本会话已完成的部分：全部被调用件的签名与输入字段已核、测试夹具路线已明（sqlite + 字面量 + `limits.Load` 先例））。
    - **组合冒烟测试蓝图（尾三十八侦察钉死——拍板会话照建）**：`TestShadowRiskInputChainComposes`——一条链把七个映射 + 两组装件接在一起过 `Evaluate`，**断言=引擎表跑过（`result.Checks` 非空）且无 `RiskInputInvalid` 拒绝**（领域拒绝可容忍；证明的是「件件咬合」而非「全闸放行」）。钉死的入参要点：① `RiskAuthorizationInput` 七字段（`Approval *llmstore.StrategyApproval`/`FrozenJSON`=**有效 ReviewablePolicy JSON**（scopes 在其内解码）+ `Candidate`/`PairEpoch`/`Grants`/`ManifestBound`/`Now`）——`RiskEnvelopeHash` 用 `RiskEnvelopeHashOf(frozen)` 现算对齐；② `RiskMarketFactsInput` 六字段（`Now`/`MarkPrice`/`IndexPrice`/`EstimatedSlippageBPS`（滑点件现算，walk 脚本化）/`QuoteVolumeRank` 字面量/`FundingRequired` false）——ATR 与锚价在快照字面量里；③ 夹具原料：sqlite（`setupProviderAdminDB`）+ 快照字面量（`Account`/`Quality`/`FreshnessMS`/`OrderBook`）+ `limits.Load` 独占 venue + 未知/部分状态零种子（计数 0 合法）；④ 末段 `buildShadowRiskInput`（Order 用字面量 LIMIT 三字段）→ `Evaluate` 断言；`buildShadowSizingInput` 一并建（断言可建，不强制 ALLOW）。

## 环境状态快照

- 无本会话新增长跑进程；mock 9443 / 代理 8788（allowWrites:false）/ vite 5173 在位。**环境更正（2026-10-12 10:08 实测）**：**真实 bot 9053 正在运行**（`gocryptot` pid 18809，非本会话启动——疑为用户或早前会话所留）；**未经用户确认不要 kill 该进程**；本会话对 engine 的验证均未触碰该进程（全部为测试进程与离线件）。下一会话若需 9053 端口/真 bot 环境，先查该 pid 归属与配置（`llm.coverage.enabled` 是否按住）。**运行面审计（2026-10-12 10:09-10:10，browser-use 活体，两路由）**：① `/strategies` 页完整渲染（8 路由菜单 + 策略/版本/授权三表 + mock 数据在流；配对 SHADOW/READY/epoch 3/已对账）；② **`/health` 页完整渲染**——三级 HALT 面板、本地代理自检（状态 ok/上游 9443/写操作关闭/凭据已加载/17 端点）、全量覆盖轮询（588 合约、214/588、权重 22/1440、周期 412 次）、对账与预算（已对账/24h 预约剩余 2/用量三族含「自估成本缺席」声明）、通知通道（telegram 已设置、webhook 未设置）、**风险开关 18 项全渲染且三把锁 disabled + 默认 12 ON/4 OFF 逐位正确**。**横幅「连接失败」与高频 `/api/getinfo` 404 为 mock 栈的已知预期**（getinfo 是真实 bot RPC，mock 网关未实现——指向 9053 真 bot 时方会绿），**勿误判为前端回归**。
