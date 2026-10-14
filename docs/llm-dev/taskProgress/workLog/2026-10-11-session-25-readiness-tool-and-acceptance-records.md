# session-25 工作日志：M6 开工——验收记录与准入检查命令

**日期**：2026-10-11
**主题**：`M6-01`（14 项验收逐项演示记录）+ 11 §8 要求的准入检查命令 `gctcli llm readiness`

---

## 1. 本会话交付

| 交付物 | 文件 | 说明 |
| --- | --- | --- |
| 验收演示记录 | `docs/llm-dev/taskProgress/acceptance/M6-01-A01-A14-demo-records.md` | A01–A14 逐项：演示内容 / 离线证据（可复跑测试名）/ 运行演示步骤 / 诚实状态（9 ✅ 离线已验证 · 3 🟡 部分离线 · 2 🔴 待运行面） |
| 准入检查引擎 | `engine/llm_readiness.go` + `engine/llm_readiness_test.go`（9 测试） | `EvaluateReadiness` 纯函数（十条门，固定顺序）+ `LLMReadinessService`（事实源接缝 `iReadinessFacts`） |
| RPC | `gctrpc/rpc.proto`（4 消息 + 1 RPC）· `engine/rpcserver_llm_strategy.go`（`LLMGetReadiness`） | `GET /v1/llm/readiness`；proto+handler 原子落地 |
| CLI | `cmd/gctcli/llm_strategy.go`（`readiness` 子命令） | `gctcli llm readiness --network binance-testnet [--mode TESTNET] [--json]`；READY→0 / NOT_READY→2 |
| 引擎接线 | `engine/engine.go`（`llmReadiness` 字段） | 未装配→RPC fail-closed（`llm readiness is not configured on this bot`） |

## 2. 设计决策记录

1. **准入命令与 pre-gate 同构**（M4-07 模式）：判定是纯函数、事实由调用方供给、零值 fail-closed。区别：pre-gate 拦「单次决策花钱」，本组门拦「整机上测试网」。
2. **十条门**（固定顺序即 CLI 打印顺序）：`INPUT_COMPLETE` → `MODE_PERMITTED` → `PAIR_READY` → `PROBE_FRESH` → `NETWORK_CONSISTENT` → `NO_FULL_HALT` → `RISK_SWITCHES_INTACT` → `ACCOUNT_RECONCILED` → `DAY_START_SEALED` → `COVERAGE_PRESENT` → `APPROVAL_LIVE`（后十条在有输入时共 10 行；输入不完整时只报 1 行）。
3. **门与 A 项挂钩**：PAIR/PROBE→A06–A09 前置；NETWORK→A11 前置（TESTNET 不许误连主网）；SWITCHES→底线 4/5；APPROVAL→A04 闭环；HALT→A14 不带病上岗。
4. **每条门的详情=实际值 vs 期望值**（A12 的读法）：例如 `pair readiness "DEGRADED", want READY (Q3: no degraded path)`、`locked switch(es) off: blockOnUnknownOrders (red lines 4/5: …)`。
5. **结构性豁免要有理由**：SHADOW/PAPER 豁免 NETWORK 与 APPROVAL 门——它们根本没有场地下单路径（M5-13 同一语义），不是放水。
6. **服务层只聚事实不做判断**：`LLMReadinessService.Evaluate` 调事实源→补默认（Network/Mode/Now）→交给纯函数。事实源返回错误=错误上抛（读不到事实的准入不是裁决）；事实源齐全但值为零=各门自行 fail。
7. **准入命令是 M6-02 的词汇表**：20 项失效矩阵演练的每一项「响应符合预期」都能落成某门 FAIL 或某拒绝码。
8. **验收记录不粉饰**：9 项离线可证（测试名逐项列出可复跑）、3 项部分（A02 数值等价离线已钉死/对话链路待装配；A13 链路写入离线 ✅/含真实成交待 A11；A14 序列离线 ✅/撤单适配器属装配）、2 项待运行面（A01 实时性、A11 测试网全链路）——待生产装配 + 测试网凭据后按记录 §4 逐项补记。

## 3. 验证证据

- `go test ./engine/ -run 'TestEvaluateReadiness|TestReadinessService' -count 1` → **9 全过**（全绿门 + 固定门序 + 输入不完整 + 逐门单点失败：配对三态/探针五态/网络三态/停机两态/开关两态/对账三态/日界三态/覆盖/审批两态 + 服务层缺省补位/错误上抛/未装配 fail-closed）
- `go build ./...` 通过 · `go vet ./engine/ ./cmd/gctcli/ ./gctrpc/` 无输出 · `gofmt -l`（修正一处测试文件对齐后无输出）
- `cd gctrpc && PATH="$HOME/go/bin:$PATH" buf generate` **连跑两次同哈希** `3155e9863b24fe61a77ae987469666f57738529f45dd4a31992709593e35bd41`（新增 4 消息 + 1 RPC 后重新锚定；此前基线 `f3ecb7ad…`）
- `go test ./cmd/gctcli/ -count 1` 全过
- `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过（101.6s）**
- `npx markdownlint-cli2@0.23.2 docs/llm-dev/taskProgress/acceptance/*.md` → **0 issues**

## 4. 本会话新增的机器化约束

- **准入是独立动作**：整机上测试网的门禁不是决策 pre-gate 的复用——它检查的是「这台机器现在能不能上」，十条门全部 PASS 才 READY（11 §8 的退出码契约 0/2 落在 CLI）。
- **门的顺序是合同**：报告顺序固定（测试逐个断言 code 与顺序）——脚本与 UI 依赖它做 diff。
- **门只报实际值 vs 期望值**：每行详情都要能回答「差在哪」，不许只有 PASS/FAIL 没有原因。
- **零值 fail-closed 到门级**：事实源给了空值不报错、由对应门独立判 FAIL——一份报告尽可能列全所有问题，而不是在第一个缺失处炸掉。
- **事实源错误是错误不是失败门**：读不到事实（DB 故障）→ RPC 报错；读到空事实 → 门 FAIL。两者不许长得一样。
- **验收记录按可复跑证据说话**：每项 ✅ 必须列出可复跑的测试函数名；不能复跑的（实时性/真成交）一律 🟡/🔴 并写清补记步骤——「不把单测通过假装成端到端通过」。

## 5. `M6-02` 失效矩阵演练（同会话续跑）

| 交付物 | 文件 | 说明 |
| --- | --- | --- |
| 演练记录 | `docs/llm-dev/taskProgress/acceptance/M6-02-failure-matrix-drills.md` | 10 §7 全 21 行逐行：触发方法 / 预期响应 / 证据 / 状态；✅15 · 🟡3 · 🔴2 · 📋1 |
| 演练修复（发现①） | `engine/llm_approval_expiry.go` + `engine/llm_approval_expiry_test.go`（3 测试） | 行 14「审批提前 30 分钟提醒」此前仅有事件构建器（`StrategyExpiring`）与 gauge 常量、**无扫描器**；同日落地 `ApprovalExpirySweeper` |

**关键决策**：

1. **演练以可复跑测试为「人为触发」**：离线可触发的行直接用既有测试作触发源（可复跑、可变异）；真网络/真交易所行写成演练步骤待装配补记——与 M6-01 同一诚实纪律。
2. **提醒与状态变更分级**：`ApprovalExpirySweeper` 是 info 级提醒、**不改变任何状态**——用内存去重（同进程每张审批恰一次；重启最多重复一次）不写审计行；改变状态的（REVIEW→EXPIRED）才落审计（M5-18 的分界）。
3. **窗口语义**：`(now, now+30min]` 含边界；剩余分钟**向上取整**（29m30s → 30——提醒不许少报时间）；出窗剪除提醒记忆（撤销/过期/续期各自的 id 自然出窗）。
4. **三项发现如实登记**：①行 14 缺口当日修复转 ✅；②行 5「持续 >300s 数据过期 → SOFT halt」升级器未实现（per-decision HOLD 已生效；升级依赖调度器/看门狗接线）；③行 18 市场下架事件生产者未接线（与 `M2-13` 残余同源）。另有口径记录④：DB 不可写时 halt 自身也写不下去——有效响应=写路径全链 fail-closed，不误判为缺口。

**验证证据**：

- `go test ./engine/ -run 'TestApprovalExpirySweep' -count 1` → **3 全过**（一次一提醒/窗口边界与 ceil/出窗剪除/误用与错误上抛/gauge）
- `gofmt -l`（一处测试文件对齐修正后无输出）· `go vet ./engine/` 无输出
- `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过（105.4 秒）**

## 7. `M6-10` `make check` 全绿（同会话续跑，**进行中**）

**背景**：`make check` = `lint`（golangci-lint v2.13.2，仓库 `.golangci.yml`：gocritic performance/style/experimental 全开 + depguard + revive + gosec 等）+ `misc_checks` + `markdownlint` + `test`（`go test -race ./...`）。本项目的 LLM 代码从未跑过 golangci-lint——首跑 **669 条 findings**，全部落在本项目新增文件（`llm/`、`engine/llm_*`、`database/repository/llm`、`cmd/gctcli`、`communications/webhook`、`config`），上游文件干净。

**已清掉的类别（669 → 260；再收口 5 条杂项后余 gocritic 210 + revive 45）**：

| 批次 | 内容 | 结果 |
| --- | --- | --- |
| misc_checks（与 lint 无关但同属 `make check`） | ① `config_example.json` JSON 排序（`make lint_configs` 自动修复）② 全树 22 个文件剥离 U+FE0F（VS16，⚠ emoji 变体选择符；检查项禁用 Cf/Z/M 类不可见字符）③ 7 处 `currency.NewPair(BTC,USDT)`→`currency.NewBTCUSDT()` ④ 136 行 testify 断言补 `f` 变体（3 处字面 `%` 转义 `%%`）⑤ 795 行 assert 消息 `must`→`should`（仓库约定：require 用 must、assert 用 should） | **9/9 PASS**（CI 等价树：临时移出 `webUI/node_modules`、`webUI/dist` 两个 gitignored 构建产物后跑；跟踪文件 0 命中） |
| depguard（64） | `encoding/json` → 仓库中间件 `gocryptotrader/encoding/json`（sonic/json-v2 可切换）；`shopspring/decimal` → `gocryptotrader/types/decimal`（别名构建）。**两个 API 缺口**：中间件 Decoder 无 `UseNumber`（evidence.go 改为不依赖，扫描只看键/深度）；无独立 `Compact`（tools.go 用 `RawMessage.Compact()` 就地压紧，先拷贝）。`llm/boundary_test.go` allowlist 增加 `encoding/json` 前缀（附理由） | 62 文件 64 处 import 互换，编译与测试全绿 |
| `golangci-lint run --fix` | modernize/perfsprint/misspell/gofumpt/gci/goimports/intrange 大部分/testifylint 部分自动修复 | 399 → 修复 3 处**误伤**：`assert.Equal(CodeInvalidJSON,…)` 被错误转成 `JSONEq`（probe_test 3 处，回退并加 `//nolint:testifylint` 说明）；1 处 unconvert 剥掉了 `. (string)` 断言（回补）|
| 手工批次一/二 | 真 bug 3 处：`RetryAfter` 空接收者返回 `g.window`（→0）、llm_snapshot 重复 nil 检查块、errname 类型改名；kline cache 4 处断言改 `cacheEntryOf` 助手；webhook `now` 字段删除 + `NewRequestWithContext`；goconst 词汇常量（msgRoleSystem/msgRoleUser/auditActorSystem/orderSideSell + 仓储列名 + CLI flag 常量）；`fetchClosedWindow` 去掉未用返回值；`collectImports` 去掉恒 "." 参数；`UseDb`→`UseDB`（引擎内部结构；**proto 字段仍是 UseDb，勿改**）；tparallel 子测试独立夹具 + `t.Parallel()`；dead code 删除（toDecimal/nowUTC/keyCandidateAction/compactJSON 及 5 个测试助手）；llm_tools_test 36 处断言改 `asMap/asList/asString` 检查助手 | 编译/vet/gofmt 全绿；`TestLLMTools`/`TestLLM` 相关全绿；`go vet` 无输出 |

**关键坑（后续接手必读）**：

- `git grep -P` 在本机（`LC_CTYPE=C`）是**字节模式**——`\p{Z}` 会匹配任意高位字节，产生假命中；判断不可见字符要用显式 UTF-8 扫描（perl `-T`/python），不要信 git grep -P 的原始输出。
- `--fix` 的 testifylint `encoded-compare` 会对**常量名含 JSON 但值不是 JSON** 的 `assert.Equal` 误报/误修——回退为 `Equal` + `//nolint:testifylint`。
- misc_checks 的不可见 Unicode 检查扫全树：本地 `webUI/node_modules`、`webUI/dist`（gitignored、CI 不存在）必然命中——本地跑前临时移出即可（验证配方）。
- **golangci-lint 的 `make lint` 管道会掩盖退出码**——必须 `>| 文件; echo $?` 捕获（本次第一遍被 `| tail` 骗过一次）。

**revive 已归零（45 → 0）**：redefine-builtin（`cap`/`max`/`close` 17 处局部名改 `cap24h`/`allowed`/`ceiling`/`capStr`/`limit`/`top`/`closeAt`）、unused-parameter 18 处改 `_`、exported 注释 9 处按名字开头改写、`applyAccount` 的 ctx 提到首参。

**gocritic 进行中：214 → 122（复活性检查）**。已完成波次（配方固定为「签名值→指针 + 夹具/factory 改返回指针 + 编译器/`go vet` 报错驱动自动补 `&`」，脚本 `/tmp/autofix_amp.py` 在本机可复用；**注意 vet 报错行带前缀需先剥掉（前缀是 vet 加冒号）**）：

- 波 1：`engine/llm_readiness.go`（`EvaluateReadiness` 与 10 门全改 `*ReadinessInput`；夹具 `readinessGreenInput() *ReadinessInput`；门报告助手改指针）——13 条清零，readiness 测试全绿。
- 波 2：`engine/llm_snapshot.go`（`SnapshotRequest`×5 签名 + `IndicatorParams`×2 + `candleFact(*kline.Candle)`；夹具 `baseRequest() *SnapshotRequest`；sloppyReassign 9 处 `err =`→`err :=` 收口）、`llm_tools.go`（7 个工具执行器 unnamedResult → `(result any, asOfAt time.Time, refID *string, err error)`——**取名必须避开体内同名局部量**，首版用 `payload/asOf/ref` 与体内局部冲突）、`decisionAndReason`/`splitIntentMarketID`/`intentSide` 具名返回。构建/vet/聚焦测试全绿。

- 波 3：`llm_decision_record`（三 adapter + `Record`/三个内部函数收 `*DecisionRecordInput`；store 行类型 `DecisionRun/RiskEvaluation/AuditEvent` 引擎侧指针化）、`llm_execution`（`BuildIntent`/`Record`/`limitPrice` 收 `*IntentPlan`；`InsertOrderIntent` 指针）、`llm_protective_exit`（`BuildProtectiveExit`/`Enforce`/`exitSubmit`/`failSafeSubmit`/`audit` 全指针，`exit *llmstore.ProtectiveExit`、`parent *llmstore.OrderIntent`）。**共享 `iAuditEvents` 缝全端指针化**（halt/mode/dispatch/day_start/pair_switch/recovery/review_expiry/reconcile/protective_exit/risk_switches 等 14 处字面量与 4 个 adapter 一并改）。
- 波 4：**仓储层系统性指针化**（`database/repository/llm` 的 14 类行类型 × Insert/Try 函数：`Strategy/StrategyVersion/StrategyApproval/StrategyStateTransition/MarketTradeGrant/ProviderProfile/ProviderRevision/DayStartInventory/ExternalFlow/Reconciliation/Halt/BudgetReservation/Conversation/ChatMessage/NewRiskCounter/MarketSnapshot`），引擎 seam 接口（`iHaltStore.InsertHalt`、`InsertReconciliation`、`InsertStrategyStateTransition`）同步指针化，测试假件与 26 处字面量由编译/`go vet` 报错驱动补 `&`。**踩坑**：仓测试里未指针化的 `InsertProfile/InsertProfileRevision/InsertProbeResult/InsertPairSwitchCommand` 被全局 `&` 扫误加，靠「`cannot use &… as VALUE`」反向循环逐处回退——**批量 `&` 扫后必须跑 `go vet ./...` 全树并回退误加项**。

**剩余 175**：gocritic hugeParam（`SnapshotRequest` 已清，余 `DecisionRecordInput 288`、`RiskInput 264`（已随 readiness 同构但未动 risk 主函数）、`IntentPlan 272`、`ProtectiveExit 184/448`、仓储行类型 96–328 等）、unnamedResult 余量、rangeValCopy ~9、regexpSimplify 2、importShadow 2、ifElseChain 2 等。**下一步**：按文件继续波 3（llm_decision_record / llm_execution / llm_protective_exit / 仓储 provider·strategy·reconcile / llm 包 snapshot·jev·coverage），全部清到 0 后跑完整 `make check`（含 `-race`，引擎两个 flaky 网络测试按 README §4.3 处置）。

### 7.1 事故与修复（同会话，重要教训）

**脚本化「删除未用函数」两次误删代码（旧版 delete_func 用“找下一个顶层 `\n}\n`”定位函数结尾）**：

1. `llm/openai/probe.go`：`float64Ptr` 是单行函数（且带 `//go:fix inline`），旧脚本从它起删到**下一个多行函数的收尾大括号**——连带剪掉了 `probeSchemaSystemPrompt` 的声明头与 `probeSchemaUserPrompt`。**修复**：按存活残片（"Rules that are checked automatically:" 起的 5 条规则原文逐字保留）+ 测试约束（禁泄漏词表：btc/eth/usdt/binance/hyperliquid/100000/1h/1m/"bid"/"ask"；SCHEMA 层“什么数据都不给”的要求）**重建两个常量**并注明为重建；`llm/openai` 全包测试转绿。
2. `engine/llm_risk_test.go`：`sp`/`ip` 单行助手同因被剪，连带删掉 M4-11 的**合规夹具 `riskOKInput`**（17 个测试共用，逐项变异触发 61 个拒绝码）。**修复**：按测试文件的全部变异集逐字段反推重建夹具（含各检查阈值：点差 10bp、指数 50bp、滑点 20bp、资金费 0.1%、ATR 3%、排名 ≤50、单笔窗口 6/单版窗 10/缩减 5、new-risk 2、并发仓 1、日损基线 1000±flows、保证金缓冲 10bp 等）；一处自纠，`NewRiskIntentsInWindow=3` 期望失败 ⇒ 装置上限应为 2（计数不含本单）。`TestRisk*` 17 测试 / 95 条断言全绿。
3. **教训（写给下一个用脚本批量改代码的人）**：批量删除函数必须**逐行解析**（单行函数体、泛型 `func name[T ...]`、`//go:fix inline` 注释），绝不能用“找下一个 `\n}\n`”猜结尾；且脚本必须**先全部断言、后统一写入**，或逐文件原子替换——本事故正是"边断言边写"在断言中断时留下的半成品状态被下一次运行二次处理所致。

**Round-3 已完成**（307 → 260，另收口 gofumpt×2 / prealloc / testifylint-empty / usetesting 杂项）：gosec G115 全部收口（测试计数器经 `digitString` 助手、配置 `uint64→int` 加 `min(…)` 饱和守卫、metrics G101 指标名 nolint 理由）；nilerr 9 处 `//nolint:nilerr` 带语义理由（拒绝即成功裁决）；unused 8 个助手/常量删除；goconst CLI/引擎/仓储收口（`flagNameVersion`/`flagUsageActor`/角色与审计员词汇常量/列名常量）；dogsled 用 `chatTestPipelineOnly` 与 `requireMACDStateError`（顺带补强断言）收口；prealloc/rowserrcheck/copyloopvar/intrange/encoded-compare/forcetypeassert 各就位；`go vet` 干净。

**当前状态（round-4/5 后）**：`golangci-lint run` **0 issues**（`make lint` 退出码 0）；`go build ./...`、`go vet ./...` 无输出；`engine`（-skip 两个既有网络 flaky）**133.3s 全绿**；`llm/...`、`config`、`cmd/gctcli`、`database/repository/llm` 全绿；`make misc_checks`（CI 等价树）**All passed**（另修：`webUI/README.md` 列表编号 5→8、`llm/chat/context_test.go` assert 消息 must→should、session-25 workLog 自身 VS16）；`markdownlint`（CI 等价树）**0 error**；`go test -race ./...` 全仓（CI 等价树 + 两个既有 engine flaky 带 -skip）**已跑完**：除 live 网络类外全绿——run-1（带 node_modules）失败集 = backtester/coinbase/deribit/gateio + `TestRootPathFromCWD`；run-A（CI 等价树）`TestRootPathFromCWD` 转绿、失败集漂移到 kucoin/okx/backtester；kucoin 复跑通过（flaky 实锤）；okx 两测（`TestGetPositionTiers` 错误码 51634、`TestGetInsuranceFundInformation` 空表）与 backtester 两测（`TestSetupFromConfig` OKX 51001、`TestExecuteStrategyFromConfig`）**在 HEAD 干净 worktree（`git worktree add /tmp/gct_baseline 492e8f06`，零本地改动）原样复现**——定性为环境/交易所侧常量差，与本改动无因果（相关目录 `git status` 亦为零改动）。`--fix` 之后又修了 4 处自身引入的回归（probe_test ×3、rpcserver_test UseDb ×3、boundary root ×1、tools_test 断言 ×1）。

**Round-4/5 收口（同日续跑，669 → 0）**：剩余大头是 hugeParam（值→指针），分两波机器化推进——**WaveA**（引擎内小签名）与 **WaveB**（`/tmp/lint_waveB.py`：仓储 6 个 provider 函数 + 引擎 ~60 个签名 + 测试假体）。调用点由三代编译错误驱动修点器完成：`autofix_amp.py`（`&` 插入/收回）→ `autofix_amp2.py`（补 `&d`/`*req.Live` 反向型与 `…` 字面量，`vet:` 前缀剥离）→ `autofix_amp3.py`（双向 argument/assignment/struct-literal 四象 + 结果集转向 `go test -c` 编译测试二进制，避免全仓 `go test -run '^$'` 每轮 2-4 分钟的枚举）。夹具约定：**被「指针上下文」与「值字段上下文」共用的夹具一律改为返回指针**（`riskOKInput`/`passingPreGateInput`/`readyPolicy`/`freezeTestPolicy`/`sizingTestInput`/`closeFact`/`loopRequest`/`dispatchTestIntent`/`mapTestIntent`/`jevTestRequest`/`analysisRequest`/`validLLMParams`/`validJevParams`/`approvalTestRequest`），接口同步指针化（`iChatHistory.AppendMessage`、`iChatConversations.Create`、`iDayStartStore` 两方法、`iProtectiveExitCanceller.CancelProtectiveExit`、`iMarketGrants.Create`、`iStrategyVersions.RecordTransition`、`iApprovals.InsertApproval`）。收尾 21 条人工清：`trigger.Event` 三函数指针化（含队列 `append(q, *event)`）、`cardToProto` 指针、`planPtr`/`candlePtr` → `new(x)`（modernize）、`decisionRunToProto(&*run)` → `run`、`decisionInputHash` 去恒 nil error、`exitRowPtr` 去恒定参数。

**Round-4/5 的四个坑（都踩过）**：①指针化的接口方法在 `*T` 与 `T` 两侧各报一次「does not implement」，脚本会把 `*canceller` 反向「修」回去——**接口签名先行、再跑修点器**；②`&&a`/`&&llmstore.`（三连指针）与 `*append(...)`（单字符 token `a` 命中 `append` 首字母）需目检扫描；③`go vet` 每包只报第一条类型错误，`vet` 静默 ≠ 测试可编译——**以 `go test -c -o /dev/null <pkg>` 为准**（本轮因此多花了一轮全仓枚举）；④对「永不改变」的参数（`exitRowPtr` 的 `id`）与「恒 nil」的 error 直接瘦身，别用 nolint 糊。

## 8. 未完成与下一步

- **`M6-10` 剩余的 307 条 lint**（见 §7 剩余清单）→ 清到 0 后跑 **`make check` 全链路**（lint + misc_checks + markdownlint + `go test -race ./...`；引擎两个既有 flaky 网络测试按 README §4.3 带 `-skip` 记录，`make test` 全量跑完后单独复跑确认两类失败）。
- **`M6-02` 三类未过行的补记**：🟡 #5/#8/#10 的升级器接线、🔴 #7 真限频、#18 事件生产者——全部挂生产装配；#15 真 kill -9 归 M6-06、#20 归 M6-07。
- **`M6-03`（人工标注评估集）依赖 SHADOW 运行数据**（生产装配后）：先做标注工具与协议准备，数据齐后抽样 ≥100 条。
- **下一候选（纯本地可做）**：`M6-09` 文档定稿；`M6-11` 两个基线策略可先落实现与对照器。
- **M6-01 的 🔴/🟡 项**：待生产装配（client factory）与测试网凭据就位后按验收记录 §4 补记。
- 并行余项不变：前端 `W5-01` 收尾、client factory 生产装配、`M1-14`/`W7-07`（proto 已解锁）、`M2-13` 两残余（与 #18 同源）。
