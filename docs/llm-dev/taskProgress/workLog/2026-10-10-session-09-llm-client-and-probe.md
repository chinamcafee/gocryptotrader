# session-09：LLM 客户端与四层探针（M1-02 + M1-03）

**日期**：2026-10-10
**范围**：`M1-02` OpenAI 兼容客户端、`M1-03` LLM 分层探针（后端 `llm/openai/`）
**前置**：session-08（M1-01 密钥 vault）完成，`llm/` 四包全绿

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/openai/types.go` | `ChatRequest`/`ChatResponse`/`Message`/`ToolSpec`/`ToolCall`/`ToolCallFunction`/`ToolChoice`/`ResponseFormat`/`Usage`/`Choice`/`ErrorEnvelope` + 角色/结束原因/工具选择/响应格式常量 |
| `llm/openai/errors.go` | 14 个哨兵错误（含 `ErrTruncated`/`ErrRefused`/`ErrToolCallUnsupported`/`ErrToolRoundIncomplete`/`ErrStreamUnsupported`/`ErrCandidate*`） |
| `llm/openai/client.go` | `Profile`/`NewClient`/`ChatCompletion`/`Post`/`ParseChatResponse`/`ContentOf` |
| `llm/openai/probe.go` | `ProbeLayer*` 四层常量 + `ProbeResult` + `Probe`/`ProbeAll` + `probeAuth`/`probeToolCall`/`probeSchema`/`probeStream` + `ParseSSE` + 22 个稳定错误码 + `ErrorCode` |
| `llm/openai/client_test.go` | 24 个测试函数 |
| `llm/openai/probe_test.go` | 22 个测试函数（含 31 条错误码映射逐项断言） |
| `llm/httpclient/httpclient.go` | **改动**：新增 `type SecretFn func(ctx) (string, error)` |
| `llm/jev/client.go` | **改动**：`type SecretFn = httpclient.SecretFn`（类型别名，零破坏） |
| `docs/llm-dev/05` | **改动**：§3 探针表 ④ 层失败码补 `STREAM_UNSUPPORTED`；§9 词表补 `STREAM_UNSUPPORTED` 与 `REQUEST_TOO_LARGE` |

## 2. 设计要点（为什么这样做）

### 2.1 客户端只做"能不能读"，不做"能不能信"

`ChatCompletion` 只负责：填默认值 → 校验请求 → 发送 → 解码 → 形状检查。它**不解释** content 是不是一个合法的 `Candidate`。理由：同一个客户端要被探针、对话路径、决策路径三方复用，如果客户端内置了决策的接受规则，对话路径就会继承一套它不需要的约束，而探针也无法用同一份代码验证真实规则。边界画在"内容 → 语义"之间。

### 2.2 空 content 必须是错误

```go
if text == "" && len(msg.ToolCalls) == 0 {
    return "", ErrContentMissing
}
```

如果返回空字符串，调用方会把"模型什么都没说"当成一个合法的空答案，最坏情况下变成一个没有论证的 HOLD。`ErrContentMissing` 让它在流水线里显式失败。

### 2.3 HTTP 200 里嵌的错误信封

部分供应商在 HTTP 200 里返回 `{"error":{"message":"model overloaded"}}`。`ParseChatResponse` 在 `len(Choices) == 0` 时尝试解错误信封，把供应商的 message 带到错误里——"为什么"才是操作者需要的。测试 `TestParseChatResponse` 直接覆盖。

### 2.4 `tool_choice` 的两种线格式

OpenAI 接受裸字符串 `"auto"`，但具名函数是对象 `{"type":"function","function":{"name":"..."}}`。供应商对 `{"mode":"auto"}` 报 400。所以 `ToolChoice` 自带 `MarshalJSON`/`UnmarshalJSON` 保持两种形状互不污染，且**可往返**（记录的请求能重放）。见 `TestToolChoiceWireForms`。

### 2.5 `Usage` 用 `types.Number` 而非 `int`

供应商对 token 计数是否加引号不一致。用 `types.Number` 两种都能读；`Usage.Total()` 优先用供应商的 `total_tokens`，缺失时回退到求和。理由见 `CODING_GUIDELINES.md`（"API 可能返回带引号的数字 → 用 `types.Number`"）。**请求方向**仍用 `int`（`max_tokens` 必须是裸数字）。

### 2.6 请求超限中止而非截断

`ChatRequest.Marshal()` 在超过 `MaxRequestBytes`（256KB）时返回 `ErrRequestTooLarge`。**截断比拒绝更危险**：截断后发出的 prompt 与审计记录里记录的不是同一个问题，而决策记录必须能回答"模型看到的是什么"。

### 2.7 探针的四层不是为了"检查端点活着"

| 层 | 失败的修复动作不同 |
| --- | --- |
| AUTH | 换 key / 换 base URL |
| TOOL_CALL | 换模型（该模型不支持 function calling） |
| SCHEMA | 改提示词或换模型（模型不守输出契约） |
| STREAM | 关闭对话流式，或换供应商 |

一层"端点是否活着"的检查给不出这四条修复路径。`ProbeAll` 顺序固定，因为落库的行要可比。

### 2.8 TOOL_CALL 层必须真跑完两轮

只验证"有没有 `tool_calls`"是不够的：供应商可能能发出工具调用，但在**回填 `tool_call_id` 的第二轮**上拒绝。所以 `probeToolCall` 真的构造第二轮（回放 assistant 轮 + tool 消息 + `tool_choice: none`），并断言第一轮请求里 `tool_choice: "auto"`、第二轮里带 `tool_call_id`。这是 `TestProbeToolCallPasses` 的核心断言。

### 2.9 SCHEMA 层的关键是"缺键 ≠ 值为 null"

```go
raw, present := fields["proposedMaxNotionalUsd"]
if !present { return ErrCandidateNotionalNotExplicit }
if strings.TrimSpace(string(raw)) != "null" { return ErrCandidateNotionalNotExplicit }
```

先解到 `map[string]json.RawMessage` 才能区分"键不存在"与"键存在但值为 null"。差别很大：**缺键说明模型根本没回答这个问题**，而 `null` 说明它回答了"不设额度"。只有后者才是通过。

之后再把这个 JSON 喂给**真实的** `candidate.Candidate.Validate()`，所以探针验证的是生产用的同一套规则，不是探针自己的一套。最后额外要求 `action == HOLD`——零数据下提案开仓的模型不能拿到真实数据。

### 2.10 探针零市场数据

`TestProbeCarriesNoMarketData` 断言四个层的请求体都不含 `btc`/`eth`/`usdt`/`binance`/`hyperliquid`/`1h`/`1m`/`"bid"`/`"ask"` 等标识。

**重要修正**：第一版测试把 `candle`/`orderbook`/`funding` 也列为禁止词，结果失败——因为 SCHEMA 层的提示词**必须说"这些数据都没有"**，否则那一层什么都没验证。所以约束的正确表述是"不泄露**在追踪什么**"，而不是"不出现任何市场名词"。这个区分写进了测试注释。

### 2.11 协议级故障优先于阶段包装

`ErrorCode` 的 switch 顺序有明确规则（已写入函数 GoDoc）：**具体的协议故障保留自己的码，即使外面套了阶段包装**。"第二轮工具调用的响应读不出来"报 `INVALID_JSON_RESPONSE` 比报 `TOOL_ROUND_INCOMPLETE` 更可操作——前者指出故障，后者只指出阶段。而"第二轮响应能读但内容是空的"确实是轮次未完成，报 `TOOL_ROUND_INCOMPLETE`。

`TestErrorCodeUnwrapsWrappedSentinels` 用真实 `candidate.Candidate.Validate()` 的错误包一层，断言 `CANDIDATE_INCOMPLETE` 能穿透包装被识别。

## 3. 本会话实际修掉的两个问题（测试先失败后修正）

| # | 现象 | 判定 | 修正 |
| --- | --- | --- | --- |
| 1 | `TestProbeToolCallRoundIncomplete`：第二轮返回空 body，期望 `TOOL_ROUND_INCOMPLETE`，实得 `INVALID_JSON_RESPONSE` | **代码行为是对的，测试期望错了**。协议级故障应保留自己的码 | 拆成两个用例分别断言两条规则；并把排序规则写进 `ErrorCode` 的 GoDoc |
| 2 | `TestProbeCarriesNoMarketData`：探针提示词里出现 `candles`/`funding`，被自己的禁止词列表命中 | **测试的约束表述错了**，不是代码错了 | 禁止词改为"标的/交易所/周期/盘口字段"标识；并额外断言提示词**必须**包含 "No market data is available" |

## 4. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| `prompt.go` 三个提示词常量 | **M1-13** | 本次只在 `probe.go` 里写了探针专用的两个提示词（`probeSchemaSystemPrompt` / `probeSchemaUserPrompt`）。生产的 `analysis-readonly-v1` / `policy-proposal-v1` / `chat-analysis-v1` 属 M1-13，未写 |
| `loop.go` 工具调用循环 | **M1-12** | 本次没有实现循环。`ChatRequest`/`ToolCall`/`DecodeArguments`/`ContentOf`/`ErrorCode` 都是为它准备的零件，但它本身（4 轮/8 次/24000 token/30s/共同截止时间）属 M1-12，且需要 `snapshot` 类型（M2）才能定签名 |
| `limits.go` | 不需要 | 规划里有这一项，但 256KB 上限已落在 `httpclient.PostJSON` + `ChatRequest.Marshal()` 两处，单独一个文件是无谓的抽象。**建议从规划中删掉** |
| 探针结果落库 | **M1-09 或 M0-04 之后** | `ProbeResult` 字段已对齐 `provider_probe_results` 列，但表还不存在 |
| `InspectSSE` 完整流式读取 | **M3-07** | 本次只有 `ParseSSE`（把完整 body 切帧 + 认 `[DONE]`），够探针用。真流式需要增量读取、半截 JSON 缓冲、中断恢复——属 M3-07 |
| 真实供应商冒烟 | **M1-15** | 本次全部是 `httptest` 离线测试，**未打过任何真实端点** |

## 5. 校验证据

```text
$ gofmt -l -w llm/            # 无残留
$ go build ./llm/...          # 通过
$ go vet  ./llm/...           # 无输出
$ go test ./llm/... -count 1
?   github.com/thrasher-corp/gocryptotrader/llm            [no test files]
ok  github.com/thrasher-corp/gocryptotrader/llm/candidate   0.713s
ok  github.com/thrasher-corp/gocryptotrader/llm/httpclient  1.725s
ok  github.com/thrasher-corp/gocryptotrader/llm/jev         1.753s
ok  github.com/thrasher-corp/gocryptotrader/llm/openai      1.828s
ok  github.com/thrasher-corp/gocryptotrader/llm/secrets     2.821s

测试函数计数（go test -v | grep -c '^=== RUN'）：
candidate 31 · httpclient 39 · jev 89 · openai 41 · secrets 20   → 合计 220
```

**全部离线**：`httptest` + `t.TempDir()`，不触网、不需要 provider key、不需要数据库、可连续重复跑。

## 6. 给接手模型的具体建议

**当前最高杠杆点：`M0-04`（8 个 PostgreSQL 迁移）。** 它是 `M1-09`~`M1-13` 的公共前置，且是纯 DDL、可离线验证。表结构来自 `docs/llm-dev/09-数据存储与配置.md` §2-§5；`provider_probe_results` 的列已在 `docs/llm-dev/05` §3 给出，`ProbeResult` 的字段已经对齐。

**如果不想碰 DB**，可做的：

1. `M1-13` 提示词常量（纯字符串，无依赖）
2. `M0-13` 架构边界测试：断言 `llm/` 不 import `engine/`、风控不 import 模型客户端
3. `W5-01`、`W7-04`、`W8-04/06`（前端，不依赖后端）

**`M1-12`（工具调用循环）虽然"只差临门一脚"，但它的签名依赖 M2 的 `snapshot` 类型**（循环的第一句就是 `deadline = min(now + pipelineDeadline, snapshot.SourceTime + autoCandidateWindow)`）。建议先做 M2 的 snapshot 契约，或至少先定 snapshot 的类型，再写循环。
