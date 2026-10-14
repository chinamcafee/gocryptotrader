# 2026-10-12 session-52：组合冒烟链预建（七映射 + 两组装件过 `Evaluate`）

## 会话目标

三项用户决策门（`decisions/02` 单型 / `03` 保证金假设 / `01` 凭据）均未拍板。本段为**非门控推进**：把尾三十八蓝图（session-51 §侦察结论第 11 条末栏）的组合冒烟测试**提前落地**——该链只用夹具字面量（02 门控的 Order 三字段=字面量 LIMIT），不依赖 02/03 的落地选择；提前建链把「件件咬合」验证从拍板会话前移，拍板后开工量压缩为 ③ 合成器 + 闭包 + 桥。

## 完成的子任务

- `M2-13` 收官第十九段（不计分）：组合冒烟链 `TestShadowRiskInputChainComposes` 落地——尾三十八蓝图②预建

## 具体做了什么

1. `engine/llm_shadow_chain_test.go`（新）——`TestShadowRiskInputChainComposes` + `chainSnapshotFixture`：
   - **一条链七个映射**：① 授权=`riskAuthorizationFromState`（复用 `riskAuthFixture`：冻结策略 + `RiskEnvelopeHashOf` 现算对齐 + 活跃 grant；候选换成本链的、保持 scope/grant 相配）② 数据=`riskDataFactsFromSnapshot`（质量折叠=EXCHANGE_REST、年龄=快照 `FreshnessMS` 1234、两龄上限=Q9 常量 `RiskMaxSnapshotAgeMs`/`RiskMaxAccountInventoryAgeMs`、`AccountReconciled=true`）③ 账户=`buildRiskAccountStateFacts`（sqlite 空种子：日界未封=如实 nil、UNKNOWN/部分计数 0）+ `riskAccountFactsFromSnapshot`（保证金模式=场地本拼写 `multi`；仓位 `BTC_USDT` LONG 0.5）④ 市场=`riskMarketFactsFromSnapshot`（盘口最优档 49990/50010 + ATR 比率 500/50000=0.01 + funding 事实；mark/index=字面量 50001/50002；**滑点由 `estimatedSlippageBPS` 现算**——脚本 walk 50015 vs 最优卖价 50010 → **1 bps**；`FundingRequired=false` 照取值表④）⑤ 系统=`riskSystemFactsFromState`（`riskSystemFixture`，Mode 对齐审批口的 SHADOW——把夹具级配置冲突排除在「咬合」信号之外）⑥ 频率=`riskFrequencyFactsFromState`（空库三窗口计数 0 + 候选折入=1；冷却无时长按 0=无闸翻译 elapsed=true）⑦ 合约规格=`buildContractSpec`（`limits.Load` **独占 venue** `ChainComposeVenue`）。
   - **两组装件收口**：`buildShadowRiskInput`（`CheckVersion` 代码盖章、**Order=字面量 LIMIT 三字段** `OrderType/Price/NotionalUSD`——02 门控字段不发明）→ `Evaluate`；**断言=表跑过（`Checks` 非空）且无 `RiskInputInvalid`**（领域拒绝可容忍——证明「件件咬合」而非「全闸放行」）。`buildShadowSizingInput` 一并建：敞口 `25050`=max(50000,50100)×0.5（C 组同一保守名义额句式）、止损距离 `1000`=max(1.5×ATR 750、2% 入场 1000) tick 取整（Q8b 公式同源断言）。
   - **夹具纪律**：sqlite 走 `setupProviderAdminDB`（空种子——「未知/部分状态零种子，计数 0 合法」）；快照字面量一具多用（市场/数据/账户/止损四处读同一份）；`limits.Load` 与库连接均进程级全局状态 → **不 `t.Parallel`**（与 `llm_contract_spec_test.go`、频率映射测试同纪律）。
   - **过程两条**：① 首跑即绿（0.04s，包级 2.134s）；② misc 两条措辞规则命中初稿（require 消息含 should→改 must、assert 消息含 `%` 未配 f 变体→改写为 pct-of-entry）——修正后 `make misc_checks` 全绿（「写测试前先自查消息措辞」再次被验证为例行步骤）。

## 证据

- 聚焦：`go test ./engine/ -run 'TestShadowRiskInputChainComposes' -count=1 -v` → `--- PASS`（0.04s；包级 2.134s）。
- `golangci-lint run ./engine/...` → **0 issues**。
- `make misc_checks`（webUI `node_modules`+`dist` 暂存 `/tmp/webui_stash` 后跑、毕恢复）→ **All miscellaneous checks passed!**
- 全套 engine（同既有 skip 表——六个非 LLM/需网络测试除外）：**108.9s 全绿**（`ok … /engine 108.947s`，后台实跑；该轮为链增量时的全绿——同段尾三修复后的 6 轮复跑见该节证据）。
- **首跑红的三件=环境 flaky（已分类）**：首跑 101.8s、`TestOrdersGet` / `TestGetByDetail` / `TestDryRunParamInteraction` 三件失败——**全部是 `www.bitstamp.net/api/v2/trading-pairs-info` 的外部读**（EOF / `connection reset by peer`，目标 `198.18.4.54`=本机 fake-IP 代理段）；**隔离复跑三件 17.9s 全过**，与本段改动无关（三件为引擎既有测试、打真网）；复跑全套全绿=上条。新链测试在两轮整套中均为绿（非 verbose 输出只列失败）。

## 账目

- **不计分**（尾链预建，与 session-51 各尾同口径）：Σ得分 105.50 ÷ 107 = **98.6% 不变**；engine LLM 相关测试 **367 → 368**（PROGRESS §1 计数线已同步）。
- **拍板后动手顺序**：① 侦察已读 → ② **已完成（本段）** → 余 ③ 合成器 + 闭包（`llm_decision_facts.go:213` 位）+ 桥、④ 例行电池 + 台账同步——PROGRESS §1 收口指引与 `decisions/README.md` 已同步。

## 环境快照（会话收尾）

- 无本会话新增长跑进程；9053 真 bot（pid 18809）无触碰；mock 9443 / 代理 8788 / vite 5173 未动。

## 同段尾一：非门控剩余全扫 + 台账陈旧处修正（同日）

1. **全扫结论（2026-10-12）**：TASKS 未勾选清单仅 4 件、全部为 `[!]` 凭据门（`M1-15`/`M6-03`/`M6-04`/`M6-05`）；开放线索逐条复核（`RateLimitDegraded` 取法、`TargetAccountID`、冷却时长政策值、M6-02 #5 升级器、proto 对齐 diff）——全部属「用户决策门 / 拍板会话定形 / 装配批 / 提交时组织」四类，**拍板前无非门控剩余**（据此不再新开预建件，避免尾三十七所指的预锁返工）。
2. **`RateLimitDegraded` 读面实勘（requester 层，2026-10-12）**：无持久降级状态可读——429/`Retry-After` 退避是 `evaluateRetry` 内的请求内瞬态；`rateLimitBarrier` 是 per-request 上下文协调而非场地状态；`RateLimiterWithWeight` 内部 `rate.Limiter` 无访问器（`GetRateLimiterDefinitions` 只到包装层）。**真读面=在共享包新增状态+访问器**——取法（读面 vs fail-closed 显式值）属尾三十七所留拍板会话定形项；已按尾注先例回注 session-51 §侦察结论第 11 条取值表⑤（尾三十九勘）。
3. **台账陈旧处修正（PROGRESS §21/§22 滞后于 §1）**：§22「`decisions/` 有 1 项等待」→ **3 项（01/02/03）**并指向 §1 收口指引的拍板后顺序；§21「不阻塞的下一候选：`RiskFacts` 装配与桥」→ **已入 02/03 决策门（拍板后执行）**；§21 的 session-23 注记「M2-13 仍余两项」→ 标注均已落地（session-28 / session-51）。**动机**：接力装置要求任何台账入口都不产生「无门可过、直接装配」的误判——§22 的「1 项」正是这种误判源。

## 同段尾二：System/F 组接线补钉 + 市场号拼写矩阵实勘（同日）

闭包（`llm_decision_facts.go:245` 的 nil 位）开工前的最后一轮逐字段实勘（**拍板会话先读本节**）：

1. **System 组读面逐字段点名（已核，直接接线）**：
   - `Halts` ← `llmstore.ListActiveHalts(ctx)`（`database/repository/llm/reconcile.go:451`；readiness 同式先例 `engine/llm_runtime_facts.go:70`）——§11 已核栏此前未点名，此勘补上；
   - `Mode` ← `bot.llmManager.EffectiveRunMode()`（`llm_manager.go:174`，nil=READ_ONLY 兜底）；
   - `LiveAcknowledged` ← `strings.TrimSpace(cfg.AcknowledgeLiveTrading) == LiveAcknowledgment`（配置字段 `config/llm_config_types.go:12` 实锤；常量 `engine/llm_mode.go:62`「I_UNDERSTAND」；与 `RiskLiveNotUnlocked` 检查的字段名同源）——尾十「确认在配置」的说法经此实勘变为具体取值式；
   - `ModelBudgetExhausted` ← `!budgetAvailable`：该值**已在闭包作用域内算好**（`llm_decision_facts.go:104-111`，24h 预约窗口，buildShadowRunRequest 既有算式）；
   - `PairReadiness` ← `runtimeFacts.PairReadiness`；两个 manager ← `bot.OrderManager`/`bot.ExchangeManager` 非 nil（同文件 158 行 `GetExchangeByName` 先例）；
   - `ClockDriftMs` ← `readClockDriftMs(ctx, src, now)`，源守卫照 `Assemble` 内三处先例（`source.(iServerTimeSource)`）；`MaxClockDriftMs` ← `RiskMaxClockDriftMs`；
   - `DatabaseHealthy` ← 装配期 `Ping`（登记不变）；`RateLimitDegraded` ← 保留（尾三十九勘）；
   - `Switches`（RiskInput 直取）← `bot.llmRiskSwitches.Current()`（`llm_risk_switches.go:92`，返回 `RiskSwitches`；`CurrentState()` 是 readiness 消费的 map 形，两者勿混）。
2. **新增未钉两件（§11 三栏与取值表均未登记；闭包开工先定源，禁发明）**：`RiskOrderFacts.ConflictsWithResting`（在挂单冲突）与 `RiskPositionFact.RestingOrder`（未成交开仓单行）的生产来源——候选意图的 RESTING 行从哪张表/哪个读面进材料未定；两字段当前只有字面量测试在用。
3. **市场号拼写矩阵（实勘）**：
   - **运行/候选侧**＝三段式、**交易所大小写随 `cfg.Exchange`**（`parseMarketPair` 要求 `parts[0]==cfg.Exchange`，配置为「Binance」）：候选（`CANDIDATE_SCOPE_DENIED` 逐字对照）、Jev 快照引用、halt RPC 测试夹具（`policyTestMarket = "Binance:usdtmarginedfutures:BTC_USDT"`）；
   - **对账侧**＝`llmMarketID`（`llm_reconcile_snapshot.go:41`，注释即「canonical market id both sides share」）**小写交易所**三段式；
   - **仓位/快照侧**＝`BTC_USDT`（BASE_QUOTE，`buildSnapshotAccountFacts` 生产写 `snapshot.PositionFact.Symbol`）；
   - **排名字典键**＝`llmMarketID`（小写）——闭包查表必须用同一函数拼键。
4. **F 组裸相等警报（生产口径下真实发生）**：`positionsAfterOpening`（`llm_risk.go:1037`，映射表键）与 `hasOpposingPosition`（`llm_risk.go:1057`，`p.MarketID != marketID`）拿 `candidate.MarketID`（如「Binance:usdtmarginedfutures:BTC_USDT」）与仓位 `MarketID`（「BTC_USDT」）**裸相等**比对——后果：① 同市场已有仓时并发位置计数多算一（过度拒绝方向）；② **反向仓位闸永不触发（REVIEW 级人工确认闸 fail-OPEN 沉睡）**。文档侧两处说法互相矛盾且代码两处均未做（尾五/账户生产者注释称「与审批同口径」＝BASE_QUOTE；残余更新九称「MarketID=candidate 拼写」）。
   - **处置（留拍板会话，归闭包定形一次修）**：三选一——(a) 闭包 raise 仓位材料时把同市场行归一为运行市场号（`marketID` 变量已在作用域）；(b) 检查侧按 pair 尾段归一比对；(c) 生产者改口径（波及快照展示）。修复时补一枚跨拼写测试（候选三段式 + 仓位 BASE_QUOTE 断言反向仓位闸正确触发），并同步两处矛盾注释。**（同段尾三已落 `runMarketID` 归一层——选项 (a) 的现成 helper，闭包应用它收口。）**
   - **同源提醒**：`policyTestMarket` 与 `parseMarketPair` 按「大写交易所」、`llmMarketID` 小写；W5 的 grant/halt 表单目前是**自由文本输入**（`请输入市场 id`）——「哪个拼写是权威」及操作员输入口径一并定死属拍板会话内容。

## 同段尾三：自动响应链拼写断点实锤并修复（`runMarketID` seam，同日）

**实锤（修复前，两条自动响应链都不可工作）**：

1. **自动决策链在门口即断**：`FactFromSnapshot` 的 `MarketID` = `snapshot.Pair`（`RefreshMarkets` 刻意钉的快照口径「BTC_USDT」）→ `TriggerConsumer` 用它（a）对照 `ApprovedMarketIDs`（操作员/审批口径=大写三段式）路由——**永不命中**；（b）直送 `buildShadowRunRequest`→`parseMarketPair`——**「BTC_USDT」不是三段式，必拒**。两条约束不可能同时成立：**任何拼写配置下自动触发都出不了运行**。
2. **下架级联同根静默**：`MarketDelistWatcher` 拿旋转器列表「BTC_USDT」对照 grant/审批存储行（大写三段式）——**级联撤授权/暂停版本不触发**（审计与推送仍响，表面「有反应」）。
3. 根因：M2 覆盖层（旋转器/扫描器）与决策栈（运行入口/审批/halt）各自自洽、两个词汇表之间**没有任何转换点**；两侧测试各自用自洽夹具，缝从未被跨测（`Build` 在消费器测试里一直是 stub）。

**修复（归一层 + 两处 seam）**：

- 新件 `engine/llm_decision_facts.go` `runMarketID(exchange, asset, raw)`：接「BASE_QUOTE 对」或「三段式」→ 验形（空/异场所/坏形拒，异场所按 `parseMarketPair` 同严格度）→ 归一到运行口径 `<cfg.Exchange>:<asset>:<BASE>_<QUOTE>`。
- `TriggerConsumer` 新 seam `MarketKey`（生产装配于 `newTriggerConsumer`）：**双侧归一**——事件市场与审批携带市场都过它后再比路由键；`Build` 收归一后的运行口径（`parseMarketPair` 可解析）。
- `MarketDelistWatcher` 新 seam `MarketKey`（装配于 `newMarketDelistWatch`）：grant 行与审批行双侧归一后再比；**审计/推送/Disappeared 保留原始旋转器拼写**（操作员口径不变）。归一失败按「坏行=失败本遍」纪律处理（与审批解码同径）。
- **测试**：`TestTriggerConsumerUnifiesMarketSpellings`（旋转器拼写事件 × 三段式审批 → 路由命中且 Build 收运行口径）、`TestMarketDelistWatchUnifiesSpellings`（同组合 → grant 撤、审批级联、版本暂停、审计行；真库）、`TestRunMarketIDNormalisesBothVocabularies`（三形态表测 + 空/异场所拒）；既有消费器/下架测试夹具补 `MarketKey`、消费器两处期望更新为运行口径。
- **不在本次修复**：F 组仓位拼写归一（同根不同缝——仍在闭包层收口，见同段尾二；`runMarketID` 即其选项 (a) 的现成 helper）。

**证据**：聚焦 8 测全过（消费器 6 + 下架 2，`-v` 逐条 PASS，2.4s）；`golangci-lint run ./engine/...` **0 issues**；全套 engine（同 skip 表）复跑：**本窗口 Bitstamp 真网持续抖动，6 轮复跑红件全部且仅为 Bitstamp 依赖件**（逐轮命中的子集各不相同：`TestOrdersGet`/`TestGetByDetail`/`TestDryRunParamInteraction`/`TestExists`/`TestGetHistoricCandles`/`TestGetRecentTrades`/`TestGetCryptocurrenciesByExchange`/`TestGetSpecificAvailablePairs`/`TestOrderManager_Modify`/`TestStore_modifyOrder`/`TestCancelOrder`/`TestFetchAndUpdateExchangeOrder` 共 12 件，全部 `EOF`/`connection reset` 至 fake-IP `198.18.4.54`）；**逐件隔离复跑全过（秒级）、6 轮中非 Bitstamp 测试零失败**——判定为环境抖动（同窗口 `curl` 301 可达），非本改动；机器记忆「Bitstamp flaky 清单」已如实扩录，**不改 skip 表**。

## 同段尾四：工具链 scope 拼写断点实锤并修复（第三处缝，同日）

**实锤（修复前，聊天与决策两条工具链在生产下每次工具调用都会被拒）**：`buildScopeToolRunner`（chat 走廊与决策运行器**共享**）把旋转器市场列表（`Rotator.Markets()`＝BASE_QUOTE「BTC_USDT」，`RefreshMarkets` 刻意钉的快照口径）直接塞进 `tools.NewScope`；而工具面词汇全程是配置口径三段式——`listMarkets` 经 `marketIDFor`（大写 `cfg.Exchange` 三段）把 id 给模型、模型原样回传、`parseMarketID` 验形、证据 `MarketID=new(args.MarketID)`——于是 `Scope.CheckEvidence` 的**精确包含比对**在生产下**每次调用必拒**（`ErrScopeDenied`）。测试侧夹具全程用 `testMarketID()`（即 `marketIDFor` 三段式）自洽，缝从未被跨测——与同段尾三同病根。

**修复**：`buildScopeToolRunner` 在 `NewScope` 前把市场列表逐个过 `runMarketID(cfg.Exchange, assetItem, market)`（归一失败＝装配报错，fail-loud）；scope 自此只持有运行口径 id。

**测试**：`TestSetupChatFromActivePairAssemblesTheCorridor` 实参改为生产拼写 `["BTC_USDT"]` 并加负例断言（scope 含 `testMarketID("BTC")`、不含裸 `"BTC_USDT"`）——既有断言就地化为回归钉子；决策运行器测试实参同步换生产拼写。聚焦 20 测全过（含工具八件套全量）、`golangci-lint run ./engine/...` 0。

**同源全景（三处缝，两个窗口内全部修复）**：① 触发消费路由+Build（同段尾三）② 下架级联（同段尾三）③ 工具 scope（本节）；**F 组仓位拼写归一仍留闭包**（`runMarketID` 即其 helper）。

**证据（收盘）**：全套 engine（同 skip 表）**复跑全绿 92.975s**（第三处缝修复后的整绿基线——同段尾三的 Bitstamp 抖动窗口此后恢复）。

## 同段尾五：候选契约上下文缺口 + 操作员入口拼写修复（第四处同族，同日）

**实锤（两条）**：

1. **分析提示词要求「三项 ID 从用户上下文逐字复制」，但用户消息没给 marketId**——`engine/llm_analysis.go` 的用户消息只有版本号 + 快照号 + 快照 JSON（其中只有 `pair:"BTC_USDT"`）；`req.MarketID`（运行口径三段式）**全程不在上下文**，而合成闸是 `proposal.MarketID != req.MarketID` 的**逐字比对**——真实模型只能猜（快照里没有可逐字复制的值），猜错即 `CANDIDATE_SCOPE_DENIED`。夹具用 `analysisMarket` 自洽、缝未被跨测（同族第四处）。
2. **操作员入口在教错误拼写**：提取表单默认值 `binance:usdtperpetual:BTC_USDT`（资产名错 + 交易所小写）——选择集原样进提取消息、模型照抄成 scopes，而 A 组 `MarketInScope` 拿 scope 与候选（`Binance:usdtmarginedfutures:BTC_USDT`）逐字比 → 该策略**全部决策必拒**；`usdtperpetual` 另有**三处 placeholder**（提取多例、授权、halt 表单）同错。**历史实证**：session-43 浏览器实测的授权行与级联撤销原因串正是 `binance:usdtperpetual:BTC_USDT`（mock 库中仍在）——这套错误词汇真实进入了操作实践。

**修复**：

- `llm_analysis.go` 用户消息补 `Market %s`（`req.MarketID`）——提示词的「copy exactly」自此成立；`TestAnalysis*` 加断言（message[1] 含 `analysisMarket`）。
- 前端四处拼写统一为运行口径：提取默认值 + 提取/授权/halt 三处 placeholder；`usdtperpetual` 全 src 零残留。

**未动（登记为拍板会话项）**：grant/halt 服务对市场串**零校验**（任意字符串可入库成永远匹配不上的死授权）——存储侧加验属「哪个拼写是权威」决策面（同段尾二），不在本修复预锁。

**证据**：`TestAnalysis*` 7 测全过、`golangci-lint run ./engine/...` 0；`tsc` 0 / `oxlint` 0（30 文件）/ `pnpm build` 成功（1.53s）；**浏览器实读**（5173 mock 栈，HMR）：修复前输入值+占位符均错、修复后两者均 `Binance:usdtmarginedfutures:BTC_USDT`。全套 engine 复跑：**全绿 110.707s**（第四处缝修复后的整绿基线）。

## 同段尾六：闭包查表键钉死（排名字典 `llmMarketID`，同日）

闭包查表键的最后一处拼写陷阱：`quoteVolumeRanks` 的键由 `llmMarketID` 生成（**小写 exchange/asset** 三段式——函数注释自述「排名键与全仓同拼写」，`llm_volume_rank.go:36`）；而闭包手里的运行市场号是 **大写交易所** 三段式（`parseMarketPair` 口径）。闭包查表**必须用同一函数拼键**（`llmMarketID(cfg.Exchange, assetItem, targetPair)`）——直接拿运行号查会 MISS、`rank=0`，撞 `liquidityTopNOnly` **默认 ON** 即每个开仓被拒。已回注 session-51 取值表⑥（尾四十五勘）。

同批检视：`llmMarketID` 的其余消费方（对账 `fetchExchangePositions`/`fetchLocalPositions` 两侧，`llm_reconcile_snapshot.go:69/113`）同用该函数、自洽——无需改动。

## 同段尾七：pre-gate 策略门状态词修复（第五处同族、拦在决策链最上游，同日）

**实锤**：`EvaluatePreGates` 的策略门只认字面 `"APPROVED"`（`llm_pregate.go:81`；测试更有明文断言 `{"frozen but unapproved", FROZEN → ReasonStrategyNotApproved}`），而 **M4-05 定案的生产语义是版本永远保持 `FROZEN`**（「审批行本身（每版 UNIQUE）即 APPROVED 信号，单一事实源，避免动冻结行」）+ 消费器只路由 FROZEN 版本 + `buildShadowRunRequest` 原样传 `version.State`——**每次自动运行与手动预览都会在第二道零成本门被判 `STRATEGY_NOT_APPROVED`**。M4-07（门）与 M4-05（冻结语义）在各自会话中各自自洽，缝从未被跨测——第五处同族，也是最要命的一处：它拦在决策链最上游，「触发→运行」修好的链条走着走着仍会死在这里。

**修复**：门改认 `strategyStateFrozen`（生产运行态；注释引 M4-05 单一事实源）；死分支 `"APPROVED"` 删除（全仓无任何写入方设置该状态）；`PreGateInput.StrategyState` 文档注释同步更替。

**测试**：green 夹具改为 `strategyStateFrozen`（绿径测试自此即回归钉子）；「frozen but unapproved」用例替换为 `paused strategy`（`StrategyStatePaused` → 拒，保留 one-at-a-time 覆盖）；`seedFrozenVersion` 夹具改 FROZEN 并在 `TestBuildShadowRunRequestFromLiveState` 加断言（生产 `PreGates.StrategyState == strategyStateFrozen`）——装配侧与门侧同时钉住。

**证据**：聚焦 12 测全过（含 ShadowRunner 五件与两个 buildShadowRunRequest 用例）；`golangci-lint run ./engine/...` 0；全套复跑：3 轮全量，红件**全部且仅为 Bitstamp 真网依赖件**（逐轮不同：`TestCancelOrder`；`TestGetHistoricCandles`；`TestGetByExchangeAndID`+`TestGetAuthAPISupportedExchanges`——最后一者的 LoadExchange 走 Bitstamp pairs 刷新）——每件隔离复跑全过、**3 轮中非 Bitstamp 测试零失败**（含全部门/LLM 件）；判定为与同段尾三相同的环境抖动，不改 skip 表。

**仓库级收口（同日）**：`go build ./...` OK；全仓非测试代码 `"APPROVED"` 零残留、`usdtperpetual` 除台账历史记录（session-43 实证）外零残留。

## 同段尾八：触发→真实运行请求的融合钉子（五缝合链证明，同日）

五处同族缝各有单元钉，但「它们是否咬合」此前只被各自自洽的夹具分别回答——本节把缝**合到一条链**上：`TestAutoTriggerBuildsAProductionShapedRun`（`llm_trigger_consume_test.go`）——旋转器拼写事件（`BTC_USDT`）→ `MarketKey` 归一 → **真实审批行**（`LLMMarketGrants{}`）路由 → **真 `buildShadowRunRequest`**（复用 `seedFrozenVersion`/`triggerTestEngine` 活状态夹具）→ 断言请求带生产口径：`MarketID`=三段式 `testMarketID("BTC")`、`PreGates.StrategyState`=**`FROZEN`**、账号与 envelope 哈希逐字；Runner 收请求计 `Runs=1`（0.26s 一次过）。

**证据**：融合钉子 + 既有聚焦面共 9 测全过、`golangci-lint run ./engine/...` 0；engine LLM 测试计数 371→**372**。全套复跑：**全绿 88.301s**（融合钉子后的整绿基线；期间一轮 Bitstamp 单件抖动已隔离复跑验证）。

## 同段尾九：新勘一项非门控残余——`LLMSetRunMode` 端点缺员（同日）

**实锤**：08 §3.6 运行控制族共 6 员（`LLMGetStatus`/`LLMSetRunMode`/`LLMHalt`/`LLMClearHalt`/`LLMGetBudget`/`LLMReconcile`）——现网 **52 个 LLM RPC**（`grep 'rpc LLM' rpc.proto` 全列已核）里**唯一缺员是 `LLMSetRunMode`**（spec: `post /v1/llm/runmode`）；`ModeSwitcher`（M5-12 交付：门禁矩阵 + LIVE 三重解锁 + 重认证/审计/通知）**零生产构造、零 handler、webUI 无写侧入口**——运行模式现只能「改配置 + 重启」落定，10 §5.2 的「切换是一道程序」没有交互面。M5-12 行自述「把新模式的落定交给调用方（配置/引擎字段）」——即**该调用方（RPC）从未落地**，且全仓无登记（TASKS/docs 零处提及为待办）。

**定性**：非门控、非闭包前置（SHADOW/PAPER 数据面用配置模式即可）——属**接口层小件**，拍板前后均可做；与五缝不同，这是「文档有、实现无」的遗漏面而非静默错配。

**实现面待决（留给动手窗口，禁抢跑）**：① 落定语义——live-only（`llmManager` 运行时模式 setter，`EffectiveRunMode` 已读它）还是 config 持久化（写 config.json，重启仍在）抑或两者；② handler 只是装配 `ModeSwitcher.Switch` 的调用方 + 应用新模式（守卫/解锁/审计/通知已全在服务内）；③ proto 工具链已校准（追加端点=纯增量 diff）；④ 写侧入口（gctcli 命令 + W 组 UI——StatusBar 已有模式徽章，缺的是切换按钮）。

## 同段尾十：补员落地——`LLMSetRunMode` 全栈（同段尾九勘定项的动手窗口）

**实现**（按尾九待决清单逐项定案）：

- **落定语义（两者都要）**：引擎字段**立即生效**（`llmManager.SetRunMode` + `cfg.LLM.RunMode`——前者喂 `EffectiveRunMode` 的消费面，后者喂 `buildShadowRunRequest` 的每-run 读取）+ **配置持久化**（`Config.SaveConfigToFile(Settings.ConfigFile)`）；**持久化失败即回滚两处字段并报错**（防「一次降级在重启后复活」的 fail-open 方向）；测试与无配置文件环境跳过持久化。
- **并发保护（实现中发现并补）**：`runMode` 此前为无同步裸字段（Setup 单线程写入），活切换引入写者——补 `modeMu sync.RWMutex`（类型文件）+ `EffectiveRunMode` 读侧 RLock + 新 `SetRunMode` 写侧 Lock。
- **handler**（`engine/rpcserver_llm_mode_rpc.go`，新）：取当前模式（`From` 现读）→ LIVE 时**服务端**组装三重解锁材料（`liveUnlockMaterial`：本账号至少一条活审批绑定 LIVE×binance-mainnet 并解其 `RiskEnvelopeJSON` 为信封；操作员只提供承认口令；**无绑定审批 = fail-closed** `ErrLiveNotUnlocked`）→ `ModeSwitcher.Switch`（重认证=`verifyOperatorPassword` 常时比较、三重解锁、`MODE_SWITCHED` 审计、`llm_mode_switched` 通知——全在服务内）→ 落定；引擎/管理器/配置未装配即拒。
- **proto**：`LLMSetRunModeRequest{run_mode,password,actor,acknowledged}` + `rpc LLMSetRunMode ... post /v1/llm/runmode`；`buf generate` **纯增量**（生成四件符号点验：pb 17 / grpc 14 / gw 16 / swagger 3 处 `LLMSetRunMode`）。
- **测试**：`TestLLMSetRunModeRPC`（真库）——SHADOW→PAPER 全绿（字段双落 + `MODE_SWITCHED` 审计行 actor 点名）→ 错口令 `ErrModeUnauthenticated` 且模式不动 → `TURBO` `ErrModeSwitchInvalid` → LIVE 无绑定审批拒 → 伪承认短语拒 → 全材料落 LIVE；**0.07s 一次过**。
- **登记为剩余面**：gctcli 命令 + W 组 UI 写侧入口（StatusBar 已有模式徽章，缺切换按钮）——接口层小件，随手窗口补；已在 TASKS/PROGRESS 登记。

**证据**：聚焦测试一过；`go build ./engine/` OK；`golangci-lint run ./engine/...` **0**；engine LLM 测试计数 372→**373**。全套复跑：**全绿 94.546s**。

## 同段尾十一：`llm mode` CLI 命令（尾十剩余面之一，同日）

`cmd/gctcli/llm_mode.go`（新；**本目录是 `package main`**——首建犯过一次 `package gctcli`，编译即报双包名，已改）——`llm mode` 子命令：`--run-mode` / `--actor` / `--acknowledged` / `--password`（`--password-stdin` 优先；口令获取照 `approve` 样板逐字复用）→ 调 `LLMSetRunMode` RPC → `jsonOutput` 直出响应。注册进 `llmCommand.Subcommands`（`llmProvidersCommand` 之后）。

**证据**：`go build ./cmd/gctcli/` OK；包测试 2.178s 全过（含既有 3 REPL 测试与命令注册面）；`golangci-lint run ./cmd/gctcli/...` 0；`go build ./...` 全仓 OK。

**剩余面**：仅 W 组 UI 写侧入口（StatusBar 已有模式徽章，缺「切换」按钮 + 确认流：密码 + LIVE 承认短语输入；UI 侧需 types/gctApi/HealthPage + mock 端点与浏览器实测——留给独立窗口）。

## 同段尾十二：HealthPage「运行模式」卡（尾十/十一剩余面收口，同日）

- **`gctApi.ts`**：新 mutation `setLlmRunMode`（内联体类型——免两处同步；`invalidatesTags: ['LlmStatus']` 让徽章与卡随切换刷新）+ 钩子导出 `useSetLlmRunModeMutation`。
- **`HealthPage.tsx`**：`ModeCard`（照 `HaltCard` 样板：自取 `useGetLlmStatusQuery`；卡体=当前模式 Tag 着色；「切换模式」按钮→ Modal 确认流：**目标模式 Select（5 档）** + 操作者（审计留痕）+ **重认证口令**（`Input.Password`）+ **LIVE 承认短语条件字段**（选 LIVE 才出现、必填）→ `setLlmRunMode` → 成功 toast「已审计并通知」；警示条写明「降级与升级同为有意动作、落定写配置、失败即回滚」）。挂载：`<HaltCard />` 之后。

**证据**：`tsc` 0 / `oxlint` 0（30 文件）/ `pnpm build` 2.33s 成功；**浏览器实读**（5173，HMR）：卡渲染「当前模式 SHADOW + 切换模式」；点开弹窗标题「切换运行模式（重新认证；LIVE 需承认短语）」、字段全对（目标模式 Select ×1 / 操作者预填 `webui-operator` / 口令 password 框 ×1）。

**剩余验证面**：`scripts/mock-gateway.mjs` 加 `POST /v1/llm/runmode` 端点（并让 `/v1/llm/status` 的模式随之变化），在代理 `GCT_ALLOW_WRITES=true` 开窗下做浏览器提交流实测（测后恢复 false）——页面功能对真实后端即完整，此行属 mock/实测补齐。

## 同段尾十三：mock 写流实测的副产物——代理端点归一化修复与 dev 栈停放（同日）

为验证「模式切换 UI 写流」（尾十二剩余面）重启 dev 栈，撞出**三个真问题**并处理：

1. **`gct-proxy.mjs` 端点归一化 bug（已修）**：`endpoint = pathname.slice('/api/'.length)` 保留 `v1/` → 白名单键（`llm/status` 等）永不命中、且转发拼成 `…/v1/v1/…`（键与目标双错）。修：`/api/v1/` 优先剥一层。**为什么长期没炸**：在跑的代理实例是**该文件若干次「已编辑但从未重启」之前的旧版**——回归一直潜伏，重启才现形。「重启 dev 组件前先想清它在跑的是哪版代码」自此为本机常识（已入机器记忆）。
2. **重启 env 纪律（已验）**：`dev-all.mjs` 以**纯 process.env** 拉起代理；而 `gct-proxy.mjs` 的文件默认 `GCT_GATEWAY_BASE=https://127.0.0.1:9053/v1/`——**真 bot**！不加 `GCT_GATEWAY_BASE=https://127.0.0.1:9443/v1/` 重启 = 代理直指 9053（窗口内一度出现 allowWrites=true+指向 9053 的实例，**已立即纠正**）。**现停放态**：8788 → mock 9443、`allowWrites:false`、5173/8788/9443 三听齐（health 实读为证）。
3. **未决（留给下一会话，一条探针可解）**：修复后经 8788 的读（`/api/v1/llm/status`）→ 代理正确归一化并转发，上游却 404（代理转 501）；**mock 直探 `http://127.0.0.1:9443/v1/llm/status` 返回空**——疑 mock 的路径基准（banner 自报 base `…/v1/`，可能期望 `/llm/status`）或旧实例同款「文件已编辑而未重启」的对称问题。**`runmode` mock 端点 + 模式切换 UI 均已就位，唯链路读通后补一次写流浏览器实测**（写闸开窗 → 切 PAPER → 卡片/徽章变化 → 恢复 false）即闭环。

**证据**：proxy `/api/health` 实读（`gateway=https://127.0.0.1:9443/v1/`、`allowWrites=false`）；三端口 lsof；mock 日志仅启动行（无崩溃）；aw3 实例上 `/api/health` 实读暴出 `gateway=9053` 的破案点。

**尾十三附记（探针已跑并已更正，2026-10-12 收盘）**：前一轮"Empty reply from server"系**误报**——mock 是 `https.createServer`（`scripts/mock-gateway.mjs:18`），用 `http://` 直探 TLS 监听器必然空响应。改用 `curl -k https://127.0.0.1:9443/...` 复验：`GET /v1/llm/status` → 200（`runMode:"SHADOW" / liveUnlocked:false`，新字段在位）；`POST /v1/llm/runmode`（PAPER+口令）→ `{"status":"ok"}`、状态实读变 `PAPER`、恢复 `SHADOW` 成功。进程（PID 16526，启动 20:30:42）晚于文件 mtime（20:26:11）——**运行的就是当前文件；mock 无漂移、读/写链均健康**。**经代理读链复验已完成**：`GET http://127.0.0.1:8788/api/v1/llm/status`（及 `/api/llm/status` 别名）→ 200 全量状态载荷（`runMode:"SHADOW"`）——5173→8788→9443 三听齐、读链全绿，代理端点归一化修复获实证（此前 404 确诊为该 bug）。**写流浏览器实测已完成（2026-10-12）**：写闸开窗重启代理（`GCT_ALLOW_WRITES=true`，PID 21576）→ 5173 健康页 ModeCard「切换模式」对话框：选 PAPER+口令提交 → 徽章与「当前模式」双双变 PAPER；curl 经代理读回 PAPER 佐证；反路径切回 SHADOW，toast「已切换到 SHADOW（已审计并通知）」、读回 SHADOW 佐证——**UI→5173→8788→9443 写链双向全通**。随后代理已恢复（PID 23863，allowWrites=false）：POST 被拦 501 WRITES_DISABLED、GET 200——写闸回归安全默认，dev 栈停放正位。
