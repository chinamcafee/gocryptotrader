# 2026-10-10 session-06 — 共用 HTTP 加固层 + Jev 客户端 + 探针（M1-04 / M1-07）

## 会话目标

落地 System One 的网络接入：先抽**共用的 HTTP 加固层**（避免 Jev 与 OpenAI 两处实现漂移），再在其上实现 Jev 客户端与能力探针。

## 完成的子任务

`M1-04`（Jev HTTP 客户端）· `M1-07`（探针）

附带完成一项本应在 M1-02/M1-03 才需要的工作：**共用 HTTP 加固层**（`llm/httpclient/`），使 OpenAI 与 Jev 两条链路共享同一份安全策略。

## 具体做了什么

| 文件 | 内容 |
| --- | --- |
| `llm/httpclient/httpclient.go` | 加固的 `http.Client` 构造：**禁重定向**（`ErrUseLastResponse`，否则 Authorization 会被带到攻击者主机）、**不读环境变量代理**（`Proxy` 显式置 nil，机级代理会静默收到模型凭据）、**DNS 解析后校验每个地址**（link-local / 元数据 / 私网）、**连到已校验的地址而非重新解析**（关掉 DNS rebinding 窗口）、**响应体经 cap 读取**（`LimitReader(max+1)`，先读再判会已经分配完内存）、HTTPS 强制 + loopback 例外 |
| `llm/httpclient/httpclient_test.go` | 9 个测试函数：URL 校验、地址策略矩阵（14 例）、尺寸闸、状态错误、**不跟随重定向**、**不读环境代理**、解析只发生一次 |
| `llm/jev/client.go` | `Client`（**自身不持有任何凭据**）+ `SecretFn` 每次调用取密钥（解密后的密钥只在单次调用内存活）+ `SystemOne()` 两步（发送 → `ValidateResponse`）+ `Profile` |
| `llm/jev/probe.go` | `Probe()`：合成三题请求 + 三类校验复用评审路径的同一套规则 + **15 个稳定错误码** + `ErrorCode(err)` 映射 |
| `llm/jev/client_test.go` | 12 个测试函数，全部基于 `httptest` **离线**运行 |

### 落实的关键约束

| 约束 | 实现 | 为什么重要 |
| --- | --- | --- |
| **禁重定向** | `CheckRedirect` 返回 `http.ErrUseLastResponse` | 重定向会把 `Authorization: Bearer <模型密钥>` 转发到另一台主机 |
| **不读环境代理** | `Transport.Proxy = nil`（仅当显式配置 `ProxyURL` 才设） | 机级 `HTTPS_PROXY` 会静默收到模型凭据；测试用 `t.Setenv` 显式证明这一点 |
| **连到已校验的地址** | `DialContext` 解析一次 → 逐个 `CheckAddress` → `dialer.DialContext(已校验 IP)` | 若连主机名就会二次解析，中间存在可被利用的重绑定窗口 |
| **元数据地址无条件拒绝** | `169.254.169.254` 与 link-local 段即使开了私网访问也拒 | 指向该地址会交出实例凭据 |
| **loopback 随私网开关** | `ip.IsLoopback()` 归入 `AllowPrivateNetwork`，不做单独规则 | macOS 上 `localhost` 常先解析到 `::1`；若把 `::1` 单独拉黑，loopback 例外会失效（本轮实际踩到，见坑 1） |
| **loopback 在构造期即拒绝** | `New()` 对 loopback + 未授权私网直接报错，不等到拨号 | 更严格、失败更早、错误信息更清楚 |
| **密钥逐次取用** | `SecretFn(ctx)` + 空密钥即拒 | 客户端自身不持有任何值得从内存转储里拿走的东西 |
| **探针零市场数据** | `NewSyntheticProbeState` 只含一句免责声明；测试断言请求体不含 `BTC`/`USDT`/`marketId`/`price`/`candle`/`position` | 探针可能打到配置错误的端点，唯一能泄露的只能是"发生了一次请求" |
| **探针复用评审校验** | `ValidateChoiceAnswer` / `ValidateScoreAnswer` / `ValidateNoulAnswer` 由 `validate.go` 导出，探针走同一套规则 | 通过探针即证明真实校验代码可用，而不是另写一套更松的检查 |
| **探针失败也返回结果而非 error** | `Probe()` 返回 `*ProbeResult{Passed:false, ErrorCode, ErrorMessage}` | 调用方可以无条件落库一行探针记录 |

## 验证证据（真实跑出）

```text
go test ./llm/... -count 1
  →  ok  llm/candidate    0.530s
  →  ok  llm/httpclient   0.731s
  →  ok  llm/jev          0.571s
go build ./llm/...   →  通过
gofmt -w llm/        →  无差异
```

客户端测试覆盖：profile 拒绝（nil 密钥回调 / 别名模型 / 空端点 / 公网 http）；端点路径拼装（含尾斜杠归一）；**happy path 断言请求体确实携带了 "不可信数据"框架与证据来源标记**；模型不匹配在发送前即拒；6 类错误码映射（401/429/502/非 JSON/schema/模型漂移）；尺寸闸；密钥回调失败透传；**context 截止时间映射为 TIMEOUT**；nil 请求与 nil 客户端；探针 happy path（含 token 用量与服务模型落位）；5 类探针失败；nil 客户端探针。

## 遇到的坑与解决

### 坑 1：`::1` 被无条件拉黑，会让 loopback 例外形同虚设（已修实现）

最初把 `::1/128` 放进"无条件拒绝"的 `blockedNets`。但 macOS 上 `localhost` 常先解析到 `::1`，而 `CheckAddress` 会校验**全部**解析结果 —— 结果是 `localhost` 端点永远连不上，loopback 例外失效。

**修正**：把 loopback 独立出来，跟随 `AllowPrivateNetwork` 开关；link-local 与元数据地址仍无条件拒绝。新增测试覆盖 `127.0.0.1`/`::1` 在开关两侧的行为。

### 坑 2：`t.Setenv` 不能与 `t.Parallel()` 共存

`TestClientIgnoresEnvironmentProxy` 需要写环境变量，但 `testing` 包在并行子测试中禁止 `Setenv`，会 panic：`testing: test using t.Setenv ... can not use t.Parallel`。

**修正**：该测试去掉 `t.Parallel()` 并注释说明原因。

### 坑 3：`time.Duration` 字面量单位笔误（我在测试里犯的经典错误）

测试里写 `Timeout: 2` 意为"2 秒"，实际是 **2 纳秒**，导致客户端在拨号前就超时，把一个"地址应被拒绝"的断言变成了"context deadline exceeded"。

**修正**：改为 `2 * time.Second` 并在测试里加注释提醒。**给接力的提醒**：本包所有 `Timeout` 字段都是 `time.Duration`，写裸数字前先确认单位。

### 坑 4：两处测试期望写错（不是实现问题）

- 我原以为"loopback 无授权私网"会在**拨号期**才失败，实际实现在**构造期**就拒 —— 更严格的行为是对的，改测试。
- 已改为分别覆盖"构造期拒绝"与"显式授权后可用"两条路径。

**给接力的提醒**：失败先判断是**实现错**还是**期望错**；本轮 4 个坑里有 3 个是测试自身的错，只有坑 1 是实现问题。**不要为了让测试变绿而放宽实现**。

### 坑 5：一处**因我改动而必须更新**的测试断言（与 flaky 网络测试要区分开）

`TestGetSubsystemsStatus`（`engine/helpers_test.go:101`）断言子系统数量为 **12**，而我给 `GetSubsystemsStatus()` 加了 `llm_manager` → 变成 13，测试失败：

```text
"map[... llm_manager:false ...]" should have 12 item(s), but has 13
```

**这是正当的、必须随实现更新的断言**，不是 flaky。已改为：

```go
statuses := (&Engine{}).GetSubsystemsStatus()
assert.Len(t, statuses, 13, "GetSubsystemStatus should return the correct number of subsystems")
assert.Contains(t, statuses, llmManagerName, "the LLM manager must be tracked so its state is observable")
```

顺手加了一条 `Contains` 断言：只比数量无法防止"未来某个子系统被静默移除、同时另一个被加入"。

> **接力的关键区分**（**必须分清，别混**）：
>
> - **`TestGetSubsystemsStatus` 这类** —— 失败是因为**我的改动改变了被断言的对象**。正确处理是**更新断言**（并尽量让它表达意图，如加 `Contains`），**绝不可以加进 skip 列表**。
> - **`TestGetDefaultConfigurations` / `TestNewExchangeByNameWithDefaults` 这类** —— 失败是因为**测试自己去打真实交易所线上 API**，与改动无关且间歇失败（同命令连跑会忽绿忽红）。正确处理是**带 `-skip` 跑**，不要改测试、不要往 `blockedCIExchanges` 加交易所。
> - `taskProgress/README.md` §4.3 的 skip 列表**只含后两类**；前一类必须保持强制执行。

## 未完成 / 下一步建议

**M1 现为 4/15**。剩余可继续推进的：

1. **`M1-08`** 证据清单 `JevInputManifest`（`requestSha256` / `parsedResponseSha256` / `candidateSha256` / `strategyInstructionSha256` / 每条工具 `resultSha256` / `factsScope` / `tradeAuthorized:false`）+ `InspectManifest` 四态分类（`MANIFEST_BOUND` / `LEGACY_MISSING` / `INVALID` / `NO_ASSESSMENT`）。纯函数，易测，且是"订单准备与发送认领都必须通过"的那道闸。
2. **`M1-01`** 密钥 vault（`llm/secrets/`）：RSA 3072 + AES-GCM 包裹、文件 0600/0700、CLI 无回显录入。`SecretFn` 接口已就位，接上即可。
3. **`M1-02`/`M1-03`** OpenAI 兼容客户端与四层探针 —— **HTTP 加固层已就绪，直接复用 `httpclient.New` + `PostJSON`**（这是本轮先把加固层抽出来的原因）。
4. **`M1-09`~`M1-13`** 依赖 provider 档案表，需先完成 `M0-04`（数据库迁移）。

**不要忘记**：新增 gRPC/REST 端点前必须先校准 proto 工具链（`TASKS.md` §0.1）。

## 环境状态快照

| 项 | 状态 |
| --- | --- |
| GoCryptoTrader bot | 运行中，PID 18809，`-portfoliomanager=false -llmmanager`，9052/9053 |
| webUI 代理 / Vite | 运行中，8788 / 5173 |
| `llm/` 树 | 4 个包：`llm` / `candidate` / `httpclient` / `jev`，全部测试绿 |
| proto 工具链 | 已装但未校准（`TASKS.md` §0.1） |
