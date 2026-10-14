# session-53 · 02/03 拍板落地：闭包装配 + PAPER/LIVE 桥 + Jev 输入清单生产化

日期：2026-10-13 接续：session-52（尾十三/尾十四） 范围：用户拍板 decisions/02=B、03=A、01=1 后的第一步全量落地

## 用户拍板（2026-10-13）

- **02 = B**：开仓 LIMIT（`suggestedEntry` 经 §5.4 tick 取整 + 盘口区间校验）、主动平仓 MARKET。
- **03 = A**：配置项 `llm.risk.expectedMarginType`，默认 `CROSS`（+ v19 迁移 + CheckLLMConfig 填充 + 样例）。
- **01 = 方案 1**（测试网全套凭据）——**凭据尚未实际交付**：M6-03/04/05 + M1-15 维持 `[!]`，待凭据经 vault 流程录入后解锁。

## 本段增量（一~五）

### 一、配置面（03 落地）

1. `config/llm_config_types.go`：`LLMConfig.Risk LLMRiskConfig{ExpectedMarginType}`（operator 词汇）。
2. `config/llm_config.go`：`defaultLLMExpectedMarginType = "CROSS"`；`CheckLLMConfig` 容忍式规范化（大写/trim；经 `margin.StringToMarginType` 校验，非法值回退默认——与 RunMode 同族风格）。
3. `config/versions/v19/`（新）：迁移=llm 为对象且 `llm.risk` 缺席时插入 `{"expectedMarginType":"CROSS"}`（单侧、守卫式；false/null/非对象一概不碰）；退化=删除 `llm.risk`。register 注册 19；`versions_test` 计数 19→20。
4. `config_example.json` + `testdata/configtest.json`：样例加 `llm.risk`；fixture 版本 18→19；`config_test.go` 版本前沿断言 18→19（6 处）。
5. **词汇翻译单点**：场地读数为 `margin.Type.String()`（binance 跨保证模式时 `margin.Multi` → `"multi"`）——闭包经 `venueMarginType()` 把 operator 的 CROSS 译为 `"multi"` 再进映射；检查两侧裸比较自此同拼写（否则「CROSS ≠ multi」会让每个开仓被 RISK_MARGIN_TYPE_UNEXPECTED 拒——与 QuoteVolumeRank 同族的装配杀手，已避）。

### 二、RiskFacts 合成器（闭包装配全材料）

`engine/llm_shadow_risk_material.go`（新）：

- `(*Engine).buildShadowRiskMaterial(ctx, in)`：七组映射（A 授权/B 数据/C 账户/E 频率/合约/D 市场/G 系统）+ 两组装件一次装齐；读不到即拒（fail-closed，run 记失败）。
- **订单事实（02=B + §5.4）**：开仓 LIMIT 价=`limitPriceFor`（从 `IntentPlan.limitPrice` 抽出本体——风险阶段与意图落库同一实现）；NotionalUSD=min(候选上界, 审批上限)（数量链可产出的最坏情形上界）；`HasProtectiveExit` = 计划承诺（switch 或 envelope 要求）——止损可导性归数量链（止损距离）自己的拒绝；平仓 MARKET（不传价），Quantity=该市场现存仓位数量、NotionalUSD=max(entry,mark)×qty（保守拼写）。
- **F 组归一**：账户事实仓位 `MarketID` 逐行过 `runMarketID`（BASE_QUOTE → 运行三段式）——否则反向持仓/并发计数永远看不见仓位（第六处同族缝，装配前修）。
- **E 组**：窗口上限 0 照传、连亏 0（值表②）、冷却 elapsed=true（时长未配置，0=无闸翻译）；**C 组日损**：`NetExternalFlowsUSD`（新方法，DailyLoss 重构后共用净额拼写）+ 基线现读；日损不可建立时跳过 sizing，让引擎自己的 `RISK_DAILY_LOSS_UNKNOWN` 记录拒绝。
- **G 组**：halts 现读、时钟漂移 venue 现读、模式 fresh 读（fail-closed）、预算=pre-gate 同算式、DB=Ping、`RateLimitDegraded` 显式 false（无持久读面——**登记未钉**，真读面=共享包新增状态）、管理器非 nil。
- 纯助手（可测）：`shadowOrderFacts`/`shadowOrderTypeFor`/`shadowSizingSide`/`venueMarginType`/`snapshotBestBidAsk`/`depthFromSnapshotBook`（快照盘口 → `orderbook.Depth`，接 `sizingWalkFromDepth`）。
- **`ConflictsWithResting`/`RestingOrder` 定源裁定**：生产无路径可达 RESTING（M5-05 生命周期喂送未接线）——`ConflictsWithResting=false` 为当下事实（记录：生命周期落地时重钉读面）；仓位事实 `RestingOrder` 恒 false（负向过滤字段，无正向消费）。
- `ShadowRunRequest.RiskFacts`（`llm_decision_facts.go:213` 位）自 nil 换成生产闭包：**SHADOW 决策此后出真实风控裁决与 sizing 记录**（awaiting-verdict 不再是常态）。

### 三、PAPER/LIVE 决策→订单桥

- `engine/llm_execution_bridge.go`（新）：`executeApprovedRun`——模式白名单（PAPER/TESTNET/LIVE 才提交）+ 全链 ALLOW（风控+重算）+ 开仓才出单（**REDUCE 执行待退出数量政策 07 §7，登记**）；流程=BuildIntentPlan → IntentRecorder.Record → OrderDispatcher.Dispatch。
- 桥缝挂在 `ShadowRunRequest.Execute`（**runner 不碰它**——SHADOW 结构性不可下单的承诺保留；由触发器消费者在运行完成后调用）。`buildShadowRunRequest` 里装配 Execute 闭包（价格/合约材料从同一次快照重取）。
- `engine/engine.go`：`llmDayStart`（M5-09 服务首次进生产）、`llmIntents`/`llmDispatcher`（OrderManager 在则装配）。
- 消费者：`ConsumeOnce` 运行完成后调 `req.Execute`，新增 `Executed` 计数；桥失败=单项失败（不静默）。

### 四、Jev 输入清单生产化（装配期发现的被掩盖缺口）

**发现**：`RiskJevManifestNotBound`（非开关、非 newRiskOnly）会拒**每一次**评估，而生产从未构建输入清单（`BuildInputManifest` 零调用方、`JevReviewOutcome` 无 Manifest 字段、`JevManifestState` 从未落库）——闭包一旦装配、若照实传 `ManifestBound=false`，**每个 run 都被拒**（生产杀手级，与 QuoteVolumeRank 同族）。修复：

1. `llm/jev/client.go`：`SystemOneWithManifest`（SystemOne 委派之）——响应校验通过后以**精确请求/响应字节** + 候选 + 策略文本 + 证据引用建清单；ref 惯例 `tool:<name>:<callId>` 分解，非惯例 ref 取其自身为标识（唯一性由证据契约保证）。
2. `engine/llm_jev.go`：`iJevJudgeManifest` 可选缝（生产 client 满足）；`JevReviewOutcome.Manifest` 字段；管道回填；`JevManifestStateBound = "MANIFEST_BOUND"`。
3. `ShadowRunner.record`：清单在则 `JevManifestState` 落库。

### 五、测试与电池

- 新测试：`llm_shadow_risk_material_test.go`（6：开仓 LIMIT 逐例/平仓 MARKET+tick 取整+盘口界+最小名义/保证金翻译/Depth 走价/走价方向）；`llm_execution_bridge_test.go`（5：PAPER 出单 LIMIT 价逐字/SHADOW 永不出单/全链 ALLOW 才出单/平仓跳过/模式白名单）；`llm_trigger_consume_test.go` +1（消费者驱动桥、桥失败计 Failed）；v19 迁移 2（升级守卫式 + 退化）；`TestBuildShadowRunRequestFromLiveState` 加闭包/桥装配断言。
- engine LLM 测试计数 373→**385**（+12，同口径）。
- 电池：`gofmt`；`golangci-lint run ./config/... ./engine/... ./llm/...` → **0 issues**（顺手修：ask/bid 常量单点化、forcetypeassert、gci、dogsled nolint、wastedassign、engine.go gosec nolint 复原）；`go build ./...` OK；聚焦+config+llm/jev 全绿；`make misc_checks` All passed（webUI node_modules 暂存法）；**engine 整套（-skip 网络类）81.066s 全绿**（实测 81.066s；首跑抓出一处真断言——备份恢复演练 `llm_backup_restore_drill_test.go` 版本前沿 18→19 两处随 v19 更新后复跑绿，非 flaky）。

### 六、REDUCE 主动平仓执行 + `RateLimitDegraded` 真读面（session-53 续）

用户追问「剩余工作有无阻塞决定」——答：无新门槛（凭据实物交付仍是唯一用户侧动作）。两件登记项本续段全部落地：

1. **REDUCE 主动平仓执行（全平语义）**：桥自「仅开仓」扩到平仓——`executeApprovedRun` 按动作分流；平仓= MARKET（02=B）+ `ReduceSide`=仓位方向 + 数量=**风控阶段评估过的同一仓位事实**（`f.positions` 由 RiskFacts 闭包自 RiskInput.Positions 捕获）；`activeExitSizing` 产全平计划（Notional=max(entry,mark)×qty 保守拼写；数量/价格读不清即拒）；ALLOWed reduce 无仓位=坏材料报错。**语义裁定（组装期决定，记录理由）**：候选契约不带减仓比例、07 §7 未定义主动平仓数量——「减少或关闭现有敞口」当下唯一可执行含义=全平；部分平仓待退出政策长成时改这一处。测试 3 新（平多→SELL/reduceOnly/0.5/25000、平空→BUY、无仓位→拒）。
2. **`RateLimitDegraded` 真读面**：`exchanges/request` 进程级降级地平线——`MarkRateLimitDegraded`（单调前移 atomic）+ `RateLimitDegradedUntil`；`evaluateRetry` 在 429/Retry-After 响应处标记，地平线=now+max(RetryAfter, backoff(attempt))（venue 指令或请求层自判等待取晚——**不发明窗口**；零值 Requester 无 backoff 可问，仅用 RetryAfter）；`DefaultRetryPolicy` 复用 `isRateLimitResponse`；引擎合成器 `rateLimitDegradedAt(now)` 读之——G 组字段自「显式 false（登记未钉）」换真读。测试 2 新（tracker 单调性 + 经管线标记，新路由 `/retry-after-future` 带未来 Retry-After）。**顺带真相**：`/always-retry` 的 Retry-After 是过去时刻（RFC1123 秒粒度）→ 地平线≈now——「venue 说立即可重试」不算持续降级，语义无误。

### 电池（session-53 续）

engine LLM 测试计数 385→**387**（+3-1）；`exchanges/request` 全包 **23.641s 全绿**（含新 2 测；首跑抓出既有 `TestEvaluateRetry` 零值 Requester 撞 nil backoff——nil guard 后复跑绿）；**engine 整套（-skip 网络类）86.150s 全绿**（终局跑，冻结点代码）；lint 0、misc All passed（首跑抓出本档 U+3000 全角空格两处与 request 测试 assert 消息一处 "must"——已修）、markdownlint 0、Unicode 干净（沿前段配方复跑）。

## 登记未钉（本段终态）

- **RESTING 生命周期**（M5-05 喂送）：`ConflictsWithResting` 重钉点——喂送落地前该字段的 false 是事实。
- **`JevManifestState` 未绑形态**：清单缺席时留 NULL（A 组自会拒），无 "UNBOUND" 词汇发明。
