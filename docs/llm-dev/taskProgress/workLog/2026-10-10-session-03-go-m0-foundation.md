# 2026-10-10 session-03 — Go 后端 M0 基础层（LLM 配置段 + 子系统 + CLI）

## 会话目标

按 `TASKS.md` 推进后端 `M0`：让 LLM 子系统真正在引擎中运行起来，并为后续 M1/M2/M4 建立配置与生命周期基础。

## 完成的子任务

`M0-08`（配置段 `LLMConfig`）· `M0-10`（`config_example.json` 新增 `llm` 段）· `M0-11`（子系统骨架 + `llm_manager.md`）· `M0-12`（CLI flag + Settings 合并）

同时补齐了本轮新产生的两类工作：`log.LLMMgr` 子日志器、以及前端经由 `/api/getinfo` 读取子系统状态的端到端链路。

## 具体做了什么

### 1. 新增文件

| 文件 | 内容 |
| --- | --- |
| `config/llm_config_types.go` | `LLMConfig` + 5 个子结构（`LLMAnalysisConfig` / `LLMIndicatorConfig` / `LLMCoverageConfig` / `LLMBudgetConfig` / `LLMHaltConfig`）。默认「开」的布尔字段用 `*bool`，以便区分「未填」与「显式 false」；`dailyBudgetUsd` 用 `*float64` 保留三态语义（未设 / 0=禁止一切模型调用 / 正数=计划值） |
| `config/llm_config.go` | 全部默认值常量 + `CheckLLMConfig()`。默认值逐一对应 OPEN-QUESTIONS 定案：`autoCandidateWindowSeconds=60`(Q9)、`scope=ALL_USDTM_PERPETUAL`/`rotationCycleMinutes=10`(Q2)、RSI 与布林默认开(Q16)、`reviewExpireHours=12`(盯盘频率)、`robotClientOrderPrefix=gct-llm-`、`weightBudgetFraction=0.6` |
| `config/llm_config_test.go` | 4 个测试：默认值、保留操作者显式选择、越界值钳制、幂等性 |
| `engine/llm_manager_types.go` | `LLMManager`、运行模式常量、`LLMStatus`（全部字段 fail-closed） |
| `engine/llm_manager.go` | `SetupLLMManager` / `Start` / `Stop` / `IsRunning` / `Status` / `Config` |
| `engine/llm_manager_test.go` | 5 个测试：缺依赖拒绝、运行模式 fail-closed（9 个用例含 LIVE 未确认降级）、生命周期、状态 fail-closed、nil 接收者安全 |
| `engine/llm_manager.md` | 子系统文档（仓库惯例：每个子系统一份 `.md`），含当前状态、配置、状态字段表、生命周期、已知缺口 |

### 2. 修改文件（共 9 处接线）

| 文件 | 改动 |
| --- | --- |
| `log/sublogger_types.go` | 新增 `LLMMgr *SubLogger` |
| `log/logger_setup.go` | 注册 `LLMMgr = registerNewSubLogger("LLM")` |
| `config/config_types.go` | `Config` 新增 `LLM LLMConfig \`json:"llm"\`` |
| `config/config.go` | 在 `CheckDataHistoryMonitorConfig()` 之后调用 `CheckLLMConfig()` |
| `engine/engine_types.go` | `CoreSettings` 新增 `EnableLLMManager bool` |
| `main.go` | 新增 `-llmmanager` flag（默认 false） |
| `engine/engine.go` | 4 处：`Engine` 结构体字段、`validateSettings` 的 `WithBool` 合并、`Start()` 中 Setup+Start 块（排在 Order manager 之后）、`Stop()` 中**先停 LLM 再停 OrderManager** |
| `engine/helpers.go` | `GetSubsystemsStatus()` 新增 `llmManagerName` 条目 |
| `config_example.json` | 新增完整 `llm` 段（顶层键数 20 → 21） |

### 3. 两条 fail-closed 安全规则（实现在 `SetupLLMManager`，不在调用点）

1. 未知或空的运行模式 → `READ_ONLY`
2. `LIVE` 但未填写精确的 `acknowledgeLiveTrading`（`I_UNDERSTAND`）→ 降级为 `READ_ONLY` 并打 WARN 日志

放在 Setup 而不是各调用点，是为了让所有下游组件观察到**同一个**有效模式，且避免某处忘记检查。

## 验证证据（全部真实跑出）

### 单元测试

```text
go test ./config/ -run LLM -count 1   →  ok  github.com/thrasher-corp/gocryptotrader/config  0.753s
go test ./engine/ -run LLM -count 1   →  ok  github.com/thrasher-corp/gocryptotrader/engine  0.912s
```

### 端到端：子系统真的在引擎里跑

```text
$ ./gocryptotrader -portfoliomanager=false -llmmanager
[DEBUG] | LOG | Enable LLM Manager:true
[DEBUG] | LLM | LLM manager started. mode READ_ONLY          ← 新子日志器生效
```

```text
$ gctcli getsubsystems
{"communications":false,"currency_state_manager":true,"data_history_manager":false,
 "database":true,"dispatch":true,"exchange_syncer":true,"grpc":true,"grpc_proxy":true,
 "internet_monitor":true, "llm_manager":true, "ntp_timekeeper":false,"orders":true,"portfolio":false}
```

**`"llm_manager": true`** —— 子系统在引擎中真实运行。

启动后 ERROR 1 / WARN 5，与改动前完全一致（未引入新告警）。

### 前端无需 proto 即可读到（意外收获）

`/api/getinfo` 的 `subsystemStatus` 直接包含子系统状态，因此 **webUI 顶栏无需任何 proto 改动就能显示 `llm_manager`**：

```text
$ curl -s http://127.0.0.1:8788/api/getinfo | jq -c '.subsystemStatus'
{... "llm_manager":true ...}
```

### 向后兼容在生产路径上验证

当前运行中的 `~/.gocryptotrader/config.json` **完全没有 `llm` 段**（是 session-01 写入的旧配置），但 bot 正常启动且 `llm_manager: true` —— 证明"缺失整段 → 由 `CheckLLMConfig` 填默认 → 落在最安全模式"这条路径在真实加载器下成立，不只是单测里成立。

## 遇到的坑与解决（接力必读）

### 坑 1：proto 工具链漂移，**加 RPC 前必须先解决**

本来计划本轮加一个 `LLMGetStatus` RPC，先做了**幂等性基线校验**：

```text
buf generate（buf 1.73.0 + go install 的插件）
→ git diff：rpc.pb.gw.go 336 行、rpc_grpc.pb.go 2 行
```

即**重新生成会产出与已提交文件不一致的结果**，说明本机插件版本与提交时不一致。此时若继续加 RPC，会把 336 行与本次改动无关的生成代码 churn 混进提交，且可能引入网关行为差异。

**处理**：`git checkout -- gctrpc/rpc.pb.gw.go gctrpc/rpc_grpc.pb.go` 回退，改用**不需要 proto 的路径**完成本轮目标（子系统状态经 `/api/getinfo` 到达前端）。已安装的工具链保留（`~/go/bin/buf` 1.73.0 与 4 个 `protoc-gen-*`）。

**接力要求**：任何要新增 RPC 的任务（`M1-14`/`M3-05`/`M4-15` 及全部 `/v1/llm/*` 端点）**必须先校准生成工具版本**，使 `buf generate` 对现有 proto 幂等（diff 为空）再动手。校准方法：查 `go.mod` 的 `tool` 块版本，用 `go tool` 或 `go install <pkg>@<version>` 安装**精确版本**的 4 个插件；若仍不幂等，说明提交的生成文件来自更早/更新的版本，需在提交信息里明确记录并单独成一个"仅重新生成"的提交。这是一项**前置阻塞**，不要跳过。

### 坑 2：`log.LLM` 不存在，日志子系统需先注册

日志子系统在 `log/sublogger_types.go` 声明、`log/logger_setup.go` 的 `init()` 注册。直接写 `log.LLM` 编译不过。已新增 `log.LLMMgr`（名称 `"LLM"`，日志前缀显示为 `| LLM |`）。新增任何子系统都要走同样两步。

### 坑 3：测试期望要与仓库既有约定对齐

我最初断言「nil 接收者调用 `Stop()` 应返回 nil」，实际仓库约定是返回 `ErrNilSubsystem`（`DataHistoryManager` 同样如此）。**已改正测试而不是改实现** —— 引擎的关闭路径靠 `IsRunning()` 空指针保护，不依赖 `Stop()` 容忍空接收者。新增子系统时请沿用该约定。

### 坑 4：`config_example.json` 必须保留原始格式

该文件是 prettified JSON（1 空格缩进）。若用 `json.load` + `json.dump` 回写会产生全文重排的巨量 diff。本轮用**文本插入**（正则定位 `dataHistoryManager` 块后追加）保持格式，仅新增 60 行。

## 未完成 / 下一步建议

**下一个模型建议顺序**：

1. **`M0-09`** — `config/versions/v18` 迁移。当前做法（缺段→默认）虽已验证可用，但按 `docs/CODING_GUIDELINES.md` 的配置迁移规则，schema 变更必须走**新增版本**而不是依赖默认值填充。
2. **`M0-13`** — 架构测试（import 边界：风控不得 import 模型客户端）。本轮写了单测但未写边界测试。
3. **`M0-01`/`M0-02`/`M0-03`** — 核心类型契约（Candidate / ToolEvidence / DataQuality 枚举 + 错误码词表），为 M1/M2 铺路。
4. **`M0-04`/`M0-05`/`M0-06`/`M0-07`** — 8 个数据库迁移 + 不可变触发器 + sqlboiler 模型 + 仓储层。注意 Q20 已定 **PostgreSQL**。
5. **加任何 RPC 之前**先解决"坑 1"的工具链校准。
6. **前端可并行**：`W5-01`（对话工作区）、`W7-04`（覆盖轮询状态）、`W8-04`/`W8-06`。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | **运行中**，PID 18809，启动参数 `-portfoliomanager=false -llmmanager`，9052/9053 |
| webUI 代理 | 运行中，8788 |
| Vite dev | 运行中，5173 |
| proto 工具链 | `~/go/bin/buf` **1.73.0** + 4 个 `protoc-gen-*` 已装（**版本未校准，见坑 1**） |
| 重启 bot | `cd <repo> && ./gocryptotrader -portfoliomanager=false -llmmanager` |
| 重启前端 | `cd webUI && pnpm dev:all` |
