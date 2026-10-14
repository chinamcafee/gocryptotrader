# 2026-10-10 session-04 — M0 核心契约（`llm/` 顶层包）

## 会话目标

建立 `M0-01`/`M0-02`/`M0-03`：LLM 决策系统的核心类型契约与错误码词表，作为 M1（模型接入）与 M2（数据与工具）的前置。

## 完成的子任务

`M0-01`（核心类型契约）· `M0-02`（错误码词表）· `M0-03`（DataQuality 枚举 + 只能降级校验）

## 具体做了什么

新增 `llm/` 顶层包（依据 `docs/llm-dev/03-总体架构设计.md` §4 的目录规划），**不 import `engine/`** —— 这是 OPEN-QUESTIONS Q22 决定的可抽出性前提。

| 文件 | 行数 | 内容 |
| --- | --- | --- |
| `llm/llm.go` | 31 | 包文档（含五条设计底线的代码化说明）+ 4 个跨包共享哨兵：`ErrNotImplemented` / `ErrInvalidResponse` / `ErrMissingData` / `ErrUnverified` |
| `llm/candidate/quality.go` | 98 | `DataQuality` 8 个标签 + 信任秩 + `Downgrade()` **只能降级不能升级** + `IsDegraded()` + `ParseQuality()` |
| `llm/candidate/evidence.go` | 162 | `ToolEvidence` + 上限常量 + 四道闸（ref 长度 / 未来时间 / 单条 12KB / 敏感键扫描深度 ≤8）+ 聚合 32KB 上限 |
| `llm/candidate/candidate.go` | 231 | `Candidate` + `Action` 枚举 + `Uncertainty` + 完整 schema 校验 + `ValidatePositiveDecimal` |
| `llm/candidate/policy.go` | 314 | `MarketScope` / `RiskEnvelope` / `PolicyProposal` / `ReviewablePolicy` + 全部边界校验 |
| `llm/candidate/candidate_test.go` | 460 | 11 个测试函数 / 34 个子用例 |

### 落实的关键不变量

| 不变量 | 实现位置 | 为什么重要 |
| --- | --- | --- |
| **质量只能降级不能升级** | `DataQuality.Downgrade()` 用信任秩比较，升级请求返回 `ErrQualityUpgrade` | 一旦事实已知过期/有缺口，后续任何处理都不得把它恢复成可信标签 —— 否则未验证数据会戴着好标签进风控 |
| **HOLD/REDUCE/REVIEW 不得携带名义额** | `Candidate.Validate()` → `ErrUnexpectedNotional` | 防止模型给"观望"附上一个金额，被下游误当成仓位 |
| **小数为字符串、必须为正、禁零** | `ValidatePositiveDecimal`（正则 + 逐位判零） | 钱走字符串，模型与订单之间没有任何浮点舍入；零名义额与缺失不可区分，恒为错误 |
| **证据引用必须以 snapshotId 开头** | `validateEvidenceRefs` → `ErrSnapshotRefMissing` | 让"引用了本次快照"成为可机器校验的事实，而非模型的陈述 |
| **敏感键递归拦截（深度 ≤8）** | `ScanForSensitiveKeys`（`secret`/`api_key`/`private_key`/`mnemonic`/`seed_phrase`/`password`/`token`/`signature`） | **故意过度匹配**：少给一条证据只会降级为 HOLD，泄露凭据无法召回 |
| **非 JSON 载荷直接拒绝** | `ScanForSensitiveKeys` 解析失败 → `ErrInvalidResponse` | 无法检查的载荷无法证明安全 |
| **`tradeAuthorized` 必须为 false** | `ReviewablePolicy.Validate()` | 类型层面拒绝"自我授权"，审批是独立的审计动作 |
| **PERP 必须有杠杆上限** | `ReviewablePolicy.Validate()` → `ErrLeverageCapRequired` | 永续强制杠杆上限，null 不等于无限 |
| **硬上限不可被提案放宽** | `RiskEnvelope.Validate()`：意图 ≤5、窗口 ≤1440min、滑点 ≤1000bps、杠杆 ≤100、有效期 ≤7 天 | 上限是**上限**而非默认值，提案不得拓宽 |
| **缺边界必须转澄清而非填值** | `PolicyProposal.State()` 返回 `NEEDS_CLARIFICATION` | 提取路径上最危险的失败是"替操作者假设了一个限额"，之后他批准的是自己从未选择的数字 |

### 错误码词表的落点

按 Go 惯例分散到使用它们的包，而非集中在一个大文件：

- 跨包共享 → `llm/llm.go`
- 候选/证据/策略相关 → `llm/candidate/*.go` 各自的包级哨兵（`ErrCandidateIncomplete` / `ErrUnknownAction` / `ErrSnapshotRefMissing` / `ErrInvalidDecimal` / `ErrTTLOutOfRange` / `ErrQualityUpgrade` / `ErrUnknownQuality` / `ErrEvidenceRefEmpty` / `ErrEvidenceRefTooLong` / `ErrEvidenceFromFuture` / `ErrToolResultTooLarge` / `ErrToolResultTooDeep` / `ErrToolResultSensitive` / `ErrEvidenceTotalTooLarge` / `ErrPolicyIncomplete` / `ErrTooManyMarketScopes` / `ErrDuplicateMarketScope` / `ErrValueOutOfRange` / `ErrUnsupportedMarketType` / `ErrLeverageCapRequired` / `ErrTradeAuthorizedMustBeFalse` / `ErrApprovalWindowInvalid` / `ErrClarificationRequired`）

全部支持 `errors.Is`，包装时用 `%w`（符合 `docs/CODING_GUIDELINES.md`）。

## 验证证据（真实跑出）

```text
go test ./llm/... -count 1   →  ok  github.com/thrasher-corp/gocryptotrader/llm/candidate  0.730s
go build ./...               →  ✅ 通过
go test ./llm/... ./config/ ./engine/ -run '...' -count 1
                             →  ok llm/candidate  ok config  ok engine
gofmt -l llm/                →  空
go vet ./llm/...             →  空
```

测试覆盖要点（34 个子用例）：schema 必填项、ID 格式、action/uncertainty 枚举、thesis/invalidation 长度、证据引用数量与首项约束、小数的符号/指数/零三类非法输入、TTL 上下界、HOLD 携带名义额被拒、质量降级与升级拒绝、未知质量标签、未来时间戳（含 1s 容差内的合法情形）、未知质量、空数据、超大载荷、非 JSON 载荷、7 类敏感键（含嵌套与数组内）、深度超限、聚合超限、澄清态判定（未答问题/缺退出策略/缺金额边界/nil 提案）、重复市场、非 PERP 市场、超出硬上限、自我授权、缺杠杆上限、审批窗口过长/为空、缺模型修订版 ID。

## 遇到的坑与解决

| 坑 | 现象 | 解决 |
| --- | --- | --- |
| **共享哨兵放错包** | `evidence.go` 引用 `ErrMissingData`/`ErrInvalidResponse` 编译失败 | 这两个是跨包的，定义在 `llm/llm.go`，改为 `llm.ErrMissingData` / `llm.ErrInvalidResponse`（无循环依赖：`candidate` → `llm` 单向） |
| 测试里硬编码聚合字节数 | `assert.Equal(t, 36000, total)` 失败——实际 payload 是 6008 字节而非 6000 | 改为断言 `Greater` 与 `ErrorIs`，不耦合具体字节数 |
| `err = X.Downgrade(...)` 单值赋值 | 该函数返回两个值，编译失败 | 改为 `_, err = ...` |

## 未完成 / 下一步建议

**M0 现为 7/14**。剩余：`M0-04`~`M0-07`（8 个数据库迁移 / 不可变触发器 / sqlboiler 模型 / 仓储层，**注意用 PostgreSQL**）、`M0-09`（v18 配置迁移）、`M0-13`（架构边界测试）、`M0-14`（Postgres provisioning）。

**下一个模型建议顺序**：

1. `M0-13` 架构边界测试 —— 小而高价值。本包的 `llm` 不 import `engine` 已是事实，把它固化成测试（用 `go/parser` 扫描 import）即可，同时校验"风控不得 import 模型客户端"这条更关键的边界（需等 `engine/llm_risk.go` 落地后补第二段）。
2. `M0-09` v18 配置迁移。
3. `M0-04`~`M0-07` 数据库层（先迁移与触发器，再 sqlboiler，再仓储）。
4. **`M1-01`~`M1-08`** —— 密钥 vault 与 Jev 客户端。Jev 的请求构造器是**纯函数**（`docs/llm-dev/06-Jev接入层-SystemOne.md` §4.3），非常适合先落地并用黄金文件测试；它不依赖 proto。
5. `M1-09`~`M1-13` 依赖 provider 档案表 → 需要 `M0-04` 完成。

**不要忘记**：新增 gRPC/REST 端点前必须先校准 proto 工具链（`TASKS.md` §0.1）。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 运行中，PID 18809，`-portfoliomanager=false -llmmanager`，9052/9053 |
| webUI 代理 / Vite | 运行中，8788 / 5173 |
| 新增包 | `llm/` + `llm/candidate/`（1296 行，其中测试 460 行） |
| proto 工具链 | 已装但**未校准**（`TASKS.md` §0.1） |
