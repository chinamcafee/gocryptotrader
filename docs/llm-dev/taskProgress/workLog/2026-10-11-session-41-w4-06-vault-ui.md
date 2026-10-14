# session-41：`W4-06` 密钥保管（vault 初始化与录入 UI + RPC）——W4 组完成

**日期**：2026-10-11
**关联任务**：`W4-06`（09 §5）
**账目**：（67）W4 5/6 → **6/6**（6.67% → 8.00%，**W4 组完成**）；**总完成度 84.8% → 86.1%**

## 1. 交付物

- **proto**：`LLMVaultStatus`（GET `/v1/llm/vault`）/ `LLMVaultInit`（POST `/v1/llm/vault/init`）/ `LLMVaultAddSecret`（POST `/v1/llm/vault/secrets`）+ 4 消息；`buf generate` 连跑幂等 `66b5dba3…`。
- **引擎** `engine/rpcserver_llm_vault.go`：
  - `LLMVaultStatus`：未初始化目录 → `initialized=false`（**状态不是错误**，UI 以此决定是否显示「初始化」）；已初始化 → 条目表（引用/类别/指纹/时间——**只描述不泄密**）；
  - `LLMVaultInit`：`secrets.Init` **幂等**——第二次调用原样打开，**永不铸新钥匙**（不会让旧密钥失联）；
  - `LLMVaultAddSecret`：kind+secret 校验 → `Open` + `Add` → **只回「引用 + 指纹」**，明文不落响应、不落日志。
  - 测试 `TestLLMVaultRPCs`：fail-closed / 未初始化录入拒绝 / 初始化→录入→指纹==`secrets.Fingerprint(明文)` / 响应不含明文 / **二次 Init 后旧密钥仍可 Reveal**（幂等的机器证明）/ 空 secret 拒绝。
- **前端** `ProvidersPage` 新增「密钥保管（vault）」卡：状态（已/未初始化 + 目录）+ 「初始化 vault」按钮（未初始化时显示）+ 条目表 + inline 录入表单（kind llm/jev + **`Input.Password` 明文不回显** + autoComplete=new-password）→ 成功 Alert 显示「引用 + 指纹」并提示把它填进档案修订版；保管纪律注释（永不重铸/只落密文/界面只见引用与指纹/写闸门）。
- **mock**：vault 三端点离线形状（未初始化 → init → 录入返回 ref+指纹并记录条目）。

## 2. 机器化约束（长期有效）

- **明文单向**：录入的 secret 是 write-only——响应只有引用与指纹；UI 输入框永远不回显（配套「永不重铸钥匙」的幂等 Init）。
- **初始化是状态不是动作开关**：重复初始化无害且不会改变任何既有密钥——把「不可逆的资源创建」做成幂等，排除了丢失旧密钥这一类事故。
- **写路径闸门**：vault init/录入与其它治理写同一闸门（默认拦截）。

## 3. 验证

- 引擎：`go build` 0 · `TestLLMVaultRPCs` 全绿。
- 代理链 curl（写开测试窗）：`GET /api/llm/vault` → `{initialized:false,…}`；`POST …/vault/init` → ok；`POST …/vault/secrets {kind:"llm",secret:"sk-live-demo-123"}` → `{"ref":"secret://llm/391f99f0","fingerprint":"854efb59e1b47b9c"}`（**明文不出现在响应里**）。
- 浏览器：`/providers` 密钥保管卡渲染（条目表 + 录入表单 + 纪律注释）。
- `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。

## 4. 未完成与下一步

- **W4 组 6/6 完成**。余下队列：W6-06（REVIEW 过期）、W7-06（通知）、W7-07（覆盖运行面）、**适配器缺口三件套**（halt 写侧/对账触发/accounting 读面——解锁 W7-01/02/03 写侧）、SHADOW `RiskFacts` 生产装配、`M2-13` 末项；`M6-03/04/05`、`M1-15` 等凭据（decisions/01）。
