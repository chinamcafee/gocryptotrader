# 03. 保证金模式假设（ExpectedMarginType）在哪声明

**阻塞的任务**：`TASKS.md` M2-13 残余的最后一格——`ShadowRunRequest.RiskFacts` 闭包装配的 C 组材料（`RiskAccountFactsInput.ExpectedMarginType`——映射层要求与 `MarginType` 成对非空，`engine/llm_risk_facts.go:99-101`）。

**为什么必须你决定**：检查 `RISK_MARGIN_TYPE_UNEXPECTED` 的定义是「实际保证金模式与**审批假设**一致，不一致→拒」（07 §5.2 C 表）——但**审批表里没有保证金模式字段**（`llmstore.StrategyApproval` 17 列已核，冻结信封 `candidate.RiskEnvelope` 亦无），「假设」这枚事实在设计的数据模型中无处安放。实际侧已可读（`PositionSummary.MarginType`，尾二十九/三十落地）；**假设侧在哪声明是契约级决定**——它决定「账户被偷偷切成逐仓」这件事会不会被系统发现。模型自行替你声明一个假设 = 替你把风控姿态定死，正是「不可半套放行」要防的。

**背景**：检查自身对缺席是宽容的（`ExpectedMarginType==""` → 不触发，`llm_risk.go:677`），但映射层成对非空（我此前按「不可半套放行」取严）——两侧不一致说明「假设从哪来」从未被设计闭环。设计文档只有一句「与审批假设一致」，无字段、无默认值、无配置项（24 项已定案里没有）。现状：SHADOW 全链其余材料已就绪，卡在 `ExpectedMarginType` 一个字段。

**可选项**：

- **A. 配置项 `llm.risk.expectedMarginType`（建议：默认 `CROSS`，可改）**：新增 v19 配置迁移 + `CheckLLMConfig` 填充默认 + 配置样例；单字段、operator 可控、立即可保护「账户被切成逐仓」的漂移；代价=配置面扩一格（与 15 开关目录同族先例）。
- **B. 审批时录入（schema + 前端）**：最贴设计原文「**审批**假设」——approval 表加列 + proto + 审批 UI；保护语义最正（每个策略版本宣布自己的假设）；代价=一张表+proto+前端三处，属较大件。
- **C. 暂不声明（检查保持不触发）**：把映射放宽到与检查同语义（空=无假设=不过闸）；零改动、无发明；代价=该检查在生产中长期关闭（账户模式漂移只被「逐仓下清算风险」间接暴露）。

**我的建议**：**A**。小额验证档先让这枚检查活着（默认 CROSS 是币安 USDT-M 的主流配置，与小额档语义相称），B 作为后续正式化路径记录在案；C 会让一条已实现的保护长期空转。

**如果不决策的后果**：闭包装配无法齐备 C 组材料（映射要求非空）——SHADOW 决策停在 awaiting-verdict，PAPER/LIVE 桥无法开工。**其余一切不受阻**（先决四件、双模式出口、两组装件、清单其余栏均已就绪）。

---

**已拍板（2026-10-13）：采纳 A** —— 配置项 `llm.risk.expectedMarginType` 默认 `CROSS` + v19 迁移 + `CheckLLMConfig` 填充 + 样例。**落地注**：operator 词汇经 `margin.StringToMarginType` 一次译为场地词汇（`margin.Type.String()`：cross→`"multi"`、isolated→`"isolated"`）再进 C 组映射——检查两侧裸比较自此同拼写（否则 cross≠multi 会让每个开仓被 RISK_MARGIN_TYPE_UNEXPECTED 拒）。见 workLog session-53。
