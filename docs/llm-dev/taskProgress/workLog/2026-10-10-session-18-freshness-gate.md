# session-18：新鲜度门禁（M2-06）

**日期**：2026-10-10
**范围**：`M2-06` 新鲜度门禁（7 类事实阈值；自动候选窗口 60s）
**前置**：session-17（M0-14 PostgreSQL provisioning + pg 触发器实测）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/snapshot/freshness.go` | 阈值常量 · `Thresholds` / `CandleMaxAge` · `EvaluateFreshness` / `ApplyFreshness` · `FactAge` / `FreshnessReport` · `AutoCandidateDeadline` · `EvaluateExecutionFreshness` |
| `llm/snapshot/freshness_test.go` | 16 个测试函数 |

## 2. 阈值全部落地（04 §6）

| 事实类别 | 阈值 | 阻断？ |
| --- | --- | --- |
| Ticker | 60s | ✅ 阻断 |
| Orderbook | 60s | ✅ 阻断 |
| 最新已收盘 K 线 | `2×间隔 + 60s` | ✅ 阻断 |
| 资金费率 | 10min | ❌ **标记但不阻断** |
| 本地仓位 | 60s | ✅ 阻断 |
| 交易所实时仓位（执行前） | **5s** | ✅ 拒绝提交 |
| 执行前盘口（滑点复核） | **5s** | ✅ 拒绝提交 |
| 自动候选窗口 | 60s，且被流水线截止时间钳制 | — |

`CandleMaxAge` 用 `Interval.Duration()` 算，未知间隔**报错**而不是假设：

```go
2 * interval + CandleGrace
// 1m → 2m+60s   15m → 30m+60s   1h → 2h+60s   4h → 8h+60s   1d → 48h+60s
```

**为什么是两倍间隔**：一格给还没收盘的那根，一格给"已经收盘但下一次轮询还没取回来"的延迟，再加 60s 宽限。**为什么阈值这么松**：币安永续没有 WS，`timeoutREST` 默认 15s。**比一次轮询还紧的阈值会让每个事实都被判过期，系统什么都做不了**——这是"阈值必须按轮询周期设定，不能按 WS 推送设定"的实际含义。

## 3. 三个设计判断

### 3.1 每类事实按**自己的读取时间**计龄

一次决策里的 K 线、盘口、资金费率、账户是**不同时刻取回**的。用 `now - snapshot.BuiltAt` 一个数描述全部，会同时错两种方向：把刚取回的盘口说成旧的，把一分钟前取回的 K 线说成新的。

所以 `Ticker.ReadAt` / `OrderBook.ReadAt` / `Funding.ReadAt` / `Account.ReadAt` 各自计龄；K 线用**最新一根已收盘 K 线的收盘时刻**（那是这个窗口真正描述到的最晚时刻），而不是 `SourceTime` 字段本身（当窗口为空时前者更准）。

### 3.2 年龄未知一律不新鲜——但报错而不是判 STALE

两种情况"年龄算不出来"：

| 情况 | 处理 | 为什么不是 STALE |
| --- | --- | --- |
| 读时间缺失 | `ErrMissingReadTime` | 这是**组装器坏了**（忘了填 `ReadAt`），不是数据旧了。判 STALE 会让人去查交易所的连接，而问题在组装代码里 |
| 时间戳在评估时刻之后 | `ErrFactFromTheFuture` | 时钟或数据源有问题，真实年龄未知；**未知年龄不能当新鲜**，但也不该伪装成"旧" |

这是"fail-closed 但要把人指对地方"的一个具体例子：拒绝的理由要指向**能修的地方**。

### 3.3 资金费率：stale 但不阻断，且**单独成列**

资金费率是**成本**不是风险，且 8 小时才结算一次，十分钟前读的值与现在几乎没差别。若让它阻断，每个结算周期的大部分时间都无法决策。

实现上它进 `NonBlockingStale` 而不是被静默忽略：

```go
assert.Contains(t, report.NonBlockingStale, CategoryFunding, "it must still be recorded as stale")
assert.Empty(t, report.BlockingStale, "it must not appear among the blocking categories")
assert.Empty(t, report.ReasonCodes, "a tolerated staleness carries no refusal code")
```

**"标记后继续"与"当成没事"是两件事**：前者在审计记录里留下"这次决策用的资金费率是 12 分钟前的"，后者什么都不留。混合场景（盘口 stale + 资金费率 stale）有专门测试，断言两个列表各自正确。

## 4. 修掉一个真实缺陷：打 STALE 会覆盖更强的既有标签

`TestApplyFreshnessDoesNotWeakenAnExistingLabel` 第一次跑就失败了：

```text
expected: "GAP"    actual: "STALE"
```

原因：`candidate.DataQuality` 的 rank 表里 **`GAP` 与 `STALE` 同秩（都是 1）**，而 `Downgrade` 只在"目标秩更高"时报错。同秩 → 放行 → GAP 被改写成 STALE。

**为什么这是缺陷**：两者语义不同。GAP 是"这段区间有缺口"（对应 `RISK_CANDLE_GAP`），STALE 是"数据太旧"（对应 `RISK_SNAPSHOT_STALE`）。覆盖之后：

1. 审计记录里丢掉了"有缺口"这个更具体的理由；
2. 拒绝码从 `RISK_CANDLE_GAP` 变成 `RISK_SNAPSHOT_STALE`，指向的排查方向从"数据源缺行"变成"轮询太慢"。

**修法**：`ApplyFreshness` 遇到已降级的类目**跳过**，不覆盖：

```go
if s.QualityOf(fact.Category).IsDegraded() {
    continue
}
```

注意这不是"只降不升"那条不变量（那条管的是别把秩调高）。这是它的补充：**只降不升还不够，还不能把"为什么降"换成更弱的解释**。

## 5. 拒绝码与标签路径同源

`EvaluateFreshness` 与 `ApplyFreshness` 都要产出 `ReasonCodes`，而 `ApplyFreshness` 会**改动**快照标签。初始实现里 `ReasonCodes` 在评估阶段算完就不再更新，导致：

```text
ApplyFreshness 返回的 report.ReasonCodes = [RISK_SNAPSHOT_STALE]
同一时刻 s.DegradedCategories()      = [ticker]   → 按文档应同时有 RISK_SNAPSHOT_QUALITY
```

**两个调用方读同一个事实会得到不同结论**。修法是把推导抽成 `deriveReasonCodes(s)`，`ApplyFreshness` 在打标**之后**再调一次：

```go
func (r *FreshnessReport) deriveReasonCodes(s *MarketSnapshot) {
	r.ReasonCodes = nil
	if len(r.BlockingStale) > 0 { append(CodeRiskSnapshotStale) }
	if s.DegradedCategories() != nil { append(CodeRiskSnapshotQuality) }
}
```

测试 `TestGateAgreesWithTheDegradedCategoryPath` 就是钉这一条的。

## 6. 边界语义：恰好等于阈值算新鲜

阈值是**最大年龄**，等于最大值就是没超过。

```go
s.Ticker.ReadAt = at.Add(-TickerMaxAge)              → 接受
s.Ticker.ReadAt = at.Add(-TickerMaxAge - time.Nanosecond) → 拒绝
```

两侧都测，且 `EvaluateExecutionFreshness` 也有同样的边界用例（5s 正好算新鲜）。**只测一侧会把"≥ 还是 >"这个选择固化成一个没人注意到的约定**。

## 7. 变异验证（证明测试不是空转）

写测试只说明"测试通过"，不说明"拒绝路径真的存在"。做了两处变异：

| 变异 | 结果 | 说明 |
| --- | --- | --- |
| `CategoryOrderBook` 的 `Blocking: true` → `false` | **4 个测试失败**（`TestThresholdsCoverEveryBlockingCategory` · `TestApplyFreshnessMarksStaleOnly` · `TestEachBlockingFactCanStopADecision` · `TestBlockingAndNonBlockingMix`） | 阻断逻辑是被真的检查的 |
| `MayDecide()` → 恒真 | **4 个测试失败**（`TestGateAgreesWithTheDegradedCategoryPath` · `TestApplyFreshnessMarksStaleOnly` · `TestBlockingAndNonBlockingMix` · `TestEachBlockingFactCanStopADecision`） | 拒绝路径是被真的覆盖的 |

还原后全绿。文件用 `cp` 备份/还原，还原的正确性由行为证明（两个变异各自对应的测试都恢复通过）。

**同时测"必须拒绝"与"必须放行"**是这里的要点——只测拒绝会让一个过严的门禁（把一切都判过期）通过，那和没有门禁一样糟。

## 8. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **把门禁接进任何流水线** | **M4** | 本次只交付**纯函数**。**没有任何代码调用它**——决策流水线还不存在 |
| `RISK_CANDLE_GAP` / `RISK_ORDERBOOK_INVALID` / `RISK_ACCOUNT_NOT_RECONCILED` 等其余快照类检查 | **M2-05 / M4** | `RISK_CANDLE_GAP` 要检查"窗口里有没有 `addPadding` 补的零值"，需要定位零值 K 线的规则（04 §5 提到"存在零值 K 线"）；`RISK_ORDERBOOK_INVALID` 的快照侧部分已由 `snapshot.Validate` 的 `ErrOrderBookCrossed` 覆盖，但**没有映射成这个码** |
| **`maxAccountInventoryAgeMs ∈ {5000, 10000}` 的二选一** | **M4/Q8** | 文档 07 §5 说这个值有两个候选（5000 或 10000），本次统一用 **5s**（更紧的那个）。若 Q8 定案选 10000，`ExecutionPositionMaxAge` 要改成可配 |
| `FreshnessMS` 字段 | 已存在但未由门禁回填 | 快照契约里的 `FreshnessMS` 是给展示用的单值。门禁产出的是**每类事实**的年龄（信息更多）。**建议不要回填单一值**，或明确它等于 `WorstAge()` |
| 真实数据的年龄核对 | **M2-15** | 全部测试用构造的时间戳。**没有对真实交易所的读取时间做过核对**（例如签名的时间戳与本地时钟的一致性） |
| 时钟偏差检查（`blockOnClockDrift`，>1000ms 禁下单） | **M5-17** | 属 18 开关目录，不在本任务 |

## 9. 校验证据

```text
$ gofmt -l llm/ && go build ./llm/... && go vet ./llm/...    # 全通过
$ go test ./llm/... -count 1
ok  llm            (root boundary)  0.455s
ok  llm/candidate                   1.616s
ok  llm/coverage                    1.626s
ok  llm/httpclient                  1.798s
ok  llm/jev                         1.833s
ok  llm/openai                      1.719s
ok  llm/secrets                     2.130s
ok  llm/snapshot                    0.765s

$ go test ./llm/snapshot/ -count 1 -race   → ok 1.782s

测试函数计数：
candidate 31 · coverage 30 · httpclient 39 · jev 89 · openai 51 · secrets 20 · snapshot 45
→ 包内合计 305，加 root 包 6 个边界测试 = 311（session-17 为 295 → +16）
```

**全部离线**：无网络、无数据库、无时钟依赖（所有时刻由测试显式构造）。

## 10. 给接手模型的具体建议

**`M2-06` 的门禁是纯函数，没有任何调用方。** 它要生效必须有人接线——那是 M4 的流水线，或 `M2-15` 组装器在交付快照前跑一次 `ApplyFreshness`。**在那之前，"新鲜度门禁"只是一个可用的零件，不是一道生效的闸。**

按杠杆排序：

1. **`M2-15` snapshot 组装器**——它同时需要 `M2-04`（指标计算，`Indicators` 现在总是空的）并把 `ApplyFreshness` 接上。做完它，`llm/` 里这一整套契约才第一次对着真实交易所数据跑起来。**这是把前面所有零件变成系统的关键一步。**
2. **`M0-06` sqlboiler → `M0-07` 仓储层**——解锁 `M1-09`~`M1-13`。前置（pg）已就位；注意 sqlboiler 二进制未装、仓库用分叉、`--wipe` 会重写既有 29 个模型文件。
3. **proto 工具链校准**（`TASKS.md` §0.1）——挡面最广的单项。

**写新检查时请沿用本会话的习惯**：① 每个阈值都测**边界两侧**；② 每加一条拒绝逻辑就配一条"必须放行"的断言；③ 交付前做一次变异验证（把拒绝改成放行，看测试会不会失败）——否则无法区分"门禁生效"与"门禁没接上"。
