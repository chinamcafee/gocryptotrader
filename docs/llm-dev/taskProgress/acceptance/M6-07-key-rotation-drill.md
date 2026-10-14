# M6-07 密钥轮换演练记录

> 依据：`docs/llm-dev/11-实施路线图.md` §8 ——「轮换币安 Key 和模型 Key，验证 vault 和配置加密流程」。
> 状态口径同 M6-01/M6-02：**机器可验证的链路已跑通并有证据；需要真实凭据的操作手册已写全，标注 🔴 待凭据执行**。

---

## 1. 自动化演练（模型 Key 侧）—— ✅ 已执行

**测试**：`engine/llm_key_rotation_drill_test.go` → `TestM6KeyRotationDrill`（真 vault 目录 + 真 sqlite 库 + 生产管理路径）。

**命令与结果**：

```console
go test ./engine/ -count 1 -run 'TestM6KeyRotationDrill'   → ok（2.503s）
```

**链路与逐项断言（每一步为什么值钱）**：

| # | 步骤 | 断言 | 为什么这一断言值钱 |
| --- | --- | --- | --- |
| 1 | 供应商侧签发新 Key → vault `Add("llm", 新明文)` | 新引用 ≠ 旧引用；**vault 重开幂等（不重铸包裹钥匙对）** | 轮换的第一步是"新密钥入库"；重开幂等保证轮换不破坏既有密钥的包围 |
| 2 | `AddProviderRevision`（生产管理路径）追加新修订版 | `RevisionNo=2`；只携带**新引用 + 新指纹** | 轮换走的是和普通编辑同一条路——没有旁路 |
| 3 | supersede 舞蹈 | **LLM 激活指针清空 + readiness=INCOMPLETE + epoch++**；Jev 侧指针不动 | 旧端点上的运行中策略被结构性排除；"只动被换的那一边" |
| 4 | 旧修订版 `SupersededAt` 非空；用它走 `ActivateModelPair` | `ErrRevisionNotLive` 拒 | **superseded 修订版永不可复活**——回滚要开新修订版，不是改历史 |
| 5 | 旧密钥 `Reveal` / 新密钥 `Reveal` | 两者都活：旧=`sk-llm-old-111`、新=`sk-llm-rotated-222` | **轮换是追加不是销毁**：审计回放要能读历史（删除属另一个动作） |
| 6 | 新修订版 + Jev 各落探针 → CAS 重新激活 | epoch 2→3、readiness=READY、指针=新修订版；`pair_switch_commands` 有 From=2/To=3/操作者 | "轮换后重新就绪"的完整闭环 + 切换留痕 |
| 7 | 泄密机器证明 | 本轮触及的修订版/配对/切换命令渲染文本**不含任何一个明文 Key**；指纹在、明文不在 | vault 与"配置加密流程"的核心主张 |

---

## 2. 操作手册：模型 Key 轮换（生产）

1. 供应商侧创建**新 Key**；旧 Key **暂不废止**（保留回滚窗口）。
2. Web UI「模型与配对」→「密钥保管」录入新 Key（无回显；得到引用+指纹）→ 编辑 LLM 档案填新引用与指纹（= 追加新修订版）。
3. 对档案「探针」四层全过（AUTH/ToolCall/SCHEMA/STREAM）。
4. 「切换配对」：重新认证 + `expected_pair_epoch` + 幂等键（CAS 三闸）。
5. 观察一个运行窗口（决策链路、用量页 token 正常结算）。
6. 供应商侧**废止旧 Key**。
7. 验收点：审计流有 `PAIR_SWITCH`；旧修订版 `superseded` 不可再激活；旧 Key 废止后探针不再可能通过。

## 3. 操作手册：币安 Key 轮换（🔴 待凭据；decisions/01 同门）

1. 币安侧创建**新 API Key**：勾选合约交易、**禁提现**、**绑 IP 白名单**（与 10 §「密钥泄露 极高影响」的既定缓解一致）。
2. `config.json` 的 `exchanges.binance` 段替换 `apiKey`/`apiSecret`（文件权限 600；**这是配置行为不是 RPC 写面**——与通知通道同一纪律）。
3. 重启 bot；健康页 `LLMReconcile` 手动对账一轮（新 Key 真的能读到账户/仓位）。
4. 观察窗口后，币安侧**废止旧 Key**。
5. 🔴 执行前提：真实币安测试网/主网凭据（`taskProgress/decisions/01-testnet-credentials.md`）——本项与 M6-03/04/05 同门等待。

## 4. Vault 目录轮换（设计边界，如实记录）

- 本实现**没有包裹钥匙对的原地重铸 API**：每个密钥独立 DEK（M1-01），"轮换 vault 自身" = **新目录 + 重新录入所有密钥 + 逐个档案追加修订版**；旧目录**只读留存**到审计窗口结束。
- 之所以不做原地重铸：原地重铸是一次能弄丢全部历史的操作，收益（包裹钥匙对泄露是灾难场景才需要）与风险不成比例；留作未来显式设计项（本记录即登记）。

## 5. 结论

- 模型 Key 侧：✅ 链路机器验证（7 步断言全过，含泄密机器证明与 superseded 不可复活）。
- 币安 Key 侧：🔴 手册已写全，待真实凭据执行（执行后按 §3 逐条补记证据）。
- vault 边界：如实记录「无原地重铸」的设计取舍与替代流程。
