# session-30：`M1-14` providers 命令族（gRPC + gctcli）——W4 组解锁

**日期**：2026-10-11
**关联任务**：`M1-14`（08 §3.4）；前置：M1-09（CRUD 引擎侧）、M1-10（CAS 配对）、M1-03/07（分层探针包）
**账目**：（57）M1 13/15 → **14/15**（5.20% → 5.60%）；**总完成度 69.7% → 70.1%**。W4 组（Provider 页面族）自此不再被后端阻塞。

## 1. 交付物

- **proto（`gctrpc/rpc.proto`）**：§3.4 全 7 个 RPC —— `LLMListProviderProfiles` / `LLMCreateProviderProfile` / `LLMUpdateProviderProfile`（追加新修订版）/ `LLMDeleteProviderProfile` / `LLMTestProviderProfile` / `LLMGetActiveModelPair` / `LLMSwitchActiveModelPair`，及 14 个消息（`LLMProviderProfile` / `LLMProviderRevision` / `LLMProbeLayerResult` 等）。`buf generate` 连跑两次**幂等**（四文件聚合哈希 `5dbc74c8d65214aeacdaa14865fad1e47717304d8feebbc87bc438abee7e4182`）。
- **`engine/rpcserver_llm_provider.go`**：7 个 handler + **分层探针生产路径**（此前 `InsertProbeResult` 只有仓储半截，没有编排方）：解析修订版（默认取 live）→ `secrets.Open` + `vaultSecretFn` 每请求解密 → `openai.ProbeAll` 四层 / `jev.Probe` 单层 → **每条判决落 `llm_probe_results`（含失败）** → 判决上 wire。转换器 `providerProfileToProto` / `providerRevisionToProto` / `probeVerdictToProto`。
- **`cmd/gctcli/llm_provider.go`**：`llm providers list|create|update|delete|test|pair|switch` 命令族（与 RPC 一一对应、JSON 原样输出）；`switch` 缺 `--idempotency-key` 在客户端即拒绝（没有幂等键的重试与第二次切换无法区分）。注册进 `llmCommand`。
- **测试**（`engine/rpcserver_llm_provider_test.go`，7 个测试函数）：fail-closed 七连、profile 生命周期（create→update×2→archive→含归档列表）、输入校验、activepair 三态、CAS 切换（错密码/陈旧 epoch/成功→DEGRADED/清空槽位→INCOMPLETE）、**探针全路径**（真 httptest 服务器：AUTH+TOOL_CALL+STREAM 过、SCHEMA 非候选拒——四层独立判决 + 四行落库 + `Bearer sk-llm-1` 上线上）、基础设施故障（未知 profile/跨 profile 修订版/无 live 修订版/无 vault）、纯转换器。

## 2. 机器化约束（长期有效）

- **探针失败是答案不是错误**：`LLMTestProviderProfile` 对层判决一律 200 返回并落库；只有基础设施故障（未知 profile、vault 不可用）才拒 RPC——「问它能不能做」的失败回答与「问都问不到」是两回事。
- **密钥不上命令行、不上 wire**：CLI 只收 `secret://` 引用 + 指纹；测试机器断言序列化后的响应不含明文 key。vault 每请求 `Reveal`。
- **切换=三重闸**：`verifyOperatorPassword`（重认证）+ `expected_pair_epoch`（CAS）+ `idempotency_key`（重试安全）缺一不可；空修订版 id = 清空该槽位，构造性 INCOMPLETE，永远不可能是合法 READY。
- **删除永远是归档**：修订版是不可变历史，行从不删除（09 §2.1）；归档未知 id 拒绝（先 Get 再 Archive）。
- **proto 扩面先于 handler**：接口由生成代码长出新方法，`RPCServer` 不实现就编译不过——「加 RPC 需与 handlers 原子落地」由编译器保证。

## 3. 验证

- `go build ./...` 0 · `go vet ./engine/` 0 · **全树 `golangci-lint run` 0 issues**（修 depguard：测试导入改用仓库 `encoding/json` 切换包；gci/gofumpt 对齐）· `make misc_checks` **All passed**（CI 等价树，含生成物一致性）。
- 聚焦：`go test ./engine/ -run 'TestLLMProvider|TestLLMGetActiveModelPair|TestLLMSwitchActiveModelPair|TestLLMTestProviderProfile' -count 1` **全绿**；`go test ./cmd/gctcli/ -count 1` **全绿**。
- 引擎整套（-skip 两个既有 flaky）：**全绿**（`go test ./engine/ -count 1 -skip 'TestGetDefaultConfigurations|TestNewExchangeByNameWithDefaults'`，91.273s，exit 0）。

## 4. 未完成与下一步

- M1 组余 `M1-15`（真实供应商冒烟，DeepSeek + Jev 凭据门）——与前排 M6 数据面同属凭据阻塞。
- **W4 组解锁**：`W4-01`（档案列表）可直接消费 `llm providers list`；`W4-03`（分层探针 UI）消费 `providers test` 的四层判决；`W4-04/05`（配对横幅/切换）消费 `pair`/`switch`。前端侧尚缺 `W1-07` 的端点包装 + 代理白名单（`/v1/llm/providers*`、`/v1/llm/activepair`）。
- 引擎整套复跑（后台任务）：**91.273s 全绿、exit 0**（结果已并回 §3）。
