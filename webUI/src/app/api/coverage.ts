/**
 * 覆盖范围选择规则 + 两层轮询预算算术。
 *
 * ⚠ 这是 `llm/coverage/scope.go` 与 `llm/coverage/plan.go` 的**前端镜像**。
 * 两份实现是已知的重复，取舍如下：
 *   - Go 侧是权威（有 44 个测试函数覆盖，含用实测 789 个 symbol 形状做的夹具）；
 *   - 这里存在的原因：RPC 未打通（`LLMGetCoverage` 被 proto 工具链阻塞，见 TASKS.md §0.1），
 *     而把真实数据先渲染出来比继续显示"待落地"更有价值；
 *   - **打通 RPC 后应删掉本地规则**，改成消费后端返回的 ScopeSummary，届时此处只留渲染。
 *
 * 之所以把规则集中在这一个文件而不是散在页面里：如果将来两处漂移，只可能漂移在这一个文件里，
 * 对比 `llm/coverage` 就能定位。散在 JSX 中的规则没有对照物。
 */

// ── 权重常量（单位：Binance request weight）─────────────────────────────────
// 依据 llm/coverage/plan.go。K 线分档按 Binance 真实表，是**半开区间**：
// limit 99 算 1，limit 100 算 2（文档 04 §7.4 写作 "≤100 → 1"，差了 1）。
export const WEIGHT_TICKER_BATCH = 40
export const WEIGHT_FUNDING_BATCH = 10
export const EXCHANGE_WEIGHT_PER_MINUTE = 2400

export const DEFAULT_ROTATION_CYCLE_MINUTES = 10
export const DEFAULT_KLINE_INCREMENT_LIMIT = 5
export const DEFAULT_KLINE_FULL_LIMIT = 250
export const DEFAULT_FULL_RELOAD_SPREAD_MINUTES = 5
export const DEFAULT_WEIGHT_BUDGET_FRACTION = 0.6

/** K 线一次性拉 `limit` 根的权重（Binance 半开区间分档） */
export const klineWeight = (limit: number): number =>
  limit < 100 ? 1 : limit < 500 ? 2 : limit < 1000 ? 5 : 10

/** 范围外 symbol 的示例最多展示几个 */
const MAX_EXAMPLES = 8

export type ExclusionReason = 'DATED_FUTURE' | 'OTHER_QUOTE' | 'EMPTY'

export interface SymbolRow {
  symbol: string
  base: string
  quote: string
  inScope: boolean
  /** 范围外的原因；在范围内时为 undefined */
  reason?: ExclusionReason
  enabled: boolean
}

export interface ScopeReport {
  received: number
  selected: number
  datedFutures: number
  otherQuote: number
  empty: number
  excluded: number
  examples: string[]
}

export interface RotationPlan {
  markets: number
  cycleSeconds: number
  /** 一次全量保温轮询的总权重 */
  rotationTotal: number
  rotationPerMinute: number
  /** 整点全量重载的总权重与摊开速率 */
  reloadTotal: number
  reloadPerMinute: number
  /** 轮询 + 重载同时段：这才是要跟预算比的数 */
  peakPerMinute: number
  budgetPerMinute: number
  /** 生成 budgetPerMinute 用的比例，供文案展示，避免把 0.6 硬编码到第二处 */
  budgetFraction: number
  headroomPerMinute: number
  /** 增量调用的匀速间隔（毫秒） */
  incrementSpacingMs: number
}

/**
 * 把交易所给的 `availablePairs` 字符串切成带分类的行。
 *
 * 规则（对齐 `llm/coverage/scope.go` 的 `SelectMarkets`）：
 *   - 尾部是 `_USDT` 的才算 USDT 本位永续；
 *   - 尾部是 6 位交割月份的算交割合约（无永续资金费率）；
 *   - 其余（`_USDC`/`_USD1`/法币/代币化股票代码形状等）算其它计价；
 *   - 空串单独计数——不静默丢弃，"789 里扣掉 201 是哪 201" 要能回答。
 *
 * 注意**不**用 ASCII 正则限制 base：`币安人生_USDT`、`我踏马来了_USDT` 是真实可交易的
 * USDT 永续，用 `^[A-Z0-9]+` 会静默丢掉真市场。
 */
export function classifySymbols(availableCsv: string | undefined, enabledCsv: string | undefined): {
  rows: SymbolRow[]
  report: ScopeReport
} {
  const raw = (availableCsv ?? '').split(',')
  const enabled = new Set(
    (enabledCsv ?? '')
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
  )

  const rows: SymbolRow[] = []
  const report: ScopeReport = {
    received: raw.length,
    selected: 0,
    datedFutures: 0,
    otherQuote: 0,
    empty: 0,
    excluded: 0,
    examples: [],
  }

  const note = (symbol: string) => {
    if (report.examples.length < MAX_EXAMPLES) report.examples.push(symbol)
  }

  for (const entry of raw) {
    const symbol = entry.trim()
    if (!symbol) {
      report.empty += 1
      continue
    }

    let reason: ExclusionReason | undefined
    if (/_\d{6}$/.test(symbol)) {
      reason = 'DATED_FUTURE'
      report.datedFutures += 1
      note(symbol)
    } else if (!symbol.toUpperCase().endsWith('_USDT') || symbol.toUpperCase() === '_USDT') {
      reason = 'OTHER_QUOTE'
      report.otherQuote += 1
      note(symbol)
    }

    const inScope = reason === undefined
    if (inScope) report.selected += 1
    rows.push({
      symbol,
      // base 是最后一个 `_` 之前的部分：1000PEPE_USDT → 1000PEPE
      base: symbol.slice(0, symbol.lastIndexOf('_')),
      quote: symbol.slice(symbol.lastIndexOf('_') + 1),
      inScope,
      reason,
      enabled: enabled.has(symbol),
    })
  }

  report.excluded = report.received - report.selected
  // 在范围内的排前面，其余按 symbol 排序，保证渲染稳定
  rows.sort((a, b) => (a.inScope === b.inScope ? a.symbol.localeCompare(b.symbol) : a.inScope ? -1 : 1))
  return { rows, report }
}

/**
 * 两层轮询第一层的预算算术（对齐 `llm/coverage/plan.go` 的 `BuildCycle`）。
 *
 * 峰值 = 轮询速率 + 重载速率，因为整点两者同时段。这个数必须小于预算，
 * 否则计划在配置期就该被拒（Go 侧返回 ErrBudgetExceeded）。
 */
export function buildRotationPlan(
  markets: number,
  opts?: {
    cycleMinutes?: number
    incrementLimit?: number
    fullLimit?: number
    spreadMinutes?: number
    budgetFraction?: number
  },
): RotationPlan {
  const cycleMinutes = opts?.cycleMinutes ?? DEFAULT_ROTATION_CYCLE_MINUTES
  const incrementLimit = opts?.incrementLimit ?? DEFAULT_KLINE_INCREMENT_LIMIT
  const fullLimit = opts?.fullLimit ?? DEFAULT_KLINE_FULL_LIMIT
  const spreadMinutes = opts?.spreadMinutes ?? DEFAULT_FULL_RELOAD_SPREAD_MINUTES
  const fraction = opts?.budgetFraction ?? DEFAULT_WEIGHT_BUDGET_FRACTION

  const cycleSeconds = cycleMinutes * 60
  const rotationTotal =
    WEIGHT_TICKER_BATCH + WEIGHT_FUNDING_BATCH + klineWeight(incrementLimit) * markets
  const rotationPerMinute = rotationTotal / cycleMinutes
  const reloadTotal = klineWeight(fullLimit) * markets
  const reloadPerMinute = reloadTotal / spreadMinutes
  const peakPerMinute = rotationPerMinute + reloadPerMinute
  const budgetPerMinute = Math.floor(EXCHANGE_WEIGHT_PER_MINUTE * fraction)

  return {
    markets,
    cycleSeconds,
    rotationTotal,
    rotationPerMinute,
    reloadTotal,
    reloadPerMinute,
    peakPerMinute,
    budgetPerMinute,
    budgetFraction: fraction,
    headroomPerMinute: budgetPerMinute - peakPerMinute,
    // 匀速：周期秒数 / 合约数，向下取整到毫秒（Go 侧同样截断）
    incrementSpacingMs: markets > 0 ? Math.floor((cycleSeconds * 1000) / markets) : 0,
  }
}

/** 计划是否放得进预算——放不进就不该启动，而不是等凌晨被闸拒 */
export const planFits = (p: RotationPlan): boolean => p.peakPerMinute < p.budgetPerMinute

/** 范围外原因的展示文案 */
export const EXCLUSION_LABEL: Record<ExclusionReason, string> = {
  DATED_FUTURE: '交割合约（无永续资金费率）',
  OTHER_QUOTE: '非 USDT 计价',
  EMPTY: '空条目',
}
