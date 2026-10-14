# session-29：W5-01 对话工作区收尾（Conversations 侧栏 + Markdown + Sources + 客户端内存草稿）

**日期**：2026-10-11
**关联任务**：`W5-01`（08 §1/§4.3、Q14=C）；前置：M3-09 已吃掉真流式半壁
**账目**：（56）W5 0/8 → **1/8**（0.00% → 1.50%）；**总完成度 68.2% → 69.7%**

## 1. 交付物

- `webUI/src/pages/ChatPage.tsx` 重写为「左栏会话列表 + 右侧对话」两栏工作区：
  - **AntD X `Conversations` 侧栏**：会话项 = 两行标签（标题 + 「n 条 · 已归档」）；per-item `menu`（归档，已归档置灰）替代原顶部「归档当前」按钮；`activeKey` 双向绑定；顶部保留「新建对话」。归档当前会话时同步清掉它的内存草稿。
  - **Markdown 渲染**：新增依赖 `react-markdown@10.1.0` + `remark-gfm@4.0.1`（pnpm）。**模型输出是外部数据**——渲染为 React 元素（标题/列表/表格/代码/链接全部组件映射），**不引入任何 HTML 直通**（无 rehype-raw、无 dangerouslySetInnerHTML）；链接统一 `target=_blank rel=noreferrer`。样式用主题无关的半透明底（明暗主题都成立）。
  - **`Sources` 证据展开视图**：证据卡片从裸 Tag 换成 `Sources`（「证据来源（n）」+ 每项两行：`tool｜marketId` / `质量 · 源时间 · sha256 截断 · 引用 ref`，`expandIconPosition=end`）。**字段渲染进 title 而非 description**：实测该版组件 description 只在 popover 中可见，而 popover 触发对合成事件不响应——把「可核对字段」放进 title 保证静态可见（sha256 截断 12 位展示，完整值仍在数据里）。
  - **草稿仅存客户端内存**：`drafts: Record<conversationId, string>` 组件态——切换会话保留、发送即清、刷新即丢、**从不落库**；Sender 改为受控（`value`/`onChange`），确认发送才走 `streamAssistantMessage` 落库。底部一行文案说明语义。
- `webUI/scripts/mock-gateway.mjs`：流式回复载荷升级为 **Markdown**（h2 + 列表 + 行内 code + GFM 表格 + 长 sha256）——离线浏览器实测从此真实覆盖 Markdown 渲染路径（此前回复是纯文本，测不到渲染）。

## 2. 机器化约束（长期有效）

- **模型输出永不进 DOM 直通**：对话渲染只走元素映射；`rehype-raw` 一类插件是注入面扩张，不引。
- **草稿纪律**：编辑器内容留在客户端内存、发送才写后端——「不落半成品」（05 §1）在输入侧的镜像。
- **证据字段静态可见**：sha256/质量/源时间/引用不依赖悬浮交互即可核对（title 两行渲染，而非 popover）。
- **mock 的流式载荷含 Markdown**：前端渲染改动必须用 mock 回复实测 Markdown（不必临时手改 mock）。

## 3. 验证（浏览器实测：mock gateway 9443 + 代理 8788 + vite 5173）

- **流式 + Markdown**：新建会话 → 发送 → 增量渲染至完成；无障碍树出现 `heading "BTC_USDT 1h 结构" level=2`、列表项、行内 `MACD`、**GFM 表格逐单元格**（指标/读数/判定 × RSI/MACD 柱）——表格是真 `<table>` 元素而非纯文本。
- **Sources 展开/收起**：点击标题展开出条目行，再点收起（正文长度 -53 字符即条目行移除）；证据卡呈现「质量 EXCHANGE_REST｜源时间 2026-10-11T11:29:49.930Z｜sha256 abcdef012345…｜引用 tool:getCandleWindow:call-1」全部字段。
- **草稿客户端内存**：会话 A 输入「草稿B-仅内存」→ 新建会话（编辑器为空）→ 切回 A（草稿仍在）——per-conversation 留存成立；发送后草稿即清。
- **门**：`tsc --noEmit` 0 · `oxlint src proxy` 0 warnings 0 errors（29 files）· `pnpm build` 成功（chunk 大小告警为既有）。

## 4. 未完成与下一步

- W5 组余项：`W5-02`（证据卡片独立面——当前证据已在对话内呈现，独立卡面/血缘可复用 Sources 数据）、`W5-03`~`W5-08`（条款提取/澄清交互/版本树/冻结审批/市场授权/策略启停）——全部依赖 M4 已活的 strategies/approvals RPC 与 W1-07 端点补齐。
- 队列：前端 W4（Providers）/W6（Decisions）页面（后端 14 RPC 已全部为活）、`W7-07` 覆盖运行面、SHADOW `RiskFacts` 生产装配、`M2-13` 末项（上游账户/下架事件生产者）。
