# session-31：`W4-01` Provider 档案列表（前端）+ `W1-07` 端点包装（providers/activepair）

**日期**：2026-10-11
**关联任务**：`W4-01`（05 §2.1、06 §8）、`W1-07`（08 §3，补两个端点）、顺带 `W4-04` 的配对数据面
**账目**：（58）W4 0/6 → **1/6**（0.00% → 1.33%）；**总完成度 70.1% → 71.4%**

## 1. 交付物

- `webUI/src/app/api/types.ts`：`LlmProviderRevision` / `LlmProviderProfile` / `LlmListProviderProfilesResponse` / `LlmActivePair`——字段与网关 JSON 同名（proto 字段的 camelCase 投影）；注释钉死红线：**界面上的密钥只有两种表达：vault 引用与指纹**。
- `webUI/src/app/api/gctApi.ts`：`getLlmProviders`（GET `llm/providers`，可选 `includeArchived`）与 `getActiveModelPair`（GET `llm/activepair`），tag `LlmProviders`，30s 轮询。
- `webUI/src/pages/ProvidersPage.tsx` 重写：**真数据档案表**（显示名+归档徽标 / 类型 / 最新修订版 `#n（共 m 版）` / 模型+Base URL / 指纹 / 启用状态；**行展开** = 修订版详情 Descriptions——引用、指纹、超时、最大输出、提示词版本、问题模板版本、成本计划值、24h 预约上限、允许本地 HTTP、建立人与时间）+ **「活化配对」卡片**（pair_epoch / readiness / 两侧修订版 / 更新者；`found=false` 显示「尚未激活过」而非错误）+ 「含归档」开关；两张编辑表单契约保留（W4-02 的依据），去掉「计划中」措辞。
- `webUI/scripts/mock-gateway.mjs`：新增 `/v1/llm/providers` 与 `/v1/llm/activepair` 的离线形状（两档案 + 修订版 + READY 配对），供浏览器实测。
- **代理白名单零改动**：GET 的 `llm/*` 规划路径本就放行（只有网关 404 才转 501）——providers/activepair 有了真 handler 后读路径即刻转正；写路径仍受 `GCT_ALLOW_WRITES` 闸门（治理写不该默认放行）。

## 2. 机器化约束（长期有效）

- **明文 key 永不经过前端**：`apiKeyRef`（引用）与 `apiKeyFingerprint`（指纹）是界面唯二的密钥表达；测试用的 mock 形状也遵循此约定。
- **归档不是删除**：archived profile 只以徽标 + 「含归档」开关出现；前端不发明「删除」这个动作（后端语义是归档）。
- **读放行/写闸门由代理统一执行**：页面从不尝试绕过（W4-02 起写操作要么走 `GCT_ALLOW_WRITES=true` 的本地代理，要么直连网关）。

## 3. 验证（浏览器实测：mock 栈 9443 → 代理 8788 → vite 5173）

- `/providers` 完整渲染：活化配对卡片（pair_epoch=3 / READY / rev-llm-2 / rev-jev-1 / 更新于 2026-10-11T09:15:00Z / op）；档案表两行——**DeepSeek 生产**（LLM_OPENAI_COMPAT、#2（共 2 版）、deepseek-chat、api.deepseek.com/v1、`abc123def4567890`、启用）与 **TypeSafe Prod**（JEV、#1（共 1 版）、jev-1.0.0、api.typesafe.ai、`fedcba0987654321`、启用）。
- 行展开：修订版详情逐字段渲染（含 `secret://llm/8a1b2c3d`、成本计划值 5.00、24h 上限 3、允许本地 HTTP 否、建立于/建立人）。
- `tsc --noEmit` 0 · `oxlint src proxy` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W4 组余：`W4-02` 编辑表单（写操作，需 `GCT_ALLOW_WRITES=true` 或直连）、`W4-03` 分层探针 UI（消费 `providers test` 的四层判决）、`W4-05` 切换（重认证+CAS+影响面提示）、`W4-06` vault 初始化 UI；`W4-04` 横幅的数据面已在（升级为横幅形态即可）。
- `W1-07` 余：`decisions` / `strategies` / `audit` 包装（随 W6 页面补）；`conversations` 族已由 `chat.ts` 覆盖。
