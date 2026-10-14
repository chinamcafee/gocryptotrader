# webUI —— GoCryptoTrader LLM/Jev 决策平台前端

币安 USDT-M 永续（全量 525 个合约）的交易可视化 + LLM/Jev 两级模型决策平台。

技术方案见 `docs/llm-dev/`；开发进度与接力说明见 `docs/llm-dev/taskProgress/`。

## 快速开始

前置：GoCryptoTrader 已在本地跑起来（`docs/localDeploy/` 有完整步骤）。

```bash
cd webUI
pnpm install

# 一条命令起「本地代理 + Vite 开发服务器」
pnpm dev:all

# 或分开起（两个终端）
pnpm proxy     # 本地代理 → http://127.0.0.1:8788/api/
pnpm dev       # 前端     → http://127.0.0.1:5173/
```

打开 <http://127.0.0.1:5173/>

## 三个进程的关系

```text
浏览器 (5173)
   │  /api/*  （前端不持有任何凭据）
   ▼
本地代理 (8788)  ← 从 ~/.gocryptotrader/config.json 读 Basic Auth 注入
   │  https（自签证书，代理侧跳过校验）
   ▼
GoCryptoTrader REST 网关 (9053)  ← grpc-gateway，protojson
   │
   ▼
GoCryptoTrader 引擎 (9052 gRPC)  ← 币安 USDT-M 永续
```

**为什么必须多这一层代理**（`proxy/gct-proxy.mjs`）：

1. **安全**：REST 网关用 Basic Auth。凭据只留在代理进程内存，绝不进浏览器（设计底线：前端不持有密钥）
2. **TLS**：网关是自签证书，浏览器直连会被拦
3. **治理**：代理实施只读白名单；写操作默认关闭（`GCT_ALLOW_WRITES=false`，返回 501）；对尚未实现的 `/v1/llm/*` 返回确定的 501，让 UI 稳定地展示"后端待实现"

## 代理配置

复制 `.env.example` 为 `.env` 可覆盖默认值（不复制也能跑）：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `GCT_GATEWAY_BASE` | `https://127.0.0.1:9053/v1/` | REST 网关地址 |
| `GCT_PROXY_PORT` | `8788` | 代理端口 |
| `GCT_CONFIG_PATH` | `~/.gocryptotrader/config.json` | 凭据来源 |
| `GCT_ALLOW_WRITES` | `false` | **保持 false** 直到后端 LLM 子系统落地 |
| `GCT_TLS_INSECURE` | `true` | 跳过自签证书校验（仅本地开发） |

代理自检：`curl -s http://127.0.0.1:8788/api/health`

## 脚本

| 命令 | 作用 |
| --- | --- |
| `pnpm dev:all` | 同时起代理 + 前端（推荐） |
| `pnpm proxy` | 只起代理 |
| `pnpm dev` | 只起前端（需代理已在跑） |
| `pnpm build` | `tsc --noEmit && vite build` |
| `pnpm typecheck` | 仅类型检查 |
| `pnpm lint` | oxlint |
| `pnpm preview` | 预览构建产物 |

## 页面

| 路由 | 内容 | 后端依赖 |
| --- | --- | --- |
| `/terminal` | 交易台：价格、K 线、盘口、资金费、账户与仓位 | ✅ 已可用（全部真实数据） |
| `/markets` | 合约覆盖：789 个 available 的范围分类（588 个 USDT 永续）+ 两层轮询预算 + 搜索/筛选 + 点击跳交易台 | ✅ 数据面真实；轮询运行面待 `W7-07` |
| `/strategies` | 策略工作区：对话、条款提取、版本树、审批 | ⏳ 待 M4 |
| `/providers` | 模型与配对：LLM/Jev 档案、探针、pair_epoch | ⏳ 待 M1 |
| `/decisions` | 决策与审计：血缘、风控证据、审计流 | ⏳ 待 M4 |
| `/health` | 健康与治理：halt、对账、覆盖轮询状态、风险开关 | ⏳ 待 M5（覆盖运行面待 W7-07） |
| `/settings` | 运行参数与安全边界说明 | ✅ |

标 ⏳ 的页面已按接口契约实现骨架，后端未实现时展示明确的"待实现"提示与对应任务 ID。

## 目录结构

```text
webUI/
├── proxy/gct-proxy.mjs      # 本地代理（安全边界）
├── scripts/dev-all.mjs      # 一键起服务
├── src/
│   ├── app/api/types.ts     # 领域类型（严格对齐网关 camelCase + pair 对象）
│   ├── app/api/gctApi.ts    # RTK Query 客户端（含实测坑位注释）
│   ├── app/api/coverage.ts  # 范围筛选与轮询预算（⚠ llm/coverage 的前端镜像，见文件头注释）
│   ├── app/store.ts         # Redux store
│   ├── state/atoms.ts       # Jotai 客户端状态
│   ├── theme.ts             # 设计 token（#0B111B / #25B49A / #EC6A70 / #5B9CFF）
│   ├── components/          # AppShell / StatusBar / NotImplemented（降级）
│   └── pages/               # 7 个路由页
└── vite.config.ts           # 含 @ → src 别名与 /api 代理
```

## 开发注意（实测坑位，别重复踩）

1. 网关响应是 **camelCase**，`pair` 是对象 `{delimiter,base,quote}`，不是字符串
2. `getticker`/`getorderbook`/`getexchangepairs` 是 **POST**（语义只读）；其余是 GET
3. `gethistoriccandles` 的 `start`/`end` 要 `"2006-01-02 15:04:05 MST"`；`gettechnicalanalysis` 要 **RFC3339** —— 两者不同
4. `getlatestfundingrate` **不要**传 `includePredicted=true`（会 500）
5. **写新东西前先找 Go 侧有没有同名规则**：`coverage.ts` 是 `llm/coverage` 的镜像，属于**有记录的重复**。
   打通 `LLMGetCoverage` RPC 后应删掉本地算术。加新规则前先问：这条规则 Go 侧有吗？有就别再写一份。
6. `futures getcollateral` 在币安上不可用（用 `getaccountbalances`）
7. 币安永续**无 WebSocket**，行情靠 REST 轮询；`orderbook` 可靠 1000 档
8. 合约 REST ticker 不填 `markPrice`/`indexPrice`/`openInterest`（恒为 0），不要当真实值展示

以上全部来自 `docs/localDeploy/README.md` 的「实测验证记录」，改接口前请先读那一节。
