# session-23：重载→扫描接缝（M2-16 残留闭环）+ M2-01 K 线取数共享 + M0-06 模型生成 + M0-07 仓储层 + M1-09~12 + M2-07/M2-07b 工具契约与执行器

**日期**：2026-10-10
**范围**：① 补齐 `M2-16` 的残留接缝——轮询执行器如何把 588 个市场的整点重载结果喂进 `TriggerScanner`；② `M2-01`——把 rpcserver 的 K 线取数逻辑抽成 RPC 与（将来的）LLM 工具共用的函数；③ `M0-06`——用 sqlboiler 重新生成 postgres + sqlite3 两份模型（含全部 24 张 `llm_*` 表）；④ `M0-07`——`database/repository/llm` 仓储层 8 域 + 修复 `--wipe` 再生成的两个副作用（附件四）；⑤ `M1-09`——Provider 档案/修订版管理面（附件五）；⑥ `M1-10`——唯一启用配对的 CAS 语义 + readiness（附件六）；⑦ `M1-11`——预算与预约硬闸（附件七）；⑧ `M1-12`——工具调用循环（附件八）；⑨ `M2-07`（半）——工具契约与校验层（附件九）
**前置**：session-22（M2-13 触发器扫描 + M2-16 整点重载；其工作日志 §8 登记了"**扫描器没有任何调用方**——这是一处需要设计的接缝"）；session-17（pg provisioning，含原生临时集群路径）

---

## 1. 会话目标

四个目标，都是"把已有的件连起来"或"把已有的 schema 兑现成代码"：

1. 让整点重载的数据真正流进触发器扫描器，闭合第一层价值链——**轮询取数 → 整点重载 → 指标派生事实 → 触发事件队列**；
2. 抽出 K 线取数共享函数（M2-01），并顺带把 **Q4=B 按需只读**（被 `currency not enabled` 挡住）的代码路径打通；
3. 生成两份 sqlboiler 模型（M0-06），让 `llm_*` 的 24 张表在 Go 类型层面可写（M0-07 的前置）。
4. 落地 `database/repository/llm` 仓储层（M0-07），并修复 `--wipe` 再生成暴露的两个副作用（附件四）。

## 2. 交付物

| 文件 | 内容 |
| --- | --- |
| `engine/llm_rotation_scan.go` | **新增**：`factFromWindow` · `closedCandles` · `deriveSignals` · `averageTrueRange` · `attachScanner`/`Scanner()` · `scanAfterReload` |
| `engine/llm_rotation.go` | **改动**：`fetchFullWindow` 改为返回 `*kline.Item`（原先抓完就丢）；`Step` 的重载分支尾部调用 `scanAfterReload` |
| `engine/llm_rotation_test.go` | **改动**：假 source 增加 `candles *kline.Item` 字段并在 `GetHistoricCandles` 返回它；+5 个新测试 |
| `engine/historic_candles.go` | **新增**：`iHistoricCandlesSource`（2 方法窄接口）· `historicCandlesOptions`（四模式）· `getHistoricCandlesShared` |
| `engine/rpcserver.go` | **改动**：`GetHistoricCandles` handler 缩成薄封装（解析/校验/`checkParams` 留在原地，取数交给共享函数）；删掉不再使用的 `exchangeDB` import |
| `engine/historic_candles_test.go` | **新增**：3 个离线测试 |
| `database/models/sqlite3/` | **再生成**：29 → 77 文件（+48 = 24 张 `llm_*` × 模型+测试），3 个既有文件机械改动 |
| `database/models/postgres/` | **再生成**：31 → 79 文件（+48），6 个既有文件机械改动 |
| `database/repository/llm/` | **新增（M0-07）**：`llm.go` + `provider.go` · `conversation.go` · `strategy.go` · `decision.go` · `order.go` · `budget.go` · `reconcile.go` · `audit.go`——仓储层 8 域 |
| `database/repository/llm/llm_repository_test.go` + `llm_domains_test.go` | **新增**：12 个端到端测试（临时 sqlite 库 + 跑全部迁移 + 触发器在环） |
| `scripts/normalize_llm_model_tests.py` | **新增**：再生成归一化工具（摘除与触发器冲突的生成测试注册 + 自动回植 boil_main 补丁；幂等） |
| `engine/llm_provider_admin.go` + `llm_provider_admin_test.go` | **新增（M1-09）**：Provider 档案/修订版管理面（每 kind 字段规则 + 不可变修订版 + 编辑活跃档案三联动）+ 6 个测试（含 17 条校验矩阵） |
| `engine/llm_active_pair.go` + `llm_active_pair_test.go`；`database/repository/llm/provider.go` 增补 | **新增（M1-10）**：CAS 启用（`ActivateModelPair` + 仓储 `CasActiveModelPair` / `GetPairSwitchCommandByIdempotencyKey`）+ 6 个测试 |
| `engine/llm_budget.go` + `llm_budget_test.go`；`database/repository/llm/budget.go` 增补 | **新增（M1-11）**：预算与预约硬闸（`ReserveBudget` + 原子 `TryReserveBudget` + 6 个测试） |
| `engine/llm_tool_loop.go` + `llm_tool_loop_test.go` | **新增（M1-12）**：工具调用循环（`RunToolLoop` + `ChatCompleter`/`ToolRunner` 窄接口 + 9 个 reason code + 8 个全离线测试） |
| `llm/tools/tools.go` + `tools_test.go` | **新增（M2-07 半）**：8 个工具契约（JSON Schema + 严格解码 + 语义/跨字段校验 + 默认关闭的 `Scope`）+ 6 个测试；执行器拆出 `M2-07b` |
| `engine/llm_tools.go` + `llm_tools_test.go` | **新增（M2-07b）**：8 个工具执行器（`LLMToolRunner` + `iToolDataSource` 窄接口 + 每次产出即跑四道闸）+ 8 个测试 |

## 3. 附件一：接缝的设计选择——为什么扫描挂在**重载后**而不是增量后

session-22 §8 给了两条路：

- (a) 执行器为每个市场组装一次**轻量快照**——贵：588 次指标计算/周期；
- (b) 扫描器直接从**取数结果**构造事实——省。

本会话实现的是 (b) 的变体 **(b′)：从重载窗口构造**，理由：

1. **增量窗口算不出它要比的指标**。增量只抓 5 根 bar；`trigger.Detect` 要比的是 MACD（最小根数 `slow+signal-2 = 33`）与均线位置（最长周期 200）。喂增量不是"省"，是"永远比较不了"。
2. **重载窗口恰好就是完整指标历史**（250 根），它本来就是为了重建指标窗口而抓的。
3. **零额外交易所权重**：扫描复用重载抓到的 `kline.Item`，不新增任何请求。session-22 要求"先确认 coverage 预算能否覆盖"，答案是不需要覆盖——这部分是免费的。这也是把 `fetchFullWindow` 从"抓完就丢"改成返回 `*kline.Item` 的原因：**同一段历史抓两次，就是为同一个答案付两次交易所权重**。

其余决定：

- `closedCandles` 只取已收盘的 bar——未收盘 bar 的"收盘价"是幻觉，用它派生信号等于按一个还在变化的价格行动。
- `deriveSignals` 从窗口内已算好的指标读数派生 `trigger.Fact`，不重算指标（一处计算、两处读取）。
- `averageTrueRange` 在 `engine/` 本地实现——`llm/` 不得 import TA 包（边界测试 allowlist 是穷尽式的），跨包转换只发生在 `engine/`。
- 588 个市场各留各的 `previous`（`TestScannerKeepsPerMarketPreviousStateAcrossReloads` 断言跨重载不串台）。
- 扫描器**可选**：未装配 → `Step` 照常走（`TestScannerIsOptional`）。第一层不因扫描缺席而失败。

### 附件一：两处真实缺陷（测试抓出来的）

1. **`scanAfterReload` 在 nil `*Rotator` 上 panic**。其他每个 `Rotator` 方法都有 nil guard，新增的这个漏了。修复：`if r == nil || r.scanner == nil || item == nil { return nil }`。
2. **最长均线周期不可用时整扫被拒**。`deriveSignals` 初版要求 `len(candles) >= longest`，否则报错——于是新上市、历史短的合约（`MA(200)` 算不了）**每小时都会被拒掉整条 close 信号**，而 close 信号恰恰是这些合约唯一可靠的信号。修复：**回退到窗口能支持的最长周期**；一根均线算不出，不代表收盘价不可用。
3. （预防性）MACD 根数不足时**先查 `snapshot.MinCandlesForMACD` 再调仓库 TA**——否则仓库的错误会以"供应商错误"的形态冒上来，被误当成输入问题。现在返回我们的哨兵 `snapshot.ErrNotEnoughCandles`。

### 附件一：夹具三修（每个都值得记，因为都是"测试骗过自己"的形态）

| 修法 | 原因 |
| --- | --- |
| `rising` → `fallingThenRising` | 线性上升序列会让 MACD 柱**恰好收敛到零**——比"没有交叉"更糟：柱值在 0 附近抖动，断言时真时假 |
| 60 根 → 220 根 candle | 60 根连 `MA(200)` 的种子都不够，测不到回退路径 |
| `scanWindow` 按整点对齐 | fixture 从 12:00:00 开始但测的是不触发重载的增量路径——整点对齐后重载/增量两条路径可分别构造 |

另有两处一笔带过的构建修复：假 source 补 `candles` 字段；测试文件补 `snapshot`/`trigger` import。

## 4. 附件二：M2-01 K 线取数共享——抽取，不是重写

doc 11 §4 的验收是"RPC handler 和 LLM 工具调同一函数；四种模式行为不变"。做法：

- 新文件 `engine/historic_candles.go`：**`iHistoricCandlesSource` 2 方法窄接口**（`GetHistoricCandles`/`GetHistoricCandlesExtended`，编译期断言 `exchange.IBotExchange` 满足它——沿用 `iMarketDataSource`/`iRotationSource` 的模式，将来的 LLM 工具测试不需要 bot）+ `historicCandlesOptions`（use-db / ex-request / sync / fill-trades 四模式）+ `getHistoricCandlesShared`。
- `rpcserver.go` 的 handler 缩成薄封装：解析时间、`checkParams`、组 `resp` 三段**留在原地**，中间取数段整体替换为一次共享函数调用。原先 handler 里分散的 fetch → fill → resp → sync 顺序改为 fetch → fill → sync 在共享函数内完成——可观测行为不变（sync 失败时两条路径都返回错误、不返回响应）。

**关键决定：共享函数刻意不做 enabled-pair 校验。** 读代码核实过：

- `currency not enabled` **只来自 rpcserver 的 `checkParams`**（它拒绝不在 enabled 子集里的 pair）；
- 币安 wrapper 的 `GetHistoricCandles` 直奔 `UKlineData` 取数，**不要求 pair 已启用**。

于是两层各自守门槛：RPC 层守参数合法性（现状不变），LLM 层将守市场范围（M2-07 工具的作用域锁定）。**这就是 Q4=B 按需只读的解锁点**——共享函数不带该门槛后，"读任意合约"只受 LLM 层自己的范围策略约束。

**验证策略：抽取不重写。** 四模式行为不变**由既有 `TestGetHistoricCandles` 端到端把守**（default / ex_request / sync / db+trades 四段全跑，且 db+trades 段顺带覆盖 fill 模式），新测试只覆盖新接缝：模式选择（断言走了哪条取数路径）、错误透传、**无 enabled 门槛**（stub 根本没有 enabled 概念，任意 pair 仍可取回）。

## 5. 附件三：M0-06 sqlboiler 模型生成（pg + sqlite3 两份）

### 工具链（全部实测）

| 二进制 | 来源 | 装机命令 |
| --- | --- | --- |
| `sqlboiler`（主程序） | 分叉 `github.com/thrasher-corp/sqlboiler@v1.0.1-0.20191001234224-71e17f37a85e`（自报版本 `SQLBoiler v3.5.0-gct`，与既有模型文件头一致） | `go install` 同 pin |
| `sqlboiler-psql` | 分叉内 `drivers/sqlboiler-psql` | `go install …/drivers/sqlboiler-psql@<pin>` |
| `sqlboiler-sqlite3` | **独立仓库** `github.com/thrasher-corp/sqlboiler-sqlite3`（**不在分叉里，上游 volatiletech v3.7.1 里也没有**） | `go install github.com/thrasher-corp/sqlboiler-sqlite3@latest` |

- 主程序用 `exec.LookPath("sqlboiler-"+driver)` 找驱动——三个二进制都要在 PATH（`~/go/bin`）里，且**不装在 PATH 就会报 "binary driver …/sqlboiler-sqlite3: no such file or directory"**（第一次失败就是这么来的）。

### pg 半：本机 Docker 不可用于此

`docker compose up -d postgres` 失败：**`all predefined address pools have been fully subnetted`**（Docker 地址池耗尽——环境级问题，影响**所有** compose 项目，不只这一份）。**没有**动 `docker network prune`（会碰用户其他项目的网络）。改用 session-17 已证的原生临时集群路径：

```bash
PGBIN=/opt/homebrew/opt/postgresql@17/bin
$PGBIN/initdb -D /tmp/gct-pg-m006/data -U gct -A trust
$PGBIN/pg_ctl -D /tmp/gct-pg-m006/data -o "-p 55432 -k /tmp/gct-pg-m006 -c listen_addresses=127.0.0.1" -l /tmp/gct-pg-m006/pg.log start
$PGBIN/createdb -h 127.0.0.1 -p 55432 -U gct gocryptotrader
cd cmd/dbmigrate && ../../dbmigrate -config /tmp/gct-pg-m006/config.json -command up
```

**新坑（session-17 没记到这一步）**：`initdb` 后直接启动会失败——`致命错误: 在启动期间 postmaster 变成多线程的`（macOS 区域设置问题）。**`export LC_ALL=C` 后即启动成功**。

pg config（`/tmp/gct-pg-m006/config.json`）= 本机 sqlite config 的 `database` 块替换为 `driver=postgres`、`127.0.0.1:55432`、用户 `gct`、`sslmode=disable`。10 个迁移全部 `OK`（current version `20261010120800`）。

### 生成结果与幂等性

| 方言 | 文件数 | 新增 | 被改动的既有文件 |
| --- | --- | --- | --- |
| sqlite3 | 29 → **77** | +48（24 张 `llm_*` × 模型+测试） | 3：`boil_suites_test.go`、`boil_table_names.go`、`script.go` |
| postgres | 31 → **79** | +48（同上） | 6：上述 3 个 + `boil_main_test.go`、`psql_suites_test.go`、`withdrawal_history.go` |

所有既有文件的改动都是**机械的**：

- 共享 where-helper **依新表集合换了宿主文件**（如 `whereHelpernull_Bytes` 从 `script.go` 移入 `llm_chat_messages.go`——sqlboiler 每个类型只在一个文件里定义，宿主由排序决定）；
- `boil_table_names.go` / `boil_suites_test.go` / `psql_suites_test.go` 的表名与测试套件清单；
- `boil_main_test.go` 删掉一个未被调用的 config-key 检查（连带其 `strings`/`viper` 引用——`go vet` 连测试文件一起编译，通过说明没有残留未用 import）。

**没有任何一个既有表出现列级差异**——这是"库与迁移一致"的证明，也是再生成幂等性的实测（对照 session-16 记录的 buf 非幂等教训）。

### 一个必须记录的生成器限制

`datahistoryjobrelations`（**复合主键**联接表）**两种方言都没有被生成**——既有检出里同样没有（两种方言都没有），所以**是生成器行为，不是本次生成的回归**。M0-07 仓储层若要用这张表，得手写模型或换生成方式。

### 清理

`pg_ctl stop` + `rm -rf /tmp/gct-pg-m006`（与 session-17 一致，不留后台进程）。**sqlboiler 三个二进制留在 `~/go/bin`**——下次改 schema 再生成还要用。

## 6. 附件四：M0-07 仓储层 + M0-06 再生成的两个副作用（同日修复并工具化）

### 6.1 M0-07：8 域仓储层（`database/repository/llm/`）

- 新包结构：`llm.go`（可用性守卫/事务助手/双方言转换助手）+ `provider.go`（档案、修订版含 supersede、单行 pair upsert、探测结果、配对切换命令）· `conversation.go`（会话 + 消息：**seq 在事务内分配**、message_count 维护）· `strategy.go`（策略/版本/审批/状态迁移/交易授权——冻结与撤销只写触发器允许的列）· `decision.go`（快照/决策运行/风控评估）· `order.go`（意图生命周期/保护离场）· `budget.go`（预算预定滑窗 + new-risk 滑窗计数）· `reconcile.go`（日初/外部流/对账/halt）· `audit.go`。
- **12 个端到端测试**（`testhelpers.ConnectToDatabase` 起临时 sqlite 库、**跑全部迁移所以不可变触发器真实生效**）。测试抓出的真缺陷：**修订版的 supersede 初版把刚插入的那一行也 supersede 了**（`UPDATE ... WHERE superseded_at IS NULL` 匹配到自己）——修复 `AND id != ?`。触发器对抗验证（不是 mock）：冻结二次写入被拒、审批改 `execution_mode` 被拒、意图改 `quantity` 被拒。
- **方言差异（写下一层代码前必须背下来的表）**：sqlite INTEGER→`int64`/`null.Int64`，pg→`int`/`null.Int`；时间戳 sqlite=RFC3339 文本、pg=`time.Time`；布尔 sqlite=`int64(0/1)`、pg=`bool`；转换只发生在仓储层。生成器 helper 名还有 y→ies 变形（`LLMDayStartInventories`）。
- 边界：`llm/` **不 import 本包**（allowlist），消费者在 `engine/`。

### 6.2 `--wipe` 再生成的两个副作用（都是 M0-06 埋下的，本次修复）

1. **覆盖了 `postgres/boil_main_test.go` 的项目补丁**。HEAD 版本带一个项目自有补丁：缺 DB 配置时**优雅跳过** postgres ORM 测试（`missingPSQLConfigKeys` + 打印 + `os.Exit(0)`，TODO#1818）。`--wipe` 把它换成上游模板（无此逻辑）→ pg 模型测试从"跳过"变"硬失败"。修复：归一化脚本**自动回植**（锚点插入 helper + 调用块，幂等；锚点缺失会报错而不是静默）。
2. **生成的 llm_* CRUD 测试与不可变触发器天然冲突**（136 个子测试失败）：11 张受保护表的 delete 系测试 + 关系测试（`...To...LLMDecisionRuns` 等）+ `strategy_versions` 的 update 系（写冻结列）+ 单行表 `CHECK (id=1)` 让通用 randomize 套件无法插入。修复方式：**摘注册，不改断言，不删文件**——从 suite 文件移除 `t.Run(..., testLLM…)` 注册行（累计 598 行；脚本幂等，复跑 0 变更）；生成测试文件保留（兄弟测试文件引用其类型映射如 `llmChatMessageDBTypes`；未注册的包级 helper 照常编译、不运行）。

工具：`scripts/normalize_llm_model_tests.py`。**恢复/验证顺序：`make gen_db_models`（两方言）→ `python3 scripts/normalize_llm_model_tests.py` → `go test ./database/models/...`（pg 优雅跳过 / sqlite 全绿）。**

**附带小坑**：Makefile 的 `gen_db_models` 依赖 `target/sqlboiler.json`，但该文件**不会因 DRIVER 改变而重建**——切方言生成前必须先手动跑 `go run ./cmd/gen_sqlboiler_config/main.go -config <该方言的配置> -outdir target/`，否则拿旧方言的 json 去连新驱动（本次就是先 `DRIVER=sqlite3` 失败、报错信息在 sqlboiler 侧）。

## 7. 附件五：M1-09 Provider 档案/修订版管理面

- `engine/llm_provider_admin.go`：`CreateProviderProfile` + `AddProviderRevision`（+ 纯函数 `validateRevisionParams` 与 `invalidateActivePairForRevision`）。消费 `database/repository/llm`（alias `llmstore`），不写裸 SQL。
- **校验层与契约层分离**：字段与 kind 的归属是机器规则——JEV 上用工具字段、LLM 上用模板版本都被拒（`ErrKindFieldMismatch`）；17 条校验矩阵逐条测试。要点：**http 明文仅限被显式豁免的回环主机**（远端 http 即使带豁免也拒——防 API key 走明文）；预算三态（NULL/"0"/正十进制）拒前导零/指数/负号；密码指纹恰 16 位小写 hex；ref 必须 `secret://llm/…` 且 kind 恰为 `llm`；JEV 模型必须 `jev-x.y.z` 形（正则）；提示词版本必须过 `openai.PromptFor`（未知版本拒）。
- **编辑活跃档案三联动**（docs 05 §2.1）：新修订版插入（同事务 supersede 旧的）之后，若被 supersede 的正是 active 指针 → 清该指针 + readiness=INCOMPLETE + pair_epoch++；**只动被编辑那一边**（LLM 侧编辑不碰 Jev 指针）、**编辑非活跃档案完全不动配对**——两条都有独立测试。真正的 CAS 启用语义留给 M1-10。
- **测试基建坑（engine 首次接真库）**：setup 里用 `t.TempDir()` 存 sqlite 路径，第一个测试结束目录即被删 → 第二个测试 `unable to open database file`。改为**进程级 `os.MkdirTemp`**（repo 侧用 TestMain 也出于同一原因：临时目录必须活过整个进程）。另外 engine 包要把 `testhelpers.MigrationDir` 指到 `../database/migrations`。
- 时间全部注入（`providerAdminNow`）——沿用"从不读墙上时钟"的全局约定。

## 8. 附件六：M1-10 唯一启用配对（CAS + pair_epoch + readiness）

- `engine/llm_active_pair.go`：`ActivateModelPair`——单行 CAS 的组合层；仓储新增 `CasActiveModelPair`（`UPDATE … WHERE id=1 AND pair_epoch = expected` 原子守卫）与 `GetPairSwitchCommandByIdempotencyKey`。
- **CAS 的原子点在仓储**：引擎的读-改-写只是编排；冲突一律映射 `ErrPairEpochConflict`。首激活（expected=0、无行）走插入路径，**插入竞争 = 丢 CAS**（唯一约束的败者返回 false+nil——不把基建错误伪装成冲突，也不把冲突伪装成错误）。
- **readiness 声明证据**（docs 05 §2.4）：缺一边=INCOMPLETE；两边齐但任一边**最新**探针未过——或**根本没有探针**——=DEGRADED；两边都过=READY。测试含"后到的失败探针胜过更早的通过"。
- **幂等**：每笔落地写 `llm_pair_switch_commands`（键有唯一约束）；同键**同载荷**重放返回已落地状态且**不涨 epoch**；同键**异载荷**拒 `ErrIdempotencyKeyReused`；并发同键的插入竞争在写失败后被复查、仅当载荷一致才算同一条命令。
- 校验：non-live 修订版（已 supersede）拒、槽位 kind 不匹配拒、未知修订版透传 `llmstore.ErrNotFound`、actor/幂等键必填。去激活（双 nil）同样走 CAS 与审计。
- **级联留给 M4**：文档切换语义还含"暂停运行中策略 + 撤销有效审批"——策略与审批子系统 M4 才存在，当前审计行记 0/0。
- 6 个测试：happy path（含审计行断言）/ 就绪三态 / epoch 冲突 / 幂等重放与键复用 / 坏输入五连 / 去激活。

## 9. 附件七：M1-11 预算与预约硬闸

- `engine/llm_budget.go` + 仓储 `TryReserveBudget`。**先占后用**（docs 05 §7.4）落在**单条原子语句**：`INSERT … SELECT … WHERE (窗口计数) < cap`——起占与计数之间不留窗口，双方言参数化（sqlite `?` / pg `$n`）。
- **窗口计数规则**：`USED` 一律计；`RESERVED` **仅在未过期时**计——进程死掉留下的 stale 预约随 TTL 出窗，**不会永久堵死配额**。测试用"25 小时前的 USED"与"已过期的 RESERVED"两个反例钉住。
- **dailyBudgetUsd 三态**（Q15）：NULL=放行（UI 标注"未设定上限"）；**恰 `"0"`=禁止一切模型请求**（`ErrBudgetDisabled`，断言**连预约行都不落**）；正数=计划值放行（软告警属 UI 层）。
- **上限解析** `EffectiveReservationCap`：未配置(0)→100；Jev 封顶 100（配置 500 实得 100）；表驱动 6 例。
- 终态区分有测试：`MarkReservationUsed`（记 token/延迟，**继续占额**）vs `ReleaseReservation`（**释放额度**，释放后可再占）。
- 防误用：superseded 修订版拒、slug kind 不匹配拒、purpose 必填。
- **一处测试自身的数学错误**（值得记）：断言"USED + 在飞 RESERVED 占满 cap 2"时把第三次预留放到了 `+6min`——而第二笔的 TTL 恰在 `+6min` 到期、于是不计数、预留合法地成功了。修正到 `+5m30s`。**窗口边界上的等于号方向是这类测试最常错的地方**（`expires_at > now` 是半开区间）。
- 6 个测试：cap 强制与释放/占用、零态禁止、窗口与陈旧（两反例）、坏输入四连、Jev 独立上限、上限解析表驱动。

## 10. 附件八：M1-12 工具调用循环

- `engine/llm_tool_loop.go`：`RunToolLoop` + **两个窄接口**——`ChatCompleter`（`*openai.Client` 直接满足）与 `ToolRunner`（`Supported`+`Run`，工具本体 M2-07 随后填）。**测试全离线**：脚本化 transport 捕获每个请求 + 假 runner。
- **循环闸在每轮顶部依次判**（docs 05 §5.1）：轮次≥4 / 工具调用≥8 / 累计 token≥24000 / 共同截止——四个方向四条 HOLD reason code。
- **`tool_choice` 轮转**：前 3 轮 `auto`、最后一轮 `none`（DeepSeek thinking 实测口径）；最后一轮仍来 tool_calls → `TOOL_ROUND_INCOMPLETE`。测试断言第二个请求的 choice 已是 none。
- **超时三方钳制**：`min(profile, 30s, 剩余截止)`；截止已过 → **零请求**直接 HOLD；截止在轮间到达 → 第二个模型调用不发生（onCall 钩子推进时钟验证）。
- **工具闸在执行前**：每轮>4 个整轮拒（cap 2 下 3 调用的轮子**工具零执行**）；重复 callId → `TOOL_CALL_DUPLICATE`；非白名单 → `TOOL_CALL_UNSUPPORTED`——**半个工具轮无法喂给模型**（每个 tool_call 都应有配对 tool 消息）。
- **参数不合法回填重试**：`ErrToolArgumentsInvalid` → tool 消息带 `"retry":true`，循环继续；该调用**仍占一个槽位**（模型确实用掉了它）。其他工具运行时错误也回填（`{"error":...}`）而不炸掉整次分析。
- **转录**：callId/name/args/result/sha256（测试用独立 sha256 复核）；结果截断 12KB 后入消息与转录。`finish_reason=="length"`→`TRUNCATED`；最终内容>12000 字符→`CANDIDATE_CONTENT_INVALID`。
- **候选解析/作用域/证据校验不在这层**（需要流水线的快照上下文，属 M4）——文件注释与 TASKS 行均注明。
- **一处测试基建坑**：包内已有 `fixedClock` 助手（同名冲突），改名 `toolLoopClock`；另一处测试自身错误——把注入的过去钟与真实钟混用作截止比较，造出"30s 未被钳制"的假象，改为一钟到底后通过。

## 11. 附件九：M2-07（半）工具契约与校验层

- `llm/tools/`（新包，**纯层**——`llm/` 边界测试原样通过，`llm/` 由此成十包）：`Specs()` 8 个 JSON Schema、8 个 `Decode*`、`Scope`、工具名与边界常量。
- **全部 schema `additionalProperties:false`**（测试逐条断言）——模型塞未声明字段=被拒，不是被忽略。
- **双重校验**（docs 05 §4.6）：`decodeStrict`（`DisallowUnknownFields`+尾随数据拒——`{}{}` 也拒）+ 手写语义规则。**不用反射式 schema 库**：错误信息要具体、可 `errors.Is`。
- **跨字段亮点**：`getIndicators.lookbackBars` 必须盖住 `max(maPeriods)` **和** MACD 最小根数（默认 12/26/9→33；自定义 `slow+signal-2`），`fast<slow` 单列——**要 MA(200) 给 100 根会被拒并回填给模型重试，绝不静默给短序列**。`getFunding` 的 `historyLimit`/`includeHistory` 互锁。
- **作用域锁定（docs 05 §4.3）默认关闭**：nil scope 拒一切；命名的 market/account/strategy 必须恰在授权集内；`CheckEvidence` 直接吃 `candidate.ToolEvidence`（其字段与 docs 05 §4.2 完全一致）。
- **重要发现（写进 M2-08 的行路）**：四道闸的构件大多**已经存在**——时间/体积/敏感键/深度在 `candidate.ToolEvidence.Validate` + `ScanForSensitiveKeys` + `TotalEvidenceBytes`（早前会话落地），作用域在本包 `Scope`。M2-08 剩**接线与端到端**，不是从零写。
- **拆项记录**：执行器（接 kline/orderbook/funding/账户/策略数据源，产 evidence，喂 `RunToolLoop`）拆出为 **`M2-07b`**——半额计分（M2 组 9/16 → 9.5/16），TASKS.md 已加行。

### 9.1 `M2-07b` 侦察结果（**已于 2026-10-11 按此实现，见附件十；此节保留作范式记录**）

本会话已把执行器所需类型全部核到字段级，**下一会话按此一次写对**：

- `fundingrate.LatestRateRequest{Asset, Pair, IncludePredictedRate}`（**binance 上 includePredictedRate 会 500——前端坑位同源，执行器不传**）；`LatestRateResponse{Exchange, Asset, Pair, LatestRate Rate, PredictedUpcomingRate, TimeOfNextRate, TimeChecked}`；`Rate{Time, Rate decimal.Decimal, Payment decimal.Decimal}`；`HistoricalRatesRequest{Asset, Pair, PaymentCurrency, StartDate, EndDate, IncludePayments, IncludePredictedRate, RespectHistoryLimits}`。
- `futures.PositionsRequest{Asset, Pairs, StartDate, EndDate, RespectOrderHistoryLimits}`；`PositionDetails{Exchange, Asset, Pair, Orders []order.Detail}`。
- `accounts.SubAccount{ID, AssetType, Balances CurrencyBalances}`；`SubAccounts []*SubAccount`。
- `orderbook` 档位：`Item{Price, Amount ...}`（**优先 `StrPrice/StrAmount`**——十进制字符串，浮点仅回退，M2-15 同源纪律）。
- **写执行器前先读 `engine/llm_snapshot.go` 约 300-340 行**：那里有 MACD/SMA/EMA 的既有调用与返回微形状（`GetMovingAverageConvergenceDivergenceOnClose(fast,slow,signal)` 等），**照抄而不是猜**；RSI/BBANDS 方法名与返回结构尚未核实。
- **教训（真实发生）**：本会话直接开写执行器首稿，速度过快——出现占位数学与杜撰 import，无法在窗口内收敛；**已删稿保绿**（`go build ./...` 与工具循环测试均验过）。正确顺序是：**先抄 llm_snapshot.go 的既有用法 → 再写执行器 → 一次编译通过**。执行器不要重蹈。

## 12. 附件十：M2-07b 8 个工具执行器（含四道闸接线）——§9.1 计划的落地

**结果**：`engine/llm_tools.go` + `llm_tools_test.go` 8 个测试全过；**上一次会话删掉的坏稿按 §9.1 侦察结果一次写对**（编译仅 2 处小修：`orderbook.Item`→`orderbook.Level`；`marshalJSON` 占位→`json.Marshal`）。

- `LLMToolRunner` 实现 M1-12 的 `ToolRunner`：`Supported` = 契约 `tools.Names()`；`Run` 按名分发（解码→执行→产 evidence）。数据源窄接口 `iToolDataSource`（8 方法），`exchange.IBotExchange` 编译期满足。
- **四道闸在每次产出时执行**（`evidence()` 里）：`candidate.ToolEvidence.Validate`（时间/体积/敏感键/深度）+ `Scope.CheckEvidence`（市场/账户/策略）——**M2-08 的接线由此就地完成**，不再是待办。
- 要点：K 线只取已收盘（与组装器同一判据）≤50 根、`asOf`=最新已收盘；指标 MACD 复用 `GetMovingAverageConvergenceDivergenceOnClose`（`Results`/`SignalVals`/`Histogram`，照 §9.1 抄 `llm_snapshot.go` 的用法），SMA/EMA 用 kline 助手；RSI/ATR/BBANDS 本地 Wilder/σ（文件注 **TODO**：待复核快照包提取面后统一，防双实现漂移）；盘口 ≤20 档 + `DeriveSpreadBPS`/`DeriveMidPrice`（与 M2-15 同源）+ 名义额滑点（数量加权均价与 bps）；`listMarkets` query 过滤 + maxItems 帽；`getAccountState` 账号闸 + 仓位市场 id；策略历史/风控预算走 `llmstore`（真库测试）。
- **不传 `IncludePredictedRate`**（binance 会 500——webUI 坑位同源）；市场 id 三段严格解析（exchange/asset 必须与运行配置一致）。
- 测试含：scope 外市场拒（`tools.ErrScopeDenied`）、错 exchange 拒、跨账号拒、scope 外策略版本拒、滑点数学断言（150 USD 跨 100/101 两档 → 均价 150/1.4950…、bps>0）、线性 ramp 上 SMA 末值 150.5 与 RSI=100、BBANDS upper>middle。

## 13. 账目说明（给接力模型的清账）

- `M2-16` 接缝补齐**不涉及计分**（session-22 已按 `[x]` 计，本次兑现）；
- `M2-01` **新增计分**：M2 组 8/16 → 9/16（3.00% → 3.38%）；
- `M0-06` **新增计分**：M0 组 11/14 → 12/14（3.93% → 4.29%）；
- `M0-07` **新增计分**：M0 组 12/14 → 13/14（4.29% → 4.64%）；
- `M1-09` **新增计分**：M1 组 9/15 → 10/15（3.60% → 4.00%）；
- `M1-10` **新增计分**：M1 组 10/15 → 11/15（4.00% → 4.40%）；
- `M1-11` **新增计分**：M1 组 11/15 → 12/15（4.40% → 4.80%）；
- `M1-12` **新增计分**：M1 组 12/15 → 13/15（4.80% → 5.20%）；
- `M2-07` **半额计分**（校验层完成、执行器拆 `M2-07b`）：M2 组 9/16 → 9.5/16（3.38% → 3.56%）；
- `M2-07b` **新增计分**（执行器完成，含 M2-08 四道闸接线）：M2 组 9.5/16 → 10.5/16（3.56% → 3.94%）；
- **总完成度 41.2% → 44.4%**（session-23 累计 +3.2）。

## 14. 验证证据

```text
gofmt -l engine/ llm/                        # 无输出
go build ./...                               # 通过（含两份再生成的模型）
go vet ./llm/... ./engine/                   # 无输出
go vet ./database/models/postgres/ ./database/models/sqlite3/   # 无输出（连测试文件编译）
go test ./llm/... -count 1                   # 九包全绿（340 个测试函数）
go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'
                                             # ok  github.com/.../engine  77.243s
```

新测试按名通过：

```text
--- PASS: TestReloadFeedsTheScanner (0.00s)
--- PASS: TestScannerKeepsPerMarketPreviousStateAcrossReloads (0.00s)
--- PASS: TestScannerIsOptional (0.00s)
--- PASS: TestFactFromWindowUsesOnlyClosedCandles (0.00s)
--- PASS: TestAverageTrueRange (0.00s)
--- PASS: TestGetHistoricCandles (5.02s)                        # 既有端到端，四模式回归
--- PASS: TestGetHistoricCandlesSharedModeSelection (0.00s)     # 含 2 个子测试
--- PASS: TestGetHistoricCandlesSharedPropagatesError (0.00s)
--- PASS: TestGetHistoricCandlesSharedDoesNotGateOnEnabledPairs (0.00s)
```

生成产物核对：`ls database/models/{sqlite3,postgres} | grep -c '^llm'` → **48 / 48**；`git status --short database/models | wc -l` → **105**（48+48 新增 + 9 改动）。
（engine 整套两次：接缝验证 119.1s、M2-01 验证 77.2s——机器负载差异，非回归。）

M0-07 与归一化（附件四）新增证据：

```text
go test ./database/repository/llm/ -count 1        # ok（12 个测试：TestProfilesAndRevisions … TestAuditEvents）
go test ./database/models/postgres/ -count 1       # ok（缺 DB 配置 → 优雅跳过；补丁已回植）
go test ./database/models/sqlite3/ -count 1        # ok（与触发器冲突的生成测试注册已摘除）
python3 scripts/normalize_llm_model_tests.py       # 幂等：复跑 removed 0 suite registration lines
go build ./... && go vet ./database/...            # 全过
go test ./engine/ -run 'TestCreateProviderProfile|TestAddProviderRevision' -count 1   # ok（6 个测试全过）
go test ./engine/ -run 'TestActivateModelPair' -count 1                               # ok（6 个测试全过）
go test ./engine/ -run 'TestEffectiveReservationCap|TestReserveBudget' -count 1       # ok（6 个测试全过）
go test ./engine/ -run 'TestToolLoop' -count 1                                        # ok（8 个测试全过）
go test ./llm/tools/ -count 1                                                         # ok（6 个测试全过）
go test ./engine/ -run 'TestLLMTools' -count 1                                        # ok（8 个测试全过）
go test ./engine/ -run 'TestCreateProviderProfile|TestAddProviderRevision|TestActivateModelPair|TestEffectiveReservationCap|TestReserveBudget|TestToolLoop|TestLLMTools' -count 1   # ok（整套 M1/M2 引擎测试）
go test ./llm/... -count 1                                                            # 十包全绿（llm/tools 加入后边界测试原样通过）
go test ./database/repository/llm/ -count 1                                           # ok（CAS/幂等查询增补后复跑全绿）
```

模型测试从 **136 个子测试失败 → 全绿**（前者是触发器在正确发挥作用；修复的是"生成测试断言了 schema 禁止的行为"这一层错配）。

## 15. 未完成 / 下一步建议

**第一层数据面闭环**（取数 → 重载 → 扫描 → 触发队列）；共享取数已就位、Q4=B 的代码路径已通；**M0-07 仓储层已落地**（8 域 + 12 个端到端测试）；**M1-09~M1-12 已落地**（档案/修订版 + CAS 启用 + 预算硬闸 + 工具循环）；**M2-07 契约层已落地**（执行器 → `M2-07b`）。仍缺的件（按建议顺序）：

1. `M2-02` K 线缓存（已收盘永久 / 未收盘不缓存、交易对移除时失效）——组装器与工具执行器的取数路径都已就位，缓存是共同乘数；随后 `M2-05` 质量标签应用与 `M2-09`~`M2-11` 工具转录/同源事实
2. `M2-02` K 线缓存（已收盘永久 / 未收盘不缓存）——`M2-01` 的下一个乘数
3. proto 工具链校准（`TASKS.md` §0.1）——解锁 `M1-14` + `W7-07` + W5/W6 接口层
4. `M2-13` 余两项：`KindFundingWindow` 分支（默认关的配置项）与账户事件生产者（属 M5 账户面）

## 16. 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot（9052/9053） | 未确认（本会话全部离线测试，不需要 bot） |
| webUI 代理（8788）/ Vite（5173） | 未启动 |
| postgres 临时集群（55432） | **已停止并删除**（`/tmp/gct-pg-m006` 与 `/tmp/gct-pg-m006b` 均已清） |
| 归一化脚本 | `scripts/normalize_llm_model_tests.py` **已入库**；模型套件已归一化（注册摘除 598 行 + boil_main 补丁已回植）；`make gen_db_models` 之后必跑 |
| `docker compose up postgres` | **本机不可用**（地址池耗尽）——要用 pg 走原生集群路径 |
| `~/go/bin/` | 新增 `sqlboiler`、`sqlboiler-psql`、`sqlboiler-sqlite3`（保留，regenerate 要用） |
| `~/.gocryptotrader/config.json` | 未改（仍是 sqlite3 驱动） |
| `target/sqlboiler.json` | 残留（后写的是 pg 版；gitignored，无影响） |

重启方式见 `PROGRESS.md` §22。
