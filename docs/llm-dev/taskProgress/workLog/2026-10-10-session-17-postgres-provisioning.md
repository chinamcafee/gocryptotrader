# session-17：PostgreSQL provisioning 与 pg 侧触发器实测（M0-14）

**日期**：2026-10-10
**范围**：`M0-14` PostgreSQL provisioning（docker compose + 迁移以 pg 为主验证）
**前置**：session-16（W8 质量与验收全绿）完成

---

## 1. 交付物

| 文件 | 改动 |
| --- | --- |
| `docker-compose.yml` | **新增 `postgres` 服务**（`postgres:17-alpine`、健康检查、命名卷 `gct-pgdata`、**端口只绑 `127.0.0.1:5432`**）+ 顶部注释说明用途与连接配置 |
| `docs/localDeploy/06-数据库与历史K线.md` | **新增 §11.1「实测记录」**：两条起库路径、`dbmigrate -config` 的正确调用、两个实测坑、验证结论 |

无 Go 代码改动。

## 2. 为什么这件事价值高于它的标题

`M0-14` 的任务描述只是"provisioning + 迁移以 pg 为主验证"，但它**顺带关掉了 `M0-05` 遗留的最大未验证项**。

`session-14` 交付 21 个 sqlite 触发器 + 2 个 plpgsql 函数时，明确记下：

> **Postgres 侧真实执行** — pg 文件只做了静态检查（括号配平、goose 标记）。`TG_ARGV` 传参、`to_jsonb`/`IS DISTINCT FROM`、`ERRCODE = 'restrict_violation'` 全部**未在真实 Postgres 上跑过**。**这是本会话最大的未验证项**

`session-15/16` 也把它列为未完成项。本会话用本机原生 PostgreSQL 17.11 把它关掉了。

**这件事也是为什么当初该把 pg 验证排在前面而不是一直挂着**：`TG_ARGV` 传参、`IS DISTINCT FROM` 的 NULL 语义、plpgsql 触发器的 `WHEN` 子句——这些都不是"读一遍 SQL 就能确认"的东西。它们要么对，要么在第一次真跑时报错。

## 3. 实测内容

### 3.1 起库（不碰任何既有实例）

本机 Homebrew 装了 `postgresql@17` 17.11，所以用**临时集群**而不是动用户的服务：

```bash
initdb -D /tmp/gct-pg-m014/data -U gct --auth=trust
pg_ctl -D /tmp/gct-pg-m014/data -o "-p 55432 -k /tmp/gct-pg-m014 -c listen_addresses=127.0.0.1" -l .../pg.log start
createdb -h 127.0.0.1 -p 55432 -U gct gocryptotrader
```

端口用 **55432**、只监听 `127.0.0.1`、socket 目录在临时目录里——**不占用 5432、不影响用户可能已有的 Postgres**。实测完 `pg_ctl stop` + `rm -rf`，端口确认已释放。

`initdb` 在 macOS 上需要 `LC_ALL`（未设时报 collation 错）。

### 3.2 跑迁移

```text
$ cd cmd/dbmigrate && ../../dbmigrate -config /tmp/gct-pg-m014/config.json -command up
OK    20261010120000_llm_provider_profiles/postgres.sql
OK    20261010120100_llm_conversations/postgres.sql
OK    20261010120200_llm_strategies/postgres.sql
OK    20261010120300_llm_market_snapshots/postgres.sql
OK    20261010120400_llm_decision_runs/postgres.sql
OK    20261010120500_llm_risk_and_intents/postgres.sql
OK    20261010120600_llm_budget_and_counters/postgres.sql
OK    20261010120700_llm_accounting_and_halts/postgres.sql
OK    20261010120800_llm_immutability_triggers/postgres.sql
goose: no further migrations to run. current version: 20261010120800
```

（另含仓库既有的 Postgres 迁移，同样全 OK。）

对象计数：

```text
tables = 24    triggers = 20    functions = 2
```

方言翻译核对（`information_schema.columns`）：

```text
built_at        -> timestamp with time zone    ✅ 逻辑 TEXT → TIMESTAMPTZ
quality_json    -> text                        ✅ 保持 TEXT
passed          -> boolean                     ✅ 逻辑 INTEGER → BOOLEAN
content_cipher  -> bytea                       ✅ 逻辑 BLOB → BYTEA
```

### 3.3 down 双向验证

```text
$ dbmigrate -config ... -command down
OK  20261010120800_llm_immutability_triggers/postgres.sql
tables = 24    triggers = 0    functions = 0
```

触发器与函数被清干净，表保留（正确的粒度：down 只回滚它自己那一个迁移）。

## 4. 两个实测坑（都会让验证结果失真）

### 4.1 Postgres 真的校验外键，SQLite 默认不校验

第一次插夹具时：

```text
ERROR: insert or update on table "llm_strategy_approvals" violates foreign key
constraint "llm_strategy_approvals_strategy_version_id_fkey"
  Key (strategy_version_id)=(sv1) is not present in table "llm_strategy_versions".
```

同样的夹具在 SQLite 上**直接通过**——SQLite 默认 `PRAGMA foreign_keys = OFF`。

**这有一个更一般的后果**：同一个缺陷（比如触发器没生效）在两种方言下会表现为**完全不同的错误**。sqlite 上是"静默放行"，pg 上是"FK 报错"。只测一种方言，会得到两种不同的错误结论，而其中一种是错的。

要插的顺序（必须满足 FK）：`llm_provider_profiles` → `llm_provider_profile_revisions` → `llm_strategies` → `llm_strategy_versions` → `llm_decision_runs` → `llm_risk_evaluations` → `llm_strategy_approvals` → `llm_order_intents`；`llm_budget_reservations` 依赖 `revisions`。

### 4.2 `psql -f` 默认不因错误中止 —— 断言会在空表上"全部通过"

夹具插入失败后，脚本**继续往下跑**，所有断言都变成 `UPDATE 0`（没有行匹配），`grep` 一看"没有 ERROR"就以为通过了。**这是本会话第三次踩"测试输入条件没被满足"这个坑**（前两次：session-13 的 `.tables` 空结果、session-14 的 `DELETE` 无行可触发）。

这次的解法比前两次更结构化——**把断言包进 plpgsql 函数，把异常转成可读文本**：

```sql
create function pg_temp.must_block(label text, stmt text) returns text as $fn$
begin
  execute stmt;
  return 'FAIL  ' || label || ' -> allowed!';          -- 没抛异常 = 该拒的没拒
exception when others then
  return 'ok    ' || label || ' -> blocked (' || left(SQLERRM, 48) || ')';
end; $fn$ language plpgsql;
```

于是**每一条断言都输出一行结果**，`FAIL` 与 `ok` 一样显眼，不存在"因为没报错所以看着像通过"。同时夹具脚本用 `\set ON_ERROR_STOP on`，插不进去就立刻停，不会带着空表往下跑。

**这个 `must_block` / `must_allow` 配对值得复用**：只测"必须被拒"会让一个**过严**的守卫（例如把所有 UPDATE 都拦掉）通过。"必须放行"的断言是必需的另一半。

## 5. pg 侧 18 条断言（全部通过）

```text
--- must be blocked ---
ok  UPDATE decision_runs            -> blocked (llm_decision_runs is immutable (attempted UPDATE)
ok  DELETE decision_runs            -> blocked (llm_decision_runs is immutable (attempted DELETE)
ok  UPDATE approvals core           -> blocked (llm_strategy_approvals has immutable columns
ok  UPDATE approvals core -> NULL   -> blocked (llm_strategy_approvals has immutable columns
ok  DELETE approvals                -> blocked (llm_strategy_approvals is immutable
ok  UPDATE intents core (quantity)  -> blocked (llm_order_intents has immutable columns
ok  UPDATE intents core -> NULL     -> blocked (llm_order_intents has immutable columns
ok  DELETE intents                  -> blocked (llm_order_intents is immutable
ok  UPDATE reservations core        -> blocked (llm_budget_reservations has immutable columns
ok  DELETE reservations             -> blocked (llm_budget_reservations is immutable
--- must be allowed ---
ok  UPDATE approvals revocation     -> allowed
ok  UPDATE intents lifecycle        -> allowed
ok  UPDATE reservations settlement  -> allowed
--- strategy_versions freeze gate ---
ok  freeze (NULL -> value)          -> allowed
ok  state moves after freeze        -> allowed
ok  frozen_json edited after freeze -> blocked (llm_strategy_versions has immutable columns
ok  strategy_hash NULLed after freeze -> blocked
ok  risk_envelope_hash set after freeze -> blocked
```

三组各有意义：

- **3 条"置 NULL 逃逸"** 全部被拒 → 证明 `IS DISTINCT FROM` 在 pg 上确实做到了 NULL 安全（这是 `session-14` 修正文档缺陷时给的理由，现在有实测支撑）。
- **3 条"必须放行"** 通过 → 证明守卫没有过严：审批能撤销、意图能推进生命周期、预算能结算。
- **冻结门控 5 条** → 冻结动作本身能写、`state` 能流转、三种冻结列改动被拒。

## 6. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **compose 服务的真实启动** | — | 我加了 `postgres` 服务并用 `docker compose config --quiet` 校验了语法，但**没有真的 `docker compose up -d postgres`**（本机已验证原生 pg 路径，起容器是等价的另一条路）。**这条未实测** |
| pg 侧的**其余迁移**（既有仓库迁移） | 不属本任务 | 它们随 `up` 一起跑了并全 OK，但那是回归确认，不是本任务的范围 |
| `dbseed` 在 Postgres 上 | — | 只跑了 `dbmigrate`。`docs/localDeploy/06` §11 的第 4 步（`dbseed`）**未在 pg 上验证** |
| LLM 表的 sqlboiler 模型 | **M0-06** | 前置已就位（pg 已在 compose 里） |
| `M0-07` 仓储层 | — | 依赖 `M0-06` |
| 用户真实 Postgres 实例 | — | 全程用的**临时集群**，没动用户可能已有的 5432 实例。**用户没有既有 pg 实例这件事我没有核实过** |

## 7. 校验证据

```text
# Postgres 侧
initdb + pg_ctl start（本机原生 17.11，端口 55432，只监听回环）
dbmigrate -command up   → 10 个迁移全部 OK，current version 20261010120800
information_schema      → 24 tables / 20 triggers / 2 functions
方言核对                → TIMESTAMPTZ · BOOLEAN · BYTEA 均按预期
dbmigrate -command down → OK，triggers=0 functions=0 tables=24
触发器行为              → 18/18 断言通过

# compose
docker compose config --quiet  → VALID（仅一条 `version` 已废弃的告警）

# 收尾
pg_ctl stop + rm -rf /tmp/gct-pg-m014 → 目录已删，55432 已释放

# 文档
markdownlint docs/llm-dev + docs/localDeploy 43 files → 0 issues
```

## 8. 给接手模型的具体建议

**`M0-06`（sqlboiler 模型生成）的前置已就位** —— `docker compose up -d postgres` 就能起库（`docs/localDeploy/06` §11.1 有命令与配置）。动手前确认两点：

1. **`sqlboiler` 二进制未安装，且仓库用的是分叉**（`github.com/thrasher-corp/sqlboiler`）。`make gen_db_models` 调的是裸 `sqlboiler`，需要先从这个分叉构建/安装它。**这一步会花时间且可能踩 Go 版本兼容**，先留预算。
2. **目标带 `--wipe`**，会重写 `database/models/{postgres,sqlite3}/` 下的既有模型（sqlite3 目录现有 29 个文件）。**这是"重新生成全部模型"而非"只加 LLM 表"**，diff 会很大。动手前先 `git status` 干净、并确认这是预期。

**改数据库相关代码后请两种方言都跑**：本会话 §4.1 的教训——同一个缺陷在 sqlite 与 pg 下表现不同，只测一种会得出错误结论。SQLite 侧用 Python `sqlite3`（不需要起服务），Postgres 侧用临时集群（`initcluster` 到 `/tmp` + 非标准端口，测完删）。

**测触发器/约束时请检查输入非空**：第三次踩这个坑了。要么 `select count(*)` 先确认行数，要么用本会话的 `must_block`/`must_allow` 写法把结果变成一行可读文本。
