# session-34：`W4-02` 档案编辑表单（追加新修订版，写路径实测）

**日期**：2026-10-11
**关联任务**：`W4-02`（05 §2.2）、顺带验证 `W1-07` 的写端点连通
**账目**：（61）W4 1/6 → **2/6**（1.33% → 2.67%）；**总完成度 74.8% → 76.1%**

## 1. 交付物

- `webUI/src/app/api/gctApi.ts`：`updateLlmProviderProfile` mutation（POST `llm/providers/{id}`，追加新修订版），`invalidatesTags: ['LlmProviders']`——提交后列表自动重取。
- `webUI/src/pages/ProvidersPage.tsx`：
  - 档案表新增**「编辑」action**（已归档禁用）→ 打开 **Drawer 表单**；
  - 表单字段与最新修订版**逐字段预填**（actor 默认 `webui-operator`、baseUrl、modelId、apiKeyRef、apiKeyFingerprint（16 hex 前端校验）、timeoutSeconds(1-120)、LLM 专属 maxOutputTokens/promptVersion 或 JEV 专属 questionTemplateVersion（按 kind 切换）、dailyBudgetUsd、reservationCap24h、allowInsecureLocalhost、proxyAddress）；
  - 抽屉顶端警示块写明**语义**：提交 = 追加新修订版不是就地修改；被取代的若是激活配对指向的版本 → 指针清空 + `pair_epoch++` + INCOMPLETE；**密钥录入在 vault（W4-06），表单只填引用与指纹**；
  - 提交成功 `message.success` 后关闭抽屉、列表重取；失败按 `errorText` 展示（含 501 `WRITES_DISABLED` 的解释）。
- `webUI/scripts/mock-gateway.mjs`：`POST /v1/llm/providers/{id}` 离线形状——在 mock 内存中追加修订版（revisionNo+1、字段回读）。

## 2. 机器化约束（长期有效）

- **写路径由代理闸门控制**：本次浏览器实测把本地代理以 `GCT_ALLOW_WRITES=true` 重启（仅 dev 栈测试窗）；**默认仍是 false**——治理写（providers/审核/配对）不开箱即用。测完已把代理恢复默认。
- **表单永不接收明文密钥**：只有 `secret://` 引用 + 16 hex 指纹两个字段（与后端校验一致：引用 kind 必须 `llm`、指纹必须 16 hex）。
- **修订版不可变**在 UI 措辞与交互里双重表达（警示块 + 「提交（追加新修订版）」按钮文案）——不出现「修改」这个动词。

## 3. 验证（浏览器实测：mock 9443 → 代理 8788（写开）→ vite 5173）

- 打开 DeepSeek 行「编辑」：抽屉全字段预填正确（deepseek-chat / secret://llm/8a1b2c3d / abc123def4567890 / 30s / 2048 / analysis-readonly-v1 / 5.00 / 3 / 关）。
- 改模型 ID 为 `deepseek-reasoner` → 提交：抽屉关闭、**列表刷新为 `#3（共 3 版）` + `deepseek-reasoner`**（mock 内修订版真实追加）。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W4 组余：`W4-03` 分层探针 UI（消费 `providers test` 的四层判决）、`W4-05` 切换（重认证 + CAS + 影响面）、`W4-06` vault 初始化/密钥录入 UI；`W4-04` 数据面已在（升级横幅形态）。
- 队列：W6-02/03（血缘/风控证据）、`W7-07` 覆盖运行面、SHADOW `RiskFacts` 生产装配、`M2-13` 末项。
