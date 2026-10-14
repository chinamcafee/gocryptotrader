# 2026-10-10 session-01 — webUI 脚手架与数据层打通

## 会话目标

按 `docs/llm-dev/` 方案在 `webUI/` 建立前端平台骨架，并打通到 GoCryptoTrader 的真实数据链路；同时建立 `taskProgress/` 接力装置。

## 完成的子任务

**接力装置（不计权重）**：`taskProgress/README.md`、`TASKS.md`、`PROGRESS.md`、`workLog/`、`decisions/`

**计入权重**：
`W0-01` `W0-02` `W0-03` `W0-04` `W0-05` `W0-06`
`W1-01` `W1-02` `W1-03` `W1-04` `W1-05` `W1-06` `W1-08`（`W1-07` 部分完成）
`W2-01` `W2-02` `W2-03` `W2-04`
`W3-04` `W3-07`（`W3-05` 部分完成）

## 具体做了什么

### 1. 依赖版本核实（避免写出装不上的 package.json）

实测 `npm view` 结果（**这些版本真实存在，可直接用**）：
antd `6.6.5` · @ant-design/x `2.9.0` · lightweight-charts `5.2.1` · react `19.3.0` ·
vite `8.3.4` · @vitejs/plugin-react `6.1.2` · @reduxjs/toolkit `2.13.0` · react-redux `9.3.0` ·
jotai `3.0.1` · react-router-dom `7.18.4` · @ant-design/icons `6.3.4` · oxlint `1.87.0`

**TypeScript 选择**：最新是 `7.0.2`，但为稳定性选 **`5.9.3`**（TS 7 过新，与 vite 8/antd 6 的组合未经验证）。
**lint 选择**：用 `oxlint`（单依赖、零配置），避开 eslint 10 + typescript-eslint 的 peer 地狱。

### 2. 脚手架（W0）

| 文件 | 作用 |
| --- | --- |
| `webUI/package.json` | 依赖与脚本（`dev`/`build`/`typecheck`/`lint`/`proxy`/`dev:all`） |
| `webUI/vite.config.ts` | `@` → `src` 别名、`/api` → 8788 代理、构建配置 |
| `webUI/tsconfig.json` | strict + `noUncheckedIndexedAccess` + `noUnusedLocals` |
| `webUI/index.html` | 入口（`color-scheme: dark`） |
| `webUI/.env.example` | 代理可覆盖的全部变量（含安全默认值说明） |
| `webUI/.gitignore` | 忽略 `node_modules`/`dist`/`.env` |

### 3. 本地代理（W0-03）—— 安全边界的关键设施

`webUI/proxy/gct-proxy.mjs`（Node，无额外依赖）：

- 从 `~/.gocryptotrader/config.json` 读 `remoteControl.username/password`，**只在进程内存**注入 Basic Auth
- 上游 `https://127.0.0.1:9053/v1/`，代理侧跳过自签证书校验
- **只读白名单**：GET 27 个端点；POST 3 个（`getticker`/`getorderbook`/`getexchangepairs`，实测确认是查询）
- **写操作默认关闭**：非白名单 POST 返回 `501 WRITES_DISABLED`，需 `.env` 显式开
- **LLM 规划端点**：17 个 `/v1/llm/*` 路径；上游 404 时转成确定的 `501 NOT_IMPLEMENTED`（附指向 TASKS.md 的提示）
- `GET /api/health` 自检端点

### 4. 数据层类型与客户端（W1）

- `src/app/api/types.ts`：领域类型，字段名**严格对齐网关 camelCase**，并在注释里标注实测坑位
- `src/app/api/gctApi.ts`：RTK Query，14 个 endpoint；统一错误处理（`NOT_IMPLEMENTED` / `PROXY_UNREACHABLE`）
- `src/app/store.ts`、`src/state/atoms.ts`（Jotai：selectedMarket / interval / bars / indicators / chatDraft）

### 5. UI 外壳与页面（W2/W3）

- `components/AppShell.tsx`：顶栏 + 侧边导航 + 内容区，6 个路由
- `components/StatusBar.tsx`：连接 / 选中合约 / 运行模式 / 配对 readiness / pair_epoch / halt / 对账
- `components/NotImplemented.tsx`：统一的"后端未实现"降级组件
- `pages/TerminalPage.tsx`：**真实数据**——价格卡、K 线表、资金费卡、账户与仓位、盘口（1000 档聚合 + 深度条 + 价差 bps）
- `pages/{Strategies,Providers,Decisions,Health,Settings}Page.tsx`：按契约实现骨架；Health 页含 18 项风险开关目录（含 3 项锁定的底线开关）

## 验证证据（全部真实跑出）

### 构建三连（W0-06）

```text
pnpm typecheck  →  tsc --noEmit 通过，0 错误
pnpm lint       →  oxlint: Found 0 warnings and 0 errors. Finished in 12ms on 18 files
pnpm build      →  ✓ 3136 modules transformed. dist/assets/index-*.js 1,134.20 kB (gzip 360.96 kB)
```

### 代理与降级链路

```text
curl /api/health            → {"ok":true,"gateway":"https://127.0.0.1:9053/v1/","allowWrites":false,"credentialsLoaded":true,"plannedLlmEndpoints":17}
POST /api/getticker         → {"last":82757,"high":83269.4,"low":82241.5,"assetType":"usdtmarginedfutures"}
POST /api/getorderbook      → {"bids":1000,"asks":1000,"bestBid":82756.9,"bestAsk":82757}
POST /api/getexchangepairs  → "BTC_USDT,ETH_USDT,SOL_USDT,BNB_USDT,XRP_USDT"
GET  /api/llm/status        → http=501（规划端点，确定性降级）✅ 符合预期
POST /api/submitorder       → http=501（写操作默认关闭）✅ 符合预期
```

### 浏览器实测（截图 `/tmp/webui-1.png`）

打开 `http://127.0.0.1:5173/` 渲染结果（全部真实币安 USDT-M 数据）：

- 最新价 **82,756.90**，24h 高/低 83,269.40 / 82,241.50，买一/卖一 82,756.90/82,757.00
- 240 根已收盘 1h K 线（未收盘的最后一根已按 Q16 排除）
- 资金费率 **-0.0014%**（年化 -1.54%），本期结算 2026-10-10 16:00 CST，下次 2026-10-11 00:00 CST
- 账户：可用 68.69 / 冻结 1.32 / 总额 70.01，杠杆 20×，**持仓模式 单向 one-way（绿色）**，近期盈亏 -0.3184
- 盘口：买 1000 档 / 卖 1000 档，价差 0.10（0.01 bps）
- 顶栏：`已连接 · 运行 11m44s` · `BTC-USDT` · `LLM 决策：待实现`

浏览器控制台仅 2 条：vite 连接日志与 antd 的 `Statistic valueStyle` 弃用警告（已在本会话修掉）。

## 遇到的坑与解决

| 坑 | 现象 | 解决 |
| --- | --- | --- |
| **Vite dev 不认 tsconfig paths** | dev 报 `@/pages/... Are they installed?`，但 `vite build` 却通过 | 在 `vite.config.ts` 加 `resolve.alias: { '@': srcDir }`（tsconfig paths 只对 tsc 生效；dev 的依赖预扫描需要显式 alias） |
| **代理把只读 POST 也拦了** | `POST /api/getticker` 返回 `501 WRITES_DISABLED` | 调整判定顺序：先判 `READ_POST`（语义只读），再判 `ALLOW_WRITES` |
| antd 6 `Statistic.valueStyle` 弃用 | 控制台警告 | 改用 `styles={{ content: {...} }}` |
| TS 报 `FetchBaseQueryError` 不能赋值 `data` | `FETCH_ERROR` 分支无 `data` 字段 | 用局部类型断言补可读错误体 |
| **文档偏差** | `docs/llm-dev/08` 未提及 dev 需显式 Vite alias；已在 `webUI/README.md` 记录 | — |

## 未完成 / 下一步建议

**下一个模型请从 `W3-01` 接手**，建议顺序：

1. `W3-01` 合约列表（525 个，搜索/排序）——数据源 `getexchangepairs`（POST）已有
2. `W3-02` / `W3-03` K 线图（Lightweight Charts 蜡烛 + 成交量）与指标叠加/副图（MACD/RSI/布林）——`gettechnicalanalysis` endpoint 已就绪，`indicatorsAtom` 已定义
3. `W3-06` 滑点模拟器（纯前端按盘口逐档累加）
4. `W3-08` 指标派生事实展示（金叉/死叉状态、柱趋势、均线上下方与斜率）

`W1-07` 只定义了 `llm/status` 与 `llm/coverage` 两个规划端点；补齐其余（providers / activepair / decisions / strategies / conversations / halt / budget / riskswitches / readiness）属该任务剩余部分。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | **运行中**，PID 56291，gRPC 9052 / REST 9053 |
| webUI 本地代理 | **运行中**，PID 见 `/tmp/webui-proxy.pid`，日志 `/tmp/webui-proxy.log`，监听 8788 |
| Vite dev server | **运行中**，PID 见 `/tmp/webui-vite.pid`，日志 `/tmp/webui-vite.log`，监听 5173 |
| 重启方式 | `cd webUI && pnpm dev:all` |
| 只读仪表盘（旧的临时方案） | 仍在 8787（`~/.gocryptotrader/dashboard/`），与 webUI 并存，可作对照 |
