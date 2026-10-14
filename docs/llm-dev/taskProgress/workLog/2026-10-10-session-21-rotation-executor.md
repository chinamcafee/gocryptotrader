# session-21：轮询执行器与生命周期接线（M2-14）

**日期**：2026-10-10
**范围**：`M2-14` CoverageRotator 执行器（调度 + 记账 + 匀速 + 生命周期）
**前置**：session-20（M2-15 snapshot 组装器）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `engine/llm_rotation.go` | `iRotationSource`（4 方法窄接口）· `Rotator` · `Step` · `Run` · `Sleeper`/`Sleep` · `RotationStats` · `StepOutcome` |
| `engine/llm_rotation_setup.go` | `planFromCoverage` / `intervalFromConfig` / `indicatorParamsFromConfig` · `SetupRotation` · `rotationLoop` |
| `engine/llm_manager.go` / `llm_manager_types.go` | **改动**：`Start` 拉起轮询；`Rotation()` 访问器 |
| `engine/llm_rotation_test.go` / `llm_rotation_setup_test.go` | 19 个测试函数 |

**这是两层覆盖设计的第一层第一次真的会运行。** 它保持每个被追踪的市场温热，好让触发器在任何地方都能被发现。

## 2. 窄接口：4 个方法，不是整个 `iExchangeManager`

```go
type iRotationSource interface {
	GetEnabledPairs(a asset.Item) (currency.Pairs, error)
	GetHistoricCandles(ctx, pair, a, interval, start, end) (*kline.Item, error)
	UpdateTicker(ctx, p, a) (*ticker.Price, error)
	UpdateTickers(ctx, a) error
}
```

轮询只做四件事：读启用列表、拉一个小窗口、刷单个 ticker、批量刷全部 ticker。`exchange.IBotExchange` 满足它（编译期断言钉住），所以生产接线是"把 exchange 传进去"。

**收益与组装器同理**：假 source + 注入时钟 + 注入 sleeper ⇒ 19 个测试全部离线，不碰交易所。

## 3. 六个设计判断

### 3.1 批 ticker 挂在**周期**上，不是挂在轮次上

```go
if r.lastTicker.IsZero() || now.Sub(r.lastTicker) >= r.cycle.CycleDuration { ... UpdateTickers ... }
```

一次 `UpdateTickers` 刷全部市场的 ticker，代价是一次请求——**整个计划就建立在这一点上**（565 vs 588×N 的差别）。

**为什么挂周期而不是挂轮次**：轮次可能因为暂停、失败、或市场数量变化而变长。挂在轮次上意味着"轮次越长 ticker 越旧"，而轮次长度不是有界的。挂周期后，ticker 的新鲜度由周期保证。

**这个选择有一个可观测的后果**：轮次边界和周期边界撞上时（4 市场 × 10 分钟周期 ⇒ 150 秒间隔 ⇒ 第 4 次访问正好落在 600 秒边界），ticker 刷新**优先**，那一次访问推迟到下一步。所以**一轮实际耗时可能略长于名义周期**。测试把这个交互显式断言出来了，而不是让它作为"偶尔慢一点"漂着。

### 3.2 权重闸先判后发，不够就等

```go
if !r.gate.Allow(weight, now) {
	outcome.Paused = true
	outcome.RetryAfter = r.gate.RetryAfter(weight, now)
	return outcome, nil
}
```

**先判后发**意味着窗口永远不会被推过上限——这正是"决策取鲜永远找得到额度"的实现方式。暂停不是错误（等待是设计行为），所以 `Paused` 是 outcome 的一个字段而不是 error。

批量 ticker 失败时**刻意不记时间戳**：

```go
if err := r.src.UpdateTickers(ctx, r.cfg.Asset); err != nil {
	r.failures++
	outcome.Failed = true
	// 记了时间戳就等于声称一份并不存在的新鲜度
	return outcome, nil
}
```

这是本会话唯一一处"宁可重复请求，也不要谎报状态"的地方——**新鲜度是安全断言，不能因为"试过了"就打勾**。

### 3.3 每个分支都必须 sleep

`Run` 的三条路径：`ErrRotatorNotPlanned` → sleep spacing；`Paused` → sleep `RetryAfter`；默认 → sleep spacing。**没有一条路径能空转**。

这不是风格问题：一个空转的轮询循环会立刻变成对交易所的请求洪水，而这是账户级限频禁令的成因。**"每个分支都 sleep"是"这里的 bug 不会变成洪水"的机械依据**——测试 `TestRunPausesWhenTheBudgetIsFull` 断言暂停路径下等待次数等于步数且等待值为正。

### 3.4 单市场失败不中断整轮，且游标照常前进

```go
r.cursor++                    // 先前进，再取数
if err := r.fetchIncrement(...); err != nil { r.failures++; outcome.Failed = true }
```

**先前进再取数**：否则一个长期不可用的市场会让所有市场排在它后面等——一轮永远走不完。失败被计数（`Stats.Failures`），而计数就是让人看见"哪个市场一直坏"的入口。

### 3.5 配置映射独立成步，并单独测

`planFromCoverage` / `intervalFromConfig` / `indicatorParamsFromConfig` 三个纯函数，各有测试。

**为什么值得单独一步**：配置里是**人填的数**，计划里是**这些数隐含的算术**。两者名字像、语义不完全一样。映射散在接线代码里时，字段名写错的表现是"计划静默用了默认值"——也就是**权重预算和操作员选的不一样**，而没人会看出来。

`TestPlanFromCoverageMapsEveryField` 除了逐字段断言，还断言映射后的分数**真的传到预算算术里**（0.45 → 1080/min）。`TestPlanFromCoverageDoesNotAliasTheConfigSlice` 断言计划不别名调用方的 slice——否则事后改配置会改到活计划。

`intervalFromConfig` 对未知拼写**报错而不是默认 1h**：新鲜度阈值和取数窗口都由间隔派生，静默替换等于用了操作员没要求的算术。并有测试断言"配置的拼写与契约的间隔列表能双向往返"。

### 3.6 构建与运行分开；`Start` 不因可选组件失败而失败

```go
// SetupRotation 不启动任何东西：构建不出来的轮询是配置问题，
// 应该在 bot 开始调交易所之前就被看到。
```

`coverage.enabled=false` → 返回 `(nil, nil)`（合法配置，不是失败）。启用但 exchange 解析不出来 → 返回错误。

`LLMManager.Start` 里：

```go
rotation, err := m.SetupRotation()
if err != nil {
	// 模式与生命周期已经建立；覆盖出问题只报告，子系统其余部分照常运行，
	// 因为决策路径不依赖第一层是否启动。
	log.Errorf(...)
} else if rotation != nil { go rotation.rotationLoop(loopCtx) }
```

**决策路径不依赖第一层**是这条设计的依据：第一层是"提前发现问题"，第二层（组装器按需现拉）才是决策的数据来源。第一层挂了会让触发器变钝，但不该让系统起不来。

## 4. `RotationStats`：W7-07 要的运行态数据

```go
Markets · CycleDuration · IncrementSpacing
WeightBudgetPerMinute · WeightUsedThisMinute
VisitedThisCycle · CycleProgressPercent
OldestMarketAge · LastTickerRefresh · LastReload
CyclesCompleted · Failures
```

**全部是实测值**。计划算术（638/63.8/235.2/299）在 `coverage.BuildCycle` 里就能拿到，把两者混在一起正是"仪表盘把计划当观测值展示"的成因（session-15 §3 记过同一件事）。

`OldestMarketAge` 的语义：**最冷的市场有多冷**。从未访问过的市场按"本轮开始时刻"计龄——这是循环刚开始时的诚实答案。测试断言它会随时间增长。

## 5. ⚠ 一个需要人工知晓的行为变更

`coverage.enabled` 在 `config_example.json` 与 `CheckLLMConfig` 的默认值里都是 **true**。所以**下次重启 bot 会开始对启用的合约做只读轮询**（5 个启用合约 ⇒ 一轮 2 次批量/周期级 + 5 次增量，约占限额 2.4%）。

**这个循环从未对真实交易所跑过。** 本会话的全部验证是离线的（假 source + 注入时钟 + 注入 sleeper）。已在 `PROGRESS.md` §3 用醒目块记录，并把"设 `coverage.enabled=false` 可先不跑"写进去。

为什么仍然接线：这是用户要的功能（"针对 300+ 的币安合约"），读只读行情、被权重预算硬约束、每条路径都 sleep、`enabled` 是配置开关、且只影响"下次重启"（正在跑的进程不受影响）。**但这仍是本会话唯一一处未经实机验证就接入生命周期的东西**——如实记在这里。

## 6. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **整点全量重载** | **M2-16**（新拆出） | 文档 §7.4 要求整点对每合约拉 `klineFullLimit=250` 根（weight 2）重算指标，摊到 `fullReloadSpreadMinutes`。**本次未实现**——第一层只做增量，指标只在决策路径的组装器里算 |
| **触发器扫描挂钩** | **M2-16** | 文档 §7.4 的"触发器检测"：维护每合约的 MACD 柱符号/均线相对位置增量状态，整点重载后扫全部市场产出触发事件。**这是第一层对决策的实际价值所在**（任何合约出信号都能被发现），也是本次没做的部分 |
| **ticker/orderbook 之外的市场级缓存刷新** | 无 | 轮询只刷 ticker（批量）与 K 线（增量）。**盘口按设计不进轮询**（weight 0）——决策时现拉 |
| 真实交易所往返 | **未验证** | 见 §5。全部离线 |
| `persistIntervals` 的落库 | **M2-02 / M0-04** | 计划里有这个字段，轮询不写库 |
| 覆盖运行面的 RPC 出口 | **W7-07** | `RotationStats` 就是它要的数据，但出口被 proto 阻塞 |
| 手动触发单市场轮询一次 | 无 | 用于验证/诊断的入口不在任务描述里，未做 |

## 7. 校验证据

```text
$ gofmt -l engine/ llm/          # 无残留
$ go build ./...                 # 通过
$ go vet ./engine/               # 无输出
$ go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'
ok  github.com/thrasher-corp/gocryptotrader/engine  79.329s     # 整套通过
$ go test ./llm/... -count 1     # 八包全绿

测试函数计数（engine 侧，LLM 相关）：
  session-19 指标跨层交叉验证      3
  session-20 组装器               12
  session-21 轮询执行器 + 配置映射  19
  → 合计 34

文档：markdownlint docs/llm-dev 38 files → 0 issues
```

**本会话修掉的自己的错误**（都是测试/夹具，不是代码）：

1. `SYM_A_USDT` 夹具被 `currency` 规范化成 `SYMA_USDT`——**货币代码不能含分隔符**，类型会把它去掉。夹具改名。
2. 期望"重建后第一步是 ticker"——错。ticker 挂周期，所以重建后第一步仍是访问。**代码对，期望错**。
3. 期望"5 步 = 1 次 ticker + 4 次访问"——错。4 市场 × 150 秒间隔，第 4 次访问正好落在 600 秒周期边界，ticker 优先。**代码对，期望错**，并且这个交互值得写进断言。
4. `nilRotator.RefreshMarkets` 期望无错——错，返回 `ErrNilRotator` 才是正确行为。**代码对，期望错**。

三次"代码对、期望错"都出在同一类地方：**我对时序的直觉比代码的实际语义乐观**。写时序断言时应当先手算一遍，而不是先写下来。

## 8. 给接手模型的具体建议

**`llm/` + 组装器 + 第一层轮询现在都齐了**，但**决策流水线（M3/M4/M5）还不存在**——没有任何东西会读快照、问模型、过风控、下单。链路的下一个断口在那里。

按杠杆排序：

1. **`M2-16`（整点重载 + 触发器扫描）**——它是第一层真正产生**价值**的部分：没有扫描，全量覆盖只是"数据温热"，没有任何东西会因此被触发。而触发器是 M4 决策流水线的入口。做完它，第一层与流水线之间的接口就定了。
2. **`M0-06` → `M0-07` → `M1-09`~`M1-13`**——决策记录要落库。pg 前置已就位（`docker compose up -d postgres`）。注意 sqlboiler 未安装、仓库用分叉、`--wipe` 会重写既有 29 个模型文件。
3. **proto 工具链校准**（`TASKS.md` §0.1）——挡面最广的单项，同时挡着 `M1-14`、`W7-07`、W5/W6 的接口层。

**做 `M2-16` 时请沿用本会话的三条做法**：

- **窄接口**：扫描器需要的是"读某市场的指标状态"与"产出一个事件"，不是整个 exchange manager。
- **注入时间**：`Step`/`Run` 的全部时间来自 `RotationConfig.Now`，所以 19 个测试没有一个真的 sleep 过。扫描器同理。
- **每个分支都 sleep（或等价地：不会空转）**：整点重载会一次性摊开 588×weight 2 的负载，是所有路径里最可能变成尖峰的一条。**先算峰值再写循环**——`coverage.Budget` 已经把重载速率算好了。
