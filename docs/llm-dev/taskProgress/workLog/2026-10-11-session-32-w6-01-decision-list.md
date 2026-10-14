# session-32：`W6-01` 决策列表（前端）+ `W1-07` decisions 端点包装

**日期**：2026-10-11
**关联任务**：`W6-01`（08 §3.5）、`W1-07`（08 §3，补 decisions 端点）
**账目**：（59）W6 0/6 → **1/6**（0.00% → 1.67%）；**总完成度 71.4% → 73.1%**

## 1. 交付物

- `webUI/src/app/api/types.ts`：`LlmDecisionRun` / `LlmListDecisionRunsResponse`（字段 = 网关 proto 投影：id / strategyVersionId / snapshotId / trigger / runMode / decision / reason / inputHash / llmModel / jevModel / createdAt / latencyMs / llmRoundCount / toolCallCount）。
- `webUI/src/app/api/gctApi.ts`：`getLlmDecisionRuns`（GET `llm/decisions`，`strategyVersionId`/`limit` 服务端参数），tag `LlmDecisions`，30s 轮询。
- `webUI/src/pages/DecisionsPage.tsx`：新增**真数据决策列表**（时间 / 触发 / 决策徽标（OPEN 绿 · CLOSE 蓝 · HOLD 灰 · REVIEW 橙）/ 原因码 mono / 策略修订版截断 / `llm × jev` 模型对 / 延迟 / 轮次/工具数 / 输入哈希截断）+ 三件筛选（**策略修订版 id** 服务端过滤 + **决策**客户端过滤 + **最近 50/200/500 条**）+ 空态说明（触发/预览产生的 SHADOW 运行会出现在这里）。血缘十阶段横幅、决策统计卡与风控拒绝码表保留（W6-02/W6-03 的契约面）。
- `webUI/scripts/mock-gateway.mjs`：新增 `/v1/llm/decisions` 离线形状（3 条运行：KLINE_CLOSE/OPEN、MACD_CROSS/REVIEW、MANUAL_PREVIEW/HOLD+RISK_ORDER_NOTIONAL_EXCEEDED；`strategyVersionId` 过滤与 `limit` 生效）。

## 2. 机器化约束（长期有效）

- **列表只读**：页面不提供任何触发/审批动作——触发与预览的入口在 `gctcli llm decisions trigger|preview`（RPC 幂等与门禁都在 CLI/后端侧），前端列表不复制那条链路。
- **SHADOW 永不成单**：空态文案与页脚明示「SHADOW 链永不成单」，血缘/风控详情的入口指向 W6-02/W6-03（避免列表页长出半个详情页）。
- **时间筛选用 limit 表达**：服务端列表 RPC 只支持策略+条数，时间维度以「最近 N 条」呈现，不自造服务端不支持的时间参数。

## 3. 验证（浏览器实测：mock 栈 9443 → 代理 8788 → vite 5173）

- `/decisions`：三行运行完整渲染（逐列断言见下）；筛选控件在位；`llm/status` 不可达时统计卡显示「运行态统计（llm/status）暂不可达」而不是错误。
- 行一：`2026-10-11T09:20:00Z / KLINE_CLOSE / OPEN / COMPOSED_BULLISH_ALIGNMENT / ver-7f3a… / deepseek-chat × jev-1.0.0 / 8420ms / 3/5 / d41d8cd98f00…`。
- 行二：`MACD_CROSS / REVIEW / JEV_LOW_CONFIDENCE / 9110ms / 2/3`。
- 行三：`MANUAL_PREVIEW / HOLD / RISK_ORDER_NOTIONAL_EXCEEDED / 6340ms / 1/2`。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W6 组余：`W6-02` 血缘可视化（十阶段 + 转录/候选/Jev 原文，消费 `llm/decisions/{id}` + lineage）、`W6-03` 风控证据表（消费 `LLMGetRiskEvidence` 的 checks/rejections JSON）、`W6-04` 手动触发预览（消费 trigger/preview RPC；写操作）+ 行详情抽屉、`W6-05` 审计事件流（消费 `llm/audit`）、`W6-06` REVIEW 过期展示（字段面待 `llm/status` 或 decisions 行补充）。
- `W1-07` 余：`strategies` / `audit` 包装（随 W5/W6 页面补）。
