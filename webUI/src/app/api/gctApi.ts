/**
 * RTK Query 客户端。
 *
 * 关键约定（实测得出，见 docs/localDeploy/README.md「实测验证记录」）：
 *   - 网关字段是 camelCase，`pair` 必须是对象
 *   - POST: getticker / getorderbook / getexchangepairs；其余为 GET
 *   - gethistoriccandles 的 start/end 要 `2006-01-02 15:04:05 MST`（例 "2026-10-01 00:00:00 UTC"）
 *   - gettechnicalanalysis 的 start/end 要 RFC3339（例 "2026-10-01T00:00:00Z"）—— 两者不同！
 *   - 自动候选窗口 60s（Q9 定案）
 */

import { createApi, fetchBaseQuery, type BaseQueryFn, type FetchArgs, type FetchBaseQueryError } from '@reduxjs/toolkit/query/react'
import type {
  AccountBalances, AlgorithmType, AssetType, Candle, CollateralModeInfo, CoverageStatus, CurrencyPair,
  EngineInfo, ExchangeAssets, ExchangePairs, FundingRateSnapshot, KlineItem, LeverageInfo,
  LlmActivePair, LlmApproveStrategyVersionResponse, LlmAuditEvent, LlmCreateProviderProfileResponse, LlmCreateStrategyResponse, LlmDecisionRun, LlmDiffStrategyVersionsResponse, LlmExtractPolicyResponse, LlmFreezeStrategyVersionResponse, LlmGetDecisionLineageResponse, LlmGetDecisionRunResponse, LlmGetRiskSwitchesResponse, LlmGetNotifyStatusResponse, LlmGetStrategyApprovalResponse, LlmGetUsageResponse, LlmGrantMarketResponse, LlmListAuditEventsResponse, LlmListDecisionRunsResponse, LlmListHaltsResponse, LlmListMarketGrantsResponse, LlmListProviderProfilesResponse, LlmListStrategiesResponse, LlmListStrategyVersionsResponse, LlmProbeLayerResult, LlmProviderProfile, LlmProviderRevision, LlmReconcileResponse, LlmRevokeMarketGrantResponse, LlmRiskEvidence, LlmRiskSwitch, LlmSaveStrategyDraftResponse, LlmSetHaltResponse, LlmStatus, LlmStrategyVersion, LlmTestNotifyResponse, LlmTestProviderProfileResponse,
  LlmBatchDecisionRow, LlmGetBatchDecisionResponse, LlmGetScreenerMetricsResponse, LlmListActiveApprovalsResponse, LlmRunScreenResponse, LlmScreenCriteria, LlmScreenRow, LlmScreenSort, LlmScreenerMetric, LlmStartBatchDecisionResponse,
  ManagedPosition, Orderbook, PositionStats, PositionSummary, TechnicalAnalysisResponse, Ticker,
} from './types'

export const EXCHANGE = 'Binance'
export const ASSET: AssetType = 'usdtmarginedfutures'
export const DELIMITER = '-'

export const toPair = (base: string, quote = 'USDT'): CurrencyPair => ({ delimiter: DELIMITER, base, quote })
/** gethistoriccandles 需要 `2006-01-02 15:04:05 MST` */
export const fmtCandleTime = (d: Date) => `${d.toISOString().slice(0, 19).replace('T', ' ')} UTC`
/** gettechnicalanalysis 需要 RFC3339 */
export const fmtRfc3339 = (d: Date) => `${d.toISOString().slice(0, 19)}Z`

/** 网关/代理返回的稳定错误体 */
export interface GatewayErrorBody {
  code?: string | number
  message?: string
  detail?: string
  hint?: string
}

export const isNotImplemented = (err: unknown): boolean =>
  (err as FetchBaseQueryError | undefined)?.status === 501

export const errorText = (err: unknown): string => {
  const e = err as FetchBaseQueryError | undefined
  if (!e) return '未知错误'
  const data = e.data as GatewayErrorBody | string | undefined
  if (typeof data === 'string') return data.slice(0, 300)
  if (data?.message) return data.message
  return `请求失败（status=${String(e.status)}）`
}

const rawBase = fetchBaseQuery({
  baseUrl: '/api/',
  // 代理已注入 Basic Auth；前端不持有任何凭据（安全红线）
  prepareHeaders: (h) => { h.set('Accept', 'application/json'); return h },
})

/** 统一把非 2xx 归一成 {status, data:{code,message}}，便于 UI 判定 NOT_IMPLEMENTED / READ_ONLY */
const baseQuery: BaseQueryFn<string | FetchArgs, unknown, FetchBaseQueryError> = async (args, api, extra) => {
  const res = await rawBase(args, api, extra)
  if (res.error) {
    if (res.error.status === 'FETCH_ERROR') {
      // FETCH_ERROR 的原始类型没有 data 字段；这里补一个可读错误体，供 UI 统一渲染
      ;(res.error as { data?: unknown }).data = {
        code: 'PROXY_UNREACHABLE',
        message: '本地代理不可达。请运行 `pnpm proxy`（或 `pnpm dev:all`）。',
      }
    }
  }
  return res
}

export const gctApi = createApi({
  reducerPath: 'gct',
  baseQuery,
  tagTypes: ['Info', 'Ticker', 'Orderbook', 'Kline', 'TA', 'Funding', 'Account', 'Position', 'Pairs', 'LlmStatus', 'LlmProviders', 'LlmDecisions', 'LlmRiskSwitches', 'LlmStrategies', 'LlmGrants', 'LlmScreener', 'LlmBatch'],
  endpoints: (b) => ({
    // ── 引擎 ──────────────────────────────────────────────────────────────
    getEngineInfo: b.query<EngineInfo, void>({
      query: () => ({ url: 'getinfo', method: 'GET' }),
      providesTags: ['Info'],
    }),

    // ── 筛选与批量决策工作台（docs/llm-dev/13）──────────────────────────────
    getLlmScreenerMetrics: b.query<LlmGetScreenerMetricsResponse, void>({
      query: () => ({ url: 'llm/screener/metrics', method: 'GET' }),
      providesTags: ['LlmScreener'],
    }),

    runLlmScreen: b.mutation<LlmRunScreenResponse, { criteria: LlmScreenCriteria[]; sort?: LlmScreenSort[]; limit?: number; include?: string[] }>({
      query: (body) => ({ url: 'llm/screener/run', method: 'POST', body }),
    }),

    startLlmBatchDecision: b.mutation<LlmStartBatchDecisionResponse, { strategyVersionId: string; marketIds: string[]; kind: 'OPEN_DECISION' | 'POSITION_REVIEW'; actor: string }>({
      query: (body) => ({ url: 'llm/batchdecisions', method: 'POST', body }),
      invalidatesTags: ['LlmBatch'],
    }),

    getLlmActiveApprovals: b.query<LlmListActiveApprovalsResponse, void>({
      query: () => ({ url: 'llm/approvals/active', method: 'GET' }),
      providesTags: ['LlmStrategies'],
    }),

    getLlmBatchDecision: b.query<LlmGetBatchDecisionResponse, { jobId: string }>({
      query: ({ jobId }) => ({ url: `llm/batchdecisions/${jobId}`, method: 'GET' }),
      providesTags: ['LlmBatch'],
    }),

    // ── 市场数据 ──────────────────────────────────────────────────────────
    getTicker: b.query<Ticker, { base: string; quote?: string }>({
      query: ({ base, quote = 'USDT' }) => ({
        url: 'getticker', method: 'POST',
        body: { exchange: EXCHANGE, pair: toPair(base, quote), assetType: ASSET },
      }),
      providesTags: ['Ticker'],
    }),

    getOrderbook: b.query<Orderbook, { base: string; quote?: string }>({
      query: ({ base, quote = 'USDT' }) => ({
        url: 'getorderbook', method: 'POST',
        body: { exchange: EXCHANGE, pair: toPair(base, quote), assetType: ASSET },
      }),
      providesTags: ['Orderbook'],
    }),

    getKlines: b.query<KlineItem, { base: string; quote?: string; intervalSeconds: number; bars: number }>({
      query: ({ base, quote = 'USDT', intervalSeconds, bars }) => {
        const end = new Date()
        const start = new Date(end.getTime() - (bars + 40) * intervalSeconds * 1000)
        return {
          url: 'gethistoriccandles',
          method: 'GET',
          params: {
            exchange: EXCHANGE,
            'pair.delimiter': DELIMITER, 'pair.base': base, 'pair.quote': quote,
            assetType: ASSET,
            timeInterval: intervalSeconds * 1e9,
            exRequest: true,
            start: fmtCandleTime(start),
            end: fmtCandleTime(end),
          },
        }
      },
      providesTags: ['Kline'],
    }),

    getTechnicalAnalysis: b.query<
      TechnicalAnalysisResponse,
      { base: string; quote?: string; algorithm: AlgorithmType; intervalSeconds: number; bars: number; period?: number; fast?: number; slow?: number; stdUp?: number; stdDown?: number }
    >({
      // ⚠ BBANDS 实测**必须同时传 standardDeviationUp 与 standardDeviationDown**，
      //    缺任一个都会 400（invalid deviation multiplier upper/lower limit）
      query: ({ base, quote = 'USDT', algorithm, intervalSeconds, bars, period, fast, slow, stdUp, stdDown }) => {
        const end = new Date()
        const start = new Date(end.getTime() - (bars + 40) * intervalSeconds * 1000)
        return {
          url: 'gettechnicalanalysis',
          method: 'GET',
          params: {
            exchange: EXCHANGE,
            'pair.delimiter': DELIMITER, 'pair.base': base, 'pair.quote': quote,
            assetType: ASSET,
            algorithmType: algorithm,
            interval: intervalSeconds * 1e9,
            start: fmtRfc3339(start),
            end: fmtRfc3339(end),
            ...(period ? { period } : {}),
            ...(fast ? { fastPeriod: fast } : {}),
            ...(slow ? { slowPeriod: slow } : {}),
            ...(stdUp != null ? { standardDeviationUp: stdUp, standardDeviationDown: stdDown ?? stdUp } : {}),
          },
        }
      },
      providesTags: ['TA'],
    }),

    // ⚠ 不要传 includePredicted=true —— 实测会让该端点返回 500
    getLatestFundingRate: b.query<FundingRateSnapshot, { base: string; quote?: string }>({
      query: ({ base, quote = 'USDT' }) => ({
        url: 'getlatestfundingrate', method: 'GET',
        params: { exchange: EXCHANGE, asset: ASSET, 'pair.delimiter': DELIMITER, 'pair.base': base, 'pair.quote': quote },
      }),
      providesTags: ['Funding'],
    }),

    // ── 账户与仓位 ────────────────────────────────────────────────────────
    getAccountBalances: b.query<AccountBalances, void>({
      query: () => ({ url: 'getaccountbalances', method: 'GET', params: { exchange: EXCHANGE, assetType: ASSET } }),
      providesTags: ['Account'],
    }),

    getPositionSummary: b.query<PositionSummary, { base: string; quote?: string }>({
      query: ({ base, quote = 'USDT' }) => ({
        url: 'getfuturespositionssummary', method: 'GET',
        params: { exchange: EXCHANGE, asset: ASSET, 'pair.delimiter': DELIMITER, 'pair.base': base, 'pair.quote': quote },
      }),
      providesTags: ['Position'],
    }),

    getLeverage: b.query<LeverageInfo, { base: string; quote?: string }>({
      query: ({ base, quote = 'USDT' }) => ({
        url: 'getleverage', method: 'GET',
        params: { exchange: EXCHANGE, asset: ASSET, 'pair.delimiter': DELIMITER, 'pair.base': base, 'pair.quote': quote },
      }),
      providesTags: ['Position'],
    }),

    getCollateralMode: b.query<CollateralModeInfo, void>({
      query: () => ({ url: 'getcollateralmode', method: 'GET', params: { exchange: EXCHANGE, asset: ASSET } }),
      providesTags: ['Position'],
    }),

    /** 无仓位时上游返回 500 `no positions found` —— 这是正常态，UI 需按「无仓位」处理 */
    getManagedPositions: b.query<{ positions?: ManagedPosition[] }, void>({
      query: () => ({ url: 'getallmanagedpositions', method: 'GET', params: { includeFullOrderData: false } }),
      providesTags: ['Position'],
    }),

    // ── 交易对 ────────────────────────────────────────────────────────────
    /** ⚠ 实测是 POST（不是 GET） */
    getExchangePairs: b.query<ExchangePairs, void>({
      query: () => ({ url: 'getexchangepairs', method: 'POST', body: { exchange: EXCHANGE, asset: ASSET } }),
      providesTags: ['Pairs'],
    }),

    getExchangeAssets: b.query<ExchangeAssets, void>({
      query: () => ({ url: 'getexchangeassets', method: 'GET', params: { exchange: EXCHANGE } }),
      providesTags: ['Pairs'],
    }),

    // ── LLM 规划端点（后端未实现时返回 501，UI 走降级展示） ────────────────
    getLlmStatus: b.query<LlmStatus, void>({
      query: () => ({ url: 'llm/status', method: 'GET' }),
      providesTags: ['LlmStatus'],
    }),

    getCoverageStatus: b.query<CoverageStatus, void>({
      query: () => ({ url: 'llm/coverage', method: 'GET' }),
      providesTags: ['LlmStatus'],
    }),

    // ── Provider 档案与活化配对（M1-14 后端已落地） ───────────────────────
    getLlmProviders: b.query<LlmListProviderProfilesResponse, { includeArchived?: boolean } | void>({
      query: (arg) => ({
        url: 'llm/providers', method: 'GET',
        params: arg?.includeArchived ? { includeArchived: true } : undefined,
      }),
      providesTags: ['LlmProviders'],
    }),

    getActiveModelPair: b.query<LlmActivePair, void>({
      query: () => ({ url: 'llm/activepair', method: 'GET' }),
      providesTags: ['LlmProviders'],
    }),

    // ── 决策与审计（M4 后端已落地） ────────────────────────────────────────
    getLlmDecisionRuns: b.query<LlmListDecisionRunsResponse, { strategyVersionId?: string; limit?: number } | void>({
      query: (arg) => ({
        url: 'llm/decisions', method: 'GET',
        params: {
          ...(arg?.strategyVersionId ? { strategyVersionId: arg.strategyVersionId } : {}),
          ...(arg?.limit ? { limit: arg.limit } : {}),
        },
      }),
      providesTags: ['LlmDecisions'],
    }),

    getLlmAuditEvents: b.query<LlmListAuditEventsResponse, { kind?: string; limit?: number } | void>({
      query: (arg) => ({
        url: 'llm/audit', method: 'GET',
        params: {
          ...(arg?.kind ? { kind: arg.kind } : {}),
          ...(arg?.limit ? { limit: arg.limit } : {}),
        },
      }),
      providesTags: ['LlmDecisions'],
    }),

    // 血缘（W6-02）：运行详情 + 风控证据 + 关联审计。读取只在展开时发生。
    getLlmDecisionRun: b.query<LlmGetDecisionRunResponse, { id: string }>({
      query: ({ id }) => ({ url: `llm/decisions/${id}`, method: 'GET' }),
      providesTags: ['LlmDecisions'],
    }),

    getLlmDecisionLineage: b.query<LlmGetDecisionLineageResponse, { id: string }>({
      query: ({ id }) => ({ url: `llm/decisions/${id}/lineage`, method: 'GET' }),
      providesTags: ['LlmDecisions'],
    }),

    // 手动触发与预览（W6-04）：都是写路径（代理 GCT_ALLOW_WRITES 闸门）。
    // preview 语义上只产出 HOLD/REVIEW；trigger 走完整门禁（含 pre-gate）。
    triggerDecisionAnalysis: b.mutation<LlmDecisionRun, { strategyVersionId: string; marketId: string; actor: string }>({
      query: (body) => ({ url: 'llm/decisions/trigger', method: 'POST', body }),
      invalidatesTags: ['LlmDecisions'],
    }),

    previewDecision: b.mutation<LlmDecisionRun, { strategyVersionId: string; marketId: string; actor: string }>({
      query: (body) => ({ url: 'llm/decisions/preview', method: 'POST', body }),
      invalidatesTags: ['LlmDecisions'],
    }),

    // 风控开关（W7-05）：读 live 状态；写受代理 GCT_ALLOW_WRITES 闸门，
    // 锁定项由后端拒绝（前端同时禁用开关）。
    getLlmRiskSwitches: b.query<LlmGetRiskSwitchesResponse, void>({
      query: () => ({ url: 'llm/riskswitches', method: 'GET' }),
      providesTags: ['LlmRiskSwitches'],
    }),

    setLlmRiskSwitch: b.mutation<{ status: string }, { name: string; enabled: boolean; actor: string }>({
      query: (body) => ({ url: 'llm/riskswitch', method: 'POST', body }),
      invalidatesTags: ['LlmRiskSwitches'],
    }),

    // 运行模式切换（08 §3.6 · LLMSetRunMode）：治理写，受代理 GCT_ALLOW_WRITES 闸门；
    // 重新认证与（LIVE 的）承认短语由后端强制——前端只负责收集与提示。
    setLlmRunMode: b.mutation<{ status: string }, { runMode: string; password: string; actor: string; acknowledged?: string }>({
      query: (body) => ({ url: 'llm/runmode', method: 'POST', body }),
      invalidatesTags: ['LlmStatus'],
    }),

    // ── 策略工作区（W5；后端策略 RPC 已落地） ──────────────────────────
    // 读路径直连仓储（列表/版本/审批），写路径经内部流水线同一服务。
    getLlmStrategies: b.query<LlmListStrategiesResponse, { includeArchived?: boolean } | void>({
      query: (arg) => ({
        url: 'llm/strategies', method: 'GET',
        params: arg?.includeArchived ? { includeArchived: true } : undefined,
      }),
      providesTags: ['LlmStrategies'],
    }),

    createLlmStrategy: b.mutation<LlmCreateStrategyResponse, { name: string; actor: string }>({
      query: (body) => ({ url: 'llm/strategies', method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    listLlmStrategyVersions: b.query<LlmListStrategyVersionsResponse, string>({
      query: (strategyId) => ({ url: `llm/strategies/${strategyId}/versions`, method: 'GET' }),
      providesTags: ['LlmStrategies'],
    }),

    getLlmStrategyVersion: b.query<{ version?: LlmStrategyVersion; tree?: LlmStrategyVersion[]; policyJson?: string }, string>({
      query: (id) => ({ url: `llm/strategyversions/${id}`, method: 'GET' }),
      providesTags: ['LlmStrategies'],
    }),

    /** 条款提取：从一段对话产出候选条款（不持久化；保存为草案是显式动作） */
    extractLlmPolicy: b.mutation<LlmExtractPolicyResponse, { conversationId: string; profileRevisionId: string; markets: string[] }>({
      query: (body) => ({ url: 'llm/extractpolicy', method: 'POST', body }),
    }),

    saveLlmStrategyDraft: b.mutation<LlmSaveStrategyDraftResponse, { strategyId: string; parentVersionId?: string; proposalJson: string; actor: string }>({
      query: ({ strategyId, ...body }) => ({ url: `llm/strategies/${strategyId}/drafts`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    freezeLlmStrategyVersion: b.mutation<LlmFreezeStrategyVersionResponse, { id: string; actor: string; policyJson: string }>({
      query: ({ id, ...body }) => ({ url: `llm/strategyversions/${id}/freeze`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    diffLlmStrategyVersions: b.query<LlmDiffStrategyVersionsResponse, { fromId: string; toId: string }>({
      query: ({ fromId, toId }) => ({ url: 'llm/strategyversions/diff', method: 'GET', params: { fromId, toId } }),
    }),

    getLlmStrategyApproval: b.query<LlmGetStrategyApprovalResponse, string>({
      query: (strategyVersionId) => ({ url: `llm/strategyversions/${strategyVersionId}/approval`, method: 'GET' }),
      providesTags: ['LlmStrategies'],
    }),

    approveLlmStrategyVersion: b.mutation<LlmApproveStrategyVersionResponse, {
      strategyVersionId: string
      accountId: string
      network: string
      executionMode: string
      approvedMarketIds: string[]
      validMinutes: number
      operatorPassword: string
      idempotencyKey: string
      expectedPairEpoch: number
      actor: string
    }>({
      query: ({ strategyVersionId, ...body }) => ({ url: `llm/strategyversions/${strategyVersionId}/approve`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    revokeLlmApproval: b.mutation<{ status: string }, { id: string; actor: string; reason?: string }>({
      query: ({ id, ...body }) => ({ url: `llm/approvals/${id}/revoke`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    pauseLlmStrategy: b.mutation<{ status: string }, { strategyVersionId: string; actor: string; reason?: string }>({
      query: ({ strategyVersionId, ...body }) => ({ url: `llm/strategyversions/${strategyVersionId}/pause`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    resumeLlmStrategy: b.mutation<{ status: string }, { strategyVersionId: string; actor: string; reason?: string }>({
      query: ({ strategyVersionId, ...body }) => ({ url: `llm/strategyversions/${strategyVersionId}/resume`, method: 'POST', body }),
      invalidatesTags: ['LlmStrategies'],
    }),

    // 市场授权（W5-07；第二道闸：看盘 ≠ 能交易）。撤销级联撤销含该市场的有效审批。
    getLlmMarketGrants: b.query<LlmListMarketGrantsResponse, string>({
      query: (accountId) => ({ url: 'llm/marketgrants', method: 'GET', params: { accountId } }),
      providesTags: ['LlmGrants'],
    }),

    grantLlmMarket: b.mutation<LlmGrantMarketResponse, { accountId: string; network: string; marketId: string; validDays: number; grantedBy: string }>({
      query: (body) => ({ url: 'llm/marketgrants', method: 'POST', body }),
      invalidatesTags: ['LlmGrants'],
    }),

    revokeLlmMarketGrant: b.mutation<LlmRevokeMarketGrantResponse, { id: string; actor: string; reason?: string }>({
      query: ({ id, ...body }) => ({ url: `llm/marketgrants/${id}/revoke`, method: 'POST', body }),
      invalidatesTags: ['LlmGrants', 'LlmStrategies'],
    }),

    // 三级停机与显式清除（W7-01）。清除需重新认证，且不自动恢复任何东西。
    getLlmHalts: b.query<LlmListHaltsResponse, void>({
      query: () => ({ url: 'llm/halts', method: 'GET' }),
      providesTags: ['LlmStatus'],
    }),

    setLlmHalt: b.mutation<LlmSetHaltResponse, {
      level: string
      marketId?: string
      reason: string
      actor: string
      flatten?: boolean
      confirmFlatten?: boolean
    }>({
      query: (body) => ({ url: 'llm/halt', method: 'POST', body }),
      invalidatesTags: ['LlmStatus', 'LlmStrategies'],
    }),

    clearLlmHalt: b.mutation<{ status: string }, { id: string; actor: string; reason: string; operatorPassword: string }>({
      query: ({ id, ...body }) => ({ url: `llm/halt/${id}/clear`, method: 'POST', body }),
      invalidatesTags: ['LlmStatus', 'LlmStrategies'],
    }),

    // 对账手动触发（W7-02）：一轮「本地视图 vs 交易所实况」；差异 → delta + SOFT halt + 告警。
    reconcileLlmNow: b.mutation<LlmReconcileResponse, { actor: string }>({
      query: (body) => ({ url: 'llm/reconcile', method: 'POST', body }),
      invalidatesTags: ['LlmStatus'],
    }),

    // 用量读面（W7-03）：窗口内已结算（USED）预约的 token/延迟汇总。
    getLlmUsage: b.query<LlmGetUsageResponse, { windowHours?: number } | void>({
      query: (arg) => ({
        url: 'llm/usage', method: 'GET',
        params: arg?.windowHours ? { windowHours: arg.windowHours } : undefined,
      }),
      providesTags: ['LlmStatus'],
    }),

    // 通知通道（W7-06）：配置就绪度（凭据布尔不回显）+ operator 测试推送。
    getLlmNotifyStatus: b.query<LlmGetNotifyStatusResponse, void>({
      query: () => ({ url: 'llm/notify', method: 'GET' }),
      providesTags: ['LlmStatus'],
    }),

    testLlmNotify: b.mutation<LlmTestNotifyResponse, { actor: string }>({
      query: (body) => ({ url: 'llm/notify/test', method: 'POST', body }),
    }),

    // 新建档案（W4-02 补缺）：kind=LLM_OPENAI_COMPAT|JEV；档案创建后需追加一个
    // 修订版（updateLlmProviderProfile）才可用于配对。写操作受代理写闸约束。
    createLlmProviderProfile: b.mutation<LlmCreateProviderProfileResponse, { kind: string; displayName: string; actor: string }>({
      query: (body) => ({ url: 'llm/providers', method: 'POST', body }),
      invalidatesTags: ['LlmProviders'],
    }),

    // 追加新修订版（W4-02）：写操作，代理默认以 GCT_ALLOW_WRITES=false 拦截；
    // API Key 明文随表单提交（vault 已移除，2026-10-15）；响应只回派生指纹。
    updateLlmProviderProfile: b.mutation<{ revision: LlmProviderRevision }, {
      id: string
      actor: string
      baseUrl: string
      modelId: string
      apiKey: string
      timeoutSeconds: number
      maxOutputTokens?: number
      promptVersion?: string
      questionTemplateVersion?: string
      dailyBudgetUsd?: string
      reservationCap24h?: number
      allowInsecureLocalhost?: boolean
      proxyAddress?: string
    }>({
      query: ({ id, ...body }) => ({ url: `llm/providers/${id}`, method: 'POST', body: { id, ...body } }),
      // 追加修订版可能清空配对指针并降级 readiness（顶部状态卡/横幅读 llm/status）
      invalidatesTags: ['LlmProviders', 'LlmStatus'],
    }),

    // 分层探针（W4-03）：每次判决都会落库并影响 readiness，因此失效 provider 列表。
    // 探针失败=200 判决（不是 RPC 错误）；只有基础设施故障才 reject。
    testLlmProviderProfile: b.mutation<LlmTestProviderProfileResponse, { id: string; revisionId?: string }>({
      query: ({ id, revisionId }) => ({
        url: `llm/providers/${id}/test`, method: 'POST',
        body: revisionId ? { revisionId } : {},
      }),
      // 判决改变 readiness：顶部状态卡/横幅同样要刷新
      invalidatesTags: ['LlmProviders', 'LlmStatus'],
    }),

    // 合约覆盖启停（N11）：写路径与 gctcli pair enable 同源；成功后交易对缓存与
    // 覆盖状态卡都要重取。
    setLlmMarketTracked: b.mutation<{ marketId: string; tracked: boolean; status: string }, { marketId: string; tracked: boolean; actor: string }>({
      query: (body) => ({ url: 'llm/markettracked', method: 'POST', body }),
      invalidatesTags: ['Pairs', 'LlmStatus'],
    }),

    // 取消批量作业（I4）：待跑行立即 CANCELLED，在途市场通过上下文取消。
    cancelLlmBatchDecision: b.mutation<{ jobId: string; done: boolean; cancelled: number }, { jobId: string; actor: string }>({
      query: ({ jobId, actor }) => ({ url: `llm/batchdecisions/${jobId}/cancel`, method: 'POST', body: { actor } }),
    }),

    // 切换活化配对（W4-05）：三重闸——操作者口令（重认证）+ expectedPairEpoch（CAS）+
    // idempotencyKey（重试安全）。写操作，受代理 GCT_ALLOW_WRITES 闸门。
    switchActiveModelPair: b.mutation<{ pairEpoch: number; readiness?: string }, {
      llmProfileRevisionId: string
      jevProfileRevisionId: string
      expectedPairEpoch: number
      operatorPassword: string
      idempotencyKey: string
      actor: string
    }>({
      query: (body) => ({ url: 'llm/activepair', method: 'POST', body }),
      // 配对切换同时改写 llm/status（顶部状态卡/横幅）与配对行（LlmProviders）
      invalidatesTags: ['LlmProviders', 'LlmStatus'],
    }),
  }),
})

export const {
  useGetEngineInfoQuery,
  useGetTickerQuery,
  useGetOrderbookQuery,
  useGetKlinesQuery,
  useGetTechnicalAnalysisQuery,
  useGetLatestFundingRateQuery,
  useGetAccountBalancesQuery,
  useGetPositionSummaryQuery,
  useGetLeverageQuery,
  useGetCollateralModeQuery,
  useGetManagedPositionsQuery,
  useGetExchangePairsQuery,
  useGetExchangeAssetsQuery,
  useGetLlmStatusQuery,
  useGetCoverageStatusQuery,
  useGetLlmProvidersQuery,
  useGetActiveModelPairQuery,
  useGetLlmDecisionRunsQuery,
  useGetLlmAuditEventsQuery,
  useCreateLlmProviderProfileMutation,
  useUpdateLlmProviderProfileMutation,
  useTestLlmProviderProfileMutation,
  useSwitchActiveModelPairMutation,
  useSetLlmMarketTrackedMutation,
  useCancelLlmBatchDecisionMutation,
  useGetLlmDecisionRunQuery,
  useGetLlmDecisionLineageQuery,
  useTriggerDecisionAnalysisMutation,
  usePreviewDecisionMutation,
  useGetLlmRiskSwitchesQuery,
  useSetLlmRiskSwitchMutation,
  useSetLlmRunModeMutation,
  useGetLlmStrategiesQuery,
  useCreateLlmStrategyMutation,
  useListLlmStrategyVersionsQuery,
  useGetLlmStrategyVersionQuery,
  useExtractLlmPolicyMutation,
  useSaveLlmStrategyDraftMutation,
  useFreezeLlmStrategyVersionMutation,
  useLazyDiffLlmStrategyVersionsQuery,
  useGetLlmStrategyApprovalQuery,
  useApproveLlmStrategyVersionMutation,
  useRevokeLlmApprovalMutation,
  usePauseLlmStrategyMutation,
  useResumeLlmStrategyMutation,
  useGetLlmMarketGrantsQuery,
  useGrantLlmMarketMutation,
  useRevokeLlmMarketGrantMutation,
  useGetLlmHaltsQuery,
  useSetLlmHaltMutation,
  useClearLlmHaltMutation,
  useReconcileLlmNowMutation,
  useGetLlmUsageQuery,
  useGetLlmNotifyStatusQuery,
  useTestLlmNotifyMutation,
  useGetLlmScreenerMetricsQuery,
  useRunLlmScreenMutation,
  useStartLlmBatchDecisionMutation,
  useGetLlmBatchDecisionQuery,
  useGetLlmActiveApprovalsQuery,
} = gctApi

export type { Ticker, Orderbook, Candle, KlineItem, PositionSummary, PositionStats, FundingRateSnapshot, AccountBalances, CurrencyPair, LlmProviderProfile, LlmProviderRevision, LlmActivePair, LlmDecisionRun, LlmAuditEvent, LlmProbeLayerResult, LlmTestProviderProfileResponse, LlmRiskEvidence, LlmGetDecisionRunResponse, LlmGetDecisionLineageResponse, LlmRiskSwitch, LlmGetRiskSwitchesResponse, LlmScreenerMetric, LlmGetScreenerMetricsResponse, LlmScreenCriteria, LlmScreenSort, LlmScreenRow, LlmRunScreenResponse, LlmBatchDecisionRow, LlmGetBatchDecisionResponse, LlmStartBatchDecisionResponse, LlmListActiveApprovalsResponse, ManagedPosition }
