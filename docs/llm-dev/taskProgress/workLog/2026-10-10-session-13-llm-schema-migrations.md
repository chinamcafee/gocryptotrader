# session-13：24 张 LLM 表迁移（M0-04）

**日期**：2026-10-10
**范围**：`M0-04` 数据库迁移 8 个（`database/migrations/<ts>_llm_*`，pg + sqlite 双份）
**前置**：session-12（M1-13 + M0-13）完成

---

## 1. 交付物

`database/migrations/` 新增 8 个目录 × 2 份 SQL，共 **24 张表**：

| 迁移目录 | 表 | 张数 |
| --- | --- | --- |
| `20261010120000_llm_provider_profiles` | `llm_provider_profiles`、`llm_provider_profile_revisions`、`llm_active_model_pair`、`llm_provider_probe_results`、`llm_pair_switch_commands` | 5 |
| `20261010120100_llm_conversations` | `llm_conversations`、`llm_chat_messages` | 2 |
| `20261010120200_llm_strategies` | `llm_strategies`、`llm_strategy_versions`、`llm_strategy_approvals`、`llm_market_trade_grants`、`llm_strategy_state_transitions` | 5 |
| `20261010120300_llm_market_snapshots` | `llm_market_snapshots` | 1 |
| `20261010120400_llm_decision_runs` | `llm_decision_runs` | 1 |
| `20261010120500_llm_risk_and_intents` | `llm_risk_evaluations`、`llm_order_intents`、`llm_protective_exits` | 3 |
| `20261010120600_llm_budget_and_counters` | `llm_budget_reservations`、`llm_new_risk_counters`（含 2 个索引） | 2 |
| `20261010120700_llm_accounting_and_halts` | `llm_day_start_inventory`、`llm_external_flows`、`llm_reconciliations`、`llm_halts`、`llm_audit_events` | 5 |

表结构来源：`docs/llm-dev/09-数据存储与配置.md` §2.1-§2.5（18 张）+ `docs/llm-dev/08-对话与接口层.md` §2.2（6 张）。

## 2. 设计要点

### 2.1 分组顺序由外键依赖决定，不是随意的

Postgres 要求被引用的表**已经存在**，所以顺序必须是依赖拓扑序：

```text
profiles → revisions → active_pair / probe_results / switch_commands
conversations → chat_messages
strategies → strategy_versions → approvals → trade_grants；transitions → strategy_versions
market_snapshots
  └→ decision_runs（引用 strategy_versions + market_snapshots + …）
       └→ risk_evaluations（引用 decision_runs）
            └→ order_intents（引用 decision_runs + risk_evaluations + strategy_approvals）
                 └→ protective_exits（引用 order_intents）
budget_reservations（引用 revisions + decision_runs）；new_risk_counters（引用 order_intents）
day_start_inventory / external_flows / reconciliations / halts / audit_events（无依赖）
```

SQLite 容忍前向引用，Postgres 不容忍。按这个顺序分 8 组，**两种方言可以共用同一顺序**，迁移脚本因此不需要分方言维护。

### 2.2 方言翻译规则（这是本次唯一需要判断的地方）

文档 §2 的逻辑模式是**方言中立**写的（`TEXT`/`INTEGER`/`BLOB`）。落地时按各自习惯翻译，**列名、可空性、默认值、UNIQUE、外键一律不动**：

| 逻辑写法 | Postgres | SQLite | 理由 |
| --- | --- | --- | --- |
| `TEXT`（id、枚举、JSON） | `TEXT` | `TEXT` | **id 保持 TEXT，不改成 `uuid`**：本方案的 id 是不透明字符串（`secret://llm/…` 引用、错误码、时间戳串），改成 uuid 会丢掉表达能力 |
| `TEXT`（时间戳） | `TIMESTAMPTZ` | `timestamp` | Postgres 的 `TIMESTAMPTZ` 是正确类型；仓库既有迁移也用它 |
| `INTEGER NOT NULL DEFAULT 0`（布尔） | `BOOLEAN NOT NULL DEFAULT false` | `INTEGER NOT NULL DEFAULT 0` | Postgres 用原生布尔；SQLite 没有布尔类型 |
| `BLOB` | `BYTEA` | `BLOB` | 加密正文/随机数/被包裹的数据密钥 |
| `TEXT`（金额、数量） | `TEXT` | `TEXT` | **绝不改成 `NUMERIC` 或 `REAL`**：十进制字符串是"钱不过浮点"的落地 |

仓库既有的 `20210503143338_datahistoryjob` 一对文件就是这种"同列名不同方言类型"的形态，本次沿用。

### 2.3 金额列一律 TEXT

以下列装的是十进制字符串，**不是**数字类型：`amount_usdt`、`quantity`、`price`、`notional_usd`、`computed_qty`、`computed_notional`、`reference_price`、`daily_budget_usd`、`equity_usdt`、`balance_usdt`、`unrealised_pnl`。

理由见 `docs/CODING_GUIDELINES.md` 与 `llm/snapshot` 的 `ValidateDecimal`：一个数字经过浮点转换后末位就变了，而由它算出的订单是另一笔订单。

### 2.4 SQLite 的 `UNIQUE` 不使用 `ON CONFLICT REPLACE`

仓库既有的 sqlite 迁移里有 `UNIQUE(id) ON CONFLICT REPLACE` 的写法。**本次刻意不沿用**：

- `ON CONFLICT REPLACE` 在冲突时**删除旧行再插入新行**。对 `llm_order_intents.client_order_id`（幂等键）意味着一次重复提交会静默换掉原记录；对事实表意味着历史被改写。
- `ON CONFLICT IGNORE` 会**静默吞掉**违规插入，让调用方以为写成功了。

两者都与 `09` §1.2「事实表不可变」和幂等键的意图直接冲突，所以用**裸 `UNIQUE`**（约束生效、报错、不静默处理）。

### 2.5 单行表

`llm_active_model_pair` 用 `id INTEGER PRIMARY KEY CHECK (id = 1)` 保证全表只有一行。这是"唯一启用配对"在**数据库层**的表达，而不是靠代码自觉。已实测：插入 `id=2` 被拒绝。

### 2.6 触发器不在本次

`08` §2.2 与 `09` §1.2 都要求事实表禁 `UPDATE`/`DELETE`，但那是 **`M0-05`** 的独立子任务。本次迁移里每张事实表的位置只留一句注释指向它，**没有创建任何触发器**（已用 `grep -ci 'CREATE TRIGGER'` 逐文件确认为 0）。

拆开是对的：触发器涉及 `RAISE(ABORT, …)` 的 sqlite 语法与 `plpgsql` 函数 + `CREATE TRIGGER` 的 pg 语法，与建表是两类工作，混在一起会让"表结构对不对"和"触发器对不对"互相掩盖。

## 3. 验证（4 项独立校验）

### 3.1 逐表逐列比对文档

写脚本解析 `docs/llm-dev/09` + `08` 里所有 ```sql 代码块的 `CREATE TABLE` 列清单，与两份迁移文件逐一比对：

```text
spec tables: 24  postgres: 24  sqlite: 24
COLUMN FIDELITY: OK — every table and column matches the docs
```

比对**列名集合与顺序**，因此少一列、多一列、顺序错乱都会失败。结果：0 处不一致。

### 3.2 SQLite Up/Down 往返（Python `sqlite3`，内存库）

```text
UP  OK 20261010120000_llm_provider_profiles
UP  OK 20261010120100_llm_conversations
UP  OK 20261010120200_llm_strategies
UP  OK 20261010120300_llm_market_snapshots
UP  OK 20261010120400_llm_decision_runs
UP  OK 20261010120500_llm_risk_and_intents
UP  OK 20261010120600_llm_budget_and_counters
UP  OK 20261010120700_llm_accounting_and_halts
after up  : 24 tables,  2 indexes
after down: 0 llm objects left []
```

约束实测：

```text
CHECK OK: llm_active_model_pair refuses a second row
IntegrityError: UNIQUE constraint failed: llm_provider_profiles.kind, llm_provider_profiles.display_name
```

（第二条是**期望**行为：`UNIQUE (kind, display_name)` 生效，第二次同名同类型插入被拒。）

### 3.3 Postgres 结构自检

```text
postgres: all statements balanced, goose markers present in all 8 files
```

逐语句检查括号配平（剔除注释行）+ 每个文件都有 `-- +goose Up` / `-- +goose Down`。

**没有**在本机跑起 Postgres 做真实执行——`M0-14` 是 Postgres provisioning 的独立子任务。本次只做了静态自检。

### 3.4 真实库经 `dbmigrate`

本地 SQLite 库（`~/.gocryptotrader/database/gocryptotrader.db`）经 `dbmigrate -command up` 后：

```text
sqlite> select count(*) from sqlite_master where type='table' and name like 'llm_%';
24
```

`go build ./...` 通过。

## 4. 一个需要如实记录的排查过程

我用 `sqlite3 <file>` 在**临时空库**上跑这 8 个文件时，`.tables` 报**0 张表**，而 `sqlite3` 返回码是 0、库文件也建出来了（12288 字节）。为排除是不是我的 SQL 有问题，我做了：

1. 用一个显然正确的 `CREATE TABLE t1 (a TEXT);` 走同样命令 → 正常创建 ✓（说明命令形式本身可用）
2. 怀疑 `trigger` 是 SQLite 保留字 → 单独测 `CREATE TABLE t2 (trigger TEXT);` → 正常创建 ✓（SQLite 3.54 允许）
3. 故意塞一条语法错句 → 返回码 **1** 且打印 `Parse error` ✓（说明报错机制正常，之前的 0 不是"被吞掉"）

三步都排除了我的 SQL 有问题。改用 Python `sqlite3` 模块执行同样的文件 → **24 张表 + 2 索引全部创建成功**；`dbmigrate`（goose）在对真实库执行时也**逐条打印 `OK`** 并留下 24 张表。

**结论**：`sqlite3` CLI 那次的静默空跑是**调用方式**的问题，与 SQL 无关。两个独立引擎（Python `sqlite3`、goose）都确认迁移正确。**但我没能解释 CLI 那次为什么静默**——如实记在这里，如果后续有人复现，请从"重定向与 CWD 解析"方向查（该命令块执行前后工具报告过一次 shell CWD 复位）。

**给后续模型的教训**：当一次校验给出"看起来不对"的结果时，先用一个必然成功的对照样本和一次必然失败的对照样本把工具本身标定掉，再怀疑被测对象。这次三步标定省掉了"去改本来正确的 SQL"的风险。

## 5. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **不可变触发器**（事实表禁 UPDATE/DELETE、冻结版本列级保护） | **M0-05** | 本次**没有**任何触发器。`grep -c 'CREATE TRIGGER'` = 0。`llm_chat_messages`/`llm_decision_runs`/`llm_audit_events`/`llm_strategy_approvals` 等表当前**可以被 UPDATE/DELETE** |
| **Postgres 真实执行** | **M0-14** | 只做了括号配平与 goose 标记的静态检查。没有起 Postgres 实例跑一遍 |
| sqlboiler 模型 | **M0-06** | 表有了，Go 模型还没有 |
| 仓储层（Repository） | **M0-07** | 同上 |
| `config.LLMConfig` 的 `v18` 配置迁移 | **M0-09** | 无关 DB 表 |
| 与 `llm/snapshot`、`llm/jev`、`llm/openai` 结构的字段对齐 | **M0-06/M1-09** | 已做**人眼**对齐（如 `provider_probe_results` 的列 ≈ `openai.ProbeResult` 字段，`llm_decision_runs.jev_manifest_state` ≈ `jev.ManifestState`），但**没有机器化断言**。建议 `M0-06` 之后加一个"表列 ↔ Go 字段"的对照测试 |

## 6. 给接手模型的具体建议

**`M0-05`（不可变触发器）是当前最高杠杆点**：纯 DDL、可完全离线验证（用本次同样的 Python `sqlite3` 往返手法），且它是 `09` §1.2 那条"事实表不可变"不变量的**实际执行者**——现在这条不变量只有文档，没有数据库强制。

触发器要覆盖的表（按 `09` §1.2 与 `08` §2.2）：

- 禁 `UPDATE`/`DELETE`：`llm_chat_messages`、`llm_decision_runs`、`llm_audit_events`、`llm_strategy_approvals`、`llm_market_snapshots`、`llm_risk_evaluations`、`llm_provider_probe_results`
- 冻结版本列级保护：`llm_strategy_versions` 的 `frozen_json` / `strategy_hash` / `frozen_at` ——**冻结后这三列不可改**（允许改 `state`，因为策略会流转）
- 注意 `llm_strategy_approvals` 的 `revoked_at`/`revoked_by`/`expired_at` 是**允许**后续写入的（撤销是合法状态变更）——所以这张表的触发器要比其他事实表窄，别一刀切成"完全禁改"

写完必须用 Python `sqlite3` 实测：`UPDATE` 事实表应抛错、`UPDATE llm_strategy_versions.state` 应成功、`UPDATE frozen_json` 应失败。
