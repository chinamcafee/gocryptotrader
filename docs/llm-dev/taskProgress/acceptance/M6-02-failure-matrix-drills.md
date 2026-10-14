# M6-02 失效矩阵演练记录（10 §7 全 21 行）

**依据**：`docs/llm-dev/10` §7（失效矩阵）+ `docs/llm-dev/11` §8（M6-02：每项人为触发，验证响应符合预期）
**记录日期**：2026-10-11（session-25，与 M6-01 同会话）
**矩阵现行 21 行**：原 20 项 + 决策阶段补入的「REVIEW 过期」行（10 §7 中编号 21 排在 14 之后）

## 1. 演练方式与图例

**演练方式**：离线可触发的行，用既有测试作为「人为触发」——可复跑、可变异（改坏一处即应转红）；必须真实运行栈的行，写成演练步骤待装配后补记。每行的判据逐字对照 10 §7 的「检测 / 自动响应 / 人工介入」三列。

| 状态 | 含义 |
| --- | --- |
| ✅ | 触发与响应均已离线复验（证据=可复跑测试） |
| 🟡 | 主响应离线 ✅；升级/巡检环节依赖运行面装配（逐行写明） |
| 🔴 | 需真实运行栈（网络/交易所依赖）后补演练 |
| 📋 | 演练内容=人工流程（无代码响应面） |

## 2. 演练结果

| # | 失效 | 人为触发 | 预期响应（10 §7） | 证据 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 1 | LLM 端点不可用 | 换入探针失败的假传输（离线）/ 断开端点（运行） | 停止新开仓；交易所侧保护性止损仍运行（结构性：止损是 venue 侧 STOP_MARKET）；`pair_readiness→DEGRADED`；推送 `llm_probe_failed` | `TestProbeReportsFailuresWithoutError`（探针失败不吞）· `TestActivateModelPair`（DEGRADED 分支）· `TestPreGatesFailOneAtATime`（PROBE_FAILED/STALE 拒决策）· `TestEveryEventTypeHasAPath`（ProbeFailed 可达） | ✅ 离线；运行演练待装配 |
| 2 | Jev 端点不可用 | judge 返回错误 | Q3=A 无降级路径：一切自动决策停止、收敛 `HOLD / JEV_UNAVAILABLE`；对话仍可用但标注 | `TestSynthesize`（JEV_UNAVAILABLE 分类）· `TestJevReviewReleasesWhenTheJudgeFails` · `TestJevReviewMisuse` | ✅ |
| 3 | 模型配对被切换 | 跑一次切换级联 | 进行中决策 `STALE_PAIR`；运行中策略 → PAUSED；有效审批 → 撤销；推送 `llm_pair_changed` | `TestPairSwitchCascadeRevokesAndPauses` · `TestJevReviewDetectsAPairChangeWhileJudging`（STALE_PAIR）· `TestApprovalBindingGates` · `TestEveryEventTypeHasAPath`（PairChanged） | ✅ |
| 4 | 模型版本漂移 | 让端点返回不同 model id | `JEV_MODEL_DRIFT` → HOLD；计数告警；探针记录实际 model 供漂移检测 | `TestSystemOneRejectsModelMismatch` · `TestSynthesize`（JEV_MODEL_DRIFT 行）· 探针落库含 `response_model`（05 §2.4 表） | ✅ |
| 5 | 数据过期 | 把快照年龄推到阈值外 | `HOLD / MARKET_SNAPSHOT_STALE`；不消耗 Jev 预算；**持续 >300s → SOFT halt** | `TestFreshnessGateIsAppliedBeforeReturning` · `TestPreGatesFailOneAtATime` · `TestJevReviewRefusesBadEvidenceBeforeBudget`；`DataStale()` 构建器已备 | 🟡 **发现②**：per-decision HOLD 立即生效 ✅；「持续 >300s 升级 SOFT halt」的看门狗未实现（登记，依赖调度器接线） |
| 6 | K 线有缺口 | 喂含缺口/零值 K 线窗口 | `HOLD / CANDLE_DATA_GAP` | llm/snapshot 质量套件（`go test ./llm/snapshot/ -count 1` 全过；缺口/零值/指标同标/GAP 不覆盖 STALE/PARTIAL 五组）· 风控 `CANDLE_DATA_GAP` 检查（`-run 'TestRisk'`） | ✅ |
| 7 | 币安限频 -1003 | 运行面制造 -1003（真撞限频） | 内置限频器退避（`-ratelimiter` 默认开）；快照标 `GAP` → HOLD | 平台 Sonic 限频器为既有行为；快照 GAP 路径同 #6 | 🔴 待运行面（真限频不可离线制造） |
| 8 | 时钟漂移 -1021 | 改系统钟 / 构造 -1021（运行面） | 订单 → UNKNOWN/REJECTED；`accountReconciled=false`；FULL halt；推送 | `TestDispatchFailureIsUnknownNeverRejected`（失败=UNKNOWN）· 风控 `RiskClockDrift` 检查（`-run 'TestRisk'`）· 对账组 `-run 'TestDiffPositions\|TestReconcile'` | 🟡 错误映射与对账位离线 ✅；-1021 特判与自动 FULL halt 升级属运行巡检（与 #5 同类升级器） |
| 9 | 订单结果不明 | 回执超时 / 缺详情 | 状态 → UNKNOWN；**绝不重试**；账户需对账；高优先级推送 `llm_order_unknown` | `TestDispatchFailureIsUnknownNeverRejected` · `TestUnknownIsNeverResubmitted`（迁移表无该边）· `OrderUnknown()`（high） | ✅ |
| 10 | 数据库不可用 | 让存储写入失败 | fail-closed：停止一切新决策（无审计的决策不可接受）；FULL halt | `TestDecisionRecorderStopsWhenAWriteFails`（任一步失败中止整链）· `TestStrategyFreezeRefusesMisuseAndReportsTransitionLoss`（审计行丢失报错） | 🟡 **口径记录④**：DB 不可写时 halt 自身也写不下去——有效响应是「每一步写失败即中止」的系统性 fail-closed；「持久化 FULL halt」预设 DB 可写，升级属运行巡检 |
| 11 | 对账差异 | 喂差异持仓 | `accountReconciled=false`；SOFT halt；记 delta；推送 `llm_reconcile_mismatch` | `-run 'TestDiffPositions\|TestReconcile'`（4 全过：数值等价/排序/清账+有效期/差异→SOFT halt+审计）· `ReconcileMismatch()` 构建器 | ✅ |
| 12 | 保护性止损提交失败 | 让止损下单失败 | fail-safe：立即市价平掉刚开的仓；`protective_exit_failures_total` 告警；最高优先级推送 | `-run 'TestProtectiveStopPrice\|TestBuildProtectiveExit\|TestEnforce'`（5 全过：fail-safe 两连）· `ProtectiveExitFailed()`（critical） | ✅ |
| 13 | 预算耗尽 | cap 设 "0" / 占满 24h 窗口 | 停止自动分析；对话提示预算；推送 `llm_budget_exhausted` | `-run 'TestReserveBudget'`（6 全过，含 "0"=禁止键）· `TestPreGatesFailOneAtATime`（BUDGET_EXHAUSTED）· `BudgetExhausted()` 构建器 | ✅ |
| 14 | 审批即将过期 | 造一张 20 分钟后过期的活审批 | 提前 30 分钟推送 `llm_strategy_expiring`；过期后 → EXPIRED、策略停 | **本行缺口在演练中修复**：`engine/llm_approval_expiry.go`（`ApprovalExpirySweeper`：`(now, now+30min]` 窗口含边界、分钟向上取整、每张恰一次提醒、出窗剪除、gauge `approvals_expiring_soon`）+ `TestApprovalExpirySweep*` 3 全过；过期后状态转移走 M4-05/M4-14 既有 | ✅ 离线（滚动调用属装配） |
| 15 | 进程崩溃在 SUBMITTING | 直接构造 SUBMITTING 行（离线）；真 kill -9（M6-06） | 重启扫出 → UNKNOWN；账户需对账；FULL halt 直到对账 | `-run 'TestRecover'`（3 全过：认领清 UNKNOWN + 干净重启也留痕 + 失败中止） | ✅ 离线；真崩溃演练归 M6-06 |
| 16 | 双向持仓被开启 | `GetCollateralMode` 报 HEDGE | `RISK_COLLATERAL_MODE_HEDGE` 拒绝一切开仓；推送 | `-run 'TestRisk'`（授权拒绝组含 HEDGE 码） | ✅ |
| 17 | 杠杆被外部改动 | `GetLeverage` 超审批上限 | `RISK_LEVERAGE_EXCEEDED` 拒绝 | `-run 'TestRisk'` 同组 | ✅ |
| 18 | 交易对被外部下架 | 运行面 `autoPairUpdates` 刷新（需真市场） | 相关市场授权失效；策略暂停；推送 | ✅ 链路机器验证（session-51：`TestMarketDelistWatch`——撤 grant / 级联审批 / 暂停版本 / 审计 / 推送五步 + 只对消失市场触发 + 恰一次）；真刷新场景待运行面 | 🟡 **生产已接线（session-51）**：`MarketDelistWatcher` 挂进旋转循环；只剩真 `autoPairUpdates` 刷新场景补演练 |
| 19 | Vault 损坏/丢失 | 删除/损坏 vault 后建客户端 | 无法调模型 → 全部 HOLD；推送 | `TestSystemOnePropagatesSecretFailure`（jev）· `TestProbeReportsFailuresWithoutError` → 探针失败 → DEGRADED → 门禁 HOLD（同 #1） | ✅ 离线；运行演练待装配 |
| 20 | gRPC 凭据泄露 | 按流程执行轮换（无自动检测） | 立即改密码、轮换币安 Key、检查审计日志 | 演练步骤=轮换流程 | 📋 归 `M6-07` 密钥轮换演练 |
| 21 | REVIEW 长期无人处理 | 造 13h 前的 REVIEW | 12h 超时自动 → EXPIRED（不执行）并推送 `llm_review_expired`；宽限窗幂等 | `-run 'TestReviewExpiry'`（2 全过） | ✅ |

## 3. 演练发现（结论性登记）

| # | 发现 | 处置 |
| --- | --- | --- |
| ① | 行 14 的提前 30 分钟提醒此前只有事件构建器（`StrategyExpiring`）与 gauge 常量，无滚动扫描器 | **已修复**（同日）：`ApprovalExpirySweeper` + 3 测试；本行由「缺口」转 ✅ |
| ② | 行 5 的「持续 >300s 数据过期 → SOFT halt」升级器未实现（per-decision HOLD 已生效） | **登记**：依赖调度器/看门狗接线（生产装配一批）；装配后补演练并回填本表 |
| ③ | 行 18 的市场下架事件生产者未接线（授权失效/策略暂停/推送三件套无触发源） | **已接线（session-51）**：`MarketDelistWatcher` 进旋转循环，完整响应链机器验证（`TestMarketDelistWatch`）；真刷新场景待运行面补演练 |
| ④ | 行 10 的口径：DB 不可写时无法持久化 halt——有效响应是写路径全链 fail-closed | **口径记录**（本表 #10 注）：升级巡检属运行面；不误判为缺口 |

## 4. 计数与结论

- **21 行演练结果**：✅ 15 · 🟡 4（#5/#8/#10 升级器属装配 + **#18 生产者已接线（session-51）**，主响应机器验证、真刷新场景待运行面）· 🔴 1（#7 真限频）· 📋 1（#20 人工流程）
- **结论**：矩阵中所有**决策面与执行面**的自动响应都在离线夹具下逐行复验通过；未通过的三类全部是「运行面巡检/事件源」环节，与已登记的生产装配缺口同源，无新增结构性风险。
- **补记挂钩**：生产装配完成后补 #1/#7/#8/#19 运行演练；M6-06 补 #15 真 kill -9；M6-07 补 #20 轮换演练；#5 随对应接线回填；#18 生产接线已完成（session-51），剩真刷新场景回填。
