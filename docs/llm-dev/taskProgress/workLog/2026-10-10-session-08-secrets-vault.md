# session-08：密钥 vault（M1-01）

**日期**：2026-10-10
**范围**：`M1-01` 密钥 vault（后端 `llm/secrets/`）
**耗时**：单会话
**前置**：session-07（M1-08 Jev 证据清单）已完成，`llm/candidate`、`llm/httpclient`、`llm/jev` 三包全绿

---

## 1. 交付物

| 文件 | 内容 |
| --- | --- |
| `llm/secrets/vault.go` | vault 目录布局、RSA 3072 包裹密钥对生成/加载、`Vault.Add/Reveal/Fingerprint/List/Remove`、`Entry`、`Fingerprint()` 助手 |
| `llm/secrets/ref.go` | `Ref{Scheme,Kind,ID}` + `String()` + `Validate()` + `ParseRef()` + 内部 `refStringFor()` |
| `llm/secrets/vault_test.go` | 20 个测试函数，覆盖布局/权限/往返/搬移/篡改/清单/删除/引用解析 |

## 2. 设计要点（为什么这样做）

### 2.1 两层密钥结构

```text
RSA 3072 密钥对（vault-private.pem / vault-public.pem，0600）
  └─ 只用来包裹「每条密钥各自的随机 AES-256-GCM 数据密钥」
       └─ 数据密钥加密真正的明文（模型 API Key）
```

**理由**：单层 RSA 直接加密长明文会触发长度上限，且需要分块；RSA 只包 32 字节数据密钥既无长度问题，又保留了**单条泄露不牵连全库**的性质（每条密钥的 data key 独立）。这与 `docs/llm-dev/09-数据存储与配置.md` §5.2 的设计一致。

### 2.2 引用作 AAD（认证数据）

```go
ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(ref.String()))
```

`ref.String()`（`secret://llm/<uuid>`）作为 GCM 的附加认证数据参与校验但**不加密**。后果：把 A 的密文文件复制到 B 的记录路径上，用 B 的引用解密会**认证失败**（`ErrDecryptFailed`），而不是解出错误内容。这堵住了"密文换位 + 记录混淆"这一类攻击。测试 `TestRevealDetectsAMovedRecord` 直接验证这个性质。

### 2.3 指纹是摘要而非前缀

```go
func Fingerprint(plaintext string) string {
    sum := sha256.Sum256([]byte(plaintext))
    return hex.EncodeToString(sum[:fingerprintBytes]) // 8 字节 = 16 位 hex
}
```

**理由**：若用 key 前缀作指纹，会泄露长度和开头字符；用摘要则**不泄露长度也不泄露任何片段**。`List()` 只返回指纹 + 引用 + 创建时间，调用方能在 CLI/UI 上让用户"认出是哪把 key"而**永远拿不到明文**。测试 `TestRecordFilesNeverContainPlaintext` 断言磁盘字节里不含明文、也不含去掉 `sk-` 前缀后的片段。

### 2.4 权限双保险

`os.WriteFile(path, body, FileMode)` 的 `FileMode` 只在**文件新建时**生效，且会受 umask 影响。因此每个写盘处**紧跟一次 `os.Chmod`**：

- `vault.go` 的 `writePEM` 末尾 `os.Chmod(path, FileMode)`
- `Vault.Add` 写记录后 `os.Chmod(v.pathFor(ref), FileMode)`
- `Init` 建目录后 `os.Chmod(dir, DirMode)`（`MkdirAll` 同样受 umask 影响）

目录 `0700` / 文件 `0600`。测试 `TestInitCreatesProtectedLayout` 断言**实际 stat 出来的 perm**，而不是常量本身。

### 2.5 `Init` 幂等 + 单入口

生成与加载共用 `Init()`：先 `os.Stat` 私钥，存在则转 `Open()`，不存在才生成。**理由**：如果允许调用方在已有 vault 上再次生成，就会产生第二对密钥，第一对加密的密钥全部不可读——这是个静默数据丢失。测试 `TestInitReusesAnExistingKeyPair` 断言第一次写的密钥能被第二次 `Init` 打开。

`Open()` 在不存在的目录上返回 `ErrVaultNotInitialised`，与"空目录"区分于其他 I/O 错误。

### 2.6 引用校验在文件访问之前

`load()` / `Remove()` 都先 `ref.Validate()` 再拼路径。`Validate()` 拒绝 `kind`/`id` 中的 `/`、`\`、空格，所以 `../escape` 这类路径穿越在**任何文件系统调用之前**就被 `ErrInvalidRef` 拦住。测试 `TestRevealMissingSecret`、`TestRemoveDeletesTheSecret` 都带了穿越用例。

### 2.7 nil 接收者安全

所有导出方法首行检查 `v == nil || v.priv == nil` → `ErrVaultNotInitialised`。**理由**：子系统在配置缺失时可能持有一个未初始化的 `*Vault`，nil panic 会把"未配置"变成崩溃。测试 `TestNilVaultIsSafe` 覆盖 5 个方法。

## 3. 与 `SecretFn` 的接线（已完成，未接入）

`llm/jev/client.go` 已定义：

```go
type SecretFn func(ctx context.Context) (string, error)
```

session-06 落地的 `SecretFn` 是"逐次取用、客户端不缓存"的形态，与 vault 的 `Reveal(ref)` 天然契合：

```go
secretFn := func(ctx context.Context) (string, error) { return vault.Reveal(ref) }
```

**尚未接线**——`Vault` → `SecretFn` 的适配函数应在 M1-09（Provider Profile CRUD）里做，因为那时才有"从 DB 读 profile → 读 ref → 取密钥"的完整路径。此处不提前抽象。

## 4. 未完成项（明确记录，避免后续模型误判）

| 项 | 归属 | 说明 |
| --- | --- | --- |
| CLI 无回显录入 | **M1-14** | `TASKS.md` 把 M1-01 描述为"…CLI 无回显录入"。vault 侧 `Add()` 已就位，但 `gctcli providers` 命令族属 M1-14，而 M1-14 需要新增 gRPC 端点 → **被 proto 工具链阻塞**（`TASKS.md` §0.1）。因此 M1-01 标记为 `[x]` 但明确记录这半个缺口 |
| 密钥轮换 | 无 | 方案未要求。`Add` 可重复调用产生新 ref，旧 ref 手动 `Remove` 即可 |
| 多档案（多把 key 并存） | 已具备 | `Add` 每次产生新 ref，`List` 返回全部；没有"唯一启用"约束，那属于 M1-10（配对 CAS） |

## 5. 校验证据

```text
$ gofmt -l -w llm/secrets/          # vault.go 被重排（结构体字段对齐）
$ go build ./llm/...                # 通过
$ go test ./llm/...  -count 1
?   github.com/thrasher-corp/gocryptotrader/llm            [no test files]
ok  github.com/thrasher-corp/gocryptotrader/llm/candidate   0.531s
ok  github.com/thrasher-corp/gocryptotrader/llm/httpclient  1.772s
ok  github.com/thrasher-corp/gocryptotrader/llm/jev         1.845s
ok  github.com/thrasher-corp/gocryptotrader/llm/secrets     2.875s
$ go test ./llm/secrets/ -count 1 -v | grep -c '^=== RUN'
20
```

**测试函数清单**（20）：

- `TestInitCreatesProtectedLayout` / `TestInitReusesAnExistingKeyPair` / `TestOpenWithoutKeyPairIsRejected`
- `TestAddRevealRoundTrip`（含 unicode、4096 字节长明文）/ `TestAddRejectsEmptySecrets` / `TestAddDefaultKind`
- `TestRecordFilesNeverContainPlaintext`
- `TestRevealDetectsAMovedRecord` / `TestRevealDetectsTampering` / `TestRevealDetectsAMalformedRecord` / `TestRevealMissingSecret`
- `TestFingerprintIsStableAndDistinct` / `TestVaultFingerprintMatchesTheHelper`
- `TestListReturnsFingerprintsOnly` / `TestListIgnoresForeignFiles`
- `TestRemoveDeletesTheSecret`
- `TestNilVaultIsSafe`
- `TestRefStringAndValidate` / `TestParseRef` / `TestParseRefRoundTripsEveryStoredSecret`

全部用 `t.TempDir()`，**无外部依赖、无网络、无固定路径**，可离线连续跑。

## 6. 给接手模型的具体建议

**下一个可做的（不依赖 proto、不依赖 DB）**：

1. **`M1-02` LLM HTTP 客户端**（OpenAI 兼容）——`llm/httpclient` 已经把加固层抽好了（禁重定向 / 不读环境代理 / DNS 后校验并连已校验地址 / 响应 cap / 元数据地址拒绝）。`M1-02` 只需在其上写 OpenAI `chat/completions` 的请求构造 + 响应解析 + 工具调用循环的**纯函数部分**。参考 `llm/jev/client.go` 的两步式（发送→校验）形态。
2. **`M1-03` 分层探针**（AUTH / TOOL_CALL / SCHEMA / STREAM）——照 `llm/jev/probe.go` 的形态：失败也返回 `ProbeResult` 而非 error，15 个稳定错误码的映射风格可复用。

**被阻塞的**：

- `M1-09`~`M1-13` 需要 DB 表（`llm_provider_profiles` 等）→ 先做 `M0-04`（8 个 PostgreSQL 迁移）
- `M1-14`（providers 命令族）需要新 gRPC 端点 → 需要先解决 `TASKS.md` §0.1 的 proto 工具链幂等问题，或者**延后到 M1 末尾**

**W 组**：`W4`（Providers 页）依赖 `M1-09`+`M1-14`，所以前端此刻不适合再往前推进到 Providers；`W7-04`、`W8-04/06` 不依赖后端，可插空做。
