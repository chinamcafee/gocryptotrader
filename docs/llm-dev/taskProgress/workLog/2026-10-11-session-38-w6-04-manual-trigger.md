# session-38：`W6-04` 手动触发与预览（写路径 + 门禁语义）

**日期**：2026-10-11
**关联任务**：`W6-04`（07 §4、08 §3.5）
**账目**：（65）W6 4/6 → **5/6**（6.67% → 8.33%）；**总完成度 82.1% → 83.8%**

## 1. 交付物

- `webUI/src/app/api/gctApi.ts`：`triggerDecisionAnalysis`（POST `llm/decisions/trigger`）与 `previewDecision`（POST `llm/decisions/preview`）两个 mutation，tag 失效重取（新运行立刻出现在列表置顶）。
- `webUI/src/pages/DecisionsPage.tsx`：决策列表卡新增 **「手动触发/预览」** action → Modal——策略修订版 id + 市场 id + actor 三字段；说明块写明**两者差异**：触发走完整门禁（pre-gate：配对/探针/风控开关/预算/运行模式，未过即拒）、**预览永远不下单**（只跑分析链、以 HOLD/REVIEW 收尾）；两者都写 SHADOW 运行记录；写操作受代理 `GCT_ALLOW_WRITES` 闸门。两个动作按钮（触发分析（过门禁）/ 仅预览（HOLD/REVIEW））各自 loading；成功 toast 带上决策与原因码。
- `webUI/scripts/mock-gateway.mjs`：`POST /v1/llm/decisions/trigger|preview` 离线形状——preview 产出 `MANUAL_PREVIEW/HOLD/PREVIEW_ONLY_HOLD` 运行、trigger 产出 `MANUAL/REVIEW/AWAITING_CONFIRMATION` 运行，均 unshift 进列表源。

## 2. 机器化约束（长期有效）

- **预览的收尾是契约**：UI 文案与 mock 都钉死「预览以 HOLD/REVIEW 收尾」——预览不是「也许能开仓」的入口。
- **触发与门禁分离**：触发按钮明示要过 pre-gate；未过由后端拒、错误经 `errorText` 原样展示——前端不预判门禁结果。
- **写路径一致性**：与 W4-02/W4-03/W4-05 同一条闸门（默认拦截，dev 测试窗临时开启后恢复）。

## 3. 验证（浏览器实测：mock 9443 → 代理 8788（写开）→ vite 5173）

- 代理链 curl：`POST /api/llm/decisions/preview` → `{"id":"run-mock-4",…,"trigger":"MANUAL_PREVIEW","decision":"HOLD","reason":"PREVIEW_ONLY_HOLD"}`。
- 浏览器：打开 Modal（三字段预填 actor）→ 填 `ver-7f3a2b1c-0001` + `Binance:usdtmarginedfutures:BTC_USDT` → 「仅预览」→ **toast「预览完成：HOLD（PREVIEW_ONLY_HOLD）——已写入决策列表」**、Modal 关闭、**列表置顶新行** `2026-10-11T11:53:09.767Z / MANUAL_PREVIEW / HOLD / PREVIEW_ONLY_HOLD / ver-7f3a…`。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W6 组余：`W6-06` REVIEW 过期展示（12h 自动过期状态与原因——字段面待补，可能随 `llm/status` 或 decisions 行扩展）。
- 队列：W4-06（vault UI，需新 RPC）、`W7-07` 覆盖运行面、SHADOW `RiskFacts` 生产装配、`M2-13` 末项。
