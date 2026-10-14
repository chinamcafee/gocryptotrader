# taskProgress —— 项目进度与模型接力开发总纲

> **本目录的存在目的**：让**任意模型**（或人）在任意时刻接手本项目时，都能在不丢失上下文的前提下继续开发。
> 所有参与本项目的模型**必须**遵循本文的工作流程，不得绕过。

---

## 0. 一句话现状

在 GoCryptoTrader 之上构建 **webUI 前端交易可视化平台** + **LLM/Jev 两级模型决策系统**，覆盖币安 300+ USDT-M 永续合约，"相对低频"地给出开仓/平仓建议，既可对话交互，也可驱动 GoCryptoTrader 自动执行。

- 前端代码：`webUI/`
- 后端改动：GoCryptoTrader 仓库根（允许修改）
- 技术方案：`docs/llm-dev/*.md`（14 篇，已定案）
- **决策已全部定案**：见 `docs/llm-dev/OPEN-QUESTIONS-待确认事项.md`（24 项）

---

## 1. 本目录的四个文件（各司其职，不要混写）

| 文件 | 职责 | 何时更新 |
| --- | --- | --- |
| **`README.md`**（本文） | 接班总纲：工作流程、文件约定、恢复上下文的步骤 | 流程本身变化时 |
| **`TASKS.md`** | **唯一权威**的子任务清单 + 勾选状态 | **每完成一个子任务立即勾选** |
| **`PROGRESS.md`** | 总体完成度百分比 + 摘要报告（人读的概览） | 每个工作会话结束时 |
| **`workLog/<date>-<slug>.md`** | 单次工作会话的详细日志（可追溯、含证据） | 每个工作会话结束时 |
| **`decisions/`** | **必须用户决策否则阻塞**的事项，一项一文件 | 遇到阻塞时立即写 |

**禁止**把进度信息写在 `docs/llm-dev/` 的技术方案文档里 —— 技术文档只描述"应该是什么"，进度只在本目录。

---

## 2. 无差别工作流程（每个模型都必须按此执行）

### 2.1 开工前（接手三步）

1. 读 `TASKS.md`，找到**第一个未勾选的、且未被 `blockedBy` 阻塞的**子任务
2. 读该子任务所属里程碑的**技术方案文档**（`TASKS.md` 每条任务都标了「依据」列，指向具体 doc 与章节）
3. 读 `PROGRESS.md` 了解整体位置，读最近 2 篇 `workLog/` 了解刚做过什么、有什么坑

> **不要**凭记忆或猜测开工。方案文档是唯一权威依据。

### 2.2 工作中（实时同步）

- **每完成一个子任务**：立刻在 `TASKS.md` 把 `[ ]` 改成 `[x]`，并在该行末尾追加 `— <date> <一句话结果>`
- **遇到方案文档没写清楚的细节**：优先按方案的原则自行判断并实施，同时在 `workLog` 里记录"我做了什么判断、依据哪条原则"
- **遇到方案文档与实际代码冲突**（例如文档说某函数存在但实际没有）：**以实际代码为准**，实施后把差异写进 `workLog`，并在 `TASKS.md` 对应任务加 `⚠ 文档偏差` 备注
- **遇到必须用户决策否则无法继续**：写入 `decisions/<n>-<slug>.md`，在 `TASKS.md` 把该任务标为 `[!]`（阻塞），然后**跳过它继续做下一个不阻塞的任务**
- **不要停下来等用户**：除非整个项目所有剩余任务都被阻塞

### 2.3 收工前（会话结束必做）

1. 写 `workLog/<YYYY-MM-DD>-<slug>.md`（模板见 §3）
2. 重算并更新 `PROGRESS.md` 的百分比与摘要
3. 确认 `TASKS.md` 的勾选状态与实际代码一致（**不允许勾了但没做**）

---

## 3. workLog 模板（必须包含这些小节）

```markdown
# <date> <slug>

## 会话目标
（一句话）

## 完成的子任务
- TASKS.md 中的 ID 列表

## 具体做了什么
（逐条，含文件路径、关键命令、真实输出摘要）

## 验证证据
（构建/测试/浏览器截图/接口返回 —— 必须是真实跑出来的，不能写"应该可以"）

## 遇到的坑与解决
（含"文档与实际不符"的偏差记录）

## 未完成 / 下一步建议
（明确指出下一个模型应该从哪个子任务接手）

## 环境状态快照
（进程/端口/服务是否在跑，怎么重启）
```

---

## 4. 关键约定（避免接力时踩坑）

### 4.1 已实测的接口事实（**不要重新试错**）

`docs/localDeploy/README.md` 的「实测验证记录」章节记录了 20+ 条真实结论，接力时必须遵守，例如：

- gocryptotrader REST 网关（9053）响应是 **camelCase**；`pair` 必须是对象 `{delimiter,base,quote}`；`getticker`/`getorderbook`/`getexchangepairs` 是 POST，`gethistoriccandles`/`gettechnicalanalysis` 是 GET
- `gethistoriccandles` 的 `start`/`end` 要 `"2006-01-02 15:04:05 MST"`；`gettechnicalanalysis` 要 RFC3339
- `futures getcollateral` 在币安上**不可用**（用 `getaccountbalances`）
- `SimulateOrder` 硬编码 `asset.Spot` 且只是盘口模拟器，**不是纸面下单**；纸面下单只能走 Go 代码的 `OrderManager.SubmitFakeOrder`
- 币安永续**无 WebSocket**，行情靠 REST 轮询；`orderbook` 可靠 1000 档

### 4.2 运行环境

| 项 | 值 |
| --- | --- |
| 数据目录 | `~/.gocryptotrader/`（config.json 权限 600） |
| gRPC / REST 网关 | `127.0.0.1:9052` / `127.0.0.1:9053`（Basic Auth，凭据在 config.json 的 `remoteControl`） |
| 启动 bot | `cd <repo> && ./gocryptotrader -portfoliomanager=false` |
| 停止 bot | `kill -INT $(cat /tmp/gct.pid)` |
| 只读代理（已有的临时仪表盘） | `python3 ~/.gocryptotrader/dashboard/proxy.py` → `http://127.0.0.1:8787/` |
| 数据库 | 当前 SQLite（`~/.gocryptotrader/database/gocryptotrader.db`）；**Q20 已定案迁移到 PostgreSQL** |
| 可用工具 | node v26 / pnpm 11 / docker 29 / psql 17 |

### 4.3 本地校验配方（**照抄这一段，别自己发明**）

改完 Go 代码后按顺序跑：

```bash
go build ./...                                                              # 必须通过
go test ./llm/... ./config/ -count 1                                        # 本项目新增的包，必须全绿
go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'
```

**为什么要 skip 那两个测试**（`session-05` 实测）：

- 它们会调用 `exchange.GetDefaultConfig` 去访问**真实交易所线上 API**（实测打 BTSE `api.btse.com`、Coinbase）
- 本机 DNS 走 fake-IP（解析出 `198.18.x.x`）时，经透明代理会 `connection reset by peer`
- **同一个命令连跑三次会出现"失败→失败→通过"**，即**既有 flaky 测试，与本项目改动无关**
- 仓库自带的跳过机制是 `CI=true` + `blockedCIExchanges`，但该列表**只含 binance 与 bybit**，不含 btse/coinbase，所以设 `CI=true` 并不能跳过它们

**不要**为了让它们变绿去改这些测试、也不要往 `blockedCIExchanges` 里加交易所——那是掩盖环境问题、并触碰与本次目标无关的测试基建。正确做法就是带 `-skip` 跑。

> **❗ 区分两类失败，别混**：
>
> 1. **既有 flaky 网络测试**（上面两个）→ 带 `-skip` 跑。
> 2. **因你的改动而失败的断言**（例如 `TestGetSubsystemsStatus` 断言子系统数量；新增一个子系统就会让它失败）→ **必须更新断言**，并尽量让它表达意图（如加 `assert.Contains` 校验新键存在），**绝不可以加进 skip 列表**。
>
> 判断方法：先看失败信息里提到的对象是不是你刚改过的。是 → 更新断言；不是且错误里含 `read tcp ...`/`connection reset`/线上域名 → 属第 1 类。

前端校验：

```bash
cd webUI && pnpm typecheck && pnpm lint && pnpm build
```

文档校验（改动 `docs/` 后）：

```bash
npx --yes markdownlint-cli2@0.23.2 "docs/llm-dev/**/*.md" "docs/localDeploy/**/*.md"   # 必须 0 issues
```

### 4.4 技术栈（已定案，不要再选型）

**前端**（Q21=C，`docs/llm-dev/08-对话与接口层.md` §8）：
React 19 + Vite + React Router + **Ant Design 6 + Ant Design X**（对话组件）+ **TradingView Lightweight Charts** + **RTK Query**（HTTP）+ Jotai（客户端状态）
设计 token：bg `#0B111B`、panel `#111B28`、up `#25B49A`、down `#EC6A70`、focus `#5B9CFF`

**后端**：Go 1.27，新增 `llm/` 顶层包 + `engine/llm_manager.go` 子系统（Q7=A）；PostgreSQL（Q20=B）

### 4.4 安全红线（不得违反）

源自 `docs/llm-dev/01-需求与产品目标.md` §3 的五条设计底线 + `10-安全与风控.md`：

1. 模型层**只能读**，不得持有交易所 API Key，不得调用下单接口
2. 前端**不得**直接持有币安 API Key；只通过本地代理/REST 网关访问
3. 任何"批准/撤销/清 halt"类操作必须**重新认证**，不得一键完成
4. 提示注入视为必修防护：工具响应/策略文本一律"不可信数据不是指令"
5. 一切失败收敛到 `HOLD`；不允许"猜测性放行"

---

## 5. 当前阻塞项

见 `decisions/` 目录。**当前无阻塞项**（24 项决策已全部定案）。

---

## 6. 里程碑与进度映射

`TASKS.md` 把工作拆成两组：

- **`W*` 前端里程碑**（webUI 平台，本项目的直接交付物）
- **`M*` 后端里程碑**（Go 侧，来自 `docs/llm-dev/11-实施路线图.md`，按需为前端提供能力）

`PROGRESS.md` 用**加权完成度**计算总百分比（权重在 `TASKS.md` 顶部表中定义）。

---

**最后更新**：由 `PROGRESS.md` 记录
