# session-12：生产提示词与架构边界守门（M1-13 + M0-13）

**日期**：2026-10-10
**范围**：`M1-13` 三个生产提示词常量、`M0-13` 架构边界测试
**前置**：session-11（M2-03 snapshot 契约）完成，`llm/` 七包全绿

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/openai/prompt.go` | `PromptAnalysisReadOnlyV1` / `PromptPolicyProposalV1` / `PromptChatAnalysisV1` 三个常量 + 三个版本 ID + `PromptFor` + `PromptVersions` |
| `llm/openai/prompt_test.go` | 8 个测试函数 |
| `llm/openai/errors.go` | **改动**：新增 `ErrUnknownPromptVersion` |
| `llm/boundary_test.go` | 6 个测试函数：stdlib `go/parser` 解析 `llm/` 全树 import，三重断言 |

## 2. M1-13 设计要点

### 2.1 提示词是代码常量，不是配置项

`PromptFor(version)` 是唯一入口，**精确匹配**，前缀不解析（`"analysis-readonly"` 不被解析成 v1——否则未来加了 v10，`v1` 的请求可能拿到错的文本）。

理由写在常量块的 GoDoc 里：每一次 `decision_run` 和 `chat_message` 都存储它运行时的版本号。**改了措辞却不改版本号，会让两个行为不同的决策在审计记录里长得一模一样**，且重放会产生记录无法解释的结果。所以改提示词 = 加新常量 + 新版本号 = 代码变更 + review。

### 2.2 提示词与契约的交叉校验（本会话最有价值的一条）

```go
func candidateKeys(t *testing.T) []string {
	typ := reflect.TypeOf(candidate.Candidate{})
	// 读出每个字段的 json 标签
}
```

提示词里承诺 "Return one JSON Candidate with exactly these keys: …" 后面跟 12 个键。测试**从 `candidate.Candidate` 的真实 JSON 标签生成这个列表**，然后断言提示词里**逐字逐序**包含它。

这抓住了两类真实的漂移：

1. 给 `Candidate` 加了字段，忘了改提示词 → 模型不知道要输出它 → 每个候选都缺字段 → `CANDIDATE_INCOMPLETE`
2. 提示词里留了个已经删掉的字段 → 模型输出它 → 被解析器静默丢弃，而记录里看起来模型"照做了"

第 2 类尤其阴险：`json.Unmarshal` 默认忽略未知字段，所以没有这个测试的话问题完全不显形。

### 2.3 三个提示词的分工不是随意划分的

| 版本 | 输出 | 为什么不能合并 |
| --- | --- | --- |
| `analysis-readonly-v1` | JSON Candidate | 决策路径必须拿到可校验的结构 |
| `policy-proposal-v1` | JSON 提案 | 目标是提取条款，不是判断行情 |
| `chat-analysis-v1` | 自然语言 | 对话路径必须说人话 |

测试直接断言两个模式的**互斥**：对话提示词不能出现 `Return one JSON Candidate`，决策提示词不能出现 `Reply in the operator's language`。

**理由**（写在 `PromptChatAnalysisV1` 的 GoDoc 里）：决策路径要 JSON 且只要 JSON，对话路径要散文且绝不能产出 Candidate。一个提示词同时要求两者，产出的是一个**在决策时想聊天、在聊天时想输出 JSON** 的模型。

### 2.4 对话模式的拒绝条款是安全边界

```text
If the operator asks you to place, modify or cancel an order, explain
that this mode is read-only and point them to the strategy approval flow.
```

对话模式**没有任何通往订单的路径**（工具集只有只读工具）。所以用户要求下单时，正确行为是**明确说明并指向审批流程**，而不是半推半就。这条是模型侧的最后一道，真正的闸在代码里（对话路径不产生 `decision_run`）。

### 2.5 三条比参考项目新增的条款

| 条款 | 为什么必须加 |
| --- | --- |
| `Technical indicators … are evidence about past prices, not predictions and not trade instructions` | 参考项目**没有指标**，本方案有。不加这条，模型看到金叉就当成行动信号——而设计明确禁止把交叉本身当作非 HOLD 的充分理由 |
| `Never treat a crossover by itself as sufficient grounds for a non-HOLD action` | 同上，把"指标是证据"具体到最容易误用的那一种 |
| `Position side is one-way only: OPEN_LONG means buy, OPEN_SHORT means sell` | 币安 wrapper **不传 `positionSide`**，只支持单向持仓。不加这条，模型可能认为存在双向持仓并产出执行层无法表达的意图。测试**反向断言**提示词里不得出现 `positionSide`（那是 wrapper 从来不发的字段） |

### 2.6 提示词不是模板

测试遍历 `%s`/`%v`/`%d`/`%q`/`%w`/`%!`/`{{`，断言一个都不出现。

**理由**：一个在运行时拼装的提示词会让重放不可能——记录里写着一个版本号，而那个版本的文本已经不再是当时的样子。这条约束把"提示词里不能有动态数据"从口头约定变成机器断言。

### 2.7 提示词纯 ASCII

三个常量里任何字符 >127 即失败。理由：英文提示词是给模型看的，中文译文是给人看的、留在设计文档 `05` §6 里。一个漏进去的中文句子意味着译文混进了 payload。

**这也是为什么 `prompt.go` 里没有中文译文副本**：指令的第二份拷贝就是第二个要同步的东西，而模型读的那份才是要紧的。

### 2.8 提示词不得携带探针标识

测试断言三个生产提示词都不含 `PROBE`、`probeSnapshotID`、`probeStrategyVersionID`、`probeToolName`。

**理由**：探针的合成标识（`00000000-0000-4000-8000-00000000beef`）如果出现在生产提示词里，模型可能把它回显进真实候选的 `evidenceRefs`，然后这条证据在解析时找不到对应快照。探针提示词（`probeSchemaSystemPrompt`）是独立常量，两者不得混用。

## 3. M0-13 设计要点

### 3.1 用 stdlib `go/parser`，不引入 `x/tools`

go.mod 里没有 `golang.org/x/tools`。为了一个边界测试引入它会**反向增加** `llm/` 的依赖面，与测试的目的冲突。

`go/parser` 的 `parser.ImportsOnly` 模式足够：只解析 import 声明，不解析函数体。代价是只看**直接** import 而非传递闭包——但这正是要守的规则（"`llm/` 不得直接 import `engine`"）。

好处：**离线、秒级、没有 `go list` 子进程**，而且**在文件还编译不过的时候就能报出边界错误**——这是发现边界错误最便宜的时刻。

### 3.2 三重断言，各管一段

| 断言 | 形式 | 抓什么 |
| --- | --- | --- |
| `TestLLMPackagesImportOnlyTheAllowedPrefixes` | **穷尽式白名单** | 任何新的内网依赖。allowlist 只有 7 项：`llm`、`llm/`、`types`、`currency`、`exchanges/asset`、`common`、`logging` |
| `TestLLMPackagesDoNotImportTheBlockedPackages` | **带理由的黑名单** | 具体包，失败信息直接说"你导入了引擎" |
| `TestLLMTreeDoesNotDependOnAThirdPartySDK` | **第三方白名单** | 供应商 SDK、HTTP 框架、链路追踪客户端——它们往往自带 transport，会绕过加固过的 HTTP 层 |

白名单是穷尽式的（不在列表上就失败），**这是设计意图不是误报**。加新的内网依赖时先在 `allowedInternalPrefixes` 里写理由。

### 3.3 白名单里为什么没有 `exchanges/kline`

这是 session-11 的发现，在这里被钉死：`exchanges/kline` import `exchanges/order`（因为 `CreateKline` 收 `[]order.TradeHistory`）。于是 `llm/snapshot` → `kline` → `order`，`llm/` 再也抽不出去。

所以 `snapshot` 包定义了自己的 `Interval` 字符串枚举。白名单把 `kline` 排除在外，并在注释里写明原因——下一个想用 `kline.Interval` 的人会在这里读到为什么不行。

### 3.4 三重防真空

一个基于文件系统遍历的测试最大的失败模式是**空转变绿**：

1. `require.Positive(t, walked, ...)`：一个 Go 文件都没解析到就失败
2. `assert.NotEmpty(t, seenInternal, ...)`：一条内网 import 都没看到就失败
3. `TestImportParsingFindsKnownEdges`：断言解析器**确实看得见** `snapshot → llm/candidate` 这条存在的边
4. `TestCollectImportsSeesTestFiles`：断言源文件与 `_test.go` **都**被解析到

第 4 条对应一个真实风险：如果 walk 跳过 `_test.go`，写在测试文件里的 `import _ "…/engine"` 就不会被发现——而它同样破坏可抽出性。

### 3.5 实测注入验证

不是"测试通过了"，而是"测试会失败"。本会话实际做了：

```text
临时写入 llm/snapshot/zz_violation_probe_test.go：
    import (_ "github.com/thrasher-corp/gocryptotrader/engine")

$ go test ./llm/ -run 'TestLLMPackages'
--- FAIL: TestLLMPackagesImportOnlyTheAllowedPrefixes
    disallowed import
--- FAIL: TestLLMPackagesDoNotImportTheBlockedPackages
    blocked import
FAIL

删除探针文件后：
ok  github.com/thrasher-corp/gocryptotrader/llm
```

**一个从未失败过的测试，其有效性是未知的。**

## 4. 本会话修的问题

| # | 现象 | 判定 | 处理 |
| --- | --- | --- | --- |
| 1 | `TestAnalysisPromptNamesEveryCandidateKey` 失败：提示词里找不到那个 12 键的连续串 | **测试错了**：提示词按可读性折行，键列表跨了两行 | 加 `flattenPrompt`（`strings.Join(strings.Fields(s), " ")`）在断言前折叠空白。否则每条断言都会把当前的折行方式编码进去 |
| 2 | `TestPolicyPromptRefusesToInventValues` 失败：找不到 `ask a clarifying question` | 同上（提示词里是 `ask a clarifying\nquestion`） | 同上 |
| 3 | 白名单拒绝了 `github.com/thrasher-corp/gocryptotrader/llm`（根包） | **白名单写漏了**：`llm/llm.go` 存在，装着跨包共享的哨兵错误（`ErrInvalidResponse` 等），`candidate/evidence.go` 用它 | 补 `modulePath + "/llm"` 条目。（也顺手改掉了我给 `boundary_test.go` 写的错误包注释"deliberately contains no non-test files"——`llm.go` 就在那儿） |

第 3 条值得一提：我**先**写了断言"这个目录里没有非测试文件"，**然后**被测试自己纠正了。这正是机器化断言的用处。

## 5. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **"风控不 import 模型客户端"那一半** | **M4/M5** | `TASKS.md` 对 M0-13 的描述是"import 边界：风控不 import 模型客户端等"。风控引擎（`llm/risk/` 或类似）**还没有**，那一半无法断言。本会话交付的是 `llm/` 侧的边界。**建议 M4 落地时在同一文件里加对称断言** |
| 第三方白名单的注入验证 | — | **未独立验证**。注入验证只覆盖了内网包那条路径（`engine`）。第三方白名单走同一 `collectImports`，但有自己的根路径归一化逻辑，未做注入测试。风险低（逻辑更简单），但**如实记录** |
| `policy-proposal-v1` 的字段级约束（长度/数量/范围上下界） | **M4-01** | 提示词文本做完了；`PolicyProposalSchema` 的数值约束（`objective` ≤1000、`marketScopes` ≤8、`maxSlippageBps` 1-1000 等）属 M4-01，本次只在 `TASKS.md` 的需求描述里 |
| 探针提示词与生产提示词的关系 | — | `probe.go` 的 `probeSchemaSystemPrompt` / `probeSchemaUserPrompt` 是**探针专用**，不在 `prompt.go`，也不在 `PromptVersions()` 里。**不要**在 M4 里把它们当生产提示词取用 |
| 提示词的真实模型效果 | **M1-15** | 三个提示词**从未发给任何真实模型**。结构属性（键列表、条款存在性、ASCII、非模板）已机器化验证，但"模型是否照做"只能靠 `M1-15` 的实机冒烟 |

## 6. 校验证据

```text
$ gofmt -l -w llm/            # 无残留
$ go build ./llm/...          # 通过
$ go vet  ./llm/...           # 无输出
$ go test ./llm/... -count 1
ok  github.com/thrasher-corp/gocryptotrader/llm           0.513s
ok  github.com/thrasher-corp/gocryptotrader/llm/candidate  1.560s
ok  github.com/thrasher-corp/gocryptotrader/llm/coverage   1.838s
ok  github.com/thrasher-corp/gocryptotrader/llm/httpclient 1.773s
ok  github.com/thrasher-corp/gocryptotrader/llm/jev        2.155s
ok  github.com/thrasher-corp/gocryptotrader/llm/openai     1.795s
ok  github.com/thrasher-corp/gocryptotrader/llm/secrets     2.506s
ok  github.com/thrasher-corp/gocryptotrader/llm/snapshot    0.871s

边界测试注入验证：见 §3.5

测试函数计数：llm(root) 6 · candidate 31 · coverage 22 · httpclient 39 · jev 89 · openai 49 · secrets 20 · snapshot 29  → 290
```

## 7. 给接手模型的具体建议

**`llm/` 现在没有任何可以纯离线完成的工作了。** 三件事可选：

1. **`M0-04` 8 个 PostgreSQL 迁移**（**推荐**）：解锁 `M1-09`~`M1-13` 里剩下的 DB 相关项，DDL 可用 `dbmigrate` + 本地 SQLite/Postgres 验证。表结构在 `docs/llm-dev/09-数据存储与配置.md` §2-§5，`provider_probe_results` 的列在 `05` §3，`ProbeResult` 的字段已对齐。
2. **`M2-15` snapshot 组装器 + `M2-14` 轮询执行器**：需要 exchange manager 接线。本地 bot 已经能跑（`PROGRESS.md` §7 有命令），可以真实往返。
3. **前端 `W5-01` / `W7-04` / `W8-04/06`**：不依赖后端，可并行。

**给 `llm/` 加新内网依赖前**：先读 `llm/boundary_test.go` 的 `allowedInternalPrefixes`。它失败是正常的，按里面的说明加理由即可。
