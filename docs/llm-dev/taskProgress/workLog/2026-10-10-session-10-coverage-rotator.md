# session-10：525 合约两层轮询的规划层与权重闸（M2-12）

**日期**：2026-10-10
**范围**：`M2-12` CoverageRotator（后端 `llm/coverage/`，只做规划层与预算闸）
**前置**：session-09（M1-02 + M1-03）完成，`llm/` 五包全绿

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/coverage/plan.go` | `Plan`（8 字段）+ `DefaultPlan`/`Normalise`/`Validate`；`KlineWeight`；`BuildCycle` + `Cycle` + `Request`/`Kind` + `Budget` |
| `llm/coverage/budget.go` | `Gate`（滑动窗口权重计数，mutex 保护）：`NewGate`/`NewGateFromPlan`/`BudgetPerMinute`/`Allow`/`Reserve`/`RetryAfter`/`Used`/`Reset`/`PerMinute` |
| `llm/coverage/plan_test.go` | 22 个测试函数 |

## 2. 为什么这样做

### 2.1 只做规划，不做执行

`BuildCycle` 是**纯函数**：给定 `Plan` + 合约列表，返回一个 `Cycle`，包含要发的请求、间距、总权重和预算算术。它不持有 exchange manager、不 sleep、不起 goroutine。

**理由**：这套设计里最容易被写错、也最难在真机上验证的就是**权重算术**。把它做成纯函数，就能用「525 个合约」这个实测规模离线断言每一个数字；而执行部分（匀速发请求、整点重载、生命周期）天生需要 exchange manager，测试成本高得多。两者混在一个类型里，就再也无法只测算术。

执行器因此拆成 **M2-14**（`TASKS.md` 已新增），与 `M2-03` MarketSnapshotBuilder 一起做——因为执行器发出的是 snapshot 的原料，签名要等 snapshot 契约定下来。

### 2.2 实测规模的算术，全部可复算

`TestDefaultPlanFitsTheBudgetForTheWholeExchange` 断言（525 个合约）：

| 量 | 值 | 来源 |
| --- | --- | --- |
| 一次全量轮询 | `40 + 10 + 525 = 575` weight | 批量 ticker 40 + 批量资金费率 10 + 每合约增量 1 |
| 轮询速率 | `575 / 10min = 57.5` weight/min | 周期 10 分钟 |
| 整点重载 | `2 × 525 = 1050` weight | `limit=250` 落在 weight 2 档 |
| 重载速率 | `1050 / 5min = 210` weight/min | `fullReloadSpreadMinutes = 5` |
| 峰值 | `57.5 + 210 = 267.5` weight/min | 整点两者同时段 |
| 预算 | `2400 × 0.6 = 1440` weight/min | `weightBudgetFraction = 0.6` |
| 余量 | `1440 − 267.5 = 1172.5` weight/min | 留给决策取鲜（单次 10-15）与用户查询 |

文档 `04` §7.4 写的"合计 ≈ 565 weight/周期"是不含资金费率批量的口径（40 + 525 = 565）。代码把资金费率算进去了，所以是 575。**代码的口径更保守**（多算 10），方向正确。

### 2.3 修正了文档里的一处 off-by-one

文档 `04` §7.4 写 `limit ≤ 100 → weight 1`。Binance 的真实表是**半开区间**：`[1,100)` → 1，`[100,500)` → 2。所以 `limit=100` 实际是 weight 2。

代码照**真实表**实现，并把这个偏差写进 `KlineWeight` 的注释。同时 `MaxKlineIncrementLimit = 99`——增量调用的上限停在最便宜档的最后一个值，而默认值 5 远在安全区。

这一点很重要：如果照文档实现（100 算 1），预算会**少算**，而少算预算的闸不如没有闸。

### 2.4 计划放不下时，配置期就失败

`BuildCycle` 在峰值超过预算时返回 `ErrBudgetExceeded`，不返回一个"能建但跑不动"的计划。

反例场景：把 `weightBudgetFraction` 设成 `0.01` → 预算 24 weight/min，但轮询要 57.5、重载要 210。这种配置如果被接受，表现是**凌晨三点闸开始拒绝一切请求**，而操作者完全不知道是配置问题。配置期报错把故障挪到了有人看着的时候。

### 2.5 参数范围的两处判断（偏离文档的取舍已记录）

| 参数 | 文档写法 | 代码实现 | 理由 |
| --- | --- | --- | --- |
| `rotationCycleMinutes` | "默认 10，可配 5-10" | 接受 **1..60** | 真正约束它的是预算，而预算是**算出来的**不是假设的。周期变慢只会延迟触发器发现，永远安全；硬性卡在 10 会拒绝一个合法配置 |
| `klineFullLimit` | 250 | 接受 **100..500** | 500 是 weight 2 档的上界，超过就跳档到 5。下界 100 是最长指标回看窗口的下限 |

### 2.6 权重闸的三个性质

```go
func (g *Gate) Allow(weight int, now time.Time) bool
func (g *Gate) Reserve(weight int, now time.Time) error
func (g *Gate) RetryAfter(weight int, now time.Time) time.Duration
```

1. **拒绝不记账**。被拒的请求不写进窗口。否则一个 tight retry loop 会把窗口越推越满——拒得越多、越拒不动。
2. **窗口是 `(now-window, now]`**。刚过期的算出去，**时间戳在 `now` 之后的也不算**。后者只在时钟偏移下出现，但把它算进去会让闸"因为还没发生的事"拒绝工作。这是本会话改掉的一个真实缺陷（见 §3）。
3. **`RetryAfter` 返回精确等待时间**而非"你等着"。测试断言：睡够返回值后，同一个请求**真的**能通过。否则调用方睡一觉醒来还是被拒，就会退化成轮询——正好烧掉预算本要保护的 CPU。

### 2.7 拒绝不记账 + 精确等待 = 轮询永远饿不死决策

这是整个设计的目的，而它需要**实证**而不是断言。`TestSimulatedSweepStaysInsideTheBudget` 做了一次 10 分钟仿真：

- 每分钟把该分钟的轮询量（57 weight）和重载量（210 weight，仅前 5 分钟）喂进闸
- 每分钟再尝试一次 15 weight 的"决策取鲜"
- 断言：**每分钟决策都拿到额度**，且窗口从未超过预算

如果哪天有人调快了周期或调低了预算，这个仿真会先失败——而不是等到真机上决策开始拿不到数据。

### 2.8 确定性：合约列表要清理并排序

`cleanMarkets` 做 trim → 去空 → 精确去重 → 排序。理由：存下来的计划必须能跟后来的计划**比较**。如果输入顺序影响输出，两次同样的配置会产出"不同"的计划，变更检测就没法用。

### 2.9 `Requests()` 是报告用的，不是执行用的

`Cycle.Requests()` 把整个周期展开成一个列表（批量两笔 + 每合约一笔），方便报告和断言总权重。但它的注释明确写了：**执行器应该用 `IncrementSpacing` 匀速发，而不是把这个 slice 一次性打出去**——那样正好制造了计划要避免的突发。

## 3. 本会话修的一个真实缺陷与三个测试错误

| # | 现象 | 判定 | 处理 |
| --- | --- | --- | --- |
| 1 | `TestGateRetryAfter`：在 `start+60s` 查询后，再在 `start+20s` 查询返回 0（应为 40s） | **测试错了**：`RetryAfter` 会顺带修剪过期事件，所以向前查询再向后查询是"时间旅行"，真实流程不会发生。但暴露了一个**真实缺陷**：`usedLocked` 会把时间戳在 `now` 之后的"未来事件"计入窗口 | **代码修**：窗口定义为 `(now-window, now]`，未来事件不计。**测试改为**每个场景用独立闸 + 按时间单调推进 |
| 2 | `TestRotationSpreadsAcrossTheCycle`：期望间距 1142ms，实得 1.142857142s | **测试错了**：`IncrementSpacing` 保留全精度，`IntervalMS` 才是截断值 | 测试改为断言全精度时长 `600s/525`，另断 `IntervalMS == 1142`（并说明截断形式会在 525 次后少 0.45s，仍在周期内） |
| 3 | `TestPlanValidation`："spread zero / cycle too short / increment zero / fraction zero" 未被拒绝 | **测试错了**：零值在 `Normalise` 里的语义就是"未设置"，所以会被填成默认值再验证通过；要拒绝必须给越界值 | 改为负值 / 超出上界；并在 `Normalise` 的 GoDoc 里**写明零值语义**，避免下一个人踩同一个坑 |

`TestPlanValidation` 曾用 `require.Error` 在第一个失败用例就 `FailNow`，所以一次只暴露一个用例——这也是为什么它迭代了三轮。已改为 map 遍历 + 逐项断言（仍是 `require`，但用例集合本身现在是对的）。

## 4. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **执行器**（接 exchange manager、匀速发请求、整点重载、触发器挂钩、引擎生命周期） | **M2-14**（新拆出） | 本次**没有**任何 I/O、没有 goroutine、没有 engine 接线。M2-12 只交付规划与预算闸 |
| 整点重载的调度决策（"只对有批准策略的市场全量重载，其余延后"） | **M2-14** | 文档 §7.4 提的优化项，属执行策略 |
| `persistIntervals` 的实际落库 | **M2-02 / M0-04** | `Plan` 里有这个字段并做了校验，但写库要等 DB |
| 资金费率批量权重 = 10 | **M1-15 / 实机冒烟** | **这是估计值，未对真实端点验证**。取偏高是刻意的：低估开销会让预算闸失效。实机要确认并回填 |
| 触发器扫描（指标增量状态） | **M2-13** | `Cycle` 的注释里提到它的价值，但扫描逻辑属 M2-13 |

## 5. 对文档的三处偏差（已被代码纠正）

| 位置 | 文档写法 | 代码实际 | 处理 |
| --- | --- | --- | --- |
| `04` §7.4 K 线权重档 | `limit ≤ 100 → weight 1` | 真实表是半开区间：99→1、100→2 | **代码按真实表**；`KlineWeight` 注释说明。文档待同步 |
| `04` §7.4 周期范围 | 可配 5-10 | 接受 1..60（预算是算出来的） | 代码放宽；理由见 §2.5。文档待同步 |
| `04` §7.4 周期权重合计 | ≈565 | 575（含资金费率批量 10） | 代码更保守。文档待同步 |

**没有**去改文档，是因为这三处都需要先和 `04` 的其他小节（如 §9 DataHistoryManager 的权重预估）保持一致；单独改一处会制造新的不一致。已在 `PROGRESS.md` §4 记录，等做到 `M2-11`/`M2-13` 时一并同步整个 §7.4 与 §9。

## 6. 校验证据

```text
$ gofmt -l -w llm/            # 无残留
$ go build ./llm/...          # 通过
$ go vet  ./llm/...           # 无输出
$ go test ./llm/... -count 1
?   github.com/thrasher-corp/gocryptotrader/llm            [no test files]
ok  github.com/thrasher-corp/gocryptotrader/llm/candidate   0.463s
ok  github.com/thrasher-corp/gocryptotrader/llm/coverage    0.525s
ok  github.com/thrasher-corp/gocryptotrader/llm/httpclient  0.692s
ok  github.com/thrasher-corp/gocryptotrader/llm/jev         0.627s
ok  github.com/thrasher-corp/gocryptotrader/llm/openai      0.585s
ok  github.com/thrasher-corp/gocryptotrader/llm/secrets     1.544s

$ go test ./llm/coverage/ -count 1 -race
ok  github.com/thrasher-corp/gocryptotrader/llm/coverage    1.762s

测试函数计数：candidate 31 · coverage 22 · httpclient 39 · jev 89 · openai 41 · secrets 20  → 242
```

**全部离线**：无网络、无时钟依赖（所有时间点由测试显式构造）、无数据库。

## 7. 给接手模型的具体建议

**当前最高杠杆点：`M2-03` MarketSnapshotBuilder。**

原因：它是两条线的公共前置。`M2-14`（轮询执行器）需要它定义"取回来的东西是什么"，M4 决策链路需要它定义"喂给模型的东西是什么"。而且它**不依赖 DB、不依赖 proto、不依赖 provider**——现在就能做，做完就能离线测。

建议先落 `snapshot` 的类型（有界 + 全十进制字符串，`docs/llm-dev/04` §3.1），再落 `M2-14`。

**如果 M2-03 的字段还没想清**，可先做：

1. `M1-13` 提示词常量（纯字符串，零依赖）
2. `M0-13` 架构边界测试（`llm/` 不得 import `engine/`）
3. `W5-01`、`W7-04`、`W8-04/06`（前端，不依赖后端）

**注意**：`llm/openai/prompt.go` 是 `M1-13` 的落点，本次**没有**创建——`probe.go` 里的两个提示词是探针专用，不要误当作生产提示词。
