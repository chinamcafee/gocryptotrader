# 02. 自动执行的开仓/平仓单用哪种订单类型（LIMIT 还是 MARKET）

**阻塞的任务**：`TASKS.md` M2-13 残余的最后一格——`ShadowRunRequest.RiskFacts` 闭包装配（`RiskOrderFacts.OrderType/Price/NotionalUSD` 的定型）；以及其后的 PAPER/LIVE 决策→订单桥（`BuildIntentPlan` 的 `OrderType` 入参来源）。

**为什么必须你决定**：订单类型决定真实成交路径与成本（挂单等 vs 立即吃单）、以及被拒/滑点画像——是「钱怎么花」的执行语义。设计文档两处都保留两种可能：07 §5.4 价格表 `LIMIT`/`MARKET` 两行并存、§7.1 状态机只画了 `→ RESTING（挂单中，LIMIT）` 一条路径；24 项已定案（OPEN-QUESTIONS）里没有这一条。模型自行选默认 = 替你把钱放进一条你没选过的路径——这正是「不可半套放行」要防的事。

**背景**：§5.4 价格表：`LIMIT` 价 = `candidate.suggestedEntry`（tick 取整 + 盘口区间校验）；`MARKET` 不传价（币安市价单不收 price）。Q12 已定：`TimeInForce`=GTC（wrapper 硬编码，不改），意图 10s 未成交由 LLMManager 主动撤单——这套撤单机制本身就是为「挂单等待」设计的。保护性止损=STOP_MARKET，不受本决策影响。合成层已强制开仓候选 `suggestedEntry` 非空——LIMIT 在开仓侧永远可走；§5.4 的 tick 取整/区间校验件（`limitPrice`）与滑点估算件（`estimatedSlippageBPS`）均已就位，三种选项都不缺零件。

**可选项**：

- **A. 全部 LIMIT**（开仓/平仓都挂单等）：价格可控、成本最低；离场可能等不到（10s 撤单后本轮不成交，靠下一轮）。
- **B. 开仓 LIMIT、主动平仓 MARKET**（推荐）：开仓守价（与 §7.1 状态机现状一致、10s 撤单机制即为其设计）；平仓要确定性（低频策略下离场确定性 > 几个 bps 的滑点，且滑点有 `maxSlippageBps` 闸兜底）。
- **C. 全部 MARKET**：成交确定性最高；每次进出都吃盘口价差，滑点估算闸是唯一价格保护。

**我的建议**：**B**。开仓用 LIMIT 守住审批价语义（suggestedEntry 是分析层给出的入场计划价，市价成交会让实际入场大幅偏离审批时的判断）；主动平仓用 MARKET 保离场确定性；止损已独立为 STOP_MARKET。

**如果不决策的后果**：`RiskFacts` 闭包不能给 `RiskOrderFacts` 定型（侦察结论第 6 条明令不许编造这三个字段）——SHADOW 决策停在 awaiting-verdict（文档化合法形态，但不出风控 eval 结论、不产 sizing 记录），PAPER/LIVE 桥无法开工。**其余一切不阻塞**（先决四件与全部组映射已落并验证闭环，W 组前端、M6 纯本地项均已交付）。

---

**已拍板（2026-10-13）：采纳 B** —— 开仓 LIMIT（`suggestedEntry` 经 `limitPriceFor` tick 取整 + 盘口区间校验，与意图落库同一实现）、主动平仓 MARKET。落地：`engine/llm_shadow_risk_material.go`（订单事实）+ `engine/llm_execution_bridge.go`（桥）。**平仓数量补充裁定（session-53 续，组装期决定）**：候选契约不含减仓比例、07 §7 未定义主动平仓数量——「REDUCE=减少或关闭现有敞口」当下唯一可执行含义=**全平**（数量与名义额取风控阶段评估过的同一仓位事实 `activeExitSizing`）；部分平仓待退出政策长成时改该一处。见 workLog session-53。
