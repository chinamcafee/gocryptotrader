# session-14：不可变触发器（M0-05）

**日期**：2026-10-10
**范围**：`M0-05` 不可变触发器（事实表禁 UPDATE/DELETE；冻结版本列级保护）
**前置**：session-13（M0-04 24 张表迁移）完成

---

## 1. 交付物

`database/migrations/20261010120800_llm_immutability_triggers/`

| 文件 | 内容 |
| --- | --- |
| `sqlite3.sql` | **21 个触发器**（无存储过程设施，列级守卫逐表写出） |
| `postgres.sql` | 2 个 plpgsql 函数（`llm_reject_mutation` / `llm_reject_immutable_columns`）+ 20 个触发器；列清单经 `TG_ARGV` 传入 |

## 2. 规则

| 表 | 规则 |
| --- | --- |
| `llm_decision_runs`、`llm_chat_messages`、`llm_audit_events`、`llm_risk_evaluations`、`llm_day_start_inventory`、`llm_provider_probe_results`、`llm_market_snapshots` | **完全不可变**：禁 UPDATE + 禁 DELETE |
| `llm_strategy_approvals` | 禁 DELETE；UPDATE 只允许改 `revoked_at`/`revoked_by`/`revoke_reason`/`expired_at` |
| `llm_order_intents` | 禁 DELETE；UPDATE 只允许改 `status`/`internal_order_id`/`exchange_order_id`/`updated_at` |
| `llm_budget_reservations` | 禁 DELETE；UPDATE 只允许改 `status`/`completed_at`/`input_tokens`/`output_tokens`/`latency_ms` |
| `llm_strategy_versions` | **冻结列**保护：`OLD.frozen_json IS NOT NULL` 后，`frozen_json`/`strategy_hash`/`risk_envelope_hash`/`source_prompt_cipher` 不可改；`state` 仍可流转；DELETE 不拦 |

## 3. 修正了文档的两处缺陷

### 3.1 「不可变的表」清单与它自己的表结构矛盾

文档 `09` §1.2 列出 9 张不可变表，其中三张**不可能**完全不可变：

| 表 | 结构里有 | 若完全不可变的后果 |
| --- | --- | --- |
| `llm_strategy_approvals` | `revoked_at`/`revoked_by`/`revoke_reason`/`expired_at` | 审批**永远无法撤销**；而 `09` §2.1 的 `llm_pair_switch_commands.revoked_approvals` 要统计撤销数，M5 的配对切换必须撤销有效审批 |
| `llm_order_intents` | `status`（PREPARED…ACKED/FILLED/CANCELED）、`internal_order_id`、`exchange_order_id`、`updated_at` | 意图**永远停在 PREPARED**；`internal_order_id` 的存在意义就是"提交后回填" |
| `llm_budget_reservations` | `status`（RESERVED/USED/RELEASED/EXPIRED）、`completed_at`、`internal_tokens` 等 | 预约**永远不会被标记为已消费**；`dailyBudgetUsd` 的三态语义落不了地 |

**代码按"列级保护 + 禁 DELETE"实现这三张**，可变的列恰好就是结构里为"后续状态"提供的那些。这是**刻意的偏离**，理由写在迁移文件的注释里。

### 3.2 文档的列级守卫 NULL 不安全

文档 §1.2 给的守卫：

```sql
WHEN OLD.frozen_json IS NOT NULL
     AND (NEW.frozen_json <> OLD.frozen_json
          OR NEW.strategy_hash <> OLD.strategy_hash
          ...)
```

**`<>` 在任一侧为 NULL 时结果是 NULL**，WHEN 子句因此不成立，**写入被放行**。后果：`UPDATE llm_strategy_versions SET strategy_hash = NULL` 会**静默成功**——把一个已经审批的策略版本的哈希擦掉，而审批记录指向一个不再存在的冻结体。

**代码改用 NULL 安全的比较**：SQLite 的 `IS NOT`、Postgres 的 `IS DISTINCT FROM`。两种方言里 `NULL IS NOT 'x'` 都为真，守卫因此无法被 NULL 绕过。

**实测覆盖**（见 §5）：3 条专门的 NULL 逃逸用例，全部被拒。

### 3.3 顺带补齐：`llm_market_snapshots` 未在不可变清单里

文档 `04` §3 明确"快照构造后不可变"，且该表**没有任何可变列**。文档 `09` §1.2 的清单漏了它——留着就是一张可写的事实表。代码把它纳入完全不可变，并在注释里说明这是补漏而非扩大范围。

## 4. 与仓库 goose 的集成缺陷

文档给出的触发器 SQL **照抄进迁移文件会在本仓库跑不起来**：

```text
ERROR sqlite3.sql: failed to run SQL migration: failed to execute SQL query
"CREATE TRIGGER llm_decision_runs_immutable\nBEFORE UPDATE ON ... \nBEGIN\n
SELECT RAISE(ABORT, 'llm_decision_runs is immutable');\n": incomplete input
```

原因：仓库用的是 `github.com/thrasher-corp/goose v2.7.0-rc4`，它**按 `;` 切分语句**，而触发器体 `BEGIN … END;` 内部含分号，被切开后前半段不完整。

解法（该 fork 的 README §193 有明确记载）：用 `-- +goose StatementBegin` / `-- +goose StatementEnd` 包裹整个多语句体。**仓库里此前没有任何触发器迁移，所以没有先例可循**——这是第一次踩到。

已处理：SQLite 侧 21 处（每个触发器一对标记）、Postgres 侧 2 处（两个 plpgsql 函数体；`CREATE TRIGGER ... EXECUTE FUNCTION f(...);` 内部无分号，不需要标记）。

**给后续模型的固定做法**：往迁移里写**任何**含内部 `;` 的语句（触发器、函数、`CASE` 里的 `;` 等）都必须加这对标记。建议 `M0-06` 之后考虑加一个"迁移文件里 `BEGIN…END;` 必须被标记包裹"的检查。

## 5. 验证：18 条行为断言

用 Python `sqlite3` 在内存库上跑完 9 个迁移（含触发器），再逐条断言。**同时测"必须拒绝"与"必须放行"**——只测拒绝会让一个过严的守卫（例如把所有 UPDATE 都拦掉）也通过。

### 5.1 必须被拒（10 条）

```text
ok  UPDATE decision_runs: blocked (llm_decision_runs is immutable)
ok  DELETE decision_runs: blocked (llm_decision_runs is immutable)
ok  UPDATE approvals core (risk_envelope_hash): blocked
ok  UPDATE approvals core -> NULL: blocked            ← NULL 逃逸
ok  DELETE approvals: blocked
ok  UPDATE intents core (quantity): blocked
ok  UPDATE intents core -> NULL: blocked              ← NULL 逃逸
ok  DELETE intents: blocked
ok  UPDATE reservations core (expires_at): blocked
ok  DELETE reservations: blocked
```

### 5.2 必须放行（3 条）

```text
ok  UPDATE approvals revocation (revoked_at/revoked_by/revoke_reason): allowed
ok  UPDATE intents lifecycle (status/internal_order_id/exchange_order_id/updated_at): allowed
ok  UPDATE reservations settlement (status/completed_at/tokens/latency): allowed
```

### 5.3 冻结门控（5 条）

```text
ok  freeze (frozen_json NULL -> value): allowed                  ← 冻结动作本身必须能写
ok  state machine moves after freeze: allowed                    ← state 流转不受影响
ok  frozen_json edited after freeze: blocked
ok  strategy_hash NULLed after freeze: blocked                    ← NULL 逃逸
ok  risk_envelope_hash set after freeze: blocked
```

### 5.4 Down 与真实库

```text
llm objects left after down: 0 []
```

真实库（`~/.gocryptotrader/database/gocryptotrader.db`）经 `dbmigrate -command up`：

```text
OK   20261010120800_llm_immutability_triggers/sqlite3.sql
goose: no further migrations to run. current version: 20261010120800
tables|24      triggers|21
```

插入一行后 `UPDATE` 与 `DELETE` 均报 `llm_decision_runs is immutable`。

**收尾**：这条测试行被触发器正确保护住（删不掉），我临时 `DROP TRIGGER llm_decision_runs_no_delete` → 删除该行 → 重建触发器，恢复为 0 行 / 21 触发器。**没有在用户的库里留下测试数据。**

### 5.5 失败迁移没有留下脏状态

第一次 `up` 因 `incomplete input` 失败后，`-command status` 显示该迁移仍是 **Pending**——goose 没有把失败的迁移记为已应用。这一点值得记下：否则"重跑 up"会静默跳过，而触发器一个都不会建。

## 6. 一个我自己犯的验证错误（记下来避免重复）

验证时我用 `DELETE FROM llm_decision_runs;` 检查守卫是否生效，看到**没有任何报错**，一度以为守卫失效。实际原因：**表里当时是 0 行**，而 `BEFORE DELETE ... FOR EACH ROW` 触发器**没有行可触发**。

结论：**行级触发器的行为必须用真实存在的行来验证**。同样的坑在 session-13 出现过一次（用 `.tables` 看空结果）。两次都是"测试的输入条件没被满足"，而不是被测对象有问题。

## 7. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| **Postgres 侧真实执行** | **M0-14** | pg 文件只做了静态检查（括号配平、goose 标记）。`TG_ARGV` 传参、`to_jsonb`/`IS DISTINCT FROM`、`ERRCODE = 'restrict_violation'` 全部**未在真实 Postgres 上跑过**。**这是本会话最大的未验证项** |
| sqlboiler 模型 | **M0-06** | 表 + 触发器有了，Go 模型还没有 |
| 仓储层 | **M0-07** | 同上 |
| "迁移里含内部 `;` 的语句必须被标记包裹" 的机器化检查 | 无 | 本次靠人工发现。建议做成检查项 |
| 其余表的可变性判定 | M0-06/后续 | 文档 §1.2 的"可变表"清单（conversations/strategies/strategy_versions/active_model_pair/provider_profiles/market_trade_grants/halts）**未加任何保护**。其中 `llm_active_model_pair.pair_epoch` 应当是**单调递增**的（CAS 语义），目前没有触发器或约束保证 |

## 8. 给接手模型的具体建议

**`M0-06`（sqlboiler 模型生成）是当前最高杠杆点**：纯代码生成，可离线验证，生成后 `M0-07` 仓储层才有基础，进而解锁 `M1-09`~`M1-13`。

跑 `M0-06`/`M0-07` 前请先确认：

1. `dbmigrate` **必须从 `cmd/dbmigrate/` 目录运行**（否则报 `../../database/migrations directory does not exists`）。
2. 本地库已是最新：`current version: 20261010120800`。
3. sqlboiler 要在**两种方言**上各生成一份（`09` §3.2 的要求）。
4. 生成后建议加一个"表列 ↔ Go 结构体字段"的对照测试——`M0-04` 的验证证明了这种做法能抓住真实的漂移。

**如果想继续留在可离线验证的区间**，另一个选项是给"可变表"补约束：`llm_active_model_pair.pair_epoch` 单调递增、`llm_halts` 的 `cleared_at` 与 `cleared_by` 必须同时为空或同时有值。这些都是纯 DDL。
