# Session-27 工作日志：生产装配·模型无关半边（metrics / 风控开关 / readiness 事实源）

**日期**：2026-10-11 · **性质**：`生产装配`的第一步（六类 fail-closed RPC 中三个不需要模型客户端的先接线）· **计分**：不改变百分比（M6 记分项不变；这是 M6-03/04/05 数据面的前置，也是 M2-13/chat 装配的共同底座）。

## 1. 交付物

- `engine/llm_runtime_facts.go`：**生产事实源 `LLMRuntimeFacts`**（实现 `iReadinessFacts`）——把 readiness 十个门的事实从真实来源读出：配对行+两侧最新探针（`GetActiveModelPair` + `ListProbeResults`）、停机（`ListActiveHalts`）、风控开关（注入的 `Switches` 取值器）、最近一次对账（**新增** `LatestReconciliation`）、当日基线（`GetDayStartInventory` + `UtcDayKey`）、覆盖市场数（注入的 `Rotation` 取值器 = `Rotator.Markets()`）、目标网络的活审批数（`ListActiveStrategyApprovals` 后按网络+有效期过滤）。纪律：`ErrNotFound`=合法空态→零值门 FAIL；其余读错误上抛（「读不到事实」≠「读到空事实」，M6-01 契约）。
- `database/repository/llm/reconcile.go`：**新增 `LatestReconciliation(ctx) (*Reconciliation, error)`**（按 started_at 取最新；无行=ErrNotFound；双方言）+ `TestHaltsAndReconcile` 补断言（最新行不被更旧行遮蔽、字段往返）。
- `engine/llm_risk_switches.go`：`RiskSwitchService.CurrentState()`（缺省已应用的状态拷贝，供 readiness 门消费）。
- `config/`：`LLMConfig.ProbeMaxAgeSeconds`（默认 **600**，`defaultLLMProbeMaxAgeSeconds`）——探针新鲜度的配置面；`config_example.json` 与 `testdata/configtest.json` 经 `make lint_configs` 过 jq。
- `engine/engine.go`：子系统启动后（`EnableLLMManager` 块）接线**模型无关三件套**——`llmMetrics = NewLLMMetrics()`、`llmRiskSwitches = &RiskSwitchService{Audit: LLMDecisionStore{}, Now: time.Now}`、`llmReadiness = &LLMReadinessService{Facts: &LLMRuntimeFacts{Config: …, Rotation: rotator.Markets, Switches: …, Now: …}}`。
- `engine/llm_runtime_facts_test.go`：两条测试——①全绿事实集（真配对+探针+对账+基线+审批+3 市场）→ **READY 10/10 PASS**；塞 FULL halt → NOT_READY 且仅 NO_FULL_HALT FAIL；②空机器人（全 nil 取值器）：10 门中 7 FAIL、SHADOW 豁免的 NETWORK/APPROVAL 与 MODE_PERMITTED PASS——**空事实≠错误**（不 panic、不假装通过）。

## 2. 行为变化（对运维可见）

- **readiness / metrics / risk-switch 三类 RPC 不再 fail-closed**：子系统一跑即活（此前 `err*Unavailable` 的错误路径仅在子系统未建时出现）。
- **chat / trigger / preview 维持 fail-closed**——它们要的是 `openai.Client`/`jev.Client`，属 client factory 的下一段。
- 新增配置键 `llm.probeMaxAgeSeconds`（缺省 600；两处 JSON 示例已同步）。

## 3. 验证

- `go test ./engine/ -run 'TestRuntimeFacts|TestRecover|TestActivateModelPair' -count 1` 全绿。
- `go test ./config/ ./database/repository/llm/ -count 1` 全绿（后者含新断言）。
- `go build ./...`、`go vet ./...`、`golangci-lint run`（engine+repo+config）**0 issues**；`make lint_configs` 双文件 OK。
- `make misc_checks`（CI 等价树）**All passed**——首次跑抓到并修掉 3 处断言措辞（`assert.True`→`Truef` 一处；assert 消息 `must`→`should` 三处：崩溃演练 + 两基线策略测试——**这两批测试属 session-26 交付、当时漏跑 misc**，本轮补网）。
- 引擎整套（-skip 两个既有网络 flaky）**108.4s 全绿**（含新事实源两测试）。

## 4. 接力提示（下一段 = client factory）

- 构造入口已备：`openai.NewClient(p *Profile, secretFn httpclient.SecretFn)` 与 `jev.NewClient(p *Profile, SecretFn)`；修订版参数在 `llmstore.ProfileRevision`（`AddProviderRevision` 的反面）——client factory 的活是「读激活配对两侧最新修订版 + 从 vault 解 API key + 拼 Profile + NewClient → `LLMManager.SetupChat(vaultDir, transport, runner)`」，再把同一 transport 接进分析流水线/Jev 管线。
- `LLMChatService.ActiveRevision` 已是 `ActiveLLMRevisionID`（读激活配对）——factory 完成前不要改动这条语义。
- 三个模型无关服务已共用 `bot.llm*` 字段；decision runtime 落地时**必须复用** `bot.llmMetrics`/`bot.llmRiskSwitches`（否则 gauge 与开关状态会分叉）。

## 6. client factory（生产装配主体）

- `engine/llm_client_factory.go`：**`BuildActiveClients(ctx, vaultDir)`**——激活配对两侧最新修订版 → `GetProfileRevision`（校验 Kind 与文档一致）→ `secrets.Open` + `ParseRef` → **每次调用才 `Reveal`** 的 `SecretFn` → `openai.NewClient` / `jev.NewClient`。映射：`ModelID→Model`、`Timeout→TimeoutSeconds`、`MaxOutputTokens→MaxTokens`、`ProxyAddress→ProxyURL`、`AllowInsecureLocalhost→{AllowInsecureLoopback, AllowPrivateNetwork}`（回环豁免是一个开关）。**Jev 别名模型在装配期被拒**（`IsPinnedModel` 语义由 NewClient 强制，测试钉死）。
- 配置：`llm.vault.directory`（默认 `<datadir>/llm-vault`）；设计稿的 `masterKeyFile/keyFileMode` 未实现（vault 键名由 `llm/secrets` 固定）——**登记的偏差**。
- 测试 ×6：映射正确；**端到端 reveal**（httptest 服务器收到 `Bearer sk-live-1` = 修订版→vault→每请求解密的完整链条）；无配对/vault 未初始化/别名 Jev/空目录各自 fail。

## 7. chat 走廊装配（生产装配下半段·chat 面）

- `engine/llm_chat_runtime.go`：`setupChatFromActivePair`（clients + 工具运行器 + `SetupChat`）与 `(bot *Engine) setupLLMChat`（取配置交易所实例作 `iToolDataSource`——`exchange.IBotExchange` 本已实现该接口——取 `Rotator.Markets()` 作 scope 市场集；scope 版本集 = **全部非归档策略的全部版本** `scopeStrategyVersions`）。
- `engine.Start`：模型无关三件套之后接 chat 走廊；**任何缺件（无配对/无 vault/未知交易所）只告警并保持 chat RPC fail-closed**，不中断启动（装配失败≠崩溃）。
- 测试 ×2：走廊装配成功（service/Cipher/runner 就绪、scope 含市场+账户+版本、未种版本不在 scope）；vault 未初始化 → 失败闭合。
- **行为变化**：六类 fail-closed RPC 已活五类（readiness/metrics/risk-switch/会话族）；仅 `trigger`/`preview`（决策运行器装配）仍 fail-closed。

## 9. 决策运行器装配（ShadowRunner，同日续段）

- `engine/llm_decision_runtime.go`：`setupDecisionRuntimeFromActivePair` 组装 `ShadowRunner{Scheduler, AnalysisPipeline, JevPipeline, DecisionRecorder}`——**与 chat 走廊共用同一批客户端与同一只读工具运行器**（模型层/scope/预算闸两侧必须一致，否则对话与自动决策对"模型能看到什么"会给出不同答案）。分析/Jev 管线统一用真实适配器（`LLMChatBudget`/`LLMChatRevisions`）；Jev 的 `PairEpoch` = `LLMApprovals.ActivePairEpoch`；记录器 = `LLMDecisionStore` 三接缝 + uuid NewID。
- 共享助手抽取：`buildScopeToolRunner`（chat/决策两处共用）；`(bot *Engine) setupLLMDecisionRunner` 从活的交易所实例取数据源。
- `engine.Start`：chat 走廊之后接决策运行器，缺件同样**只告警、fail-closed**；Engine 新增字段 `llmDecisionRunner *ShadowRunner`。
- 测试：装配断言（运输/评审/节流/记录器各就位、分析运输即构建出的 OpenAI 客户端）+ 真实调度去抖短路（窗口内第二次 `RunOnce` 返回 `Skipped`，不触网）。
- 顺带修复：`runtimeCtx` 上的过期 `//nolint:gosec`（nolintlint 报未使用——gosec 已不再标记该行，改为普通注释保留语义）。

**仍差最后一段**：`trigger`/`preview` 两个 RPC 的 **per-run 事实闭包**（`Assemble` 快照组装 / `BuildJev` 证据清单 / `RiskFacts` 风控事实）——三者在 `ShadowRunRequest` 上是调用方注入的闭包，生产实现（`llm_decision_facts.go`）与两个 handler 的换血是下一段。

**验收**：`go test ./engine/ -run 'TestSetupDecisionRuntime|TestSetupChatFromActivePair|TestBuildActiveClients|TestRuntimeFacts' -count 1` 全绿 · 全树 `golangci-lint run` **0** · 引擎整套见 §10。

## 11. trigger/preview 换血（per-run 事实闭包，同日续段——生产装配收口）

- `engine/llm_decision_facts.go`：**`buildShadowRunRequest`**——一次触发/预览的全部 per-run 事实从活状态组装：
  - 冻结版本 + **复核过的策略文本**（`ProposalJSON.objective`，无提案不许跑）+ 审批行（账户/窗口/信封哈希/世代）+ 配对两侧修订版（模型名）；
  - **门禁事实**：复用 `LLMRuntimeFacts`（配对就绪/探针/停机/对账）+ 版本状态 + 审批窗口 + 24h 预算窗口余量（`ListReservationsInWindow` vs 配置 cap）；
  - **`Assemble`**：`BuildMarketSnapshot` + 交易所数据源——**懒取**（只有真跑到组装才碰 venue，门禁拒绝不需要交易所实例）；
  - **`BuildJev`**：快照引用来自同一次 Assemble 的产物（共享 `shadowRunFacts`），证据从分析转录按 candidate 引用**逐条重建**（转录里存的就是序列化的 ToolEvidence 信封——"byte for byte"）；
  - **`RiskFacts` 维持 nil（明确标注）**：SHADOW 运行记录 awaiting-verdict、不评估硬风控——这是 seam 的文档化语义（影子不下单）；风控事实的生产装配是独立后续项。
- **handler 换血**：`LLMTriggerAnalysis`/`LLMPreviewDecision` 从 stub 换成 `runShadowDecision`（同一链；预览同样只产 HOLD/REVIEW 记录，永不成单）；未装配 runner 或任一事实读不到 → 报错，绝不假装跑过。
- 测试 ×4：全活状态组装（策略文本/窗口/世代/预算余量/探针逐项断言）+ 无交易所时 Assemble fail-closed + BuildJev 无快照报错 + 未冻结/未知版本/异场市场各自拒绝。

**到此 14 个 LLM RPC 全部为活**（readiness/metrics/risk-switch/会话族/trigger/preview + 既有读写族）。剩余装配级事项：SHADOW 硬风控事实（`RiskFacts`）、`M2-13` 两残余、`M0-09` 配置 v18。

**验收**：聚焦测试全绿 · 全树 `golangci-lint run` 0 · `go build`/`go vet` 0 · 引擎整套见 §10。

## 5. 未完成与下一步

- **决策运行器装配**（trigger/preview 的最后一块：AnalysisPipeline/JevPipeline/ShadowRunner 的构造与接线，复用本轮的 clients 与 scope 思路）；`M2-13` 两残余、`W5-01` 前端收尾、`M0-09` 配置 v18 迁移——按 PROGRESS「下一个模型建议」排序不变。

**验收命令**：`go test ./engine/ -run 'TestBuildActiveClients|TestSetupChatFromActivePair|TestRuntimeFacts' -count 1`（全绿）· `golangci-lint run ./engine/... ./config/...` 0 · `go build ./...` / `go vet ./...` 无输出 · markdownlint（llm-dev 50 文件）0。

## 10. 引擎整套（含决策运行器与 per-run 事实闭包）

两次跑分（均 `-skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'`）：

- **非网络用例两轮全绿**（含本轮全部新测试：模型无关接线、client factory、chat 走廊、ShadowRunner、per-run 事实——LLM/仓储/配置相关零失败）。
- **网络类用例集合在两次跑分间漂移**（Run A 15 个、Run B 8 个；Bitstamp/OKX/Slack 连接重置/EOF 为主——本机已知的 fake-IP DNS 抖动），**逐项复跑全部 PASS**（含 `TestOrderManager_Modify` 14.3s、`TestGetHistoricCandles` 6.7s 等）。这是环境类失败，与改动无因果；本地验收按 README §4.3 纪律带 skip 记录。

## 8. 引擎整套

`go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'` → **110.2s 全绿**（含本轮 8 个新测试：事实源 2 + client factory 6 + chat 走廊 2 中的新断言——按文件计 10 个用例）。
