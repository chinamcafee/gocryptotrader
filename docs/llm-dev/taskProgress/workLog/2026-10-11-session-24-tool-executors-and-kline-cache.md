# session-24：M2-07b 工具执行器 + M2-02 K 线缓存 + M2-05 数据质量应用

**日期**：2026-10-11
**范围**：① `M2-07b`——8 个只读工具执行器（含 M2-08 四道闸接线）；② `M2-02`——K 线缓存（已收盘永久 / 未收盘不缓存 / LRU / 配对失效）；③ `M2-05`——数据质量标签**应用面**（缺口/补齐/成形/本地视图）
**前置**：session-23（同日早段：M0-07 仓储层、M1-09~12、M2-07 契约层；其 workLog §9.1 留了 M2-07b 的字段级侦察）

---

## 1. 会话目标

按 session-23 的两条接力线落地：先用 §9.1 侦察结果把执行器一次写对（上一会话的坏稿已删），再收 K 线缓存关闭"重复打 REST"的口子。

## 2. 交付物

| 文件 | 内容 |
| --- | --- |
| `engine/llm_tools.go` | **新增（M2-07b）**：`LLMToolRunner`（实现 `ToolRunner`）+ `iToolDataSource` 8 方法窄接口 + 市场 id 三段严格解析 + 8 个执行器 + `evidence()`（四道闸） |
| `engine/llm_tools_test.go` | **新增**：8 个测试（fake 源 + 真库策略工具） |
| `engine/llm_kline_cache.go` | **新增（M2-02）**：`KlineCache`（LRU 整键、收盘永久、`InvalidatePair`）+ `fetchCandlesThroughCache` + `iCandleSource` |
| `engine/llm_kline_cache_test.go` | **新增**：6 个测试（含取数计数四段与并发） |
| `engine/llm_snapshot.go` | **改动**：`SnapshotRequest.Cache` 字段；`fetchCandles` 改走共享取数 |
| `engine/llm_tools.go` | **改动**：`LLMToolRunner.Cache` 字段；`fetchClosedWindow` 改走共享取数 |
| `llm/snapshot/quality_apply.go` | **新增（M2-05）**：`CandleGaps` · `IsZeroCandle`/`CountZeroCandles` · `ApplyCandleGaps` |
| `llm/snapshot/snapshot.go` | **改动**：`CandleFact.Partial` 字段 + `MarketSnapshot.Gaps` 字段 |
| `llm/snapshot/quality_apply_test.go` | **新增**：5 个测试（缺口/零值/降级记录/不许覆盖更具体理由/干净窗不动） |

## 3. M2-07b：一次写对的关键（对比上一会话的删稿）

- 编译仅 2 处小修：`orderbook.Item`→`orderbook.Level`（档位类型在 `orderbook_types.go`）；占位 `marshalJSON`→`json.Marshal`。**§9.1 侦察把 90% 的未知量提前消灭**——这就是它存在的原因。
- 8 个执行器的实质决定：
  - K 线只取已收盘（与组装器同一判据）、≤50 根、`asOf`=最新已收盘；
  - 指标：MACD 复用 `GetMovingAverageConvergenceDivergenceOnClose`（`Results`/`SignalVals`/`Histogram`），SMA/EMA 用 kline 助手，**RSI/ATR/BBANDS 本地公式**（文件注 TODO：待复核快照包提取面后统一，防双实现漂移）；
  - 盘口：`DeriveSpreadBPS`/`DeriveMidPrice` 同源；名义额滑点=数量加权均价与 bps（150 USD 跨 100/101 两档 → 均价 150/1.4950… 有精确断言）；
  - `getFunding` **不传 `IncludePredictedRate`**（binance 500——webUI 坑位同源）；`listMarkets` query 过滤 + maxItems 帽；`getAccountState` 账号闸；策略历史/风控预算走 `llmstore`（真库测试）。
- **四道闸在 `evidence()` 内执行**（`candidate.ToolEvidence.Validate` + `Scope.CheckEvidence`）——M2-08 接线由此就地完成；scope 外市场/账号/策略各有拒绝测试。

## 4. M2-02：缓存的三条纪律与一处关键修正

- **成形 bar 永不入缓存**；**LRU 淘汰整键**（{pair,asset,interval}），热市场不会只剩旧 bar；**指标不进缓存**。
- **关键修正（写测试时暴露）**：`covers` 必须**豁免 `end` 所在仍在成形的槽位**——调用方天然请求 `[start, now]`，成形槽永远不可缓存，不豁免则命中路径永不触发（缓存写了等于白写）。修正后测试四段验证：取数(计数1)→命中(仍1)→跨窗重取(计数2)→再命中(仍2)。
- **取数路径与命中路径返回语义不同**：取数路径原样带上成形 bar（组装器要继续携带 `LiveCandle`）；命中路径只回已收盘历史（"现在价在哪"来自 ticker/盘口，不来自缓存的 kline 猜测）。
- `InvalidatePair` 方法已备；**SetExchangePair 的调用点接线**与"轮询重载喂缓存"的接线属后续顺手项（已记入 PROGRESS §20）。

## 5. M2-05：数据质量标签的应用面

词表与 only-lower 机制（`llm/candidate/quality.go`）在早前会话已落地；本条补的是"标签真的被贴上"的部分：

- `CandleGaps`：相邻收盘 K 线间隔 > interval 即缺口，逐段记 `GapRange{Start, End, MissingBars}`（3 根缺失的洞记 3）。
- `IsZeroCandle`：全零 OHLCV = `addPadding` 的产物——"穿着 K 线外衣的缺口"；零收盘会污染它进入的任何均值。一根真实成交但零变动的 K 线（Volume>0）不算。
- `ApplyCandleGaps`：**蜡烛与指标同时标 GAP**（指标跨缺口算出来的就是交易所从未提供过的插值），`Gaps` 字段留明细（非空 ⇔ 类目 GAP）。
- **关键修正（测试抓出）**：必须**跳过已降级的类目**——GAP 与 STALE 同秩，只靠 SetQuality 的 only-lower 守卫会允许覆盖，`TestApplyCandleGapsOnlyLowers` 钉住"STALE 幸存、指标仍被标记"。
- `CandleFact.Partial` + 组装器给成形根置位（`applyCandles` 里 `live.Partial = true`）；组装器函数尾改为跑完指标再 `ApplyCandleGaps()`。
- **账户事实从 EXCHANGE_REST 改标 LOCAL_TRACKED**（docs 04 §5 表：缓存余额/调用方供给仓位是本地视图，仅供参考；风控放行前必须 EXCHANGE_REST 复核）。
- **一处层级修复**：共享取数把空窗口**透传**给调用方而不是自造哨兵——`ErrSnapshotIncomplete`（组装器）与工具拒绝各自保留自己的短语，跨层的哨兵污染被既有的 `TestProviderErrorsAreNotSwallowed` 抓出后改正。
- 另修：`snapshot.Interval.Duration()` 的单位是**秒**（int64），不是纳秒——缺口算术按秒×`time.Second` 换算（测试里 3.6µs 的诡异缺口暴露了它）。

## 6. 测试夹具教训（两处，都是"夹具错了不是代码错了"）

1. `cacheWindow()` 初版 61 根从 `-61h` 起——末根落在 `now-1h`，**全部已收盘**，与"末根成形"的意图相反：merge 断言 60 实得 61。改从 `-60h` 起（末根=now=成形）。
2. "跨窗重取后命中"断言期望 51 根，实得 50——**忘了请求窗 `[later-50h, later]` 里成形槽没有 bar**。窗口边界又一次是自摆乌龙的地方（与 session-23 预算测试同类）。

## 7. 账目说明

- session-23 收尾：**总完成度 44.4%**（M2 组 10.5/16，3.94%——`M2-07b` 已在 session-23 §3 计分，本会话兑现落地）；
- `M2-02` **新增计分**：M2 组 10.5/16 → 11.5/16（3.94% → 4.31%）；
- `M2-05` **新增计分**：M2 组 11.5/16 → 12.5/16（4.31% → 4.69%）；
- `M2-09` **新增计分**：M2 组 12.5/16 → 13.5/16（4.69% → 5.06%）；
- `M2-11` **新增计分**：M2 组 13.5/16 → 14.5/16（5.06% → 5.44%）；
- `M2-10` **新增计分**：M2 组 14.5/16 → 15.5/16（5.44% → 5.81%）。
- **总完成度 44.4% → 46.3%**（session-24 累计 +1.9；M2 组仅余 `M2-13` 两项残余）。
- 另：K 线缓存接线（非计分项，3 个新测试）——engine LLM 相关测试 106 → 109。
- 另：proto 工具链校准（非计分项，清除全项目唯一技术阻塞）——见 §12。
- 另：`M3-01` **新增计分**：M3 组 0/9 → 1/9（0.00% → 0.44%）——见 §13。
- 另：`M3-02` **新增计分**：M3 组 1/9 → 2/9（0.44% → 0.89%）——见 §13。
- 另：`M3-03` **新增计分**：M3 组 2/9 → 3/9（0.89% → 1.33%）；**总完成度口径修正**：M3=2/9 处应为 47.2%（47.1% 系舍入值二次累计）——见 §13。
- 另：`M3-04` **新增计分**：M3 组 3/9 → 4/9（1.33% → 1.78%）——见 §13。
- 另：`M3-05` **新增计分**：M3 组 4/9 → 5/9（1.78% → 2.22%）——见 §13。
- 另：`M3-06` **新增计分**：M3 组 5/9 → 6/9（2.22% → 2.67%）——见 §13。
- 另：`M3-08` **新增计分**：M3 组 6/9 → 7/9（2.67% → 3.11%）——见 §13。（M3-07 真流式仍待做。）
- 另：`M3-07` **新增计分**：M3 组 7/9 → 8/9（3.11% → 3.56%）——见 §13。（M3 仅剩 M3-09 Web UI 消费流式。）
- 另：`M3-09` **新增计分**：M3 组 8/9 → 9/9（3.56% → 4.00%，**M3 组完成**）——见 §13。总完成度跨过 50%。
- 另：`M4-01` **新增计分**：M4 组 0/16 → 1/16（0.00% → 0.50%，M4 开工）——见 §14。总完成度 50.8%。
- 另：`M4-02` **新增计分**：M4 组 1/16 → 2/16（0.50% → 1.00%）——见 §15。总完成度 51.3%。
- 另：`M4-03` **新增计分**：M4 组 2/16 → 3/16（1.00% → 1.50%）——见 §16。总完成度 51.8%。
- 另：`M4-04` **新增计分**：M4 组 3/16 → 4/16（1.50% → 2.00%）——见 §17。总完成度 52.3%。
- 另：`M4-05` **新增计分**：M4 组 4/16 → 5/16（2.00% → 2.50%）——见 §18。总完成度 52.8%。
- 另：`M4-06` **新增计分**：M4 组 5/16 → 6/16（2.50% → 3.00%）——见 §19。总完成度 53.3%。
- 另：`M4-07` **新增计分**：M4 组 6/16 → 7/16（3.00% → 3.50%）——见 §20。总完成度 53.8%。
- 另：`M4-08` **新增计分**：M4 组 7/16 → 8/16（3.50% → 4.00%）——见 §21。总完成度 54.3%。
- 另：`M4-09` **新增计分**：M4 组 8/16 → 9/16（4.00% → 4.50%）——见 §22。总完成度 54.8%。
- 另：`M4-10` **新增计分**：M4 组 9/16 → 10/16（4.50% → 5.00%）——见 §23。总完成度 55.3%。
- 另：`M4-11` **新增计分**：M4 组 10/16 → 11/16（5.00% → 5.50%）——见 §24。总完成度 55.8%。
- 另：`M4-12` **新增计分**：M4 组 11/16 → 12/16（5.50% → 6.00%）——见 §25。总完成度 56.3%。
- 另：`M4-13` **新增计分**：M4 组 12/16 → 13/16（6.00% → 6.50%）——见 §26。总完成度 56.8%。
- 另：`M4-14` **新增计分**：M4 组 13/16 → 14/16（6.50% → 7.00%）——见 §27。总完成度 57.3%。
- 另：`M4-16` **新增计分**：M4 组 14/16 → 15/16（7.00% → 7.50%）——见 §28。总完成度 57.8%。
- 另：`M4-15` **新增计分**：M4 组 15/16 → **16/16 完成**（7.50% → 8.00%）——见 §29。总完成度 58.3%。
- 另：`M5-01` **新增计分**：M5 组 0/18 → **1/18**（0.00% → 0.39%）——见 §30。总完成度 58.7%。
- 另：`M5-03` **新增计分**：M5 组 1/18 → **2/18**（0.39% → 0.78%，先于 M5-02 完成——纯映射可离线验收）——见 §31。总完成度 59.1%。
- 另：`M5-02` **新增计分**：M5 组 2/18 → **3/18**（0.78% → 1.17%）——见 §32。总完成度 59.5%。
- 另：`M5-04` **新增计分**：M5 组 3/18 → **4/18**（1.17% → 1.56%）——见 §33。总完成度 59.9%。
- 另：`M5-05` **新增计分**：M5 组 4/18 → **5/18**（1.56% → 1.94%）——见 §34。总完成度 60.3%。
- 另：`M5-06` **新增计分**：M5 组 5/18 → **6/18**（1.94% → 2.33%）——见 §35。总完成度 60.7%。
- 另：`M5-07` **新增计分**：M5 组 6/18 → **7/18**（2.33% → 2.72%）——见 §36。总完成度 61.1%。
- 另：`M5-08` **新增计分**：M5 组 7/18 → **8/18**（2.72% → 3.11%）——见 §37。总完成度 61.5%。
- 另：`M5-09` **新增计分**：M5 组 8/18 → **9/18**（3.11% → 3.50%）——见 §38。总完成度 61.9%。
- 另：`M5-10` **新增计分**：M5 组 9/18 → **10/18**（3.50% → 3.89%）——见 §39。总完成度 62.3%。
- 另：`M5-11` **新增计分**：M5 组 10/18 → **11/18**（3.89% → 4.28%）——见 §40。总完成度 62.7%。
- 另：`M5-12` **新增计分**：M5 组 11/18 → **12/18**（4.28% → 4.67%）——见 §41。总完成度 63.1%。
- 另：`M5-13` **新增计分**：M5 组 12/18 → **13/18**（4.67% → 5.06%）——见 §42。总完成度 63.5%。
- 另：`M5-14` **新增计分**：M5 组 13/18 → **14/18**（5.06% → 5.44%）——见 §43。总完成度 63.9%。
- 另：`M5-15` **新增计分**：M5 组 14/18 → **15/18**（5.44% → 5.83%）——见 §44。总完成度 64.3%。
- 另：`M5-16` **新增计分**：M5 组 15/18 → **16/18**（5.83% → 6.22%）——见 §45。总完成度 64.7%。
- 另：`M5-17` **新增计分**：M5 组 16/18 → **17/18**（6.22% → 6.61%）——见 §46。总完成度 65.1%。
- 另：`M5-18` **新增计分**：M5 组 17/18 → **18/18 完成**（6.61% → 7.00%）——见 §47。总完成度 65.5%。

## 8. 验证证据

```text
gofmt -l engine/ llm/ database/                       # 无输出（另修了一处再生成套件的格式）
go build ./...                                        # 通过
go vet ./engine/                                      # 无输出
go test ./engine/ -run 'TestLLMTools' -count 1        # ok（8 个测试全过）
go test ./engine/ -run 'TestKlineCache|TestFetchCandlesThroughCache' -count 1   # ok（6 个测试全过）
go test ./engine/ -run 'TestCreateProviderProfile|TestAddProviderRevision|TestActivateModelPair|TestEffectiveReservationCap|TestReserveBudget|TestToolLoop|TestLLMTools' -count 1   # ok（M1/M2 引擎整套）
go test ./llm/snapshot/ -count 1                      # ok（含 5 个新缺口测试）
go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'   # ok（整套 85.3s）
go test ./llm/... -count 1                            # 十包全绿（253 个顶层测试函数 / 383 条 PASS 含子测试）
go test ./llm/jev/ ./llm/coverage/ -count 1           # ok（M2-11/M2-10 续跑；含 8 + 4 个新测试）
go test ./llm/chat/ -count 1                          # ok（M3-01 + M3-02；14 个测试）
go test ./database/repository/llm/ -run TestChatStorageRoundTripsEncryptedBodies -count 1   # ok（真库迁移 + 加密往返）
go test ./engine/ -run 'TestChatRun' -count 1         # ok（M3-03；7 个新测试）
go test ./engine/ -run 'TestToolLoop' -count 1        # ok（ResponseFormat 字段化后 8 个既有测试全过）
go test ./engine/ -run 'TestChatRun|TestEvidenceCards' -count 1   # ok（9 全过，含 2 个证据卡片测试）
go test ./engine/ -run 'TestChatService|TestSetupChat' -count 1   # ok（M3-05 服务层；6 全过）
go test ./engine/ -run 'TestLLMConversationRPC|TestLLMSendMessageCarries' -count 1   # ok（M3-05 RPC 层；3 全过）
git diff gctrpc | shasum                              # 95883d6f…（buf generate 二次跑同一哈希=新端点纯增量）
go test ./cmd/gctcli/ -count 1                        # ok（M3-06；整套含 3 个 REPL 测试）
go test ./engine/ -run 'TestChatSafety' -count 1      # ok（M3-08；3 个安全边界测试）
go test ./llm/openai/ -count 1                        # ok（M3-07；含 5 个流式新测试）
go test ./engine/ -run 'TestChatStream|TestLLMChatStream' -count 1   # ok（M3-07；4 + 2 个测试）
node scripts/check-sse-parser.mjs                     # sse parser checks: 6/6 ok（含逐字节切碎喂入）
go test ./engine/ -run 'TestPolicyExtraction' -count 1 # ok（M4-01；7 个测试）
go test ./llm/openai/ -run TestPolicyPrompt -count 1   # ok（含防漂移 reflect 测试）
go test ./engine/ -run 'TestStrategyFreeze|TestRiskEnvelopeHash' -count 1 # ok（M4-02；7 个测试）
go test ./engine/ -run 'TestBuildVersionTree|TestDiffPolicies' -count 1 # ok（M4-03；3 个测试）
go test ./engine/ -run 'TestMarketGrant|TestApprovedMarkets' -count 1 # ok（M4-04；4 个测试）
go test ./database/repository/llm/ -count 1  # ok（14 个 e2e，新函数真库验收）
go test ./engine/ -run 'TestApproval' -count 1  # ok（M4-05；8 个测试）
go test ./engine/ -run 'TestEvaluationScheduler' -count 1  # ok（M4-06；4 个测试）
go test ./engine/ -run 'TestPreGates' -count 1  # ok（M4-07；4 个测试，19 个子测试）
go test ./engine/ -run 'TestAnalysis' -count 1  # ok（M4-08；7 个测试，7 个子测试）
go test ./engine/ -run 'TestJevReview' -count 1  # ok（M4-09；6 个测试）
go test ./engine/ -run 'TestSynthesize' -count 1  # ok（M4-10；15 个测试，含 12 个子测试）
go test ./engine/ -run 'TestRisk' -count 1  # ok（M4-11；17 个测试，95 条 PASS 含子测试）
go test ./engine/ -run 'TestSizing' -count 1  # ok（M4-12；9 个测试，20 条 PASS 含子测试）
go test ./engine/ -run 'TestDecision' -count 1  # ok（M4-13；8 个测试，11 条 PASS 含子测试）
go test ./engine/ -run 'TestPairSwitchCascade' -count 1  # ok（M4-14；4 个测试）
go test ./engine/ -run 'TestShadowRunner' -count 1  # ok（M4-16；5 个测试）
go test ./engine/ -run 'TestLLMStrategyRPC|TestOperatorPassword|TestStrategyAndDecisionConverters' -count 1  # ok（M4-15；4 个测试）
go test ./cmd/gctcli/ -count 1  # ok（含既有 3 个 REPL 测试）
go test ./engine/ -run 'TestBuildIntent|TestIntentRecorder|TestLimitPrice|TestClientOrderID' -count 1  # ok（M5-01；8 个测试）
go test ./engine/ -run 'TestBuildOrderSubmit|TestFloatWithin' -count 1  # ok（M5-03；5 个测试）
go test ./engine/ -run 'TestDispatch' -count 1  # ok（M5-02；6 个测试）
go test ./engine/ -run 'TestDispatch' -count 1  # ok（M5-04 扩测；7 个测试）
go test ./engine/ -run 'TestUnknownIsNeverResubmitted|TestIntentStateMachineTransitions' -count 1  # ok（M5-05；3 个测试）
go test ./engine/ -run 'TestProtectiveStopPrice|TestBuildProtectiveExit|TestEnforce' -count 1  # ok（M5-06；5 个测试）
go test ./engine/ -run 'TestExitTracker' -count 1  # ok（M5-07；4 个测试）
go test ./engine/ -run 'TestDiffPositions|TestReconcile' -count 1  # ok（M5-08；4 个测试）
go test ./engine/ -run 'TestUtcDayKey|TestSealIsOnce|TestRecordFlowValidates|TestDailyLossMath|TestTripDailyLossHalt' -count 1  # ok（M5-09；5 个测试）
go test ./engine/ -run 'TestHalt' -count 1  # ok（M5-10；5 个测试）
go test ./engine/ -run 'TestEmitFormats|TestTheTwoLoudBells|TestDecisionRejectCarries|TestEveryEventType' -count 1  # ok（M5-11；4 个测试）
go test ./engine/ -run 'TestModeCapabilityMatrix|TestUnlockLiveTripleGate|TestModeSwitchAuth' -count 1  # ok（M5-12；3 个测试）
go test ./engine/ -run 'TestNetworkConsistency' -count 1  # ok（M5-13；3 个测试）
go test ./engine/ -run 'TestMetricStoreMath|TestSemanticRecorders|TestSnapshotJSONAndLogSeam|TestLLMGetMetricsRPC' -count 1  # ok（M5-14；4 个测试）
go test ./engine/ -run 'TestRecover' -count 1  # ok（M5-15；3 个测试）
go test ./communications/... -count 1  # ok（M5-16；webhook 4 个测试 + 存量六通道全绿）
go test ./engine/ -run 'TestRiskSwitch|TestLLMSetRiskSwitchRPC' -count 1  # ok（M5-17；5 个测试）
go test ./engine/ -run 'TestReviewExpiry' -count 1  # ok（M5-18；2 个测试）
npm run typecheck && npm run lint && npm run build    # 全过（oxlint 0 警告）
# 浏览器实测：mock 网关(9443) + 代理(8788 指向 mock) + vite(5173) → 发送→流式渲染→证据卡片→回放
go test ./engine/ -run 'TestFullReloadFeedsTheKlineCache|TestEngineInvalidatesTheLLMCachedPair|TestSetupRotationHandsTheManagerCacheToTheRotator' -count 1   # ok（3 个新接线测试）
go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'   # ok（整套 83.6s）
go vet ./llm/... ./engine/...                         # 无输出
go build ./...                                        # 通过（续跑收尾再跑一次）
markdownlint-cli2 docs/llm-dev, docs/localDeploy      # 0 issues
```

## 9. M2-11 回填批次预估（续跑）

- 新文件 `llm/coverage/backfill.go`：`BuildBackfillPlan(BackfillRequest)` **纯函数**（无 I/O、无时钟）——`Candles` 含端口（闭区间 +1 根）；`Batches = ceil(Candles/请求 K 线数)`（默认 1000/批；单批 ≤3 + 在途 ≤3 双约束）；`WeightPerBatch` 复用 `coverage.KlineWeight`（**与扫描器同一张表**——预算口径唯一）；`Proposal` 是可直接粘贴的 `gctcli datahistory addjob savecandles` 命令（`--start_date="2006-01-02 15:04:05"` 引号日期；单批 `request_size_limit` 取计划值）。
- `ErrInvalidPlan` 哨兵：缺失市场/空区间/倒置区间/非法时长逐项拒绝；`nickname` 运营者可覆盖，默认 `<exchange>-<asset 短名>-<base>-<1h>` 形。
- **权重对照 04 §9 例题**：3 年 BTC 1h → 26280 根 / 27 批 / 单批权重 10 / 总 270，`TestBackfillPlanMatchesTheDesignExample` 逐字段钉住；另 3 个测试覆盖取整边界（26281 根 → 28 批，夹具用 27001h）、坏请求矩阵、昵称覆盖。
- **用途边界**：这是"产出确认提案"的规划工具，**不直接驱使** DataHistoryManager——执行仍由运维对提案人工确认（04 §9 流程）。

## 10. M2-10 同源事实传递（续跑）

- 新文件 `llm/jev/evidence_guard.go`：`ValidateReviewEvidence(evidence, manifest, now)` + `CheckEvidenceScope(evidence, marketID, strategyVersionID)` + `ErrEvidenceRejected`——**06 §4.2 的复验在裁判入口成硬闸**。
- **复验项**（逐项有测试）：空证据 / >8 条 / 重复 ref / 单条 >12KB / 总量 >32KB / 未知质量标签（`candidate.ParseQuality` 判） / 敏感键（`candidate.ScanForSensitiveKeys`） / 时间越界（`item.Validate(now)`）。
- **清单哈希比对**：提供 `InputManifest` 时——条数必须相等、每条证据的 `Ref` 必须在清单 `ToolResponses` 里有对应条目、且 `HashBytes(data)` 必须等于 `ResultSha256`；不等即拒（`evidence changed between calls`）。测试钉两种改法：改原文 + 改非规范 JSON（哈希按**字节**，改一个空白就露馅）。
- **作用域闸对裁判侧同样生效**：证据携带的 `MarketID`/`StrategyVersionID` 与快照/策略不一致即拒（与工具层同构；`CheckEvidenceScope` 并挂进 `BuildReviewRequest`）。
- `ReviewInput` 新增三必填：`PromptVersion`（非空时必须恒等 `openai.PromptVersionAnalysisReadOnly`）、`Manifest`（可选，提供即比对）、`Now`（必填——证据老化基准由调用方供给：重放场景可对回原始窗口，构造器保持纯函数**不读系统时钟**）。
- **逐字节保真测试**（`TestReviewRequestCarriesEvidenceByteForByte`）：夹具走"组装器 marshal → 反序列化 → `BuildReviewRequest` → 判官侧"全链路——`bytes.Equal(original.Data, state.Evidence[0].Data)` + 两个 `HashBytes` 相等 + 同输入二次构建请求体字节一致（重放可证明"当时给裁判看的就是这些字节"）。
- **踩坑两处（均为编译/测试期即抓出）**：① `SystemOneRequest.State` 是 `any`，测试取值需 `request.State.(ReviewState)` 类型断言；② `Now` 必填使既有 `TestMarshalEnforcesByteCap` 的裸 `ReviewInput` 字面量失败——按"因自己改动而失败的断言**更新断言**"补 `Now`（不是加 skip）。

## 11. K 线缓存接线（收尾）

- **缓存所有权落在 `LLMManager`**：`cache *KlineCache` 字段 + `defaultLLMCacheKeys = 256`（LRU 整键淘汰，单键=单市场历史，几百键覆盖全部跟踪市场的一两个周期而不随运行时长涨内存）；`SetupLLMManager` 构造并 `Cache()` 暴露。
- **重载喂缓存**：`RotationConfig.Cache` 由 `SetupRotation` 传入（`Cache: m.cache`）；`Step` 的重载分支在 `fetchFullWindow` 成功后、`scanAfterReload` 之前把全量窗口交给 `merge`——**扫描失败不许丢弃已经发生的取数**，所以先喂缓存；`merge` 错误只记日志（fetch 已成功，缓存是加速器不是正确性依赖）。
- **禁用即失效**：新钩子 `Engine.InvalidateLLMCachedPair(pair, asset)`（nil 引擎 / 未启用子系统均安全返回 0）挂在 `RPCServer.SetExchangePair` 的禁用分支——pair 离开启用集即从缓存删除；重新启用走重新取数而非复用旧数据。
- **3 个测试**：`TestFullReloadFeedsTheKlineCache`（增量步不进缓存；整点重载后恰好 1 键、6 根已收盘、最新一根=整点收盘、成形根不进；`InvalidatePair` 清键）· `TestEngineInvalidatesTheLLMCachedPair`（种子合并→钩子删除→nil 引擎与空引擎安全）· `TestSetupRotationHandsTheManagerCacheToTheRotator`（`&binance.Exchange{}` 走真实 SetupRotation 路径，`assert.Same` 钉住传的正是管理器那把缓存，不是私有副本）。
- **踩坑一处（夹具）**：binance wrapper 的类型名是 `binance.Exchange` 不是 `binance.Binance`（编译期抓出，见 rpcserver_test 同款用法）。

## 12. proto 工具链校准（阻塞项清除，不计分）

- **根因（实测，非猜测）**：本机插件版本与 `go.mod` tool 块**完全一致**（`export PATH="$HOME/go/bin:$PATH"` 后：buf 1.73.0 / protoc-gen-go v1.36.12 / protoc-gen-go-grpc 1.6.2 / protoc-gen-grpc-gateway v2.31.0 / protoc-gen-openapiv2 v2.31.0）。不幂等的真正原因是**提交的生成文件出自更旧插件**：`rpc_grpc.pb.go` 头部仍写 v1.6.1（本机 1.6.2，该文件 diff 恰 2 行=版本注释）；`rpc.pb.gw.go` 的 336 行**全部是同一种 reorder**——每处只有 `if req.Body != nil { io.Copy(io.Discard, req.Body) }` 相对 `ParseForm`/`PopulateQueryParameters` 的位置变化（旧 gateway 输出把 drain 放在前面）。**逐行核查无第二种差异形状**；`rpc.pb.go` 与 swagger 零差异。
- **校准动作**：`cp -a gctrpc /tmp/gctrpc_backup` 备份后 `cd gctrpc && buf generate`——一次性对齐 diff = 2 文件 / +169−169（`rpc.pb.gw.go` 336 行、`rpc_grpc.pb.go` 2 行）。
- **幂等验收**：`buf generate` **连跑两次**，`git diff gctrpc | shasum` = `80497e7459dc694c025d64122999e68b36524f1b` 两次完全一致 → **从现在起生成幂等**，之后新增端点只会产生新增端点的 diff。
- **建议**：这次「仅重新生成」单独成一个提交（message 注明 protoc-gen-go-grpc 1.6.1→1.6.2、grpc-gateway →v2.31.0）——本轮工作按惯例不擅自提交，已在台账注明待办。
- **解锁项**：`M1-14`、`M3-05`、`M4-15`、`M5-14`、`M5-17`、`W1-07` 及全部 `/v1/llm/*` 端点。
- **验收命令**：`go build ./gctrpc/ && go vet ./gctrpc/ && go build ./...` 全部通过（生成目录无单测）。

## 13. M3-01~M3-09 对话能力（存储/上下文/流水线/卡片/RPC/REPL/安全边界/真流式/Web 消费），M3 组完成

- 新包 `llm/chat`（`llm/` 由此成**十一包**）：`ConversationCipher`（`Seal`/`Open`）+ `LoadOrCreateConversationKey`。设计依据 09 §5.4 / Q17=C：标题明文（保持可搜索）、正文加密。
- **三态落地**：正文 AES-256-GCM 进 `content_cipher`、nonce 进 `content_nonce`、明文 SHA-256（小写 hex）进 `content_sha256`；**AAD 恰为 `message_id`**。
- **AAD 绑行的验证**有单测与真库两层：`TestOpenRejectsMovedCiphertext`（同字节换 id 即拒）+ `TestChatStorageRoundTripsEncryptedBodies`（真库两行互换密文必拒、本行原样开锁且逐字节一致）。
- **错误分类**：认证失败 `ErrDecrypt`（该行不可信：错钥/篡改/跨行）与摘要不符 `ErrDigestMismatch`（解密成功但哈希对不上——写入方不是本 cipher）分开报。
- **密钥文件** `llm-vault/conversation-key.bin`：32 字节 0600；`O_EXCL` 创建——双进程竞争时败者重读胜者的钥匙而不是覆写（两把钥匙=一半历史不可读）；**错尺寸文件拒绝而非重建**（同一个理由）。
- **7 个单测**：往返（含中文/无明文残留断言）· nonce 每封必新 · AAD 跨行拒收 · 篡改（密文/ nonce / 截断/ 错钥）· 摘要不符与换摘要两种 · 误用（nil cipher / 空 id / 16·31·33 字节钥匙）· 密钥文件生命周期（创建 0600/幂等/跨进程可用/错尺寸拒绝）。
- **真库 e2e**：`database/repository/llm` 新增 `TestChatStorageRoundTripsEncryptedBodies`（8 域仓储层 12→13 个端到端测试）。
- **踩坑零**：一次通过（与 M2 批次不同，这次先读形状后落笔）。

### M3-02 上下文重建（同包 `context.go`，7 测试）

- `BuildContext(messages []StoredMessage) ([]ContextMessage, error)`：把存储行重建成模型可用的指令序列。**只收 `user` 行**——**测试故意放一行坏掉的 assistant**（密文第一个字节取反），重建必须照常成功：损坏的助手行不许掩盖用户指令。
- **按 `Seq` 排序**（`SliceStable` 于副本上——不修改调用方切片，有测试钉住"输入乱序→输出按序"与"不改输入"）。
- **上限=拒绝不是截断**（05 §8.1 硬要求）：257 条 / 16001 字符各自 `ErrContextTooLarge`，错误信息带**实际数与上限**并提示"summarise into a new conversation"（"明确告知用户"）。
- **字符按 rune 计**：8000 CJK + 8000 ASCII = 16000 字符（32000 字节）必须放行——按字节算会误拒中文指令（测试同时钉住 16001 rune 被拒）。
- **边界含等号**：恰 256 条、恰 16000 字符放行。
- **坏掉的 user 行必须炸出来**（`ErrDecrypt` 包装、带行 id）：静默跳过它就是另一种形式的截断。
- 空指令集（含"只有 assistant 行"的用例）→ `ErrContextEmpty`。

### M3-03 chat 模式流水线（`engine/llm_chat.go`，7 测试）

- **共享工具集与循环**：直接复用 `RunToolLoop` + `tools.Specs()` 的 8 只读工具——两个模式的代码路径共享"怎么问工具"，不共享"输出什么"。
- **自由文本**：`ToolLoopRequest.ResponseFormat` 从循环里的硬编码 `json_object` 改为字段——决策侧显式传 json_object（既有断言随夹具更新），chat 侧留 **nil**。这是"对话不产 Candidate"在协议层的落点。
- **先验证后落库**：新输入先与历史合并为前瞻上下文过 `BuildContext` 的 256 条/16000 字符闸——超限时**库零写入、预算零占用、provider 零调用**（测试对三者逐一断言）。这也顺手解决了"首条消息无历史"的 `ErrContextEmpty` 陷阱（前瞻里已有新句）。
- **预算在环**：`CHAT` purpose 预约 → 成功结算。`ToolLoopResult` 新增 `UsageInputTokens`/`UsageOutputTokens`（循环按响应累加拆分；`CumulativeTokens` 保留原义）——预算是按输入/输出分列记账的。传输失败：**释放槽位**且**不留助手行**（问句保留）。HOLD（截断等）：按已耗 token 结算 + 助手行 `error_code` 记因。
- **持久化**：问句与回复都走 M3-01 密封（AAD=行 id）；助手行带 profile 修订版/模型/`chat-analysis-v1`/用量/延迟/工具转录（有工具调用时）。工具转录构建失败只降级为一行日志——答案已经拿到，复盘细节不拖垮回复。
- **修订版把关**：Jev 修订版没有对话端点 → `ErrChatProfileKind`（kind 校验前置，provider 未调用）。
- **踩坑一处（编译期）**：`BudgetReservationRequest` 是 engine 自己的类型不是仓储类型（仓储只有 `BudgetReservation`）——接口签名改回本包类型即好。

### M3-04 证据卡片（`engine/llm_chat_cards.go`，2 测试）

- **卡片是转录的显示视图，不是新的事实源**：关键发现是 `LLMToolRunner.evidence()` 交给循环的就是完整 `candidate.ToolEvidence` 信封（Ref/AsOf/Quality/Exchange/MarketID/Data）——所以转录里存的本来就是信封，卡片提取 = **解码信封**，不需要猜 executor 的 JSON 形状。
- 字段按 11 §M3-04：`market + sourceTime(asOf) + snapshotId + dataQuality + ref + sha256`；`sha256` 用循环对结果字节算的哈希（**显示与取证同源**）；`snapshotId` 对话工具没有快照留空（M4 决策路径填充）。
- **降级策略**：参数回填反馈串等非信封结果直接跳过——卡片少一张不等于回合失败，转录仍是完整记录（有测试钉住"反馈串不出卡片"与"空信封不算卡片"）。
- `ChatRunResult.Evidence` 对齐 08 §3.1 `LLMSendMessageResponse.evidence`；提取器无状态可直接被 M4 复用。

### M3-05 conversations/messages 6 RPC（proto 新端点首次实战）

- **proto**：`gctrpc/rpc.proto` +17 message / +6 RPC（List/Create/Get/Archive/ListMessages/SendMessage，REST 注解齐备）；
  `cd gctrpc && buf generate` 产出**纯增量 diff**——连跑两次哈希 `95883d6f…` 不变（校准后的工具链价值首次兑现：这个改动在
  proto 校准之前根本不该动）。
- **引擎侧两层**：`engine/llm_chat_service.go`（会话 CRUD、解密读取、SendMessage→Pipeline；`SetupChat` 首次使用落 32B 0600
  密钥；`ActiveLLMRevisionID` = 激活配对的 LLM 边——**切配对即切对话模型**）+ `engine/rpcserver_llm.go`（6 个 handler 只做
  proto↔struct 转换，零业务逻辑）。`Engine.llmChatService` 字段挂装配位。
- **fail-closed**：服务未装配（惰性装配在 `engine.go` 里还需要 client factory）→ handler 报 `errLLMChatUnavailable`，
  **不回空列表**——空会话列表与坏子系统在 webUI 眼里必须是两回事。
- **测试**：服务层 6（创建/校验/归档时钟/收发回读/坏行炸错/无修订版拒绝）+ RPC 层 3（全链路 round-trip、证据卡片转换、
  fail-closed 三态）——全离线：fake 表 + fake 历史 + fake 预算 + 脚本化 transport。
- **顺带**：`ChatRunResult` 增 `AssistantMessageID`/`ToolTranscript`（RPC 响应要回"存的是哪一行"与留存转录）；
  `iChatHistory` 增 `MessageDetails`（服务读展示元数据与管线读密封体分开）。
- **生产装配待办（写进 §14）**：`engine.go` 需要按激活修订版构造 openai 客户端的 client factory 才能创建 `llmChatService`；
  在那之前 RPC 保持 fail-closed，不影响测试与其它路径。

### M3-06 gctcli REPL（`cmd/gctcli/llm_chat.go`，3 测试）

- `llmchat` 命令进 `app.Commands`：`--conversation`（空则建档 + `--title`）/`--turn-timeout`（默认 30s）/`--it`（放宽 120s）。
- **REPL 核心对 `llmChatSession` 窄缝编程**（Send/History/ConversationID）——gRPC 只在一层适配里出现，测试用假会话 + 脚本化
  stdin 全离线跑（08 §4.3 的实现要点逐条落）。
- **`bufio.Reader` 非 `bufio.Scanner`**：Scanner 的默认行上限（64KB）会把长指令**静默切成两条**——分两截到达模型的指令是
  另一条指令。读错只认 EOF（行尾无换行也算一整句，测试钉住）。
- 每轮：`LLMSendMessage` → 打印回复、**转录摘要**（工具名/参数/sha 前 12 位）、证据卡片行、用量/延迟、HOLD 原因；
  `:evidence <ref>` 打全卡 JSON + sha256；`:history` 打已存消息；`:save` 说明逐轮已持久化并打会话 id；`:quit`/`:exit` 退出。
- **交互错误不终止会话**：回合 RPC 报错、坏转录 JSON、未知 ref、`:history` 失败——都只打印提示（测试覆盖四种），
  且 `:quit` 后零发送（测试断言 sent 列表）。
- 踩坑一处（机器检查抓出）：`gofmt -l` 报测试文件未格式化——`gofmt -w` 即好。

### M3-08 chat 安全边界（`engine/llm_chat_safety_test.go`，3 测试）

- **机器化边界优先于提示词边界**——提示词会被下一版改掉，类型与结构不会撒谎：
  - **无执行面**：`reflect` 白名单遍历 `ChatPipeline` 与 `ChatRunResult` 的字段——出现任何 Order/Decision 型字段（或名字不在
    白名单里的新字段）即测试失败。"对话模式能不能下单"由类型系统回答，而不是靠读代码。
  - **提示词逐字**：chat 提示词必须含三段拒绝语（"no signing, order, network URL or secret tools" /
    "NEVER produces trade candidates, orders, approvals" / "point them to the strategy approval flow"），且不得含
    `ResponseFormat` 或 `candidate_action`（决策契约漏进对话会让模型在只读模式里想产出 Candidate）。
  - **要下单也只是对话**：脚本化"直接帮我开个 BTC 多单"→ 模型拒绝 → 拒绝文本按普通助手行密封落库、**零工具调用**、
    预算正常结算；每轮系统提示都带 refusal framing（断言转录第一句就是它）。
- 踩坑一处（测试自身）：断言片段"NEVER produces ... approvals or execution"跨了提示词的换行——收窄到不带换行的片段即好
  （提示词约定里本来就保留手工换行，是断言写得太长，不是提示词错了）。

### M3-07 真流式对话通道（三层，11 测试）

- **① 客户端 SSE（`llm/openai/stream.go`）**：`ChatCompletionStream` + `StreamingCompleter` 接口 + `ErrStreamInterrupted`；
  五项纪律——**空行整帧才 decode**（事件没闭合不碰 JSON）、**`data:` 多行拼接**、**tool_call 分片按 index 聚合**（id/name/arguments 各段
  拼成与非流式同形的 `ToolCall`——这是循环能零改动跑在流式轮上的关键）、**usage 显式索取**（`stream_options.include_usage`；末帧到达；
  不来则零不编造）、**`[DONE]` 缺失=中断**（部分内容随 assembled 返回供诊断，但错误说它不是回复）。5 测试：增量+迟达 usage+请求形状、
  中断、status 错误、消费者中止/坏分片/中途 error 信封、tool_call 分片聚合。
- **② 循环/引擎**：`ToolLoopRequest.OnDelta`（非空时才把传送输升级为 `StreamingCompleter`，否则报"cannot stream"；决策路径**恒 nil**——
  05 §1"决策永远非流式"由字段语义守住）；`ChatPipeline.RunStream`/`SendMessageStream` 与 `Run` 同体（`run(ctx,req,onDelta)`），
  **中断零助手行 + 预算释放**（"半截回复不是回复"）。4 测试：增量与落库文本一致、中断零落库、非流式传送输拒绝、消费者报错同样零落库。
- **③ proto+RPC**：`LLMChatStream`（server-streaming；`LLMChatStreamEvent{delta, final, error}`）+ handler（`chatResultToProto` 提取复用）；
  中断=gRPC 错误且**无 final 事件**（客户端看到半截文本后又看到错误——不是一段像完整回复的东西）。2 测试（假 ServerStream）。
- **幂等再验收**：追加端点后 `buf generate` 二次哈希 `9bb0bb2d…` 一致（第二次证明"纯增量"不是巧合）。
- **踩坑一处（编译期）**：循环里 `response, err` 原为 `:=` 声明——改双分支后 err 需显式声明；夹具函数参数从具体类型放宽为
  `ChatCompleter` 两处（`chatTestPipeline`/`chatTestService`）。

### M3-09 Web UI 消费流式（三层 + 浏览器实测）

- **① 代理流式直通**（`proxy/gct-proxy.mjs`）：上游 2xx 且**无 Content-Length**（=chunked 流式）→ 写 head 后边收边转；
  缓冲会把真流式变成"等完再一次性给"——那是对 M3-07 的嘲讽。另放开**会话本地数据写**：`llm/conversations*` 的 POST
  （创建/归档）默认放行（写的是本地会话表，不是交易）；extractpolicy/strategies/providers/activepair 等治理端点仍受
  `GCT_ALLOW_WRITES` 闸。计划端点注释同步更新（conversations/chatstream 已实现；其余 404→501 映射保留）。
- **② 前端分帧解析器**（`src/app/api/sse.ts`）：网关把每个 message 作为 JSON、换行分隔写入 chunked 响应；解析器
  **累积 + 按行 decode**（半行 JSON 不解析）——与 Go 侧同纪律。`scripts/check-sse-parser.mjs` 6 项自查：整段/逐字节切碎/
  `result` 包装/`error` 信封透传/坏分片报错/flush 尾行。**逐字节切碎喂入**是核心钉子：TCP 分块与消息边界无关。
- **③ 对话 UI 开工**（`api/chat.ts` + `pages/ChatPage.tsx` + 路由/导航「对话」）：会话选择/新建/归档、消息回放、
  `streamAssistantMessage` 增量渲染、证据卡片 Tag、中断/降级提示（流无 final=按中断展示，后端零半成品行）。
- **浏览器实测**：mock 网关（`scripts/mock-gateway.mjs`，https+chunked+180ms 间隔流）→ 代理 → vite 全栈；
  浏览器发送「BTC 现在的结构如何？」→ 增量文本逐段出现 → final 带证据卡片 Tag `getCandleWindow｜Binance:usdtmarginedfutures:BTC_USDT｜EXCHANGE_REST`；
  切换/回放持久；`typecheck`/`oxlint`/`build` 全过。
- **踩坑两处（都是环境/接线，非设计）**：a) 客户端一度把 URL 写成 `/api/v1/…`——网关 `/v1/` 前缀在代理 `GATEWAY_BASE` 里，
  浏览器侧不带；curl 直测代理把 bug 抓了出来。b) **上会话遗留进程占住 8788（旧代理）与 5173（旧 vite）**——旧代理无流式直通、
  旧前端无 /chat；按端口找 PID 重启新构才生效（排查记录：EADDRINUSE → lsof → kill → 重启）。

## 14. M4-01 策略条款提取（M4 开工）

- **契约层早已在位**（`llm/candidate/policy.go`，M0/M1 时代）：`PolicyProposal`/`RiskEnvelope`/`MarketScope`/`State()`/`ReviewablePolicy`。
  本任务补的是**提取执行器** `engine/llm_policy.go`：会话上下文（复用 M3-02 的 `BuildContext`——"从会话开始到所选消息的全部用户指令"正是同一契约）
  → 预算（`PROPOSAL`）→ 单次 `json_object` 调用（决策路径**不流式**、无工具）→ 严格 decode（未知字段/尾随数据即拒）→ Validate → State。
- **两条硬纪律的落点**：缺边界（null 限额）一律 `NEEDS_CLARIFICATION`——模型不提问也算没答（批准者不能批准一个操作者从没选择的数）；
  矛盾→模型按 prompt 返回 `clarifyingQuestions` 且矛盾值保持 null（`TestPolicyExtractionAsksInsteadOfChoosing` 钉住"不静默选值"）。
- **选择集是服务端闸**：prompt 的 "use only selected market IDs" 在服务端复述为 scope ⊆ 选择集（出集 `ErrExtractionMarketDenied`）——
  与工具作用域同构的"prompt 约束必须服务端复述"纪律。
- **契约漂移修复两处（真缺陷，会在联调时表现为"模型输出永远被拒"）**：① prompt 要 `maxNewRiskIntents`、结构体解码 `maxNewEntryIntents`——
  严格 decode 下必然失败；按 reviewable-v1 契约（12 对照表/08 §441）统一为 `MaxNewRiskIntents`（结构体+tag+测试 3 处）；
  ② prompt 未列 `marketType`，而 `MarketScope.Validate` 强制 `PERP`——模型永远给不出合法 scope；prompt 补 "marketType (always PERP)"。
  新增 `TestPolicyPromptListsEveryPolicyProposalKey`：reflect 出 `PolicyProposal`/`RiskEnvelope`/`MarketScope` 全 JSON 键 ⊆ prompt 逐字，
  加三个 Action 枚举——**键名漂移从此由机器看门**（07 文档样例里的 `maxNewEntryIntents` 是旧称，以代码契约为准）。
- **计费语义**：provider 答了就结算（内容非法也 `MarkUsed`——token 花了）；仅传输失败释放。**提取不持久化**：草案成为版本的唯一道路是冻结（M4-02）。

## 15. M4-02 策略版本冻结（reviewable-v1）

- `engine/llm_strategy_freeze.go`：`StrategyFreezer.Freeze(versionID, policy, actor)`——校验契约 → 读版本 → **前置状态闸** → 规范化序列化 → 双哈希 → 冻结入行 → 记状态迁移。
- **确定性序列化即哈希契约**：Go `encoding/json` 对结构体按键声明序、零空白、数字不重排；测试里 **golden 字面量**（完整规范 JSON 字符串）同时钉校验和哈希——
  任何字段顺序/标签改动都会让 golden 失败，逼一次有意识的迁移（重放哈希是本系统审计的根，不许悄悄变）。
- **双哈希**：`strategy_hash = SHA-256(canonical(frozen 版本))`、`risk_envelope_hash = SHA-256(canonical(risk envelope))`；后者配
  `RiskEnvelopeHashOf(frozenJSON)` 运行时重算助手——**运行时比对的两边各算各的**才是监督（07 §2.2 的另一半在 M4-11 接）。
- **前置状态闸**：存储版本 `ProposalState` 必须是 `READY_FOR_REVIEW`——带着未答澄清问题的版本不许铸成可批准版本（否则"澄清"就成了装饰）。
- **契约全检**在 `ReviewablePolicy.Validate`（10 真/假授权、≤7 天窗、永续必带杠杆、双边修订版、READY_FOR_APPROVAL）。
- **二次冻结拒**（服务预检 + DB 触发器纵深）；**二次冻结后 actor 不进哈希**（同策略同哈希——哈希是策略语义的，不是操作者的）。
- **审计行丢失要报错**：冻结已持久而 transition 写失败时返回带 "transition" 上下文的错——重试会命中 ErrAlreadyFrozen，运维能读出真实状态；吞掉它=审计链有洞。
- 踩坑一处（测试自身）：复用 fake 时 `freezer, _ = ...` 丢弃了新 fake 却把故障注入到旧 fake——失败点在"预期有错却得了 nil"，改 `freezer, fake = ...` 即好。

## 16. M4-03 版本树与 diff

- `engine/llm_strategy_tree.go`：`BuildVersionTree(versions)` —— 生命周期里"继续这版"是从同一父复制成新 DRAFT，所以**历史是一棵树**：
  分叉合法，子序按（创建时间，id）全序，输入乱序不影响输出。
- **腐坏模型**：悬空父链 = 数据损坏（调用方按策略取全集；缺父宁拒不糊）；重复 id 拒；跨策略混入拒。
- **环检测两态**（写测试时抓出的实现空洞）：纯环=无根（`len(roots)==0`）；**但环旁还有健康链时**"无根"判断失效——必须数
  walk 可达计数，达不到的版本就是骑在环上。初版只查无根会把旁路环悄悄丢掉，测试把这条路径逼了出来。
- `DiffPolicies(from, to)` —— 字段级、确定性：按结构体字段序输出（两次 diff 字节一致）；市场按 id 排序报增/删/动作集变化；
  money nil 渲染为空串（"移除"与"从未设置"对 LIVE 是同一种阻断，读法也同一种）；时间窗/PairEpoch/双边修订版全列。
- **只读视图**：树和 diff 都不改动任何版本——审阅 UI 的原料，不是状态机的一步。
- 踩坑两处（编译/夹具）：① `var walk func(...) error {` 不是合法 Go（var 声明不能直接带函数体）——恢复两行"声明再赋值"写法；
  ② 树夹具里父指针接错（v3/v4 都挂到了 v1，根下出现 2 个子树）——断言"根恰 1 个子"当场失败，修指针即好。

## 17. M4-04 逐市场授权（第二道闸 + 撤销级联）

- `engine/llm_market_grants.go`：`MarketGrantService.Grant`（窗口 1..30 天硬顶、**活体唯一**——同 account+network+market 至多一条有效授权；
  过期与已撤不挡新，测试三态）与 `Revoke`（**级联**到有效审批）。仓储补 `GetMarketTradeGrant` + `ListActiveStrategyApprovals`（双方言，
  `approvalFromPSQL` 从 `GetStrategyApprovalByVersion` 抽取复用），真库 e2e 一件。
- **级联的因果点**：审批是"授权在→批准才在"的下一环；撤授权先撤审批（reason 记 `market grant revoked: <market>`），
  运行面暂停与挂单处置按文档归属 M4-14/M5——本任务把住审批这一层。
- **四面见血**（测试钉住）：同账号、同网络、市场清单含该市场、未被并行撤的审批才级联；跨账号/跨网络/不含该市场一律不动。
- **坏市场清单炸错不跳过**：`ApprovedMarketIDs` 解析失败时宁拒——跳过可能留下"授权没了、批准还活着"的行。
- **编解码单一拼写**：`Encode/DecodeApprovedMarkets`（JSON 数组）是本仓库的唯一拼写，M4-05 写审批必须复用——写者与级联读者不许漂移。
- **二次撤销拒**：再撤一次会重写 `revoked_at/by/reason`，那是篡改审计行不是幂等。
- **真库验收抓出的事实**：`llm_strategy_approvals` 有 `UNIQUE(strategy_version_id)`——"一个版本一次审批"（07 §1 不变量）由 schema 把门；
  夹具初版三个审批共用一个版本 id 被当场拒绝，修夹具不改约束。

## 18. M4-05 逐版限时审批（8 要素 + 重新认证 + 双 epoch 闸）

- `engine/llm_approval.go`：`ApprovalService.Approve` —— **审批只是"接受"冻结信封**：`RiskEnvelopeJSON`/`RiskEnvelopeHash` 从冻结版本
  复制，审批层没有修改限额的输入面（07 §2.1 的关键不变量在这里成为类型事实）。
- **三件套齐发**：
  1. **8 要素显式**（08 §3.2）：版本/账号/网络/执行模式/市场清单/窗口 1..7 天/操作者密码/幂等键 + `expected_pair_epoch`。
  2. **重新认证先行**：空白密码直接 `ErrReAuthFailed`（不是可跳过字段）；验证器失败在任何持久化动作之前截断。密码校验通过
     `VerifyPassword` 窄缝注入——RPC 层（M4-15）接 RemoteControl 凭据，测试注入假验证器。
  3. **双 epoch 闸**：请求 `expected_pair_epoch` 必须等于当前 epoch（CAS，`ErrPairEpochConflict`）；**且冻结信封自身的
     `pairEpoch` 也必须等于当前**（`ErrStalePolicyPair`）——只查一个会放过"配对换掉之后才被审阅"的版本。
- **绑定校验**：账号/网络/模式与冻结信封一致；市场 ⊆ 冻结范围；**每个市场必须有活体授权**（`ErrMarketNotGranted`——
  第二道闸的因果闭环：没有授权就没有可交易市场，批准也就无从谈起）。
- **幂等=同绑定重放**：一版一审批（UNIQUE(strategy_version_id) 已由 schema 把门），重放判定比较
  账号/网络/模式/市场 JSON/风险哈希/epoch/**有效窗口**——窗口是"所接受内容"的一部分，不同窗口是不同审批（`ErrApprovalConflict`
  而不是静默替换）。踩坑一处（测试自纠）：初版谓词漏了窗口，冲突用例失败——改的是谓词与断言，不是放松约束。
- **审计行丢失要报错**：审批已落库而 `llm_audit_events` 写失败时返回带 "audit" 上下文的错。
- **版本状态保持 FROZEN**：审批行本身即 APPROVED 信号（每版唯一），单一事实源；避免为状态再动冻结行（触发器边界自觉）。

## 19. M4-06 触发调度器（每版本 1 次/分钟）

- `engine/llm_schedule.go`：`EvaluationScheduler` —— `Admit(version, market, triggerKind, at)` 回答「现在这一下能不能跑」；
  窗口内后续触发返回 `SKIPPED_RATE_LIMITED` + `RetryAfter`（调用方据此记账，**不消耗预算**——没跑的评估不占预约）。
- **跳过不延长窗口**：被跳过的触发不写 `last`——否则一串触发会靠礼貌把评估无限推迟（饥饿）；测试钉住 RetryAfter 递减到窗口在
  `first+1m` 重开。
- **按版本去抖不按市场**：一个版本在任意市场触发共享同一分钟窗（06 §9 的「每策略版本」），多市场事件不会放大评估额度。
- **有界性**：`Forget`（暂停/撤销/过期即清——旧节流不许跟着策略复活）与 `Prune`（超过一个窗口的条目不可能再节流谁）；
  内存按活跃版本数而非运行时长增长。
- **并发**：32 个同刻触发同版本，恰一人胜（trigger storm 纪律，mutex 保证）。
- **职责边界写进注释**：每修订版 24h 预约上限=M1-11 预算闸；全局共享流水线截止时间=M4-08；调度器只管去抖。
- 踩坑一处（测试自纠）：`Prune` 的「新窗口不被早剪」断言方向写反（期望 1 实得 0）——改断言为 `Zero`。

## 20. M4-07 前置门禁（七道零成本闸）

- `engine/llm_pregate.go`：`EvaluatePreGates(PreGateInput) PreGateVerdict` —— **纯函数**：事实全由调用方供给（模式/版本状态/窗/readiness/
  探针/halt/预算/对账），离线可钉顺序与判定；失败只回 reason（05 §9 词表常量），调用方记 decision_run 后退出，**不消耗模型预算**。
- **序即 03 §6 的序**：模式 → 策略状态 → 有效期窗 → 配对 readiness → 探针 → halt → 预算 → 对账。**多故障取文档序第一个**——同一事实集
  在任何时点重跑都得到同一条 reason（测试三组组合钉住），复盘时读到的就是读者最先撞上的那条。
- **fail-closed 三处**：READ_ONLY 与未知模式一律 `MODE_NOT_PERMITTED`；**未知 readiness 归 `PAIR_INCOMPLETE`**（不认识的 readiness
  不是 ready 的证据）；**TTL 未配置即 `PROBE_STALE`**（没有 TTL 什么都证明不了）+ 探针时间来自未来也算 stale。
- 策略状态三码分流：REVOKED/EXPIRED 各自成码，其余非 APPROVED 归 `STRATEGY_NOT_APPROVED`；窗未开始=未批准、窗已过=过期。
- 缺时钟 = 事实集不完整（`PREGATE_INPUT_INCOMPLETE`）：宁可拒也不拿零时间去比窗。
- 19 个单点失败子测试 + 4 个行为测试全过；engine 整套 86.2s 绿。

## 21. M4-08 LLM 决策分析流水线（组装层）

- `engine/llm_analysis.go`：`AnalysisPipeline.Run` 把已有构件按 05 §5 顺序装上同一根时钟：
  **前置门禁（M4-07，零成本先拒）→ 快照 → 预算预约（DECISION）→ 工具循环（M1-12）→ 候选契约闸 → 服务端 safety**。
- **快照不得决策即止**：`Freshness.MayDecide()` 为假、或决定性类目（candles/indicators/account）降级，直接
  `HOLD MARKET_SNAPSHOT_STALE` 且在预算之前——给模型付钱去论证一个已知不合格的事实集，只会得到一篇漂亮的有理有据的 HOLD。
- **双时钟**：`deadline = min(now+90s, snapshot.SourceTime+60s)`——自动候选的新鲜度窗（Q9=60s）就是快照的寿命；
  循环的轮次/工具/token/截止四闸复用 M1-12 原语。
- **候选契约的三次契约发现**（夹具照契约施工，不放松断言）：
  1. `StrategyVersionID` 与 `SnapshotID` 必须是 **UUID**（`Validate` 拒绝 "ver-1" 这类简写）；
  2. **`evidenceRefs[0]` 必须恰为 snapshotId**（工具证据 refs 从索引 1 起）——"refs 以 snapshotId 开头"在实现里是
     "首位就是 snapshot id"；
  3. 因此越界用例（跨快照）的 refs 必须自洽到它自己的 snapshot id，否则先在 schema 层被拒、到不了 scope 判定。
- **候选闸四连**：严格 decode（未知字段/尾随即拒）→ `Validate` → 三 ID 逐字对照（`CANDIDATE_SCOPE_DENIED`）→
  工具 ref 必须属于本回合转录（`CANDIDATE_EVIDENCE_UNKNOWN`）。**非 HOLD 硬要件**：引用 ≥1 条工具证据
  （只引 snapshot 不够——`CANDIDATE_TOOL_EVIDENCE_MISSING`）+ entry/exit 双非空（`CANDIDATE_INCOMPLETE`）。
- **safety（本层实现，码待并表）**：带风险候选的每条工具证据都必须是 `EXCHANGE_REST`，否则 `VerdictReview` +
  `REVIEW_EVIDENCE_QUALITY`（本地视图/缓存证据可以出现在候选里，但不能作为风险建议的唯一依据继续走）——**该码是 §9 词表
  的扩展，M4-10 分诊并表时统一**。
- **计费语义**：provider 从未到达（循环前失败/传输错）→ 释放；循环跑过 → 结算（模型干了活，候选过不过闸是另一回事）。
- 测试 7 个（含 7 子测试）：全绿；engine 整套 86.0s 绿。

## 22. M4-09 Jev 评估流水线（调用位 + 结束前重查）

- `engine/llm_jev.go`：`JevPipeline.Review` —— 06 §4/§5 的调用位。**复验不是本层重写**：§4.2 的九项检查早已内建在
  `jev.BuildReviewRequest`（M2-10 的证据闸 + M2-09 转录纪律的同一套），本层负责顺序与闸位——**复验与 64KiB 体积闸都在
  预算预约之前**（拒绝是免费的；judge 零调用、预算零占用），随后预约（JEV kind）→ 单次 `SystemOne` → 结算 → **结束前重查
  pair_epoch**。
- **STALE_PAIR 语义**：裁判作答期间配对被换掉 → 评估作废（`Assessment` 置空 + Detail "3 to 4"），不把它当本世界的裁决。
- **拒绝=outcome 而非 error**：`ReasonCode + Detail`（Detail 保留具体是哪一项检查拒的，如 "sensitive"）——失败是决策的事实，
  要能进 decision_run；error 留给基建故障（解析修订版/预算 DB/传输）。
- **证书**：`RequestSha256` 是送审字节的独立重算（测试用 judge 捕获的请求重算比对）——重放时能证明"当时发给裁判的就是这个"。
- **同源保真的最后一里**：测试把 judge 收到的 `State` 反解出来，与原证据 `Data` **逐字节**比对——M2-10 保证构造器不偷换，
  这里保证调用位不偷换。
- 6 测试全过；engine 整套 90.4s 绿。

## 23. M4-10 决策合成（分诊表：分歧取保守方、永不 ALLOW）

- `engine/llm_synthesis.go`：`Synthesize(in SynthesisInput, th SynthesisThresholds)` **纯函数**——06 §7 的伪代码逐行落表。
  输入是 M4-08 的 `AnalysisOutcome`、M4-09 的 `JevReviewOutcome` 与裁判阶段的 `error`：**阶段拒绝是 outcome+ReasonCode，
  基建故障才是 error**，两条通道合成层都收（`errors.Is` 词表三分类）。
- **顺序即文档序**：① 分析 HOLD/REVIEW 一律透传（安全闸转人工的候选**到不了裁判**，`REVIEW_EVIDENCE_QUALITY` 并表在此）；
  ② 候选 HOLD → `MODEL_HOLD`（§7 第 1 行在裁判行之前——持币候选可以不叫裁判，调度器可据此省一次调用，但表是权威）；
  ③ 分析师 REVIEW → `MODEL_REQUESTS_REVIEW`（文档只写了裁判的 review 行，这是对称扩展）；④ 阶段拒绝码透传
  （`STALE_PAIR`/`ANALYSIS_EVIDENCE_INVALID`/`REQUEST_TOO_LARGE`，Detail 保留）；⑤ `JEV_HOLD`；⑥ `JEV_REQUESTS_REVIEW`；
  ⑦ **§7.1 分歧矩阵**（OPEN_LONG×open_short/reduce 等 6 行全测 + 未知 choice 按保守读归分歧）；⑧ 证据分 <2.0
  （文档 01 的全程成功行是 Adequate → 门槛取量表顶）→ `JEV_EVIDENCE_INSUFFICIENT`；⑨ 对齐 <0.5 → `JEV_STRATEGY_MISALIGNMENT`；
  ⑩ **分离度 <0.10**（§7.2 唯一钉死的数）→ `JEV_LOW_SEPARATION`；⑪ 全一致 → `AWAITING_HARD_RISK_AND_APPROVAL`。
- **永不产出 ALLOW**：出口只有 HOLD/REVIEW；总括测试遍历 8 场景断言 verdict ∈ {HOLD, REVIEW} 且 reason 非空。
  ALLOW 只能由硬风控（M4-11）产出——职责隔离的落点。
- **阈值零值≠无门槛**：`SynthesisThresholds` 未设置/非正的字段回退文档默认——零阈值会读成「无门槛」，
  那是这个包里任何其他规则都不会同意的读法；显式阈值照常生效（测试双证）。
- `jev.Criteria*` 五常量导出（合成层与判官共用同一份字符串）+ `TestExportedCriteriaCoverTheOfferedQuestion`
  钉住「导出名恰好覆盖题目给出的 criteria」。
- 15 测试全过（含 12 个子测试）；`llm/jev` 新测试全过；engine 整套 103.0s 绿。

## 24. M4-11 硬风控引擎（61 码纯函数、ALLOW 唯一产地）

- `engine/llm_risk.go`：`Evaluate(in RiskInput) RiskResult` **纯函数零 I/O**（§5.1 四理由：可测试/可审计/不可污染/可重放）。
  A–G **61 码全部落表**（含 §5.6 开关专属 5 条与 3 条扩展码）：A 授权 10 · B 数据 6 · C 资金 9+2 · D 成交保护 10 · E 频率 5 · F 仓位委托 6 · G 系统 9。
  检查表按文档序执行，每项结果入 `Checks`（`{code, passed}`），拒绝带 `actual/allowed/unit/field`（§5.5"REJECT with which limit"）。
- **REDUCE 豁免新增风险检查**：平仓不消耗新增风险预算（§5.2 E）——C 类资金限额、新风险窗口、冷却、并发仓位、反向仓位检查对 REDUCE 不执行；
  但系统（halt/模式/数据库/时钟/配对）、数据、成交保护、审计类检查对任何订单都执行。理由写在注释里：**平仓通道不得被开仓限额堵死**，
  否则日损 halt 连止损也锁死。
- **UNKNOWN 的定位**：输入不完整（无候选/无时钟）→ `RISK_INPUT_INVALID`（fail-closed）；日损基线缺失或划转识别失败 → `RISK_DAILY_LOSS_UNKNOWN`。
  两者都是拒绝：缺事实集不等于零风险。
- **ALLOW 唯一产地**：全目录通过才 ALLOW；模型层与分诊层都没有 ALLOW 权限。`reviewGrade` 目前只含 `RISK_OPPOSING_POSITION`
  （单向持仓下反向开仓实为减仓——意图须人工确认）；硬型拒绝与 review 级共存时 REJECT 胜出。
- **数值口径**：十进制全程（`shopspring/decimal`，18 位精度取整）——敞口 = Σ max(entry, mark) × qty（保守名义额，多空同式），
  日损 = 基线 ± 划转 − 权益**（划转带符号）**，杠杆 = 前瞻敞口/权益，保证金 = 名义/杠杆 + 10bps 手续费缓冲（文档未钉数，注明是保守默认）。
  精度检查用 10^18 整数化后取模（tick/step 整除判定不碰浮点）。
- **§5.6 开关目录**：`RiskSwitches` 16 项，12 ON / 4 OFF 与文档一致并被测试逐个钉住；关一个开关只移除那一条检查（每条有独立 `enabled` 谓词），
  `requireProtectiveExit` 是唯一"开关 ∨ 审批字段"的析取门（关开关但 envelope 要求时仍执行——测试用 `staysExecuted` 注明）。
- **夹具两处自纠**：① C1/C2 原先在同一行内换码（missing/incomplete 从 Exceeded 行里发出），测试无法在 `Checks` 里定位——
  拆成 `RISK_ORDER_NOTIONAL_MISSING`、`RISK_ENVELOPE_INCOMPLETE` 两个独立行，**码↔行 1:1**；② 敞口用例算错（3.1+20=23.1 未超 25）——
  改夹具上限为 22 而不是放松断言。
- 17 测试全过（95 条 PASS 含子测试）；engine 整套 98.0s 绿。

## 25. M4-12 数量重算（九步取 min + Q13 容差 step）

- `engine/llm_sizing.go`：`ComputeQuantity(in SizingInput) SizingResult`——07 §5.3 的九步逐级取 min：
  ①候选上界↔审批上限（缺一 = `RISK_ORDER_NOTIONAL_MISSING`）→ ②剩余敞口（`maxTotalExposureUsd − 现有敞口`）→
  ③剩余日损额度经**止损距离反推**（`remainingLoss / stopDistance × refPrice`；无止损距离 = 损失上界不可证 = `RISK_DAILY_LOSS_UNKNOWN`）→
  ④参考价 = 盘口可成交均价（`Walk` 缝，生产接 orderbook movement；**不是最新成交价**）→ ⑤rawQty = cap/refPrice →
  ⑥floor 到 step **再减一个容差 step**（Q13 选项 A：0.02 → 0.0199，float64 转换只能变小不能变大）→
  ⑦最小数量/最小名义额 → ⑧实际名义额 → ⑨**用实际名义额重跑滑点**。
- **`Caps` 台账**（哪一级、卡到什么值）与拒绝共用硬风控词表（`RiskRejection` 同一结构）：一份词汇表贯穿风控与重算，
  decision_run 里不需要翻译。
- **两次 walk 的说明**：日损额度可能在第一次 walk 之后才把 cap 卡小，取整前**刷新参考价**（多一次 walk），
  保证 ⑤-⑧ 用的是"将要送出的规模"对应的价格；第 ⑨ 步是第三次 walk（实际名义额）——注释里写清了为什么是三次。
- **REDUCE 不走这条链**：九步全是"新增风险上限"，平仓数量归退出策略（07 §7）；误用返回 `RISK_INPUT_INVALID`。
- 9 测试全过（20 条 PASS 含子测试）；engine 整套 99.5s 绿。

## 26. M4-13 审计链（三张不可变表 + 写入失败中止整链）

- `engine/llm_decision_record.go`：`DecisionRecorder.Record`——一次决策按序落三张表：
  `llm_decision_runs` →（有硬风控才落）`llm_risk_evaluations` → 一条 `llm_audit_events`（kind `DECISION_RECORDED`，subject=run id）。
  **任何一步失败都中止整链并返回 error**：decision_run 是「为什么交易」的唯一答源，不能落库的决策不许继续走向下单。
- **决策映射**（`decisionAndReason`）：有硬风控则其 verdict 覆盖分诊层（这正是风控引擎存在的意义），reason 取**第一条拒绝码**
  （与评估器报告的稳定第一条一致）；ALLOW 时 reason 保留上游闸码——记录是哪个上游闸把运行推进到了风控。
- **`input_hash`**：版本化前缀 + 九个字段的规范化 `key=value\n` 拼接（策略/快照/双修订版/pair_epoch/envelope/候选与裁决的
  **原文 SHA-256**）再取 SHA-256。测试钉住两点：同输入同哈希（重放对齐）、改任一输入必变（epoch、候选文本、快照 id 各一例）。
- **`AnalysisOutcome` 增两个分列字段**（`UsageInputTokens/OutputOutputTokens`——记账列 `usage_input_tokens`/`usage_output_tokens`
  是 NOT NULL，累计 Tokens 无法事后拆分；ToolLoopResult 早已有分列，管道顺路带上）。既有测试不受影响（无整结构体断言）。
- 窄缝三接口 + `LLMDecisionStore` 适配器（与 `LLMApprovals` 同一模式）；`TargetAccountID` 等调用方事实照旧由输入携带。
- 8 测试全过（11 条 PASS 含子测试）；`llm/...` 十一包全绿；engine 整套 108.2s 绿。

## 27. M4-14 配对切换失效语义（级联：撤审批 + 停策略 + 审计）

- `engine/llm_pair_switch.go`：`PairSwitchCascade.Apply`——05 §2.4 切换语义的后半段（前半段是 M1-10 的 CAS：
  清空指针 + epoch 递增，已含幂等键与操作者）。级联对每个**仍有效**的审批：撤销（reason=`PAIR_SWITCHED`、操作者、注入时钟）
  → 版本转 `PAUSED` → 留一条 `FROZEN→PAUSED` 状态迁移行；最后写一条 `PAIR_SWITCHED` 审计事件（subject=`pair-epoch-N`，
  detail 含 from/to epoch、两个计数、以及 STALE_PAIR 的作废说明）。
- **零活动审批也写事件**：epoch 前进了，事件流必须看得见——「什么都没发生」这句话本身必须是可审计的事实。
- **失败中止且不写审计**：级联中途失败不许声称完成（测试覆盖 列审批失败 / 迁移写失败 两处）。
- **无 STALE_PAIR 新表**：进行中的运行由各自的 **epoch 重查**自我作废（M4-09 结束前重查、M4-11 `RISK_PAIR_EPOCH_MISMATCH`）；
  不可变行无法也无需加标。订单意图的 `pair_epoch` 列在提交前比对（M5 落地时接）。级联的计数返回给调用方随切换命令行落账
  （`PairSwitchCommand.AffectedStrategies/RevokedApprovals` 两列的填充点在 RPC 组装流里——M1-14/M4-15 接线时用之）。
- 4 测试全过；engine 整套 99.9s 绿。

## 28. M4-16 SHADOW 组装位（全链串接：门禁 → 分析 → Jev → 合成 → 风控 → 落账）

- `engine/llm_shadow.go`：`ShadowRunner.RunOnce`——把 M4 的每一段构件按 07 的文档序串成一次决策：
  ① 调度去抖（M4-06，**跳过不落账**——什么都没发生，下一个被准入的运行才值得审计；skip 不延长窗口的语义在调度器里）→
  ② 前置门禁（M4-07，**零成本且发生在付费之前——拒绝也是决策，理由码随 decision_run 落账**，「为什么今天没跑这个市场」必须可回答）→
  ③ 分析流水线（M4-08）→ ④ Jev（仅当候选推进；`BuildJev` 的基建故障走 JevError 通道，由分诊映射 `JEV_UNAVAILABLE`）→
  ⑤ 决策合成（M4-10）→ ⑥ 仅在 `AWAITING_HARD_RISK_AND_APPROVAL` 位运行硬风控（M4-11）+ 数量重算（M4-12）→ ⑦ 审计链（M4-13）。
- **SHADOW 保证是结构事实**：`ShadowRunner` **没有订单提交接缝**——不是开关拨到「关」，是根本没有「开」的位置。
  ALLOW 在这里的含义是「若在可执行模式下，这一单会走到订单落地」（M5 才有执行）。测试断言全链 ALLOW 时：
  decision_run=ALLOW、risk_evaluations 含重算数量 0.0199、审计事件一条、**没有任何订单副作用可产生**。
- 测试：门禁拒绝+去抖（第二次同版本立即调用 → Skipped & RetryAfter>0 & 不落账）、分析 HOLD（stale 快照不叫裁判）、
  全链 ALLOW（真实分析流水线夹具 + 假裁判 + 默认风险夹具 + 尺寸夹具）、Jev 构建失败（HOLD/JEV_UNAVAILABLE/judge 零调用）、误用。
- 5 测试全过；engine 整套 99.7s 绿。

## 29. M4-15 strategies/decisions RPC + gctcli（M4 组收官的 proto 大件）

- **proto**：`gctrpc/rpc.proto` 增补 30 消息 / 16 RPC（§3.2 九条策略与审批 + §3.5 七条决策与审计），含 google.api.http 注解；
  `cd gctrpc && buf generate` 连跑两次，`git diff gctrpc | shasum` 两次同为 `52eb7d4587866e187e92cd6737fc5b001aab8df5`（幂等）。
  **纪律**：生成接口要求全方法实现——proto 与 handlers 必须原子落地（中间态编译即断，注册处就是契约）。
- **handlers**（`engine/rpcserver_llm_strategy.go`，16 条）：**读写同源**——freeze/approve/pause/resume/revoke/extract 全部经内部流水线
  的同一服务（`StrategyFreezer`/`ApprovalService`/`PolicyExtractor` 由 chat 服务的接缝现场装配，操作员命令与自动运行不会各修一套闸）；
  读路径直连仓储（runs / risk evals / audit）。**审批重认证** = `RemoteControl.Password` 常时比较（与 RPC 层自身同一口径）；
  **resume 需活审批**（新增哨兵 `ErrApprovalRequired`——被切换级联撤销的版本必须重新审批，静默复活=绕过治理）；
  **trigger/preview fail-closed**（`errLLMDecisionRunner`——决策运行器的生产装配未到位前拒绝执行，沿用 chat RPC 的约定）；
  freeze 冻结的是**操作员审阅的 reviewable-v1 原文**（请求携带 policy_json）；lineage = run + 风控证据 + `DECISION_RECORDED` 审计事件（A12/A13 复盘面）。
- **gctcli**：`cmd/gctcli/llm_strategy.go` 注册 `llm` 命令族——`strategies`（list/get/extract/freeze/diff/approve/revoke/pause/resume 9 命令）、
  `decisions`（list/get/lineage/risk/trigger/preview 6 命令）、`audit list`；approve 支持 `--password-stdin`；全部输出原始 JSON（脚本与人工同一字节）。
- 4 测试（fail-closed 三径 + 11 条输入校验 + 重认证 + 转换器）；`go test ./cmd/gctcli/` 全过；`go build ./...` 通过。
- **M4 组至此 16/16 完成**：提取→冻结→树/diff→授权→审批→调度→门禁→分析→Jev→合成→风控→重算→审计→切换级联→SHADOW 组装→RPC/CLI。

## 30. M5-01 订单意图（不可变凭据 + 三重 ALLOW 把关）

- `engine/llm_execution.go`：`BuildIntent`（纯函数）+ `IntentRecorder`（落库+审计）。**三重 ALLOW 把关**：风控 verdict=ALLOW、
  重算 verdict=ALLOW、动作 ∈ {OPEN_LONG, OPEN_SHORT, REDUCE}——任何一重不是 ALLOW 都拒绝生成。**先有凭据再有执行**：
  intent 绑定 decision_run / 审批 / envelope 哈希 / pair_epoch，「当时批准的到底是什么」由它回答。
- **方向映射只用 Buy/Sell**（币安 wrapper 只认二者）：OPEN_LONG→BUY、OPEN_SHORT→SELL；**REDUCE 必须给出被平仓位方向**
  （LONG→SELL、SHORT→BUY）且恒 `reduceOnly=true`——单向持仓下「平多」与「开空」是两件不同的事。
- **限价只从盘口推导**（§5.4）：suggestedEntry 按 tick **保守取整**（买 floor：不高于审阅价；卖 ceil：不低于审阅价），
  再界于 `bestBid×(1−slip)` / `bestAsk×(1+slip)` 内；越界=`RISK_PRICE_OUT_OF_BOUNDS`。MARKET 无价（币安拒收）。
- **幂等键 namespace**：`gct-llm-` 前缀——裸键自动补前缀，全键重试幂等；有效期默认 10s、上限 2min（操作者可给更长但不越顶）。
- **落库语义**：intent → `ORDER_INTENT_PREPARED` 审计；任一步失败中止（写入失败不留审计、审计失败上抛）——
  不能落库的意图不许流向执行，与 M4-13 决策链同一纪律。
- 8 测试全过；engine 整套 100.3s 绿。

## 31. M5-03 order.Submit 映射（三铁律 + Q13 回读重验）

- `engine/llm_order_map.go`：`BuildOrderSubmit(intent) (*order.Submit, error)` **纯函数**——不查库不下单，
  只把不可变意图翻译成 gocryptotrader 的下单结构。**三条容易忘记又必须记住的规则钉在这里**：
  ① **Side 只用 Buy/Sell**（币安 wrapper 不认 order.Long/Short）；② **Leverage=0**（非 0 直接 `ErrSubmitLeverageNotSupported`——
  杠杆在账户层设定，不进单笔提交）；③ **Q13 转换回读**（decimal→float64→最短表示回读重验，超过审阅值即拒——
  M4-12 的容差 step 只保证方向，回读才是证明）。
- **STOP_MARKET 的价位进 `TriggerPrice`**（止损位是触发价，不是限价；`Price` 留零）；LIMIT 必须带价（缺价拒）；
  MARKET 无价。资产/交易对从字符串严格解析（`BTC_USDT`→base/quote，`usdtmarginedfutures`→`asset.New` 白名单校验）。
- **`RetrieveFees=true` + 500ms 延迟**（回测器同款：wrapper 成交回执不总带 fee）；`gct-llm-` 幂等键直通 venue。
- 5 测试全过；engine 整套 100.8s 绿。

## 32. M5-02 执行分流（白名单 + 疑罪从有）

- `engine/llm_dispatch.go`：`OrderDispatcher.Dispatch`——07 §6.2 的四行 switch 落成**白名单**：
  READ_ONLY/SHADOW **不执行**（生命周期原封不动——shadow 的结构承诺在这个最可能被打破的地方再写一遍）；
  PAPER → `SubmitFakeOrder(submit, nil, true)`（**开校验**，与回测器同一条路径）；TESTNET/LIVE → `Submit`。
  模式不认识=拒、只有 `PREPARED` 能派发。
- **失败=UNKNOWN 而非 REJECTED**（疑罪从有）：结果不明的提交先落 UNKNOWN 再上抛错误——绝不当作已拒绝、绝不盲重试。
  完整状态机（SUBMITTING 认领、RESTING/FILLED、RECONCILIATION_REQUIRED 标记）归 M5-05；本层只保证「不明=不重试」。
- **回执缺详情=UNKNOWN**：没有内部/交易所订单号的成功不是成功。成功落 `ACKED`（两个订单号）+ `ORDER_SUBMITTED` 审计
  （含 clientOrderId/quantity/mode）；生命周期写失败上抛不吞——审计链的纪律（不能落库不许继续）一以贯之。
- 适配器：`LLMOrderManager{Manager}`（同一点同时接 Submit 与 SubmitFakeOrder）、`LLMIntentLifecycle`（仓储直通）。
- 6 测试全过；engine 整套 106.3s 绿。

## 33. M5-04 提交前新鲜度（陈旧授权先去死）

- `engine/llm_dispatch.go` 增补：派发前两道闸——**自身 `ExpiresAt` 已过**或**`CreatedAt` 距今 > 5s**（`IntentSubmitMaxAge`）
  一律先落 `EXPIRED` 生命周期再上抛 `ErrIntentExpired`。**venue 零调用、无 `ORDER_SUBMITTED` 审计**：
  陈旧的授权不是场地故障，绝不许长得像；对已移动的盘口执行旧授权等于拿旧地图走新路，重推（re-derive）才是出路。
- **边界=5s 整放行**（是天花板不是余量，测试钉住）；**SHADOW/READ_ONLY 先短路**——不执行模式下旧意图保持 `PREPARED`，
  不误标 `EXPIRED`（不执行的模式不该消耗授权状态）。
- 7 测试全过（含新鲜度 5 例）；engine 整套 101.2s 绿。

## 34. M5-05 订单状态机 + 原子认领（永不重试是结构性事实）

- **迁移表**（`engine/llm_dispatch.go` `intentTransitions`，10 态）：PREPARED→{SUBMITTING, EXPIRED, REJECTED}；
  SUBMITTING→{ACKED, REJECTED, UNKNOWN}；ACKED→{RESTING, PARTIALLY_FILLED, FILLED, CANCELED, UNKNOWN}；
  RESTING→{PARTIALLY_FILLED, FILLED, CANCELED, UNKNOWN}；PARTIALLY_FILLED→{FILLED, CANCELED, UNKNOWN}；
  **UNKNOWN/REJECTED/EXPIRED/FILLED/CANCELED 为终态、无出边**。派发前走表校验——跳步（PREPARED→ACKED）被拒。
- **永不重试不是约定是结构**：表里根本不存在 UNKNOWN→SUBMITTING 这条边；测试把 9 条拒绝迁移逐个钉住。
  UNKNOWN 的善后（RECONCILIATION_REQUIRED 账户标记与自动执行暂停）归 M5-08/M5-15（崩溃恢复扫描 SUBMITTING→UNKNOWN→需对账）。
- **原子认领**（`ClaimOrderIntent`，条件 `UPDATE ... WHERE status='PREPARED'`）：夺标成功者的认领本身即 SUBMITTING 迁移；
  丢标（`ErrIntentClaimLost`）=venue 零调用、生命周期归夺标者。双发与盲重试两个事故共用这一道闸。
  认领在 `BuildOrderSubmit` 之后、venue 之前——纯校验失败让意图干净地留在 PREPARED。
- 3 测试全过（迁移表 23 例 + 丢标 + UNKNOWN 拒重提）；engine 整套 100.9s 绿。

## 35. M5-06 保护性退出（挂不上止损就平仓，绝不留裸仓）

- `engine/llm_protective_exit.go`：两个纯函数 + 一个执行器。**止损距离（Q8b=取较紧者）**：`max(ATR×1.5, entry×2%)`
  ——低波动时不被固定百分比放得太宽、高波动时不被 ATR 放得太宽；随后**按 tick 向远离市场侧取整**（多 floor/空 ceil），
  取整绝不把止损挤进市场（盘口噪声不该触发一次本可避免的止损）。止损价 ≤0 直接拒。
- **挂单构造**：STOP_MARKET + `ReduceOnly=true` + 反向 Side（保护多仓=卖出）+ TriggerPrice（复用映射层同款字段口径）；
  数量=被保护仓位数量（缺省取父意图数量）。行落 `llm_protective_exits`（PLACED/FAIL_SAFE/UNKNOWN + 订单号）+ `PROTECTIVE_EXIT_PLACED` 审计。
- **fail-safe 两连（§7 第 5 步，关键）**：挂止损失败 → **立即市价平掉刚开的仓**（ReduceOnly、无触发价、ClientOrderID 加 `-fs` 后缀），
  行标 `FAIL_SAFE` + `PROTECTIVE_EXIT_FAIL_SAFE` 审计；**fail-safe 也失败=UNKNOWN 且上抛**——这套系统里最响的状态，
  绝不许静默（测试钉住：更新行 UNKNOWN、无审计、错误上抛）。「有仓位没止损」是不允许存在的状态。
- 5 测试全过；engine 整套 103.0s 绿。

## 36. M5-07 退出计划生命周期（重启先读账，平仓即撤单）

- `database/repository/llm/order.go` **+`ListOpenProtectiveExits`**（status IN PREPARED/PLACED，双方言）——重启恢复的读：
  重启后进程先知道「哪些止损还该挂着」。字段名修正两处教训：sqlboiler 模型列名是 `ParentIntentID/ClientOrderID/...`（大写 D 结尾），
  psql 的 `FailSafeTriggered` 是 bool 而 sqlite 是 int——照抄同文件既有映射才不掉坑。
- `engine/llm_exit_lifecycle.go`：`ExitTracker` 三件事——① `Snapshot`（跨重启快照）；② `ApplyVenueStatus`（venue 状态→行状态，
  经 6 态迁移表：挂单中状态不重写、成交/CANCEL 落库、**不可分类=SOMETHING_NEW 归 UNKNOWN——绝不读作已结算**、
  **终态再动=拒**、**UNKNOWN 在此终态**——消解它是对账动作不是同步动作）；③ `RetireForClosedParent`（仓位平了撤止损：
  只撤该父意图的 PLACED；撤单失败=UNKNOWN 上抛——**已不存在仓位上方的止损是一张会反手开仓的单**）。
- `order.Status` 是 uint32 + Stringer——`string(order.New)` 会被 vet 拒（single-rune 陷阱），用 `.String()`。
- venue 抓取与撤单的 I/O 接线（OrderManager.GetOrderInfo/Cancel 映射）随运行时监督者装配；本层拥有状态语义与落库。
- 4 测试全过；engine 整套 106.4s 绿。

## 37. M5-08 对账（数值等价比对；差异即 SOFT halt）

- `engine/llm_reconcile.go`：`Reconciler.Pass` + `DiffPositions` 纯函数。**数值等价比对**：0.1=0.10（decimal 比较）、
  缺边读作 0（我们有的、交易所没有=交易所读 0——「幽灵仓位」正是要抓的）、**不可读数字绝不算匹配**
  （解析失败按原文比——读不懂的账只能放大差异，不能假装一致）；差异按 market+side 排序（重放读同一序列）。
- **清账分支**：`CLEAN` + `reconciled=true` + `validUntil=now+5min`——§9 的「有效期到下次对账」落成字段；
  **差异分支**：`MISMATCH` + deltas JSON + **SOFT halt**（`RECONCILE_MISMATCH` 理由）+ `RECONCILE_MISMATCH` 审计。
  三重落账任一步失败中止（不许出现写了差异没 halt、或 halt 了没审计的状态）。
- `accountReconciled` 是 M4-11 硬门禁（`RISK_ACCOUNT_NOT_RECONCILED` 拒一切新开仓）的输入——
  §9 明确这是底线"成交与仓位以交易所查询和对账为准"的直接落地。交易所侧抓取（GetOrders /
  UAccountTradesHistory / UPositionsInfoV2 / **强平单**）是调用方 I/O，本层拥有比对语义与落账。
- 4 测试全过；engine 整套 107.5s 绿（**一次 98.7s 失败=已知网络类抖动**（README §4.3 的两类测试纪律）；重跑绿——本次记录的即重跑结果）。

## 38. M5-09 UTC 日界基线（一日一封；缺失即拒）

- `engine/llm_day_start.go`：`DayStartService` 四件事——① `Seal`（00:00 权益/余额/未实现盈亏/仓位快照落 `llm_day_start_inventory`，
  **同日二次落基线=拒**：重写基线=悄悄挪掉日损阈值的零点；落库 + `DAY_START_SEALED` 审计）；② `RecordFlow`（外部划转校正：
  IN/OUT 方向校验、金额正数、按 observedAt 归日）；③ `DailyLoss`（**基线 ± 划转 − 当前权益**；盈利读负值；**基线缺失或
  划转不可读一律 `ErrDayStartMissing`**——算不出来的额度等于没有额度，调用方转成风险输入的 nil 后 M4-11 拒开仓）；
  ④ `TripDailyLossHalt`（越限=**FULL halt** + `HALT_SET` 审计——Q8c 人工解除，本层不清除）。
- **坑**：盈利日损失为负——`decParse` 拒负值，判定处改用 `decimal.NewFromString`（带符号）；测试自纠一处（划转净额
  +30 忘了算，「权益 990」实际是 40 的损失，改成 1015 得 15 才是不越限例——改夹具不改逻辑）。
- 5 测试全过；engine 整套 92.7s 绿。

## 39. M5-10 HaltController（三级停机；恢复永远显式）

- `engine/llm_halt.go`：`HaltController.Set`/`Clear` + 适配器。**三级语义**：
  SOFT（停新仓、停运行中策略，**但不撤挂单**——保护性止损必须能活过 soft stop）、
  MARKET（需带市场，撤机器人单，允许平仓）、FULL（拦一切模型驱动动作；确定性保护性止损仍可运行——它不依赖模型）。
- **触发序列**（§8）：落 halt 行（持久化即立即生效）→ 停运行中策略 → 撤单（SOFT 除外）→（**FULL 且显式
  二次确认**）reduce-only 平掉全部仓位 → `HALT_SET` 审计（含三计数）；任一步失败中止。
  **只撤机器人单**：`gct-llm-` 前缀由运行时适配器过滤——用户同时在币安 App 手下的单不可被撤。
  FULL 平仓无确认即拒（`ErrHaltConfirmationRequired`，二次确认不是形式）。
- **un-halt 显式且需认证**：密码重认证（复用与审批同一 VerifyPassword 签名）+ 原因必录 + `HALT_CLEARED` 审计；
  **清除后不自动恢复**——策略逐个重新启用、已撤审批重新审批（测试钉住：清除不碰策略、不碰订单）。
  审批撤销（§8 第 3 步"可选"）留给调用方显式执行（PairSwitchCascade 或 LLMRevokeApproval）。
- 5 测试全过；engine 整套 102.1s 绿。

## 40. M5-11 事件通知（15 事件成文；两处响铃；永不拦执行）

- `engine/llm_events.go`：15 个事件常量（08 §5 表全落：决策三态、订单四态、配对/探针/预算、halt 两态、对账差异、审批将过期、止损失败、数据过期）
  - `LLMNotifier` 每个事件一个成文方法——**每个事件都回答同一个问题："系统刚做了什么、我该看哪里"**：
  allow 带策略/市场/动作/数量/名义额/理由；reject 带首个拒绝码 + actual-vs-allowed（全集入 detail JSON）；
  submitted/filled 带订单号/ClientOrderID 与成交量/均价/手续费；pair 带新 epoch + 受影响策略数；probe 带 profile/层/错误码；
  reconcile 带差异全集；expiring 带剩余分钟；stale 带事实与年龄。
- **两处响铃**按文档钉等级：`llm_order_unknown`=high（账户已标记需对账）、`llm_protective_exit_failed`=critical
  （已触发 fail-safe 平仓）；halt 设置=high。测试逐字断言 `[high]`/`[critical]` 前缀。
- **Emit 永不失败、永不阻塞**：nil 推（comms 未装配/未运行）静默丢弃——与 `PushEvent` 自身「未运行即丢」同语义；
  一个 Telegram 故障不该拦下一笔已经过闸的止损。`requireConfirmation=false`（Q19）：批准/撤销/清 halt 必须回
  Web UI/gctcli 重新认证——聊天软件里没有一键完成。**webhook 通道包是 M5-16 的独立交付**（本层只到「推送」边）。
- 4 测试全过（15 事件全可达断言 + 两处响铃 + 消息字段）；engine 整套 101.4s 绿。

## 41. M5-12 运行模式切换（§5.1 矩阵 fail-closed；LIVE 三重解锁）

- `engine/llm_mode.go`：三块。① **`ModeAllows` 矩阵纯函数**（10 §5.1 八行逐格）：对话分析与手动预览恒可达、
  急停恒可达；SHADOW 无纸面单/无真实单/无保护性下单、READ_ONLY 无自动执行；PAPER 的止损是纸面；TESTNET 即真实 I/O
  （`Submit` 打到测试网）。**未知模式或操作一律否**——一个不认识的名字没有「默认允许」的读法。
  ② **`UnlockLive` 三重闸**（§5.2）：配置承认 `I_UNDERSTAND` 精确匹配 + 审批绑定
  `execution_mode==LIVE 且 network==binance-mainnet` + 信封金额全设；任一缺= `ErrLiveNotUnlocked`
  （映射 `RISK_LIVE_NOT_UNLOCKED`）。**null 金额阻止 LIVE 授权不是「无限制」**——与 M4-11 的行事一脉相承。
  ③ **`ModeSwitcher.Switch`**：每次切换都要重认证（TESTNET→LIVE 是最高危跳变，不加轻），LIVE 目标须先过三重解锁；
  落 `MODE_SWITCHED` 审计 + 推 `llm_mode_switched`（§5.2 明确要求切换推通知；08 §5 的 15 事件表无此行，
  作为文档化扩展引入）。新模式的落定交给调用方（配置/引擎字段）——本层保证「这次切换是有意的且留了痕」。
- 测试夹具一处契约发现：`RiskEnvelope` 字段名是 `MaxNewRiskIntents`（M4-10 改名后的拼写），非 `MaxNewEntryIntents`。
- 3 测试全过；engine 整套 94.164s 绿。

## 42. M5-13 网络一致性（错网下单被挡在下单之前）

- `engine/llm_network_check.go`：`CheckNetworkConsistency` 纯函数——10 §4.2 的三方一致（runMode ↔ endpoint ↔
  approval.network）两向钉住：LIVE⇒`fapi.binance.com`∧`binance-mainnet`；TESTNET⇒`testnet.binancefuture.com`∧
  `binance-testnet`；大小写/空白归一。**https 强制**（基础层 `SetAPIURL()` 只告警，风控层直接拒）；
  **非交易模式豁免**（READ_ONLY/SHADOW/PAPER 没有场地下单路径——豁免不是放水）；未知模式 fail-closed。
- 八条错配路径全拒（`ErrNetworkMismatch` → `RISK_NETWORK_MISMATCH`）：主网端点点测试网、测试网端点点主网、
  审批网络反向错配、外来主机（api.binance.com）、http、垃圾 URL、未知模式。**这条的动机写在测试注释里**：
  错网下单不是配置瑕疵，是「要么下单失败、要么在主网上真下单」的事故。
- 3 测试全过；engine 整套 101.054s 绿。

## 43. M5-14 可观测性（A+C：日志 seam + LLMGetMetrics RPC）

- `engine/llm_metrics.go`：`LLMMetrics` 并发安全注册表——29 个指标名常量（10 §6.1 表逐行落名，拼错=编译错）、
  标签键排序折叠（同名同标签永不裂成两条序列）、counter/gauge/histogram 三种数学（histogram 存 sum/count/max，
  完整分位留给外部采集）、`Snapshot`/`SnapshotJSON`/`LogSnapshot`（A：JSON 快照经 `Log` seam 出结构化日志——
  挂到 `logging.structuredLogging` 由采集层接续）、语义记账器（`RecordDecision` 双计 verdict+reason——
  文档点名 reason 是最重要的诊断指标；`RecordTokens` 按方向分；model_drift/reconcile_mismatch/db_write_error/
  protective_exit_failure 四个立即告警类各一记账函数）。**告警阈值不在这里判定**（产出数值，判定归采集层）。
- proto：`LLMGetMetricsRequest/Response` + `LLMGetMetrics`（GET /v1/llm/metrics），buf 连跑两次同哈希
  `09119dc3f2dae60009fa08bdf8256f22138c3fd2`；handler 读 `Engine.llmMetrics`，**未装配≠空快照**
  （`errLLMMetricsUnavailable`，与 chat RPC 同一 fail-closed 约定；Engine 字段与运行时装配在 client factory 收尾时一并落位）。
- 测试自纠一处：快照断言里把 gauge 写成了 `Add`（counter）——改用 `Set`，数学与断言同源。
- 4 测试全过；`cmd/gctcli` 全过；engine 整套 99.717s 绿。

## 44. M5-15 崩溃恢复（留下的认领必然是没结果）

- repo **+`ListOrderIntentsByStatus`**（从 `ListOrderIntentsByDecisionRun` 克隆换 where——字段名直接复用唯一定义处，绕开 sqlboiler 拼写坑）；
  `engine/llm_recovery.go`：`CrashRecovery.Recover`。**核心推论**：`SUBMITTING` 只有一个写入者（M5-05 的原子认领），
  所以进程重启后还停在那里**必然**是「认领了但没等到结果」——UNKNOWN（不盲重试）+ `ReconciliationRequired`。
  过期 PREPARED 顺手清成 EXPIRED（新鲜的不动——可能在途或待派发）；每个 UNKNOWN 响一次 high 铃 +
  `unknown_orders` gauge 记数；`CRASH_RECOVERY` 审计**每轮都写**（干净的重启也留痕）；失败中止不写审计。
- 启动接线（进程 boot 时调用 `Recover`）随运行时装配（client factory/HaltController 同一批）；M6-06 用 kill -9 演练。
- 3 测试全过；engine 整套 99.283s 绿。

## 45. M5-16 webhook 通道（上游缺口的补写）

- `communications/webhook/`：`Webhook` 实现 `base.ICommunicate` 全接口（Setup/Connect/PushEvent/IsEnabled/IsConnected/GetName），
  与 Telegram 同形接入 `NewComm` 注册表；`base.CommunicationsConfig` +`WebhookConfig{enabled,url,secret}`（`IsAnyEnabled` 同步更新）。
  **上游只有 Telegram/SMTP/Slack/SMSGlobal——通用 webhook 是上游缺口**，Q19 定案"Telegram 主 + 通用 webhook 接自建告警系统"。
- **载荷拆解**（`ParseMessage`）：把通知层的 `[severity] summary | detail` 反解回结构化 `{type,severity,summary,detail,pushedAt}`——
  接收端拿到的是数据不是一句人话（自建告警系统需要可编程字段）。无标记的消息退化为 summary=whole。
- **重试=3 次指数退避固定政策**（250ms 起翻倍，测试逐档钉住 sleeps 序列；只把 URL/secret 留给配置——告警通道的可靠性
  不许被配置调成"不重试"）；非 2xx=失败、耗尽后上抛最后错误（comms 包装层记日志）；secret 经 `X-Webhook-Secret` 头；
  http(s) 校验；disabled/nil 全安全。
- 4 测试全过（httptest 捕获 body 与头）；`go test ./communications/...` 六包全绿（存量通道零回归）；`go build ./...` 通过。

## 46. M5-17 硬风控开关目录（18 行编译期清单 + 三把锁）

- `engine/llm_risk_switches.go`：`RiskSwitchCatalog` **18 行**（07 §5.6 逐行：名字/默认值/理由码/锁位）、
  `RiskSwitchService`（并发安全现值 + `Current()` 读出 16 个引擎位——对接 M4-11 的 `RiskSwitches`）、`CatalogJSON`/`SortedNames`。
- **三把锁**：`haltOnDailyLoss`、`blockOnUnknownOrders`、`blockOnClockDrift` **不可 RPC 关闭**（`ErrRiskSwitchLocked`
  ——「可随时开关」的目录里必须留着不可关的：底线 4/5 的直接表达，只能改配置重启）；测试把「恰好这三把」钉死。
- **翻转即审计**：每次 `Set` 写 `CONFIG_CHANGED`（switch/from/to/actor）；**审计失败回滚**——无审计的开关变更不许生效。
- proto `LLMSetRiskSwitch`（buf 幂等 `f3ecb7ad73006d89a04b6b8a72f320b12db2971d` 两次）+ handler（`Engine.llmRiskSwitches`，fail-closed）。
- 5 测试全过；engine 整套 94.5s 绿。

## 47. M5-18 REVIEW 自动过期（12h 不执行；宽限窗幂等）

- repo **+`ListReviewRunsBefore`**（decision=REVIEW ∧ created_at < cutoff ∧ > since）；`engine/llm_review_expiry.go`：`ReviewExpirySweeper`。
  **12h 默认线**（`ReviewExpireAfter`，操作者可收紧——测试用 2h）。**EXPIRED 不执行**：`decision_runs` 不可变，「转 EXPIRED」落成
  `REVIEW_EXPIRED` 审计 + `llm_review_expired` 推送（事件类型由 §7 第 21 行命名，与 `llm_mode_switched` 同一文档化扩展模式），
  detail 明写 "EXPIRED, not executed"——**堆积的待办会诱人批量草率放行，比过期更危险**。
- **宽限窗幂等**：since=cutoff−24h——扫过的旧 run 不再命中，重复扫描不重复执行；审计失败不推送。用户盯盘频率「每天看几次」是 12h 的由来。
- 2 测试全过；engine 整套 99.0s 绿。
- **M5 组至此 18/18 完成**：意图→映射→分流→新鲜度→状态机+认领→保护性退出→退出生命周期→对账→日界基线→HaltController→事件通知→模式切换→网络一致性→可观测性→崩溃恢复→webhook→风控开关→REVIEW 过期。

## 48. 未完成 / 下一步建议

1. `M2` 组只剩 `M2-13` 两项残余：`KindFundingWindow` 分支（默认关）与账户事件生产者（属 M5 账户面）——均不阻塞。
2. ~~顺手接线~~ **已完成**（见 §11）：`InvalidatePair` 调用点、轮询重载喂缓存、管理器缓存所有权与钩子。
3. **`M5` 组完成（18/18，§30~§47）**。**下一主战场：`M6` 验收与演练**（11 §7：按 TASKS.md 第一个未勾选的 `M6-xx` 开工——M6-06 崩溃演练可直接用 M5-15 的 `Recover`）；并行余项：前端 W5/W6 页面与 W5-01 收尾、client factory 生产装配（触发/预览/metrics/风控开关四个 fail-closed RPC 届时解锁）。

## 49. 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 未运行（本会话全部离线/真库 sqlite 测试） |
| webUI 代理 / Vite | 未启动 |
| sqlboiler 三件套 / 归一化脚本 | `~/go/bin` 在位；`scripts/normalize_llm_model_tests.py` 未再触发（本会话未跑生成） |
