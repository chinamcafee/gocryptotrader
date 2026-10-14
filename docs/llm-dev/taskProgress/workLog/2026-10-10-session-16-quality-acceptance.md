# session-16：质量与验收全绿（W8 6/6）

**日期**：2026-10-10
**范围**：`W8-01`~`W8-06`（质量与验收）
**前置**：session-15（W7-04 合约覆盖页 + M2-12 范围筛选）完成

---

## 0. 先修一处我自己造成的前后不一致

`PROGRESS.md` 的 W8 行写着 `1/6 = 0.83%`，而 `TASKS.md` 里 W8-01~W8-06 **六项全是 `[ ]`**。两份台账互相矛盾。

我没去猜哪份对，而是**把六项逐条实测了一遍**，按实测结果落账。结论是六项确实都成立，于是补齐 `TASKS.md` 的勾选与证据，`PROGRESS.md` 的 W8 从 0.83% 修正为 5.00%。

**教训**：`PROGRESS.md` 的完成度是**算出来的**，它的输入是 `TASKS.md` 的勾选数。如果两份不同步，`PROGRESS.md` 就会说出一个没有依据的百分比。两份必须在同一次改动里一起更新——本会话后续与 `session-13` 的 `M0-04` 都是这么做的，这一处是更早落下的债。

## 1. 交付物

| 子任务 | 改动/产出 |
| --- | --- |
| `W8-01` | 无代码改动（验证项） |
| `W8-02` | 无代码改动（验证项） |
| `W8-03` | 无代码改动（验证项） |
| `W8-04` | `AppShell.tsx`（Sider 响应式收起 + 顶栏瘦身）· `MarketsPage.tsx` / `HealthPage.tsx` / `ProvidersPage.tsx` / `DecisionsPage.tsx`（表格加 `scroll.x`）· `styles/global.css`（窄屏隐藏品牌后缀与版本号） |
| `W8-05` | `webUI/README.md`（补 `/markets` 行、7 个路由页、`coverage.ts` 目录项、第 5 条实测坑位） |
| `W8-06` | 无代码改动（审计项） |

## 2. W8-04：实测发现 5 个路由横向溢出，并修掉

### 2.1 先解决"怎么测"

浏览器工具集**没有视口缩放能力**（16 个工具里没有 resize/setViewport）。而响应式缺陷恰恰只在窄视口下出现——不测就等于没做。

解法：**在同源页面里注入一个 390px 宽的 iframe**，让 iframe 内的页面自己加载。**媒体查询按 iframe 的宽度求值**，所以 iframe 内的布局就是真实的 390px 布局；同源又可以读 `contentDocument` 做测量。

这比"看代码里有没有写响应式类"强得多——后者只是读声明，前者是量结果。

### 2.2 修复前的实测（7 个路由）

| 路由 | innerWidth | scrollWidth | 溢出 |
| --- | --- | --- | --- |
| `/markets` | 386 | 577 | **+191** |
| `/decisions` | 386 | 594 | **+208** |
| `/health` | 386 | 561 | **+175** |
| `/providers` | 386 | 440 | **+54** |
| `/terminal` | 386 | 431 | **+45** |
| `/strategies` | 386 | 378 | −8 |
| `/settings` | 386 | 378 | −8 |

**5 个路由溢出**。最宽的元素只有 378px（能放下），但 `scrollWidth` 到了 431–594——说明是**布局容器**把它们推出去的，不是某个元素本身太宽。

根因：`Sider width={176}` 固定且在位。390px 视口下侧栏吃掉 176，只剩 ~210px 给内容；内容的 min-content 宽度（表格固定列宽合计、卡片内边距等）超过 210，整页就开始横向滚动。`/markets` 的表格列宽合计 **556px**，是最突出的一个。

### 2.3 修复

| 改动 | 为什么 |
| --- | --- |
| `Sider` 加 `breakpoint="lg"` + `collapsedWidth={0}` | <992px 时侧栏收成 0 宽（antd 留一个悬浮触发器）。这一项就找回 176px |
| 宽表加 `scroll={{ x: n }}` | 让表格**在卡片内部**横向滚动，而不是把整页撑开。列宽本来就超窄屏可用宽度，硬压会把列挤到不可读 |
| 顶栏品牌后缀（`· LLM/Jev 决策平台`）与版本号 <992px 隐藏 | 顶栏是 `display:flex` 单行，长文本会顶出最小宽度 |
| `Content` 加 `overflowX: 'hidden'` | 兜底：任何漏网的子元素都不该把整页推宽 |

### 2.4 修复后的实测

```text
7 个路由：stillOverflowing = 0（全部 over ≤ 0）
```

**390px**：

```text
sider 宽  = 1px（已收起）
4 张卡片 left 全为 15  → distinctLefts = 1  → 单列 ✅
overflow  = −8
```

**1440px**：

```text
sider 宽  = 176px（在位）
卡片 left ∈ {190, 911} → distinctLefts = 2 → 多栏 ✅
            709px + 503px 并排
overflow  = −8
```

两条要求（390px 单列 / ≥1440px 多栏）都是**量出来的**，不是声明出来的。

## 3. W8-06：四项密钥自查

| 项 | 做法 | 结果 |
| --- | --- | --- |
| ① 源码 | `webUI/src/` 全文搜 `sk-`/`apiKey`/`api_key`/`secret`/`Bearer`/`Basic`/`authorization` | 只有**注释与字段名文案**（`apiKeyRef`/`apiKeyFingerprint` 是契约字段名，`代理已注入 Basic Auth` 是注释），**无任何密钥值** |
| ② 构建产物 | 取 `config.json` 里 **2 个真实凭据值**，在 `dist/` 的 4 个文件里逐字节搜 | **0 处泄露** |
| ③ 网络 | 浏览器 **1186 条请求**全量分析 | **全部**指向 `http://127.0.0.1:5173`；**0 条直连网关 9053**、**0 条非本地主机**、**0 条 URL 内嵌凭证** |
| ④ 代理 | 读 `proxy/gct-proxy.mjs` | `Authorization` 只加在**上游** `https.request` 的 headers（:109）；回给浏览器的自检体只有布尔 `credentialsLoaded`，不含值 |

②③ 是有分量的两条：②证明"就算前端被打包发布了也不带密钥"，③证明"浏览器实际发出的流量里没有凭证，也没有绕过代理直连网关的旁路"。只做 ①（看源码）会漏掉构建期注入与运行时旁路。

**一处自己抓到的自查质量问题**：第一次跑 ① 用了 `grep -rniE ... --include=*.ts`，zsh 报 `no matches found`，**grep 根本没执行**，而 `|| echo "（无匹配）"` 却打印了"无匹配"——一个假阴性。改用 Grep 工具重跑才拿到真实结果。**"看起来通过了"和"真的跑过了"是两件事**，尤其在用了 shell 通配符的时候。

## 4. W8-01/02/03 的证据

**W8-01**（三连）：

```text
pnpm typecheck   → tsc --noEmit，无输出（通过）
pnpm lint        → Found 0 warnings and 0 errors（26 files, 96 rules, 25ms）
pnpm build       → 3150 modules transformed, built in 1.02s
```

注意 `pnpm build` 的定义就是 `tsc --noEmit && vite build`，所以 typecheck 在构建里已经跑过；单独保留它只是为了更快失败。

**W8-02**（交易台真实数据）：浏览器实测 `/terminal` BTC——

```text
240 根已收盘 K 线
14 个 canvas：640×274 / 640×274 / 68×274 / 68×274（主图+成交量+MACD，各含价格轴与时间轴）
账户面板：可用余额 68.76 · 冻结 hold 1.25 · 总额 total 70.01
```

**W8-03**（降级）：4 个待实现页在 1200px 视口下均渲染明确文案并**指名负责的子任务 ID**：

```text
/health     → 待实现 / 尚未实现 / M5-17 与 LLMSetRiskSwitch RPC
/providers  → 待实现 / 尚未实现
/decisions  → 待实现 / 尚未实现 / 尚未实现，暂无决策记录
/strategies → 待实现 / 尚未实现 / M4-01 条款提取、M4-02 冻结、M4-05 审批、M4-15 gRPC 命令族、W5-01 对话 UI
```

无白屏、无崩溃、无 501 原始错误裸露。**"还没做"要说得像"还没做"，并且指向负责它的任务**——否则使用者会以为是自己配错了。

## 5. W8-05：README 复核

`docs/llm-dev/08` §1 要求 README 含四项：启动 / 构建 / 与 bot 的关系 / 代理说明。复核现有 README：

| 要求 | README 中的位置 |
| --- | --- |
| 启动 | 「快速开始」（`pnpm dev:all` / 分开起） |
| 构建 | 「脚本」表含 `pnpm build` / `typecheck` / `lint` / `preview` |
| 与 bot 的关系 | 「三个进程的关系」节 + 端口 9053/8788/5173 |
| 代理说明 | 「代理配置」节 + 自检 `curl http://127.0.0.1:8788/api/health` |

四项齐备。本会话补：`/markets` 行、路由页数 6→7、「页面」表里 `/health` 的覆盖运行面指向 `W7-07`、目录结构加 `coverage.ts`、并新增第 5 条实测坑位：

> **写新东西前先找 Go 侧有没有同名规则**：`coverage.ts` 是 `llm/coverage` 的镜像，属于有记录的重复。打通 `LLMGetCoverageRPC` 后应删掉本地算术。加新规则前先问：这条规则 Go 侧有吗？有就别再写一份。

## 6. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| 前端**单元测试** | 无计划 | `webUI` 没有测试运行器。`coverage.ts` 的算术只有类型检查与构建把关。**响应式与降级是手工实测的，不是自动化的**——改动后需重测 |
| 真机窄屏（真手机） | 无 | 390px 是在**同源 iframe** 里测的，不是真机。"iframe 里宽度对了"与"iOS Safari 上对"仍有差距（`100vh`、地址栏、字体缩放等） |
| ≥1440px 的"多栏"要求解释 | — | 我按"卡片分列"解读（1440px 下 `/markets` 为 2 栏）。文档 `08` §8 未给更细的定义，若原意是三栏或以上需要重看 |
| `M0-06` 前置 | **M0-14** | 见 §7 |
| proto 工具链 | §0.1 | 仍挡着 `M1-14` / `W7-07` / W5-W6 的接口层 |

## 7. 修正我上一轮给出的"下一步"建议

`session-14/15` 的工作日志里我把 `M0-06`（sqlboiler）称作"当前最高杠杆点、可离线验证"。**这个判断是错的**，本会话核实后：

1. **仓库用的是分叉 sqlboiler**（`github.com/thrasher-corp/sqlboiler`），而 `make gen_db_models` 调的是**裸 `sqlboiler` 二进制**——`which sqlboiler` → **未安装**。
2. **Postgres 那一半需要活的 Postgres 来 introspect**（`make gen_db_models DRIVER=psql`）。本机没有；那是 `M0-14`。**所以 `M0-06` 的 pg 半依赖 `M0-14`**，不是可离线项。
3. 目标带 **`--wipe`**：会重写 `database/models/{postgres,sqlite3}/` 下的既有模型（sqlite3 目录现有 **29 个文件**）。diff 会很大，**动手前要先确认这是预期行为**。

正确的顺序是：**`M0-14`（Postgres provisioning）→ `M0-06` → `M0-07` → `M1-09`~`M1-13`**。

## 8. 校验证据

```text
# 前端
$ pnpm typecheck     # 无输出
$ pnpm lint          # Found 0 warnings and 0 errors（26 files, 96 rules）
$ pnpm build         # ✓ 3150 modules transformed, built in 1.02s

# 浏览器实测（真实 bot + 真实代理）
7 个路由 @390px  ：overflow 全 ≤ 0，sider 1px，卡片单列
/markets @1440px ：sider 176px，卡片 2 栏（709+503），overflow ≤ 0
/terminal        ：240 根已收盘 K 线 · 14 canvas · 真实账户数字
4 个待实现页      ：降级文案 + 子任务 ID，无白屏
密钥自查          ：源码 0 · dist 0 · 1186 请求 0 外泄 · 代理仅上游注入

# 后端（未受本会话改动影响，回归确认）
$ go test ./llm/... -count 1   # 8 个包全绿，295 个测试函数
```

## 9. 给接手模型的具体建议

**前端已没有"顺手做完"的独立项了**：W8 全绿，W0-W3 全绿，W4/W5/W6 都等后端接口，W7 只剩 `W7-07`（覆盖运行面 RPC）。所以下一步必然在后端，且都需要外部资源：

1. **`M0-14` PostgreSQL provisioning**——**先做这个**。仓库有 `docker-compose.yml`，`docs/localDeploy` 有配方。它是 `M0-06`（pg 半）的唯一前提，进而解锁 `M0-07` 与 `M1-09`~`M1-13` 一整条链。
2. **`M2-01` K 线取数共享逻辑**——`M2-14` 的前置，同时是**实测被 `currency not enabled` 挡住的 Q4=B 按需只读**的唯一出路（详见 `session-15` 工作日志 §4）。做时记得手工调 `Request.ProcessResponse` 保证去重/排序/填充一致。
3. **校准 proto 工具链**（`TASKS.md` §0.1）——它现在**同时**挡着 `M1-14`（providers 命令族）、`W7-07`（覆盖运行面 RPC）、以及 W5/W6 的接口层。**校准一次能解锁一整片**，是目前阻塞面最广的单项。

**改前端后请重跑响应式实测**：`session-16` 的 §2.1 有可复用的注入手法。它是手工的，不会因为 `pnpm build` 通过就保持成立。
