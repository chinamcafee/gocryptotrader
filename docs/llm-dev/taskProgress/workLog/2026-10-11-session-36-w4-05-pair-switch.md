# session-36：`W4-05` 切换活化配对（CAS + 重新认证 + 影响面提示）

**日期**：2026-10-11
**关联任务**：`W4-05`（05 §2.4）
**账目**：（63）W4 3/6 → **4/6**（4.00% → 5.33%）；**总完成度 77.4% → 78.7%**

## 1. 交付物

- `webUI/src/app/api/gctApi.ts`：`switchActiveModelPair` mutation（POST `llm/activepair`；三重闸字段全部显式：口令/`expectedPairEpoch`/`idempotencyKey`/actor），tag 失效重取（配对卡与档案列表同步刷新）。
- `webUI/src/pages/ProvidersPage.tsx`：活化配对卡「切换配对」action → **切换 Modal**——
  - **修订版下拉只列 live 版本**（从 provider 列表 flatMap 出未归档档案的最新修订版，label = `档案名 · #n · 模型`）；
  - `expected_pair_epoch` 预填当前读到的 epoch（CAS 的「你读到的那一版」）；
  - 操作者口令 `Input.Password`（重认证；带显示切换）+ **自动生成幂等键**（`crypto.randomUUID`，同键同参=同一次切换）+ actor；
  - 警示块写明影响面：激活后 `pair_epoch+1`；旧配对下运行中的策略将暂停、有效审批将被撤销（影响登记在切换命令里）；提示先跑探针（readiness 由最新判决推导）。
- `webUI/scripts/mock-gateway.mjs`：`POST /v1/llm/activepair` 离线形状——**三重闸逐项**：口令不符 403 `operator password rejected`；epoch 不符 409 `pair epoch conflict`；通过后 epoch+1、两侧修订版写入、readiness=两侧都有则 READY。

## 2. 机器化约束（长期有效）

- **切换=三重闸，缺一不可**：口令（重认证，界面不存不缓存）+ `expected_pair_epoch`（CAS，防并发覆盖）+ 幂等键（重试安全）。UI 与后端 `ActivateModelPair` 的语义一字不差。
- **只能切到 live 修订版**：下拉选项来自未归档档案的最新修订版——历史版本不可激活（后端 `validateLiveRevision` 同规则）。
- **影响面提示不说假数**：策略暂停/审批撤销的数量当前由后端登记（M1-10 起为 0），UI 只陈述语义不编数字。

## 3. 验证（浏览器实测：mock 9443 → 代理 8788（写开）→ vite 5173）

- 负路径（经真代理链 curl）：错误口令 → `403 {"code":7,"message":"operator password rejected"}`；陈旧 epoch → `409 {"code":10,"message":"pair epoch conflict: expected 999, stored 3"}`。
- 正路径（浏览器）：打开切换 Modal——修订版预填 `DeepSeek 生产 · #2 · deepseek-chat` / `TypeSafe Prod · #1 · jev-1.0.0`、epoch=3、幂等键自动 UUID；填口令提交 → Modal 关闭、**配对卡刷新 `pair_epoch: 4`、readiness READY、更新于 11:49:17Z、更新者 webui-operator**。
- `tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- W4 组余：`W4-06` 密钥保管（vault）初始化与密钥录入 UI——**需要新的 RPC**（proto 里 `llm/vault` 仍是规划端点）；`W4-04` 数据面已在（升级横幅形态）。
- 队列：W6-02/03（血缘/风控证据）、`W7-07` 覆盖运行面、SHADOW `RiskFacts` 生产装配、`M2-13` 末项。
