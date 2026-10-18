# 总体进度报告

**最后更新**：2026-10-12（session-52：组合冒烟链预建落地，不计分续段）
**总完成度**：**98.6%**（口径：权重表之和 107——总完成度 = **Σ（组得分）÷ 107**；`M6-08` 备份恢复演练落地（M6 7/11 → 8/11），Σ得分 105.50 → **105.50 ÷ 107 = 98.6%**。**W 组（W0-W8）已全部完成**。历史 series 曾按「得分 ÷ 100」拼写（读数偏高 ~7%），换算除 1.07。逐项账目见 §3 session-42~51）

---

## 1. 一句话状态

**前端交易台可用**（真实币安 USDT-M 数据，K 线 + 指标 + 盘口 1000 档 + 账户仓位 + 滑点模拟 + 派生事实）；
**后端 **M0 14/14 组完成**、M1 14/15、**M2 16/16 组完成**、M3 9/9 完成、M4 16/16 完成、M5 18/18 完成、**M6 8/11（M6-01 验收记录 + M6-02 失效矩阵演练 + M6-10 `make check` 全绿 + M6-11 两个基线策略对照跑通 + M6-06 kill -9 崩溃演练 + M6-09 文档定稿 + M6-07 密钥轮换演练 + M6-08 备份恢复演练）**；前端 **W 组（W0-W8）全部完成**（8/8 路由、交易台/对话/策略/模型配对/决策审计/健康治理/合约覆盖全链 live，含 halt/对账/用量/通知治理面）：`llm/` **十一包**全绿（**274 个顶层测试函数 / 404 条 PASS（含子测试）**——口径从本次起双列，此前"351"未注明含子测试）+ `engine/` **373 个 LLM 相关测试**（3 指标跨层 + 12 组装器 + 19 轮询执行器 + 9 扫描器 + 10 配置映射/生命周期 + 5 重载→扫描接缝 + 3 取数共享 + 6 Provider 管理 + 6 启用配对 + 6 预算预约 + 8 工具循环 + 8 工具执行器 + 6 K 线缓存 + 5 转录留存 + 3 缓存接线 + 7 对话流水线 + 2 证据卡片 + 6 会话服务 + 3 会话 RPC + 3 安全边界 + **4 流式 + 2 流式 RPC + 7 条款提取 + 7 版本冻结 + 3 版本树 + 4 市场授权 + 8 限时审批 + 4 触发调度 + 4 前置门禁 + 7 分析流水线 + 6 Jev 评估 + 15 决策合成 + 17 硬风控引擎 + 9 数量重算 + 8 审计链 + 4 切换级联 + 5 SHADOW 组装 + 4 策略/决策 RPC + 8 订单意图 + 5 提交映射 + 10 执行分流/状态机 + 5 保护性退出 + 4 退出生命周期 + 4 对账 + 5 日界基线 + 5 紧急停机 + 4 事件通知 + 3 模式切换 + 3 网络一致性 + 4 可观测性 + 3 崩溃恢复 + 5 风控开关 + 2 REVIEW 过期 + 9 准入检查（M6-01）+ 3 审批到期提醒（M6-02）+ 2 崩溃演练（M6-06）+ 1 密钥轮换演练（M6-07）+ 2 备份恢复演练（M6-08）+ 2 下架监视 + 4 触发消费 + 1 账户事件 seam + 1 数量链走价 + 3 IntentPlan 生产者 + 2 账户事实生产者 + 3 风控事实映射 + 1 授权映射 + 2 市场映射 + 2 系统映射 + 2 频率映射 + 1 合约规格生产者 + 1 排名生产者 + 1 滑点估算件 + 2 premiumIndex 事实件 + 1 账户态事实件 + 2 闭包组装配件（SizingInput/RiskInput）+ 1 时钟漂移件（M2-13 收官）+ 1 组合冒烟链（session-52）+ 3 自动响应链拼写归一（session-52 同段尾三）+ 1 触发→运行融合钉子（session-52 同段尾八）**）+ `cmd/gctcli` **3 个 REPL 测试**；**24 张 LLM 表 + 21 个不可变触发器**已落地、**两份 sqlboiler 模型已生成**（pg + sqlite3，含全部 24 张 `llm_*`）、**`database/repository/llm` 仓储层 8 域就位**（**18 个端到端测试**：真库迁移 + 触发器对抗验证 + 加密会话往返 + **授权/审批活体读取 + 订单意图滑窗计数 + 最近开仓时刻 + 状态计数**）；**前端 8 个路由，含「合约覆盖」与「对话」页**（对话页浏览器实测：流式增量渲染 + **Markdown（h2/列表/GFM 表格）** + **Sources 证据展开** + **客户端内存草稿** + 回放；M3-09 含代理流式直通与 SSE 分帧解析器 6 项自查）；**W8 质量与验收 6/6 完成**——三连校验、交易台实测、降级实测、390px/1440px 响应式实测（并修掉 5 个路由的横向溢出）、密钥自查、README。

**`llm/` 的离线部分已经做满**，数据库模式也已就位，**第一层数据面（轮询→重载→扫描→触发队列）已闭环**。接下来要写**读写这些表的代码**（M0-06/M0-07），决策流水线（M3/M4）另开。

**下一个模型建议**（M6 已完成 8/11；M6-10 的 lint 战役已把「机器化改签名的整套配方」写进 workLog session-25 §7，接 `hugeParam` 类改动前先读它）：

1. **M6 纯本地项已全部完成**（M6-01/02/06/07/08/09/10/11 = 8/11）；剩余 `M6-03/04/05` 全部依赖生产装配后的运行数据（见下）
2. **依赖运行面（生产装配后补记）**：`M6-03` 人工标注评估集（依赖 SHADOW 数据）、`M6-04` 纸面表现报告、`M6-05` 压力与限频 24h；**`M6-07` 密钥轮换 / `M6-08` 备份恢复属可离线演练**（vault 与 sqlite 都是本地文件——与 `M6-06` 崩溃演练同法，建议直接做，不等凭据）
3. **生产装配已收口**（session-27 四段：模型无关三件套 + client factory + chat 走廊 + ShadowRunner + trigger/preview 换血）——**14 个 LLM RPC 全部为活 + providers 族 7 个（M1-14）**；剩装配级小项：SHADOW 硬风控事实（`RiskFacts`）、`M2-13` 末项（上游账户/下架事件生产者）——然后 M6-03/04/05 在测试网凭据到位后可出数据
4. 并行余项（session-51 后；只剩最后几件；**+ 已补：`LLMSetRunMode` 端点（08 §3.6 全家唯一缺）——session-52 同段尾十全栈落地（proto/handler/持久化/测试），剩余面=gctcli 已补（同段尾十一 `llm mode` 命令）；UI 写侧入口也已落（同段尾十二：HealthPage「运行模式」卡，浏览器实读过）；剩余验证面已全部结清：mock 读链探针=误报（mock 为 https，http 直探空响应；经代理读链 200 实证）+ 写流浏览器实测已通（session-52 尾十三：SHADOW↔PAPER 双向、toast+读回双证、写闸恢复 false）；**`gct-proxy.mjs` v1/ 归一化 bug 已修**（同段尾十三）**）：**收口指引（2026-10-13 更新：02=B / 03=A 已拍板并落地，③ 步完成）**——**剩余 = 凭据门（`decisions/01` 已选方案 1，待凭据经 vault 录入 → 解锁 M6-03/04/05 + M1-15）+ 两件以契约为准的登记**：RESTING 生命周期重钉（M5-05 喂送落地时）、`JevManifestState` 未绑形态（留 NULL，不发明词汇）。**session-53 续已落地**：REDUCE 主动平仓执行（全平语义）+ `RateLimitDegraded` 真读面（共享包降级地平线）——见 M2-13 残余更新四十九。**任一拍板后的动手顺序**：① 读 workLog session-51 §侦察结论**第 10/11 条**（接线清单三栏 + **装配期取值表**——已核栏照抄、取值表照填）**+ session-52 同段尾二~八勘误**（System 组读面逐字段点名 / 新增未钉两件 / **市场号拼写矩阵与 F 组裸相等警报** / **闭包查表键：排名=`llmMarketID` 小写三段式** / 同段尾七·八为已修缝与融合钉子——闭包开工前先读）；② 按**组合冒烟测试蓝图（尾三十八）**建链测试（入参字段与夹具配方已钉）【**已预建**：session-52 `TestShadowRiskInputChainComposes` 全链绿——拍板后从 ③ 起步】；③ 合成器 + 闭包 + 桥【**已完成（session-53 尾一~五）**：`buildShadowRiskMaterial` 七组全材料 + 订单事实 02=B + 保证金词汇翻译 + F 组归一 + `SystemOneWithManifest` 清单生产化（被掩盖的生产杀手级缺口）+ `executeApprovedRun` 桥与消费者驱动；engine LLM 测试 373→385】；④ 例行电池（lint/misc/整套/markdownlint）+ 台账同步【本段进行中】。**环境警示**：9053 有遗留真 bot（pid 18809）在跑——未经用户确认勿 kill；5173/8788/9443 dev 栈在位，「连接失败/getinfo 404」为 mock 栈预期。以下为历史长链（按时间序，供追溯）：**PAPER/LIVE 决策→订单桥**（决策行→intent 创建→`OrderDispatcher` 无生产调用方——**这是生产接线的最大剩余**；账户事件 seam 已就位、随桥一并接线；**桥第一件 `BuildIntentPlan` 已落**——session-51 尾四；**`RiskFacts` 链头（M5-08 账户事实生产者 `buildSnapshotAccountFacts`）、B/C 组映射（`engine/llm_risk_facts.go`）、A 组授权、D 组市场、G 组系统、E 组频率（含窗口计数仓库查询 `CountOrderIntentsInWindow`）悉数落**——session-51 尾五~尾十二；**仅余：桥的接线件（开关目录读面 `RiskSwitchService.Current()` 已备——装配侧一行消费）**）；SHADOW `RiskFacts` 生产装配（桥的前置：无它决策停在 awaiting-verdict；**数量链 `Walk` 装配件已落**；快照已带 Ticker/OrderBook/Funding/Account 四组事实可直映射、账户侧装配属 M5-08——分解清单+**装配前哨更新（尾十三：Q9 两龄常量已落=60s+钉子、mark/index 端点 `UGetMarkPrice`；尾十四：`QuoteVolumeRank` 取数件已落——装配期剩一验：USDT-M ticker 是否实际进引擎缓存；尾十五：滑点估算件已落——装配先决 ③ 兑现；尾十六侦察：**Funding 组无生产者实锤**（构建器零 funding 引用——「已带 Funding」为误记）——funding+mark/index 同源（binance premiumIndex `UGetMarkPrice` 一次给全）**合并为新先决 ④**——装配前四件齐才算无缺口；尾十七：premiumIndex 件已落（先决 ④ 兑现——funding+mark/index 一次读 + 快照接缝 `SnapshotRequest.Funding` 标 EXCHANGE_REST）；尾十八：排名数据面验证闭合（`Rotator.Step` 每周期批量 `UpdateTickers` 灌缓存）——**先决四件全部验证闭环**；尾十九：账户态事实件已落（日界基线+UNKNOWN 计数——`buildRiskAccountStateFacts`，`ExternalFlowsUSD` 归 `DailyLoss` 既有公式））**；尾二十：**决策门（02）**——自动执行单型（LIMIT/MARKET）设计未定案、闭包 `RiskOrderFacts` 定型与桥的单型来源压其上，已入 `decisions/02-auto-execution-order-type.md`（建议 B：开仓 LIMIT / 主动平仓 MARKET）——**其余一切不受阻，等用户拍板**；尾二十一：**闭包装配接线清单已固化**（侦察第 11 条——**尾二十二核毕**：grants=`ListGrants`、reconciled=`AccountReconciled` 直接接线、C 组保证金模式装配期定源；**尾二十四**：`buildShadowSizingInput` 元组第二件先行落地、不依赖 02；**尾二十五**：`buildShadowRiskInput` 元组第一件可决部分落地（十三字段直取、`Order` 参数化受 02 门）——**闭包两件即插化，拍板 02 后仅剩 raise 材料 + Order 构造**；**尾二十六：premiumIndex 读面已进 `Assemble`**（Funding 上快照、mark/index 存 `f.premium`——非 02 门的最后接线缺口已清）；**尾二十七：冷却数据侧 `LastEntryIntentAt` 已落**（opens-only、无行=从未开仓）；**尾二十九：C 组场地材料地形钉死**（`PositionSummary.MarginType` 同读可直取／`collateral.Mode`≠对冲模式／dualSide 无读面二选一／`ExpectedMarginType` 补配置或显式输入——装配会话照此收口；**尾二十九续：账户模式出口已开**（`venueAccountModes` 随账户生产者返回、闭包存 `f.accountModes`——`MarginType` 同读直取兑现）；**尾三十：对冲模式读面已落**（`UGetDualSidePosition`+`readVenueHedgeMode`——HedgeMode 二选一取 a、真实事实胜过静默假设；C 组模式双出口就位）；**尾三十一：决策门（03）立档**（`ExpectedMarginType` 的「审批假设」设计数据模型无处安放——审批表与冻结信封均无该字段；`decisions/03` 三选项、建议 A：配置默认 CROSS）；**尾三十二：未对账计数件已落**（`CountOrderIntentsByStatus` + 账户态切片第三孔 `UnreconciledPartials`——**C 组三出口就位**，仅 `ExpectedMarginType`（03 门）与 `TargetAccountID` 留装配）；**尾三十三：装配期取值表已固化**（§侦察结论第 11 条末栏——显式输入逐项建议取值与理由，照表即装）；**尾三十四：时钟漂移读面已落**（`readClockDriftMs`——venue `UServerTime` → 幅度毫秒值；G 组补读面仅余限频状态）；**尾三十五：dualSide venue 侧测试已补**（尾三十一「无独立测试」口子闭合）；**尾三十七：闭包合成器设计归属已判**（材料 API 约 28 字段、与 02/03 落地相连——拍板会话一次设计，测试夹具路线已明）；**尾三十八：组合冒烟测试蓝图已钉**（七映射+两组装件一条链过 `Evaluate`——断言 Checks 非空且无 `RiskInputInvalid`；入参要点全钉））**见 workLog session-51 §RiskFacts 侦察结论——**装配开工前先读第 10/11 条**）；**M6-03/04/05 + M1-15 凭据门**（decisions/01）；**尾三十九（2026-10-12 session-52）**：组合冒烟链**已预建**（`TestShadowRiskInputChainComposes` 全链绿——七映射+两组装件一条链过 `Evaluate`、断言 `Checks` 非空且无 `RiskInputInvalid`；02 门控 Order 三字段=字面量 LIMIT，拍板后该链不动）；**尾四十一（同段尾二）**：System/F 组读面逐字段点名 + F 组拼写警报；**尾四十二（同段尾三）**：自动响应链拼写断点实锤并修复——`runMarketID` 双侧归一（触发路由+Build、下架级联），3 测试钉住；**尾四十三（同段尾四）**：工具链 scope 拼写断点已修复——`buildScopeToolRunner`（chat/决策共享）归一，chat 走廊测试实参改生产拼写+负例断言钉住；**尾四十四（同段尾五）**：候选契约上下文缺口 + 操作员入口拼写修复——分析消息补 `Market <req.MarketID>`（三项 ID 逐字复制自此有据）、前端四处 `usdtperpetual` 清零；grant/halt 零校验登记拍板；**尾四十六（同段尾七）**：pre-gate 策略门状态词修复（门认 `FROZEN`——生产运行态；此前每次运行在第二道门被拒）；**尾四十七（同段尾八）**：触发→真实运行请求融合钉子（五缝合链断言生产口径）；**尾四十八（session-53，2026-10-13）**：02=B/03=A 拍板落地——合成器 + 闭包 + PAPER/LIVE 桥 + Jev 输入清单生产化 + v19 迁移（见 M2-13 残余更新四十八与 workLog session-53；engine LLM 测试 373→385）；**尾四十九（session-53 续）**：REDUCE 主动平仓执行（全平语义）+ `RateLimitDegraded` 真读面（共享包降级地平线；测试 385→387 + request 包 2）；**session-54（2026-10-15）新增功能线立项**（`docs/llm-dev/13-合约筛选与批量决策工作台.md` + `OPEN-QUESTIONS-新增功能-待确认.md`；**不引入 Mastra 类外部 Agent 框架**——既有 Go 工具循环自任子 Agent）——增量一~九已落地（ticker 修复 + screener 注册表 + `screenMarkets` 工具/执行器 + 指标目录/Run RPC + 批量作业与两 RPC + **活跃审批列表 RPC（A5 完整版：GUI 自动预选）** + 持仓复核运行 + webUI 两页浏览器实测（含两条单市场路径与 A5 预选）+ **对话走廊×真筛选执行器集成测试**；N4 迁移暂缓；engine LLM 测试 387→397；**续二（持仓数据面收口）：持仓复核页真实形状修复（RTK 响应 `{positions:[...]}` 解包 + 方向/数量改用 `currentDirection`/`currentSize`——`total` 在 `gctrpc.FuturePosition` 不存在、真实 bot 非空持仓下 `.map` 会崩）+ mock 补 `getallmanagedpositions`——需求③离线全链浏览器实测（两行持仓多/空渲染 → 全量复核 → 2/2 DONE）；代理写闸开窗后已恢复 false（POST 501、GET 200 双实证）**；**续三：筛选页自然语言引导入口（「对话」链接+示例话术——需求①可发现性）+ A8 登记（决策方向选择权，默认=分析链产出）+ mock 批量↔决策列表/血缘联动（浏览器实测，写闸全程未开）**；**续五：13 号文 §3.3 两字段补落（自开仓资金费累计 + 保护性止损现状进复核消息块；`LastEntryIntentAt` 带 intent id；engine 测试 397→400）**；**续六：扩展演练——新增 `macdhist`（注册表 11→12，全链路零改动自动继承；screener 包 6→7；mock 补齐至 12 指标，浏览器徽章自动 11→12）**；**续九：`screenMarkets` 证据预算裁剪（12KB 硬限边界——工具路径按尾部诚实裁剪+notes 标注，GUI 不受限；engine 测试 400→401）**；**续十：批量起批审计（`BATCH_DECISION_STARTED`，fail-closed；mock 审计流同步；engine 测试 401→402）**；**追加四原则立项（2026-10-15）：全量扫描与覆盖联动——设计=`15-全量扫描与覆盖联动.md`（QPS 预算内全量 available∪启用、`LLMSetMarketTracked` 联动端点、覆盖页手动开关、启用优先）；口径 A9/A10 已登记；实施=TASKS N10-N13**；**同日晚：真栈拉起（新版引擎+webUI 全栈连真 bot，LLM/Jev 待用户配密钥）+ W4-02「新建档案」入口补缺（此前 UI 无从创建首个档案）**＋**vault 移除改造（用户拍板：删密钥保管、Provider 直填明文 API Key）全链完成并重启上线**——零迁移、明文不回传、前端/引擎已切换；写闸按用户要求保持开启；**首次激活入口修复**：「切换配对」按钮曾在 found=false（从未激活）时被禁用锁死——后端本就支持 expected=0 首插，改按钮条件 + 首发预填两侧最新 live 修订版/epoch=0，浏览器实测（session-54 §二十）；**＋切配/探针/编辑三 mutation 补 LlmStatus 失效**（激活成功后状态卡不自动刷新——点探针实测失效链：POST 后 status/activepair/providers 自动重取；session-54 §二十一；**用户配对已激活成功：epoch 1 / READY**）；**＋对话走廊运行期重装**（启动装配一次性的缺口：激活前启动则 chat 永久 500——`reassembleLLMRuntime` 挂上配对激活 handler + 触发器循环每 tick 重建 consumer + 三 service 指针加锁；实弹两轮对话往返 + 侧栏计数就地刷新；session-54 §二十二）；**＋对话配置能力（N15）**：`proposeAction` 惰性提案 + 可编辑确认卡片 + 开仓/复核/覆盖/取消四类接线 + 两端取消批量作业；`LLMSetMarketTracked` + 覆盖页行内开关（N11/N12 覆盖页部分落地，N10/N13 仍待）；实弹对话确认启用 DOGE（审计 + enabledPairs 5→6）+ 手动开关 0G 往返；engine 整套 96.312s 绿（session-54 §二十三）；**＋对话筛选回复不可读的连环修复**（DSML 文本工具调用解析 + 气泡清理 + screener 缓存装配 + coverage 启用与冷启动预热 + 对话输出预算/轮次 + include 上限 12；实弹返回完整指标表与 9 张证据卡；engine 109.736s 绿；session-54 §二十四）；**＋全量扫描（N10/N13）与实时进度卡**：589 个合约（available∪enabled）约 90 秒扫完（批量 ticker + 按需 K 线 6 并发/120ms、缓存回灌、scope 同步），对话显示「已扫描 X/589」进度卡；实弹前三 AGT/Q/EPIC 含 RSI/MACD/ATR/布林（session-54 §二十五）；**＋筛选结果 Jev 开盘机会评估**（追问 4-8h 机会 → 两段 Jev：机会→方向，预算/作用域/进度全生效；表格市场列去交易所前缀；实弹三市场 NO_TRADE（session-54 §二十六））；详见 TASKS「新增功能线」与 workLog session-54）

**proto 工具链已校准（2026-10-11）**：`buf generate` 幂等（连跑同哈希）；追加端点=纯增量 diff——`M1-14`/`W7-07` 的 proto 前置不再阻塞（仅剩一次性对齐 diff 待单独提交，见 `TASKS.md` §0.1）。

---

## 2. 分组完成度

| 组 | 权重 | 已完成 | 得分 | 说明 |
| --- | --- | --- | --- | --- |
| W0 脚手架与基础设施 | 3% | **6/6** | 3.00% | ✅ |
| W1 API 客户端层 | 6% | **8/8** | **6.00%** | ✅ 组完成：**strategy 工作区 13 个端点包装补齐（W1-07 收口）** |
| W2 布局与导航 | 4% | **4/4** | 4.00% | ✅ |
| W3 交易台 Terminal | 12% | **8/8** | 12.00% | ✅ |
| W4 模型与配对 Providers | 8% | **6/6** | **8.00%** | **组完成**：+ 档案列表（W4-01）+ 编辑表单（W4-02）+ 分层探针（W4-03）+ 切换配对（W4-05）+ **密钥保管 vault（W4-06）** + **活动配对横幅（W4-04，全局横幅，名实相符）** |
| W5 策略工作区 Strategies | 12% | **8/8** | **12.00%** | **组完成**：+ 对话工作区（W5-01）+ 证据卡片核对（W5-02）+ 提取结果展示（W5-03）+ 澄清问题交互（W5-04）+ 版本树与 diff（W5-05）+ 冻结与限时审批（W5-06）+ **市场授权（W5-07：三 RPC + 级联撤销实测）** + 启停暂停撤销恢复（W5-08） |
| W6 决策与审计 Decisions | 10% | **6/6** | **10.00%** | **组完成**：+ 决策列表（W6-01）+ 血缘可视化（W6-02）+ 风控证据表（W6-03）+ 手动触发/预览（W6-04）+ 审计事件流（W6-05）+ **REVIEW 过期展示（W6-06：窗口随配置下发 + 「已过期·未执行」派生）** |
| W7 健康与治理 Health | 7% | **7/7** | **7.00%** | **组完成**：+ 合约覆盖数据面与计划面（W7-04）+ 风险开关 live 面板（W7-05）+ 覆盖运行面（W7-07）+ halt 三级控制写侧（W7-01）+ 对账手动触发（W7-02）+ 用量读面（W7-03）+ **通知通道（W7-06：就绪度面 + 测试推送；凭据只报布尔）** —— **W 组（W0-W8）全部完成** |
| W8 质量与验收 | 5% | **6/6** | **5.00%** | ✅ 三连校验 · 交易台实测 · 降级实测 · 索引 390/1440px 实测（修掉 5 个路由溢出）· 密钥自查 · README |
| M0 基础与契约 | 5% | **14/14** | **5.00%** | **M0 组完成**：+ PostgreSQL provisioning（M0-14）+ sqlboiler 模型生成（M0-06）+ 仓储层骨架（M0-07）+ **配置迁移 v18（M0-09）** |
| **M1 模型接入** | 6% | **14/15** | **5.60%** | + 三个生产提示词（M1-13）+ Provider 管理面（M1-09）+ 启用配对 CAS（M1-10）+ 预算预约闸（M1-11）+ 工具调用循环（M1-12）+ **providers 命令族（M1-14：7 RPC + gctcli + 分层探针生产路径，W4 组解锁）**；余 M1-15（真实供应商冒烟，凭据门） |
| M2 数据与工具 | 6% | **16/16** | **6.00%** | **组完成**：+ 整点重载（M2-16）+ 触发器扫描（M2-13）+ K 线取数共享（M2-01）+ 工具契约（M2-07）+ 工具执行器与四道闸接线（M2-07b）+ K 线缓存（M2-02）+ 数据质量应用（M2-05）+ 转录留存（M2-09）+ 同源事实传递（M2-10）+ 回填批次预估（M2-11）+ **四道闸核对补勾（M2-08：执行器 `evidence()` 每产出即跑 Validate + CheckEvidence）** |
| M3 对话能力 | 4% | **9/9** | **4.00%** | ✅ 组完成：M3-01~M3-09（会话加密/上下文/流水线/卡片/RPC/REPL/流式/安全边界/Web 消费流式） |
| M4 决策链路 | 8% | **16/16** | **8.00%** | **M4 组完成**：+ **strategies/decisions RPC + gctcli（M4-15）**——proto 16 RPC（buf 幂等 `52eb7d45`）、读写同源、trigger/preview fail-closed、A12/A13 复盘面 |
| M5 执行与治理 | 7% | **18/18** | **7.00%** | **M5 组完成**：+ **风控开关目录（18 行 + 三把锁）** + **REVIEW 自动过期（12h + 宽限窗幂等 + 不执行）** |
| M6 测试网验收 | 4% | **8/11** | **2.91%** | **M6 推进**：验收记录（A01–A14）+ 准入检查命令 `gctcli llm readiness`（十条门）+ 失效矩阵全 21 行演练（✅15/🟡3/🔴2/📋1；行 14 缺口同日修复）+ `M6-10` `make check` 全绿 + M6-11 固定规则基线 + M6-06 崩溃演练 + M6-09 文档定稿 + M6-07 密钥轮换演练 + **M6-08 备份恢复演练（三件套两跳进新进程；含两负例与泄密机器证明）** |
| **合计** | **107 分** | — | **98.6%** | **Σ得分 105.50 ÷ 107**（权重表之和=107，表头『100%』笔误——session-44 审计修正；旧 series 按 ÷100 拼写，读数除以 1.07 即得新口径） |

---

## 3. 本会话完成的任务摘要（session-25）

> **M6 开工**（11 §8 的第一个子任务）：验收记录 + 11 §8 要求的准入检查命令。

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M6-01` 14 项验收逐项演示记录 | `taskProgress/acceptance/M6-01-A01-A14-demo-records.md`（A01–A14 逐项：演示内容 / 离线证据 / 运行步骤 / 诚实状态）+ 准入命令三件套：`engine/llm_readiness.go`（`EvaluateReadiness` 十条门纯函数 + `LLMReadinessService` 事实接缝 + 9 测试）· RPC `LLMGetReadiness`（proto+handler 原子，buf 双跑同哈希 `3155e986…`）· `gctcli llm readiness`（0=READY / 2=NOT_READY / 未装配报错 fail-closed） | **状态诚实分级**：9 ✅ 离线已验证（每项列可复跑测试名）· 3 🟡 部分离线（A02 数值等价已钉死/对话链路待装配；A13 链路写入 ✅/真实成交待 A11；A14 序列 ✅/撤单适配器属装配）· 2 🔴 待运行面（A01 实时性、A11 测试网全链路——所缺前提与补记步骤已写进记录 §4）。**十条门**（报告顺序即契约）：INPUT/MODE/PAIR/PROBE/NETWORK/HALT/SWITCHES/RECONCILE/DAYSTART/COVERAGE/APPROVAL；SHADOW/PAPER 豁免 NETWORK 与 APPROVAL（无场地下单路径，M5-13 同一语义）；事实源错误上抛、空值各门 fail——「读不到事实」与「读到空事实」不许长得一样；门清单即 M6-02 失效矩阵的词汇表 |
| `M6-02` 20 项失效矩阵演练 | `taskProgress/acceptance/M6-02-failure-matrix-drills.md`（10 §7 全 21 行：触发/预期/证据/状态）+ 演练修复：`engine/llm_approval_expiry.go`（`ApprovalExpirySweeper`：`(now, now+30min]` 含边界、分钟 ceil、一次一提醒、出窗剪除、gauge；+ 3 测试） | ✅15 · 🟡3（#5/#8/#10 离线主响应过、升级器属装配）· 🔴2（#7 真限频、#18 事件生产者）· 📋1（#20 归 M6-07）；发现：①行 14 缺口同日修复②行 5「>300s→SOFT halt」升级器登记③行 18 下架事件生产者登记（与 M2-13 同源）；口径记录④DB 不可写时 halt 自写悖论=写链 fail-closed |
| `M6-10` `make check` 全绿（施工本身） | **零 lint 化**：669 条 findings → **0**。三战役：①杂项批（depguard import 互换、revive/testifylint/dogsled/prealloc/rowserrcheck/G115/…）②**hugeParam 值→指针大迁徙**——仓储 + 引擎 ~90 个签名（WaveA/WaveB），调用点由三代编译错误驱动修点器（`autofix_amp(2/3).py`：双向 argument/assignment/struct-literal、`…` 字面量、`vet:` 前缀、单字符 token 防护）自动完成；夹具统一约定「共用即返回指针」；接口同步指针化 7 个 ③收尾 21 条（trigger.Event 指针、`planPtr`/`candlePtr` → `new(x)`、`&*run`、unparam 瘦身、decisionInputHash 去恒 nil error） | `make lint` 退出码 0（**0 issues**）· `go build ./...` / `go vet ./...` 0 · `make misc_checks` All passed（CI 等价树：移出 `webUI/node_modules`+`dist`；另修 `webUI/README.md` 列表编号、`context_test.go` assert must→should、自叙 VS16）· `markdownlint` 0 error · `go test -race ./...`（同树 + 两个 engine 既有 flaky 带 -skip）：**除 live 网络类外全绿**——kucoin run A 失败/复跑通过（flaky 实锤）；okx×2 + backtester×2 常量差在 **HEAD 干净 worktree 基线同样复现**（51634 / 空表 / 51001），与本改动无关；`TestRootPathFromCWD` 系 `node_modules` 产物（同树通过）· engine 整套 -skip 133.3s 全绿 · `llm/...`+config+repo+cmd 全绿 |

**校验**：`go test ./engine/ -run 'TestApprovalExpirySweep' -count 1`（3 全过——窗口边界与 ceil/一次一提醒/出窗剪除/误用与错误上抛）· `go test ./engine/ -run 'TestEvaluateReadiness|TestReadinessService' -count 1`（9 全过——固定门序 + 逐门单点失败 + 服务层三态）· `go build ./...` · `go vet ./engine/ ./cmd/gctcli/ ./gctrpc/` 无输出 · `gofmt -l`（一处测试文件对齐修正后无输出）· `cd gctrpc && buf generate` 连跑两次同哈希 `3155e9863b24fe61a77ae987469666f57738529f45dd4a31992709593e35bd41` · `go test ./cmd/gctcli/ -count 1` 全过 · `go test ./engine/ -count 1 -skip '<既有网络类测试>'` 整套通过（M6-01 后 101.6s；含 M6-02 三新测试复跑 105.4 秒）· `npx markdownlint-cli2@0.23.2 docs/llm-dev/taskProgress/acceptance/*.md`（0 issues）

**账目说明**：本会话：㊾ `M6-01`：M6 组 0/11 → **1/11**（0.00% → 0.36%，**M6 开工**——验收记录 + 准入检查命令）；㊿ `M6-02`：→ **2/11**（0.36% → 0.73%，失效矩阵全 21 行演练——行 14 缺口同日修复）。**（51）** `M6-10`：→ **3/11**（0.73% → 1.09%，`make check` 全绿——lint 669→0 全清 + misc/markdownlint/vet/单测/-race 收口）。**总完成度 65.5% → 66.3% → 66.7%**（M6 组 11 项等权计入 4%）。

### session-25 新增的机器化约束

**准入是独立动作**（上测试网的门禁不是决策 pre-gate 的复用——检查的是「这台机器现在能不能上」）· **门的顺序是合同**（测试逐个断言 code 与顺序，脚本/UI 靠它做 diff）· **门只报实际值 vs 期望值**（每行详情回答「差在哪」）· **零值 fail-closed 到门级**（一份报告尽量列全所有问题，不在第一处缺失就炸）· **事实源错误是错误不是失败门**（读不到事实=报错；读到空值=门 FAIL——两者不许长得一样）· **验收记录按可复跑证据说话**（不能复跑的实时性/真成交一律 🟡/🔴 并写清补记步骤——不把单测通过假装成端到端通过）· **提醒与状态变更分级**（info 级提醒用内存去重不写审计行；改变状态的才落审计——M5-18 与 M6-02 的分界）· **演练以可复跑测试为触发源**（离线行=既有测试可复跑可变异；缺口的处置只有两种：当日修复或登记进表——不许静默略过）

---

### session-26（同日续段）：`M6-11` 两个固定规则基线策略 + 对照运行

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M6-11` 基线策略（Q24=A） | `backtester/eventhandlers/strategies/macdcross/`（金叉开多/死叉平，12/26/9，`fast ≥ slow` 拒绝）与 `sma200trend/`（上穿 SMA 开/下穿平，默认 200）——各含 README + 测试（合计 17 个，含逐 bar 定向断言）；注册进 `supportedStrategies`；`config/strategyexamples/` 两 `.strat`（CSV 数据源）+ 示例索引 README 补行 | `go test ./backtester/eventhandlers/strategies/... ./backtester/config/...` 全绿 · `golangci-lint run` 全树 0 · `go build`/`go vet`/`gofmt -l` 无输出 · **端到端两跑**（2019 BTC/USDT 日线）：MACD **+3.73%**、SMA200 **−0.94%**、买入持有 **+89.49%**——牛市朴素规则跑输为预期，刻度用途与口径见 `acceptance/M6-11-baseline-strategies-comparison.md`；LLM 对照列 🟡 待 SHADOW/PAPER 决策留痕（与 M6-03/04 同数据门） |

**账目**：**（52）** `M6-11`：M6 3/11 → **4/11**（1.09% → 1.45%）。**（53）** `M6-06`：→ **5/11**（1.45% → 1.82%）。**（54）** `M6-09`：→ **6/11**（1.82% → 2.18%）。**总完成度 66.7% → 67.0% → 67.4% → 67.8%**。

### session-26 新增的机器化约束

**崩溃演练不造假**（kill -9 必须是真 SIGKILL 打在真进程上、认领必须走真仓储——SQLite 单文件跨进程共享使离线既真实又可复跑）· **UNKNOWN 的不可认领性是机器事实**（`ClaimOrderIntent` 只认 PREPARED，断言它返回 false——不是文档承诺）· **重启扫描的每条处置都必须留痕**（CRASH_RECOVERY 审计 + gauge + 高优通知三件套）

### session-27：生产装配（不改变百分比）

`生产装配`第一步——把**不需要模型客户端**的三件套随子系统启动接线：`llmMetrics`、`llmRiskSwitches`（§5.6 开关服务）、`llmReadiness`（**新增生产事实源 `engine/llm_runtime_facts.go`**：配对/探针/停机/对账/日界/覆盖/审批七个来源读真库真配置，`ErrNotFound`=合法空态、其余读错上抛）。配套：仓储新增 `LatestReconciliation`（+断言），`RiskSwitchService.CurrentState()`，配置新增 `llm.probeMaxAgeSeconds`（默认 600，两处 JSON 已过 `make lint_configs`）。测试：READY 10/10 PASS + FULL halt 翻转单门 FAIL + 空机器人 7 FAIL/3 PASS（空事实≠错误）。

**下半段（同日续）·client factory + chat 走廊**：`engine/llm_client_factory.go`（激活修订版 → vault 每请求解密 → `openai`/`jev` NewClient；Jev 别名装配期拒绝）+ `engine/llm_chat_runtime.go`（工具运行器 + scope：rotator 市场 / 配置账户 / 全部非归档策略版本）+ `engine.Start` 接线（缺件只告警、保持 fail-closed）+ 配置 `llm.vault.directory`（默认 `<datadir>/llm-vault`）。测试 ×8（含 **httptest 端到端证明 `Bearer sk-live-1` 上线上**）。

**第三段（同日续）·决策运行器**：`engine/llm_decision_runtime.go` 组装 `ShadowRunner`（分析/Jev 管线 + 节流器 + 决策记录器），与 chat 走廊**共用同一批客户端与工具运行器**；`Engine.Start` 同款 warn-on-missing 接线（新字段 `llmDecisionRunner`）；共享助手 `buildScopeToolRunner` 抽取。

**第四段（同日续）·trigger/preview 换血（生产装配收口）**：`engine/llm_decision_facts.go` 把一次触发的 per-run 事实从活状态组装（冻结策略文本 + 审批 + 配对修订版 + 门禁事实 + 快照组装懒取交易所 + Jev 证据从转录逐条重建）；两 handler 换成 `runShadowDecision`（同一影子链，永不成单）。

**行为变化**：**14 个 LLM RPC 全部为活**。`RiskFacts`（SHADOW 硬风控事实）维持 nil = 记录 awaiting-verdict（seam 文档化语义，影子不下单）——登记为剩余装配项。验证：全树 lint 0、聚焦测试全绿、引擎整套见 workLog §10。细节见 workLog session-27 §6–§11。

### session-28：`M0-09` 配置迁移 v18（M0 组收官）

`config/versions/v18/`：v17→v18 只做一件事——`llm` 段缺失时插入 `{"enabled":false}`；**显式 false/true/空对象/null 一律视为操作员的表态，不覆盖**。注册进版本链、`testdata/configtest.json` 升 18、守卫矩阵 7 例、**真实加载器**（`ReadConfigFromFile`）双侧测试（无段→补段+默认补齐；有段→原样升版）。**账目（55）：M0 13/14 → 14/14 组完成**（4.64% → 5.00%）。**总完成度 67.8% → 68.2%**。细节见 workLog session-28。

### session-28（续段二）：触发配置解锁 + 扫描器生产装配（M2-13 残余尾项，不改变百分比）

`config.LLMTriggerConfig`（`llm.triggers`：`FundingWindow`/`FundingWindowLeadSeconds`/`ATRSpike`/`ATRSpikeMultiple`；两开关默认 OFF 按 Q5；`CheckLLMConfig` 对非正值填充 900s/1.5×）→ `engine/llm_rotation_setup.go` `triggerPolicyFromConfig` 叠加到 `trigger.DefaultPolicy` → **`SetupRotation` 真正构建 `NewTriggerScanner` 并 `attachScanner` 挂到旋转器**（此前生产路径上没有扫描器，「重载后扫描」只在测试里被调用过）；`attachScanner` 新增 `snapshot.Interval` 形参（`Rotator.scanInterval`）。测试 ×2（Policy 叠加逐值断言 + 配置缺省填充），`config_example.json`/`testdata/configtest.json` 经 `make lint_configs` 对齐。验证：全树 lint 0 · `make misc_checks` All passed · `go test ./engine/ ./config/... ./llm/trigger/ -count 1`（-skip 两个既有 flaky）**exit 0（engine 93.955s）**。细节见 workLog session-28 §6。

### session-29：`W5-01` 对话工作区收尾（Conversations 侧栏 + Markdown + Sources + 客户端内存草稿）

`ChatPage.tsx` 重写为两栏工作区：AntD X **`Conversations` 侧栏**（两行标签 + per-item 归档菜单）替代原 Select；**Markdown 渲染**新增依赖 `react-markdown@10.1.0` + `remark-gfm@4.0.1`——渲染为 React 元素、**零 HTML 直通**（模型输出是外部数据，不引 rehype-raw），链接统一 `target=_blank rel=noreferrer`；证据卡片从裸 Tag 换成 **`Sources` 展开视图**（`tool｜market` + 质量/源时间/sha256 截断/引用——字段进 title 保证静态可见）；**草稿仅存客户端内存**（per-conversation Record：切换保留、发送即清、从不落库，Sender 改受控）。`mock-gateway.mjs` 流式载荷升级为 Markdown（h2/列表/行内 code/GFM 表格）。**验证**：浏览器实测（mock 栈）——流式增量 → h2/表格结构化渲染（无障碍树逐单元格）→ Sources 展开/收起 → 草稿切换往返保留；`tsc --noEmit` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（56）：W5 0/8 → 1/8**（0.00% → 1.50%）。**总完成度 68.2% → 69.7%**。细节见 workLog session-29。

### session-30：`M1-14` providers 命令族（7 RPC + gctcli + 分层探针生产路径，W4 组解锁）

`gctrpc/rpc.proto` 落地 08 §3.4 全 7 个 RPC（list/create/update/delete/test/activepair get/switch + 14 消息；`buf generate` 连跑幂等 `5dbc74c8…`）；`engine/rpcserver_llm_provider.go` 7 个 handler，其中 **`LLMTestProviderProfile` 补上了分层探针的生产编排**（修订版→vault 每请求 Reveal→`openai.ProbeAll` 四层/`jev.Probe`→每条判决落 `llm_probe_results`——此前只有仓储半截、没有编排方）；`cmd/gctcli/llm_provider.go` 命令族一一对应（switch 缺幂等键客户端即拒）。**语义锚点**：探针失败=200 判决不是 RPC 错误；切换=重认证+epoch CAS+幂等键三闸；空修订版=清槽→构造性 INCOMPLETE；删除永远是归档；密钥不上命令行不上 wire。**验证**：7 个测试函数（httptest 四层探针全路径 + 落库断言 + `Bearer sk-lm-1` 上线上）· 全树 lint 0 · `make misc_checks` All passed · 引擎整套见 workLog。**账目（57）：M1 13/15 → 14/15**（5.20% → 5.60%）。**总完成度 69.7% → 70.1%**。细节见 workLog session-30。

### session-31：`W4-01` Provider 档案列表（前端）+ `W1-07` 端点包装

`ProvidersPage` 重写为真数据档案表（显示名+归档徽标 / 类型 / 最新修订版 / 模型+Base URL / 指纹 / 启用状态；**行展开 = 修订版详情**：引用、指纹、超时、最大输出、提示词版本、成本计划值、24h 上限、本地 HTTP、建立人）+ 「活化配对」卡片（pair_epoch / readiness / 两侧修订版 / 更新者；`found=false` 显示「尚未激活过」而不是错误）+ 「含归档」开关；`types.ts`/`gctApi.ts` 新增 `LlmProviderProfile`/`LlmActivePair` 类型与 `llm/providers`/`llm/activepair` 端点（W1-07 同步推进）；mock 网关补两侧离线形状。**代理白名单零改动**：GET 的 llm/* 规划路径本就放行，写路径仍受 `GCT_ALLOW_WRITES` 闸门。**验证**：浏览器实测（配对卡片 + 两档案 + 行展开逐字段）· `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（58）：W4 0/6 → 1/6**（0.00% → 1.33%）。**总完成度 70.1% → 71.4%**。细节见 workLog session-31。

### session-32：`W6-01` 决策列表（前端）+ `W1-07` decisions 端点包装

`DecisionsPage` 新增真数据决策列表（时间 / 触发 / 决策徽标 / 原因码 / 策略修订版 / `llm × jev` 模型对 / 延迟 / 轮次工具数 / 输入哈希；**策略修订版 id 服务端过滤 + 决策客户端过滤 + 最近 N 条**）+ 空态说明「触发/预览产生的 SHADOW 运行会出现在这里」；`types.ts`/`gctApi.ts` 新增 `LlmDecisionRun`/`llm/decisions` 端点（W1-07 同步推进）；mock 补三条运行离线形状（含 `RISK_ORDER_NOTIONAL_EXCEEDED` 的 HOLD 行）。**列表只读**——触发/预览入口留在 gctcli，SHADOW 链永不成单。**验证**：浏览器实测逐列断言 · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（59）：W6 0/6 → 1/6**（0.00% → 1.67%）。**总完成度 71.4% → 73.1%**。细节见 workLog session-32。

### session-33：`W6-05` 审计事件流（前端）+ `W1-07` audit 端点包装

`DecisionsPage` 新增审计事件流卡片：事件表（时间 / 类型 / 操作者 / 对象 / 详情截断+悬浮全文）+ **事件类型服务端筛选** + 最近 100/500/2000 条 + **「导出 JSON」客户端下载**（Blob + object URL，发不出任何服务端请求）；`types.ts`/`gctApi.ts` 新增 `LlmAuditEvent`/`llm/audit` 端点（W1-07 同步推进）；mock 补 4 条离线事件（PAIR_SWITCH / APPROVAL_GRANTED / RISK_SWITCH_SET / CRASH_RECOVERY）。**审计流只读**——页面无改写动作，导出即当前筛选后的字节。**验证**：浏览器实测四行逐字段 · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（60）：W6 1/6 → 2/6**（1.67% → 3.33%）。**总完成度 73.1% → 74.8%**。细节见 workLog session-33。

### session-34：`W4-02` 档案编辑表单（追加新修订版，写路径实测）

档案表「编辑」action → **Drawer 表单**（逐字段预填：actor/baseUrl/modelId/引用/16-hex 指纹/超时/LLM∨JEV 专属字段/dailyBudgetUsd/24h 上限/本地 HTTP/代理）；`gctApi.ts` 新增 `updateLlmProviderProfile` mutation（POST `llm/providers/{id}` + tag 失效重取）。抽屉警示块写明**语义**：提交=追加新修订版；被取代的若是激活配对指向的版本 → 指针清空 + `pair_epoch++` + INCOMPLETE；密钥录入在 vault（W4-06），表单只填引用与指纹。**写路径实测**（本地代理 `GCT_ALLOW_WRITES=true` 测试窗，测后恢复默认 false）：打开 DeepSeek 编辑 → 改模型为 `deepseek-reasoner` → 提交 → 列表刷新 `#3（共 3 版）`。**验证**：浏览器实测全链路 · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（61）：W4 1/6 → 2/6**（1.33% → 2.67%）。**总完成度 74.8% → 76.1%**。细节见 workLog session-34。

### session-35：`W4-03` 分层探针触发与结果展示（auth/tool_call/schema/stream；systemone）

档案行「探针」action → 结果 **Modal**（层 / PASS·FAIL 徽标 / 错误码 / 延迟 / tokens / 返回模型；失败行可展开读解释；说明行写明四层各自失败的诊断含义与「判决已落库并影响 readiness」）；`gctApi.ts` 新增 `testLlmProviderProfile` mutation（POST `llm/providers/{id}/test` + tag 失效）；mock 补 LLM 四层判决（含 SCHEMA 失败样本 `CANDIDATE_INCOMPLETE`）与 JEV 单层。**探针=真招**（每次点击真实请求并落库）。**验证**：浏览器实测 LLM 四层逐行（3 PASS + 1 FAIL）· JEV 单层经真代理链 `{"kind":"JEV","layers":["JEV:PASS"]}` · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（62）：W4 2/6 → 3/6**（2.67% → 4.00%）。**总完成度 76.1% → 77.4%**。细节见 workLog session-35。

### session-36：`W4-05` 切换活化配对（CAS + 重新认证 + 影响面提示）

活化配对卡「切换配对」→ 切换 Modal：修订版下拉**只列 live 版本**（未归档档案的最新修订版）、`expected_pair_epoch` 预填、口令 `Input.Password`（重认证）+ **自动 UUID 幂等键** + actor；警示块写明影响面（`pair_epoch+1`、旧配对下策略暂停/审批撤销登记在切换命令里、先跑探针）。`gctApi.ts` 新增 `switchActiveModelPair` mutation；mock 三重闸逐项。**三重闸全链路实测**：错误口令 `403 operator password rejected`；陈旧 epoch `409 pair epoch conflict: expected 999, stored 3`；正路径浏览器提交 → **pair_epoch 3→4 + READY + 更新者 webui-operator**。**账目（63）：W4 3/6 → 4/6**（4.00% → 5.33%）。**总完成度 77.4% → 78.7%**。细节见 workLog session-36。

### session-37：`W6-02` 决策血缘可视化 + `W6-03` 风控证据表（同一血缘抽屉）

决策行「血缘」→ 血缘 Drawer（860px）：**十阶段垂直 rail**（前六段有据勾选——运行行/风控评估为证；执行段如实标「PAPER/TESTNET 前不产生记录」不画假勾）+ 运行摘要 + **候选/Jev/转录三段原文**（`prettyJson` 解析失败回退原文）+ **风控证据表**（判定/检查版本/数量/名义额/参考价/信封哈希/评估时间；**行展开 = 检查码逐项表** 检查码/实际值/允许值/单位/通过·拒绝，`parseChecks` 形态不符回退原文）+ 关联审计表；`gctApi.ts` 新增 `llm/decisions/{id}` 与 `/lineage`（按需 skip）。**原文永不丢、假勾不存在、W6-03 不另建重复页**（同一入口同一读取路径）。**验证**：浏览器实测（六段勾选 + 三段原文 + 3 检查项逐行 + audit 行）· `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（64）：W6 2/6 → 4/6**（3.33% → 6.67%）。**总完成度 78.7% → 82.1%**。细节见 workLog session-37。

### session-38：`W6-04` 手动触发与预览（写路径 + 门禁语义）

决策列表卡「手动触发/预览」→ Modal（策略修订版 id / 市场 id / actor）：说明块钉死两者差异——**触发走完整门禁（pre-gate），预览永远不下单（HOLD/REVIEW 收尾）**；`gctApi.ts` 新增 `trigger`/`preview` mutation（tag 失效→新运行置顶）；mock 补两侧离线形状。**写路径全链路实测**：代理链 curl（`MANUAL_PREVIEW/HOLD/PREVIEW_ONLY_HOLD`）+ 浏览器「仅预览」→ toast「预览完成：HOLD（PREVIEW_ONLY_HOLD）——已写入决策列表」+ 列表刷新（新行置顶）。**账目（65）：W6 4/6 → 5/6**（6.67% → 8.33%）。**总完成度 82.1% → 83.8%**。细节见 workLog session-38。

### session-39：`W7-05` 风险开关目录面板（live + 翻转闭环）+ 阻塞项台账（测试网凭据）

**阻塞项**：`decisions/01-testnet-credentials.md` 建立（测试网凭据与真实供应商口令——阻塞 `M6-03/04/05`、`M1-15`；三选项 + 建议 + 不决策后果），`decisions/README.md` 与 `TASKS.md` 四条对应行 `[!]` 同步。**W7-05**：proto 新增 **`LLMGetRiskSwitches`**（`buf generate` 幂等 `3e387899…`）；engine handler 输出目录 18 行的 live 状态（默认已应用）+ 三把红线锁 + 拒绝码（+测试）；`HealthPage` 开关卡升级 **live 面板**——18 项徽标、锁定项禁用 + Tooltip、未锁定项可翻转（toast 明示写 `CONFIG_CHANGED` 审计）、中文注解保留；mock 同锁同语义（锁定关闭 403）。**静态时代的五个「锁定」猜测由 live 数据纠正为三个**。**验证**：代理链 curl（翻转 ok / 锁定 403）+ 浏览器（curl 翻转回读 checked=true → UI 翻回 checked=false + toast）· `tsc` 0 · `oxlint` 0 · `pnpm build` 成功 · 聚焦引擎测试全绿。**账目（66）：W7 1/7 → 2/7**（1.00% → 2.00%）。**总完成度 83.8% → 84.8%**。细节见 workLog session-39。

### session-40：`LLMGetStatus` 落地（W7 运行控制读侧）+ 适配器缺口登记（不计分）

**proto 新增 `LLMGetStatus`**（`buf generate` 幂等 `327a910f…`）+ `engine/rpcserver_llm_control.go` handler——**只填可诚实取数的字段**：runMode/liveUnlocked（=模式已 LIVE，不发明独立解锁旗标）/配对两侧（readiness、epoch、档案名+模型、探针年龄）/停机（halted + 取最严级别）/对账（Reconciled + 年龄）/24h 预约余量/活跃审批计数。**诚实缺席**：clockDrift/oldestDataAge/running/decisionsToday/newRiskIntents/tradeAuthorized/executionReady **不进消息**（诚实的『—』胜过伪造的 0）。站点头部徽章与健康页自动转 live（SHADOW｜READY·epoch 3｜已对账｜预约剩余 2）；`tradeAuthorized` 与 `decisionsToday` 缺席渲染『—』（此前把「没报」伪装成 false/0）。**新登记适配器缺口**：halt 写侧需 `iRunningStrategyPauser`/`iBotOrderCanceller`/`iPositionFlattener` 生产实现（仅测试侧有，与 A14 同源）；对账手动触发需 `LLMReconcile` + 账户快照适配器；W7-03 token/成本需 accounting 读面。W7-01/02/03 转 `[~]`（读侧 live、写侧登记）。细节见 workLog session-40。

### session-41：`W4-06` 密钥保管（vault 初始化与录入 UI + RPC）——W4 组完成

**proto 三端点**（`LLMVaultStatus` GET / `LLMVaultInit` POST / `LLMVaultAddSecret` POST；`buf generate` 幂等 `66b5dba3…`）+ `engine/rpcserver_llm_vault.go`：未初始化目录 = 状态不是错误（UI 以此显示「初始化」按钮）；**`secrets.Init` 幂等——第二次调用原样打开、永不铸新钥匙**（测试含「二次 Init 后旧密钥仍可 Reveal」的机器证明）；录入只回「引用 + 指纹」，**明文不落响应不落日志**。前端 `ProvidersPage`「密钥保管」卡：状态/目录/条目表 + 初始化按钮 + `Input.Password` 无回显录入表单（成功 Alert 回显引用+指纹并提示填进档案修订版）+ 保管纪律注释；mock 三端点。**验证**：引擎测试全绿 · 代理链 curl 全链路（`initialized:false → init → ref+fingerprint`，明文不在响应里）· 浏览器卡渲染与交互 · `tsc` 0 · `oxlint` 0 · `pnpm build` 成功。**账目（67）：W4 5/6 → 6/6**（6.67% → 8.00%，**W4 组完成**）。**总完成度 84.8% → 86.1%**。细节见 workLog session-41。

### session-42：`W4-04` 配对横幅 + `W5-03/04/05/06/08` 策略工作区（后端 4 RPC + 全链浏览器实测）+ `W1-07`/`M2-08` 核账

**后端**：proto **+4 RPC / +8 消息**（`LLMCreateStrategy`、`LLMSaveStrategyDraft`（草案=**追加新版本**；`proposal_state` 由服务端从提案文档推导，客户端不能自称就绪）、`LLMListStrategyVersions`、`LLMGetStrategyApproval`（无审批行 = `found:false` 是状态不是错误）；`buf generate` 连跑幂等 `9d7efc894c2d4e32dee6609b38ba16ad746c5f91`）+ `engine/rpcserver_llm_strategy.go` 4 handlers（+`approvalToProto`，坏市场清单炸错不吞）+ `engine/llm_strategy_admin.go`（`LLMStrategyAdmin`：`InsertStrategy`+`STRATEGY_CREATED` 审计；`GetStrategyVersion` 校验父版本同策略、归档策略拒写；`SourcePromptSha256`=提案字节 SHA-256）+ `engine/llm_strategy_admin_test.go` **3 测试函数**（真库 sqlite：创建→草案→冻结 全链（含未答澄清问题不许冻结的服务端闸）、校验拒错矩阵、四 handler 转换 + 审批读数）。

**前端**：`PairBanner`（**W4-04** 全局横幅：两侧显示名×model、readiness、epoch、未就绪写明后果与去向、未知 readiness 按未就绪、后端不可达整条隐藏）+ `StrategiesPage` 重写（策略列表 + 新建；版本列表「根/续」；**详情抽屉**：提案视图（未设项警示高亮）/冻结信封、澄清问题块+去对话链接、审批卡+倒计时、暂停/恢复/撤销按钮；**提取 Modal**（会话/市场/actor → 提案预览）；**冻结 Modal**（reviewable-v1 组合全量预览）；**审批 Modal**（重认证）+ gctApi **13 端点** + types **15 类型** + mock 全族（三重闸 + 状态机同语义）。

**验证**：引擎聚焦测试全绿（exit 0）· `buf generate` 双跑同哈希 · `tsc` 0 / `oxlint` 0 / `pnpm build` 成功 · **浏览器全链实测**（mock 栈）：横幅 READY 实名渲染；草案抽屉澄清问题+提案视图；**代理写窗**（临时 `GCT_ALLOW_WRITES=true`，测后恢复 false）——curl 建 READY_FOR_REVIEW 草案 → 浏览器**冻结成功**（双哈希 toast `b9091d6a…`）→ 错口令 403 → **对口令审批成功**（appr-2 + 倒计时 4h59m 逐秒）→ **版本 diff 真表 5 行** → curl 暂停/恢复/撤销/**撤销后恢复被拒**。

**账目（68）**：`W4-04` 核账（名实相符）+ `W5-02`~`W5-06`+`W5-08`（W5 1/8 → **7/8**，+9.00pp）+ `W1-07` 收口（7.5/8 → 8/8，+0.375pp）+ `M2-08` 补勾（15.5/16 → 16/16，+0.19pp）——**逐组从零重算：总完成度 96.6%**（原滚动值 86.1% 含 ~0.95pp 记账漂移；逐组表见 §2）。细节见 workLog session-42。

### session-43（同日续段）：`W5-07` 市场授权（三 RPC + 级联撤销实测）——W5 组完成

**后端**：proto **+3 RPC / +9 消息**（`LLMListMarketGrants` GET `llm/marketgrants`、`LLMGrantMarket` POST、`LLMRevokeMarketGrant` POST `{id}/revoke`；buf 连跑幂等 `5517c225205d236e554801172822ad9037ea8d23`）+ `engine/rpcserver_llm_grants.go` 三 handlers（**复用 M4-04 `MarketGrantService`**——30 天硬顶与活体唯一都是服务端的；撤销响应带回被级联撤销的审批 id 全集）+ `engine/rpcserver_llm_grants_test.go`（真库：授权→列表→级联撤销（含该市场有效审批被撤 + 审批读数侧确认 revokedAt）→二次撤销拒；31 天拒 / 缺字段拒）。

**前端**：`StrategiesPage` 新增「市场授权（第二道闸：看盘 ≠ 能交易）」卡（账号切换查询、授权 Modal（1..30 天）、撤销 Modal（**级联警示** + 原因入审计）、行内状态「已撤销·操作者 / 已过期 / xx 前有效」）+ `gctApi` 3 端点 + types 4 类型 + mock 全族（活动重复 409、窗口校验、级联撤销同语义）。

**验证**：`go test ./engine/ -run 'TestMarketGrantRPCs'` ok（2.463s）· `go build ./...` 绿 · `tsc` 0 / `oxlint` 0 / `pnpm build` 成功 · **浏览器全链实测**（写窗开启测后已恢复 false）：撤销种子授权 BTC → 行变「已撤销 · webui-operator」+ **审批侧确认** `revokeReason="market grant revoked: binance:usdtperpetual:BTC_USDT"`（级联因果可见）；UI 授权 SOL 成功（toast + 新行置底）；curl 验收重复授权 409 / 31 天 400。

**账目（69）**：`W5-07` 完成——W5 7/8 → **8/8**（10.50% → **12.00%，W5 组完成**）。**总完成度 96.6% → 98.1%**。细节见 workLog session-43。

### session-44（同日续段）：`W6-06` REVIEW 过期展示（W6 组完成）+ 权重口径审计修正

**交付**：`LLMGetStatusResponse` +`review_expire_hours`（proto 增量，`buf generate` 幂等 `00008ae3f9a4f14e81102b2ad1a5cb95b19f1a15`）——handler 随配置下发，**未配置时与扫描器同源回退 12h**（显示与行为不许分叉；测试钉 12h 缺省与 2h 收紧两态）+ `DecisionsPage` 决策列派生「**已过期·未执行**」标签（Tooltip：超时自动转 EXPIRED + 审计 + 通知，**自动过期 = 不执行**）+ 统计卡注释改 live 值 + types/mock（含 17h 前 REVIEW 种子）。**浏览器实测**：17h 行带标签、5h 行不带、注释显示 `REVIEW 12h 自动过期`。

**审计修正（重要）**：核对 `TASKS.md` 权重表——各项之和为 **107**（W 67 + M 40），表头「合计 100%」系早期笔误；而全部历史 summary 按「Σ得分 ÷ 100」拼写，**读数一贯偏高 ~7%**。自本会话起：① `TASKS.md` 表头改 **107%** 并写明公式；② 本文件总完成度 = **Σ得分 ÷ 107**（旧条目数字保留为史，除以 1.07 即换算为当前口径）。

**账目（70）**：`W6-06` 完成——W6 5/6 → **6/6**（8.33 → 10.00 分，**W6 组完成**）。Σ得分 98.115 + 1.667 = **99.78 / 107 = 93.3%**（旧口径同点写作 98.1/98.2——见上方修正说明）。细节见 workLog session-44。

### session-45（同日续段）：`W7-07` 覆盖轮询运行面（`LLMGetCoverageStatus` RPC + 健康页运行面卡）

**后端**：proto +`LLMGetCoverageStatus`（GET `/v1/llm/coverage`——**套在既有的 501 计划路径上，前端 `getCoverageStatus` 端点零改动转 live**；`buf generate` 连跑幂等 `25fde8b89cc6648531465e34f90817488a65d605`）+ `RotationStats` +`LastCycleCompletedAt`（完成时刻是运行证据，不只是计数）+ `engine/rpcserver_llm_coverage.go`（**计划值与实测值同响应分列**；coverage 未启用 = `enabled:false` 状态不是错误；子系统未装配 fail-closed）+ 测试（真旋转器 sweep 中途形状 + 收尾完成时刻 + 两态）。

**前端**：`CoverageStatus` types +9 运行面字段；`HealthPage` 覆盖卡升级——范围·合约 / **周期进度 visited/总数（%）** / 最旧合约年龄 / **实际权重占用** / **周期完成次数 + 上次完成时刻** / 失败计数 + ticker 刷新·整点重载脚注；未启用显示合法态文案。`coverage.ts` 计划算术保留（MarketsPage 逐行渲染需要），运行面改由后端权威。

**验证**：`go test ./engine/ -run 'TestLLMGetCoverageStatus'` ok · `go build` 绿 · buf 幂等 `25fde8b8…` · tsc 0 / oxlint 0 / build 成功 · **浏览器实测**：健康页卡渲染 `ALL_USDTM_PERPETUAL · 588`、`214 / 588 合约（36%）· 10 分钟/周期`、`最旧合约年龄 331s`、`权重占用 22 / 1440 /min`、`周期完成 412 次 · 上次 2026-10-11 12:50:03`、`失败计数 2` + 脚注（ticker 刷新/重载/增量间距）。

**账目（71）**：`W7-07` 完成——W7 2/7 → **3/7**（2.00 → 3.00 分）。Σ得分 99.78 + 1.0 = **100.78 ÷ 107 = 94.2%**（旧口径同点写作 ≈100.8）。细节见 workLog session-45。

### session-46（同日续段）：`W7-01` halt 写侧（三生产适配器 + 三 RPC + 健康页写侧 UI，全链实测）

**适配器（`engine/llm_halt_adapters.go`）**：`LLMRunningStrategies`（暂停每个 FROZEN+活审批的版本，逐条记状态迁移；已暂停/无审批不动——「可运行」= 会被调度的那批）、`LLMBotOrders`（`GetOrdersActive` + `gct-llm-` 前缀过滤 → `DeriveCancel`+`Cancel`——**用户手动单不可撤**）、`LLMPositionFlattener`（逐仓位 reduce-only 反向市价单；Latest→Opening 回退；方向不可分类拒）。三者失败即中止并带已完成计数上抛。

**RPC**：proto +`LLMListHalts`/`LLMSetHalt`/`LLMClearHalt`（buf 幂等 `ff6c788bda42bdaa1e44e8bc29ecbd0765ce4c4c`）+ `engine/rpcserver_llm_halt_rpc.go`（控制器接生产子系统；未装配 fail-closed）。

**测试**（2 个函数，真库）：SOFT 暂停可运行版本 + 版本转 PAUSED；**中止不回滚**——MARKET 无 canceller 时 halt 保持生效（落库即生效，错误指出哪一步没做完——安全方向的半成品）；错口令清除被拒且 halt 在档；对口令清除 → 策略**保持 PAUSED**、审批不恢复、`HALT_CLEARED` 审计；FULL 平仓缺二次确认拒。

**前端**：`HealthPage` halt 卡升级（halts 表 / 设置 Modal（三级 + MARKET 市场 + FULL 平仓双确认）/ 清除 Modal（口令 + 不自动恢复警示））+ gctApi 3 端点 + mock 全族（s3cret 口令、halt 驱动 status 的 halted/haltLevel）。**浏览器实测**：UI 设 SOFT → 行渲染 + **状态条变 SOFT**；错口令拒（halt 在档）；对口令清除 → 卡回 NONE、状态条复位。

**账目（72）**：`W7-01` 完成——W7 3/7 → **4/7**（3.00 → 4.00 分）。Σ得分 100.78 + 1.0 = **101.78 ÷ 107 = 95.1%**。细节见 workLog session-46。

### session-47（同日续段）：`W7-02` 对账手动触发（交易所真值快照适配器 + `LLMReconcile` RPC + 健康页按钮）

**取数适配器（`engine/llm_reconcile_snapshot.go`）**：`fetchExchangePositions`（逐对 `GetFuturesPositionSummary`——**符号尺寸→LONG/SHORT+正数量**；零仓不入册；**任一对读不到整体拒**——「读不到」不是「没有仓位」，把前者当后者是在编造对账）+ `fetchLocalPositions`（跟踪仓位；Latest→Opening 回退；非本所/本资产过滤；方向不可分类拒）。两侧 MarketID 统一 `<exchange>:<asset>:<BASE>_<QUOTE>`（小写）——同一拼写是比对成立的全部前提。

**RPC**：proto +`LLMReconcile`（POST `/v1/llm/reconcile`；buf 幂等 `8f3fe13de92c58b8768a3114bd6ab531881c4b74`）+ `engine/rpcserver_llm_reconcile_rpc.go`（组装两侧读数 + 本地挂单快照 → **复用 M5-08 `Reconciler.Pass`**——比对语义与落账不动；未装配 fail-closed）。

**测试**（3 函数）：符号/零仓/失败传播（含错误带 pair 名）/范围过滤/Opening 回退/方向不可分类拒 + fail-closed 两态。

**前端**：`HealthPage` 对账卡「立即对账」按钮（`onReconcile` 按结果分叉 toast：CLEAN 注明 5 分钟有效期 / 差异注明 N 处 + halt id + 新开仓被阻断）+ `gctApi` mutation + mock（恒 CLEAN；差异路径由 M5-08 测试覆盖）。**浏览器实测**：点击 → 「对账完成：CLEAN（有效期至今后 5 分钟）」。

**账目（73）**：`W7-02` 完成——W7 4/7 → **5/7**（4.00 → 5.00 分）。Σ得分 101.78 + 1.0 = **102.78 ÷ 107 = 96.1%**。细节见 workLog session-47。

### session-48（同日续段）：`W7-03` 用量读面（`LLMGetUsage`）——口径修正 + 成本刻意缺席

**口径修正（重要）**：工程中**没有 `llm_accounting_*` 表**（W7-03 登记时假设的表不存在）——token/延迟实际写在 **`llm_budget_reservations`** 的结算列。读面按实际 schema 做；此后一切「accounting」提法指预算预约表。

**后端**：proto +`LLMGetUsage`（GET `/v1/llm/usage`；buf 幂等 `4ac5592c7794cfb74fac8958de6d2eb7c662ae0a`）+ `engine/rpcserver_llm_usage.go`：窗口缺省 24h、**上限 720h**（无界扫描是一个 RPC 背后的延迟炸弹）；**只计 state=USED**（在途没结算、已释放没跑起来）；按 provider kind × purpose 聚合 + 排序；**未激活配对=空用量是状态**；未装配 fail-closed。

**测试**（真库）：两 kind×三 purpose 聚合 + 均值延迟 + **窗口外/RELEASED 均不入账** + totals + 720 钳制。**测试坑**：token 列在**结算时**写入——直插初始行不带 token，seed 必须走生产路径（insert RESERVED → `SettleBudgetReservation`）。

**前端**：`HealthPage` 用量行（Jev/LLM × purpose：次数 · in/out（k 格式化）· ⌀延迟）+ 窗口合计 + 口径注释。**浏览器实测**：`Jev·SYSTEMONE 6 次 in 4.2k/out 1.8k ⌀2300ms` / `LLM·CHAT 9 次 31.5k/12.4k ⌀8100ms` / `LLM·DECISION 3 次 20.4k/6.9k ⌀15200ms` + 合计 + 「自估成本缺席」说明。

**「自估成本刻意缺席」**：schema 无任何价目源（provider 修订版无价格列）——猜出来的美元数字比缺席的字段更糟（session-40 诚实纪律）；成本展示等有价目源的那天。

**账目（74）**：`W7-03` 完成——W7 5/7 → **6/7**（5.00 → 6.00 分）。Σ得分 102.78 + 1.0 = **103.78 ÷ 107 = 97.0%**。细节见 workLog session-48。

### session-49（同日续段）：`W7-06` 通知通道（就绪度面 + 测试推送）——**W7 组完成，W 组（W0-W8）全完成**

**后端**：proto +`LLMGetNotifyStatus`（GET `/v1/llm/notify`）/`LLMTestNotify`（POST `/v1/llm/notify/test`；buf 幂等 `b86f0fe0bdaddc3889f907208209e79fe5b8c29f`）+ `engine/rpcserver_llm_notify.go`：

- **凭据只报布尔从不回显**（测试机器断言：响应全文不含 token/secret/URL path——与 M1-14 的密钥测试同族）；端点只回 `scheme://host`（完整 URL 可能带 query 密钥）；不可解析时报「已设置但无法解析」（不隐藏也不冒充）。
- `Engine.LLMNotifier()` **首次用时装配**（Push→`CommunicationsManager.PushEvent`；nil 管理器=静默丢弃——文档化语义「Telegram 故障不拦已过闸的止损」；测试可预置捕获推送）。
- `LLMTestNotify` 经**生产 notifier** 推 `llm_notify_test` 探针事件（15 事件目录的文档化扩展——操作员需要一个可见的方式证明通道端到端可用）；响应诚实报中继运行态（未运行=被丢弃）。

**前端**：`HealthPage`「通知通道（Telegram + webhook · Q19）」卡——通道表（enabled/凭据布尔/目标数/端点 host）+ 通信中继状态 + 「发送测试通知」按钮（按 relayRunning 分叉 toast）+ 配置纪律注释（凭据不回显 / `config.json` 编辑+重启 / Q19 高危确认不靠通知）。

**验证**：`go build` + 聚焦测试（含凭据不回显机器断言）· buf 幂等 `b86f0fe0…` · tsc/oxlint/build · **浏览器实测**：telegram「是/已设置/1」、webhook「否/未设置/—」、中继「运行中」、测试推送 toast「已投递进通信中继」均渲染。代理写闸已恢复默认。

**账目（75）**：`W7-06` 完成——W7 6/7 → **7/7（组完成）**。Σ得分 103.78 + 1.0 = **104.78 ÷ 107 = 97.9%**。**W 组（W0-W8）自此全部完成**（前端平台最后一个未勾选项清零）。细节见 workLog session-49。

### session-50（同日续段）：`M6-07` 密钥轮换演练（模型 Key 机器验证 + 币安 Key 手册）

**机器链路**（`engine/llm_key_rotation_drill_test.go` → `TestM6KeyRotationDrill`，真 vault+真库，**7 步断言**）：vault 追加新密钥（**重开幂等不重铸**）→ **生产管理路径** `AddProviderRevision` 追加新修订版（新引用+新指纹）→ supersede 舞蹈（**指针清空 / INCOMPLETE / epoch++**，只动被换一侧）→ **superseded 永不可复活**（`ErrRevisionNotLive`）→ **轮换是追加不是销毁**（旧/新密钥都可 Reveal——审计回读）→ 新修订版探针 + CAS 重新激活回 READY（epoch 2→3 + 切换命令留痕）→ **泄密机器证明**（本轮触及的行渲染不含任何明文 Key，指纹在）。

**演练记录**：`taskProgress/acceptance/M6-07-key-rotation-drill.md`——模型 Key 生产手册（7 步，含"旧 Key 保留回滚窗口再废止"）+ **币安 Key 手册**（新 Key 禁提现绑 IP → config.json 600 替换+重启 → 对账验证 → 废止旧 Key；🔴 待凭据与 M6-03/04/05 同门）+ **vault 边界如实记录**（无原地重铸 API——每密钥独立 DEK；"轮换 vault"=新目录+重录+追加修订版，旧目录只读留存）。

**账目（76）**：`M6-07` 完成——M6 6/11 → **7/11**（2.18 → 2.55 分）。Σ得分 104.78 + 0.36 = **105.14 ÷ 107 = 98.3%**。细节见 workLog session-50。

### session-51（同日续段）：`M6-08` 备份恢复演练（三件套两跳恢复进新进程）

**机器链路**（`engine/llm_backup_restore_drill_test.go` → `TestM6BackupRestoreDrill` 父 + `TestM6BackupRestoreChild` 子，**10 步断言**）：机器 A = 真 vault（RSA 包裹钥匙对 + 两条密钥 + `conversation-key.bin`）+ 真库（生产路径写入：档案/修订版、活跃配对、订单意图、审计行、已结算预算预约）+ 双版本配置（v18 显式 LLM 段 / v17 无 LLM 段）；**文件级两跳** A → 备份介质 → B（库先干净关闭再拷）；机器 B **全新进程 + 全新目录树**重开：迁移干净空跑 → 从库里引用揭密钥（LLM+Jev 两半咬合、指纹一致）→ 会话密文逐字节重开 → 意图/审计行在 + `LLMGetUsage` 跨机器出数（token 与延迟）→ v18 配置原样加载（`CheckLLMConfig` 不覆写显式值）/ **v17 配置在 B 上迁移到 18**（缺席 llm 段 disabled + READ_ONLY）→ **负例①只有库没有 vault = `ErrVaultNotInitialised`**（不凭空铸新钥匙——保密主张跨机器成立）→ **负例②漏拷 `conversation-key.bin` = 重铸新钥匙、旧密文 `ErrDecrypt` 永久不可读**（备份完整性是要害）→ **泄密机器证明**（恢复库原始字节不含任何明文 Key/消息）。

**演练记录**：`taskProgress/acceptance/M6-08-backup-restore-drill.md`——备份集定义（vault 目录四件套 / 库 / 配置，缺一不可）+ 生产备份/恢复手册（含「vault 与库分开保管」红线）+ 边界如实记录（同机新进程非真双机、SQLite 口径——pg 的 `pg_dump`/`pg_restore` 单列未执行；机器 B 只读+迁移不声称写路径）。

**账目（77）**：`M6-08` 完成——M6 7/11 → **8/11**（2.55 → 2.91 分）。Σ得分 105.14 + 0.36 = **105.50 ÷ 107 = 98.6%**。细节见 workLog session-51。

**同会话第二段（不计分）**：`M2-13` 残余「下架事件生产者」收官（+ M6-02 #18 由 🔴 转 🟡）——`engine/llm_market_delist.go` `MarketDelistWatcher`：旋转循环每 10 分钟重读列表后与上轮做差，消失的市场走**行 18 完整响应链**（撤活跃 grant → 级联撤覆盖它的有效审批 → 暂停对应 FROZEN 版本 → `MARKET_DELISTED` 审计 → 高优推送），**首见只登记不响应**、失败留待下轮重试（全链幂等）、**恰好一次**；`SetupRotation` 经 `m.marketWatch` 不落锁挂接、`Engine.Start` 在 Start 前装配（竞态为零）。机器验证：`TestMarketDelistWatch`（真库：撤 A 不动 B / 审批级联后活跃集恰余 B / 版本 A PAUSED 而 B 仍 FROZEN / 审计一行点名 A / 推送一条点名 A / 不再重复 / 新市场入基线后再消失可被抓）+ `TestSetupRotationAttachesTheDelistWatch`（装配位与 nil 语义）。**同段尾：触发队列消费者已落**（`engine/llm_trigger_consume.go` `TriggerConsumer`——`Engine.Start` 装配后每 30s 排空扫描器队列（上限 50/轮），事件按「市场 → 活跃审批覆盖且版本 FROZEN」路由进 `ShadowRunner.RunOnce`；**READ_ONLY 不排空**、路由不是门禁、单版本失败隔离；4 测试）。**侦察遗留登记**：账户事件生产者（`RaiseAccountEvent` 无生产调用方）与 **PAPER/LIVE 决策→订单桥**（决策行→intent 创建→`OrderDispatcher` 无生产调用方）为仅存接线项。**同段附记（lint 清零，不计分）**：`golangci-lint run ./engine/...` 由 14 条（session-40 控制面文件 5 条 + 本会话新文件 9 条：goconst `reason`×4、iface 冗余审计接缝、gosec WalkDir、testifylint contains×4）清到 **0**。**同段尾二（账户事件生产者 seam，不计分）**：`OrderDispatcher.Events`——受理成功（ACKED 审计后）即 `Events(市场, "order acked: <交易所单号>", now)`（市场=扫描器口径）；**UNKNOWN/拒绝/Shadow 不响**（模糊结果走对账旗标而非再看一眼）、nil 静默；4 路径测试钉住。**真调用方随 PAPER/LIVE 桥装配**（`Dispatch` 现无生产调用方，seam 就位即待桥——已在 §1 明确登记）。**会话级竞态验证**：`go test -race ./engine/`（-skip 网络类）**全绿 109.9s**——本会话新增并发路径（下架监视挂载、消费循环×扫描器队列）无竞态。**同段尾三十五（dualSide venue 侧测试补缺，不计分）**：`binance_test.go` `TestUGetDualSidePosition`（凭证跳过惯例 + NoError）——尾三十一「无独立测试」口子闭合。**同段尾三十四（时钟漂移读面——G 组补读面，不计分）**：`engine/llm_clock_drift.go` `readClockDriftMs`——venue `UServerTime` → 漂移=**幅度毫秒值**（映射拒负、检查 ±limit 只等幅值）；nil/零 as-of/零回包/venue 错全拒；1 测试函数 7 例。**同段尾三十三（装配期取值表固化，不计分）**：§侦察第 11 条末栏——窗口上限 0 照传/连亏 0（回退 3）/冷却无时长→elapsed=true（0=无闸模式）/`FundingRequired` false/G 组逐项定源/排名走 `quoteVolumeRanks`——照表即装、免重议。**同段尾三十二（未对账计数件——`CountOrderIntentsByStatus`，不计分）**：仓库通用状态计数（两方言、缺状态计零负例）+ `buildRiskAccountStateFacts` 补第三孔 `UnreconciledPartials`（`PARTIALLY_FILLED` 计数）——**C 组 `MarginType`/`HedgeMode`/`UnreconciledPartials` 三出口就位**，仅 `ExpectedMarginType`（03 门）与 `TargetAccountID`（取 approval.AccountID）留装配。**同段尾三十一（决策门 03 立档，不计分）**：`ExpectedMarginType` 的「审批假设」设计数据模型无处安放（审批 17 列/冻结信封均无该字段）——立 `decisions/03`（三选项，建议 A：配置默认 CROSS + v19 迁移）。**同段尾三十（对冲模式读面——HedgeMode 二选一取 a，不计分）**：binance `UGetDualSidePosition`（`GET /fapi/v1/positionSide/dual`，同 `GetAssetsMode` 形制；写侧不暴露）+ 引擎 `iDualSideSource`/`readVenueHedgeMode`（nil 拒、venue 错透传）+ `venueAccountModes.HedgeMode` 兑现 + `Assemble` 闭包读入 `f.accountModes.HedgeMode`——**真实事实胜过静默单向假设**；引擎测试 4 例 + binance 包编译核查 + vet/lint 0。**C 组 `MarginType`/`HedgeMode` 双出口就位**。**同段尾二十九（账户模式出口——`venueAccountModes`，不计分）**：`buildSnapshotAccountFacts` 增返回 `venueAccountModes`（`MarginType`=同一 PositionSummary 读的 `.String()`、首读支付；**`HedgeMode` 刻意不申报**——无读面、装配会话二选一收口）+ 闭包存 `f.accountModes`——**C 组 `MarginType` 自此「同读直取」有出口**（测试含 margin 值断言）。**同段尾二十七（冷却检查的数据侧——`LastEntryIntentAt`，不计分）**：仓库 `LastEntryIntentAt(ctx, exchange, pair, asset)`——按市场取最近**开仓**意图时刻（opens only，REDUCE 不计；无行=从未开过仓=过了任何冷却）；两方言 `MAX(created_at)` + 3 负例真库测试；**冷却检查的「上次开仓」半自此可读**（时长半仍为政策输入——装配期定值并记录）。**同段尾二十六（premiumIndex 读面进 Assemble 闭包，不计分）**：`llm_decision_facts.go`——`source.(iPremiumIndexSource)` 守卫（同 `iPositionSummarySource` 同式）→ `buildFundingMarkIndexFacts` 一次读 → `SnapshotRequest.Funding` **影子快照自此真正携带 funding 事实**（此前 funding 类别空挂）+ `f.premium` 存 mark/index 供 02 后 RiskFacts 闭包直取；既有装配测试复跑全绿（成功路径需真交易所，同既有约定）。**同段尾二十五（RiskInput 组装件——元组第一件的可决部分，不计分）**：`engine/llm_shadow_risk_input.go` `buildShadowRiskInput`：十四字段十三路直取已落映射件，两件组装期决定单点化——`CheckVersion` 代码常量盖章（无外部输入面）、`Order` 参数进入（受 02 门——不许编造）；材料齐备缺一即拒；1 测试函数 7 例。**闭包两件现均「材料齐→即插」形态，拍板 02 后仅剩 raise 材料 + Order 事实构造 + 塞闭包位**。**同段尾二十四（SizingInput 装配件——元组第二件先行，不计分）**：`engine/llm_shadow_sizing.go` `buildShadowSizingInput`（**不依赖单型决策 02**）：敞口=`existingExposure(positions)`（C 组同一句式）、日损=`DayStartService.DailyLoss` 产物、止损距离=`ProtectiveStopPrice`（Q8b 公式+远离市场取整）取 |entry−stop|——三块材料单一口径复用；只装开仓（REDUCE 拒绝）、缺 ATR/锚价/tick/日损/仓位读不清各自拒；1 测试函数 12 例。**同段尾二十~二十三（决策门 02 立档 + 接线清单固化与核毕 + 完成度审计，均不计分）**：`decisions/02-auto-execution-order-type.md`（单型未定案——建议 B）；清单第 11 条三栏核毕（grants=`ListGrants`、reconciled=`AccountReconciled` 直接接线；C 组保证金模式装配期定源）；审计快照（`tsc` 0 / `oxlint` 0 / 全仓 build OK）。**同段尾十九（仓储侧账户态事实件，不计分）**：`engine/llm_account_state_facts.go` `buildRiskAccountStateFacts`——`RiskAccountFactsInput` 的仓储切片：日界基线（未封如实留 nil——检查按 `RISK_DAILY_LOSS_UNKNOWN` 拒，发明 0 才是伪造）+ UNKNOWN 意图计数（全表近似同 E 组口径）；`ExternalFlowsUSD` 刻意不在（净额属 `DailyLoss` 既有公式——第二拼写拒）；1 测试函数真库两段 + 两拒绝径。**同段尾十八（排名数据面验证闭合，不计分）**：`Rotator.Step` **每周期批量** `UpdateTickers(ctx, r.cfg.Asset)`（`llm_rotation.go:322-339`——`WeightTickerBatch` 计权、失败不记 `lastTicker` 下轮重试、与 sweep 解耦）→ binance `USDTMarginedFutures` 分支 `U24HTickerPriceChangeStats(EMPTYPAIR)` 全符号 → `ProcessTicker` 灌缓存——**排名取数件的数据面确有生产者（LLM 旋转器自身）**；批间最多一周期（默认 1h）陈旧如实记录（top-50 分类=慢变量、检查消费排名非新鲜度戳，可接受）。**先决四件全部验证闭环**。**同段尾十七（premiumIndex 事实件——先决 ④ 兑现，不计分）**：`engine/llm_funding_facts.go`——`iPremiumIndexSource`+`buildFundingMarkIndexFacts`（binance `/fapi/v1/premiumIndex` 一次读同给 funding 事实与 mark/index：**带符号**费率、结算时刻、`ReadAt`；空回包/非正 mark/index 即拒）+ 快照接缝 `SnapshotRequest.Funding`（builder `applyFunding` 标 **EXCHANGE_REST**——场地直读区别于 account 的 LOCAL_TRACKED）；2 测试函数（含「缺席读不标注=UNKNOWN」钉子）。**装配先决四件自此全部到位**。**同段尾十六（侦察更正：Funding 组无生产者 + 新先决 ④，不计分）**：构建器零 funding 引用实锤——funding+mark/index 同源 premiumIndex `UGetMarkPrice` 合并为一个新件。**同段尾十五（§5.2 D 滑点估算件，不计分）**：`engine/llm_slippage_bps.go` `estimatedSlippageBPS(walk, side, notionalUSD, bestQuote)`——按审批名义额上界走价与最优档比得 bps：**ask=(均价−最优)÷最优×10000、bid 反向**（卖出吃买盘；两侧各有「错误一侧」即拒——盘口不一致不许报 0 静默过闸）、**向上取整**（fail-closed）、最优档缺/非正/未知侧拒；1 测试函数（两侧正例/取整/平坦 0/双侧错误一侧/未知侧/坏材料）。**同段尾十四（24h quoteVolume 排名取数件，不计分）**：`engine/llm_volume_rank.go` `quoteVolumeRanks`——§5.6 `liquidityTopNOnly` 的取数件（检查 `rank>0 && rank<=50` 判过而开关默认 ON——排名缺席=闭闸不是没闸）：过滤本场所本资产、quoteVolume 降序、**同量按对名升序去随机**（同批两次调用必须同表——审计排名可复现）、零量照排（票数<50 时「进前 50」是文档语义）、**空宇宙报错不报 0**（报 0 会让「没读到」与「排名很差」同貌）；数据面=引擎 ticker 缓存（`GetAllActiveTickers`/`GetCachedTicker` 路径）；1 测试函数（排序/平手确定性/异场所异资产排除/大小写宽松/零量/乱序同表/空宇宙拒）。**装配期遗留一验**：USDT-M 的 ticker 是否实际进缓存（未进则需补喂数路径）。**同段尾十三（装配前哨更新 + Q9 常量落地，不计分）**：`RiskMaxSnapshotAgeMs`/`RiskMaxAccountInventoryAgeMs`=60_000 落常量（04 §6/Q9 定案，钉子断言；年龄闸 `<=0` 即拒——装配的正值来源）；**三个装配先决登记**：mark/index 价新 seam（binance `UGetMarkPrice`）或扩展 FundingFacts 二选一、**`QuoteVolumeRank` 无来源且闸默认 ON（装配必须先决）**、`EstimatedSlippageBPS` 走价链可装配。**同段尾十二（`RiskContractSpec` 生产者 + 开关目录读面澄清，不计分）**：`engine/llm_contract_spec.go` `buildContractSpec`——§5.4 合约规格（tick/step/最小量/最小名义额）从**进程内执行限额仓库**取档（`exchange/order/limits`，`UpdateOrderExecutionLimits` 灌入；**侦察单出处更正**：`futures.Contract` 不带 tick/step——限额仓库才是事实源）；**tick/step 非正即拒**（缺了就静默跳过取整/对齐=fail-open）、最小量/最小名义额 0 申报**缺席**（空串——仓库把「未声明」折叠成 0，报空串与检查公开语义一致）、规格值十进制字符串出门（最短往返）；1 测试函数（正例/缺席/tick 缺/step 缺/未装载五径）。**开关目录读面澄清**：`RiskSwitchService.Current()` 已返回填好默认的 `RiskSwitches`——装配侧一行消费，无需新映射件（仅余清单据此收敛为「桥的接线件」）。**同段尾十一（`RiskFacts` E 组频率映射 + 窗口计数仓库查询，不计分）**：仓库层 `CountOrderIntentsInWindow`（intents 按窗口计数，版本/动作两个可选等值过滤、两方言单形状；**候选单不计入**——「would this order breach」由装配侧折入）+ `engine/llm_risk_frequency_facts.go` `riskFrequencyFactsFromState`：新风险计数=**未释放** counters（M1-11 的账，撤单释放即出窗）、版本/全部/平仓三计数读 intents 表（表无账户列→全表即诚实口径里最大的「账户级」近似）、**候选折进计数**（检查 `count <= max` 判过而候选行未落库——不折放行的就是第 max+1 条；任何订单折版本+全部，开仓再折新风险、REDUCE 再折平仓通道）；上限/冷却/连亏显式输入（`<=0`=无闸照传、负数拒）；2 测试函数（计数与折入口径 + 10 拒绝径）。**同段尾十（`RiskFacts` G 组系统映射，不计分）**：`engine/llm_risk_system_facts.go`——halt 作用域从活跃行派生（SOFT/FULL 全局、MARKET 命中才成立）；模式/确认/就绪由调用方读；预算/DB/漂移/限频/manager 显式输入；无限额即拒、负漂移即错。**同段尾九（`RiskFacts` D 组市场映射，不计分）**：`engine/llm_risk_market_facts.go`——盘口优先/行情回落、ATR 比值派生（缺读即错，防 fail-open）、资金费事实直取；mark/index/滑点估算/排名/资金费意图显式输入（禁发明）。**同段尾八（`RiskFacts` A 组授权映射，不计分）**：`engine/llm_risk_authorization.go`——来源全为运行已取材料（不新读）；缺席如实申报；`EnvelopeHashMatches` 对冻结原文重算哈希（防审批后换策略）。**同段尾七（账户事实进快照接缝 + 折叠修正，不计分）**：`Assemble` 闭包先读交易所账户真值（`iPositionSummarySource` 守卫）再构建快照——影子运行快照自此携带 `Account`；`worstSnapshotQuality` 修正排除 `account`（构建器恒标 LOCAL_TRACKED，纳入必全拒）与 `funding`（专属检查+非阻断），排除后无市场类别→UNKNOWN。**同段尾六（`RiskFacts` B/C 组映射，不计分）**：`engine/llm_risk_facts.go`——质量最坏值折叠（`Downgrade` 即全序 min；无标注=UNKNOWN）、年龄=`FreshnessMS`、缺口/盘口有效、两龄上限政策输入；账户/仓位直映射 + venue 字段显式输入；缺读/未来钟/坏侧全拒；7 用例。**同段尾五（`RiskFacts` 链头：M5-08 账户事实生产者，不计分）**：`engine/llm_account_facts.go` `buildSnapshotAccountFacts`——逐启用合约单次摘 `GetFuturesPositionSummary`（同一次读给账户权益+逐仓：entry/mark/清算/PNL/杠杆），市场拼写 `BASE_QUOTE`、side 由 `CurrentSize` 符号推、零仓跳过、**读不到整体拒**；`snapshot.AccountFacts`（快照 107-110 行所指的"另一件"）自此可生产。**同段尾四（桥第一件 `BuildIntentPlan`，不计分）**：`engine/llm_intent_plan.go`——决策记录+候选+行情材料+审批绑定 → `IntentPlan`（三重 ALLOW 生成期再核、OrderType 执行策略给定且 LIMIT 要 §5.4 材料齐/MARKET 免价材/STOP_MARKET 拒、MarketID 三段生成期校验、平仓 LONG/SHORT 归一）；plan 端到端喂 `BuildIntent` 断言取价；顺手把 `"LIMIT"/"MARKET"/"STOP_MARKET"` 与 `"LONG"/"SHORT"` 收口为常量（`OrderType*`/`PositionSide*`，跨 5 文件 12 处——goconst 阈值被新代码顶破后的单一口径修复）。**同段尾三（数量链走价，不计分）**：`SizingInput.Walk` 装配件 `sizingWalkFromDepth`——07 §5 复用 `orderbook` 报价遍历（ask 投报价额 / bid 换回报价额），跨档平均成交价；**填不满（含恰好停在最后一档）拒绝**；nil 盘/坏边/坏名义额各自拒绝；5 断言。

### session-52（2026-10-12）：组合冒烟链预建（七映射 + 两组装件过 `Evaluate`）

**机器链路**（`engine/llm_shadow_chain_test.go` → `TestShadowRiskInputChainComposes`，**七映射 + 两组装件一条链**）：① 授权 `riskAuthorizationFromState`（`riskAuthFixture`：冻结策略 + `RiskEnvelopeHashOf` 现算对齐 + 活跃 grant）→ ② 数据 `riskDataFactsFromSnapshot`（质量折叠 EXCHANGE_REST、两龄上限=Q9 常量）→ ③ 账户 `buildRiskAccountStateFacts`（sqlite 空种子：日界未封=如实 nil、UNKNOWN/部分计数 0）+ `riskAccountFactsFromSnapshot`（场地拼写 `multi`）→ ④ 市场 `riskMarketFactsFromSnapshot`（盘口最优档 + ATR 比率；**滑点=`estimatedSlippageBPS` 现算**=1 bps；mark/index 字面量；`FundingRequired` false 照取值表）→ ⑤ 系统 `riskSystemFactsFromState`（fixture、Mode 对齐 SHADOW）→ ⑥ 频率 `riskFrequencyFactsFromState`（空库窗口 0 + 候选折入=1；冷却无时长→elapsed=true）→ ⑦ 合约规格 `buildContractSpec`（`limits.Load` 独占 venue）→ `buildShadowRiskInput`（`CheckVersion` 代码盖章、**Order=字面量 LIMIT 三字段**）→ `Evaluate`。**断言=表跑过（`Checks` 非空）且无 `RiskInputInvalid`**（领域拒绝可容忍——证明「件件咬合」而非「全闸放行」）；`buildShadowSizingInput` 一并建（敞口 `25050`=max(50000,50100)×0.5、止损距离 `1000`=max(1.5×ATR 750、2% 入场 1000) tick 取整——Q8b 公式同源断言）。

**账目（不计分）**：尾链预建（session-51 各尾同口径）——Σ得分 **105.50 ÷ 107 = 98.6% 不变**；engine LLM 相关测试 **367 → 371**（+3 同段尾三修复测试）。**拍板后动手顺序 ② 自此完成**：① 侦察已读、② 链已建，拍板后从 ③（合成器 + 闭包 + 桥）起步（PROGRESS §1 收口指引已同步）。细节见 workLog session-52。

**同段尾三（同日）：自动响应链拼写断点实锤并修复**（`runMarketID` seam）——`Fact.MarketID`=`snapshot.Pair`（BASE_QUOTE）与审批（大写三段式）**永不命中**、`Build`→`parseMarketPair` **必拒**——自动决策链在门口即断（下架级联同根静默）；修复=归一层双侧归一 + 消费者/delist 两处 `MarketKey` seam，3 新测试钉住、既有期望更新为运行口径。Σ 不变（缺陷修复，不计分）。

**同段尾四（同日）：第三处同源缝**——工具链 scope 拼写断点（`buildScopeToolRunner` 把旋转器 BASE_QUOTE 列表直接入 scope，而工具证据为三段式——生产下每次工具调用必在证据闸被拒）：修复=scope 归一（`runMarketID`，归一失败即装配报错）+ chat 走廊测试实参改生产拼写并加负例断言（就地化为回归钉子）。**触发路由+Build / 下架级联 / 工具 scope 三缝至此全部修复。** Σ 不变（缺陷修复，不计分）。

**同段尾五（同日）：第四处同族**——分析用户消息缺 `marketId`（提示词要求「逐字复制」却未给值——真实模型猜错即 `CANDIDATE_SCOPE_DENIED`）+ 操作员入口四处错拼 `binance:usdtperpetual:...`（提取默认值/三处 placeholder——毒化 scopes 使 A 组必拒；session-43 实测授权行即此错拼）：修复=分析消息补 `Market` + 前端四处统一运行口径；grant/halt 服务零校验（死授权可入库）登记拍板会话。Σ 不变（缺陷修复，不计分）。

**同段尾七（同日）：第五处同族、且拦在决策链最上游**——pre-gate 策略门只认 `"APPROVED"` 而生产版本永远 `FROZEN`（M4-05：审批行即 APPROVED 信号）→ 每次自动运行/手动预览都停在第二道门 `STRATEGY_NOT_APPROVED`；修复=门认 `strategyStateFrozen` + 夹具改产与断言钉住。Σ 不变（缺陷修复，不计分）。

**同段尾九~十（同日）：`LLMSetRunMode` 缺员勘定并补齐**——08 §3.6 运行控制族曾在现网 52 RPC 中唯一缺此端点（ModeSwitcher 零接线）；尾十全栈落地：proto（`/v1/llm/runmode`）+ handler（重认证/服务端三重解锁材料/审计/通知/落定+持久化失败回滚）+ `modeMu` 并发保护 + 真库测试；剩余面=gctcli 命令与 UI 写侧入口。Σ 不变（接口补员，不计分）。

**同段尾八（同日）：五缝合链证明**——`TestAutoTriggerBuildsAProductionShapedRun`：旋转器拼写事件 → 归一 → 真实审批行 → **真 `buildShadowRunRequest`** → 生产口径断言（三段式 MarketID / FROZEN 门态）。Σ 不变（测试增量，不计分）。

## 4. 上会话完成的任务摘要（session-24）

> 同日续跑（session-23 的接力项按 workLog §9.1 落地）。

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-07b`（session-23 §3 已计分，本会话兑现） | `engine/llm_tools.go` + `llm_tools_test.go` 8 个测试 | `LLMToolRunner` 8 个执行器闭环；**四道闸每次产出即执行**（时间/体积/敏感键/深度 + 作用域）——M2-08 接线就地完成；市场 id 三段严格解析；滑点数量加权均价；scope 外市场/账号/策略各自拒绝；详见 session-23 §3 行与 workLog 附件十 |
| `M2-02` K 线缓存 | `engine/llm_kline_cache.go`（`KlineCache` + `fetchCandlesThroughCache` + `iCandleSource`）+ `llm_kline_cache_test.go` 6 个测试 | **已收盘永久 / 未收盘不入缓存 / LRU 整键淘汰 / `InvalidatePair` 清对**（钩子方法已备；**SetExchangePair 调用点与重载喂缓存已在 session-24 收尾接线**——见 workLog §11）。共享取数接组装器与工具执行器：覆盖命中、否则整窗取数并合并收盘段。**`covers` 豁免 `end` 所在成形槽**（否则命中路径永不触发）；取数路径保留成形 bar、命中路径只回已收盘。fake 源计数验证"取数→命中→跨窗重取→再命中"四段；并发 32 goroutine |
| `M2-05` 数据质量标签应用 | `llm/snapshot/quality_apply.go`（`CandleGaps` · `IsZeroCandle` · `ApplyCandleGaps`）+ `CandleFact.Partial` + 组装器接线 + 5 个测试 | **缺口检测**：相邻收盘 K 线间隔 > interval → `GapRange`（含 MissingBars）；**零值补齐检测**（全零 OHLCV=穿着 K 线外衣的缺口）；**蜡烛与指标同时标 GAP**（跨缺口算的指标是插值）。**关键修正**：跳过已降级类目——GAP 与 STALE 同秩，只靠 only-lower 守卫会覆盖更具体的理由（测试钉住 STALE 幸存）。**PARTIAL**：`CandleFact.Partial` + 组装器给成形根置位。**账户=LOCAL_TRACKED**（缓存余额/调用方仓位是本地视图，风控放行前须 EXCHANGE_REST 复核）。**层级修复**：共享取数透传空窗口，各调用方保留自己的哨兵（跨层哨兵污染由既有测试抓出） |
| `M2-09` 工具转录留存 | `engine/llm_tool_transcript.go`（`BuildToolTranscriptJSON` + `ToolLoopResult.TranscriptJSON`）+ 5 个测试 | **预算三限**：≤8 条 / 单条 ≤12KB / 总 ≤32KB；**超限从最旧丢**（复盘要的是最新证据），`Dropped` 计数区分"短转录"与"被截断"；单条超限拒（纵深防御）。**含原始参数**（复盘要回答"模型问了什么"）+ 结果字节独立 SHA-256。**确定性**（同记录同字节）；空转录取 `{"entries":[]}`。测试用 9 条/4×10KB 两组验证两级淘汰与哈希一致性 |
| `M2-11` 回填批次预估 | `llm/coverage/backfill.go`（`BuildBackfillPlan` + `ErrInvalidPlan`）+ 4 个测试 | **纯估算器**：批次 = `ceil(请求 K 线/1000)`（默认 1000/批；单批 ≤3 + 在途 ≤3 双约束），权重复用 `KlineWeight`（**与扫描器同一张表**——预算口径不设第二份）；产出**可直接粘贴的 `gctcli datahistory addjob savecandles` 提案**（引号日期格式，单批请求数取计划值）。**04 §9 文档例题由 `TestBackfillPlanMatchesTheDesignExample` 钉住**（3 年 BTC 1h = 26280 根 / 27 批 / 总权重 270）；`ErrInvalidPlan` 拒缺失市场/倒置区间/非法时长；昵称默认 `<exchange>-<asset>-<base>-<interval>` 且运营者可覆盖 |
| `M2-10` 同源事实传递 | `llm/jev/evidence_guard.go`（`ValidateReviewEvidence` · `CheckEvidenceScope`）+ `evidence_guard_test.go` 8 个测试；`ReviewInput` 新增 `PromptVersion`/`Manifest`/`Now` 三必填 | **06 §4.2 复验在裁判入口成硬闸**：空证据/>8 条/重复 ref/单条 >12KB/总量 >32KB/未知质量标签/敏感键/未来时间逐项拒；**哈希翻译**——`HashBytes(data)` 必须等于清单 `ResultSha256`，"两次调用之间被改过的证据"被拒（改原文与改非规范 JSON 两种改法都钉住）；**作用域闸对证据同样生效**（跨市场/跨策略版本拒）。**逐字节保真链路测试**：组装器字节 → 反序列化 → `BuildReviewRequest` → 判官侧 `bytes.Equal` + 双哈希相等 + 二次构建请求体字节一致。**构造器新增三必填**：`PromptVersion`（非空时必须是 `analysis-readonly-v1`）、`Manifest`（可选，提供即比对）、`Now`（调用方供给——重放可对齐原始窗口，不读系统时钟保纯函数）；因 `Now` 必填，既有 `TestMarshalEnforcesByteCap` 裸字面量补 `Now`（因自己改动而失败的断言更新，非 skip） |
| proto 工具链校准（阻塞项清除，**不计分**） | 重新生成 `gctrpc/rpc.pb.gw.go` + `rpc_grpc.pb.go`（一次性对齐 diff 2 文件 / +169−169，全部为旧版插件的对齐形状） | **根因=提交出自旧插件**（go-grpc 头部 v1.6.1；旧 gateway 的 drain 位置）——逐行核查 336 行无第二种差异形状。**幂等验收**：`buf generate` 连跑两次 `git diff gctrpc \| shasum` 两次同为 `80497e7459dc694c025d64122999e68b36524f1b`。`go build ./...` 通过。建议单独提交「仅重新生成」。解锁 `M1-14`/`M3-05`/`M4-15`/`M5-14`/`M5-17`/`W1-07` |
| `M3-01` 会话存储 + AES-GCM 加密 | `llm/chat/cipher.go`（`ConversationCipher` · `Seal`/`Open` · `LoadOrCreateConversationKey`）+ `cipher_test.go` 7 个测试 + 仓储真库往返测试 | **Q17=C 落地**：标题/元数据明文、正文 AES-256-GCM（`content_cipher`/`content_nonce`）、明文 SHA-256 存 `content_sha256`、**AAD=message_id**——密文搬到别的行打不开（单测 + `TestChatStorageRoundTripsEncryptedBodies` 真库双行互换必拒）。每次封新 nonce；`Open` 区分认证失败（`ErrDecrypt`）与摘要不符（`ErrDigestMismatch`）；密钥文件 32 字节 0600、`O_EXCL` 创建（双进程竞争败者重读不覆写）、错尺寸拒绝而非重建。`llm/` 成**十一包** |
| `M3-02` 上下文重建 | `llm/chat/context.go`（`BuildContext` · `MaxContextMessages`/`MaxContextChars`）+ 7 个测试 | **只收 user 行**（损坏的 assistant 行不碰——不掩盖用户指令）；**按 seq 排序**且不改调用方切片；**超限拒绝不截断**（257 条 / 16001 字符 → `ErrContextTooLarge` 带提示语，05 §8.1 的"明确告知"）；**字符=rune**（与字节计数的区别有测试钉死）；边界含等号（恰 256/16000 放行）；**坏 user 行炸错不跳过**（错误带行 id）；空指令 `ErrContextEmpty` |
| `M3-03` chat 模式流水线 | `engine/llm_chat.go`（`ChatPipeline.Run` + 3 个仓库适配器）+ `llm_chat_test.go` 7 个测试 | **共享工具集与循环**：`RunToolLoop` + 8 只读工具，**`ResponseFormat` 留 nil**（自由文本）——`ToolLoopRequest.ResponseFormat` 从硬编码改为字段（决策侧显式传 json_object，既有断言随夹具更新）；**先验证后落库**（前瞻上下文含新句过上限：超限库零写入/预算零占用/provider 零调用）；**预算在环**（CHAT 预约→聚合拆分结算——`ToolLoopResult` 新增 `UsageInputTokens`/`UsageOutputTokens`；失败释放且不留助手行；HOLD 结算+`error_code` 记因）；问句与回复均密封（M3-01），助手行带用量/延迟/工具转录；Jev 修订版拒 chat |
| `M3-04` 证据卡片结构 | `engine/llm_chat_cards.go`（`EvidenceCard` · `EvidenceCards`）+ 2 个测试 | **卡片=转录的显示视图**（转录里存的就是完整 `ToolEvidence` 信封——`LLMToolRunner.evidence()` 的产物，所以提取是解码不是猜测）：`market + sourceTime(asOf) + snapshotId + dataQuality + ref + sha256`（11 §M3-04）；**非信封结果跳过不炸回合**（参数回填反馈串不是证据）；空信封/缺 ref 不算卡片；`ChatRunResult.Evidence` 与 08 §3.1 的 `LLMSendMessageResponse.evidence` 字段对齐，M4 决策侧可复用同一提取器 |
| `M3-05` conversations/messages 6 RPC | `gctrpc/rpc.proto`（17 message + 6 RPC）· `engine/llm_chat_service.go`（`LLMChatService` · `SetupChat` · `ActiveLLMRevisionID`）+ 6 个测试 · `engine/rpcserver_llm.go`（6 个转换 handler）+ 3 个测试 · `Engine.llmChatService` 字段 | **proto 新端点首次实战**：`buf generate` 纯增量 diff、幂等再验收（`95883d6f…` 两次一致）。服务层与 RPC 层分离（转换零逻辑）；`SetupChat` 首次使用落 32B 0600 密钥；SendMessage 走激活配对 LLM 边；**未装配时 RPC fail-closed**（空列表与子系统坏了不许长得一样）。生产装配（client factory）记录为待办 |
| `M3-06` gctcli REPL | `cmd/gctcli/llm_chat.go`（`llmchat` · `runChatREPL` · `llmChatSession`）+ 3 个测试 | **`bufio.Reader` 非 Scanner**（行上限会把长指令切两半）；每轮 `LLMSendMessage`、超时 30s/`--it` 120s；**转录+卡片实时打印**（回复后：工具名/参数/sha 前 12 位 + 卡片行）、`:evidence <ref>` 打全卡 JSON+sha256；坏转录/未知 ref/回合报错全部降级提示不结束会话；`:quit` 后零发送（测试钉住）；命令注册进 `app.Commands` |
| `M3-08` chat 安全边界 | `engine/llm_chat_safety_test.go` 3 个测试 | **机器化边界优先于提示词边界**：① `reflect` 白名单遍历 `ChatPipeline`/`ChatRunResult` 字段（Order/Decision 型字段出现即失败——类型层面无法下单）；② 提示词逐字含三段拒绝语且不含决策契约；③ 「直接帮我开个 BTC 多单」=普通对话：拒绝文本密封落库、零工具调用、预算正常结算、系统提示每轮带 refusal framing |
| `M3-07` 真流式对话通道 | `llm/openai/stream.go`（SSE 客户端 · `StreamingCompleter` · `ErrStreamInterrupted`）+ 5 个测试 · `ToolLoopRequest.OnDelta` · `ChatPipeline.RunStream`/`SendMessageStream` + 4 个测试 · `LLMChatStream` proto+handler + 2 个测试 | **半截 JSON 不解析**（空行整帧才 decode）；**tool_call 分片按 index 聚合还原**（与非流式同形——循环零改动跑在流式轮上）；**usage 迟达显式索取**（`stream_options.include_usage`，末帧计费，不来不编造）；**中断=错误**（`[DONE]` 缺失 → `ErrStreamInterrupted`；**零助手行、预算释放**——05 §1"中断时不产生半成品 chat_message"）；handler 中断时**无 final 事件**；决策路径恒非流式（`OnDelta` 只能由 chat 置位）|
| `M3-09` Web UI 消费流式 | `proxy/gct-proxy.mjs` 流式直通 + `src/app/api/sse.ts` + `api/chat.ts` + `pages/ChatPage.tsx` + `scripts/check-sse-parser.mjs` + `scripts/mock-gateway.mjs`（开发用）| **三层贯通**：代理对 chunked 无 Content-Length 的 2xx 边收边转（缓冲=伪流式）；前端按行累积整行 decode（6 项自查含逐字节切碎）；ChatPage 流式增量渲染 + 证据卡片 Tag。**浏览器实测**（mock 网关栈）：发送→流式→final 证据→回放；typecheck/oxlint/build 全过 |
| `M4-01` 策略条款提取（M4 开工） | `engine/llm_policy.go`（`PolicyExtractor` · `decodePolicyStrict`）+ `llm_policy_test.go` 7 个测试 + `TestPolicyPromptListsEveryPolicyProposalKey`（reflect 全键⊆prompt） | **缺边界→澄清**（null 限额 = 没答，模型不问也算）；**矛盾→提问不静默选值**（questions 保留、矛盾值 null）；**选择集服务端闸**（scope ⊆ 选定市场）；不持久化（冻结另属 M4-02）；**契约漂移两修**：prompt↔结构体键名（`maxNewRiskIntents`）与 prompt 漏 `marketType`——严格 decode 下都会让真输出必拒；计费：provider 答了即结算、仅传输失败释放 |
| `M4-02` 策略版本冻结 | `engine/llm_strategy_freeze.go`（`StrategyFreezer.Freeze` · `RiskEnvelopeHashOf`）+ 7 个测试 | **确定性序列化**（结构体声明序、零空白；**golden 字面量**钉住规范 JSON 与哈希——漂移即失败）· **双哈希入行** + `RiskEnvelopeHashOf` 运行时重算与冻结一致（07 §2.2 比对的一半）· **前置状态闸**（提案须 READY_FOR_REVIEW）· **二次冻结拒** · 契约全检（10 真/假授权、7 天窗、永续杠杆、双边修订版）· **审计行丢失要报错**（冻结已持久但 transition 写失败：带 context 报错不许吞）；actor 不进哈希 |
| `M4-03` 版本树与 diff | `engine/llm_strategy_tree.go`（`BuildVersionTree` · `DiffPolicies`）+ 3 个测试 | **历史是树**（分叉合法；子序=创建时间再 id 全序）；**悬空父链拒**（缺失父=腐坏不糊弄）；**环检测两态**（纯环=无根 + 环旁健康链=可达计数暴露——只查无根会丢旁路环）；**diff 确定性**（结构体字段序、两次字节一致；市场排序报增删与动作集；money nil=空串——同行为同读法）；只读视图 |
| `M4-04` 逐市场授权 | `engine/llm_market_grants.go`（`MarketGrantService` · `Encode/DecodeApprovedMarkets`）+ 4 个测试；仓储 `GetMarketTradeGrant` / `ListActiveStrategyApprovals` + e2e（`approvalFromPSQL` 抽取）| **第二道闸**（看盘≠能交易）：1..30 天窗口硬顶；**活体唯一**（同 account+network+market 至多一条有效；过期/已撤不挡新——三态钉住）；**撤销级联到有效审批**（同账号+网络+市场清单命中；四面钉住不误伤；坏清单炸错不跳过）；二次撤销拒；**编解码单一拼写**供 M4-05 复用；运行面暂停留 M4-14/M5 |
| `M4-05` 逐版限时审批 | `engine/llm_approval.go`（`ApprovalService.Approve`）+ 8 个测试 | **审批=接受冻结信封**（风控 JSON/哈希复制自冻结版本，无改限额输入面）· **8 要素齐发**（窗口≤7 天、模式四选一、市场唯一非空、幂等键必填）· **重新认证先行**（空密码/验证失败在任何写入前截断）· **双 epoch 闸**（请求 CAS + 信封自身 pairEpoch=当前——`ErrPairEpochConflict`/`ErrStalePolicyPair`）· **市场 ⊆ 冻结范围 + 活体授权**（`ErrMarketNotGranted`）· **幂等=同绑定重放**（窗口属绑定；不同窗口=冲突）· 审计失败要报错 · 版本状态保持 FROZEN（审批行=APPROVED 单一事实源）|
| `M4-06` 触发调度器 | `engine/llm_schedule.go`（`EvaluationScheduler`）+ 4 个测试 | **每版本 1 次/分钟**（窗口内只跑第一次；后续 `SKIPPED_RATE_LIMITED` + `RetryAfter`，不耗预算）· **跳过不延窗**（防触发器风暴饿死评估）· 版本间独立 · `Forget`/`Prune` 有界（按活跃版本数）· **并发 32 同刻恰一人胜**；24h 上限在 M1-11、全局截止在 M4-08 |
| `M4-07` 前置门禁 | `engine/llm_pregate.go`（`EvaluatePreGates` 纯函数）+ 4 个测试（19 个子测试） | **七道零成本闸按 03 §6 文档序**（模式→策略状态/窗→readiness→探针→halt→预算→对账）；**未知 readiness 归 INCOMPLETE**、**TTL 未配置即 stale**（fail-closed）；**多故障取文档序第一个**（decision_run 的 reason 稳定）；缺时钟=事实集不完整拒；纯函数零 I/O |
| `M4-08` LLM 分析流水线 | `engine/llm_analysis.go`（`AnalysisPipeline` · `AnalysisOutcome`）+ 7 个测试 | **组装序**：门禁→快照（`MayDecide` 或类目降级即 HOLD，不给模型付钱）→预算→循环（**双时钟**min(90s, SourceTime+60s)）→候选闸→safety。**候选闸**：严格 decode + Validate（**UUID 双 ID + refs[0]=snapshotId** 的契约发现）+ 三 ID 对照 + 工具 ref ⊆ 本回合转录；非 HOLD 须引用 ≥1 工具证据且 entry/exit 双非空；**风险候选仅在 EXCHANGE_REST 证据上放行**，否则 REVIEW。计费：未触 provider=释放；跑过循环=结算 |
| `M4-09` Jev 评估流水线 | `engine/llm_jev.go`（`JevPipeline.Review` · `iJevJudge`）+ 6 个测试 | **调用位**：复验（§4.2 全套内建在 `BuildReviewRequest`）→**64KiB 在预算之前**→预约（JEV）→`SystemOne`→结算→**结束前重查 epoch**（`STALE_PAIR` 作废评估）；拒绝=outcome+ReasonCode+Detail（非 error）；证书哈希 `RequestSha256` = 送审字节独立重算；**同源逐字节在真请求上断言**（judge 收到的 State 反解比对原证据）|
| `M4-10` 决策合成 | `engine/llm_synthesis.go`（`Synthesize` · `SynthesisThresholds`）+ 15 个测试 | **§7 文档序**：分析师 HOLD/REVIEW 透传（安全闸转人工的候选到不了裁判）→ 候选 HOLD→`MODEL_HOLD` → 裁判失败三分类（`errors.Is` 词表）→ 阶段拒绝码透传（`STALE_PAIR` 等，Detail 保留）→ `JEV_HOLD`/`JEV_REQUESTS_REVIEW` → **§7.1 矩阵** 6 行 + 未知答案保守归分歧 → 证据 <2 / 对齐 <0.5 / **分离度 <0.10** 三闸 → `AWAITING_HARD_RISK_AND_APPROVAL`。**永不 ALLOW**（8 场景总括断言出口只有 HOLD/REVIEW）；阈值零值回退文档默认；`jev.Criteria*` 导出 + 判官侧覆盖测试 |
| `M4-11` 硬风控引擎 | `engine/llm_risk.go`（`Evaluate` · `RiskSwitches` · `RiskInput`/`RiskResult`）+ 17 个测试 | **A–G 61 码文档序全落表**（每项检查结果入册 `Checks`）；**REDUCE 豁免**新增风险检查（平仓通道不被开仓限额堵死，系统/数据/保护仍适用）；**ALLOW 唯一产地**（模型与分诊只到 REVIEW）；敞口=max(entry,mark)×qty 保守名义额、日损=基线±划转−权益（十进制全程）；拒绝带 actual-vs-allowed/unit/field（§5.5）；**§5.6 开关目录** 12 ON/4 OFF 且关一开关只移除该条检查；反向开仓=`REVIEW`（硬型拒绝不降级） |
| `M4-12` 数量重算 | `engine/llm_sizing.go`（`ComputeQuantity` · `Walk` 缝）+ 9 个测试 | **九步逐级取 min**（候选↔审批→敞口→日损经止损距离反推；`Caps` 台账）；**Q13 容差 step**（floor 后再减一个 step，0.02→0.0199）；**第 9 步实际名义额重跑滑点**；拒绝码与硬风控同词表；REDUCE 不走本链（平仓数量归退出策略） |
| `M4-13` 审计链 | `engine/llm_decision_record.go`（`DecisionRecorder` · `LLMDecisionStore`）+ 8 个测试 | **三表一次落账**：decision_run →（有硬风控才落）risk_evaluation → audit `DECISION_RECORDED`；**写入失败中止整链**（不许出现"没审计的下单"）；决策映射=风险 verdict 覆盖分诊、reason=第一条拒绝码；`input_hash`（规范化拼接的 SHA-256）测试钉住确定性 |
| `M4-14` 配对切换级联 | `engine/llm_pair_switch.go`（`PairSwitchCascade`）+ 4 个测试 | **撤审批+停策略+迁移行+`PAIR_SWITCHED` 审计**（reason=`PAIR_SWITCHED`、时刻=注入时钟）；零活动审批也写事件；失败中止不写审计；进行中运行靠 epoch 重查自废（不加新表） |
| `M4-16` SHADOW 组装位 | `engine/llm_shadow.go`（`ShadowRunner`）+ 5 个测试 | **全链组装**：去抖→门禁（拒绝落账、零成本）→分析→Jev→合成→（awaiting 位）风控+重算→审计链；**无订单接缝**（ALLOW=「若可执行会走到订单落地」）；跳过不落账；构建失败走 JevError→JEV_UNAVAILABLE。次序：先 16（可离线验收）后 15（proto 大件） |
| `M4-15` strategies/decisions RPC + gctcli | `gctrpc/rpc.proto`（+30 消息/16 RPC）· `engine/rpcserver_llm_strategy.go`（16 handlers）· `cmd/gctcli/llm_strategy.go`（17 叶命令）+ 4 个测试 | **读写同源**（写走内部同服务、读直连仓储）；审批重认证=RemoteControl.Password 常时比较；resume 需活审批（`ErrApprovalRequired`）；trigger/preview fail-closed；lineage=A12/A13 复盘面；buf 两次同哈希 `52eb7d45` |
| `M5-01` 订单意图 | `engine/llm_execution.go`（`BuildIntent` · `IntentRecorder`）+ 8 个测试 | **三重 ALLOW 把关**；方向=Buy/Sell（REDUCE 需仓位方向）；限价 tick 保守取整 + 盘口界；`gct-llm-` 幂等键；10s 有效期（上限 2min）；落库+`ORDER_INTENT_PREPARED` 审计失败中止 |
| `M5-03` 提交映射 | `engine/llm_order_map.go`（`BuildOrderSubmit`）+ 5 个测试 | **Buy/Sell 只用二者**；**Leverage=0**；**Q13 回读重验**（转换不许变大）；STOP_MARKET→`TriggerPrice`；LIMIT 必须带价；`RetrieveFees`+500ms；幂等键直通 |
| `M5-02` 执行分流 | `engine/llm_dispatch.go`（`OrderDispatcher`）+ 6 个测试 | **白名单**：READ_ONLY/SHADOW 不执行；PAPER→fake（开校验）；TESTNET/LIVE→venue；**失败=UNKNOWN**（疑罪从有、绝不盲重试）；回执缺详情=UNKNOWN；成功→ACKED+`ORDER_SUBMITTED` |
| `M5-04` 提交前新鲜度 | `engine/llm_dispatch.go` 增补（`IntentSubmitMaxAge` · `EXPIRED`）+ 1 个测试 | **过期或超 5s 一律 EXPIRED**（venue 零调用、无提交审计——陈旧授权绝不长得像场地故障）；边界=5s 放行；SHADOW 先短路不误标 |
| `M5-05` 状态机 + 认领 | `database/repository/llm/order.go`（`ClaimOrderIntent` 条件 UPDATE）· `engine/llm_dispatch.go`（`intentTransitions`）+ 3 个测试 | **迁移表 10 态**（终态无出边、UNKNOWN→SUBMITTING 这条边不存在）；**原子认领先于 venue**（丢标拒、生命周期归夺标者）；跳步被拒 |
| `M5-06` 保护性退出 | `engine/llm_protective_exit.go`（`ProtectiveStopPrice` · `ProtectiveExitEnforcer`）+ 5 个测试 | **止损=较紧者（ATR×1.5 vs 2%）+ tick 远离市场取整**；STOP_MARKET/ReduceOnly/反向；**fail-safe 两连**（挂失败→市价平；再失败=UNKNOWN 上抛）——绝不留裸仓 |
| `M5-07` 退出生命周期 | `database/repository/llm/order.go`（`ListOpenProtectiveExits`）· `engine/llm_exit_lifecycle.go`（`ExitTracker`）+ 4 个测试 | **跨重启快照**（读回 PREPARED/PLACED）；**venue 校准经 6 态迁移表**（不可分类=UNKNOWN 绝不读作已结算）；**仓位平了撤止损**（撤失败=UNKNOWN 上抛——已不存在仓位上方的止损会反手开仓） |
| `M5-08` 对账 | `engine/llm_reconcile.go`（`Reconciler` · `DiffPositions`）+ 4 个测试 | **数值等价**（0.1=0.10、缺边=0、不可读≠匹配）；差异排序输出；清账=+5min 有效期；差异=SOFT halt+审计（三重落账失败中止）；交易所抓取为调用方 I/O |
| `M5-09` UTC 日界基线 | `engine/llm_day_start.go`（`DayStartService`）+ 5 个测试 | **一日一封**（二次落基线拒）；划转 IN/OUT 校验；日损=基线±划转−权益（盈利读负）；**缺失即拒**（基线/划转不可读=UNKNOWN）；越限=FULL halt+审计（人工解除） |
| `M5-10` HaltController | `engine/llm_halt.go`（`HaltController`）+ 5 个测试 | **三级**：SOFT 不撤挂单（保护性止损活过 soft stop）、MARKET 带市场、FULL 拦一切；**只撤机器人单**；序列落 halt 立即生效→停策略→撤单→（二次确认）平仓→审计；**清除需认证+原因+不自动恢复** |
| `M5-11` 事件通知 | `engine/llm_events.go`（15 事件常量 · `LLMNotifier`）+ 4 个测试 | **消息成文**（每个事件回答「刚做了什么、我该看哪里」）；**两处响铃**（unknown=high、止损失败=critical）；**Emit 永不失败永不阻塞**（通知故障不许拦执行）；webhook 通道包归 M5-16 |
| `M5-12` 模式切换 | `engine/llm_mode.go`（`ModeAllows` · `UnlockLive` · `ModeSwitcher`）+ 3 个测试 | **§5.1 矩阵逐格 fail-closed**（未知=否）；**LIVE 三重解锁缺一不可**（null 金额阻止授权）；切换=重认证+`MODE_SWITCHED` 审计+`llm_mode_switched` 通知 |
| `M5-13` 网络一致性 | `engine/llm_network_check.go`（`CheckNetworkConsistency`）+ 3 个测试 | **三方一致两向钉住**；https 强制；非交易模式豁免；未知模式 fail-closed；八条错配全拒 |
| `M5-14` 可观测性 | `engine/llm_metrics.go`（`LLMMetrics`）+ proto `LLMGetMetrics` + 4 个测试 | **A+C**（日志 seam + RPC）；标签排序折叠；counter/gauge/histogram；语义记账器封闭词表；RPC fail-closed；nil 安全 |
| `M5-15` 崩溃恢复 | repo `ListOrderIntentsByStatus` · `engine/llm_recovery.go`（`CrashRecovery`）+ 3 个测试 | **SUBMITTING→UNKNOWN+需对账**（唯一写入者=认领者，留下必是没结果）；过期 PREPARED→EXPIRED；响铃+gauge+审计每轮都写 |
| `M5-16` webhook 通道 | `communications/webhook/`（`Webhook` · `ParseMessage`）+ `base.WebhookConfig` + 注册 + 4 个测试 | **Q19 三件套落地**（Telegram 主 + 通用 webhook 接自建告警）；载荷反解结构化；3 次指数退避固定政策；非 2xx 失败上抛 |
| `M5-17` 风控开关目录 | `engine/llm_risk_switches.go`（`RiskSwitchCatalog` · `RiskSwitchService`）+ proto `LLMSetRiskSwitch` + 5 个测试 | **18 行=编译期清单**；**三把锁不可 RPC 关**；翻转即 `CONFIG_CHANGED` 审计；**审计失败回滚**；默认逐位等于引擎默认；RPC fail-closed |
| `M5-18` REVIEW 过期 | repo `ListReviewRunsBefore` · `engine/llm_review_expiry.go`（`ReviewExpirySweeper`）+ 2 个测试 | **12h 默认线可收紧**；**EXPIRED=审计+推送+不执行**；**宽限窗幂等**；审计失败不推送 |

**校验**：`go build ./...` · `go vet ./engine/` · `go test ./llm/snapshot/ -count 1`（全过，含 5 个新缺口测试）· `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过（85.3s）** · `go test ./engine/ -run 'TestKlineCache|TestFetchCandlesThroughCache'`（6 全过）· `go test ./engine/ -run 'TestLLMTools'`（8 全过）· `go test ./engine/ -run 'TestTranscript|TestLoopResultTranscriptJSON'`（5 全过）· `go test ./llm/... -count 1` 十包全绿 · markdownlint 0 issues。**M2-11 + M2-10 续跑（同日）**：`go test ./llm/jev/ ./llm/coverage/ -count 1`（两包全绿，含 8 + 4 个新测试）· `go test ./llm/... -count 1` 十包全绿（**253 个顶层测试函数 / 383 条 PASS（含子测试）**）· `go vet ./llm/... ./engine/...` 无输出 · `go build ./...` 通过。**缓存接线收尾**：`go test ./engine/ -run 'TestFullReloadFeedsTheKlineCache|TestEngineInvalidatesTheLLMCachedPair|TestSetupRotationHandsTheManagerCacheToTheRotator' -count 1`（3 全过）· `go test ./engine/ -count 1 -skip '<既有网络类测试>'` 整套通过（83.6s）。**proto 校准**：`cd gctrpc && buf generate` 连跑两次（`git diff gctrpc \| shasum` = `80497e7459dc694c025d64122999e68b36524f1b` 两次一致）· 一次性对齐 diff = 2 文件 / +169−169 · `go build ./gctrpc/ && go vet ./gctrpc/ && go build ./...` 通过。**M3-01**：`go test ./llm/chat/ -count 1`（7 全过）· `go test ./database/repository/llm/ -run TestChatStorageRoundTripsEncryptedBodies -count 1`（真库）· `go test ./llm/... -count 1` 十一包全绿（**273 个顶层测试函数 / 403 条 PASS**，chat 包 14 个）。**M3-03**：`go test ./engine/ -run 'TestChatRun' -count 1`（7 全过）· `go test ./engine/ -run 'TestToolLoop' -count 1`（循环既有测试 8 个全过——ResponseFormat 字段化后夹具更新）· `go test ./engine/ -run 'TestChatRun|TestEvidenceCards' -count 1`（9 全过）· `go test ./engine/ -run 'TestChatService|TestSetupChat' -count 1`（6 全过）· `go test ./engine/ -run 'TestLLMConversationRPC|TestLLMSendMessageCarries' -count 1`（3 全过）· `go test ./cmd/gctcli/ -count 1`（整套 + 3 个 REPL 测试全过）· `go test ./engine/ -run 'TestChatSafety' -count 1`（3 全过——一处测试片段跨了提示词换行，收窄断言即好）· `go test ./llm/openai/ -count 1`（含 5 个流式新测试全过）· `go test ./engine/ -run 'TestChatStream' -count 1`（4 全过）· `go test ./engine/ -run 'TestLLMChatStream' -count 1`（2 全过）· `go build ./...` 通过。**M3-09**：`node scripts/check-sse-parser.mjs`（6/6，含逐字节切碎）· `npm run typecheck` · `npm run lint`（oxlint 0 警告）· `npm run build` 通过 · **M4-01**：`go test ./engine/ -run 'TestPolicyExtraction' -count 1`（7 全过）· `go test ./engine/ -run 'TestStrategyFreeze|TestRiskEnvelopeHash' -count 1`（7 全过）· `go test ./engine/ -run 'TestBuildVersionTree|TestDiffPolicies' -count 1`（3 全过）· `go test ./engine/ -run 'TestMarketGrant|TestApprovedMarkets' -count 1`（4 全过）· `go test ./engine/ -run 'TestEvaluationScheduler' -count 1`（4 全过——一处断言方向写反被自纠：新窗口不该被 Prune 剪掉）· `go test ./engine/ -run 'TestPreGates' -count 1`（4 全过，含 19 个单点失败子测试）· `go test ./engine/ -run 'TestAnalysis' -count 1`（7 全过，含 7 个子测试——夹具三修：UUID 双 ID、refs[0]=snapshotId、跨快照用例的 refs 自洽，全是契约发现而不是放松断言）· `go test ./engine/ -run 'TestJevReview' -count 1`（6 全过）· `go test ./engine/ -run 'TestSynthesize' -count 1`（15 全过，含 12 个子测试——分诊表逐行）· `go test ./engine/ -run 'TestRisk' -count 1`（17 全过，95 条 PASS 含子测试——A–G 逐码 + 开关 + REDUCE 豁免 + 拒绝形状）· `go test ./engine/ -run 'TestSizing' -count 1`（9 全过，20 条 PASS 含子测试——九步 + 容差 step + 滑点重跑）· `go test ./engine/ -run 'TestDecision' -count 1`（8 全过，11 条 PASS——三表落账 + 决策映射 + 哈希敏感性）· `go test ./engine/ -run 'TestPairSwitchCascade' -count 1`（4 全过——级联四件套 + 零活动 + 失败中止 + 误用）· `go test ./engine/ -run 'TestShadowRunner' -count 1`（5 全过——门禁+去抖 + 分析 HOLD + 全链 ALLOW + 构建失败 + 误用）· `go test ./engine/ -run 'TestLLMStrategyRPC|TestOperatorPassword|TestStrategyAndDecisionConverters' -count 1`（4 全过——fail-closed 三径 + 输入校验 11 径 + 重认证 + 转换器）· `go test ./cmd/gctcli/ -count 1` 全过 · `go build ./...` 通过 · `go test ./engine/ -run 'TestBuildIntent|TestIntentRecorder|TestLimitPrice|TestClientOrderID' -count 1`（8 全过——三重把关 + 方向映射 + 限价界 + 幂等键 + 落库中止） · `go test ./engine/ -run 'TestBuildOrderSubmit|TestFloatWithin' -count 1`（5 全过——三铁律 + 止损触发价 + 8 条校验路径） · `go test ./engine/ -run 'TestDispatch' -count 1`（6 全过——白名单 + PAPER 校验位 + UNKNOWN 两张面孔 + 误用） · `go test ./engine/ -run 'TestDispatch' -count 1`（7 全过，含新鲜度边界=5s 与 SHADOW 短路不误标）· `go test ./engine/ -run 'TestUnknownIsNeverResubmitted|TestIntentStateMachineTransitions' -count 1`（3 全过，含迁移表 23 例——9 条拒绝边逐个钉住） · `go test ./engine/ -run 'TestProtectiveStopPrice|TestBuildProtectiveExit|TestEnforce' -count 1`（5 全过——较紧者两例 + tick 取整 + 挂单 + fail-safe 两连） · `go test ./engine/ -run 'TestExitTracker' -count 1`（4 全过——快照 + 状态映射 6 例 + 撤止损 + 撤失败 UNKNOWN） · `go test ./engine/ -run 'TestDiffPositions|TestReconcile' -count 1`（4 全过——数值等价 + 排序 + 清账/差异两分支 + 失败中止） · `go test ./engine/ -run 'TestUtcDayKey|TestSealIsOnce|TestRecordFlowValidates|TestDailyLossMath|TestTripDailyLossHalt' -count 1`（5 全过——一日一封 + 日损三例 + 缺失即拒 + FULL halt） · `go test ./engine/ -run 'TestHalt' -count 1`（5 全过——三级语义 + 二次确认 + 序列中止 + 认证清除不自动恢复） · `go test ./engine/ -run 'TestEmitFormats|TestTheTwoLoudBells|TestDecisionRejectCarries|TestEveryEventType' -count 1`（4 全过——15 事件全可达 + 两处响铃 + actual-vs-allowed） · `go test ./engine/ -run 'TestModeCapabilityMatrix|TestUnlockLiveTripleGate|TestModeSwitchAuth' -count 1`（3 全过——矩阵逐格 + 三重闸逐条 + 切换程序） · `go test ./engine/ -run 'TestNetworkConsistency' -count 1`（3 全过——两向一致 + 八条错配 + 豁免） · `go test ./engine/ -run 'TestMetricStoreMath|TestSemanticRecorders|TestSnapshotJSONAndLogSeam|TestLLMGetMetricsRPC' -count 1`（4 全过——三种数学 + 语义记账 + 日志 seam + RPC 两态） · `go test ./engine/ -run 'TestRecover' -count 1`（3 全过——认领清 UNKNOWN + 干净重启 + 失败中止） · `go test ./communications/... -count 1`（六包全过，含新 `webhook` 4 测试——载荷 + secret 头 + 重试逐档 + 连接校验） · `go test ./engine/ -run 'TestRiskSwitch|TestLLMSetRiskSwitchRPC' -count 1`（5 全过） · `go test ./engine/ -run 'TestReviewExpiry' -count 1`（2 全过） · `cd gctrpc && buf generate` 连跑两次同哈希 `f3ecb7ad73006d89a04b6b8a72f320b12db2971d`· `go test ./cmd/gctcli/ -count 1` 全过 · `cd gctrpc && buf generate` 连跑两次同哈希 `09119dc3f2dae60009fa08bdf8256f22138c3fd2` · `cd gctrpc && buf generate` 连跑两次（`git diff gctrpc \| shasum` = `52eb7d4587866e187e92cd6737fc5b001aab8df5` 两次一致）· `go test ./llm/jev/ -run 'TestExportedCriteria' -count 1`（1 全过）· `go test ./engine/ -run 'TestApproval' -count 1`（8 全过——两处测试自纠：stale-policy 场景要先让 CAS 过去；重放判定补上"窗口属绑定"，改的是断言与谓词而不是放松约束）· `go test ./database/repository/llm/ -count 1`（14 个 e2e 全过——夹具一度复用同一版本 id 被 UNIQUE(strategy_version_id) 拒绝：schema 在替"一个版本一次审批"把门，修夹具不改约束）· `go test ./llm/openai/ ./llm/candidate/ -count 1`（含防漂移测试全过）· **浏览器实测**（mock 网关 + 代理 + vite：发送「BTC 现在的结构如何？」→ 流式渲染 → 证据卡片 Tag `getCandleWindow｜…｜EXCHANGE_REST` 显示、回放持久）。**编译修复两条**：① 证据保真测试里 `request.State` 是 `any`，补 `ReviewState` 类型断言（`state, ok := request.State.(ReviewState)`）；② 新构造器必填 `Now` 后 `TestMarshalEnforcesByteCap` 的裸 `ReviewInput` 字面量补 `Now`（因自己改动而失败的断言更新断言，非 skip）

**账目说明**：session-23 收尾 44.4%（M2 组 10.5/16，3.94%）。本会话：① `M2-02` 新增计分：M2 组 10.5/16 → **11.5/16**（3.94% → 4.31%）；② `M2-05`：→ **12.5/16**（4.31% → 4.69%）；③ `M2-09`：→ **13.5/16**（4.69% → 5.06%）；④ `M2-11`：→ **14.5/16**（5.06% → 5.44%）；⑤ `M2-10`：→ **15.5/16**（5.44% → 5.81%）；⑥ `M3-01` 新增计分：M3 组 0/9 → **1/9**（0.00% → 0.44%）；⑦ `M3-02`：→ **2/9**（0.44% → 0.89%）；⑧ `M3-03`：→ **3/9**（0.89% → 1.33%）；⑨ `M3-04`：→ **4/9**（1.33% → 1.78%）；⑩ `M3-05`：→ **5/9**（1.78% → 2.22%）；⑪ `M3-06`：→ **6/9**（2.22% → 2.67%）；⑫ `M3-08`：→ **7/9**（2.67% → 3.11%）；⑬ `M3-07`：→ **8/9**（3.11% → 3.56%）；⑭ `M3-09`：→ **9/9**（3.56% → 4.00%，**M3 组完成**）；⑮ `M4-01`：M4 组 0/16 → **1/16**（0.00% → 0.50%，M4 开工）；⑯ `M4-02`：→ **2/16**（0.50% → 1.00%）；⑰ `M4-03`：→ **3/16**（1.00% → 1.50%）；⑱ `M4-04`：→ **4/16**（1.50% → 2.00%）；⑲ `M4-05`：→ **5/16**（2.00% → 2.50%）；⑳ `M4-06`：→ **6/16**（2.50% → 3.00%）；㉑ `M4-07`：→ **7/16**（3.00% → 3.50%）；㉒ `M4-08`：→ **8/16**（3.50% → 4.00%）；㉓ `M4-09`：→ **9/16**（4.00% → 4.50%）；㉔ `M4-10`：→ **10/16**（4.50% → 5.00%）；㉕ `M4-11`：→ **11/16**（5.00% → 5.50%）；㉖ `M4-12`：→ **12/16**（5.50% → 6.00%）；㉗ `M4-13`：→ **13/16**（6.00% → 6.50%）；㉘ `M4-14`：→ **14/16**（6.50% → 7.00%）；㉙ `M4-16`：→ **15/16**（7.00% → 7.50%，先于 M4-15 完成——引擎侧可离线验收，proto 大件留整块预算）；㉚ `M4-15`：→ **16/16**（7.50% → 8.00%，**M4 组完成**——决策链路全部落地）；㉛ `M5-01`：M5 组 0/18 → **1/18**（0.00% → 0.39%）；㉜ `M5-03`：→ **2/18**（0.39% → 0.78%，先于 M5-02 完成——纯映射可离线验收）；㉝ `M5-02`：→ **3/18**（0.78% → 1.17%）；㉞ `M5-04`：→ **4/18**（1.17% → 1.56%）；㉟ `M5-05`：→ **5/18**（1.56% → 1.94%）；㊱ `M5-06`：→ **6/18**（1.94% → 2.33%）；㊲ `M5-07`：→ **7/18**（2.33% → 2.72%）；㊳ `M5-08`：→ **8/18**（2.72% → 3.11%）；㊴ `M5-09`：→ **9/18**（3.11% → 3.50%）；㊵ `M5-10`：→ **10/18**（3.50% → 3.89%）；㊶ `M5-11`：→ **11/18**（3.89% → 4.28%）；㊷ `M5-12`：→ **12/18**（4.28% → 4.67%）；㊸ `M5-13`：→ **13/18**（4.67% → 5.06%）；㊹ `M5-14`：→ **14/18**（5.06% → 5.44%）；㊺ `M5-15`：→ **15/18**（5.44% → 5.83%）；㊻ `M5-16`：→ **16/18**（5.83% → 6.22%）；㊼ `M5-17`：→ **17/18**（6.22% → 6.61%）；㊽ `M5-18`：→ **18/18**（6.61% → 7.00%，**M5 组完成**）。**总完成度 44.4% → 65.5%**（W5-01 部分未计分）（口径修正：M3=2/9 时应为 47.2%，此前一处 47.1% 系从舍入值二次累计——已随本条修正；M2 组仅余 `M2-13` 两项残余——`KindFundingWindow` 分支与账户事件生产者，均不阻塞，见 §20）。

### session-24 新增的机器化约束

**成形 bar 永不入缓存**（它的收盘价是猜测，下次取数就会打脸）· **覆盖判定要豁免成形槽**（调用方请求到 now，成形槽不可缓存；不豁免=缓存永远不命中）· **取数路径与命中路径的返回语义不同要说清**（取数保留成形 bar 给组装器；命中只回已收盘——"现在价"来自 ticker/盘口）· **LRU 淘汰整键**（不会把热市场缓存削成只剩旧 bar）· **指标不进缓存**（双份实现会漂移）· **缓存钩子要可调用**（`InvalidatePair` 已备并接线：SetExchangePair 禁用分支）· **GAP 不许覆盖更具体的降级理由**（与 STALE 同秩——跳过已降级类目，测试钉住）· **跨层不许造哨兵**（共享取数透传空窗口；`ErrSnapshotIncomplete` 由组装器自己说）· **本地视图标 LOCAL_TRACKED**（缓存余额可能滞后，风控放行前必须 EXCHANGE_REST 复核）· **转录超限丢最旧**（复盘对象是最新证据；`Dropped` 计数不许静默截断）· **转录含原始参数**（模型问了什么与得到什么同等重要）· **同源事实用哈希当翻译器**（裁判侧比对 `HashBytes(data)` 与清单 `ResultSha256`——按字节判等价，改一个空格都算改证据） · **证据的清单引用必经交叉核对**（每条证据的 `Ref` 必须在 `InputManifest.ToolResponses` 里有对应条目，双缺席即拒） · **裁判构造器不读系统时钟**（`Now` 调用方供给：重放要能对齐原始窗口，纯函数性不许破） · **证据跨市场/跨策略版本即拒**（作用域闸对裁判侧同样生效——与工具层同构） · **回填提案字符串粘贴即用**（单批请求数取计划值、日期带引号——不留占位符给运营猜） · **回填权重与扫描器共用一张表**（`KlineWeight` 复用——预算口径不许有第二份） · **重载抓到就缓存**（全量窗口先喂缓存再扫描——同一段历史不对第二个读者再收一次权重） · **禁用即失效**（pair 离开启用集即从缓存删除；重新启用走重建而非复用旧数据） · **缓存写失败不拖垮重载**（fetch 已成功；merge 错误只是一行日志，绝不把重载标失败） · **密文绑行**（AAD=message_id：把 ciphertext 搬到别的行打不开——不是攻击面） · **密钥文件错尺寸拒绝而非重建**（覆盖=全部历史不可读） · **每次封新 nonce**（同明文不重复密文） · **认证失败与摘要不符分开报**（一个说明该行不可信，一个说明写入方不是本 cipher） · **只收 user 行做指令上下文**（损坏的助手行不许掩盖用户指令） · **上限定在"拒绝"而不是"截断"**（被丢的可能是关键风险约束——错误要带实际数、上限与"新开会话写汇总"的指引） · **字符按 rune 计**（按字节会三倍惩罚中文指令） · **坏 user 行必须炸出来**（静默跳过=另一种截断） · **对话与决策共享工具不共享输出契约**（chat `ResponseFormat: nil` 自由文本、不产 Candidate/decision_run） · **预算对对话同样生效**（CHAT 预约/结算/释放三态全在环） · **被拒的回合要像没发生过**（上下文超限：库零写入、预算零占用、provider 零调用） · **失败释放槽位**（provider 失败不留助手行；HOLD 结算并记 `error_code`） · **卡片是视图不是权威**（枚举失败跳过不炸回合；sha256 必等于模型看到的字节哈希——显示与取证同源） · **新端点=纯增量 diff**（工具链校准的价值兑现：追加 6 个 RPC 后 `buf generate` 二次哈希不变） · **RPC 只做转换**（会话逻辑全在服务层——离线可测；handler 里出现业务判断就是分层破了） · **未装配必须报错**（fail-closed：空列表与坏子系统不许长得一样） · **对话跟激活配对走**（`ActiveLLMRevisionID`——切配对即切对话模型，与决策一致） · **REPL 用 Reader 不用 Scanner**（Scanner 的行上限会把长指令悄悄切两半——分两截到达模型的指令是另一条指令） · **转录即胜过猜测**（每轮打印工具名/参数/sha 与卡片——M3-07 真流式前这是"模型看了什么"的窗口） · **交互错误只提示不终止**（坏转录/未知 ref/回合报错：会话继续，`:quit` 后零发送） · **机器化边界优先于提示词边界**（字段反射白名单：对话类型里长出 Order/Decision 型字段即测试失败——提示词会被改，类型不会撒谎） · **要下单也只是对话**（拒绝文本=普通助手行；零工具、预算正常结算——没有第二条路径） · **流式按整帧解析**（SSE 空行前不 decode——半截 JSON 不是数据） · **tool_call 分片聚合还原**（index 聚合后的形态与非流式一致，循环不许为流式写第二套工具路径） · **usage 迟达不编造**（索取 `include_usage`；不来就是零） · **中断=错误且零半成品行**（部分内容可返回供诊断，但绝不作为回复落库；预算槽释放） · **代理不许缓冲流**（chunked 无 Content-Length 的 2xx 必须边收边转——缓冲把真流式变成"等完再给"） · **会话本地数据写默认放行**（创建/归档写的是本地会话表，不是交易；治理端点仍受写闸） · **分帧按行不按 chunk**（TCP 分块与消息边界无关；逐字节切碎自查即为钉子） · **前端 URL 不带 /v1**（网关前缀在代理 GATEWAY_BASE 里——curl 实测抓出的客户端 bug） · **缺边界=没答**（null 限额一律 NEEDS_CLARIFICATION，模型不问也算——批准者不能批准一个操作者从没选择的数） · **矛盾不许静默选值**（模型给 questions、值保持 null） · **选择集是服务端闸**（prompt 的约束必须在服务端复述为校验） · **prompt 与结构体的键要机器对照**（漂移一次全拒真输出；reflect 测试是钉子） · **提取不持久化**（草案→版本的唯一道路是冻结 M4-02） · **provider 答了即计费**（内容非法也结算 token） · **冻结前查提案状态**（带未答澄清问题的版本不许铸成可批准版本） · **序列化就是哈希契约**（golden 字面量测试：字段序/标签漂移=当场失败，重放哈希不许悄悄变） · **运行时重算与冻结必然一致**（`RiskEnvelopeHashOf` 与冻结侧同一序列化——比对两边各算各的才是监督） · **审计行丢失要报错不许吞**（冻结持久了而 transition 没写：带 context 的错） · **列表按策略取全集**（悬空父链=腐坏：宁拒不糊） · **环检测要查可达计数不只查无根**（旁路环会躲过"无根"判断） · **diff 顺序是结构体的不是 map 的**（两次 diff 必须字节一致——审计 UI 的确定性不是审美） · **活体唯一**（授权去重查的是"有效"不是"存在"：过期/已撤不挡新，测试三态） · **撤销级联要四面见血**（同账号、同网络、清单含市场、未被并行撤——缺一面就是误伤或漏杀） · **坏市场清单炸错不跳过**（跳过可能留下"授权没了、批准还活着"） · **二次撤销拒**（重写审计行=篡改） · **审批读写的编解码单一拼写**（M4-05 复用） · **重新认证在任何写入之前**（空密码=失败不是可跳过字段） · **双 epoch 闸**（请求 CAS + 信封自己的 pairEpoch 都必须等于当前——只查一个会放过"配对换后才审阅"的版本） · **审批无改限额输入面**（信封复制自冻结版本） · **幂等即同绑定重放**（窗口是绑定的一部分；不同窗口=冲突不是替换） · **去抖是「跳过不延期」**（被跳过的触发不许写 last——礼貌风暴会把评估无限推迟；饥饿由 RetryAfter 递减测试钉住） · **节流按版本不按市场**（版本的一天额度不因多市场触发而放大） · **节流要有界**（Forget/Prune：暂停撤销即清、超窗即剪） · **门禁序=读者序**（多故障取文档序第一个：decision_run 的 reason 要稳定可复现） · **不认识的 readiness 不是 ready**（未知归 INCOMPLETE） · **没配 TTL 就是 stale**（没有 TTL 什么都证明不了） · **免费的先拒、付费的别花**（门禁纯函数零 I/O，失败记 reason 后退出不碰模型预算） · **不合格的快照不给模型付钱**（MayDecide/类目降级在预算之前拦截——论证一个已知坏事实集的钱不值得花） · **双时钟截止**（min(管道 90s, 快照源时间+60s)——自动候选的新鲜度窗就是它的寿命） · **候选契约的意外发现**（两个 ID 必须是 UUID 且 refs[0] 恰为 snapshotId——夹具被 `Validate` 三次打回后才照契约施工） · **风险候选必须站在交易所证据上**（本地视图/缓存证据只能去 REVIEW，不能带着开仓建议继续走） · **循环跑过就结算**（模型干了活，候选过不过闸是另一回事） · **拒绝在预算之前**（证据不过/体积超=judge 零调用、预算零占用——复验是免费的，别先占坑再验） · **评估输给时间**（结束前重查 pair_epoch：对已不存在的配对的裁决不是对本世界的裁决——STALE_PAIR 不是继续用） · **证书哈希≠请求对象**（`RequestSha256` 是送审字节的哈希——重放证明"当时发给裁判的就是这个"） · **分歧取保守方、永不产出 ALLOW**（两模型完全一致也只是 `AWAITING_HARD_RISK_AND_APPROVAL`——`ALLOW` 只能由硬风控（M4-11）产出，这是"模型不能授权订单"的落点） · **阈值零值≠无门槛**（未设置的阈值回退文档默认——零阈值是任何其他规则都不会同意的读法） · **未知答案按保守读**（未识别的裁判 choice 归实质分歧，不归一致） · **平仓通道不得被开仓限额堵死**（REDUCE 只豁免"新增风险"检查——系统/数据/成交保护照样管平仓，否则日损 halt 会把止损也锁死） · **ALLOW 唯一产地**（模型、分诊都没有 ALLOW 权限；硬风控纯函数是订单前最后一站） · **输入不完整不是白名单**（无候选/无时钟=`RISK_INPUT_INVALID` 直接拒——缺事实集不等于零风险） · **容差 step 是单向安全带**（floor 后再减一个 step：float64 表示 step 有微小上偏，减完后转换只能变小不能变大——Q13 选项 A 的全部意义） · **取整会改变深度消耗**（实际名义额必须重跑滑点检查——第 6 步的 floor 不是纯记账，它改变了订单在市场里的重量） · **不能落库的决策不许继续**（审计链任一步失败中止整链——decision_run 是「为什么交易」的唯一答源，先写账再下单不是礼貌是前提） · **哈希绑全输入**（`input_hash` 覆盖策略/快照/双修订版/epoch/envelope/候选与裁决原文——重放研究靠它证明「当时喂进去的就是这些」） · **换模型即失权**（切换的不是一个指针，是一整代授权：审批撤销、策略暂停、进行中运行自我作废——旧配对下作出的每一个判断都不再属于新配对的"当时"） · **SHADOW 保证是结构事实**（组装位没有订单接缝——不是开关拨到"关"，是根本没有"开"的位置） · **门禁拒绝也是决策**（理由码随 decision_run 落账——"为什么今天没跑这个市场"必须可回答，且零成本发生在付费之前） · **proto 与 handlers 必须原子落地**（生成的 service 接口要求全方法实现——注册处编译即契约；buf 幂等验证=连跑两次同哈希） · **resume 不许绕过审批**（被切换级联撤销的版本重新运行=重新审批，静默复活=绕过治理） · **读写同源**（操作员命令与自动流水线走同一服务——两条路不会各修一套闸） · **意图是不可变凭据**（先有凭据再有执行：凭据绑定决策/审批/envelope/epoch，"当时批准的到底是什么"由它回答） · **幂等键带命名空间**（`gct-llm-` 前缀：重试可安全、重复可识别——盲重试与重复下单是两个事故） · **Leverage=0 是硬约束**（币安 wrapper 对非零 leverage 直接 `ErrSubmitLeverageNotSupported`——杠杆在账户层设定，不进单笔提交） · **疑罪从有**（提交结果不明=UNKNOWN，绝不当作已拒绝、绝不盲重试——双倍仓位的事故比少一次报价贵得多） · **没有订单号的成功不是成功**（回执缺详情同样归 UNKNOWN——只有拿到内部/交易所订单号才算 ACKED） · **陈旧授权先去死**（提交前再过自身过期与 5s 新鲜度两道闸——对已移动的盘口执行旧授权等于拿旧地图走新路） · **永不重试是结构性事实**（UNKNOWN→SUBMITTING 这条边不存在于迁移表——不是"我们记得别重试"，是状态机里根本没有那条路） · **认领先于 venue**（条件 UPDATE 原子夺标：双发与盲重试两个事故共用一道闸） · **绝不留裸仓**（保护性止损挂不上=立即市价平——"有仓位没止损"是这套系统里最不能存在的状态） · **取整不许把止损挤向市场**（tick 向远离市场侧取整——盘口噪声不该触发一次本可避免的止损） · **重启先读账**（跨重启恢复=先列"还该挂着的止损"再逐单用 venue 校准——内存不是真相，venue 才是） · **已不存在仓位上方的止损会反手开仓**（平仓即撤止损；撤失败=UNKNOWN 进对账，不许静默） · **不可读数字绝不算匹配**（解析失败按原文比——读不懂的账只能放大差异） · **清账的另一种写法**（无差异=reconciled=true 且带 5min 有效期——对账不是一次性动作，"干净"的有效期只到下次对账） · **重写基线=挪阈值的零点**（一日一封；同日二次落基线即拒——日损阈值的参照系不许被"修正"） · **算不出来的额度等于没有额度**（基线/划转不可读一律拒，不给"按 0 算"的机会） · **保护性止损必须活过 soft stop**（SOFT 不撤挂单——撤掉止损的"软停机"比不停机更危险） · **用户手下的单不可被撤**（只撤 `gct-llm-` 前缀——停机不是替用户平仓的借口） · **恢复永远显式**（清除需认证+原因+不自动复活策略与审批——un-halt 是重新授权不是恢复出厂设置） · **通知永不拦执行**（Emit 无错无阻塞：一个 Telegram 故障不该拦下一笔已过闸的止损） · **消息要能回答"我该看哪里"**（每个事件带齐：谁/哪个市场/哪个订单/差在哪——告警的价值在指向） · **未知模式/操作一律否**（矩阵 fail-closed；一个不认识的名字没有"默认允许"的读法） · **null 金额阻止 LIVE 授权**（不是"无限制"——三重解锁的第三条与 M4-11 一脉相承） · **最高危跳变不加轻**（TESTNET→LIVE 的重认证与任何切换同级，且必须先过三重解锁） · **错网下单是真实事故不是配置瑕疵**（三方一致检查把"主网 Key 配测试网端点"挡在下单之前——它要么下单失败，要么在主网上真下了单） · **豁免要有理由**（非交易模式免检不是放水：它们根本没有场地下单路径） · **同名同标签永不裂序列**（标签键排序折叠——一个指标的名字空间由代码常量钉死，拼错=编译错） · **未装配≠空快照**（metrics RPC 与 chat RPC 同一 fail-closed 约定——空数据与坏子系统的样子必须不同） · **重启后还停在 SUBMITTING 必然是坏消息**（唯一写入者是认领者——留下即没结果，UNKNOWN 不重试） · **干净的重启也留痕**（CRASH_RECOVERY 审计每轮都写——"什么都没发生"同样值得可查） · **重试与退避是固定政策**（3 次指数退避写死在代码里——告警通道的可靠性不许被配置调成"不重试"） · **接收端要数据不要句子**（payload 把通知层的成文反解回 type/severity/summary/detail——自建告警系统需要可编程的字段） · **锁是底线 4/5 的表达**（三个开关不许 RPC 关——只能改配置重启；「可随时开关」的目录里必须留着不可关的） · **无审计的开关变更不许生效**（审计失败=回滚——配置面与订单面同一条纪律） · **堆积的待办比过期更危险**（REVIEW 超时自动 EXPIRED 不执行——无限堆积的待办会诱人批量草率放行） · **宽限窗=重复扫描的幂等**（扫过的旧 run 不再命中——通知可以至多重发一次，执行永远不重） · **转换回读是 Q13 的收尾**（decimal→float64→最短表示回读，超过审阅值即拒——容差 step 只保证方向，回读才是证明） · **生成器版本写在文件头**（`rpc_grpc.pb.go` 头部即版本证据——2 行版本 diff 解释了 336 行之谜） · **重生成前先备份、生成后连跑两次**（一次性 diff 可能是版本追赶；两次同哈希才是幂等）

---

## 5. 上上会话完成的任务摘要（session-23）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-16` 残留接缝（session-22 登记"扫描器没有调用方"） | `engine/llm_rotation_scan.go`（`factFromWindow` · `closedCandles` · `deriveSignals` · `averageTrueRange` · `attachScanner`/`Scanner()` · `scanAfterReload`）+ `engine/llm_rotation.go`：`fetchFullWindow` 改返回 `*kline.Item` 并接入 `Step` 重载分支 + 5 个新测试 | **第一层价值链闭环**：轮询取数 → 整点重载 → 指标派生事实 → `TriggerScanner` 事件队列。**扫描只挂重载后**（增量窗口 5 根 bar 算不出 MACD——要 33 根）；**重载抓到的历史直接喂扫描，零额外交易所权重**（同一段历史不抓两次——第二次抓是为同一个答案付第二次权重）；`closedCandles` 只取已收盘（未收盘 bar 的"收盘价"是幻觉）；588 个市场各留各的 `previous`（跨重载不串台）；未装配扫描器时 `Step` 照常（第一层不因扫描缺席而失败） |
| `M2-01` K 线取数共享（从 rpcserver 抽出） | `engine/historic_candles.go`（`iHistoricCandlesSource` 2 方法窄接口 + `historicCandlesOptions` 四模式 + `getHistoricCandlesShared`）；`engine/rpcserver.go` handler 缩成薄封装；`engine/historic_candles_test.go` 3 个离线测试 | **四模式行为不变由既有端到端测试把守**（`TestGetHistoricCandles` 的 default / ex_request / sync / db+trades 四段全跑），新测试只覆盖新接缝：模式选择、错误透传、**无 enabled 门槛**。**解锁 Q4=B**：读代码核实币安 wrapper `GetHistoricCandles` 直奔 `UKlineData`、不要求 pair 已启用，`currency not enabled` 只来自 rpcserver 的 `checkParams`——共享函数不带该门槛后，LLM 层（守市场范围）即可按需读任意合约 |
| `M0-06` sqlboiler 模型生成（pg + sqlite3 两份） | 工具链：`sqlboiler` + `sqlboiler-psql` 从分叉构建，**`sqlboiler-sqlite3` 来自独立仓库**；产出：sqlite3 29→**77**、postgres 31→**79** 文件（各 +48 = 24 张 `llm_*` × 模型+测试），既有文件仅 3+6 个机械改动（共享 where-helper 换宿主、表名/套件清单、`boil_main_test.go` 删未调用函数） | **幂等性实测接近完美**——没有任何既有表出现列级差异（对照 buf 非幂等教训）。pg 半需要活库：本机 **Docker 地址池耗尽**（`docker compose up` 建网络失败，环境级），走 session-17 原生临时集群路径（55432）；**新坑：`initdb` 后启动须 `export LC_ALL=C`**（否则 postmaster 多线程启动失败）。**发现生成器限制**：复合主键联接表 `datahistoryjobrelations` 两种方言都不生成（既有检出里也没有，属生成器行为）。`go build ./...` + `go vet ./database/models/{postgres,sqlite3}`（连测试文件）全过 |
| `M0-07` 仓储层骨架（8 域） | `database/repository/llm/`：`llm.go`（守卫/事务/双方言转换助手）+ `provider.go` · `conversation.go` · `strategy.go` · `decision.go` · `order.go` · `budget.go` · `reconcile.go` · `audit.go`；`llm_repository_test.go` + `llm_domains_test.go` 共 12 个端到端测试；`scripts/normalize_llm_model_tests.py`（再生成归一化工具） | **双方言、真库、触发器在环**：测试用 `testhelpers` 起临时 sqlite 库并**跑全部迁移（不可变触发器真实生效）**。覆盖：修订版活体唯一且新插入自动 supersede 旧的、且**不误伤自身**（初版把自己也 supersede 了——测试抓出的真缺陷）· 单行 pair upsert + epoch · 会话 seq 事务内分配 + message_count 维护 · **冻结二次写入被触发器拒绝** · **审批改受保护列被拒** · **意图改 quantity 被拒** · 预算滑窗（含出窗用例）· new-risk 滑窗计数 + 释放 · halt 设/清/活动列表 · 日初/外部流/对账 · 审计事件。**方言差异实测**：sqlite INTEGER→`int64`/`null.Int64`、pg→`int`/`null.Int`；时间戳 sqlite=RFC3339 文本、pg=`time.Time`——转换只发生在仓储层。**`llm/` 不 import 本包**（边界 allowlist），消费者在 `engine/`。**同日修复 `--wipe` 再生成的两个副作用并工具化**（见 M0-06 注 + §3 约束） |
| `M1-09` Provider Profile CRUD + 不可变修订版 | `engine/llm_provider_admin.go`（`CreateProviderProfile` · `AddProviderRevision` · `validateRevisionParams` · `invalidateActivePairForRevision`）+ 6 个测试（含 17 条校验矩阵） | **每 kind 的字段规则机器化**：JEV 模型必须 `jev-x.y.z` 形（正则）、LLM 工具字段出现在 JEV 上/JEV 模板版本出现在 LLM 上都被拒（`ErrKindFieldMismatch`）、secret ref 必须 `secret://llm/…` 且 kind 恰为 `llm`、指纹恰 16 位小写 hex、预算三态（NULL/"0"/正十进制；拒前导零/指数/负号）、**http 明文仅限被显式豁免的回环主机**（远端 http 即使带豁免也拒）、超时>0、代理可解析。**修订版不可变 + supersede**：插入新修订版在同事务里 supersede 旧的、`revision_no` 自增。**编辑活跃档案三联动**（docs 05 §2.1）：若被 supersede 的正是 active 指针指向的修订版 → 该指针清空 + readiness=INCOMPLETE + `pair_epoch`++——**且只动被编辑那一边**（LLM 侧编辑不碰 Jev 指针；编辑非活跃档案完全不动配对）。归档档案拒写。时间全部注入 |
| `M1-10` 唯一启用配对（CAS + pair_epoch + readiness） | `engine/llm_active_pair.go`（`ActivateModelPair` · `computeReadiness` · `latestProbePassed` · `validateLiveRevision`）+ 仓储 `CasActiveModelPair` / `GetPairSwitchCommandByIdempotencyKey` + 6 个测试 | **CAS 守卫在仓储**：`UPDATE … WHERE id=1 AND pair_epoch = expected` 是原子点；首激活（expected=0）走插入路径，插入竞争=丢 CAS 而非基建错误。**readiness 声明证据**：缺一边=INCOMPLETE；两边齐但任一边**最新**探针未过或**根本没有探针**=DEGRADED；都过=READY（测试覆盖三态 + 「失败的最新探针胜过更早的通过」）。**幂等**：每笔落地写 `llm_pair_switch_commands` 审计行（幂等键唯一约束）；同键同载荷重放返回已落地状态**且不涨 epoch**，同键异载荷拒。校验：non-live 修订版拒（`ErrRevisionNotLive`）、槽位 kind 不匹配拒、未知修订版透传、actor/键必填。去激活（双 nil）也是 CAS 切换。**策略暂停/审批撤销级联记 0 计数——留给 M4** |
| `M1-11` 预算与预约（24h 上限 + UTC 滑动窗口 + dailyBudgetUsd 三态） | `engine/llm_budget.go`（`ReserveBudget` · `EffectiveReservationCap` · `MarkReservationUsed` / `ReleaseReservation`）+ 仓储 `TryReserveBudget` + 6 个测试 | **先占后用且原子**：起占与计数之间不留窗口（`INSERT … SELECT … WHERE (窗口计数) < cap` 单条语句，双方言）。**窗口计数规则**：USED 一律计；RESERVED 仅在未过期时计——陈旧预约（进程死掉）不永久堵窗。**三态**：NULL 放行 / **恰 "0" 禁止一切请求**（不落预约行，测试断言） / 正数为计划值放行。**上限**：未配置→100；**Jev 封顶 100**（表驱动测试 6 例）。终态区分：USED（记 token/延迟并继续占额）vs RELEASED（释放额度）。防误用：superseded 拒、kind 不匹配拒、purpose 必填 |
| `M1-12` 工具调用循环 | `engine/llm_tool_loop.go`（`RunToolLoop` + `ChatCompleter`/`ToolRunner` 两个窄接口 + 9 个 reason code）+ 8 个测试（全离线：脚本化 transport + 假 runner） | **循环闸**（每轮顶部）：轮次≥4 / 工具调用≥8 / 累计 token≥24000 / 共同截止——四个方向四道 HOLD 码。**`tool_choice` 轮转**：前 3 轮 auto、最后一轮 none；最后一轮仍来 tool_calls→`TOOL_ROUND_INCOMPLETE`（测试断言第 2 请求的 choice 已是 none）。**超时三方钳制**：min(profile, 30s, 剩余截止)；截止已过→零请求直接 HOLD。**工具闸在执行前**：每轮>4 整轮拒（cap 2 下 3 调用的轮子工具零执行）、重复 callId 拒、非白名单拒。**参数不合法回填重试**（`retry:true`；仍占槽位）。**转录含独立复核的 sha256**；结果截断 12KB。TRUNCATED / CANDIDATE_CONTENT_INVALID 两码在册。候选语义不在本层 → M4 |
| `M2-07` 工具契约与校验层（执行器拆到 `M2-07b`） | `llm/tools/tools.go`（`Specs()` 8 个 schema · `decodeStrict` · 8 个 `Decode*` · `Scope`）+ `tools_test.go` 6 个测试 | **双重校验**（docs 05 §4.6）：`DisallowUnknownFields`+尾随数据拒 + 手写语义规则。**全部 schema `additionalProperties:false`**（测试逐条断言）。**跨字段**：`getIndicators.lookbackBars` 必须盖住 `max(maPeriods)` 与 MACD 最小根数（默认 33；自定义按 `slow+signal-2`；`fast<slow`）——要 MA(200) 给 100 根会被拒、回填重试而非静默给短序列。`getFunding` 的 history 双参互锁。**作用域锁定默认关闭**：nil scope 拒一切；命名的 id 必须恰在授权集内；`CheckEvidence` 吃 `candidate.ToolEvidence`。**发现**：四道闸构件多数已在 `candidate.ToolEvidence.Validate`（时间/体积/敏感键/深度）——**接线已由 M2-07b 的执行器就地完成**（每次产 evidence 即跑两道检查）。`llm/` 因此成 **十包** |

**账目说明**：① `M2-16` 接缝补齐**不涉及计分**（session-22 已按 [x] 计，本次兑现）；② `M2-01` 新增计分：M2 组 8/16 → **9/16**（3.00% → 3.38%）；③ `M0-06` 新增计分：M0 组 11/14 → **12/14**（3.93% → 4.29%）；④ `M0-07` 新增计分：M0 组 12/14 → **13/14**（4.29% → 4.64%）；⑤ `M1-09` 新增计分：M1 组 9/15 → **10/15**（3.60% → 4.00%）；⑥ `M1-10` 新增计分：M1 组 10/15 → **11/15**（4.00% → 4.40%）；⑦ `M1-11` 新增计分：M1 组 11/15 → **12/15**（4.40% → 4.80%）；⑧ `M1-12` 新增计分：M1 组 12/15 → **13/15**（4.80% → 5.20%）；⑨ `M2-07` 半额计分（校验层完成、执行器拆 `M2-07b`）：M2 组 9/16 → **9.5/16**（3.38% → 3.56%）；⑩ `M2-07b` 工具执行器完成（含 M2-08 四道闸接线）：M2 组 9.5/16 → **10.5/16**（3.56% → 3.94%）。**总完成度 41.2% → 44.4%**。

**校验**：`gofmt -l engine/ llm/ database/` 无输出 · `go build ./...`（含两份再生成的模型）· `go vet ./llm/... ./engine/` · `go test ./llm/... -count 1` **九包全绿**（340 个测试函数）· `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过**（接缝验证 119.1s / 取数共享验证 77.2s，机器负载差异）· `go test ./database/models/... -count 1`（pg 优雅跳过 / sqlite 全绿）· `go test ./database/repository/llm/ -count 1`（12 个测试）· `go test ./engine/ -run 'TestCreateProviderProfile|TestAddProviderRevision'`（6 个测试全过）· `go test ./engine/ -run 'TestActivateModelPair'`（6 个测试全过）· `go test ./engine/ -run 'TestEffectiveReservationCap|TestReserveBudget'`（6 个测试全过）· `go test ./engine/ -run 'TestToolLoop'`（8 个测试全过）· `go test ./llm/tools/ -count 1`（6 个测试全过；`go test ./llm/...` 十包全绿）· `go test ./engine/ -run 'TestLLMTools'`（8 个测试全过）· `scripts/normalize_llm_model_tests.py` **幂等**（连跑第二次 0 变更）

### session-23 新增的机器化约束

**重载抓到的历史就是扫描的输入**（同一段历史不抓两次）· **扫描窗口必须足够算出它要比的指标**（喂增量算不出 MACD）· **只有已收盘的 bar 参与信号**· **最长周期均线不可用 → 回退到窗口能支持的最长周期**（否则新上市合约每小时被拒掉整条 close 信号）· **先过本地最小根数校验再调仓库 TA**（别把供应商错误当输入错误）· **接缝方法也要 nil guard**（`scanAfterReload` 初版在 nil 接收者上 panic——其他 `Rotator` 方法都有守卫，它漏了）· **扫描器可选**（未装配 → 不扫，而不是失败）· **共享取数不带 enabled 门槛**（两层各自守：RPC 用 `checkParams`，LLM 用市场范围——币安 wrapper 本身不要求 pair 已启用，这是 Q4=B 的解锁点）· **抽取不重写**（四模式行为不变由既有端到端测试把守，而不是新写一套测试自证）· **窄接口为离线可测铺路**（新调用方不需要 bot）· **再生成有副作用，必须跑归一化脚本**（`make gen_db_models` 的 `--wipe` 会覆盖 `postgres/boil_main_test.go` 的项目补丁、重建与不可变触发器冲突的 llm_* CRUD 测试——`scripts/normalize_llm_model_tests.py` 一键修复，**gen 后必跑**）· **生成测试与触发器天然冲突**（摘注册而非改断言：那些 CRUD 测试断言的行为正是 schema 禁止的）· **触发器防线要在闭环里测**（冻结二次写入/审批改列/意图改量在真库上被拒绝——不是 mock）· **校验层与契约层分离**（"字段属于哪个 kind"是机器规则：JEV 上出现工具字段、LLM 上出现模板版本都被拒——17 条校验矩阵逐条断言）· **编辑活跃档案 = 清指针 + 降 readiness + epoch++**（docs 05 §2.1 的三联动在 M1-09 就地实现并测到"不误伤另一边"）· **测试的临时目录要活过整个进程**（`t.TempDir()` 随第一个测试销毁——第二个测试的 sqlite 路径蒸发；改用进程级 `os.MkdirTemp`）· **CAS 守卫放仓储不放引擎**（`UPDATE … WHERE pair_epoch = expected` 是原子点；引擎的读-改-写只是组合，冲突一律映射为 `ErrPairEpochConflict`）· **readiness 声明证据**（没有探针 ≠ 可读：缺证据记 DEGRADED 而不是放行）· **先占后用的原子性**（起占与计数之间不许有窗口：`INSERT … SELECT … WHERE count() < cap` 单条落地）· **陈旧预约不堵窗**（未过期才计 RESERVED；进程死掉留下的行随窗口自然出局）· **三态里的 "0" 是禁止键**（NULL 与正数都放行；测试断言 "0" 连预约行都不落）· **工具闸在执行前**（重复 callId/非白名单/超总数都在跑之前拒——半个工具轮无法喂给模型）· **最后一轮禁工具**（`tool_choice:none` 的轮子再要工具=`TOOL_ROUND_INCOMPLETE`，不许拖到下一轮）· **只有一个时钟**（截止比较与超时钳制必须同一口钟——测试混用注入钟与真实钟会造出假窗口）· **Schema 全量 `additionalProperties:false`**（模型塞未声明字段=被拒，不是被忽略；测试扫过全部 8 个）· **闸在执行器产出处**（evidence 一诞生就跑时间/体积/敏感键/深度与作用域——不是等循环回来补）· **市场 id 三段严格解析**（exchange/asset 与运行配置不一致即拒）· **工具与实时取数同判据**（K 线收盘判据、盘口 spread/mid 都复用组装器同一实现）· **跨字段读数陷阱要被拒而不是被补**（要 MA(200) 给 100 根：拒→回填重试，绝不静默短窗）· **作用域默认关闭**（nil scope 拒一切；命名 id 恰在授权集内才过——作用域闸的语义是"没授权就不许出现"）· **生成器产物幂等性要实测**（regen 后 diff 只应有：新表文件 + 表名/套件清单 + 共享 helper 换宿主；出现列级差异就是库与迁移漂移）· **生成前先跑迁移**（模型是 DB 快照的镜像，库落后于迁移就会生成旧世界）· **复合主键联接表本生成器不产出**（`datahistoryjobrelations` 两方言皆无——需要时手写）

---

## 6. 上上上会话完成的任务摘要（session-22）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-13` | `llm/trigger/`（`Policy`/`Fact`/`Event`/`Detect`/`SignOf`/`NextScanInstant`，13 个测试）+ `engine/llm_trigger.go`（`TriggerScanner` 去抖动/复核/有界队列/`Settle`，9 个测试）+ `FactFromSnapshot` 接缝 | **默认集按 Q5 定案**（收盘 + MACD 交叉 + 均线穿越默认开；ATR 突增与资金费率窗口默认关）。**交叉必须等 4h 复核**才升级（Q5b）：首次只见记录、窗口内再见仍记录、过窗后仍在则升级、反向信号**不会**确认前一个。**经零点不算穿越**（否则双触发）、**首次读数不算突增**、**去抖动按市场**、**队列有界丢最旧**、**待确认集有淘汰**。22 个测试 |
| `M2-16` | `engine/llm_rotation.go` 扩展：`StepKlineFullReload` + `reloadWindowLocked`/`startReloadLocked`/`fetchFullWindow` + `klineWeightFor`，3 个新测试 | 整点后窗口内**重载优先**、每市场每小时一次、**重载窗口是完整指标历史**（不是增量窗口）、权重**等于** `coverage.Budget` 的每市场重载假设。峰值 299/min < 预算 1440/min |

**校验**：`go build ./...` · `go vet ./engine/ ./llm/...` · `go test ./llm/... -count 1` **九包全绿**（340 个测试函数）· `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过（78.0s）**

### session-22 新增的机器化约束

**信号按"变动内容"而不是按"种类"识别**（按种类会让死叉确认金叉）· **短间隔信号必须过一轮长间隔复核**才升级（1h 翻转噪声太多）· **复核时钟不因再次出现而重置**（否则闪烁的信号永远等不到复核）· **经零点不算穿越**（零点不是一侧，否则每次翻转双触发）· **首次读数不算突增**（前值不可比）· **去抖动按市场**（同一刻的多个触发是一条理由）· **队列有界且丢最旧**（消费者可能慢或不存在）· **待确认集要淘汰**（否则每个出过信号的市场永久驻留）· **整点重载窗口内优先于增量**（增量重建不了指标窗口）· **短间隔信号不可直接驱动决策**

### ⚠ 一条被纠正的自身错误

`DefaultPolicy` 初版把 **MACD 交叉与均线穿越设为默认关**，理由是我把 Q5 读成"指标交叉默认关"。查 `01-需求与产品目标.md` §6 与 Q5 决策表后确认：**用户选的是"最小集 + 交叉"**，即交叉**默认开**。已改正默认值、注释与断言。**教训**：任务清单里的括注（`M2-13` 写的正是"指标交叉默认开"）与我的读解冲突时，应当去查定案原文而不是相信自己记住的取舍。

---

## 7. 上上上上会话完成的任务摘要（session-21）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-14` | `engine/llm_rotation.go`（`iRotationSource` 4 方法窄接口 · `Rotator` · `Step` · `Run` · `RotationStats`）+ `llm_rotation_setup.go`（配置映射 · `SetupRotation` · `rotationLoop`）+ `LLMManager` 生命周期接线 | **19 个测试函数，全部离线**（假 source + 注入时钟 + 注入 sleeper）。断言覆盖：每轮每个启用市场访问一次且顺序确定、批 ticker 每周期一次（且与轮次边界交互被显式断言）、单市场失败不中断且游标不卡、权重闸满时暂停并等待（不硬撞、不打交易所）、失败不记 ticker 时间戳、统计值全部实测（进度百分比/最冷市场年龄/本分钟权重/失败数）、空 enabled 列表不是错误、启用新市场无需重建、配置字段逐个映射且不别名调用方 slice、间隔拼写双向往返、`Start` 不因覆盖失败而失败 |

**校验**：`go build ./...` · `go vet ./engine/` · `go test ./engine/ -count 1 -skip '<既有网络类测试>'` **整套通过（79.3s）** · `go test ./llm/... -count 1` 八包全绿

### session-21 新增的机器化约束

**每个分支都必须 sleep**（循环不可能空转——这是"bug 不会变成请求洪水"的机械依据）· **权重闸先判后发**（不够就等，不硬撞）· **失败不记新鲜度时间戳**（否则会声称一份并不存在的新鲜度）· **单市场失败不中断整轮，游标照常前进**（不卡在坏市场上）· **配置映射独立成步并单独测**（人填的数 vs 计划隐含的算术）· **映射不得别名调用方的 slice**（事后改配置不能改到活计划）· **`Start` 不因可选组件失败而失败**（决策路径不依赖第一层）

### ⚠ 一个需要人工知晓的行为变更

`coverage.enabled` 在示例配置里是 **true**，所以**下次重启 bot 会开始对启用的合约做只读轮询**（按计划约占限额 2.4%）。该循环**从未对真实交易所跑过**（本会话全部离线测试）。若想先不跑，把 `coverage.enabled` 设为 false。

---

## 8. 上上上上上会话完成的任务摘要（session-20）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-15` | `engine/llm_snapshot.go` + `llm_snapshot_test.go`（12 个测试函数） | **真机不需要**：窄接口 + 假 source 完全离线。断言覆盖 K 线窗口反推（`now` 为 231 小时前）、未收盘那根单独归入 `LiveCandle`、`SourceTime` 取最新已收盘、指标与默认参数集（7 条均线）、盘口保留交易所十进制字符串、冷缓存不致命、**新鲜度门禁在交付前生效**且被拒快照仍返回、余额按 USDT 汇总、窗口 50 根上限、**两次组装字节级一致**、6 个间隔双向映射、ID 生成与指定。**编译期断言 `exchange.IBotExchange` 满足窄接口** |

**校验**：`go build ./...` · `go vet ./engine/` · `go test ./llm/... -count 1` 八包全绿 · `engine/` 相关 17 个测试全过

### session-20 新增的机器化约束

**接口窄到刚好够用**（5 个方法，编译期断言平台满足它，因此不需要适配器，也不需要 bot 就能测）· **注入 `now`，从不读墙上时钟**（年龄必须可复现）· **未收盘的那根不进指标**（只作为"价格现在在哪"的证据）· **盘口用交易所的字符串不用浮点副本**· **不要为了凑齐字段而发明数据**（位置方向缺失就留空，属 M5-08）· **被拒的快照仍要返回**（它仍是证据）· **市场标识用固定分隔符**（不随配置编辑而改写法）

---

## 9. 上上上上上上会话完成的任务摘要（session-19）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-04` | `llm/snapshot/indicators.go` + `indicators_test.go`（22 个测试函数）· `engine/llm_indicators_test.go`（3 个跨层交叉验证） | **与平台自身实现交叉验证**：喂入仓库 `technical_analysis.go` 的真实输出，断言携带值逐点相等、`Latest` 等于平台最新值、位置事实等于"最新收盘 vs 最新均线"的直接比较、派生的叉态等于平台柱状图在**携带窗口内**的符号翻转。**修掉一个真实缺陷**（下方）。参数集、最小根数、可选指标缺省、确定性排序、非有限值拒绝、指数记法禁止、组装结果能过快照自身校验——全部覆盖 |

**修掉的真实缺陷（只有跨层验证才能发现）**：float64 的最短精确渲染可能比契约允许的更长。真实的 MACD 柱值是 `0.0035044457052606504`——**19 位小数**，而 `ValidateDecimal` 的上限是 18 位，于是**平台算出来的合法指标会被契约拒绝**。纯单测发现不了，因为我自己造的合成值都是短小数。

修法：新增 `MaxRenderedFractionDigits = 12`，所有**计算得出**的值统一按此精度舍入并去掉尾零。**没有**去放宽契约的小数规则——那条规则存在的意义就是"钱不过浮点"，为一个读数放宽它是拿安全换方便。指标是证据不是钱，12 位小数的分辨率远超范围内任何价格尺度。

**校验**：`go build ./...` · `go vet ./llm/...` · `go test ./llm/... -count 1` 八包全绿（**329 个测试函数**）· `go test ./engine/ -run 'TestIndicatorAssembly|TestAssembler'` 3/3 通过 · 合计 **332** 个测试函数

### session-19 新增的机器化约束

**一处计算，两处读取**（指标只能有一个实现，否则决策与图表会不一致）· **计算值统一精度 + 去尾零**（一个数只有一种写法，快照哈希才可比）· **不为读放宽钱的规则**（要收窄计算值，不是放宽契约）· **派生事实按携带窗口计算**（模型看不到的证据不能成为当前状态的依据）· **跨层双验证**（同一层内的单测无法发现"我造的值恰好合法"这类盲区）

---

## 10. 上上上上上上上会话完成的任务摘要（session-18）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-06` | `llm/snapshot/freshness.go` + `freshness_test.go`（16 个测试函数） | **阈值全落**（ticker/orderbook 60s · K 线 `2×间隔+60s` · 资金费率 10min 不阻断 · 本地仓位 60s · 执行前 5s · 自动候选窗口 60s 且被流水线截止时间钳制）。**按事实各自读取时间计龄**。**年龄未知即不新鲜**（缺读时间/未来时间戳 → 报错而非 STALE）。**边界值恰好等于阈值算新鲜**（实测 ±1ns 两侧）。**资金费率单独可 stale 而不阻断**，且与阻断类混合时两列表都正确。**修掉一个真实缺陷**：打 STALE 不覆盖已有的更强降级标签（GAP 与 STALE 同秩会被 `Downgrade` 放行，覆盖会丢信息）。**变异验证**：两处变异各让 4 个测试失败，还原后全绿 |

**校验**：`go test ./llm/... -count 1` **八包全绿**（311 个测试函数）· `go test ./llm/snapshot -race` 通过 · `go vet` 无输出

### session-18 新增的机器化约束

**每类事实按自己的读取时间计龄**（一次快照里的 K 线、盘口、账户是不同时刻取回的）· **年龄未知一律不新鲜**（缺读时间、未来时间戳都是拒绝而非放行）· **阈值必须按轮询周期设定**（比一次轮询还紧的阈值会让系统什么都做不了）· **非阻断的 stale 要单独成列**（资金费率是成本不是风险；但"标记后继续"与"静默忽略"不同）· **打降级标签不得覆盖更强的既有标签**· **拒绝码与标签同源**（否则读 report 的调用方与读快照的调用方会得到不同结论）

---

## 11. 上上上上上上上上会话完成的任务摘要（session-17）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M0-14` | `docker-compose.yml` 新增 `postgres` 服务（只绑回环）+ `docs/localDeploy/06` §11.1 实测记录 | **本机原生 PostgreSQL 17.11 临时集群实测**：10 个迁移全部 `OK`，**24 张表 + 20 触发器 + 2 个 plpgsql 函数**；`TIMESTAMPTZ`/`BOOLEAN`/`BYTEA` 翻译核对无误；`down` 干净（触发器/函数清空、表保留）；**pg 侧 18 条触发器行为断言全过**（含 3 条 NULL 逃逸用例）；临时集群已删除 |

**校验摘要**：`docker compose config --quiet` 通过 · Postgres 迁移 up/down 双向实测 · pg 触发器 18/18 断言 · 文档 markdownlint 43 文件 0 issue

### session-17 新增的机器化约束

**Postgres 真的校验外键而 SQLite 默认不校验**（夹具必须先插父行）· **`psql -f` 默认不因错误中止**（夹具失败后断言会在**空表**上全部"通过"；必须把异常转成可读文本并确认表里有行）· **两种方言都要跑**（同一个缺陷在 sqlite 表现为静默放行、在 pg 表现为 FK 报错）· **provisioning 服务只绑回环**（dev 默认口令 + 发布端口 = 必须不可从外部达）

---

## 12. 上上上上上上上上上会话完成的任务摘要（session-16）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `W8-01` | — | `pnpm typecheck`（无输出）· `pnpm lint`（oxlint **0 warnings 0 errors**，26 files/96 rules）· `pnpm build`（3150 modules, 1.02s）**三连全绿** |
| `W8-02` | — | 浏览器实测 `/terminal` BTC：**240 根已收盘 K 线** · **14 个 canvas**（主图 + 成交量 + MACD 副图 + 价格轴/时间轴）· 真实账户数字（可用 68.76 / hold 1.25 / total 70.01） |
| `W8-03` | — | 4 个待实现页（/health · /providers · /decisions · /strategies）均渲染「待实现/尚未实现」并**指名子任务 ID**；无白屏、无崩溃、无 501 裸露 |
| `W8-04` | `AppShell.tsx`（Sider 响应式收起）· `MarketsPage`/`HealthPage`/`ProvidersPage`/`DecisionsPage`（表格 `scroll.x`）· `global.css`（窄屏隐藏品牌后缀与版本） | **实测发现并修掉 5 个路由的横向溢出**。390px：sider 收成 1px、卡片单列、**0 溢出**；1440px：sider 176px、卡片两栏并排（709 + 503）、**0 溢出** |
| `W8-05` | `webUI/README.md` | 复核已含四项必需内容（启动/构建/与 bot 的关系/代理说明）；补 `/markets` 行、7 个路由页、`coverage.ts` 目录项与第 5 条实测坑位 |
| `W8-06` | — | **四项密钥自查**：`src/` 无任何密钥值 · `dist/` 比对 config 2 个真实凭据 **0 泄露** · 1186 条网络请求**全部**指向 127.0.0.1:5173（0 直连网关、0 非本地、0 URL 内嵌凭证）· 代理只在**上游**请求注入 Authorization |

**校验**：`pnpm typecheck` + `pnpm lint` + `pnpm build` 全绿 · 浏览器实测覆盖 7 个路由

### session-16 新增的机器化约束

**窄屏必须收侧栏**（固定 Sider 在 390px 下只剩 ~210px，必然溢出）· **宽表用 `scroll.x` 在卡片内滚动**而不是撑开整页 · **响应式要用可测量的视口验证**（同源窄 iframe 能让媒体查询按指定宽度求值）· **质量项要逐项实测**，不能靠"看起来还行"

---

## 13. 上上上上上上上上上上会话完成的任务摘要（session-15）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-12` 补充 | `llm/coverage/scope.go`：`SelectMarkets`/`SummaryFor`/`ScopeSummary` + 8 个测试函数 | **修掉一个会让覆盖计划失真的缺口**：`Plan.Scope` 此前只是被携带、从未用于选市场。实测 Binance 给的 **789** 个 available 里混着 USDC/USD1/法币/代币化股票形状；不加筛选会把设计外的市场写进计划并按它们算权重。筛选出 **588** 个真 USDT 永续；按 588 重算预算 299/min < 1440/min。**不用 ASCII 正则限 base**（`币安人生_USDT` 是真实可交易永续，正则限死会静默丢真市场） |
| `W7-04` | `webUI/src/pages/MarketsPage.tsx` + `webUI/src/app/api/coverage.ts`；侧栏新增「合约覆盖」 | **真实数据**：789 提供 / 588 在范围 / 5 启用 / 201 范围外（交割 4 · 非 USDT 197 · 空 0），范围外给原因 + 命名示例。**计划面**：638 weight/周期 → 63.8/min，重载 1176 → 235.2/min，峰值 299 vs 预算 1440，余量 1141/min，间隔 1020ms。**搜索/三态筛选/分页 20×30**。**点击行 → 跳交易台**（实测 `1000PEPE_USDT` → `/terminal`，base 解析为 `1000PEPE`）。`tsc --noEmit` + `oxlint` 0 issue + `vite build` 通过 |

**浏览器实测发现并修正一处自家 UI 的过度承诺**：页面原把"未启用"标为「按需只读」，实测选中后交易台报 `currency not enabled`——网关取数路径会校验 `enabled` 列表，**Q4=B 的按需只读需要 `M2-01` 提供绕过校验的路径，现在并不存在**。已把标签改为「未启用」并把告警改成"目前查不了（实测）"，附上正确的依赖与最终语义。

### session-15 新增的机器化约束

**范围要显式筛选并报告被排除项**（"看了 789 里的 588"可核对；静默丢弃没人能发现范围定错）· **不要用 ASCII 正则限合约 base**（会静默丢掉非 ASCII 的真实永续）· **计划值不得冒充运行值**（页面明写"这是计划值"）· **UI 不得承诺未落地的能力**（未启用 ≠ 按需只读，实测打脸后立即改）

---

## 14. 上上上上上上上上上上上会话完成的任务摘要（session-14）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M0-05` | `20261010120800_llm_immutability_triggers/{postgres,sqlite3}.sql`：**21 个 sqlite 触发器** + pg 侧 2 个 plpgsql 函数（`llm_reject_mutation` / `llm_reject_immutable_columns`，列清单经 `TG_ARGV` 传入） | **18 条行为断言全过**：10 条"必须被拒"（含 **3 条 NULL 逃逸用例**）· 3 条"必须放行"（撤销/生命周期/结算）· 5 条冻结门控（冻结本身放行、`state` 放行、3 种冻结列改动被拒）。**真实库实测** UPDATE/DELETE 被拒。Down 后 0 残留 |

**校验摘要**：`go build ./...` 通过 · `dbmigrate -command up` 在真实库成功（`current version: 20261010120800`）· 迁移的独立校验见 `workLog/2026-10-10-session-14-immutability-triggers.md`

### session-14 新增的机器化约束

**NULL 不安全比较会毁掉守卫**（`<>` 遇 NULL 得 NULL → WHEN 不成立 → 写入被放行；改用 `IS NOT`/`IS DISTINCT FROM`）· **部分可变的表不能一刀切禁改**（有撤销列的表必须能撤销）· **多语句体需要 goose 的 `StatementBegin/End`**（仓库的 goose 按 `;` 切语句）· **"必须放行"用测与"必须拒绝"同样重要**（只测拒绝会让一个过严的守卫也通过）

---

## 15. 上上上上上上上上上上上上会话完成的任务摘要（session-13）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M0-04` | `database/migrations/` 新增 8 个目录 × 2 份 SQL = **24 张表**（provider 配对 5 / 会话 2 / 策略与授权 5 / 快照 1 / 决策 1 / 风控与意图 3 / 预算与计数 2 / 对账与 halt 5） | **逐表逐列比对文档 → 24/24 表、每列全一致（含顺序）**。Python `sqlite3` 执行 Up → 24 表 + 2 索引；逆序 Down → **0 残留**。单行表 `CHECK(id=1)` 与 `UNIQUE(kind,display_name)` 实测拒绝违规行。pg 语句括号配平 + goose 标记齐全。本地真实库 `dbmigrate -command up` 后确有 24 张 `llm_*` 表。`go build ./...` 通过 |

**校验摘要**：`go build ./...` 通过 · `go test ./llm/...` 八包全绿（290 测试函数，未受本次改动影响）· 迁移的 4 项独立校验见 `workLog/2026-10-10-session-13-llm-schema-migrations.md`

### session-13 新增的机器化约束

**迁移分组顺序按 FK 依赖**（Postgres 要求被引用表先存在）· **金额/数量列一律 TEXT**（绝不浮点）· **id 保持 TEXT 不改成 uuid**（本方案用不透明字符串）· **方言各自惯用类型**（pg `TIMESTAMPTZ`/`BOOLEAN`/`BYTEA`，sqlite `timestamp`/`INTEGER`/`BLOB`）· **Down 必须真正回滚干净**（实测 0 残留）· **sqlite 的 `UNIQUE` 不用 `ON CONFLICT REPLACE`**（会静默删改事实行，与不可变意图冲突）

---

## 16. 上上上上上上上上上上上上上会话完成的任务摘要（session-12）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M1-13` | `llm/openai/prompt.go`：`analysis-readonly-v1` / `policy-proposal-v1` / `chat-analysis-v1` 三个常量 + `PromptFor` + `ErrUnknownPromptVersion` | **与 `candidate` 契约交叉校验**：`reflect` 读真实 JSON 标签，断言提示词承诺的键**逐字逐序一致**（12 个键）。**不是模板**：禁 `%s`/`%v`/`%d`/`%q`/`%w`/`%!`/`{{`。**纯 ASCII**（中文译文留在设计文档）。**不含探针标识**。两模式互不串味（对话提示词不得出现 `Return one JSON Candidate`，决策提示词不得出现 `Reply in the operator's language`）。版本前缀不解析 |
| `M0-13` | `llm/boundary_test.go`：stdlib `go/parser` 解析全树 import，三重断言（allowlist / 带理由 blocklist / 第三方白名单） | **实测注入验证**：临时加 `_ "…/engine"` → `TestLLMPackagesImportOnlyTheAllowedPrefixes` 与 `TestLLMPackagesDoNotImportTheBlockedPackages` 同时失败；移除后全绿。**解析器自证**：另有测试断言 walk 确实解析到了源文件与测试文件，且看得见 `snapshot → candidate` 这条真实边 |

**校验**：`go test ./llm/... -count 1` **八包全绿** · `go build` + `go vet` 通过 · 测试函数合计 **290**

### session-12 新增的机器化约束

**提示词的键列表必须等于契约的 JSON 标签**（加字段忘改提示词即失败）· **提示词是常量不是模板**（运行时插值会让重放不可能）· **提示词纯 ASCII**（译文漏进 payload 即失败）· **对话/决策两模式互不串味**· **`llm/` 的内网依赖是白名单**（新依赖需要在这里写理由）· **测试文件也受边界约束**· **空 walk 会失败**（防止测试空转变绿）

---

## 17. 上上上上上上上上上上上上上上会话完成的任务摘要（session-11）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-03` | `llm/snapshot/`：`snapshot.go`（`MarketSnapshot` + 9 类事实 + 6 个质量类目 + `Validate` + 深度 `Clone` + `SetQuality` 只降不升 + `DegradedCategories`）+ `derive.go`（`DeriveMACDState`/`DeriveMAState`/`DeriveSpreadBPS`/`DeriveMidPrice`/`DeriveExecutablePrice`/`DeriveAnnualisedFundingPct`/`SummariseCandles`）+ `snapshot_test.go`（29 个测试函数） | **全十进制字符串**：拒 `-1`/`+1`/`1e5`/`1.`/`.1`，签名值用 `ValidateSignedDecimal`。**有界**：9 项上限逐项断言（K 线 50、指标 20、盘口 20、资金费率 21、仓位 64）。**派生事实**：MACD 三种叉态 + 柱趋势 + 最近交叉优先；均线斜率有 0.01% 平阈（1 美分漂移不读成方向）；**可成交均价随规模单调不优**（1→10→100 逐档递增逼近），且**吃不下就报错而非给部分成交价**。**`Clone` 深度**：改 clone 的每个嵌套 slice 后原快照不变。**质量**：只降不升（升级报 `ErrQualityUpgrade`）、缺失 ≠ 未知。JSON 往返后价格仍是原字符串 |

**校验**：`go test ./llm/... -count 1` **七包全绿** · `go test ./llm/snapshot -race` 通过 · `go build` + `go vet` 通过 · 测试函数合计 **271**

### session-11 新增的机器化约束

**`Clone` 必须深拷贝每个嵌套 slice**（浅拷贝会让两个快照共享一条序列，改一个改两个）· **比率与它除的那个数用同一套算术**（价差 bps 原来用 float，与 decimal 中价不同源，12 位就开始漂）· **`DegradedCategories` 区分"缺失"与"未知"**（否则只算 K 线的快照永远不可用；有数据无标签仍阻断）· **吃不下就不给价**（部分成交当全额会低估滑点）· **只校验读取到的值**（`DeriveMAState` 只读末尾，全序列由 `Validate` 覆盖，不重复劳动）

---

## 18. 上上上上上上上上上上上上上上上会话完成的任务摘要（session-10）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M2-12` | `llm/coverage/`：`plan.go` 纯规划层（`Plan`/`Normalise`/`Validate`/`BuildCycle`/`Cycle`/`Budget`/`KlineWeight`）+ `budget.go` 滑动窗口权重闸（`Gate`，mutex 保护）+ `plan_test.go`（22 个测试函数） | **525 合约实测算术**：轮询 575 weight/周期 = 57.5/min，整点重载 1050 weight 摊 5 分钟 = 210/min，峰值 267.5/min vs 预算 1440/min。**KlineWeight 按真实半开区间分档**（99→1、100→2），修正文档 off-by-one。**计划放不进预算 → 配置期报错**。并发争用测试（50 goroutine 抢 1000 额度，恰好 40 个通过）。**重载+轮询同时段的 10 分钟仿真**：每一分钟决策取鲜 15 weight 都能拿到额度，窗口从未超预算。`-race` 通过 |

**校验**：`go test ./llm/... -count 1` **六包全绿**（candidate / coverage / httpclient / jev / openai / secrets）· `go test ./llm/coverage -race` 通过 · `go build ./llm/...` + `go vet ./llm/...` 通过 · 测试函数合计 **242**

### session-10 新增的机器化约束

**轮询永不能饿死决策**（权重闸让路；仿真实证每分钟决策额度都在）· **计划在自己都放不下时配置期就失败**（不留到凌晨被闸拒绝）· **权重分档照真实半开区间**（文档的 off-by-one 被代码修正）· **拒绝不记账**（重试不会把窗口推得更满）· **窗口是 (now−60s, now]**（刚过期的算出去，时钟偏移导致的"未来事件"不算进来）· **`Normalise` 的零值语义写明**（零 = 未设置；要拒绝必须给越界值）

---

## 19. 累计记录的文档偏差（已被代码纠正，文档待同步）

| 位置 | 文档写法 | 代码实际 | 处理 |
| --- | --- | --- | --- |
| `04` §7.4 K 线权重 | `limit ≤ 100 → weight 1` | Binance 真实表是半开区间：99→1、100→2 | **代码按真实表**；`KlineWeight` 注释说明；文档可在下次编辑 §7.4 时同步 |
| `05` §9 词表 | 无 `STREAM_UNSUPPORTED` / `REQUEST_TOO_LARGE` | 两者已实现 | 已在 session-09 补进 `05` §3 与 §9 |
| `05` §10 包设计 | 列了 `limits.go` | 256KB 上限已落在 `httpclient` + `ChatRequest.Marshal()` | 建议删掉该行（单独文件是无谓抽象） |
| `04` §3.1 结构 | `Interval kline.Interval` | `exchanges/kline` import `exchanges/order`，会把订单管理器拖进 `llm/`，破坏 Q22 的"`llm/` 可整体抽出" | 代码改用本地 `Interval` 字符串枚举；`M0-13` 应加机器化断言 |
| `04` §3.1 结构 | `Pair currency.Pair` / `Asset asset.Item` | 契约要序列化给模型与审计读，朴素字符串更稳 | 代码用 config 格式字符串（`BTC_USDT`）+ `usdtmarginedfutures`；文档本身已注明内容格式 |
| `09` §1.2 不可变清单 | 把 `llm_strategy_approvals` / `llm_order_intents` / `llm_budget_reservations` 列为"完全不可变" | 与它自己的表结构矛盾：有撤销列却永远不能撤销、有 status 却永远停在 PREPARED | 代码按"列级保护 + 禁 DELETE"实现这三张；可变的列恰好是结构里为后续状态提供的那些 |
| `09` §1.2 列级守卫 | `NEW.col <> OLD.col` | **NULL 不安全**：把列置 NULL 就能绕过保护（`NULL <> 'x'` 为 NULL → WHEN 不成立 → 放行） | 代码改 `IS NOT`(sqlite) / `IS DISTINCT FROM`(pg)；**3 条 NULL 逃逸用例实测被拒** |
| `09` §1.2 不可变清单 | 未列 `llm_market_snapshots` | `04` §3 明确"快照构造后不可变"，且该表无可变列；漏掉就是一张可写的事实表 | 代码纳入完全不可变，注释说明是补漏 |
| `09` §1.2 触发器 SQL | 未用 goose 标记 | 仓库的 goose 按 `;` 切语句，触发器体含内部分号 → `incomplete input`，**照抄跑不起来** | 代码用 `-- +goose StatementBegin/End` 包裹全部 23 处多语句体 |

---

## 20. 上上上上上上上上上上上上上上上上会话完成的任务摘要（session-9）

| 子任务 | 交付物 | 验证 |
| --- | --- | --- |
| `M1-02` | `llm/openai/`：`types.go`（ChatRequest/ChatResponse/Message/ToolSpec/ToolCall/ToolChoice/ResponseFormat/Usage）+ `errors.go`（14 个哨兵）+ `client.go`（Profile/NewClient/ChatCompletion/ParseChatResponse/ContentOf） | 24 个测试函数：工具调用往返（含 `tool_call_id` 回填 + 字符串化 arguments 的双重解码）、`tool_choice` 两种线格式往返、`Usage` 兼容带引号计数、256KB 上限**在发送前**中止、空 content 报错而非空串、HTTP 200 内嵌错误信封被拒、密钥**逐次取用**、模型漂移被拒且**不发网络** |
| `M1-03` | `llm/openai/probe.go`：`Probe(ctx, layer)` / `ProbeAll` / `ProbeResult` / `ParseSSE` + **22 个稳定错误码**与 `ErrorCode` 映射 | 22 个测试函数：TOOL_CALL 层**真跑两轮**（断言第二轮带 `tool_call_id`+`tool_choice:none`+回放 assistant 轮）；SCHEMA 层**要求 `proposedMaxNotionalUsd` 键存在且恰为 `null`**（缺键单独报错）且 `action==HOLD`，再走真实 `candidate.Validate()`；STREAM 层认 `[DONE]` 收尾；错误码映射 **31 条**逐项断言 |

**校验**：`go test ./llm/... -count 1` **五包全绿**（candidate 0.713s / httpclient 1.725s / jev 1.753s / openai 1.828s / secrets 2.821s）· `go build ./llm/...` + `go vet ./llm/...` 通过 · 测试函数合计 **31 + 39 + 89 + 41 + 20 = 220**

### session-09 新增的机器化约束

**空 content 是错误不是空答案**（否则"模型什么都没说"会被当成合法 HOLD）· **HTTP 200 里嵌的错误信封必须被拒**（部分供应商在 200 里报失败）· **`tool_choice` 裸字符串 vs 具名对象分开发送**（供应商对 `{"mode":"auto"}` 报 400）· **请求超限中止而非截断**（截断后的 prompt 与审计记录里的不是同一个问题）· **探针零市场数据**（断言不含标的/交易所/周期标识；描述"数据缺失"本身是必需的）· **`proposedMaxNotionalUsd` 缺键 ≠ 值为 null**（缺键说明模型没回答这个问题）· **协议级故障优先于阶段包装**（"第二轮响应读不出来"比"轮次未完成"更可操作）· **`SecretFn` 单一类型定义**（`httpclient.SecretFn`，`jev.SecretFn` 改为别名，两适配器可共用同一密钥源）

---

## 21. 下一步建议（给接手的模型）

**前端已无可"顺手做完"的独立项**（W8 全绿；其余 W 组都等后端）。后端剩下的事都需要外部资源：

**`M6` 已 8/11（session-51）**：纯本地项全部完成（M6-01/02/06/07/08/09/10/11）——验收记录 + 准入命令 + 失效矩阵全 21 行演练 + `make check` 全绿 + 两基线策略 + kill -9 崩溃演练 + 文档定稿 + 密钥轮换演练 + 备份恢复演练。**下一候选（2026-10-12 session-52 更正：二者均已入用户决策门 `02`/`03`——见 §1 收口指引）**：SHADOW `RiskFacts` 生产装配（PAPER/LIVE 桥的前置，拍板后从合成器起步）与 PAPER/LIVE 决策→订单桥（账户事件 seam 已就位随桥激活，同门）；`M6-03/04/05` 依赖 SHADOW/PAPER/24h 长跑与测试网凭据（生产装配 + decisions/01 后补）；并行余项：
2. **proto 已校准**（`TASKS.md` §0.1，session-24 收尾）——`M1-14`（providers 命令族）、`W7-07`、`W5/W6` 接口层全部解锁；下一次接口工作直接 `cd gctrpc && buf generate`，diff 应只含新增端点。

**session-23 注记（历史，2026-10-12 校准）**：`M2-16` 残留接缝闭合 + `M2-01` 取数共享就位（`engine/historic_candles.go`，Q4=B 按需只读；工具层接入已随 M2-07b 落地）。当时 `M2-13` 余下两项**均早已落地**：`KindFundingWindow` 分支（session-28）与账户事件生产者（session-51）。

**⚠ 给数据库改动的提醒**：`dbmigrate` 必须从 `cmd/dbmigrate/` 目录运行（否则报 `../../database/migrations directory does not exists`）。多语句 SQL（触发器/函数）**必须**用 `-- +goose StatementBegin/End` 包裹。

**⚠ 重新生成模型时**（改了 schema 之后）：三个二进制都要在 PATH——`sqlboiler` 与 `sqlboiler-psql` 从分叉构建，**`sqlboiler-sqlite3` 来自独立仓库 `github.com/thrasher-corp/sqlboiler-sqlite3`**（分叉里没有）。pg 半需要活库：**本机 Docker 地址池耗尽**（`docker compose up postgres` 会失败），走原生临时集群路径（`initdb` 须 `export LC_ALL=C`）。完整命令与产物核对见 workLog session-23 附件三。**生成后必跑 `python3 scripts/normalize_llm_model_tests.py`**（修复上述两个副作用），再 `go test ./database/models/...` 验收。

**⚠ 提醒**：`boundary_test.go` 的 allowlist 是**穷尽式**的。给 `llm/` 加任何新的内网依赖都会让它失败——这是设计意图，不是误报。加之前先在 `allowedInternalPrefixes` 里写明理由。

---

## 22. 阻塞项

**0 项技术阻塞**：proto 工具链已校准（`TASKS.md` §0.1，2026-10-11）——`buf generate` 幂等验收通过，可新增 gRPC/REST 端点（含 `/v1/llm/*`）；一次性对齐 diff 待随提交单列。
**`decisions/` 有 3 项用户决策等待（2026-10-12 session-52 更正）**：`01-testnet-credentials.md`（测试网与真实供应商凭据——阻塞 `M6-03/04/05` 与 `M1-15`）、`02-auto-execution-order-type.md`（自动执行单型——阻塞 `RiskFacts` 闭包装配与 PAPER/LIVE 桥）、`03-expected-margin-type-source.md`（保证金假设声明处——阻塞同一闭包装配的 C 组材料）。用户不提供凭据/不拍板则其余队列照常推进；**拍板后的动手顺序见 §1 收口指引**（① 侦察已读、② 组合冒烟链已预建（session-52）——从 ③ 合成器 + 闭包 + 桥起步）。

---

## 23. 风险与注意

| 风险 | 现状 | 应对 |
| --- | --- | --- |
| proto 工具链漂移 | **已校准**（session-24 起三次新增端点各连跑两次同哈希） | 新增端点后仍连跑两次验幂等 |
| **技术文档数值示例不可信** | 已在 `docs/llm-dev/06` §2.2 发现一处与自身不变量矛盾的示例（1.42 vs 1.24） | **以不变量与校验器为准**；发现矛盾改示例并留注记，不放宽校验器 |
| `config/versions/v18` 迁移缺失 | 靠 `CheckLLMConfig` 填默认（已验证可用） | `M0-09` |
| 数据库仍为 SQLite（Q20 定案 Postgres） | LLM 表与模型已就位（`M0-04`/`M0-06`/`M0-14`），但运行时仍连 SQLite | `M0-07` 起仓储层，切库在 M5 前 |
| **本机 Docker 地址池耗尽** | `docker compose up` 建网络即失败（环境级，影响所有 compose 项目） | pg 用原生临时集群路径（已验证）；要修 Docker 需 `network prune` 或改 daemon 配置——**属用户决策** |
| **`--wipe` 再生成会覆盖项目补丁/重建冲突测试** | M0-06 实测连续两次中招（boil_main 补丁、136 个触发器冲突子测试） | `scripts/normalize_llm_model_tests.py`（**gen 后必跑**，幂等，含补丁自动回植） |
| LLM 页面缺真实数据 | 后端 M1/M4/M5 已完成；运行面（生产装配）未接 | W1-08 确定性降级；装配后灌真实数据 |
| 前端 bundle 1.36 MB | 未做路由懒加载 | 非阻塞；`W8` 处理 |

---

## 24. 环境状态（重启方法）

| 服务 | 端口 | 启动 |
| --- | --- | --- |
| GoCryptoTrader bot | 9052 / 9053 | `cd <repo> && ./gocryptotrader -portfoliomanager=false -llmmanager` |
| webUI 本地代理 | 8788 | `cd webUI && pnpm proxy` |
| Vite dev server | 5173 | `cd webUI && pnpm dev` |
| （一键前端） | — | `cd webUI && pnpm dev:all` |
| postgres（生成模型用） | 55432 | 原生临时集群（`LC_ALL=C` + `initdb`）；**compose 路径本机不可用** |
| sqlboiler 工具链 | — | `~/go/bin/{sqlboiler,sqlboiler-psql,sqlboiler-sqlite3}` **已安装**（regen 要用） |
| 模型归一化脚本 | — | `scripts/normalize_llm_model_tests.py`（`make gen_db_models` 之后必跑；幂等） |
| proto 工具链 | — | `~/go/bin/buf` 1.73.0 + 4 个 `protoc-gen-*`（**已校准**：session-24 起新增端点=纯增量，连跑两次同哈希） |
