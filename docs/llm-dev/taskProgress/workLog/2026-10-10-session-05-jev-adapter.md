# 2026-10-10 session-05 — Jev 纯函数请求构造器与响应校验（M1-05 / M1-06）

## 会话目标

落地 System One 适配层中**不依赖网络与密钥**的两块：请求构造器与响应校验。这两块是双模型决策的契约核心，也是唯一可以在没有 Jev 账号、没有 proto 工具链的前提下完整验证的部分。

## 完成的子任务

`M1-05`（纯函数请求构造器）· `M1-06`（响应校验）

## 具体做了什么

新增 `llm/jev/` 包（3 个源文件 + 1 个测试文件，共 ~1200 行）：

| 文件 | 内容 |
| --- | --- |
| `llm/jev/types.go` | 契约常量（`SchemaVersionFactualInput` / `QuestionVersionReviewV2` / `SystemOnePath` / `MaxRequestBytes` 64KiB）、三题型与三答型的具名类型、`ChoiceQuestion` / `ScoreQuestion` / `NoulQuestion`、`ChoiceAnswer` / `ScoreAnswer` / `NoulAnswer`、`SystemOneRequest` / `SystemOneResponse`、`EvidencePolicy` / `SnapshotRef` / `SafetyStatus`、以及两个按题型分派解码的访问器 |
| `llm/jev/request.go` | `BuildReviewRequest(in ReviewInput)` 纯函数 + `(*SystemOneRequest).Marshal()` 尺寸闸 + `IsPinnedModel` + `NewSyntheticProbeState` |
| `llm/jev/validate.go` | `ValidateResponse(req, resp) (*Assessment, error)` + 容差常量 + 10 个哨兵错误 |
| `llm/jev/jev_test.go` | 13 个测试函数，覆盖纯度、拒绝路径、choice/score/noul 三组规则、模型漂移、尺寸上限、探针无数据 |

### 落实的关键约束

| 约束 | 实现 | 为什么重要 |
| --- | --- | --- |
| **同输入 → 字节级一致** | `BuildReviewRequest` 不读时钟、不做 I/O、不碰密钥；`Questions` 用 `map` 但在 `encoding/json` 下键有序 | 请求体要能哈希落库、事后重放，证明"当时问的就是这些" |
| **超限整体拒绝，绝不截断** | `Marshal()` 超 64 KiB 返回 `ErrRequestTooLarge` | 截断会破坏同源不变量：评委看到的就不再是分析师看到的那份事实 |
| **拒别名，只收精确版本** | `IsPinnedModel` 正则 `^jev-\d+\.\d+\.\d+$` | `jev-latest` 会漂移，审计链会断 |
| **模型漂移即拒绝** | `resp.Model != req.Model` → `ErrModelDrift` | 供应商静默换模型时，决策记录的"可重放"承诺失效 |
| **分布键集必须**恰等**criteria** | choice 与 score 都校验键集完全一致，多一个少一个都拒 | 防止"漏报一档"被误读为小概率 |
| **选中项不得低于最高概率 − 0.03** | 检测"choice 说选 A，但分布里 B 明显更高"这种自相矛盾 | 这类响应不可信，必须拦截 |
| **加权分一致性 ±0.06** | score 校验 `≈ Σ i·p_i` | 同上，检测分数与分布互相打架 |
| **题型字段必须匹配** | 每类答案校验自己的 `type` | 防止把 score 塞进 choice 槽位 |
| **探针零市场数据** | `NewSyntheticProbeState` 只含一句免责声明 | 探针可能打到配置错误的端点，唯一能泄露的只能是"发生了一次请求" |
| **强类型消除 JSON 歧义** | `Questions` 用 `json.RawMessage` 承接，再经 `DecodeChoice`（对象形 criteria）与 `DecodeScore`（数组形 criteria）分派解码 | 契约把同一个键 `criteria` 重载成两种形态，`encoding/json` 无法直接处理 |

## 验证证据（真实跑出）

```text
go test ./llm/... -count 1
  →  ok  github.com/thrasher-corp/gocryptotrader/llm/candidate  0.542s
  →  ok  github.com/thrasher-corp/gocryptotrader/llm/jev        0.727s
go build ./llm/...   →  通过
gofmt -w llm/        →  无差异
```

测试覆盖：构造器纯度（两次构建字节相等）· 框架与版本常量出现在 wire payload · choice 与 score 题面两种 criteria 形态均可解码 · 8 类构造拒绝（别名模型/空模型/nil 候选/非法候选/无证据/空快照 ID/空策略来源/策略声称有交易权）· 尺寸闸（正常请求远低于上限；超限请求被拒而非截断）· 响应校验 happy path（含 confidence/legend/usage/服务模型全部落位）· 4 类信封拒绝（模型漂移/空 answers/非 JSON/缺 answer）· 8 类 choice 规则 · 7 类 score 规则 · 3 类 noul 规则 · nil 入参 · 模型正则正反向用例 · 探针状态无市场数据

## 遇到的坑与解决（**接力必读**）

### 坑 1：文档示例与文档自身的不变量相矛盾（已修正文档）

`docs/llm-dev/06-Jev接入层-SystemOne.md` §2.2 的 score 示例写的是 `score: 1.42` 配 `p = {0.18, 0.40, 0.42}`，但同一节紧接着声明不变量 `score ≈ p1 + 2·p2`（容差 ±0.06）。实际算出来是 `0.40 + 2×0.42 = 1.24`，与 1.42 差 0.18 —— **示例会被自己的校验器拒绝**。

**处理**：把示例的 `score` 改为 `1.24`，并在该节加了一条实现注记说明此事。测试数据同步改为 1.24。

**给接力的提醒**：技术文档里的数值示例可能只为"看起来像"而写，**不要把它当成黄金值**；以不变量与校验器为准。若发现类似矛盾，改示例并在文档里留注记，不要放宽校验器。

### 坑 2：engine 包有两个既有的 flaky 网络依赖测试（**不是回归**）

`go test ./engine/` 全量跑会出现失败，实测抓到两个：

```text
TestGetDefaultConfigurations/btse
  → Get "https://api.btse.com/spot/api/v3.2/market_summary": read tcp ...->198.18.3.38:443: read: connection reset by peer
TestNewExchangeByNameWithDefaults/coinbase
  → NewExchangeByNameWithDefaults should not error
```

两者都会调 `exchange.GetDefaultConfig` 去访问**真实交易所线上 API**。本机 DNS 走 fake-IP（`198.18.x.x`）经透明代理时被重置，因此**间歇失败**：同一命令连跑三次得到"失败→失败→通过"。

**处置**：判定为既有 flaky 测试，与本项目改动无关。**没有**去改这些测试、也**没有**往 `blockedCIExchanges` 里加交易所（那是掩盖环境问题）。改为在接力文档里给出带 `-skip` 的可靠配方（见 `taskProgress/README.md` §4.3）：

```bash
go build ./...
go test ./llm/... ./config/ -count 1
go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'
```

轮换到 `LIVE` 与 `M6-10`（要求 `make check` 全绿）时，需要先解决这台机器的网络/tests 基建问题，或把这些交易所加进仓库的 CI 跳过列表并单独提交说明。

### 坑 3：JSON 无法表达 `NaN`

原本想测"noul 为 NaN 时必须拒绝"，但 JSON 语法没有 NaN，构造不出该输入。校验器里的 `math.IsNaN` 分支因此保留为**防御性检查**（走同一函数的直接调用路径），测试改为覆盖 `>1`、`<0`、`type` 不匹配三种可达情形。不要试图用 JSON 测 NaN。

## 未完成 / 下一步建议

**M1 现为 2/15**。本包剩余可直接推进的：

1. **`M1-04`** Jev HTTP 客户端（`llm/jev/client.go`）—— 需要落实网络加固：禁重定向、DNS 解析后校验 IP、阻断 link-local/元数据地址、显式超时、**不读环境变量代理**、`Authorization: Bearer` 由 `secretFn` 回调注入。可用 `httptest` 服务器做离线测试（**不需要真实 Jev 账号**）。
2. **`M1-07`** 探针（`NewSyntheticProbeState` 已就绪，补三题合成问句 + 结果结构）。
3. **`M1-08`** 证据清单 `JevInputManifest`（`requestSha256` / `parsedResponseSha256` / `candidateSha256` / `strategyInstructionSha256` / 每条工具 `resultSha256` / `factsScope` / `tradeAuthorized:false`）+ `InspectManifest` 四态分类。纯函数，易测。
4. **`M1-01`** 密钥 vault（`llm/secrets/`）：RSA 3072 + AES-GCM 包裹、文件 0600/0700、CLI 无回显录入。
5. **`M1-02`/`M1-03`** OpenAI 兼容客户端与四层探针（`llm/openai/`）—— 其 HTTP 加固逻辑应与 `M1-04` 共用同一套（**建议先抽公共 HTTP 加固层**，避免两处实现漂移）。

**不要忘记**：新增 gRPC/REST 端点前必须先校准 proto 工具链（`TASKS.md` §0.1）。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 运行中，PID 18809，`-portfoliomanager=false -llmmanager`，9052/9053 |
| webUI 代理 / Vite | 运行中，8788 / 5173 |
| 新增包 | `llm/jev/`（types/request/validate/test） |
| proto 工具链 | 已装但未校准（`TASKS.md` §0.1） |
