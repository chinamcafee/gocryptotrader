# session-33：`W6-05` 审计事件流（前端）+ `W1-07` audit 端点包装

**日期**：2026-10-11
**关联任务**：`W6-05`（08 §3.5、10 §6）、`W1-07`（08 §3，补 audit 端点）
**账目**：（60）W6 1/6 → **2/6**（1.67% → 3.33%）；**总完成度 73.1% → 74.8%**

## 1. 交付物

- `webUI/src/app/api/types.ts`：`LlmAuditEvent` / `LlmListAuditEventsResponse`（id / kind / actor / subject / detailJson / createdAt）。
- `webUI/src/app/api/gctApi.ts`：`getLlmAuditEvents`（GET `llm/audit`，`kind`/`limit` 服务端参数），tag `LlmDecisions`，30s 轮询。
- `webUI/src/pages/DecisionsPage.tsx`：新增**审计事件流卡片**——事件表（时间 / 类型 mono / 操作者 / 对象 mono / 详情 mono 截断+悬浮全文）+ 事件类型筛选（PAIR_SWITCH / APPROVAL_GRANTED / APPROVAL_REVOKED / RISK_SWITCH_SET / CRASH_RECOVERY / STRATEGY_PAUSED）+ 最近 100/500/2000 条 + **「导出 JSON」**（Blob 客户端下载，含时间戳文件名）。
- `webUI/scripts/mock-gateway.mjs`：新增 `/v1/llm/audit` 离线形状（4 条：PAIR_SWITCH / APPROVAL_GRANTED / RISK_SWITCH_SET / CRASH_RECOVERY；kind 过滤与 limit 生效）。

## 2. 机器化约束（长期有效）

- **审计流只读**：页面无任何改写动作——append-only 的精神在 UI 层也成立（连「清除」按钮都不存在）。
- **导出是客户端下载**：Blob + object URL，不发任何服务端请求；导出的就是当前筛选后看到的字节。
- **kind 过滤走服务端**：与列表 RPC 的能力对齐，不在前端伪造服务端没有的过滤维度。

## 3. 验证（浏览器实测：mock 栈 9443 → 代理 8788 → vite 5173）

- `/decisions` 审计卡片四行完整渲染：`PAIR_SWITCH/op/pair#epoch3/{"fromEpoch":2,…}`、`APPROVAL_GRANTED/op/strategy-ver-…/{"validUntil":…}`、`RISK_SWITCH_SET/op/maxSlippageBps/{…}`、`CRASH_RECOVERY/engine/intent-a/{"reconciliationRequired":true,…}`；筛选与条数控件、导出按钮在位。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W6 组余：`W6-02` 血缘（行详情：转录/候选/Jev 原文）、`W6-03` 风控证据表（checks/rejections）、`W6-04` 手动触发预览（行详情抽屉 + trigger/preview 写路径）、`W6-06` REVIEW 过期展示（字段面待补）。
- `W1-07` 余：仅 `strategies` 包装（随 W5 页面补）；`conversations`/`providers`/`activepair`/`decisions`/`audit` 均已就位。
