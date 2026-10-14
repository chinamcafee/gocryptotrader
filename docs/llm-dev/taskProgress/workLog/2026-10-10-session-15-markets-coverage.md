# session-15：合约覆盖页与范围筛选（W7-04 + M2-12 补充）

**日期**：2026-10-10
**范围**：`W7-04` 全量覆盖轮询状态（前端数据面 + 计划面）、`M2-12` 补充（范围筛选）
**前置**：session-14（M0-05 不可变触发器）完成

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/coverage/scope.go` | `SelectMarkets` / `SummaryFor` / `ScopeSummary` / `ScopeExplicit` |
| `llm/coverage/scope_test.go` | 8 个测试函数 |
| `llm/coverage/plan.go` | **改动**：新增 `ErrUnknownScope` |
| `webUI/src/app/api/coverage.ts` | 范围分类 + 权重算术（`classifySymbols` / `buildRotationPlan` / `klineWeight`） |
| `webUI/src/pages/MarketsPage.tsx` | 「合约覆盖」页 |
| `webUI/src/App.tsx`、`src/components/AppShell.tsx` | **改动**：路由 `/markets` + 侧栏项「合约覆盖」 |

## 2. 修掉一个会让覆盖计划失真的缺口

`Plan.Scope` 字段从 session-10 起就存在，默认值 `ALL_USDTM_PERPETUAL`，**但从未被用来选市场**——`BuildCycle` 只接受调用方给的列表。当时没有真实数据，看不出问题。

本会话拿真实端点核对后发现：

```text
availablePairs（usdtmarginedfutures）= 789 个
  ├─ 尾部 _USDT 且非交割 ......... 588   ← 设计要覆盖的
  ├─ 其它计价（USDC/USD1/法币/代币化股票形状）.. 197
  └─ 交割合约（_261225 / _270326）.. 4
```

混进去的东西不是假数据，是**真的可交易合约**，只是不是这个设计说的那种：

| 类别 | 例子 | 为什么不在范围内 |
| --- | --- | --- |
| USDC 计价 | `BTC_USDC`、`SUI_USDC` | 名义额不是 USDT，而风控信封以 USD 计价 |
| USD1 计价 | `BTC_USD1`、`ASTER_USD1` | 同上 |
| 代币化股票形状 | `AMZ_NUSDT`、`TSL_AUSDT`、`INT_CUSDT` | 计价与前缀都不符合"USDT 本位永续" |
| 交割合约 | `BTCUSDT_261225` | 有到期日，**没有永续资金费率**；资金费是决策证据之一 |
| 非 ASCII base | `币安人生_USDT`、`我踏马来了_USDT` | **这些是真永续，必须在范围内** |

如果照单全收，覆盖计划会按 789 个算权重、把交割合约排进轮询（它们的 K 线在到期后就没有了）、并对 USDT 名义额之外的合约做资金费率推断。

### 2.1 筛选规则

```go
// 尾部 _USDT 且不是交割合约
if datedFutureSuffix.MatchString(symbol) → 交割
else if !strings.HasSuffix(strings.ToUpper(symbol), "_USDT") → 其它计价
else → 在范围内
```

**刻意不用 ASCII 正则限 base**。一个自然的写法是 `^[A-Z0-9]+_USDT$`——它会把 `币安人生_USDT`、`我踏马来了_USDT`、`哈基米_USDT`、`龙虾_USDT`、`牛来_USDT` 全部静默丢掉。这些是真实的、可交易的 USDT 本位永续。用正则限死字母表 = 悄悄把范围缩小到"英文名合约"，而没人能从界面上看出来。

测试 `TestSelectMarketsKeepsNonAsciiPerpetuals` 专门锁这一条。

### 2.2 范围外要计数并给名字

`ScopeSummary` 分三类计数（交割 / 其它计价 / 空）并保留最多 12 个**命名示例**。

理由写在页面上：**「看了 789 个里的 588 个」是个可核对的陈述；静默丢掉 201 个则没人能发现范围定错了。** 计数让人能问"那 201 个是什么"，示例让人能立刻看出"哦，是 USDC 和股票"。

### 2.3 用真实数量重算预算

文档 `04` §7.4 的算术是按 **525** 个合约估的。真实在范围内是 **588**：

| 量 | 文档（525） | 实测（588） |
| --- | --- | --- |
| 单周期总权重 | ≈565 | **638** |
| 轮询速率 | 56.5 /min | **63.8 /min** |
| 整点重载 | 1050 → 210 /min | **1176 → 235.2 /min** |
| 峰值 | 266.5 /min | **299 /min** |
| 预算（60%×2400） | 1440 /min | 1440 /min |
| 余量 | 1173.5 /min | **1141 /min** |

仍然有充裕余量，所以默认参数不用改——但**这个数字必须来自实测而不是沿用文档**，否则第一次真跑时才发现放不下。

`TestScopeBudgetCoversTheLiveExchangeCount` 用 588 个市场断言整条算术链。

## 3. 前端：数据面是真实的，运行面不冒充

「合约覆盖」页（`/markets`）分三块：

| 块 | 数据来源 | 真实性 |
| --- | --- | --- |
| 范围分类（789 / 588 / 5 / 201） | **真实端点** `getexchangepairs` | ✅ 实测 |
| 合约列表（搜索/三态筛选/分页） | 同上 | ✅ 实测 |
| 两层轮询预算（638 / 63.8 / 235.2 / 299 / 1440 / 1141） | `llm/coverage` 算术镜像 | ⚠ **计划值** |

页面在预算块底部明写：

> **这是计划值，不是运行值。** 运行态的轮询进度、最旧数据年龄、实际权重占用需要 `LLMGetCoverageStatus` RPC（被 proto 工具链阻塞），执行器本身是 `M2-14`。

**为什么要把"计划值"标出来**：把算出来的计划数字和实测数字并排展示、不加区分，会让读者以为系统已经在按 588 个合约轮询了。它还没有——`M2-14` 执行器没写。这类"算出来的数字看起来像观测值"的混淆，是仪表盘最常见的失真来源。

### 3.1 点击行 → 跳交易台（实测通过）

`MarketsPage` 写 `selectedMarketAtom` 并 `navigate('/terminal')`。实测点 `1000PEPE_USDT` 的「看盘」：

```text
url        : /terminal
symbolsSeen: ["1000PEPE-USDT", "1000PEPE"]
```

base 从 `1000PEPE_USDT` 正确解析为 `1000PEPE`（`slice(0, lastIndexOf('_'))`），没有把 `1000PEPE` 截成 `1000PEPE_USDT` 或丢掉数字前缀。

## 4. 浏览器实测发现的一处自家 UI 过度承诺（已修正）

第一版页面把"在范围内但未启用"的合约标为 **「按需只读」**，tooltip 说"可只读查询，但不会进入决策流水线（Q4=B）"。

实测点进去：

```text
行情: 请求失败
1000PEPE-USDT currency not enabled
```

网关的取数路径（`GetKlineRequest` / `GetTicker`）会校验 `enabled` 列表。所以 **Q4=B 的"按需只读"现在并不存在**——它需要绕过该校验的取数路径，那是 `M2-01`（"K 线获取共享逻辑，从 rpcserver 抽出供 RPC 与 LLM 工具共用"）的工作。

修正后的页面：

- 标签：`按需只读`（蓝）→ **`未启用`（橙）**
- tooltip：写明"实测网关直接拒绝（`currency not enabled`），所以**现在查不了**；按需只读需要 M2-01 提供绕过启用校验的取数路径"
- 底部告警：`按需只读（Q4=B）`（info）→ **`未启用的合约目前查不了（实测）`（warning）**，并保留最终语义（届时标 `onDemand`，永远不进决策流水线）

**这条值得单独记**：UI 上的一句话就是一个承诺。文档 `04` §7.3 把 Q4=B 写成了已定案的设计，我把"已定案"当成了"已实现"——直到点了一下才发现网关不认。**已定案的设计 ≠ 已存在的路径**。

## 5. 已知重复：`coverage.ts` 与 `llm/coverage`

`webUI/src/app/api/coverage.ts` 是 `llm/coverage/scope.go` + `plan.go` 的**前端镜像**。这是**有意的、有记录的重复**，取舍如下：

- **Go 侧是权威**：30 个测试函数覆盖，含用实测 789-symbol 形状做的夹具与 588 合约的预算断言；
- **重复的原因**：`LLMGetCoverage` RPC 被 proto 工具链阻塞（`TASKS.md` §0.1）。先把真实数据渲染出来，比继续显示"待落地 M2-12"更有价值；
- **漂移风险与消除路径**：规则集中在**一个** TS 文件里而不是散在 JSX 中——如果将来漂移，只可能漂在这一处，与 `llm/coverage` 对照即可定位。打通 RPC 后（`W7-07`）**应删掉本地规则**，改为消费后端返回的 `ScopeSummary`。
- 权重常量（40 / 10 / 2400 / 0.6 / 分档阈值）在两处各写了一份，已有注释互相指认。

**没有**做的事：没有加"JS 常量 == Go 常量"的自动比对测试。当前 webUI 没有任何测试运行器（`package.json` 里只有 typecheck/lint/build），为一条断言引入 vitest 不划算。这条债记在 `W7-07` 里。

## 6. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **运行面**（周期进度、最旧合约年龄、实际权重占用、上次周期完成时刻） | **W7-07**（新拆出） | 本页**没有**这些数字。它们需要执行器（`M2-14`）才有，且出口 RPC 被 proto 阻塞 |
| `LLMGetCoverageStatus` RPC | **W7-07** | 被 proto 工具链阻塞（§0.1） |
| **按需只读（Q4=B）的取数路径** | **M2-01** | 实测被 `currency not enabled` 挡住。需要一个绕过启用校验的路径（原文提的 `UKlineData`），且**必须手工调 `Request.ProcessResponse`** 保证去重/排序/填充一致（文档 §7.3 的告警） |
| 前端**没有测试运行器** | W8 | `coverage.ts` 的算术**只有**类型检查与构建把关，没有单元测试。它的正确性靠与 Go 测试的规则一致性 + 人工核对 |
| `Web UI 消费流式` / 对话工作区 | W5-01、M3-09 | 未开始 |
| 真实轮询（真的去拉 588 个合约） | **M2-14** | 本会话**没有发起任何轮询**。页面上的 638/63.8/235.2 全是算术 |
| `scope` 的可配置性 | 后续 | `ScopeExplicit` 已实现，但配置段（`09` §4.1）里还没有 `scope` 字段的读取路径 |

## 7. 校验证据

```text
# Go
$ gofmt -l llm/                       # 无残留
$ go build ./llm/...                  # 通过
$ go test ./llm/coverage/ -count 1
ok  github.com/thrasher-corp/gocryptotrader/llm/coverage    0.739s
   coverage 包测试函数：22 → 30（+8 范围筛选）；llm/ 全树 295（含 root 包 6 个边界测试）

# 前端
$ pnpm typecheck     # tsc --noEmit 无输出
$ pnpm lint          # Found 0 warnings and 0 errors（26 files, 96 rules）
$ pnpm build         # ✓ 3150 modules transformed, built in 1.02s

# 浏览器实测（真实 bot + 真实代理）
/markets 渲染：789 / 588 / 5 / 201（交割 4 · 非 USDT 197 · 空 0）
             预算：638 / 63.8 / 1176→235.2 / 299 / 1440 / 1141 / 1020ms
点「看盘」(1000PEPE_USDT) → location=/terminal，交易台显示 1000PEPE-USDT
未启用合约选入交易台 → 报错 `1000PEPE-USDT currency not enabled`（这条实测就是修正 UI 的依据）
```

**本轮修掉的三个我自己的错误**：

1. `TestSelectMarketsForWholeExchangeScope` 的排序期望按"直觉"排了 `我踏马来了` 在 `币安人生` 之前——实际 `slices.Sort` 按 UTF-8 字节，`币`(E5B8) < `我`(E688)。**代码对，期望错**。
2. `OtherQuote` 期望 12、实际 10——我在夹具上数错了。**代码对，期望错**。
3. `TestSummaryForReportsWhatTheScopeExcluded` 期望 `Selected=4`、实际 6（夹具里有 6 个 USDT 永续）——同样是我数错。

三次都是"期望值靠记忆写，没有回夹具数"。**教训**：写夹具计数断言时回夹具数一遍，别信直觉。

## 8. 给接手模型的具体建议

优先级与理由：

1. **`M0-06` sqlboiler 模型生成**——表与触发器都在了，生成后 `M0-07` 仓储层才能写，进而解锁 `M1-09`~`M1-13`。纯生成、可离线验证。
2. **`M2-01` K 线取数共享逻辑**——一举两得：它既是 `M2-14` 轮询执行器的前置，也是**实测被挡住的 Q4=B 按需只读**的唯一出路。做的时候记得手工调 `Request.ProcessResponse`。
3. **`W8-04/06`（响应式 + 前端密钥自查）** 与 **`W5-01`（对话工作区骨架）**——不依赖后端，可并行。

**如果要继续做前端覆盖相关**：先看 `webUI/src/app/api/coverage.ts` 顶部的注释。那里写着它是重复实现、权威在 `llm/coverage`、以及消除路径。**加新的前端算术之前先问：这个规则 Go 侧有吗？有就别再写一份。**
