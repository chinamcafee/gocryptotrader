# 2026-10-10 session-07 — Jev 输入证据清单与四态分类（M1-08）

## 会话目标

落地 `M1-08`：把"评委当时拿到的是哪些字节"变成可校验的记录，作为订单准备与发送认领的前置闸。

## 完成的子任务

`M1-08`（证据清单 `InputManifest` + `InspectManifest` 四态分类）

## 具体做了什么

新增 `llm/jev/manifest.go`（~300 行）+ `manifest_test.go`（~300 行）：

| 组成部分 | 内容 |
| --- | --- |
| `InputManifest` | 11 个字段：`schemaVersion`（`jev-factual-input-v1`）、`questionVersion`（`jev-review-v2`）、`requestModel`、`requestSha256`、`requestBytes`、`parsedResponseSha256`、`candidateSha256`、`strategyInstructionSha256`、`toolResponses[]`、`factsScope`、`tradeAuthorized`(恒 false) |
| `ToolResponseRef` | 每条工具响应记 `callId` / `name` / `ref` / `resultSha256` |
| `BuildInputManifest` | 纯函数：同输入产出同一清单（可比较、可重放） |
| `Validate` | 14 类拒绝路径（见下） |
| `InspectManifest` | 四态分类 + `MayProceed()` 闸 |
| `NewToolResponseRef` | 从 `candidate.ToolEvidence` 生成引用并哈希原始 payload |

### 落实的关键约束

| 约束 | 实现 | 为什么重要 |
| --- | --- | --- |
| **`tradeAuthorized` 恒 false 且校验拦截** | `Validate()` 见 true 即返回 `ErrManifestAuthorized` | 清单内自带"这份数据没有授权任何交易"的自证声明；若哪天代码错误置 true，校验会失败 |
| **四条哈希必须齐备且形状正确** | `isHexDigest` 校验 64 位 hex | 缺一行或截断的哈希等于无法复核 |
| **`factsScope` 只接受 `RETAINED_LLM_TOOL_RESPONSES`** | 其它值 → `ErrManifestScope` | 换成"证据摘要"等更弱来源就破坏了同源不变量，必须拒 |
| **必须有工具响应引用** | 空列表 → `ErrManifestIncomplete` | 没有工具响应的清单证明不了评委看过什么 |
| **schema/question 版本必须匹配** | 未知 schema → `ErrManifestSchema` | 版本漂移会让复核读错字段 |
| **模型必须是精确版本** | 复用 `IsPinnedModel` | 别名模型会让清单不可重放 |
| **只有 `MANIFEST_BOUND` 可放行** | `ManifestState.MayProceed()` | **这是闸**：订单准备与发送认领都要求 bound，否则阻断（码 `JEV_FACTUAL_INPUT_NOT_RECORDED` / `JEV_FACTUAL_INPUT_INVALID`） |
| **缺失即分类而非报错** | `InspectManifest(nil)` 返回 `(LEGACY_MISSING, nil)` | 调用方可以无条件地为每条决策落库一个状态，无需特判 |
| **哈希覆盖"收到的字节"** | `parsedResponseSha256` 哈希响应原始字节，而非解析后的结构体 | 解析是对字节的有损投影；哈希字节还能覆盖本版本不认识的字段（见坑 1） |

### 四态语义（供后续 M5 使用）

| 状态 | 含义 | 可否放行 |
| --- | --- | --- |
| `MANIFEST_BOUND` | 清单存在、版本正确、自洽 | ✅ 是 |
| `LEGACY_MISSING` | 没有清单（早于清单机制的历史记录） | ❌ 否 |
| `INVALID` | 有清单但校验失败 | ❌ 否 |
| `NO_ASSESSMENT` | 根本没调 Jev，无可绑定 | ❌ 否（由知道"未调用"的调用方选择） |

## 验证证据（真实跑出）

```text
go test ./llm/... -count 1
  →  ok  llm/candidate    1.674s
  →  ok  llm/httpclient   2.769s
  →  ok  llm/jev          2.982s
go build ./llm/...   →  通过
```

测试覆盖要点：

- **哈希正确性用独立实现复核**：测试内自行 `sha256.Sum256` + `hex.EncodeToString` 重算，与包内 `HashBytes` 的产物比对 —— 证明的是数值本身，而不是把实现复述一遍
- **确定性**：同一输入两次构建，JSON 序列化后字符串相等
- **6 类构建拒绝**：nil 请求 / 空请求体 / 空响应体 / nil 候选 / 无工具响应 / 别名模型
- **14 类校验拒绝**：声称有交易权、未知 schema、空 question 版本、空模型、零字节、截断请求哈希、非 hex 响应哈希、空候选哈希、空策略哈希、弱 factsScope、无工具响应、缺 callId、哈希形状错
- **四态分类**：bound（含 `MayProceed()` 为真）；legacy（nil / 空对象 / 纯空白三种输入均不报错且不可放行）；invalid（非 JSON / 空对象 / 错误 scope / 声称有权限四种）；`NO_ASSESSMENT` 不可放行
- **引用哈希覆盖 payload**：改动 payload 一个数字即导致哈希变化

## 遇到的坑与解决

### 坑 1：字段名 `parsedResponseSha256` 与"哈希什么"的语义有张力（已按更强语义实现并记录）

文档给的字段名是 `parsedResponseSha256`（"解析后的响应"），但**解析后的结构体是对响应字节的有损投影**：未知字段会被丢弃，重新序列化还可能改变字段顺序。

**处置**：字段名保持与文档一致（避免与参考设计脱节），但**哈希的是"收到的响应原始字节"**，并在代码注释与本节写明理由 —— 哈希字节是更强的保证，能覆盖本版本不认识的字段。

**给接力的提醒**：若将来要为该字段改名为 `responseSha256`，属于 schema 变更，需要同时升 `SchemaVersionFactualInput`，不能只改名字。

### 坑 2：`InspectManifest` 的"缺失"该报错还是该分类（已定为分类）

直觉上"没有清单"像错误。但若返回 error，调用方就得在每条决策上写特判，而实际需求是**每条决策都要落库一个状态**（哪怕状态是"没有"）。

**处置**：返回 `(LEGACY_MISSING, nil)`，即"分类成功、且该分类阻断推进"。`INVALID` 才是"分类成功并附上失败原因（error）"。文档里 `MAY_PROCEED` 的语义据此写成"只有 BOUND 为真"。

## 未完成 / 下一步建议

**M1 现为 5/15**。剩余可继续推进的：

1. **`M1-01`** 密钥 vault（`llm/secrets/`）：RSA 3072 密钥对 + 每 key 一个随机 AES-256-GCM 数据密钥、数据密钥用 RSA-OAEP-SHA256 包裹、文件 0600/0700、`secret://llm/<uuid>` 引用 + 截断 SHA-256 指纹。**`SecretFn` 接口已就位**（`llm/jev/client.go`），接上即可；`dailyBudgetUsd` 同类的三态语义可复用 config 里的 `*float64` 写法。
2. **`M1-02`/`M1-03`** OpenAI 兼容客户端与四层探针 —— **直接复用 `llm/httpclient`**（`New` + `PostJSON`），并把 `ErrorCode` 的错误码词表按 LLM 侧的码补齐（`TOOL_CALL_UNSUPPORTED` / `TOOL_ARGUMENTS_INVALID` / `CANDIDATE_SCHEMA_INVALID` 等）。
3. **`M1-09`~`M1-13`** 依赖 provider 档案表，需先完成 `M0-04`（数据库迁移，**PostgreSQL**）。

**不要忘记**：新增 gRPC/REST 端点前必须先校准 proto 工具链（`TASKS.md` §0.1）。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 运行中，PID 18809，`-portfoliomanager=false -llmmanager`，9052/9053 |
| webUI 代理 / Vite | 运行中，8788 / 5173 |
| `llm/` 树 | 4 包全绿：`llm` / `candidate` / `httpclient` / `jev`（含 manifest） |
| proto 工具链 | 已装但未校准（`TASKS.md` §0.1） |
