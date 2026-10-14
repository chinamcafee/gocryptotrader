# session-19：指标组装与派生事实（M2-04）

**日期**：2026-10-10
**范围**：`M2-04` 指标计算与派生事实（MACD 交叉/柱趋势、均线上下方/斜率、RSI、ATR、布林）
**前置**：session-18（M2-06 新鲜度门禁）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/snapshot/indicators.go` | `BuildIndicatorSet` · `IndicatorParams.Validate`/`LongestPeriod` · `DefaultIndicatorParams` · `MinCandlesForMACD` · `DecimalFromFloat` / `ParseFloatValues` · `renderComputed` |
| `llm/snapshot/indicators_test.go` | 22 个测试函数 |
| `llm/snapshot/snapshot.go` | **改动**：`IndicatorParams` 把 `MovingAverages []int64` 拆成 `SMAPeriods` + `EMAPeriods` |
| `llm/snapshot/derive.go` | **改动**：`formatFloat` 改为按统一精度舍入 + 去尾零 |
| `engine/llm_indicators_test.go` | **3 个跨层交叉验证**（放在 `engine/` 因为 `llm/` 不得 import `exchanges/kline`） |

## 2. 分工是刻意的：一处计算，两处读取

文档 `04` §2.1 直接点名了三个仓库函数（`GetMovingAverageConvergenceDivergenceOnClose` / `GetSimpleMovingAverageOnClose` / `GetExponentialMovingAverageOnClose`）。**本层不重新实现指标。**

理由不是省事：

- webUI 的交易台显示的 MACD 就是 `exchanges/kline/technical_analysis.go` 算出来的（经 `gettechnicalanalysis`）。**如果模型读的是第二份实现，图表和决策就会在没人注意的地方分叉**，而分叉时两边都"看起来对"。
- 指标计算是纯数值代码，重写一遍的正确性收益接近于零，重复维护的成本却持续存在。

所以边界划在"计算"与"计算之外"之间：

| 归属 | 内容 |
| --- | --- |
| `exchanges/kline`（既有） | 算 SMA / EMA / MACD 的数值 |
| `llm/snapshot/indicators.go`（本会话） | 最小根数校验 · float64 → 十进制字符串 · 按 `MaxIndicatorPoints` 收边 · 派生事实 · 记录参数 · 组装后跑一遍契约校验 |
| `engine/`（`M2-15`） | 把 `kline.Item` 喂给上面两层（转换层只能在这里，见 §4） |

参数集按 `04` §2.2：MACD 12/26/9、SMA 20/50/200、EMA 12/26/50/200、RSI 14、ATR 14、布林 20/2/2。`LongestPeriod()` = 200，正好落在 coverage 计划抓取的 `klineFullLimit = 250` 之内。

**为什么 MACD 用 12/26/9**：它是教科书默认，也是模型训练语料里最常见的一组，模型最不容易读错。

## 3. 修掉一个真实缺陷：计算值可能超出契约的小数上限

**只有跨层验证能发现这个。** 第一次把平台自己的 MACD 输出喂进组装器：

```text
macd histogram: value 1 (0.0035044457052606504):
  value must be a plain decimal string: "0.0035044457052606504"
```

真实 MACD 柱值 `0.0035044457052606504` 有 **19 位小数**，而 `ValidateDecimal` 的上限是 **18 位**（`^[0-9]{1,20}(\.[0-9]{1,18})?$`）。

**后果**：平台算出来的**合法**指标会被契约拒绝。任何小值指标（MACD 柱、RSI 的小数、比率）都可能触发。

**为什么 22 个单测没抓到**：我自己造的合成值都是短小数（`0.5`、`1`、`1e-9`）。**被测数据的形状恰好比真实数据简单**——这类盲区只能靠引入真实来源才能暴露，这也是跨层验证不可替代的原因。

### 修法与不修的地方

```go
const MaxRenderedFractionDigits = 12

func renderComputed(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) { return "", ErrInvalidDecimal }
	text := strconv.FormatFloat(value, 'f', MaxRenderedFractionDigits, 64) // 舍入
	text = strings.TrimRight(text, "0"); text = strings.TrimSuffix(text, ".") // 去尾零
	...
}
```

**没有**去放宽契约的小数规则。那条规则存在的意义是"钱不过浮点"，为了一个**读数**放宽它等于拿安全换方便。正确的方向是**收窄计算值**：

- 指标是**证据**，不是钱。12 位小数的分辨率远超范围内任何价格尺度。
- 去尾零使一个数**只有一种写法**——这正是快照哈希可比的前提（也是当初禁止指数记法的同一个理由）。
- 同一精度同时用在 `decimalsOf`（指标）与 `formatFloat`（K 线摘要）上，**规则只有一条**。摘要路径有同样的暴露面（小价格窗口的涨跌幅），顺手一起收窄。

精度值本身有测试钉住：`TestComputedValuesAreRoundedToOnePrecision` 断言 `MaxRenderedFractionDigits == 12`，并给出 7 个具体值的期望渲染（含 `1e-13 → "0"`）。

## 4. 为什么跨层测试放在 `engine/`

`llm/snapshot` 的边界测试（`llm/boundary_test.go`）**穷尽式禁止** import `exchanges/kline`：`kline` → `exchanges/order` → 订单管理器，会把 `llm/` 的可抽出性毁掉。

所以"把 `kline.Item` 转成本层吃的普通切片"这件事**只能发生在 engine 层**，而证明"组装结果与平台数字一致"的测试也只能在 engine 层。`engine/llm_indicators_test.go` 现在占住了这个位置——`M2-15` 写组装器时，它的验证已经先写好了。

## 5. 跨层验证的三条断言

`TestIndicatorAssemblyAgreesWithThePlatformsOwnIndicators` 用**仓库自己的** TA 函数算一遍，再喂进组装器：

1. **携带值逐点相等**：`set.MACD.Histogram[i]` 必须等于平台对应点经 `DecimalFromFloat` 的渲染（携带的是平台值的尾部，不是重算或舍入过的副本）。
2. **`Latest` 等于平台最新值**：SMA(20) 与 EMA(26) 各自核对。这里踩过一次自己的坑——见 §6。
3. **位置事实等于直接比较**：`PriceVsMA` 必须等于"最新收盘 vs 最新均线"的直接比较结果（契约就是这么定义的）。
4. **派生叉态按携带窗口**：

```go
carried := macd.Histogram[len(macd.Histogram)-snapshot.MaxIndicatorPoints:]
assert.Equal(t, crossOf(carried), set.MACD.CrossState, ...)
assert.NotEqual(t, crossOf(macd.Histogram), set.MACD.CrossState, ...)  // 夹具刻意让两者不同
```

第 4 条是**最有价值的一条**。夹具的合成价格先跌后涨，金叉发生在 250 根序列里但**落在被携带的 20 根窗口之外**。于是：

- 全序列的答案 = `GOLDEN`
- 窗口内的答案 = `NONE`
- 组装器必须报 `NONE`

**为什么必须是 NONE**：模型只能看到窗口。报一个模型看不到证据的"当前状态"，等于让它按看不见的理由行动——而这正是 `RISK_*` 那一整套存在要防的事。

夹具刻意让两个答案不同，并把断言写成 `NotEqual(crossOf(全序列), 派生值)`。**如果哪天有人把派生改成按全序列算，这条会失败并说明原因**，而不是静默通过。

`crossOf` 在测试里**独立重写了定义**（不调用包内助手），所以它验证的是行为而不是复述实现。

另两条跨层测试：

- `TestAssemblerRejectsAWindowThePlatformWouldAlsoReject`：30 根窗口。平台拒绝（`slow+signal-2 = 33`），组装器也必须拒绝，**并且在我们调用平台之前就拒绝**——否则会白跑一次引擎调用再拿到同样的错误。
- `TestAssemblerRefusesNonFiniteInputFromThePlatform`：窗口中途出现 NaN（短窗口的除法指标会产出）。必须拒绝而不是让它走到会 panic 的 decimal 构造器。

## 6. 本会话踩的三个自己的坑（都是测试/夹具问题）

| # | 现象 | 判定 | 修法 |
| --- | --- | --- | --- |
| 1 | `TestSeriesAreTrimmedToTheCap`：`Latest` 期望 `"210"` 实得 `"200.5"` | **我把均线当成了收盘价** | 改为断言 `Latest == Values[len-1]`（真正的不变量），并在注释里写明上升序列上 20 期均线滞后于最新收盘 |
| 2 | `TestPositionAndSlopeFacts`：`maOf(…, +5)` 期望 `BELOW` 实得 `ABOVE` | **夹具的偏移量没盖过均线滞后**：上升序列上运行均值滞后约半周期（9.5），+5 不足以把它抬到收盘价之上 | 偏移改 ±50，并注释说明为什么要清过这个滞后 |
| 3 | `TestNonFiniteValuesAreRefused`：`Inf`/`NaN` 没被拒 | **我把非有限值放在了被携带窗口之外**（索引 100 / 5，而窗口是最后 20 个） | 移到窗口内。**并补一条断言**：窗口外的非有限值被裁掉、不导致失败——这不是漏洞（模型看不到、事实也不从它派生），但值得写下来 |

第 3 条附带一个真实结论：**裁剪发生在渲染之前**，所以窗口外的坏值既不进契约也不影响派生。行为可辩护，但必须写明，否则下一个人看到"窗口外有个 NaN 却通过了"会以为是漏了。

## 7. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **组装器的真实接线**（把 `kline.Item` → 本层输入） | **`M2-15`** | 本次**没有**任何调用方。`engine/llm_indicators_test.go` 里的转换是**测试内联**的，不是产品代码 |
| **RSI / ATR / 布林的计算** | `M2-15` | 本层**只做组装与派生**，三个可选指标的数值仍由 engine 侧调用（文档 `04` §2.2 把它们列为默认开/可选，但没点名仓库函数）。写组装器时需确认用 `exchanges/kline` 的哪个方法，或 `technical_analysis.go` 里是否已有 |
| **指标缓存**（`04` §2.4） | **`M2-02`** | 文档明确列了缓存策略（已收盘 K 线永久有效、未收盘不缓存、`SetExchangePair` 移除时失效）。本次未实现 |
| **多间隔共振** | 无 | `04` §2.3 提到"同时要多个间隔"的权重估算，但没要求首期实现。当前一次分析一个间隔 |
| `MaxRenderedFractionDigits = 12` 的选择依据 | — | 由"远超范围内任何价格尺度"这句论证支撑，**没有**参照供应商精度或币安 tick size 做定量核对。若后续发现 12 位不够（例如某指标需要更细的刻度），这是一处要重新论证的常量 |
| `SMA/EMA` 周期是否该按市场调整 | 无 | 固定 20/50/200 与 12/26/50/200。`04` §2.2 给的就是固定值 |

## 8. 校验证据

```text
$ gofmt -l llm/ engine/            # 无残留
$ go build ./...                   # 通过
$ go vet ./llm/...                 # 无输出
$ go test ./llm/... -count 1
ok  llm (root boundary) · candidate · coverage · httpclient · jev · openai · secrets · snapshot   八包全绿

$ go test ./engine/ -run 'TestIndicatorAssembly|TestAssembler' -count 1 -v
--- PASS: TestIndicatorAssemblyAgreesWithThePlatformsOwnIndicators
--- PASS: TestAssemblerRejectsAWindowThePlatformWouldAlsoReject
--- PASS: TestAssemblerRefusesNonFiniteInputFromThePlatform
ok  github.com/thrasher-corp/gocryptotrader/engine

测试函数计数：
candidate 31 · coverage 30 · httpclient 39 · jev 89 · openai 51 · secrets 20 · snapshot 63
→ 包内 323，加 root 包 6 + engine 交叉验证 3 = 332（session-18 为 311 → +21）

文档：markdownlint docs/llm-dev 36 files → 0 issues
```

**全部离线**：无网络、无数据库、无时钟依赖。

## 9. 给接手模型的具体建议

**`M2-15`（snapshot 组装器）现在是链路里唯一的缺口**，而它需要的东西全部就位：

- `snapshot.BuildIndicatorSet` 接收 `kline.Item` 的 TA 输出（跨层测试已给出转换范式）
- `snapshot.ApplyFreshness` 在交付前跑一次
- `snapshot.SummariseCandles` 造 K 线摘要
- `coverage.BuildCycle` 决定覆盖哪些市场

建议顺序：先把 `engine/llm_indicators_test.go` 里内联的转换提成产品代码，再补 ticker/orderbook/账户的装配，然后接 `ApplyFreshness`。做完它，**`llm/` 里这一整套契约才第一次对着真实交易所数据跑起来**——在那之前它们全是零件。

**做 `M2-15` 时请沿用本会话的做法**：① 转换层放 `engine/`（`llm/` 不许 import `exchanges/kline`，边界测试会拦）；② 数值类断言拿**平台的真实实现**当参照物，别自己造合成值——本会话的 19 位小数缺陷就是这么抓到的；③ 用真实数据之前，先确认自己的合成数据形状**不比真实数据简单**。

**另外两件仍在等外部条件的**：`M0-06`（sqlboiler，前置已就位）与 proto 工具链校准（`TASKS.md` §0.1，挡面最广）。
