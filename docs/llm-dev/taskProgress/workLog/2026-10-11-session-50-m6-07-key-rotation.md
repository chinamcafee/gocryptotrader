# 2026-10-11 session-50（同日续段）：M6-07 密钥轮换演练（模型 Key 机器验证 + 币安 Key 手册）

## 会话目标

补齐 M6-07：「轮换币安 Key 和模型 Key，验证 vault 和配置加密流程」。模型 Key 侧做成机器可验证的演练；币安 Key 侧（需真实凭据）写成可执行手册并如实标注 🔴。

## 完成的子任务

- `M6-07` 密钥轮换演练

## 具体做了什么

1. `engine/llm_key_rotation_drill_test.go`（新）——`TestM6KeyRotationDrill`（真 vault 目录 + 真 sqlite 库 + 生产管理路径），7 步断言：
   - vault 追加新密钥（`secrets.Init` 重开**幂等**——轮换不重铸包裹钥匙对）；
   - `AddProviderRevision` 追加新修订版（`RevisionNo=2`，只携带**新引用 + 新指纹**）；
   - supersede 舞蹈：**LLM 指针清空 / readiness=INCOMPLETE / epoch++**，Jev 侧指针不动（只动被换一侧）；
   - **superseded 永不可复活**（旧修订版走 `ActivateModelPair` → `ErrRevisionNotLive`）；
   - **轮换是追加不是销毁**（旧/新密钥都可 `Reveal`，明文分别 == 原值——审计回读）；
   - 新修订版 + Jev 各落探针 → CAS 重新激活（epoch 2→3、READY、`pair_switch_commands` From/To/Operator 留痕）；
   - **泄密机器证明**：本轮触及的修订版/配对/切换命令渲染文本不含任何明文 Key；指纹在。
2. `taskProgress/acceptance/M6-07-key-rotation-drill.md`（新）——演练记录：
   - 自动化部分（测试名/命令/结果 + 7 步断言逐项「为什么值钱」表）；
   - **模型 Key 生产手册**（供应商建新 Key→vault 录入→追加修订版→四层探针→切换配对三闸→观察窗口→废止旧 Key）；
   - **币安 Key 手册**（新 Key 禁提现绑 IP → `config.json` 600 替换 + 重启 → `LLMReconcile` 对账验证 → 废止旧 Key；🔴 待凭据，与 M6-03/04/05 同门）；
   - **vault 边界如实记录**：无包裹钥匙对原地重铸 API（每密钥独立 DEK）——"轮换 vault"= 新目录 + 重录 + 追加修订版，旧目录只读留存；不做原地重铸是收益/风险取舍，登记为未来显式设计项。

## 验证证据

- `go test ./engine/ -count 1 -run 'TestM6KeyRotationDrill'` → **ok**（2.503s）。
- （过程中修正一处字段名：`PairSwitchCommand.Operator` 而非 `Actor`。）

## 遇到的坑与解决

1. `secrets.Ref` 的读取要 `secrets.ParseRef(字符串)` 再 `vault.Reveal(ref)`——ref 不是裸串。
2. `PairSwitchCommand` 的操作者字段叫 `Operator`——编译错误驱动修正。

## 未完成 / 下一步建议

- **M6-08 备份恢复演练**（vault + sqlite 数据库 + config——"从备份完整恢复到另一台机器"；本地可离线演练，建议下一步）。
- `M2-13` 末项（上游账户/下架事件生产者）、SHADOW `RiskFacts` 生产装配。
- 凭据门：M6-03/04/05、M1-15（decisions/01）。

## 环境状态快照

- 无新增长跑进程；mock 9443 / 代理 8788（allowWrites:false）/ vite 5173 在位；真实 bot 9053 未启动。
