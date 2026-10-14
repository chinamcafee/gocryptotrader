# M6-01 验收演示记录（A01–A14）

**依据**：`docs/llm-dev/01` §10（14 项验收总纲）+ `docs/llm-dev/11` §8（M6 是「能不能上主网」的唯一判据；本方案应有等价的准入检查命令）
**记录日期**：2026-10-11（session-25）
**配套工具**：`gctcli llm readiness`（与本文档同日落地，见 §2）

## 1. 状态图例与总则

| 状态 | 含义 |
| --- | --- |
| ✅ 离线已验证 | 行为由离线测试钉住（测试名列在证据栏），无需外部资源即可复跑 |
| 🟡 部分离线 | 核心逻辑离线已验证；端到端演示仍缺运行面（缺什么逐项写明） |
| 🔴 待运行面 | 必须真实运行栈（bot + 网关 + 模型端点 / 币安测试网）才能演示；步骤已写好，装配到位后逐项补记 |

**总则（与 01 §10 的「容易被误判为通过」牌对齐）**：单元测试通过 ≠ 真实模型供应商调用通过；测试网成交 ≠ 主网成交。本记录**不把单测通过假装成端到端通过**——凡依赖真实运行面的项一律标 🟡/🔴 并写明所缺前提，而不是记一个含糊的「通过」。

**当前运行面缺口的清单（已登记的生产待办，非本记录新发现）**：

1. **生产装配（client factory）**：按激活修订版构造 `openai.Client` → `LLMManager.SetupChat` → 各服务接线——未落地前 chat/trigger/preview/metrics/risk-switch/readiness 六类 RPC 全部 fail-closed。
2. **币安测试网凭据**：属用户侧资源（密钥存入 vault 的操作见 `docs/localDeploy/`）。
3. 撤单适配器（按 `gct-llm-` 前缀过滤 `CancelBotOrders`）与账户事件生产者：属生产装配一部分。

## 2. 配套准入工具：`gctcli llm readiness`（11 §8 要求的等价物）

11 §8 原文要求：「本方案应该有等价的准入检查命令：`gctcli llm readiness --network binance-testnet`，退出码 0 = READY，2 = NOT_READY，并列出每一项未通过的原因」。本条目落地为：

- **引擎侧纯函数评估器** `engine/llm_readiness.go`（`EvaluateReadiness`）+ 事实源接缝 `LLMReadinessService`——与 pre-gate（M4-07）同构：零值 fail-closed，没有「没数据就算过」的路径。
- **RPC** `LLMGetReadiness`（`GET /v1/llm/readiness`，proto 与 handlers 原子落地，buf 双跑同哈希 `3155e986…`）+ **CLI** `gctcli llm readiness --network binance-testnet [--mode TESTNET] [--json]`（READY → 退出码 0；NOT_READY → 退出码 2；运行面未装配 → 报错退出）。
- **fail-closed**：`llmReadiness` 未装配时 RPC 答 `llm readiness is not configured on this bot`——空报告与坏子系统不许长得一样（与 chat/trigger/metrics 同一约定）。

**十条门**（报告固定顺序，就是 CLI 的打印顺序）：

| 门 | 拦什么 | fail-closed 规则 |
| --- | --- | --- |
| `INPUT_COMPLETE` | 请求自身 | network/mode/时钟缺任一 → 只报这一行 |
| `MODE_PERMITTED` | 模式白名单 | READ_ONLY 及未知模式不得自动决策 |
| `PAIR_READY` | 激活配对 | 非 READY（含 DEGRADED）一律拒（Q3：无降级路径） |
| `PROBE_FRESH` | 双方最新探针 | 未过/无时间戳/未来时间戳/未配 TTL/超龄 → 拒 |
| `NETWORK_CONSISTENT` | 错网下单 | TESTNET/LIVE 必须 configure 网络 == 请求网络；SHADOW/PAPER 豁免（M5-13 同一语义） |
| `NO_FULL_HALT` | 带病上岗 | 存在 FULL halt → 拒（SOFT/MARKET 计数放行） |
| `RISK_SWITCHES_INTACT` | 底线 4/5 | 三个锁定开关任一为关（含状态未供给）→ 拒 |
| `ACCOUNT_RECONCILED` | 账目起点 | 未对账或 5min 有效期已过 → 拒 |
| `DAY_START_SEALED` | 日损盲区 | 今日 UTC 基线未封 → 拒 |
| `COVERAGE_PRESENT` | 轮询范围 | 范围内 0 市场 → 拒 |
| `APPROVAL_LIVE` | 授权闭环 | TESTNET/LIVE 需 ≥1 活审批（A04）；其余模式不要求 |

测试：`engine/llm_readiness_test.go` 9 个测试（全绿门 + 输入不完整 + 逐门单点失败 + 逐门详情文案 + 服务层事实源缺省/错误/未装配）。

## 3. A01–A14 逐项记录

| # | 验收内容 | 离线证据 | 运行演示步骤（装配后补记） | 状态 |
| --- | --- | --- | --- | --- |
| A01 | 运行时添加一个新永续合约，5 秒内能查到 ticker 和 K 线 | 范围选择 `llm/coverage`（含 588 筛选）、轮询执行器对新增市场免重建（session-21 断言）、K 线共享取数四模式（`engine/historic_candles_test.go`）| 在 config 加入新合约 → 重启（或运行时启用）→ `gctcli getticker` + 交易台 `/terminal` 查 K 线，掐表 ≤5s | 🔴 待运行面 |
| A02 | 对话问 MACD，数值与 `gctcli technicalanalysis macd` 一致 | **跨层等价已钉死**：`TestIndicatorAssemblyAgreesWithThePlatformsOwnIndicators`（喂仓库真实 TA 输出逐点相等）+ `TestLLMToolsIndicators`（执行器路径同判据）| 对话页/`llmchat` 问「BTC MACD」→ 取回复数值 → 同参数 `gctcli technicalanalysis macd` 对账 | 🟡 数值等价离线 ✅；对话链路待装配 |
| A03 | 自然语言策略提取成结构化草案；缺硬边界提澄清问题不自行填值 | `TestPolicyExtractionAsksInsteadOfChoosing` · `TestPolicyExtractionMissingBoundaryNeedsClarification` · `TestPolicyExtractionRejectsMarketsOutsideSelection` | `gctcli llm strategies extract --text "…"` 两个样例：完备→READY_FOR_REVIEW；缺限额→NEEDS_CLARIFICATION | ✅ 离线已验证 |
| A04 | 冻结不可变；审批绑 `pair_epoch` + `risk_envelope_hash` | `TestStrategyFreezeIsDeterministic` · `TestApprovalBindsTheFrozenPolicy` · `TestApprovalBindingGates`；真库触发器：二次冻结被拒、审批改列被拒（`database/repository/llm` e2e）| `llm strategies freeze` → 尝试 `policy_json` 再冻结被拒；`approve` 后改 epoch 再批被拒 | ✅ 离线已验证 |
| A05 | 切换 LLM/Jev 端点后，旧配对的进行中决策全部失效（STALE_PAIR） | `TestPairSwitchCascadeRevokesAndPauses` · `TestJevReviewDetectsAPairChangeWhileJudging`；**实现口径**：不用 STALE_PAIR 表，进行中运行在结束前重查 `pair_epoch` 自废（M4-14 注释 + 测试钉住）| `llm switchpair` → 观察旧 run 记录 `STALE_PAIR` 拒绝码、审批已撤、策略已暂停 | ✅ 离线已验证（语义等价实现）|
| A06 | 工具调用 ≤8、轮次 ≤4、token ≤ 预算；超限即 HOLD | `TestToolLoopTokenBudgetAndRoundLimit` · `TestToolLoopToolCallGuards` · `TestReserveBudgetCapEnforcement` | 配置小预算 → 触发分析 → 断言 HOLD + 零超发 | ✅ 离线已验证 |
| A07 | Jev 收到的 `state.evidence` 与 LLM 工具响应逐字节一致（哈希校验） | llm/jev：`TestReviewRequestCarriesEvidenceByteForByte` · `TestReviewEvidenceAcceptedWithMatchingManifest` · `TestReviewEvidenceRejectsChangedDataAgainstManifest`；engine：`TestJevReview*` 6 个（真请求上反解比对）| 走一次完整决策 → `llm decisions lineage <id>` 核对 jev_json 清单哈希 | ✅ 离线已验证 |
| A08 | Jev 响应校验：概率分布求和、加权分一致性、模型版本漂移 | llm/jev：`TestValidateResponseScoreRules` · `TestValidateResponseRejectsBadReply` · `TestSystemOneRejectsModelMismatch` · `TestProbeReportsFailuresWithoutError` | 探针 ③ 层（SCHEMA）+ 一次真评估的验收 | ✅ 离线已验证 |
| A09 | 数据过期（>10s）强制 HOLD 且不消耗模型预算 | `TestFreshnessGateIsAppliedBeforeReturning` · `TestPreGatesFailOneAtATime` · `TestJevReviewRefusesBadEvidenceBeforeBudget` | 冻结订阅 >10s 快照 → 触发分析 → HOLD + 预算零占用 | ✅ 离线已验证 |
| A10 | PAPER 走 `SubmitFakeOrder`，本地仓位更新，交易所零请求 | `TestDispatchPaperGoesToTheFakePathWithLimitsChecked` · `TestDispatchShadowAndReadOnlyNeverSubmit` | readiness(PAPER) → 触发 → `getorders` 只见假单；交易所请求日志核对 | ✅ 离线已验证 |
| A11 | TESTNET 开仓 → 查仓 → 平仓全链路一致 | 映射/分流/状态机/保护性退出/对账全链离线测试见 M5 组（PROGRESS §3）| 见 §4 步骤 11 | 🔴 待运行面（需测试网凭据）|
| A12 | 风控拒绝返回具体码 + 实际值 vs 允许值，并推送通知 | `TestDecisionRejectCarriesActualVsAllowed` · `TestEveryEventTypeHasAPath` · webhook 载荷反解 4 测试 | 构造越限数量 → 拒绝卡显示 actual/allowed；通知落 Telegram/webhook | ✅ 离线已验证（推送通道配置后补端到端）|
| A13 | 审计链：观察→候选→Jev→风控→订单→成交→仓位 | `TestDecisionRecorderWritesTheHoldChain` 等 8 测试；`LLMGetDecisionLineage` handler 从同一批存储组装 | `gctcli llm decisions lineage <run-id>` 对照 §4 步骤 13 的检查清单 | 🟡 链路写入离线 ✅；含真实成交的完整链待 A11 |
| A14 | 紧急停机后：新开仓被阻、策略暂停、机器人挂单撤销 | `TestHaltSetLevels` · `TestHaltSequenceAbortsOnFailure` · `TestHaltClearRequiresAuthenticationAndNeverAutoResumes` | `gctcli llm halt set --level FULL --reason 演练` → `getorders` 核对 `gct-llm-` 单已撤；clear 需重新认证 | 🟡 序列离线 ✅；撤单适配器属生产装配 |

**A15–A17 归属**（01 §10 的扩展三项）：A15（LIVE 小额首单）→ M8（三重解锁已在 M5-12 落地并通过测试）；A16（全量覆盖 ≤10min、权重 ≤60%）→ M6-05 压力测试（588 实测口径见 `llm/coverage/scope.go`）；A17（REVIEW 过期）→ **已落地**（M5-18：12h 自动 EXPIRED + 推送 + 不执行、宽限窗幂等，`TestReviewExpirySweep`）。

## 4. 运行面到位后的补记步骤（照此逐项补记）

1. 装配：按激活修订版构造传输 → `LLMManager` 启动 → `gctcli llm readiness --network binance-testnet` 应报 0。
2. A01：config 加新合约 → 重启 → `getticker` + `/terminal` 掐表。
3. A02：对话问「BTC 的 MACD 是多少（1h，默认参数）」→ 与 `gctcli technicalanalysis macd` 对账。
4. A11：`approve --network binance-testnet --execution-mode TESTNET` → 触发分析 → `llm decisions list` 查意图 → `getorders` / `getpositions` 对齐币安测试网 → 平仓 → `llm reconcile`（或等对账轮）→ 链上核对。
5. A13：`llm decisions lineage <id>` 检查八段齐全（snapshot/transcript/candidate/jev/risk/intent/order/fill+position）。
6. A14：`llm halt set FULL` → `getorders` 见 `gct-llm-` 挂单消失、策略列表全 PAUSED → `llm halt clear`（带密码与原因，不自动恢复）。
7. 每次补记在本文档追加「复核记录」小节（日期 + 命令 + 实际输出摘要 + 结论）。

## 5. 复跑与挂钩

- **M6-02（20 项失效矩阵演练）** 直接复用本工具的门清单作为「响应符合预期」的词汇表：每个失效场景对应到某门 FAIL 或某个拒绝码。
- **M6-06（kill -9 崩溃演练）** 复用 M5-15 `CrashRecovery.Recover`；演练后跑 `readiness` 与 `llm reconcile` 验证收敛。
- 本记录在 M6 期间**随每次装配进展重跑**；未补记完 A01/A11 前，M6 整体不得进入 M8（11 §8：M6 是唯一判据）。
