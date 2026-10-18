/**
 * 领域类型。字段名严格对齐 GoCryptoTrader REST 网关（gRPC-gateway）的 protojson 输出。
 *
 * ⚠ 接力必读：网关输出是 **camelCase**（不是 gctcli 的 snake_case），
 * 且 `pair` 是对象 `{delimiter, base, quote}` 而非字符串。
 * 依据：docs/localDeploy/README.md「实测验证记录」。
 */

export type AssetType = 'usdtmarginedfutures' | 'coinmarginedfutures' | 'spot' | 'margin'

export interface CurrencyPair {
  delimiter: string
  base: string
  quote: string
}

export interface PairRef {
  pair?: CurrencyPair
  currencyPair?: string
}

/** 系统信息（GET /v1/getinfo） */
export interface EngineInfo {
  uptime?: string
  availableExchanges?: number | string
  enabledExchanges?: number | string
  defaultFiatCurrency?: string
  subsystemStatus?: Record<string, boolean>
  rpcEndpoints?: {
    grpc?: { started?: boolean; listenAddress?: string }
    grpcProxy?: { started?: boolean; listenAddress?: string }
  }
}

/** ticker（POST /v1/getticker）。注意：合约 REST ticker 不填 markPrice/indexPrice/openInterest（恒为 0/空）。 */
export interface Ticker {
  exchangeName?: string
  assetType?: string
  pair?: CurrencyPair
  currencyPair?: string
  last?: number
  lastSize?: number
  bid?: number
  ask?: number
  bidSize?: number
  askSize?: number
  high?: number
  low?: number
  open?: number
  open24Hour?: number
  close?: number
  volume?: number
  baseVolume?: number
  quoteVolume?: number
  volumeWeightedAveragePrice?: number
  percentChange24Hour?: number
  markPrice?: number
  indexPrice?: number
  openInterest?: number
  openInterestValue?: number
  lastUpdated?: number | string
}

export interface OrderbookLevel {
  price?: number
  amount?: number
  strPrice?: string
  strAmount?: string
  id?: number
}

/** 盘口（POST /v1/getorderbook）。币安永续实测可拿到 1000 档。 */
export interface Orderbook {
  exchange?: string
  assetType?: string
  pair?: CurrencyPair
  bids?: OrderbookLevel[]
  asks?: OrderbookLevel[]
  lastUpdated?: number | string
  error?: string
}

/** 单根 K 线（GET /v1/gethistoriccandles） */
export interface Candle {
  time: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  isPartial?: boolean
}

export interface KlineItem {
  exchange?: string
  pair?: CurrencyPair
  start?: string
  end?: string
  interval?: string
  candle?: Candle[]
}

/**
 * 技术指标响应（GET /v1/gettechnicalanalysis）。
 * 顶层 `signals` 是 map<string, {signals: number[]}>，键名随 algorithmType 变化，
 * 例如 MACD 返回 MACD / SIGNAL / HISTOGRAM 三个键；EMA 返回 EMA。
 */
export interface TechnicalAnalysisResponse {
  signals?: Record<string, { signals?: number[] }>
}

export type AlgorithmType =
  | 'TWAP' | 'VWAP' | 'ATR' | 'BBANDS' | 'COCO' | 'SMA' | 'EMA' | 'MACD' | 'MFI' | 'OBV' | 'RSI'

/** 资金费率（GET /v1/getlatestfundingrate）。⚠ 实测带 includePredicted=true 会 500，不要传。 */
export interface FundingRateSnapshot {
  rate?: {
    exchange?: string
    asset?: string
    pair?: CurrencyPair
    latestRate?: { date?: string; rate?: number | string }
    predictedUpcomingRate?: { date?: string; rate?: number | string }
    timeOfNextRate?: string
    rates?: Array<{ time?: string; rate?: number | string }>
  }
}

export interface AccountCurrency {
  currency?: string
  totalValue?: number | string
  hold?: number | string
  free?: number | string
  freeWithoutBorrow?: number | string
  borrowed?: number | string
  updatedAt?: { seconds?: number | string; nanos?: number }
}

export interface AccountBalances {
  exchange?: string
  accounts?: Array<{ id?: string; currencies?: AccountCurrency[] }>
}

/**
 * 仓位汇总（GET /v1/getfuturespositionssummary）。
 * 币安 USDT-M 实测字段齐全，是终端页「账户/风险」面板的主要数据源。
 */
export interface PositionStats {
  availableEquity?: string
  averageOpenPrice?: string
  breakEvenPrice?: string
  cashBalance?: string
  collateralUsed?: string
  contractMultiplier?: string
  contractSettlementType?: string
  contractSize?: string
  currentSize?: string
  discountEquity?: string
  equityOfCurrency?: string
  equityUsd?: string
  estimatedLiquidationPrice?: string
  freeCollateral?: string
  frozenBalance?: string
  initialMarginRequirement?: string
  isolatedEquity?: string
  isolatedLiabilities?: string
  isolatedUpl?: string
  maintenanceMarginRequirement?: string
  marginFraction?: string
  markPrice?: string
  notionalLeverage?: string
  recentPnl?: string
  strategyEquity?: string
  totalCollateral?: string
  totalEquity?: string
}

export interface PositionSummary {
  exchange?: string
  asset?: string
  pair?: CurrencyPair
  positionStats?: PositionStats
}

/** 杠杆（GET /v1/getleverage） */
export interface LeverageInfo {
  exchange?: string
  asset?: string
  pair?: CurrencyPair
  leverage?: number
  marginType?: string
  orderSide?: string
}

/** 持仓模式（GET /v1/getcollateralmode）。`single` = 单向，本系统必需。 */
export interface CollateralModeInfo {
  exchange?: string
  asset?: string
  collateralMode?: string
}

/** 期货持仓（GET getallmanagedpositions；网关 protojson camelCase 形状，
 *  方向是 order.Side 词汇表 BUY/SELL，数量为十进制字符串） */
export interface ManagedPosition {
  exchange?: string
  asset?: string
  pair?: CurrencyPair
  currentSize?: string
  currentDirection?: string
  openingDirection?: string
  [k: string]: unknown
}

/** 交易对（POST /v1/getexchangepairs） */
export interface ExchangePairs {
  supportedAssets?: Record<string, { availablePairs?: string; enabledPairs?: string }>
}

export interface ExchangeAssets {
  assets?: string
}

// ── LLM 规划端点（后端尚未实现，见 TASKS.md M0/M1/M4） ──────────────────────

export type DataQuality =
  | 'EXCHANGE_REST' | 'LOCAL_TRACKED' | 'DATABASE_CACHED' | 'DERIVED'
  | 'PARTIAL' | 'GAP' | 'STALE' | 'UNKNOWN'

export type PairReadiness = 'READY' | 'INCOMPLETE' | 'DEGRADED'
export type RunMode = 'READ_ONLY' | 'SHADOW' | 'PAPER' | 'TESTNET' | 'LIVE'
export type Decision = 'HOLD' | 'REVIEW' | 'REJECT' | 'ALLOW'
export type CandidateAction = 'OPEN_LONG' | 'OPEN_SHORT' | 'REDUCE' | 'HOLD' | 'REVIEW'
export type ProviderKind = 'LLM_OPENAI_COMPAT' | 'JEV'

/** GET /v1/llm/status —— 仪表盘的核心状态（docs/llm-dev/08 §3.6） */
export interface LlmStatus {
  runMode: RunMode
  liveUnlocked: boolean
  pairReadiness: PairReadiness
  pairEpoch: number
  llmProfileName?: string
  llmModel?: string
  jevProfileName?: string
  jevModel?: string
  halted: boolean
  haltLevel: 'NONE' | 'SOFT' | 'MARKET' | 'FULL'
  accountReconciled: boolean
  reconciliationAgeSeconds?: number
  runningStrategies?: number
  approvedStrategies?: number
  decisionsToday?: number
  reservationsRemaining24h?: number
  /** REVIEW 过期窗口（小时；服务端随配置下发，未配置回退 12） */
  reviewExpireHours?: number
  newRiskIntentsInWindow?: number
  newRiskWindowLimit?: number
  clockDriftMs?: number
  oldestDataAgeSeconds?: number
  tradeAuthorized: boolean
  executionReady: boolean
}

/** 覆盖轮询状态（docs/llm-dev/04 §7.4） */
export interface CoverageStatus {
  enabled: boolean
  scope: string
  contractCount: number
  rotationCycleMinutes: number
  lastCycleCompletedAt?: string
  worstContractAgeSeconds?: number
  weightUsedPerMinute?: number
  weightBudgetPerMinute?: number
  cycleProgressPercent?: number
  // ── W7-07 运行面扩展（LLMGetCoverageStatus 实测值）─────────────────────
  visitedThisCycle?: number
  cyclesCompleted?: number
  failures?: number
  reloadsThisHour?: number
  reloadMarkets?: number
  lastTickerRefresh?: string
  lastReload?: string
  incrementSpacingMs?: number
  checkedAt?: string
}

/** Provider 档案的一个不可变修订版（docs/llm-dev/05 §2.2；M1-14 起的真后端） */
export interface LlmProviderRevision {
  id: string
  profileId: string
  revisionNo: number
  kind: string
  baseUrl: string
  modelId: string
  /** apiKey 只在请求里出现；响应仅回派生指纹，明文 key 从不经过前端 */
  apiKeyRef: string
  /** SHA-256 截断指纹：界面只显示指纹，可核对不泄密 */
  apiKeyFingerprint: string
  timeoutSeconds: number
  maxOutputTokens?: number
  maxToolRounds?: number
  maxToolCalls?: number
  maxCumulativeTokens?: number
  promptVersion?: string
  questionTemplateVersion?: string
  /** 空 = 未设；"0" = 禁止花钱；正数 = 计划值（非硬闸） */
  dailyBudgetUsd?: string
  reservationCap24h?: number
  allowInsecureLocalhost?: boolean
  proxyAddress?: string
  createdAt: string
  createdBy: string
  /** 非空表示已被更新版取代（修订版不可变，只有这一列会变） */
  supersededAt?: string
}

/** Provider 档案（LLM_OPENAI_COMPAT 或 JEV），revision 为最新一版 */
export interface LlmProviderProfile {
  id: string
  kind: 'LLM_OPENAI_COMPAT' | 'JEV' | string
  displayName: string
  enabled: boolean
  createdAt: string
  createdBy: string
  archivedAt?: string
  revisionCount: number
  revision?: LlmProviderRevision
}

export interface LlmListProviderProfilesResponse {
  profiles?: LlmProviderProfile[]
}

/** 新建档案的响应（W4-02 补缺）：返回含 id 的档案行，随后追加首个修订版。 */
export interface LlmCreateProviderProfileResponse {
  profile?: LlmProviderProfile
}

/** 一层探针的判决（05 §3；探针失败是结果不是错误） */
export interface LlmProbeLayerResult {
  layer: string
  passed: boolean
  errorCode?: string
  errorMessage?: string
  latencyMs?: number
  inputTokens?: number
  outputTokens?: number
  responseModel?: string
}

export interface LlmTestProviderProfileResponse {
  revisionId?: string
  kind?: string
  results?: LlmProbeLayerResult[]
}

/** 活化配对（单行 CAS 记录；found=false 表示从未激活过——是状态不是错误） */
export interface LlmActivePair {
  found: boolean
  llmProfileRevisionId?: string
  jevProfileRevisionId?: string
  pairEpoch: number
  readiness?: 'READY' | 'INCOMPLETE' | 'DEGRADED' | string
  updatedAt?: string
  updatedBy?: string
}

/** 一次决策运行（docs/llm-dev 08 §3.5；列表视图的字段集） */
export interface LlmDecisionRun {
  id: string
  strategyVersionId?: string
  snapshotId?: string
  trigger?: string
  runMode?: string
  decision?: 'OPEN' | 'CLOSE' | 'HOLD' | 'REVIEW' | string
  reason?: string
  inputHash?: string
  llmModel?: string
  jevModel?: string
  createdAt?: string
  latencyMs?: number
  llmRoundCount?: number
  toolCallCount?: number
}

export interface LlmListDecisionRunsResponse {
  runs?: LlmDecisionRun[]
}

/** 一次风控评估的证据（docs/llm-dev 07 §5.5、08 §3.5；checks/rejections 为 JSON 字符串） */
export interface LlmRiskEvidence {
  riskEvaluationId: string
  checkVersion?: string
  verdict?: string
  checksJson?: string
  rejectionsJson?: string
  envelopeHash?: string
  computedQty?: string
  computedNotionalUsd?: string
  referencePrice?: string
  evaluatedAt?: string
}

/** 单次决策运行详情（含候选/Jev 原文/工具转录原文） */
export interface LlmGetDecisionRunResponse {
  run?: LlmDecisionRun
  candidateJson?: string
  jevJson?: string
  toolTranscriptJson?: string
}

/** 决策血缘（run + 风控评估 + 关联审计事件） */
export interface LlmGetDecisionLineageResponse {
  run?: LlmDecisionRun
  riskEvaluations?: LlmRiskEvidence[]
  auditEvents?: LlmAuditEvent[]
}

/** §5.6 风控开关目录行（live 状态 + 红线锁 + 拒绝码；W7-05） */
export interface LlmRiskSwitch {
  name: string
  enabled: boolean
  defaultOn?: boolean
  locked?: boolean
  reasonCode?: string
}

export interface LlmGetRiskSwitchesResponse {
  switches?: LlmRiskSwitch[]
}

/** 审计事件（docs/llm-dev 08 §3.5；append-only，不删除不改写） */
export interface LlmAuditEvent {
  id: string
  kind?: string
  actor?: string
  subject?: string
  detailJson?: string
  createdAt?: string
}

export interface LlmListAuditEventsResponse {
  events?: LlmAuditEvent[]
}

// ── 策略工作区（W5；docs/llm-dev 07 §1、08 §3.2） ─────────────────────────

/** 策略行（状态是「版本的状态」；此行为骨架） */
export interface LlmStrategy {
  id: string
  name: string
  createdAt?: string
  createdBy?: string
  archived?: boolean
}

/** 策略版本行（state=生命周期；proposalState=提案状态，服务端从提案文档推导） */
export interface LlmStrategyVersion {
  id: string
  strategyId: string
  parentVersionId?: string
  state: string
  proposalState?: string
  strategyHash?: string
  riskEnvelopeHash?: string
  createdAt?: string
  frozenAt?: string
}

export interface LlmListStrategiesResponse { strategies?: LlmStrategy[] }
export interface LlmListStrategyVersionsResponse { versions?: LlmStrategyVersion[] }
export interface LlmCreateStrategyResponse { strategy?: LlmStrategy }
export interface LlmSaveStrategyDraftResponse { version?: LlmStrategyVersion }

/** 风控信封（candidate.RiskEnvelope 的视图；金额是十进制字符串，null=未设） */
export interface LlmRiskEnvelope {
  maxNewRiskIntents?: number
  riskWindowMinutes?: number
  maxOrderNotionalUsd?: string | null
  maxTotalExposureUsd?: string | null
  maxDailyLossUsd?: string | null
  maxSlippageBps?: number | null
  maxLeverage?: number | null
  maxConcurrentPositions?: number
  requiresProtectiveExit?: boolean
}

/** 条款提案（提取输出；candidate.PolicyProposal 的视图） */
export interface LlmPolicyProposal {
  objective?: string
  marketScopes?: Array<{ marketId: string; marketType?: string; actions?: string[] }>
  validMinutes?: number | null
  risk?: LlmRiskEnvelope
  exitPolicy?: string | null
  clarifyingQuestions?: string[]
}

/** 提取响应（LLMExtractPolicy；澄清问题=缺硬边界的交互面） */
export interface LlmExtractPolicyResponse {
  proposalJson?: string
  state?: string
  clarifyingQuestions?: string[]
  inputTokens?: number
  outputTokens?: number
  latencyMs?: number
}

/** 冻结响应（reviewable-v1 的两个哈希落库） */
export interface LlmFreezeStrategyVersionResponse {
  strategyHash?: string
  riskEnvelopeHash?: string
  frozenJson?: string
  frozenAt?: string
}

/** 审批行（W5-06 倒计时/撤销的数据面） */
export interface LlmStrategyApproval {
  id: string
  strategyVersionId?: string
  accountId?: string
  network?: string
  executionMode?: string
  approvedMarketIds?: string[]
  riskEnvelopeHash?: string
  pairEpoch?: number
  validFrom?: string
  validUntil?: string
  approvedBy?: string
  approvedAt?: string
  revokedAt?: string
  revokedBy?: string
  revokeReason?: string
  expiredAt?: string
}

export interface LlmGetStrategyApprovalResponse {
  found: boolean
  approval?: LlmStrategyApproval
}

export interface LlmApproveStrategyVersionResponse {
  approvalId?: string
  approvedAt?: string
  validUntil?: string
  idempotentReplay?: boolean
}

/** 版本 diff 一行（字段级；DiffPolicies 输出） */
export interface LlmPolicyChange {
  field: string
  from?: string
  to?: string
}

export interface LlmDiffStrategyVersionsResponse {
  fromVersionId?: string
  toVersionId?: string
  changes?: LlmPolicyChange[]
}

/** 市场授权（W5-07；逐市场、≤30 天；撤销级联撤销含该市场的有效审批） */
export interface LlmMarketGrant {
  id: string
  accountId?: string
  network?: string
  marketId?: string
  grantedBy?: string
  grantedAt?: string
  validUntil?: string
  revokedAt?: string
  revokedBy?: string
  revokeReason?: string
}

export interface LlmListMarketGrantsResponse {
  grants?: LlmMarketGrant[]
}

export interface LlmGrantMarketResponse {
  grant?: LlmMarketGrant
}

export interface LlmRevokeMarketGrantResponse {
  grantId?: string
  revokedApprovalIds?: string[]
}

/** 停机行（W7-01；三级 SOFT/MARKET/FULL） */
export interface LlmHaltRow {
  id: string
  level: 'SOFT' | 'MARKET' | 'FULL' | string
  marketId?: string
  reason?: string
  setBy?: string
  setAt?: string
  affectedStrategies?: number
  canceledOrders?: number
}

export interface LlmListHaltsResponse {
  halts?: LlmHaltRow[]
}

export interface LlmSetHaltResponse {
  halt?: LlmHaltRow
  suspendedStrategies?: number
  canceledOrders?: number
  flattenedPositions?: number
}

/** 对账差异行（W7-02；两侧读数都保留） */
export interface LlmPositionDelta {
  marketId: string
  side: string
  local: string
  exchange: string
}

export interface LlmReconcileResponse {
  reconciliationId?: string
  reconciled: boolean
  deltas?: LlmPositionDelta[]
  haltId?: string
}

/** 用量行（W7-03；按 provider kind × purpose 汇总——自估成本刻意缺席：库中无价目源） */
export interface LlmUsageLine {
  kind: string
  purpose: string
  reservations: number
  inputTokens: number
  outputTokens: number
  avgLatencyMs?: number
}

export interface LlmGetUsageResponse {
  windowHours: number
  lines?: LlmUsageLine[]
  totalInputTokens?: number
  totalOutputTokens?: number
  totalReservations?: number
  checkedAt?: string
}

/** 通知通道就绪度（W7-06；凭据只报布尔，从不回显） */
export interface LlmNotifyChannel {
  name: string
  enabled: boolean
  credentialSet: boolean
  targets: number
  endpointHost?: string
}

export interface LlmGetNotifyStatusResponse {
  channels?: LlmNotifyChannel[]
  relayRunning: boolean
  checkedAt?: string
}

export interface LlmTestNotifyResponse {
  relayRunning: boolean
  eventType?: string
}

// ── 筛选与批量决策工作台（docs/llm-dev/13）────────────────────────────────────

/** 筛选指标目录行（GUI 表单与后端注册表同源，不硬编码） */
export interface LlmScreenerMetric {
  name: string
  kind: 'NUMERIC' | 'CATEGORY' | string
  tier: string
  unit?: string
  description: string
  categories?: string[]
}

export interface LlmGetScreenerMetricsResponse {
  metrics?: LlmScreenerMetric[]
  maxLimit: number
  defaultLimit: number
}

export interface LlmScreenCriteria {
  metric: string
  op: string
  value?: number
  value2?: number
  state?: string
}

export interface LlmScreenSort {
  metric: string
  desc?: boolean
}

export interface LlmScreenRow {
  marketId: string
  numbersJson?: string
  statesJson?: string
}

export interface LlmRunScreenResponse {
  rows?: LlmScreenRow[]
  universeSize: number
  matched: number
  filteredOut: number
  excludedForMissing: number
  notes?: string[]
}

/** 批量决策作业（docs 13 §3.2；只产决策记录，绝不下单） */
export interface LlmBatchDecisionRow {
  marketId: string
  status: 'PENDING' | 'RUNNING' | 'DONE' | 'SKIPPED' | 'FAILED' | string
  decision?: string
  reason?: string
  decisionRunId?: string
  error?: string
}

export interface LlmStartBatchDecisionResponse {
  jobId: string
}

export interface LlmGetBatchDecisionResponse {
  jobId: string
  kind: string
  strategyVersionId: string
  actor: string
  createdAt: string
  done: boolean
  total: number
  finished: number
  rows?: LlmBatchDecisionRow[]
}

/** 活跃审批列表（A5：批量工作台默认绑定版本） */
export interface LlmListActiveApprovalsResponse {
  approvals?: LlmStrategyApproval[]
}
