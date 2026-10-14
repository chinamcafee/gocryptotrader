# session-35：`W4-03` 分层探针触发与结果展示（auth/tool_call/schema/stream；systemone）

**日期**：2026-10-11
**关联任务**：`W4-03`（05 §3、06 §8）
**账目**：（62）W4 2/6 → **3/6**（2.67% → 4.00%）；**总完成度 76.1% → 77.4%**

## 1. 交付物

- `webUI/src/app/api/types.ts`：`LlmProbeLayerResult` / `LlmTestProviderProfileResponse`。
- `webUI/src/app/api/gctApi.ts`：`testLlmProviderProfile` mutation（POST `llm/providers/{id}/test`；可选 `revisionId`）+ tag 失效重取（判决落库影响 readiness）。
- `webUI/src/pages/ProvidersPage.tsx`：档案行新增**「探针」action** → **结果 Modal**——标题含档案名与测试的修订版 id；说明行写四层各自失败的诊断含义；结果表 = 层 / PASS·FAIL 徽标 / 错误码 mono / 延迟 / tokens（出/入）/ 返回模型；失败行可展开读 `errorMessage`（mock 的 SCHEMA 失败带完整解释文本）；行注释明示「判决已落库并影响 readiness」。
- `webUI/scripts/mock-gateway.mjs`：`POST /v1/llm/providers/{id}/test` 离线形状——LLM 返回四层判决（AUTH/TOOL_CALL/STREAM 过、SCHEMA 失败 `CANDIDATE_INCOMPLETE` + 解释文本）；JEV 返回单层判决；未知档案 404、无 live 修订版 400。

## 2. 机器化约束（长期有效）

- **探针是「真招」**：每次点击都会真实请求供应商并落库（warn 文案明示），不是静态演示——测试链路与后端 `LLMTestProviderProfile` 的语义一字不差（失败=判决不是错误；基础设施故障才拒）。
- **四层各自诊断**：UI 直接呈现「哪层失败 → 修什么」（密钥/函数调用/编造交易/流式），与 05 §3 的分层动机对齐。
- **写闸门**：探针是 POST——本地 dev 栈测试窗临时 `GCT_ALLOW_WRITES=true`，测后恢复默认 false。

## 3. 验证（浏览器实测：mock 9443 → 代理 8788（写开）→ vite 5173）

- DeepSeek 行「探针」→ Modal：`rev-llm-2`；四行判决——AUTH PASS 402ms 18/2、TOOL_CALL PASS 1240ms 64/21、**SCHEMA FAIL CANDIDATE_INCOMPLETE** 880ms（可展开读解释）、STREAM PASS 512ms 12/1；返回模型均 `deepseek-chat`。
- Jev 单层路径（经真代理链）：`POST /api/llm/providers/prof-jev-1/test` → `{"status":200,"kind":"JEV","layers":["JEV:PASS"]}`。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W4 组余：`W4-05` 切换配对（重认证 + CAS + 影响面提示）、`W4-06` vault 初始化/密钥录入 UI；`W4-04` 数据面已在（升级横幅形态）。
- 队列：W6-02/03（血缘/风控证据）、`W7-07` 覆盖运行面、SHADOW `RiskFacts` 生产装配、`M2-13` 末项。
