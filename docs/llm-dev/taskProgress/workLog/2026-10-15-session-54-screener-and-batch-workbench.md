# session-54 · 新增功能立项：对话筛选合约 + 批量决策工作台 + 持仓复核

日期：2026-10-15 目标：用户三项新需求（①对话里按自然语言指标筛 300+ 合约取 TopN ②GUI 批量调 Jev 生成开仓决策 ③持仓复核调 Jev 生成持有/平仓决策）

## 立项文书（先读）

- `docs/llm-dev/13-合约筛选与批量决策工作台.md` —— 技术方案（现状评估证据先行 + 设计 + 实施顺序）
- `docs/llm-dev/OPEN-QUESTIONS-新增功能-待确认.md` —— A 类（已按安全默认落地，备查可改）/ B 类（真阻塞——暂无）

**核心结论**：**不引入 Mastra 类外部 Agent 框架**——"自动收集指标"由既有的 Go 原生工具循环承担（4 轮/8 调用/24k token 预算、scope 闭集、ToolEvidence 信封、落库审计全会自动继承）；新增的是**确定性筛选器 + 一个工具**，指标"不硬编码"=metric registry 注册表（新增指标=加一行）。

## 本段增量

### 一、USDT-M 批量 ticker 补 `PercentChange24Hour`（[x]）

- **病根**：`U24HrPriceChangeStats` 响应含 `priceChangePercent`，但 `binance_wrapper.go` 三条路径（USDT-M 批量、USDT-M 单对、CMF 批量/单对）都只拷 OHLC+量——`PercentChange24Hour` 恒 0（快照 ticker 事实同受害）。
- **修复**：四处映射补 `PercentChange24Hour`（CMF 用其 `PriceChangeStats` 同名字段）。
- **测试**：`TestUpdateTickers` 扩断言——批量刷完后对首对 `ticker.GetTicker` 断言 24h 涨跌幅非零；`go test ./exchanges/binance/ -run TestUpdateTickers` 绿。

### 二、`llm/screener` 全宇宙筛选器（[x]）

- `screener.go`：`Market`（ticker 三事实 + 收盘 K 线窗口 + 可选 T2 流）、`Criteria{metric,op,value,value2,state}`、`Evaluate`（AND 过滤 → 排序 → 截断 ≤50/默认 20）；**计数语义**：`FilteredOut`（条件不满足）与 `ExcludedForMissing`（指标缺席）分开计——缺席永不伪装成 0。
- `metrics.go`：**注册表**（12 指标）——
  - T0（缓存 ticker，零权重）：`change24h`、`quoteVolume24h`、`lastprice`；
  - T1（缓存收盘 K 线，零权重；250 根量级）：`change4h`、`change48h`（收盘对收盘）、`atrpercent`、`range24h`、`macdstate`（BULL/BEAR 柱符号）、`macdcross`（3 根内变号 BULL_CROSS/BEAR_CROSS/NONE）；
  - T2（逐符号场地读）：`takernetflowratio24h`（(买−卖)/(买+卖)，**跨市场可比、免单位争议**——净流入口径 A1 的默认）、`takernetflow24h`（原样差额，注记单位未换算）。
  - MACD 自实现 12/26/9（EMA 以 SMA 播种）；**夹具纪律**：纯单调序列的柱状图收敛到 0（退化）——测试用"长窗+尾部反转"形态。
- 测试：6 函数（过滤/排序/截断、缺席双计数、分类指标与校验拒绝、between/边界、目录完整性、走查）全绿。

### 三、`screenMarkets` 对话工具 + 引擎执行器（[x]）

- `llm/tools/screener.go`：工具名 `screenMarkets`；schema **由注册表生成**（指标 enum、分类 state、操作符、上限 8/3/8/50）——提示词里的指标清单与求值器同源不漂移；`DecodeScreenMarkets` 严格解码+边界。
- `engine/llm_screener.go`：`buildScreenerUniverse` 三层齐料（T0 `ticker.GetTicker`；T1 `KlineCache.closedCandles`；T2 `iTakerFlowSource`（binance `UTakerBuySellVol` 形态接缝）仅在 criteria/sort/include 引用 T2 指标时读，**quoteVolume 前 K=FlowScanLimit（默认 100，上限 600）**、8 并发、逐市场失败记缺席）；`notes` 如实带出扫面（缺 ticker 数/窗口/流量扫描成败数）。
- 装配：`LLMToolRunner` 增 `Markets/Interval/FlowScanLimit`，`buildScopeToolRunner` 三件齐设；config 新增 `llm.screener.flowScanLimit`（默认 100 + 钳制）+ config_example。
- 执行器：Run switch 第 9 个 case；证据质量 `DATABASE_CACHED`（缓存主档），工具结果经既有证据卡通道自动回显给聊天。
- 测试：`engine/llm_screener_test.go` 端到端（真缓存合并 + 真 ticker 全局存储 + 假 T2 源；三次筛选断言行/计数/比值；未装配 fail-closed）绿。
- 计数：本段新测试 = screener 包 6 + engine 2 + binance 扩 1 → **engine LLM 测试计数 387→389**。

### 四、筛选指标目录 RPC（[x]）

`LLMGetScreenerMetrics`（GET `/v1/llm/screener/metrics`）——GUI 表单与后端注册表同源（表单不可能给出后端算不了的指标）；`LLMScreenCriteria/Sort/Row` + `LLMRunScreen`（POST `/v1/llm/screener/run`）——GUI 直调同一求值器（handler 经 `llmDecisionRunner.Analysis.Tools` 断言到生产 `LLMToolRunner`，测试替身即拒）。

### 五、批量决策作业 + RPC（[x]，增量五）

- `engine/llm_batch_runner.go`：内存作业表 + **串行**执行 + 失败隔离（单市场错误记该行）+ 审批绑定（版本在本部署无活跃审批即拒，A5）；`RunOnce` 只读承诺继承（只记录不下单）。
- `rpcserver_llm_batch.go` + proto 两 RPC（`LLMStartBatchDecision`/`LLMGetBatchDecision`，纯增量再生成）；`llm.batch.maxMarkets`（默认 10、硬上限 50）。
- 澄清：`RISK_ALL_ORDER_BUDGET` 的窗口上限在生产装配=0（无闸）——批处理串行不撞它，**无需豁免件**。
- 测试：引擎 3（串行+失败隔离/全部拒绝径/复核 kind 走 ReviewBuild）+ handler 往返（起批→轮询→行核验→未知作业拒）。

### 六、持仓复核运行（[x]，增量六）

- `engine/llm_position_review.go`：场地持仓读（`iPositionSummarySource`；无持仓即拒）→ 开仓时刻（`LastEntryIntentAt`，读不到以 48h 兜底并**如实标注** fallback）→ 缓存 K 线窗口（change%/最大不利偏移/区间振幅，纯函数 `positionReviewStats`）；`PositionReviewContext` 经 `ShadowRunRequest.Review` 注入分析消息（`positionReviewBlock`）；`Trigger` 字段落 `MANUAL_POSITION_REVIEW`；`ReviewBuild` 接进批量作业。
- 测试：纯函数 2（统计/消息块含 fallback 标注）。

### 七、webUI 两页 + mock/代理配套（[x]，增量七）

- `ScreenerPage`（`/screener`「筛选与开仓决策」）：指标目录下拉（tier 分组）、条件构建器（数值 op vs 分类 eq 自适应）、排序/TopN、运行筛选、结果表**动态列**（— 表示缺席）、勾选→批量开仓决策 + 2s 轮询 + 逐行裁决/运行 id。
- `PositionsReviewPage`（`/positions-review`「持仓决策」）：持仓表（getallmanagedpositions）→勾选/全量→`POSITION_REVIEW` 批量 + 轮询。
- `gctApi` 四端点 + 两 tag；types 补齐；App/NAV 注册。
- mock-gateway 补 4 端点（screener metrics/run + batchdecisions start/get，内存作业表）。
- **浏览器实测（写闸窗口，测后已恢复）**：`/screener` 渲染（10 指标徽章）；运行筛选→2 行渲染；填版本 id→「对选中 2 个生成开仓决策」→起批→轮询→逐行 `DONE/HOLD/AWAITING_HARD_RISK_AND_APPROVAL/run-*`；`/positions-review` 渲染（mock 未实现持仓端点时如实红条 `not found: /v1/getallmanagedpositions`，不伪装）。**代理写闸已恢复 false（POST 501 实证、GET 200）**；dev 栈三听齐（9443/8788/5173）。
- 前端门：`tsc --noEmit` 0 · `pnpm lint`(oxlint) 0/0 · `pnpm build` ✓。

### 八、增量四（迁移 v20 `decision_runs.market_id`）——**暂缓（[-]）**

批量结果逐行自带 marketId；既有决策列表可经 snapshot join 展示市场——避免 sqlboiler 双方言重生成链的开销。有明确列表需求时再做（登记在 TASKS N4）。

### 九、验证补强（session-54 续，[x]）

1. **对话走廊 × 真筛选执行器集成测试**（`TestChatCorridorExecutesScreenMarketsThroughTheLoop`）：脚本化模型传输发出一次 `screenMarkets` 工具调用 → 管线经**真实工具循环**执行（真 K 线缓存/真 ticker 全局存储/假 T2 源）→ 证据信封（`ref: tool:screenMarkets:call-9`、`DATABASE_CACHED`、只含命中行）回填给模型第二轮 → 最终答复引用 `BTC_USDT` 并正常封存回合——需求①「自然语言→子 Agent 抓取」在**管线组件层**的最强离线证明（此前只到 `runner.Run` 直调层）。
2. **持仓页单市场入口**（需求③"单/批量"的单一侧补齐）：`/positions-review` 增"单个市场 id → 生成单个持有/平仓决策"；与筛选页单市场（DOGE）双双**浏览器实测**：起批→轮询→逐行 `DONE/HOLD/AWAITING_HARD_RISK_AND_APPROVAL/run-*`（写闸窗口，测后恢复 false=POST 501 实证）。
3. engine lint 0（新测试含内联管线装配，复用既有夹具）。
engine LLM 测试计数 389→**397**（本续段再 +8：批量作业 3 + handler 1 + 持仓复核 2 + 对话走廊 1 + 活跃审批 1）。**engine 整套（-skip 网络类）81.380s 全绿**（A5 落定后终版跑）。

4. **A5 完整版：活跃审批列表 RPC**（`LLMListActiveApprovals`，GET `/v1/llm/approvals/active`，按账号/网络可选过滤；引擎测试：只列活审批（RevokeStrategyApproval 后再列不出现）+ 过滤径）。webUI 两页的"策略版本"由手输升级为**下拉自动预选活跃审批版本**（清空本地存储后重载浏览器实测：自动选中 `ver-7f3a2b1c-0001（SHADOW · epoch 3）`；手输仅剩无审批列表时的兜底）。mock 补该端点（无审批时给出一条 `ver-mock-1` 供离线演示）。

### 十、持仓数据面收口（session-54 续二，[x]）

增量七实测时 mock 尚无持仓端点（持仓页只验了空态红条）；本次补齐需求③的真实数据面，并**自查修掉一个真实形状 bug**：

1. **前端形状 bug（真实 bot 下必崩）**：`PositionsReviewPage` 此前把 `positions.data` 直接当数组用（RTK 返回的实为 `{positions: [...]}` 包装对象）、方向判断读的 `total` 字段在 `gctrpc.FuturePosition` 中**根本不存在**——真实 bot 返回非空持仓时 `.map` 抛 TypeError、方向列恒 "—"。修复：`positions.data?.positions ?? []` 解包；数量列改 `currentSize`（十进制字符串）、方向列改 `currentDirection ?? openingDirection`（order.Side 词汇表 BUY=多 / SELL=空，非 BUY/SELL 渲染 "—"）；`ManagedPosition` 类型同步真实字段（去 `total`）。
2. **形状实勘（读源码钉死）**：`engine/rpcserver.go:202` `runtime.NewServeMux()` 无自定义 marshaler → protojson 默认 **camelCase**（与 `gctApi.ts` 头注释既有实测约定一致）；字段清单出自 `buildFuturePosition`（rpcserver.go:3760：`currentDirection`/`currentSize` 为 `position.LatestDirection/LatestSize` 的字符串化）；空持仓返回 200 `{"positions":[]}`（非 500——"no positions found" 是单个 `getmanagedposition` 的行为）。
3. **mock 补 `getallmanagedpositions`**（老端点；BTC 多 0.01 / SOL 空 0.5，与真实网关同形状同词汇表）——持仓页离线演示完整。
4. **浏览器实测（写闸窗口，测后已恢复）**：持仓表两行渲染（`Binance:usdtmarginedfutures:BTC_USDT` 0.01 多 · `...:SOL_USDT` 0.5 空）→ 版本下拉自动预选 `ver-7f3a2b1c-0001`（A5）→「对全部 2 个生成持有/平仓决策」→ 进度 2/2 → 逐行 `DONE/HOLD/AWAITING_HARD_RISK_AND_APPROVAL/run-1-BT · run-1-SO`。**代理写闸已恢复 false（POST 501、GET 200 双实证）**。
5. 前端门：`tsc --noEmit` 0 · `pnpm lint`(oxlint) 0/0 · `pnpm build` ✓（1.98s）。无 Go 侧改动，engine LLM 测试计数不变（397）。
6. **两处检查器对本地环境误报的修复（入库脚本，CI 同源）**：
   - `scripts/misc_checks.sh` §7（LLM invisible Unicode）：命中两类非注入源——① `gctrpc/rpc.pb.go:25365` 的 descriptor 字节串（`0xCC 0x9F` 恰为合法 protobuf varint 长度前缀、同时构成组合字符 U+031F；**上游旧版恰好没有、任何 proto 重生成可能往返出现**——已用 `git show 34d834da:gctrpc/rpc.pb.go` 实测对照）；② `webUI/node_modules/**` 第三方 README/locale（gitignore、CI checkout 不存在）。**修法=排除生成物与依赖目录**（`*.pb.go`/`*.pb.gw.go`/`*.swagger.json` 与 `node_modules`/`dist`），并加**反例自测**（临时注入 U+3000 → 仍被检出 FAIL，检测未削弱）+ 移除后 All passed。
   - `.markdownlint-cli2.yaml` ignores 加 `**/node_modules/**`（同根因；修后全仓 362 文件 0 issues）。

### 十一、可发现性与演示链补强（session-54 续三，[x]）

1. **双向自然语言引导**（13 号文 §3.4 设计项补落）：`ScreenerPage` 左卡片顶部 Alert + `<Link to="/chat">「对话」</Link>` 带示例话术（"24h 涨幅 >5%、4h 也上涨、MACD 为 BULL 的前 10 个合约"）+ **对话页空态**（`ChatPage`）同句示例（"也可以直接让我筛合约…"）——需求①"完全靠对话筛选"的双向可发现性入口；**浏览器实测**：筛选页 Alert（含链接 url=/chat）与对话页空态示例均实读渲染。
2. **A8 登记**（`OPEN-QUESTIONS-新增功能-待确认.md`）：需求②"开多/开空"的两解——默认=方向由分析链产出（GUI 不指定方向，与"候选只能由分析产出"的安全边界一致）；备选=GUI 方向意图仅作分析提示注入（不硬约束）；变更记录同步。
3. **mock 演示链闭合**：批量作业行联动 `decisionRuns`（`trigger=MANUAL_POSITION_REVIEW|MANUAL_BATCH`）——批量产出在「决策与审计」列表可见、血缘抽屉完整渲染（十步链 + 运行摘要 + 候选/Jev 原文/转录/风控证据/审计事件）；curl 直探 mock + 浏览器双验证（**写闸全程未开**——mock 直探绕过代理，只读纪律零妥协）。
4. 门：tsc/oxlint/build 0；misc All passed；markdownlint 362 文件 0。无 Go 改动（engine LLM 测试计数 397 不变）。
5. **mock 对话筛选场景分叉**（chatstream 按消息含「筛/找/符合」→ 演示 screenMarkets 全流程：筛选话术 + 两行命中表格 + `tool:screenMarkets` 证据卡〔DATABASE_CACHED〕；否则保持 BTC 分析场景）——需求①对话链**离线全貌**：空态引导 → 提问 → 流式筛选结果 → 证据卡，浏览器实测全流程渲染。

### 十二、文档↔实现审计：复核消息块两字段补落（session-54 续五，[x]）

逐条核对 13 号文 §3.3 时发现设计列明但实现缺的两字段：**自开仓资金费累计**与**保护性止损现状**（复核消息块只到走势统计）。本次补落：

- **资金费累计**：新 venue seam `iFundingIncomeSource`（binance `UAccountIncomeHistory`，engine 已 import binance——同 `iTakerFlowSource` 先例）+ `fundingFeesSince`（FUNDING_FEE Σ + payments 数，窗口=自开仓或 48h 兜底；**读失败/venue 无读面 → note 如实标注，绝不伪装 0**）。
- **保护性止损现状**：`LastEntryIntentAt` 扩签名带 intent id（两方言改 `ORDER BY created_at DESC, id DESC LIMIT 1`；原 `MAX(created_at)` 无法把 id 对上）→ `latestProtectiveStop` 经 `ListProtectiveExitsByParent` 取最新 exit 行（状态+触发价）；无父意图/无行/读失败三分支均如实。
- `positionReviewBlock` 新增两行：`funding fees since entry: X USD (N payments)` / `unavailable (...)`；`protective stop: PLACED at X` / `FAIL_SAFE` / `none on record` / `unavailable (...)`。
- 测试：engine 3（`TestFundingFeesSince` 三分支含无读面 / `TestLatestProtectiveStop` 真库三态〔新 sqlite helper `setupPositionReviewDB`，同 strategy-admin 款〕/ `TestPositionReviewBlockRendersVenueFacts` 两渲染形态）；repository `TestLastEntryIntentAt` 更新（id 断言）。**engine LLM 测试计数 397→400**。
- 电池：lint 0（清 gocritic `unnamedResult`/`sloppyReassign`、ineffassign）、gofmt 净、repository 聚焦绿、**engine 整套（-skip 网络类）89.733s 全绿**。

### 十三、"指标不硬编码"扩展演练：新增 `macdhist`（session-54 续六，[x]）

对"新增指标=注册表加一行"的工程承诺做实战演练（新的这个指标同时给用户"MACD 走势"一个**连续可排序**的读数——`macdstate` 三态的数值版）：

- `llm/screener/metrics.go`：注册表**加一行**（KindNumeric/TierCache/Unit "price units"）+ `macdHistValue`（复用 `macdHistogram`，取最新柱值）——注册表 11→12。**其余全链路零改动自动继承**：工具 schema enum（Catalogue 生成）、GUI 指标下拉与 tier 分组（RPC 目录驱动）、criteria/sort 校验、证据/缺席计数。
- 测试：screener 包 +1（`TestMacdHistMatchesState`：柱值符号与 `macdstate` 同源、SOL 缺席计数、数值 criteria 通过面）→ 该包 6→7；engine LLM 计数不变（400）。
- mock metrics 同步补齐至与注册表**逐项一致**（12 指标；顺带补上此前 mock 缺的 `takernetflow24h`）。
- 工具 description 修订：加**何时使用**触发句（"Use when the operator asks to find, filter or scan markets by indicator conditions…"）——直接提高真实模型对需求①的自主触发率（活体验证前的最后一处提示面收口）。
- **浏览器实证**：筛选页徽章由 10→「11 个指标」自动更新（前端零改动；mock 补 takernetflow24h 后为 12）。
- 电池：`go test ./llm/... ./config/` 全绿、engine Screen 聚焦绿、`golangci-lint run ./llm/screener/...` 0。
- **活体验证剧本成文**：`docs/llm-dev/14-三需求活体验证剧本.md`（前置配置 + 三需求逐段步骤/预期/排障 + 通过标准）——把环境门控的剩余验证变成用户可直接照跑的清单；13 号文 §5 已指向。

### 十四、`screenMarkets` 证据预算裁剪（12KB 硬限边界，session-54 续九，[x]）

对 §3.1 逐条核对时发现文档与实现语义不符：`candidate.MaxToolResultBytes=12288` 是**硬拒绝**（`Validate` 返回 `ErrToolResultTooLarge`），不是"截断自动生效"——`screenMarkets` 每行含多指标数字（值可能很长），**limit=50 且多指标时序列化可达 ~20KB → 整个工具调用会失败**（对照：`getCandleWindow` 有 `toolCandleReturnLimit=50` 且行更瘦 ≈7.5KB，天然安全——这正是它没爆的原因）。

- 修法（`engine/llm_screener.go` + `llm_tools.go`）：`fitScreenResultToEvidenceBudget`——工具路径在封 `ToolEvidence` 前按 `MaxToolResultBytes-1024` 预算**从尾部（排名最后）逐行裁剪**至满足，并在 `notes` 追加"trimmed to N rows…（M omitted; narrow criteria or lower limit for a complete view）"——**诚实裁剪，永不静默**；**仅工具路径**生效（GUI RPC 直调 `RunScreenMarkets` 不受限）。
- 测试：engine +1（`TestFitScreenResultToEvidenceBudget`：小结果零改动；50 行×12 指标触发裁剪、序列化 ≤ 预算、notes 标注）→ **engine LLM 测试计数 400→401**。
- 13 号文 §3.1 的"12KB 截断自动生效"句已修正为准确语义。
- 电池：lint 0（清 forcetypeassert ×2）、聚焦绿、**engine 整套（-skip 网络类）90.975s 全绿**。

### 十五、批量起批审计（治理缺口修复，session-54 续十，[x]）

§3.2 逐条核对时发现：批量起批（GUI 的"批量生成开仓决策 / 持仓复核"）**不落任何审计**——对照模式切换（`MODE_SWITCHED`）、审批授予/撤销、市场授权均有审计留痕；起批是同类治理动作（谁、何时、对哪些市场、绑哪个版本）。

- 实现：`BatchRunner` 增 `Audit iAuditEvents` 字段；`Start` 在审批校验通过后、作业落内存表前写 `BATCH_DECISION_STARTED`（detail=`{kind, strategyVersionId, accountId, network, markets}`、subject=jobId、actor=操作者）；**审计写失败即拒绝起批**（fail-closed，与 `MODE_SWITCHED` 同级）；字段 nil 容错（生产装配 `LLMDecisionStore{}` 必设——engine.go 装配已补）。
- 测试：engine +1（`TestBatchRunnerAuditsLaunch`：审计字段/subject=jobId/detail 市场数；审计 store 失败→起批拒绝）→ **engine LLM 测试计数 401→402**。
- mock 同步：`POST /v1/llm/batchdecisions` 写入审计流（`BATCH_DECISION_STARTED` 进 auditEvents）。
- 13 号文 §3.2 已补"审计"条目。
- 电池：lint 0、`TestBatchRunner*` 聚焦绿、**engine 整套（-skip 网络类）91.193s 全绿**；curl 直探 + 浏览器双验证（审计流渲染 `BATCH_DECISION_STARTED · probe-op · job-1` 行；写闸全程未开）。
- 教训（过程）：mock 重启曾用记忆 pid 而旧实例占着端口——新进程 EADDRINUSE 静默死掉、请求仍由旧代码处理；**重启一律先 `lsof` 取实际 pid**（本轮以 12 指标特征码识别出旧进程后纠正）。

### 十六、真 bot 拉起 + 「新建档案」入口补缺（2026-10-15，会话收尾）

### 十七、vault 移除改造（用户拍板：删密钥保管，Provider 直接填明文 Key）——**【进行中】**

用户 2026-10-15 明确要求："把密钥保管这一个能力彻底删掉；Provider 的配置中直接配置明文 API Key；写闸保持开启不要关。" 按此改造：

**后端（已完成，全绿）**：

- `llm/secrets` 整包删除；`engine/rpcserver_llm_vault.go` 删除；proto 三 vault RPC/三消息删除（重生成 4 产物）。
- 明文 key 落点：**复用 `api_key_ref` 列承载明文**（表无 CHECK、**零迁移**）；`api_key_fingerprint` 列改为**服务端派生**（`deriveKeyFingerprint` = sha256 前 8 字节 hex）。
- `ProviderRevisionParams.APIKeyRef → APIKey`（删 Fingerprint 字段）；`llm_client_factory.go`（`revisionSecretFn` 明文直用、`BuildActiveClients(ctx)` 去 vaultDir）；probe handler 同改；`providerRevisionToProto` **不回明文**（proto `LLMProviderRevision.api_key_ref` reserved 7）；`LLMUpdateProviderProfileRequest.api_key_ref → api_key`（fingerprint reserved 6）。
- chat 会话加密**保留但解耦**：密钥目录改 `<datadir>/llm-chat-keys`（`conversationKeyDirName`），config 删 `llm.vault.directory` 字段。
- 测试改造：`vaultFixture → keyFixture`（明文）；factory/chat/decision/consume/usage/provider 六测试文件切换；M6-07 演练改为"明文 key 轮换=追加新修订版"；M6-08 演练改两件半套（库+配置+chat 键目录），**库含明文 key 如实断言 + 会话正文仍须密文**。**全仓 `go vet` 干净、相关测试全过**。

**前端（已完成）**：`ProvidersPage` 删 vault 卡整块与三 hooks/状态/handlers；编辑表单 **apiKeyRef+指纹 两框 → 单个「API Key（明文）」Password 框**（编辑修订版须重新粘贴——修订版是完整快照，key 从不回显）；新建 Modal 说明与抽屉警示文案改明文口径；契约表 `apiKey` 行；`gctApi/types` 删 vault 三端点与类型、update 参数 `apiKey`；SettingsPage 密钥存放文案改；mock 删 vault 段与 `apiKeyRef` 字段。前端门：tsc 0 · oxlint 0/0 · build ✓。

**引擎切换（已完成）**：新二进制构建（`gocryptotrader.bak-vault-era` 留旧档）→ 重启 9053（pid 22003）→ 冒烟：health 写闸保持 true（用户要求）、getinfo/llm status 正常、**`GET llm/vault` 上游 404→代理 501（路由确已消失）**；浏览器实读 /providers：**vault 卡消失、契约表明文口径、用户此前建的 "DeepSeek"（0 修订）档案保留**。

**电池（全绿）**：engine 整套 88.850s · 聚焦（factory/chat/decision/rotations/backup/provider）全绿 · `golangci-lint` 0（清 gci×4/gocritic×2/revive×1） · gofmt 净 · 全仓 `go vet` 干净 · misc All passed · markdownlint 0 · 前端 tsc 0/oxlint 0/0/build ✓。

**安全语义（如实）**：明文 provider key 现按设计存于本地库修订版行；界面/接口只回派生指纹；chat 会话正文加密不变。

**过渡期提醒（对用户）**：已可重新开始配置——新建档案 → 填 Base URL/模型/**明文 API Key** → 提交 → 探针 → 激活配对。

### 十八、jev-latest 放开 + 问题模板版本免填（2026-10-16 续，用户拍板）

用户两条新决定：①**放开 `jev-latest`**（已知回放代价仍要）；②问题模板版本**用户免填**（该系统常量无用户更新入口）。

- **jev-latest 放开（单点化）**：`llm/jev/request.go` 正则 `^jev-(\\d+\\.\\d+\\.\\d+|latest)$`；`IsPinnedModel → IsAcceptedModel`（6 处调用点：client/manifest/probe/validate/BuildReviewRequest）；`ErrModelInvalid` 文案改；**engine admin 删自持 `jevModelPattern`、改调 `jev.IsAcceptedModel`**（规则唯一来源）。**回放对冲（如实）**：每次评审响应原文（含实际 `model`）照旧落库——审计可回读真实版本；06 号文 §5.5 与字段表已加变更注记。
- **模板版本免填（后端默认）**：`AddProviderRevision` 对 JEV 且未填时自动取 `jev.QuestionVersionReviewV2`（新修订版自动跟随常量升级）；`validateRevisionParams` 不再苛求非空；**前端 JEV 表单移除该输入项**（改说明"由系统自动记录（jev-review-v2）"）、提交不再带该字段、契约表行改；展开行仍展示各修订版记录的模板版本（审计可见）。
- **测试**：llm/jev 三处更新（`jev-latest` 移入接受集、非 jev 形状仍拒："gpt-4o"/"jev-1.13.0-beta" 等）+ **engine 新增** `TestAddProviderRevisionAcceptsJevLatestAndDefaultsTemplate`（latest 接受 + 模板版本默认=常量）。
- 电池：`llm/jev` 全绿 · **engine 整套（-skip 网络类）88.444s 全绿** · `golangci-lint` 0 · 前端 tsc 0/oxlint 0 · 引擎已换新构建重启（pid 52712）并冒烟（health/getinfo/llm status 正常）。

**背景**：用户要求核查配置并拉起全栈实测。核查结论：币安密钥已配；**LLM/Jev 完全未配**（config 无 llm 段、DB provider 0 行、vault 未初始化）。真栈已拉：新版二进制构建（旧档 `gocryptotrader.bak-oct10`、config 备份 `config.json.bak-2026-10-15`）→ 停旧起新（9053，pid 80687，`llm_manager=READ_ONLY`，两条 WARN 如实报 provider/配对未配）→ 代理 8788 切真网关 → 浏览器横幅"已连接"、健康页全部卡实时。

**W4-02 补缺（用户在真栈上使用「模型与配对」时发现）**：档案表**没有"新建档案"入口**（行操作仅编辑/探针；`onSave` 只走 update 追加修订版）——表空时无法从 UI 创建首个档案。补齐：

- `gctApi.ts`：`createLlmProviderProfile` mutation（POST `llm/providers`，body `{kind, displayName, actor}` → `{profile}`）+ 类型 `LlmCreateProviderProfileResponse`（import/export 两处同步）。
- `ProvidersPage.tsx`：表头 extra「新建档案」→ Modal（类型下拉 LLM_OPENAI_COMPAT/JEV、显示名、操作者默认 webui-operator）→ 创建成功后**自动直开编辑抽屉**填首个修订版（Base URL/模型/密钥引用/指纹）。
- mock：`POST /v1/llm/providers` 同步（直探验证 prof-mock-3 入库可见）。
- 验证：tsc 0 · oxlint 0/0 · build ✓（827ms）· 浏览器实测「新建档案」按钮→Modal 渲染（三类字段与默认值就位）→零提交关闭（不产生演示数据）。
- **纪律注记**：本次配置核查脚本曾把一个嵌套字段（`api.credentials`）明文打印到会话输出——已向用户如实告知并建议轮换币安 key；后续检查一律布尔化输出。

## 电池（增量一~八）

`gofmt`；`golangci-lint run ./engine/ ./llm/screener/... ./llm/tools/... ./config/...` → **0 issues**；`go build ./...` OK；聚焦测试全绿（screener 包 6、engine 端到端/批量/复核 8、binance ticker、tools 契约 9 件）；`make misc_checks` **All passed**（首跑抓修：workLog U+3000 一处）；前端 tsc/oxlint/build 0；浏览器实测三段落（见增量七）。**engine 整套（-skip 网络类）85.018s 全绿**（前跑抓出 chat 八工具断言→已改九后复跑绿）。

### 十九、探针实弹连环修复（2026-10-16，用户实配模型后的真实上游验证）

用户按新表单实配 LLM=deepseek-flash / JEV（base 填了全端点）后探针报错；逐层根因定位（证据=DB probe_results 四层 + 官方 `api.typesafe.ai/openapi.json` + 蓝本 `hyperliquid-auto-trader/packages/ai-adapters/src/jev.ts`），共九处修复：

1. **JEV 404**：base 已含 `/v1/systemone` 而构造又拼 → **endpoint dedupe**（两种填法均可）；测试加两形态断言。前端 placeholder 同步说明。
2. **LLM STREAM 400「you passed .」**：`probeStream` 直发 `c.post` **绕过了 `normaliseRequest`**（model 空）→ 补 normalise；测试加 wire `"model"` 断言（此盲区曾让旧测试假绿）。
3. **LLM SCHEMA TRUNCATED**：预算 256 太小 → 1024 仍被 deepseek-flash 的推理过程全量耗尽 → **4096**（实测过）。测试断言同步。
4. **LLM SCHEMA CANDIDATE_INCOMPLETE**：探针提示词未列全键、模型未逐字抄 ID → prompt 补**完整键清单 + “IDs 逐字复制” + thesis/invalidation 非空**。
5. **JEV 422 `union_tag_not_found`**：官方 openapi 实证 questions/answers 以 `type`(=choice|score|noul) 为判别器，**我们的问题缺 type** → 三问题类型加 `Type` 字段 + 六构造点（探针/评审各三）+ 回归测试 `TestQuestionsCarryTheirTypeTags`。
6. **JEV 400「Unknown model: jev-1.0.0」**：placeholder 示例误导 → 前端示例改 `如 jev-1.13.0`；**替用户追加 JEV 修订版 v3（model=jev-latest，key 库内搬运未回显）**。
7. **JEV_MODEL_DRIFT 误判**：请求 latest 而实服务 1.13.0 → 新 `ServedModelMatches`（精确请求仍查漂移；latest 接受任意打点实版本——实版本本就随响应落库可审计）+ 测试六分支。
8. **JEV_SCHEMA_INVALID（`.synthetic_score.legend`）**：官方 ScoreAnswer 的 `legend` 是**对象**、且**无 probabilities**（score=概率加权期望）——我方形状错 → `ScoreAnswer` 官方对齐（legend map、去分布、score∈[0,n-1]+confidence 0..1 校验）+ 全部 fixtures 更新；“missing bucket/分布求和”等 score 侧旧 case 删除（choice 侧分布校验不动）。
9. **llm 目录树测试**：secrets 包删除后更新断言清单。

**终局（真实上游）**：LLM 四层全过（AUTH/TOOL_CALL/SCHEMA/STREAM）；JEV SYSTEMONE 全过（served `jev-1.13.0`）。电池：`llm/...`+config 全绿 · lint 0 · misc All passed · markdownlint 0 · 引擎新构建运行（9053）。

**下一步（用户）**：页面上「切换配对」激活 → readiness READY（探针已全过，材料齐）。

### 二十、首次激活入口修复（2026-10-16 续，用户在真栈上撞到的阻断）

用户配好双探针（全过）后仍无法激活配对，问「如何把两个已配置的模型启用并配对」。根因=**前端把首次激活入口自己锁死了**：`/providers` 顶部「切换配对」按钮 `disabled={!pair.data?.found}`——而 found=false（从未激活）恰是首次激活的唯一场景；后端全链本就直接支持（`LLMSwitchActiveModelPair` 重认证 + `CasActiveModelPair` 在 expectedEpoch=0 且无行时直插首行）。修复仅前端三处：

- `ProvidersPage.tsx`：按钮禁用条件改 `!pair.data`（仅数据未到时禁用）；`openSwitch` 移居 live 选项定义之后并改「当前指针或两侧最新 live 修订版」预填 + `Number(pairEpoch ?? 0)`（首次激活直接带出两侧选择、epoch=0）；not-found 文案补指路（点右上角按钮、expected 保持 0）；弹窗正文在首发场景改「首次激活：把两侧选定的 live 修订版设为全局决策模型」。
- 验证：tsc 0 · oxlint 0/0 · **浏览器实测（真栈 5173/8788/9053）**：按钮已可点 → 弹窗预填 `DeepSeek #1 · deepseek-flash` / `Jev #3 · jev-latest` / epoch=0 / 幂等键自动 UUID，口令留空不回显 → 未提交关闭（不产生数据）。
- 后端零改动；mock 侧 POST activepair 的 CAS 语义本就与真端一致。

### 二十一、切配/探针/编辑三 mutation 的 LlmStatus 失效补全（2026-10-16 续，用户激活成功后的实测反馈）

用户激活成功（epoch 1 / READY / updatedBy=webui-operator，代理读回实证）后反馈：顶部「当前 LLM/Jev 组合」卡（读 `llm/status`，tag=LlmStatus）在提交成功后不自动刷新，需整页刷新。根因=`switchActiveModelPair` 的 `invalidatesTags` 只列了 `LlmProviders`——配对行会重取而状态卡不重取；同类漏配还有 `testLlmProviderProfile`（探针判决改 readiness）与 `updateLlmProviderProfile`（追加修订版可能清指针+降级）——三处一并补 `'LlmStatus'`。

- 验证：tsc 0 · oxlint 0/0；**真栈浏览器实测失效链**——点 DeepSeek「探针」（四层全 PASS：AUTH 913ms / TOOL_CALL 1275ms / SCHEMA 3105ms / STREAM 717ms）→ 探针 POST 返回后**自动**依次重取 `llm/activepair`、`llm/providers`（含 includeArchived）、`llm/status`（网络日志 reqid 128955~128958 紧跟 128954）——状态卡/横幅与配对行同源刷新机制就此证实。
- `StatusBar.tsx` 顶部横幅与 ProvidersPage 状态卡共用同一 `useGetLlmStatusQuery`（无参）缓存条目，失效一次两处同更。

### 二十二、对话走廊运行期重装（2026-10-16 续，用户激活后点「对话」Tab 撞到 500）

用户切「对话」Tab 报 `[500] llm chat service is not configured on this bot`。根因：**装配是一次性的启动动作**——引擎 01:11:55 启动时配对尚未激活（日志实锤 `llm chat assembly: llm clients: active pair: llm repository: record not found`），`setupLLMChat`/`setupLLMDecisionRunner` 失败后 chat/决策/批量三件停留 nil；用户在 08:24 运行期激活配对后没有任何重装路径，于是永久 fail-closed（首激活场景）——同理，运行期切换配对也会让走廊继续握着**旧配对**的客户端，与「对话/决策跟激活配对走」相悖。

- **引擎修复**：新文件 `engine/llm_runtime_reassembly.go`——三对 `currentXxx()/swapXxx()` getter/setter（`llmRuntimeMu sync.RWMutex` 保护：启动赋值与 RPC 读并发）+ `newBatchRunner` 抽取（engine.go 与重装共用）+ `reassembleLLMRuntime(ctx, reason)`（重装失败**清空**而非保留：激活后旧客户端属于退场配对，保留即静默违反切换语义）。`LLMSwitchActiveModelPair` handler 在 CAS 落定后同步重装（首激活与后续切换统一；幂等重放同样自愈）。全部直读点改 getter（requireChatService / runShadowDecision / LLMRunScreen / batch 两 handler / newTriggerConsumer）。
- **触发器循环自愈**：consumer 改**每 tick 重建** + 启动改为无条件（`engine.go` 移出 else）——装配晚于启动的场景也能被排空，切换后的 runner 无需重启即被消费。
- **顺带修存量编译破坏**：`llm_synthesis_test.go` 仍按旧 ``ScoreAnswer``（无 Probabilities/legend 对象）构造——上一批 JEV 官方对齐时只跑了 `llm/...` 没跑 engine 整套，测试包一直编译不过；已改新形状。
- **webUI 同类小修**：ChatPage 侧栏会话计数滞后（发完消息仍显示 0 条）——`onSend` finally 里刷新会话列表（与 §二十一 同族「写后就地刷新」）。
- **测试**：`TestReassembleLLMRuntimeBindsAndClears`（配对活跃→三件装配；清空 LLM 槽→重装失败关闭三件全 nil）+ `TestLLMSwitchActiveModelPairReassemblesTheRuntime`（handler 激活即装配、二次激活换绑 NotSame）。
- **电池**：engine 整套（-skip 网络类）**97.574s 全绿** · `golangci-lint run ./engine/...` 0 · **misc All passed**（首跑抓修 6 处 require 消息写 "should" 应为 "must"） · 前端 tsc 0/oxlint 0。
- **实弹（新二进制重启 9053，pid 2612）**：启动日志无装配告警；代理 `llm/conversations` **200**（`{"conversations":[]}`，原 500 消失）；浏览器端**真实两轮对话往返**（DeepSeek 实际回复）+ 侧栏计数 **2→4 条就地刷新**（无需整页刷新）。

### 二十三、对话配置能力对齐（2026-10-17，用户拍板 I1+I2+I3 全量 + 两端取消）

用户要求评估「对话 Tab 能否 1:1 覆盖合约覆盖/筛选与开仓决策/持仓决策的手动配置」，评估文档=`docs/llm-dev/16-对话配置能力对齐评估.md`；拍板：全量实施、覆盖开关按普通配置写、两端新增取消批量作业。**核心机制=惰性提案 + 确认卡片 + 执行同源**：模型永不写（红线不破），对话只产出结构化草稿，卡片确认后前端调用与手动页面完全相同的治理 RPC。

- **I1 框架**：`llm/candidate/proposal.go`（四动作枚举 + ActionProposal）；`llm/tools/proposal.go`（`proposeAction` schema、逐动作必填/互斥校验、去重与上限）；`engine/llm_tool_proposal.go`（执行器只做市场规模归一化 + 作用域核对 + 默认操作者，零写路径）；`ChatRunResult.Proposals` + `ProposalCards`（工具结果仍是证据包络，transcript 单一记录）；proto 新增 `LLMActionProposal` + `proposals=9`（buf 纯增量、连跑同哈希）；safety 白名单同步（提案=惰性数据）。前端 `chat.ts` 类型 + `ProposalCard`（市场/版本/操作者/市场号/作业号可就地编辑、确认/忽略、就地结果）。
- **I2 接线**：开仓批量（`OPEN_DECISION`）与持仓复核（`POSITION_REVIEW`，支持 allPositions 在确认时按最新持仓解析）→ `LLMStartBatchDecision`；策略版本从活跃审批下拉（与手动页一致）。
- **I3 覆盖**：proto + `LLMSetMarketTracked`（复用 `SetExchangePair` 配置写入 + `MARKET_TRACKED_CHANGED` 审计先于变更 + `RefreshMarkets`）；MarketsPage 行内启停开关（Pairs 缓存失效、防行点击导航）；mock 端点；对话 `SET_MARKET_TRACKED` 提案。**N10 未启用按需取数与 N13 启用优先仍待实现**（本轮只做配置面）。
- **I4 取消**：`BatchRunner.Cancel`（待跑行立即 CANCELLED、在途市场经 context 取消、审计先于生效、已取消幂等、已结束拒绝）+ `LLMCancelBatchDecision` RPC（proto 纯增量）+ 筛选/持仓两页「取消作业」按钮 + 对话 `CANCEL_BATCH_JOB` 提案。
- **测试**：tools 规格 9→10（新增 proposeAction 契约/拒绝表）；engine 提案执行器（作用域拒绝/归一化/默认 actor）、`ChatRunCarriesProposalCards`、批量取消（在途+待跑+二次取消拒绝）、取消 RPC handler；聊天工具数断言 9→10。
- **电池**：engine 整套 **96.312s 全绿**（首跑抓修 `TestChatRunSealsTheTurnAndReturnsTheReply` 的 9 工具断言）· `golangci-lint ./engine/... ./llm/...` 0 · `go test ./llm/...` 全绿 · **misc All passed** · proto 连跑同哈希 · 前端 tsc 0/oxlint 0/build ✓。
- **实弹（新二进制重启 9053，pid 21003）**：对话输入「启用 DOGE_USDT」→ 模型自动先 `listMarkets` 确认存在、再调 `proposeAction` → 回复明确「尚未执行」→ 卡片渲染（市场/开关/操作者可编辑）→ 点「确认执行」→ **DOGE 实际进入 enabledPairs（5→6）+ `MARKET_TRACKED_CHANGED` 审计**（actor/subject/detail 回读）→ 经同端点停用复原（6→5）；覆盖页手动开关 0G_USDT 启停往返（5→6→5，CSV 复原）。批量/复核的确认执行需活跃审批（当前系统 0 个，与手动页同前置），取消语义由单元测试覆盖。

### 二十四、对话筛选回复不可读的连环修复（2026-10-17，用户报障）

**报障**：「筛选近24h涨幅前3并给详细指标」返回一串 `<｜｜DSML｜｜ invoke ...>` 原始标记，完全不可读。逐层定位出**五层问题**，全部修复：

1. **DSML 文本工具调用**：该模型（deepseek-flash）把工具调用写成 DSML 文本（U+FF5C 全角竖线），循环只认结构化 `tool_calls`，标记被当成最终回答展示并落库。修复=`engine/llm_dsml.go`（解析 calls/invoke/parameter，`string=true` 纯文本、`false` JSON；未闭合标记裁剪；生成的调用仍走工具名白名单、schema、作用域与预算）+ 循环接入；测试覆盖实测原文、混合文本、未闭合、纯文本与端到端执行。
2. **气泡渲染**：前端 `stripToolMarkup` 防御性清理（含流式半截标记）、证据卡中文工具标签、空正文友好状态（`正在调用工具并整理结果…` / 标记已隐藏）；`toTurn` 对历史消息同样清理。
3. **生产装配断点**：`screenMarkets` 的 K 线缓存从未接线（`runner.Cache` 为 nil），对话/决策在真实引擎上必然报「needs … the candle cache」。修复=两处装配 `runner.Cache = m.Cache()`（并加断言钉住）；启动期市场列表在 rotation 尚未刷新（或 coverage 关闭）时用 `enabledPairs` 兜底。
4. **coverage 关闭**：用户配置 `llm.coverage.enabled=false` → 轮询/缓存无生产者、市场列表为空。已**启用**（先备份 `config.json.bak-2026-10-17-coverage`；只读轮询 5 个启用市场），并让冷启动首轮重载**不等到整点**（`warmOnFirstSweep` 仅生产装配开启，测试的整点契约不变）。
5. **回复预算与轮次**：`max_output_tokens` 未配置（0）→ 模型推理耗尽默认输出，`TRUNCATED`；README 对话默认每轮 4 工具 → 模型「筛选+逐币指标」触发 `TOOL_ROUND_LIMIT`。修复=对话输出预算默认 4096；对话工具上限放宽到 6 轮/每轮 6/总计 12（预算门与累计 token 上限不变）；提示词要求紧凑作答、勿写标记；screener 的环境性错误改为明确文案（不再当参数错误重试）；`include` 上限 8→12（全目录，"详细指标"的自然需求；结果仍受证据字节预算裁剪并如实标注）。

**实弹**：同一问题在真栈返回完整可读答复——覆盖范围说明（仅 5 个启用市场、24h 全线为负故为「跌幅最小的前3」）+ 三行指标表（最新价/24h/4h/48h/ATR%/振幅）+ 逐币补充指标（RSI/MACD/ATR/净流入/资金费率）+ 数据质量说明（XRP 缓存指标缺失如实标注、两类证据时点不同）+ **9 张中文标签证据卡**。电池：engine 整套 **109.736s 全绿** · lint 0 · 前端 tsc/oxlint 0 · misc 全过。**注记**：重启后 K 线缓存需冷启动重载约 5-10 分钟（按权重计划摊开，`llm/coverage` 可看进度）；N10（未启用市场按需取数）仍待实现。

### 二十五、全量扫描（N10/N13）+ 实时进度卡（2026-10-17 深夜，用户要求）

**需求**：筛选题要把全部 500+ 合约扫描一遍（允许几分钟，远低于币安 QPS），对话窗口要有实时扫描进度卡让等待有预期。

- **N10 全量宇宙**：筛选范围改为 `available ∪ enabled`（复用 `coverage.SelectMarkets` 的范围规则，实弹 589/793），`enabled` 优先排序（N13）；T0 用**一次批量 24h ticker**（`U24HTickerPriceChangeStats` 空符号，权重 40）覆盖全市场，缓存兜底；T1 对缓存未覆盖的市场**按需取数**（新增 `binance.GetHistoricCandlesUnrestricted`：跳过「必须启用」闸门、仍校验资产/周期，只读），并回灌 K 线缓存（容量 256→2048，覆盖 589×多周期）；取数采用 6 并发 + 120ms 发射节奏（约 8 req/s、权重约 960/min，远低于 2400/min）。
- **scope 同步扩展**：chat/决策工具的证据作用域同步扩到全宇宙（N10 设计要求）——此前排行榜上的未启用合约会被 `evidence outside the run scope` 拒绝；`getCandleWindow`/`getIndicators` 对未启用合约增加不受限只读兜底。实弹中前三名的 RSI/MACD/ATR/布林/资金流均成功读取。
- **实时进度卡**：proto `LLMChatStreamEvent.progress`（tool/phase/done/total/message）→ 引擎按调用挂 context 进度槽（并发回合互不串扰）→ screener 每完成一个按需取数上报 → SSE 逐帧透传 → ChatPage 渲染进度卡（进度条 + “已扫描 X/589（按需取数…）" + 工具/阶段 + 只读提示），回合结束即消失。
- **对话轮次**：全量筛选 + 3 个市场的深读已需要 7-9 个工具调用，对话上限调整为 6 轮 / 每轮 12 / 总计 24（预算门与累计 token 上限不变）。
- **实弹**：589 个合约扫描约 90 秒（进度 1/589→589）；结果 AGT_USDT +101.5%、Q_USDT +50.1%、EPIC_USDT +25.9%，含 RSI(1h)/MACD/ATR/布林/资金流与逐项解读；`getFunding` 对这三个未启用合约仍返回 “not a perpetual future …”，模型**如实标注资金费率不可用**、未做填充（跟进项：资金费率的未启用只读取数面）。
- **电池**：engine 整套 **99.707s 全绿** · `golangci-lint ./engine/... ./llm/...` 0 · **misc All passed** · proto 连跑同哈希 · 前端 tsc/oxlint 0。

### 二十六、筛选表去前缀 + Jev 开盘机会评估（2026-10-17 深夜，用户要求）

**需求**：① 对话筛选结果表格的市场列不显示 `Binance:usdtmarginedfutures:` 前缀；② 对筛选结果追问「以 4~8 小时为周期，此时是否有机会开仓」时，调用 Jev 对本次筛选过的所有合约判断是否有机会开仓，并对判为适合开仓的市场追加一次「做多还是做空」的 Jev 调用，最终只呈现「适合开仓 + 方向」的结论。

- **表格去前缀**：提示词新增「表格里只写合约名（如 AGT_USDT），不写交易所前缀」；证据卡展示层新增 `shortMarketID`（去掉三段式前缀，保留完整 id 在引用/sha 行）。实弹新回答的表格已只显示 `Q_USDT / AGT_USDT / EPIC_USDT`。
- **Jev 通用有界问题（后端）**：`llm/jev/judge.go` 新增 `Client.Judge` + `ValidateAnswers`——任意有界问题集的通用路径，沿用评审的模型漂移规则（精确请求精确回显、`jev-latest` 接受实际打点版本）与逐题校验（choice 分布覆盖/score 区间/noul 0..1）；问题类型仍只允许 choice/score/noul。
- **新对话工具 `assessOpeningOpportunities`**（只读）：
  - 输入：`markets`（1..20，必须在本会话作用域内）、`horizon`（≤40 字，如 4-8h）、可选 `note`；严格解码、去重、越界拒绝。
  - 事实：逐市场取批量 ticker + 缓存/按需 K 线，复用 screener 指标计算（lastprice/change24h/quoteVolume24h/change4h/change48h/atrpercent/range24h/macdstate/macdhist/macdcross）。
  - **两段 Jev**：第一问问 `open_opportunity`（choice：open / no_trade）；判 `open` 的市场再问 `open_direction`（choice：long / short）。每次调用都走预算预约/结算（ProviderKind=JEV、Purpose=CHAT）；单市场失败隔离；进度事件 `JEV done/total`。
  - 输出：`{horizon, rows:[{market, opportunity, direction?, confidence, directionConfidence?}]}`；未命中方向时 direction 为空（不猜测）。
- **装配**：chat/决策工具运行器接入活跃配对的 Jev 客户端、模型与修订版 ID + 共享预算适配器；工具目录 10→11，ChatPage 增加「Jev 开盘机会评估」标签与 `assessOpeningOpportunities` 提示词指引。
- **测试**：`ValidateAnswers` 通用校验（有效/漂移/缺答案/非法 choice）、工具解码边界、执行器两段调用（2 市场 → 3 次 Jev 调用、预算 3 次预约/结算、方向仅对 open、越界拒绝、进度）。
- **实弹**：追问「以 4~8 小时为周期，此时是否有机会开仓」→ 模型先刷新筛选（589 合约）再调用 `assessOpeningOpportunities`（进度卡 `Jev 判断 1/3`）→ Jev 对三个市场均判 `NO_TRADE`（置信度 Q 0.3 / AGT 0.86 / EPIC 0.44）→ 最终仅呈现机会表（方向列为 —，因无 open）与逐项说明；表格市场列无前缀。
- **电池**：engine 整套 **93.090s 全绿** · `golangci-lint ./engine/... ./llm/...` 0 · 前端 tsc/oxlint 0 · **misc All passed**。
