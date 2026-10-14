# Session-28 工作日志：M0-09 配置迁移 v18（llm 段进入配置版本链）

**日期**：2026-10-11 · **任务**：`M0-09`（docs 09 §4.3；CODING_GUIDELINES 配置迁移规则）· **结果**：✅ 完成，M0 组 14/14 组完成。

## 1. 交付物

- **`config/versions/v18/v18.go`**：`Version` 实现 `ConfigVersion`——
  - `UpgradeConfig`：**仅当 `llm` 段缺失**时插入 `{"enabled":false}`（写进文件的内容自己说清含义）；任何已存在的形状——显式 false、显式 true、空对象、**null**、键序任意——一律原样保留（迁移不覆盖操作员的表态）。
  - `DowngradeConfig`：降级时删除 `llm` 段；无段时 no-op。
  - 迁移专用类型本地化在 `v18/` 包（不污染 config 包）；历史版本零改动。
- **`config/versions/register.go`**：注册 v18。
- **`config/versions/versions_test.go`**：版本计数 18→19、v18 索引断言。
- **`config/config_test.go`**：既有升级断言 17→18；**新增 `TestReadVersion17ConfigAddsLLMSection`**——用**真实加载器** `ReadConfigFromFile`：① 无 llm 段的 v17 配置 → 版本升 18、`llm.enabled=false`、`CheckLLMConfig` 后 `runMode=READ_ONLY`（默认值补齐）② 显式 `{"enabled":true,"runMode":"SHADOW"}` → 原样保留。
- **`testdata/configtest.json`**：`version` 17→18。
- **`config/versions/v18/v18_test.go`**：守卫矩阵 7 例（缺失/显式 false/true/空对象/null/键序/嵌套同名键不误判）+ 降级 2 例。

## 2. 规范要点落实（docs 09 §4.3 + CODING_GUIDELINES）

| 要求 | 落实 |
| --- | --- |
| 归一化匹配（键序/大小写不漏判） | jsonparser 按键路径取值，天然键序无关；大小写：JSON 键区分大小写，`"LLM"` 不算 `llm` 段（与 config 结构体反序列化一致——结构体 tag 也是小写精确匹配） |
| 显式 false/true/omitted/null 区分 | 四态 + 空对象全测，**present 即不覆盖** |
| 守卫两侧都测 | 有段/无段各为独立用例（单测 + 加载器测各覆盖） |
| no-op 也推进版本号 | 加载器测：显式段场景同样断言 `Version==18`（Deploy 统一写版本） |
| 真实加载器测试 | `TestReadVersion17ConfigAddsLLMSection` 走 `ReadConfigFromFile`（不只看迁移函数） |

## 3. 验证

- `go test ./config/... -count 1`：config + 19 个 versions 包**全绿**。
- 引擎整套（-skip 两个既有 flaky）：**全绿**（本次网络窗口良好）；`./llm/... ./cmd/... ./database/repository/llm/` **全绿**。
- `go build ./...`、`go vet ./config/...`、`golangci-lint run ./config/...` **0 issues**（含 dogsled×2 与 encoded-compare×2 的处置：`//nolint:dogsled` 注明「只需 error」、降级断言 `//nolint:testifylint` 注明「字节序即断言点」）。

## 5. 同日续段：M2-13 两残余收口

- **残余①（资金费率窗口分支）**：`llm/trigger`——`Policy.FundingWindowLead`（默认 **15m**）+ `Validate` 拒绝「armed 但零窗」（零窗会在每根带结算时间的 bar 上开火）；`Detect` 新增 `KindFundingWindow` 分支（比较 `NextFundingAt` 与事实的 close，窗内即报、需确认——它是「去看一眼」不是「动手」）；`FactFromSnapshot` 把 `snapshot.FundingFacts.NextFundingTime` 填进 `Fact.NextFundingAt`（此前该字段永远为零，分支即使写了也永不触发——同一接缝的两半）。
- **残余②（账户事件入口）**：`TriggerScanner.RaiseAccountEvent(marketID, detail, at)`——账户侧是这些事件（成交/强平/保证金变动）唯一能看到的地方，扫描器无法自发现；事件**不需确认**（已发生的事实不是待复核的读数），但**过同一每市场节流**（一串成交描述的是一个时刻）；空市场报 `trigger.ErrNoMarket`、nil 接收者 fail-closed。
- **测试**：trigger 包 4 个新测试（窗内开火/窗外/未知时间/未武装/过去时间/零窗守卫/Armed 列表）+ engine 2 个新测试（快照→事实的结算时间、账户事件节流入队与 nil/空参）。lint 0；聚焦全绿；引擎整套见复跑。
- **残余尾项更新（见 §6）**：「触发配置解锁」与「生产扫描器构建点」同日完成；「上游账户/下架事件生产者」（失效矩阵 #18 同源）仍属装配。

## 6. 同日续段二：触发配置解锁 + 扫描器生产装配

M2-13 残余尾项里的「触发配置解锁」与「生产扫描器构建点」同日落地。此前的状态概貌：`engine/llm_trigger.go` 的扫描器写好了、`llm/trigger` 的 FundingWindow/ATRSpike 分支也齐了，但**生产路径上旋转器造好了却没有扫描器喂它**——`SetupRotation` 只搭 `CoverageRotator`，`attachScanner` 只在测试里被调用过。本段把 配置 → Policy → 扫描器 → 旋转器 这条链焊死在生产装配路径上。

- **配置面（Q5 两个可选触发从文档走进配置）**：`config/llm_config_types.go` 新增 `Triggers LLMTriggerConfig`（`json:"triggers"`）——`FundingWindow bool`、`FundingWindowLeadSeconds int`、`ATRSpike bool`、`ATRSpikeMultiple float64`（无指针三态）。**两开关默认 OFF** 不变（Q5 定案：armed 集是 K 线收盘/MACD 交叉/均线穿越；这两个是 opt-in）。`CheckLLMConfig` 对 `<= 0` 填充缺省（`FundingWindowLeadSeconds=900` 即 15m，与 `trigger.DefaultPolicy` 同值；`ATRSpikeMultiple=1.5`）——**正数显式值保留**，0/负值视为未配置（因此配置面产不出「武装但零窗」的 Policy，残余①的 Validate 零窗守卫只拦直接构造 Policy 的调用方）。
- **Policy 叠加**：`engine/llm_rotation_setup.go` 新增 `triggerPolicyFromConfig(cfg)`——从 `trigger.DefaultPolicy` 出发只叠加四个配置值，其余策略字段仍只有一处默认来源。
- **生产装配点**：`SetupRotation` 现在构建 `NewTriggerScanner(triggerPolicyFromConfig(cfg), time.Now)`，解析快照周期（`snapshotInterval(interval)`）并 `rotator.attachScanner(scanner, &params, scanInterval)` 挂到旋转器——**每次整点重载后喂给 `scanAfterReload` 的就是这个扫描器**。与 M2-16 的语义成对：**只重载不喂触发的扫掠什么也发现不了**，两者必须同生于一处装配。
- **签名变更**：`attachScanner(s *TriggerScanner, params *snapshot.IndicatorParams, interval snapshot.Interval)` 第三参落进 `Rotator.scanInterval`（重载后扫描必须知道 bar 周期才能把离线窗口翻译成 bar 数）；`llm_rotation_test.go` 调用点同步传 `snapshot.Interval1h`。
- **测试**：`engine/llm_trigger_config_test.go`（`TestTriggerPolicyFromConfig`：默认 OFF 读进、武装后 lead/倍数逐值断言、零配置回落 `DefaultPolicy`）+ `config/llm_trigger_config_test.go`（`TestCheckLLMConfigFillsTriggerDefaults`：缺省填充 900/1.5、正数保留、两开关默认 false）。`config_example.json` 与 `testdata/configtest.json` 经 `make lint_configs` 对齐（OK）。
- **验证**：全树 `golangci-lint run` **0 issues**；`make misc_checks` **All passed**（两条含 must 的断言措辞同批修为 should）；`go test ./engine/ ./config/... ./llm/trigger/ -count 1`（-skip 两个既有 flaky）**exit 0**——engine 93.955s、config+19 versions+trigger 全 ok。
- **不改变计分**：M2-13 早已勾选计分，本段是其「残余更新二」的生产装配落点；记账不动。

## 4. 未完成与下一步

- M0 组至此 **14/14 全部完成**；配置版本链推进到 **v18（当前最新）**。
- 余下排队：SHADOW 硬风控事实（`RiskFacts` 生产装配，需覆盖 contract 规格/盘口滑点/排名/未对账部分成交等事实源——宁可继续 nil 记录 awaiting-verdict，不可半套事实放行）、`M2-13` 末项（上游账户/下架事件生产者）、M6-03/04/05/07/08（测试网凭据）、前端 W4–W6 页面。
