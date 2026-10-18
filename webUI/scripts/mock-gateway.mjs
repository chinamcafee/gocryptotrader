/**
 * 开发用 mock 网关（M3-09 浏览器实测）。
 *
 * 模拟 gRPC gateway 的 server-streaming 形态：`/v1/llm/chatstream` 用 chunked
 * 响应、以换行分隔的 JSON message，无 Content-Length——代理的流式直通与
 * 前端分帧解析器都按这个形态验收。仅供本地验证，不参与生产。
 *
 * 运行：node scripts/mock-gateway.mjs（证书路径用 MOCK_GW_CERT/MOCK_GW_KEY）
 */

import https from 'node:https'
import fs from 'node:fs'

const PORT = Number(process.env.MOCK_GW_PORT ?? 9443)
const CERT = process.env.MOCK_GW_CERT ?? '/tmp/mock-gw-cert.pem'
const KEY = process.env.MOCK_GW_KEY ?? '/tmp/mock-gw-key.pem'

const server = https.createServer({
  cert: fs.readFileSync(CERT),
  key: fs.readFileSync(KEY),
})

const conversations = new Map()
let messageSeq = 0
const messages = new Map()

// Provider 档案与活化配对（M1-14 端点的离线形状：字段名与网关 JSON 一致）
const providerProfiles = [
  {
    id: 'prof-llm-1', kind: 'LLM_OPENAI_COMPAT', displayName: 'DeepSeek 生产', enabled: true,
    createdAt: '2026-10-11T09:00:00Z', createdBy: 'op', revisionCount: 2,
    revision: {
      id: 'rev-llm-2', profileId: 'prof-llm-1', revisionNo: 2, kind: 'LLM_OPENAI_COMPAT',
      baseUrl: 'https://api.deepseek.com/v1', modelId: 'deepseek-chat',
      apiKeyFingerprint: 'abc123def4567890',
      timeoutSeconds: 30, maxOutputTokens: 2048, promptVersion: 'analysis-readonly-v1',
      dailyBudgetUsd: '5.00', reservationCap24h: 3,
      createdAt: '2026-10-11T09:10:00Z', createdBy: 'op',
    },
  },
  {
    id: 'prof-jev-1', kind: 'JEV', displayName: 'TypeSafe Prod', enabled: true,
    createdAt: '2026-10-11T09:01:00Z', createdBy: 'op', revisionCount: 1,
    revision: {
      id: 'rev-jev-1', profileId: 'prof-jev-1', revisionNo: 1, kind: 'JEV',
      baseUrl: 'https://api.typesafe.ai', modelId: 'jev-1.0.0',
      apiKeyFingerprint: 'fedcba0987654321',
      timeoutSeconds: 20, questionTemplateVersion: 'jev-review-v2', reservationCap24h: 100,
      createdAt: '2026-10-11T09:01:00Z', createdBy: 'op',
    },
  },
]
const activePair = {
  found: true, llmProfileRevisionId: 'rev-llm-2', jevProfileRevisionId: 'rev-jev-1',
  pairEpoch: 3, readiness: 'READY', updatedAt: '2026-10-11T09:15:00Z', updatedBy: 'op',
}

// 风控开关目录（W7-05；与 engine RiskSwitchCatalog 同 18 行、同锁）
const riskSwitches = [
  { name: 'haltOnDailyLoss', enabled: true, defaultOn: true, locked: true, reasonCode: 'RISK_DAILY_LOSS_EXCEEDED' },
  { name: 'dailyLossRecoveryManualOnly', enabled: true, defaultOn: true, locked: false },
  { name: 'requireProtectiveExit', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_MISSING_PROTECTIVE_EXIT' },
  { name: 'stopDistanceTighterOfAtrAndPct', enabled: true, defaultOn: true, locked: false },
  { name: 'blockOnUnknownOrders', enabled: true, defaultOn: true, locked: true, reasonCode: 'RISK_UNKNOWN_ORDER_UNRESOLVED' },
  { name: 'blockOnStaleReconciliation', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_ACCOUNT_NOT_RECONCILED' },
  { name: 'blockOnClockDrift', enabled: true, defaultOn: true, locked: true, reasonCode: 'RISK_CLOCK_DRIFT' },
  { name: 'blockOnOpposingPosition', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_OPPOSING_POSITION' },
  { name: 'blockOnWideSpread', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_SPREAD_TOO_WIDE' },
  { name: 'blockOnThinDepth', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_INSUFFICIENT_DEPTH' },
  { name: 'blockOnMarkIndexDeviation', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_INDEX_DEVIATION' },
  { name: 'blockOnConsecutiveLosses', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_CONSECUTIVE_LOSS_CIRCUIT' },
  { name: 'maxOrdersPerMinute', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_ALL_ORDER_BUDGET' },
  { name: 'liquidityTopNOnly', enabled: true, defaultOn: true, locked: false, reasonCode: 'RISK_MARKET_LOW_LIQUIDITY' },
  { name: 'blockOnExtremeFunding', enabled: false, defaultOn: false, locked: false, reasonCode: 'RISK_FUNDING_EXTREME' },
  { name: 'blockNearFundingSettlement', enabled: false, defaultOn: false, locked: false, reasonCode: 'RISK_FUNDING_SETTLEMENT_IMMINENT' },
  { name: 'tradingHoursOnly', enabled: false, defaultOn: false, locked: false, reasonCode: 'RISK_OUTSIDE_TRADING_HOURS' },
  { name: 'blockOnHighVolatility', enabled: false, defaultOn: false, locked: false, reasonCode: 'RISK_VOLATILITY_EXTREME' },
]

// 决策运行样本（W6-01 列表）
const decisionRuns = [
  {
    id: 'run-1', strategyVersionId: 'ver-7f3a2b1c-0001', snapshotId: 'snap-1',
    trigger: 'KLINE_CLOSE', runMode: 'SHADOW', decision: 'OPEN', reason: 'COMPOSED_BULLISH_ALIGNMENT',
    inputHash: 'd41d8cd98f00b204e9800998ecf8427e00000000000000000000000000000000',
    llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
    createdAt: '2026-10-11T09:20:00Z', latencyMs: 8420, llmRoundCount: 3, toolCallCount: 5,
  },
  {
    id: 'run-2', strategyVersionId: 'ver-7f3a2b1c-0001', snapshotId: 'snap-2',
    trigger: 'MACD_CROSS', runMode: 'SHADOW', decision: 'REVIEW', reason: 'JEV_LOW_CONFIDENCE',
    inputHash: '5d41402abc4b2a76b9719d911017c59200000000000000000000000000000000',
    llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
    createdAt: '2026-10-11T08:05:00Z', latencyMs: 9110, llmRoundCount: 2, toolCallCount: 3,
  },
  {
    id: 'run-3', strategyVersionId: 'ver-7f3a2b1c-0001', snapshotId: 'snap-3',
    trigger: 'MANUAL_PREVIEW', runMode: 'SHADOW', decision: 'HOLD', reason: 'RISK_ORDER_NOTIONAL_EXCEEDED',
    inputHash: '7d793037a0760186574b0282f2f435e700000000000000000000000000000000',
    llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
    createdAt: '2026-10-11T07:40:00Z', latencyMs: 6340, llmRoundCount: 1, toolCallCount: 2,
  },
  {
    // 超 12h 未处理的 REVIEW（W6-06 过期展示样本：自动过期 = 不执行）
    id: 'run-4', strategyVersionId: 'ver-7f3a2b1c-0001', snapshotId: 'snap-4',
    trigger: 'SCAN', runMode: 'SHADOW', decision: 'REVIEW', reason: 'JEV_REQUESTS_REVIEW',
    inputHash: '9a1b2c3d4e5f60718293a4b5c6d7e8f900000000000000000000000000000000',
    llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
    createdAt: '2026-10-10T20:00:00Z', latencyMs: 8100, llmRoundCount: 3, toolCallCount: 5,
  },
]

// 审计事件样本（W6-05 事件流）
const auditEvents = [
  { id: 'aud-1', kind: 'PAIR_SWITCH', actor: 'op', subject: 'pair#epoch3', detailJson: '{"fromEpoch":2,"toEpoch":3,"llm":"rev-llm-2","jev":"rev-jev-1"}', createdAt: '2026-10-11T09:15:00Z' },
  { id: 'aud-2', kind: 'APPROVAL_GRANTED', actor: 'op', subject: 'strategy-ver-7f3a2b1c-0001', detailJson: '{"validUntil":"2026-10-18T09:00:00Z"}', createdAt: '2026-10-11T09:02:00Z' },
  { id: 'aud-3', kind: 'RISK_SWITCH_SET', actor: 'op', subject: 'maxSlippageBps', detailJson: '{"enabled":true,"value":"20"}', createdAt: '2026-10-11T08:40:00Z' },
  { id: 'aud-4', kind: 'CRASH_RECOVERY', actor: 'engine', subject: 'intent-a', detailJson: '{"reconciliationRequired":true,"unknown":1}', createdAt: '2026-10-11T08:00:00Z' },
]

// ── 策略工作区（W5 离线形状）────────────────────────────────────────────
// 版本是追加式的：保存草案=新版本行；冻结把 reviewable-v1 文档+双哈希钉进行。
const strategies = [
  { id: 'strat-1', name: 'BTC 动量跟随', createdAt: '2026-10-11T08:30:00Z', createdBy: 'op', archived: false },
]

const frozenPolicySample = {
  schemaVersion: 'reviewable-v1', state: 'READY_FOR_APPROVAL', sourceVersionId: 'ver-7f3a2b1c-0001',
  accountId: 'binance-usdtm', network: 'binance-testnet', executionMode: 'SHADOW',
  marketScopes: [{ marketId: 'binance:usdtperpetual:BTC_USDT', marketType: 'PERP', actions: ['OPEN_LONG', 'REDUCE'] }],
  validFrom: '2026-10-11T09:00:00Z', validUntil: '2026-10-11T13:00:00Z',
  risk: {
    maxNewRiskIntents: 2, riskWindowMinutes: 300,
    maxOrderNotionalUsd: '20', maxTotalExposureUsd: '100', maxDailyLossUsd: '20',
    maxSlippageBps: 20, maxLeverage: 1, maxConcurrentPositions: 1, requiresProtectiveExit: true,
  },
  llmRevisionId: 'rev-llm-2', jevRevisionId: 'rev-jev-1', pairEpoch: 3, tradeAuthorized: false,
}

const draftProposalSample = {
  objective: '在 BTC 永续上跟随 MACD 金叉/死叉做相对低频的开平仓',
  marketScopes: [{ marketId: 'binance:usdtperpetual:BTC_USDT', marketType: 'PERP', actions: ['OPEN_LONG', 'REDUCE'] }],
  validMinutes: 240,
  risk: {
    maxNewRiskIntents: 2, riskWindowMinutes: 300,
    maxOrderNotionalUsd: '20', maxTotalExposureUsd: '100', maxDailyLossUsd: null,
    maxSlippageBps: 20, maxLeverage: 1, maxConcurrentPositions: 1, requiresProtectiveExit: true,
  },
  exitPolicy: null,
  clarifyingQuestions: ['日损上限应设为多少？（硬边界未给出，不能替你填值）'],
}

const strategyVersions = [
  {
    id: 'ver-7f3a2b1c-0001', strategyId: 'strat-1',
    state: 'FROZEN', proposalState: 'READY_FOR_REVIEW',
    strategyHash: 'aa11bb22cc33dd44ee55ff66aa77bb88cc99dd00ee11ff22aa33bb44cc55dd66',
    riskEnvelopeHash: '11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff',
    createdAt: '2026-10-11T08:35:00Z', frozenAt: '2026-10-11T08:40:00Z',
    proposalJson: JSON.stringify({ ...draftProposalSample, risk: { ...draftProposalSample.risk, maxDailyLossUsd: '20' }, exitPolicy: 'MACD 死叉即平', clarifyingQuestions: [] }),
    frozenJson: JSON.stringify(frozenPolicySample),
  },
  {
    id: 'ver-7f3a2b1c-0002', strategyId: 'strat-1', parentVersionId: 'ver-7f3a2b1c-0001',
    state: 'DRAFT', proposalState: 'NEEDS_CLARIFICATION',
    createdAt: '2026-10-11T09:20:00Z',
    proposalJson: JSON.stringify(draftProposalSample),
  },
]

const approvals = new Map([
  ['ver-7f3a2b1c-0001', {
    id: 'appr-1', strategyVersionId: 'ver-7f3a2b1c-0001', accountId: 'binance-usdtm',
    network: 'binance-testnet', executionMode: 'SHADOW',
    approvedMarketIds: ['binance:usdtperpetual:BTC_USDT'],
    riskEnvelopeHash: '11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff',
    pairEpoch: 3, validFrom: '2026-10-11T09:00:00Z', validUntil: '2026-10-11T13:00:00Z',
    approvedBy: 'op', approvedAt: '2026-10-11T09:02:00Z',
  }],
])

let versionSeq = 10
let approvalSeq = 1

// 停机（W7-01 离线形状：三级 + 显式清除；清除口令 s3cret，不自动恢复任何东西）
const halts = []
let haltSeq = 0
let llmRunMode = 'SHADOW'

// 市场授权（W5-07 离线形状：逐市场、≤30 天、活体唯一、撤销级联审批）
const marketGrants = [
  {
    id: 'grant-1', accountId: 'binance-usdtm', network: 'binance-testnet',
    marketId: 'binance:usdtperpetual:BTC_USDT', grantedBy: 'op',
    grantedAt: '2026-10-11T09:00:00Z', validUntil: '2026-10-18T09:00:00Z',
  },
]
let grantSeq = 1
let batchJobSeq = 0
const batchJobs = new Map()

// 期货持仓（老端点 getallmanagedpositions 的离线形状：真实网关为 protojson
// camelCase，方向是 order.Side 词汇表 BUY/SELL，数量为十进制字符串）
const managedPositions = [
  {
    exchange: 'Binance', asset: 'usdtmarginedfutures',
    pair: { delimiter: '_', base: 'BTC', quote: 'USDT' }, status: 'OPEN',
    openingDate: '2026-10-14T02:10:00Z', openingDirection: 'BUY', openingPrice: '61250.5', openingSize: '0.01',
    currentDirection: 'BUY', currentPrice: '61890.0', currentSize: '0.01',
    unrealisedPnl: '6.395', realisedPnl: '0', orderCount: '1',
  },
  {
    exchange: 'Binance', asset: 'usdtmarginedfutures',
    pair: { delimiter: '_', base: 'SOL', quote: 'USDT' }, status: 'OPEN',
    openingDate: '2026-10-14T05:40:00Z', openingDirection: 'SELL', openingPrice: '152.4', openingSize: '0.5',
    currentDirection: 'SELL', currentPrice: '149.8', currentSize: '0.5',
    unrealisedPnl: '1.3', realisedPnl: '0', orderCount: '1',
  },
]

const json = (res, status, body) => {
  res.writeHead(status, { 'content-type': 'application/json' })
  res.end(JSON.stringify(body))
}

server.on('request', (req, res) => {
  const method = req.method
  const path = new URL(req.url ?? '/', `https://127.0.0.1:${PORT}`).pathname
  const url = new URL(req.url ?? '/', `https://127.0.0.1:${PORT}`)

  // ── 策略工作区（W5）────────────────────────────────────────────────
  if (path === '/v1/llm/strategies' && method === 'GET') {
    const includeArchived = url.searchParams.get('includeArchived') === 'true'
    return json(res, 200, { strategies: strategies.filter((s) => includeArchived || !s.archived) })
  }
  if (path === '/v1/llm/strategies' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.name || !body.actor) return json(res, 400, { code: 3, message: 'name and actor are required' })
      const strategy = {
        id: `strat-${strategies.length + 1}`, name: body.name,
        createdAt: new Date().toISOString(), createdBy: body.actor, archived: false,
      }
      strategies.push(strategy)
      json(res, 200, { strategy })
    })
    return
  }
  if (path === '/v1/llm/extractpolicy' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.conversationId || !body.profileRevisionId) {
        return json(res, 400, { code: 3, message: 'conversationId and profileRevisionId are required' })
      }
      const markets = body.markets?.length ? body.markets : ['binance:usdtperpetual:BTC_USDT']
      // 故意缺一个硬边界（日损上限）→ NEEDS_CLARIFICATION + 澄清问题；不替用户填值
      const proposal = { ...draftProposalSample, marketScopes: markets.map((m) => ({ marketId: m, marketType: 'PERP', actions: ['OPEN_LONG', 'REDUCE'] })) }
      json(res, 200, {
        proposalJson: JSON.stringify(proposal), state: 'NEEDS_CLARIFICATION',
        clarifyingQuestions: proposal.clarifyingQuestions,
        inputTokens: 812, outputTokens: 366, latencyMs: 2140,
      })
    })
    return
  }
  const versionsMatch = path.match(/^\/v1\/llm\/strategies\/([^/]+)\/versions$/)
  if (versionsMatch && method === 'GET') {
    return json(res, 200, { versions: strategyVersions.filter((v) => v.strategyId === versionsMatch[1]) })
  }
  const draftMatch = path.match(/^\/v1\/llm\/strategies\/([^/]+)\/drafts$/)
  if (draftMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const strategy = strategies.find((s) => s.id === draftMatch[1])
      if (!strategy) return json(res, 404, { code: 5, message: 'strategy not found' })
      if (!body.proposalJson) return json(res, 400, { code: 3, message: 'proposalJson is required' })
      let parsed
      try { parsed = JSON.parse(body.proposalJson) } catch { return json(res, 400, { code: 3, message: 'proposal json is malformed' }) }
      const questions = Array.isArray(parsed.clarifyingQuestions) ? parsed.clarifyingQuestions : []
      const incomplete = questions.length > 0 || parsed.validMinutes == null || parsed.exitPolicy == null ||
        !parsed.risk || parsed.risk.maxDailyLossUsd == null || parsed.risk.maxOrderNotionalUsd == null
      versionSeq += 1
      const version = {
        id: `ver-7f3a2b1c-${String(versionSeq).padStart(4, '0')}`, strategyId: draftMatch[1],
        parentVersionId: body.parentVersionId || undefined,
        state: 'DRAFT', proposalState: incomplete ? 'NEEDS_CLARIFICATION' : 'READY_FOR_REVIEW',
        createdAt: new Date().toISOString(), proposalJson: body.proposalJson,
      }
      strategyVersions.push(version)
      json(res, 200, { version })
    })
    return
  }
  // 先于通用版本路由匹配（diff 也在 /strategyversions/ 前缀下）
  if (path === '/v1/llm/strategyversions/diff' && method === 'GET') {
    const from = strategyVersions.find((v) => v.id === url.searchParams.get('fromId'))
    const to = strategyVersions.find((v) => v.id === url.searchParams.get('toId'))
    if (!from || !to) return json(res, 404, { code: 5, message: 'version not found' })
    if (!from.frozenJson || !to.frozenJson) return json(res, 400, { code: 3, message: 'both versions must be frozen to diff' })
    const a = JSON.parse(from.frozenJson)
    const b = JSON.parse(to.frozenJson)
    const changes = []
    const cmp = (field, av, bv) => {
      const as = av == null ? '' : String(av)
      const bs = bv == null ? '' : String(bv)
      if (as !== bs) changes.push({ field, from: as, to: bs })
    }
    for (const f of ['state', 'accountId', 'network', 'executionMode', 'validFrom', 'validUntil', 'llmRevisionId', 'jevRevisionId', 'pairEpoch']) {
      cmp(f, a[f], b[f])
    }
    for (const f of ['maxOrderNotionalUsd', 'maxTotalExposureUsd', 'maxDailyLossUsd', 'maxSlippageBps', 'maxLeverage', 'maxNewRiskIntents', 'riskWindowMinutes', 'maxConcurrentPositions', 'requiresProtectiveExit']) {
      cmp(`risk.${f}`, a.risk?.[f], b.risk?.[f])
    }
    cmp('marketScopes', JSON.stringify(a.marketScopes), JSON.stringify(b.marketScopes))
    return json(res, 200, { fromVersionId: from.id, toVersionId: to.id, changes })
  }
  const freezeMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)\/freeze$/)
  if (freezeMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const version = strategyVersions.find((v) => v.id === freezeMatch[1])
      if (!version) return json(res, 404, { code: 5, message: 'version not found' })
      if (version.frozenJson) return json(res, 400, { code: 3, message: 'the version is already frozen' })
      if (version.proposalState !== 'READY_FOR_REVIEW') {
        return json(res, 400, { code: 3, message: 'the stored proposal is not ready for review' })
      }
      let policy
      try { policy = JSON.parse(body.policyJson) } catch { return json(res, 400, { code: 3, message: 'policy json is malformed' }) }
      if (policy.state !== 'READY_FOR_APPROVAL' || policy.tradeAuthorized !== false) {
        return json(res, 400, { code: 3, message: 'reviewable contract violated' })
      }
      const now = new Date().toISOString()
      const hex = () => Array.from({ length: 64 }, () => '0123456789abcdef'[Math.floor(Math.random() * 16)]).join('')
      version.frozenJson = body.policyJson
      version.state = 'FROZEN'
      version.frozenAt = now
      version.strategyHash = hex()
      version.riskEnvelopeHash = hex()
      return json(res, 200, { strategyHash: version.strategyHash, riskEnvelopeHash: version.riskEnvelopeHash, frozenJson: body.policyJson, frozenAt: now })
    })
    return
  }
  if (path === '/v1/llm/approvals/active' && method === 'GET') {
    const listed = [...approvals.values()]
    if (listed.length === 0) {
      listed.push({
        strategyVersionId: 'ver-mock-1', accountId: 'binance-usdtm-main', network: 'binance-testnet',
        executionMode: 'SHADOW', pairEpoch: 3, riskEnvelopeHash: 'env-hash',
        validFrom: '2026-10-01T00:00:00Z', validUntil: '2027-10-01T00:00:00Z',
        approvedMarketIds: JSON.stringify(['Binance:usdtmarginedfutures:BTC_USDT']),
      })
    }
    return json(res, 200, { approvals: listed })
  }
  const approvalGetMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)\/approval$/)
  if (approvalGetMatch && method === 'GET') {
    const approval = approvals.get(approvalGetMatch[1])
    return json(res, 200, approval ? { found: true, approval } : { found: false })
  }
  const approveMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)\/approve$/)
  if (approveMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const version = strategyVersions.find((v) => v.id === approveMatch[1])
      if (!version || !version.frozenJson) return json(res, 400, { code: 3, message: 'only frozen versions can be approved' })
      if (body.operatorPassword !== 's3cret') return json(res, 403, { code: 7, message: 'operator password rejected' })
      if (Number(body.expectedPairEpoch) !== activePair.pairEpoch) {
        return json(res, 409, { code: 6, message: `pair epoch conflict: expected ${body.expectedPairEpoch}, stored ${activePair.pairEpoch}` })
      }
      const validMinutes = Number(body.validMinutes || 240)
      if (validMinutes > 10080) return json(res, 400, { code: 3, message: 'window must be at most 7 days' })
      if (!Array.isArray(body.approvedMarketIds) || body.approvedMarketIds.length === 0) {
        return json(res, 400, { code: 3, message: 'approvedMarketIds must not be empty' })
      }
      const now = new Date()
      approvalSeq += 1
      const approval = {
        id: `appr-${approvalSeq}`, strategyVersionId: version.id,
        accountId: body.accountId, network: body.network, executionMode: body.executionMode,
        approvedMarketIds: body.approvedMarketIds, riskEnvelopeHash: version.riskEnvelopeHash,
        pairEpoch: activePair.pairEpoch,
        validFrom: now.toISOString(),
        validUntil: new Date(now.getTime() + validMinutes * 60000).toISOString(),
        approvedBy: body.actor || 'webui-operator', approvedAt: now.toISOString(),
      }
      approvals.set(version.id, approval)
      return json(res, 200, { approvalId: approval.id, approvedAt: approval.approvedAt, validUntil: approval.validUntil, idempotentReplay: false })
    })
    return
  }
  const revokeMatch = path.match(/^\/v1\/llm\/approvals\/([^/]+)\/revoke$/)
  if (revokeMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      let target
      for (const approval of approvals.values()) {
        if (approval.id === revokeMatch[1]) target = approval
      }
      if (!target) return json(res, 404, { code: 5, message: 'approval not found' })
      if (target.revokedAt) return json(res, 400, { code: 3, message: 'the approval is already revoked' })
      target.revokedAt = new Date().toISOString()
      target.revokedBy = body.actor || 'webui-operator'
      target.revokeReason = body.reason || 'operator revoke'
      return json(res, 200, { status: 'ok' })
    })
    return
  }
  const pauseMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)\/pause$/)
  if (pauseMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const version = strategyVersions.find((v) => v.id === pauseMatch[1])
      if (!version) return json(res, 404, { code: 5, message: 'version not found' })
      version.state = 'PAUSED'
      json(res, 200, { status: 'ok' })
    })
    return
  }
  const resumeMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)\/resume$/)
  if (resumeMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const version = strategyVersions.find((v) => v.id === resumeMatch[1])
      if (!version) return json(res, 404, { code: 5, message: 'version not found' })
      const approval = approvals.get(version.id)
      if (!approval || approval.revokedAt) {
        return json(res, 400, { code: 3, message: 'strategy version has no live approval' })
      }
      version.state = 'FROZEN'
      json(res, 200, { status: 'ok' })
    })
    return
  }
  // 市场授权（W5-07）：第二道闸；撤销级联撤销含该市场的有效审批
  if (path === '/v1/llm/marketgrants' && method === 'GET') {
    const accountId = url.searchParams.get('accountId')
    if (!accountId) return json(res, 400, { code: 3, message: 'accountId is required' })
    return json(res, 200, { grants: marketGrants.filter((g) => g.accountId === accountId) })
  }
  if (path === '/v1/llm/marketgrants' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.accountId || !body.network || !body.marketId || !body.grantedBy || !body.validDays) {
        return json(res, 400, { code: 3, message: 'accountId, network, marketId, validDays and grantedBy are required' })
      }
      if (body.validDays < 1 || body.validDays > 30) {
        return json(res, 400, { code: 3, message: 'grant window must be 1..30 days' })
      }
      const now = Date.now()
      const duplicate = marketGrants.find((g) => g.accountId === body.accountId && g.network === body.network &&
        g.marketId === body.marketId && !g.revokedAt && Date.parse(g.validUntil) > now)
      if (duplicate) return json(res, 409, { code: 6, message: 'an active grant already covers this market' })
      grantSeq += 1
      const grant = {
        id: `grant-${grantSeq + 1}`, accountId: body.accountId, network: body.network, marketId: body.marketId,
        grantedBy: body.grantedBy, grantedAt: new Date(now).toISOString(),
        validUntil: new Date(now + body.validDays * 86400000).toISOString(),
      }
      marketGrants.push(grant)
      json(res, 200, { grant })
    })
    return
  }
  const grantRevokeMatch = path.match(/^\/v1\/llm\/marketgrants\/([^/]+)\/revoke$/)
  if (grantRevokeMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const grant = marketGrants.find((g) => g.id === grantRevokeMatch[1])
      if (!grant) return json(res, 404, { code: 5, message: 'grant not found' })
      if (grant.revokedAt) return json(res, 400, { code: 3, message: 'the grant is already revoked' })
      grant.revokedAt = new Date().toISOString()
      grant.revokedBy = body.actor || 'webui-operator'
      grant.revokeReason = body.reason || 'operator revoke'
      const revokedApprovalIds = []
      for (const approval of approvals.values()) {
        if (approval.revokedAt) continue
        if (approval.accountId !== grant.accountId || approval.network !== grant.network) continue
        if (!(approval.approvedMarketIds ?? []).includes(grant.marketId)) continue
        if (approval.validUntil && Date.parse(approval.validUntil) <= Date.now()) continue
        approval.revokedAt = new Date().toISOString()
        approval.revokedBy = body.actor || 'webui-operator'
        approval.revokeReason = `market grant revoked: ${grant.marketId}`
        revokedApprovalIds.push(approval.id)
      }
      json(res, 200, { grantId: grant.id, revokedApprovalIds })
    })
    return
  }
  const versionMatch = path.match(/^\/v1\/llm\/strategyversions\/([^/]+)$/)
  if (versionMatch && method === 'GET') {
    const version = strategyVersions.find((v) => v.id === versionMatch[1])
    if (!version) return json(res, 404, { code: 5, message: 'version not found' })
    const { proposalJson, frozenJson, ...shape } = version
    return json(res, 200, {
      version: shape,
      tree: strategyVersions.filter((v) => v.strategyId === version.strategyId).map(({ proposalJson: _, frozenJson: __, ...rest }) => rest),
      policyJson: frozenJson ?? proposalJson ?? '',
    })
  }

  if (path === '/v1/llm/providers' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.kind || !body.displayName || !body.actor) {
        return json(res, 400, { code: 3, message: 'kind, displayName and actor are required' })
      }
      const profile = {
        id: `prof-mock-${providerProfiles.length + 1}`, kind: body.kind, displayName: body.displayName,
        enabled: true, createdAt: new Date().toISOString(), createdBy: body.actor, revisionCount: 0,
      }
      providerProfiles.push(profile)
      json(res, 200, { profile })
    })
    return
  }
  if (path === '/v1/llm/providers' && method === 'GET') {
    return json(res, 200, { profiles: providerProfiles })
  }
  if (path === '/v1/llm/status' && method === 'GET') {
    return json(res, 200, {
      runMode: llmRunMode, liveUnlocked: llmRunMode === 'LIVE',
      pairReadiness: 'READY', pairEpoch: activePair.pairEpoch,
      llmProfileName: 'DeepSeek 生产', llmModel: 'deepseek-chat',
      jevProfileName: 'TypeSafe Prod', jevModel: 'jev-1.0.0',
      llmProbeAgeSeconds: 42, jevProbeAgeSeconds: 61,
      halted: halts.length > 0,
      haltLevel: halts.length
        ? ['FULL', 'MARKET', 'SOFT'].find((lvl) => halts.some((h) => h.level === lvl)) ?? 'SOFT'
        : 'NONE',
      accountReconciled: true, reconciliationAgeSeconds: 120,
      reservationsRemaining24h: 2, approvedStrategies: 1,
      reviewExpireHours: 12,
      checkedAt: new Date().toISOString(),
    })
  }
  if (path === '/v1/llm/activepair' && method === 'GET') {
    return json(res, 200, activePair)
  }
  // 停机写侧（W7-01）：三级设置 + 重新认证清除；halts 驱动 status 的 halted/haltLevel
  if (path === '/v1/llm/halts' && method === 'GET') {
    return json(res, 200, { halts })
  }
  if (path === '/v1/llm/halt' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const level = String(body.level || '').toUpperCase()
      if (!['SOFT', 'MARKET', 'FULL'].includes(level)) return json(res, 400, { code: 3, message: 'level must be SOFT, MARKET or FULL' })
      if (!body.reason || !body.actor) return json(res, 400, { code: 3, message: 'reason and actor are required' })
      if (level === 'MARKET' && !body.marketId) return json(res, 400, { code: 3, message: 'a MARKET halt names its market' })
      if (body.flatten && !body.confirmFlatten) return json(res, 400, { code: 3, message: 'flattening positions requires explicit confirmation' })
      haltSeq += 1
      const halt = {
        id: `halt-${haltSeq}`, level, marketId: body.marketId || undefined,
        reason: body.reason, setBy: body.actor, setAt: new Date().toISOString(),
        affected_strategies: 1, canceled_orders: level === 'SOFT' ? 0 : 0,
        suspendedStrategies: 1,
      }
      halts.push(halt)
      json(res, 200, { halt: { id: halt.id, level: halt.level, marketId: halt.marketId, reason: halt.reason, setBy: halt.setBy, setAt: halt.setAt }, suspendedStrategies: 1, canceledOrders: 0, flattenedPositions: 0 })
    })
    return
  }
  const haltClearMatch = path.match(/^\/v1\/llm\/halt\/([^/]+)\/clear$/)
  if (haltClearMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (body.operatorPassword !== 's3cret') return json(res, 403, { code: 7, message: 'operator password rejected' })
      if (!body.reason || !body.actor) return json(res, 400, { code: 3, message: 'actor and reason are required' })
      const idx = halts.findIndex((h) => h.id === haltClearMatch[1])
      if (idx === -1) return json(res, 404, { code: 5, message: 'halt not found' })
      halts.splice(idx, 1)
      json(res, 200, { status: 'ok' })
    })
    return
  }
  // W7-06 通知通道（离线样本：凭据只报布尔、端点只报 host）
  // 运行模式切换（08 §3.6）：重新认证 + LIVE 承认短语；成功后 status 的模式随之变化
  if (path === '/v1/llm/runmode' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const mode = String(body.runMode || '').toUpperCase()
      if (!['READ_ONLY', 'SHADOW', 'PAPER', 'TESTNET', 'LIVE'].includes(mode)) return json(res, 400, { code: 3, message: 'runMode must be one of READ_ONLY/SHADOW/PAPER/TESTNET/LIVE' })
      if (body.password !== 's3cret') return json(res, 403, { code: 7, message: 'operator password rejected' })
      if (mode === 'LIVE' && body.acknowledged !== 'I_UNDERSTAND') return json(res, 403, { code: 7, message: 'LIVE requires the acknowledgement phrase' })
      llmRunMode = mode
      json(res, 200, { status: 'ok' })
    })
    return
  }
  if (path === '/v1/llm/notify' && method === 'GET') {
    return json(res, 200, {
      channels: [
        { name: 'telegram', enabled: true, credentialSet: true, targets: 1 },
        { name: 'webhook', enabled: false, credentialSet: false, targets: 0, endpointHost: '' },
      ],
      relayRunning: true,
      checkedAt: new Date().toISOString(),
    })
  }
  if (path === '/v1/llm/notify/test' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.actor) return json(res, 400, { code: 3, message: 'an actor is required' })
      json(res, 200, { relayRunning: true, eventType: 'llm_notify_test' })
    })
    return
  }
  // W7-03 用量读面（离线样本：24h 已结算调用汇总；成本刻意缺席）
  if (path === '/v1/llm/usage' && method === 'GET') {
    return json(res, 200, {
      windowHours: 24,
      lines: [
        { kind: 'JEV', purpose: 'SYSTEMONE', reservations: 6, inputTokens: 4200, outputTokens: 1800, avgLatencyMs: 2300 },
        { kind: 'LLM_OPENAI_COMPAT', purpose: 'CHAT', reservations: 9, inputTokens: 31500, outputTokens: 12400, avgLatencyMs: 8100 },
        { kind: 'LLM_OPENAI_COMPAT', purpose: 'DECISION', reservations: 3, inputTokens: 20400, outputTokens: 6900, avgLatencyMs: 15200 },
      ],
      totalInputTokens: 56100, totalOutputTokens: 21100, totalReservations: 18,
      checkedAt: new Date().toISOString(),
    })
  }
  // W7-02 手动对账：mock 恒 CLEAN（差异路径由真端点的 M5-08 测试覆盖）
  if (path === '/v1/llm/reconcile' && method === 'POST') {
    return json(res, 200, { reconciliationId: `recon-mock-${Date.now()}`, reconciled: true, deltas: [] })
  }
  // W7-07 覆盖轮询运行面（实测样本：一个 sweep 进行到一半）
  if (path === '/v1/llm/coverage' && method === 'GET') {
    return json(res, 200, {
      enabled: true, scope: 'ALL_USDTM_PERPETUAL', contractCount: 588,
      rotationCycleMinutes: 10, visitedThisCycle: 214, cycleProgressPercent: 36.4,
      worstContractAgeSeconds: 331, weightUsedPerMinute: 22, weightBudgetPerMinute: 1440,
      cyclesCompleted: 412, lastCycleCompletedAt: '2026-10-11T12:50:03Z',
      lastTickerRefresh: '2026-10-11T12:50:00Z', lastReload: '2026-10-11T12:04:41Z',
      reloadsThisHour: 588, reloadMarkets: 588, incrementSpacingMs: 1020, failures: 2,
      checkedAt: new Date().toISOString(),
    })
  }
  // mock 校验口令为 s3cret（仅本地演示这三重闸：重认证 + CAS + 幂等键）
  if (path === '/v1/llm/activepair' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (body.operatorPassword !== 's3cret') {
        return json(res, 403, { code: 7, message: 'operator password rejected' })
      }
      if (Number(body.expectedPairEpoch) !== activePair.pairEpoch) {
        return json(res, 409, { code: 10, message: `pair epoch conflict: expected ${body.expectedPairEpoch}, stored ${activePair.pairEpoch}` })
      }
      activePair.pairEpoch += 1
      activePair.llmProfileRevisionId = body.llmProfileRevisionId || null
      activePair.jevProfileRevisionId = body.jevProfileRevisionId || null
      activePair.readiness = body.llmProfileRevisionId && body.jevProfileRevisionId ? 'READY' : 'INCOMPLETE'
      activePair.updatedAt = new Date().toISOString()
      activePair.updatedBy = body.actor || 'op'
      json(res, 200, { pairEpoch: activePair.pairEpoch, readiness: activePair.readiness })
    })
    return
  }
  if (path === '/v1/llm/decisions' && method === 'GET') {
    const version = url.searchParams.get('strategyVersionId')
    const limit = Number(url.searchParams.get('limit') ?? decisionRuns.length)
    const filtered = version ? decisionRuns.filter((r) => r.strategyVersionId === version) : decisionRuns
    return json(res, 200, { runs: filtered.slice(0, limit) })
  }
  if ((path === '/v1/llm/decisions/trigger' || path === '/v1/llm/decisions/preview') && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const isPreview = path.endsWith('/preview')
      const run = {
        id: `run-mock-${decisionRuns.length + 1}`,
        strategyVersionId: body.strategyVersionId || '',
        snapshotId: `snap-mock-${decisionRuns.length + 1}`,
        trigger: isPreview ? 'MANUAL_PREVIEW' : 'MANUAL',
        runMode: 'SHADOW',
        decision: isPreview ? 'HOLD' : 'REVIEW',
        reason: isPreview ? 'PREVIEW_ONLY_HOLD' : 'AWAITING_CONFIRMATION',
        inputHash: 'ff00ff00ff00ff00ff00ff00ff00ff0000000000000000000000000000000000',
        llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
        createdAt: new Date().toISOString(), latencyMs: 5400, llmRoundCount: 2, toolCallCount: 3,
      }
      decisionRuns.unshift(run)
      json(res, 200, run)
    })
    return
  }
  const lineageMatch = path.match(/^\/v1\/llm\/decisions\/([^/]+)\/lineage$/)
  if (lineageMatch && method === 'GET') {
    const run = decisionRuns.find((r) => r.id === lineageMatch[1])
    if (!run) return json(res, 404, { code: 5, message: `decision run not found: ${lineageMatch[1]}` })
    const rejected = run.id === 'run-3'
    return json(res, 200, {
      run,
      riskEvaluations: run.id === 'run-2' ? [] : [{
        riskEvaluationId: `risk-${run.id}`, checkVersion: 'v7', verdict: rejected ? 'REJECTED' : 'PASSED',
        checksJson: JSON.stringify({
          orderNotional: { actual: rejected ? '7200' : '4500', allowed: '5000', pass: !rejected },
          totalExposure: { actual: '12000', allowed: '30000', pass: true },
          dailyLoss: { actual: '-120', allowed: '-2500', pass: true },
        }),
        rejectionsJson: rejected ? JSON.stringify([{ code: 'RISK_ORDER_NOTIONAL_EXCEEDED', actual: '7200', allowed: '5000' }]) : '[]',
        envelopeHash: 'env-9f3a2b1c4d5e6f70', computedQty: '0.073', computedNotionalUsd: '4500',
        referencePrice: '61643.8', evaluatedAt: run.createdAt,
      }],
      auditEvents: [{
        id: `aud-${run.id}`, kind: 'DECISION_RUN', actor: 'engine', subject: run.id,
        detailJson: JSON.stringify({ trigger: run.trigger, decision: run.decision }), createdAt: run.createdAt,
      }],
    })
  }
  const decisionDetailMatch = path.match(/^\/v1\/llm\/decisions\/([^/]+)$/)
  if (decisionDetailMatch && method === 'GET') {
    const run = decisionRuns.find((r) => r.id === decisionDetailMatch[1])
    if (!run) return json(res, 404, { code: 5, message: `decision run not found: ${decisionDetailMatch[1]}` })
    return json(res, 200, {
      run,
      candidateJson: JSON.stringify({
        snapshotId: run.snapshotId, strategyVersionId: run.strategyVersionId,
        marketId: 'Binance:usdtmarginedfutures:BTC_USDT', action: run.decision === 'OPEN' ? 'OPEN_LONG' : 'HOLD',
        evidenceRefs: ['tool:getCandleWindow:call-1'], proposedMaxNotionalUsd: null,
        uncertainty: 'LOW', ttlSeconds: 30,
      }),
      jevJson: JSON.stringify({
        requestHash: 'aa11bb22cc33dd44', model: 'jev-1.0.0',
        response: {
          choice: { selected: 'hold_observe', distribution: { open: 0.18, hold_observe: 0.66, close: 0.16 } },
          score: { value: 0.42 }, noul: { flagged: false },
        },
      }),
      toolTranscriptJson: JSON.stringify([{
        tool: 'getCandleWindow', ref: 'tool:getCandleWindow:call-1',
        args: { marketId: 'BTC_USDT', interval: '1h', bars: 220 }, sha256: 'abcdef0123456789', rows: 220,
      }]),
    })
  }
  // ── 期货持仓（老端点；持仓复核页的需求③数据面）──────────────────────
  if (path === '/v1/getallmanagedpositions' && method === 'GET') {
    return json(res, 200, { positions: managedPositions })
  }
  // ── 筛选与批量决策工作台（docs/llm-dev/13）────────────────────────────
  if (path === '/v1/llm/screener/metrics' && method === 'GET') {
    return json(res, 200, {
      maxLimit: 50, defaultLimit: 20,
      metrics: [
        { name: 'lastprice', kind: 'NUMERIC', tier: 'TICKER_CACHED', unit: 'USDT', description: '最新成交价' },
        { name: 'change24h', kind: 'NUMERIC', tier: 'TICKER_CACHED', unit: '%', description: '24h 涨跌幅' },
        { name: 'quotevolume24h', kind: 'NUMERIC', tier: 'TICKER_CACHED', unit: 'USDT', description: '24h 报价额' },
        { name: 'change4h', kind: 'NUMERIC', tier: 'KLINES_CACHED', unit: '%', description: '4h 涨跌幅（收盘对收盘）' },
        { name: 'change48h', kind: 'NUMERIC', tier: 'KLINES_CACHED', unit: '%', description: '48h 涨跌幅（收盘对收盘）' },
        { name: 'atrpercent', kind: 'NUMERIC', tier: 'KLINES_CACHED', unit: '%', description: 'ATR(14)/close 百分比' },
        { name: 'range24h', kind: 'NUMERIC', tier: 'KLINES_CACHED', unit: '%', description: '24 根区间振幅百分比' },
        { name: 'macdstate', kind: 'CATEGORY', tier: 'KLINES_CACHED', description: 'MACD 柱符号', categories: ['BULL', 'BEAR'] },
        { name: 'macdhist', kind: 'NUMERIC', tier: 'KLINES_CACHED', unit: 'price units', description: 'MACD 柱状图最新值（macdstate 的连续可排序版）' },
        { name: 'macdcross', kind: 'CATEGORY', tier: 'KLINES_CACHED', description: '近期交叉', categories: ['BULL_CROSS', 'BEAR_CROSS', 'NONE'] },
        { name: 'takernetflowratio24h', kind: 'NUMERIC', tier: 'VENUE_READ', description: '净流入占比 ∈[-1,1]' },
        { name: 'takernetflow24h', kind: 'NUMERIC', tier: 'VENUE_READ', unit: 'venue volume units', description: '24h 主动买量−主动卖量（原样报数）' },
      ],
    })
  }
  if (path === '/v1/llm/screener/run' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      JSON.parse(raw || '{}')
      json(res, 200, {
        universeSize: 588, matched: 2, filteredOut: 4, excludedForMissing: 9,
        notes: ['K 线缓存窗口：最多 300 根 1h（mock 数据）', '净流入读：按 24h 成交额前 100 扫描（mock 固定返回 2 行）'],
        rows: [
          { marketId: 'Binance:usdtmarginedfutures:BTC_USDT', numbersJson: JSON.stringify({ change24h: 3.2, quotevolume24h: 912340000, change4h: 0.8, change48h: -1.2, takernetflowratio24h: 0.18 }), statesJson: JSON.stringify({ macdstate: 'BULL', macdcross: 'BULL_CROSS' }) },
          { marketId: 'Binance:usdtmarginedfutures:ETH_USDT', numbersJson: JSON.stringify({ change24h: -1.5, quotevolume24h: 512340000, change4h: -0.4, change48h: 2.1, takernetflowratio24h: -0.07 }), statesJson: JSON.stringify({ macdstate: 'BEAR', macdcross: 'NONE' }) },
        ],
      })
    })
    return
  }
  if (path === '/v1/llm/batchdecisions' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.strategyVersionId || !Array.isArray(body.marketIds) || body.marketIds.length === 0) {
        return json(res, 400, { code: 3, message: 'strategyVersionId and marketIds are required' })
      }
      const jobId = `job-${++batchJobSeq}`
      const rows = body.marketIds.map((m) => {
        const runId = `run-${batchJobSeq}-${m.slice(-8)}`
        // 批量产出的决策记录联进 decisionRuns：决策列表/详情/血缘三页可查（与真实落库语义一致）
        decisionRuns.push({
          id: runId, strategyVersionId: body.strategyVersionId, snapshotId: `snap-${runId}`,
          trigger: body.kind === 'POSITION_REVIEW' ? 'MANUAL_POSITION_REVIEW' : 'MANUAL_BATCH',
          runMode: 'SHADOW', decision: 'HOLD', reason: 'AWAITING_HARD_RISK_AND_APPROVAL',
          inputHash: '00'.repeat(32), llmModel: 'deepseek-chat', jevModel: 'jev-1.0.0',
          createdAt: new Date().toISOString(), latencyMs: 0, llmRoundCount: 0, toolCallCount: 0,
        })
        return { marketId: m, status: 'DONE', decision: 'HOLD', reason: 'AWAITING_HARD_RISK_AND_APPROVAL', decisionRunId: runId }
      })
      batchJobs.set(jobId, { jobId, kind: body.kind ?? 'OPEN_DECISION', strategyVersionId: body.strategyVersionId, actor: body.actor ?? 'webui-operator', createdAt: new Date().toISOString(), done: true, total: rows.length, finished: rows.length, rows })
      // 起批是治理动作：真后端落 BATCH_DECISION_STARTED 审计（fail-closed），mock 同步进审计流
      auditEvents.unshift({
        id: `aud-batch-${batchJobSeq}`, kind: 'BATCH_DECISION_STARTED', actor: body.actor ?? 'webui-operator',
        subject: jobId, detailJson: JSON.stringify({ kind: body.kind ?? 'OPEN_DECISION', strategyVersionId: body.strategyVersionId, markets: rows.length }),
        createdAt: new Date().toISOString(),
      })
      json(res, 200, { jobId })
    })
    return
  }
  const batchMatch = path.match(/^\/v1\/llm\/batchdecisions\/([^/]+)$/)
  if (batchMatch && method === 'GET') {
    const job = batchJobs.get(batchMatch[1])
    if (!job) return json(res, 404, { code: 5, message: `batch job not found: ${batchMatch[1]}` })
    return json(res, 200, job)
  }
  const cancelMatch = path.match(/^\/v1\/llm\/batchdecisions\/([^/]+)\/cancel$/)
  if (cancelMatch && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.actor) return json(res, 400, { code: 3, message: 'actor is required' })
      const job = batchJobs.get(cancelMatch[1])
      if (!job) return json(res, 404, { code: 5, message: `batch job not found: ${cancelMatch[1]}` })
      if (job.done) return json(res, 400, { code: 3, message: `job ${cancelMatch[1]} already finished` })
      let cancelled = 0
      for (const row of job.rows) {
        if (row.status === 'PENDING' || row.status === 'RUNNING') {
          row.status = 'CANCELLED'
          row.reason = 'cancelled by operator'
          cancelled++
        }
      }
      job.done = true
      auditEvents.unshift({
        id: `aud-cancel-${Date.now()}`, kind: 'BATCH_DECISION_CANCELLED', actor: body.actor,
        subject: job.jobId, detailJson: JSON.stringify({ markets: job.rows.length }),
        createdAt: new Date().toISOString(),
      })
      json(res, 200, { jobId: job.jobId, done: job.done, cancelled })
    })
    return
  }
  // N11 覆盖联动（mock）：真后端写交换配置并刷新轮询器；mock 只回状态。
  if (path === '/v1/llm/markettracked' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      if (!body.marketId || !body.actor) return json(res, 400, { code: 3, message: 'marketId and actor are required' })
      json(res, 200, {
        marketId: body.marketId,
        tracked: !!body.tracked,
        status: body.tracked ? 'TRACKED' : 'UNTRACKED',
      })
    })
    return
  }
  if (path === '/v1/llm/audit' && method === 'GET') {
    const kind = url.searchParams.get('kind')
    const limit = Number(url.searchParams.get('limit') ?? auditEvents.length)
    const filtered = kind ? auditEvents.filter((e) => e.kind === kind) : auditEvents
    return json(res, 200, { events: filtered.slice(0, limit) })
  }
  if (path === '/v1/llm/riskswitches' && method === 'GET') {
    return json(res, 200, { switches: riskSwitches })
  }
  if (path === '/v1/llm/riskswitch' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const body = JSON.parse(raw || '{}')
      const row = riskSwitches.find((s) => s.name === body.name)
      if (!row) return json(res, 404, { code: 5, message: `unknown switch: ${body.name}` })
      if (!body.enabled && row.locked) {
        return json(res, 403, { code: 7, message: `this switch cannot be turned off over RPC: ${row.name}` })
      }
      row.enabled = !!body.enabled
      json(res, 200, { status: 'ok' })
    })
    return
  }
  const providerUpdateMatch = path.match(/^\/v1\/llm\/providers\/([^/]+)$/)
  if (providerUpdateMatch && method === 'POST') {    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const profile = providerProfiles.find((p) => p.id === providerUpdateMatch[1])
      if (!profile) return json(res, 404, { code: 5, message: `profile not found: ${providerUpdateMatch[1]}` })
      const body = JSON.parse(raw || '{}')
      const nextNo = (profile.revision?.revisionNo ?? 0) + 1
      profile.revisionCount = nextNo
      profile.revision = {
        id: `rev-mock-${profile.id}-${nextNo}`, profileId: profile.id, revisionNo: nextNo, kind: profile.kind,
        baseUrl: body.baseUrl ?? '', modelId: body.modelId ?? '',
        apiKeyFingerprint: body.apiKey ? `fp-${String(body.apiKey).slice(-8)}` : '',
        timeoutSeconds: body.timeoutSeconds ?? 0,
        ...(body.maxOutputTokens ? { maxOutputTokens: body.maxOutputTokens } : {}),
        ...(body.promptVersion ? { promptVersion: body.promptVersion } : {}),
        ...(body.questionTemplateVersion ? { questionTemplateVersion: body.questionTemplateVersion } : {}),
        ...(body.dailyBudgetUsd ? { dailyBudgetUsd: body.dailyBudgetUsd } : {}),
        ...(body.reservationCap24h ? { reservationCap24h: body.reservationCap24h } : {}),
        ...(body.allowInsecureLocalhost ? { allowInsecureLocalhost: true } : {}),
        createdAt: new Date().toISOString(), createdBy: body.actor ?? 'op',
      }
      json(res, 200, { revision: profile.revision })
    })
    return
  }
  const providerTestMatch = path.match(/^\/v1\/llm\/providers\/([^/]+)\/test$/)
  if (providerTestMatch && method === 'POST') {
    req.on('data', () => {})
    req.on('end', () => {
      const profile = providerProfiles.find((p) => p.id === providerTestMatch[1])
      if (!profile) return json(res, 404, { code: 5, message: `profile not found: ${providerTestMatch[1]}` })
      if (!profile.revision) return json(res, 400, { code: 3, message: 'profile has no live revision to test' })
      const results = profile.kind === 'JEV'
        ? [{ layer: 'JEV', passed: true, latencyMs: 812, inputTokens: 240, outputTokens: 96, responseModel: profile.revision.modelId }]
        : [
            { layer: 'AUTH', passed: true, latencyMs: 402, inputTokens: 18, outputTokens: 2, responseModel: profile.revision.modelId },
            { layer: 'TOOL_CALL', passed: true, latencyMs: 1240, inputTokens: 64, outputTokens: 21, responseModel: profile.revision.modelId },
            { layer: 'SCHEMA', passed: false, errorCode: 'CANDIDATE_INCOMPLETE', errorMessage: 'candidate is missing required field: proposedMaxNotionalUsd is absent', latencyMs: 880, responseModel: profile.revision.modelId },
            { layer: 'STREAM', passed: true, latencyMs: 512, inputTokens: 12, outputTokens: 1, responseModel: profile.revision.modelId },
          ]
      json(res, 200, { revisionId: profile.revision.id, kind: profile.kind, results })
    })
    return
  }

  if (path === '/v1/llm/conversations' && method === 'GET') {
    return json(res, 200, { conversations: [...conversations.values()] })
  }
  if (path === '/v1/llm/conversations' && method === 'POST') {
    let raw = ''
    req.on('data', (c) => { raw += c })
    req.on('end', () => {
      const id = `conv-mock-${conversations.size + 1}`
      const now = new Date().toISOString()
      const conv = {
        id, title: JSON.parse(raw || '{}').title ?? 'mock', kind: 'CHAT',
        createdAt: now, updatedAt: now, archived: false, messageCount: 0,
      }
      conversations.set(id, conv)
      messages.set(id, [])
      json(res, 200, conv)
    })
    return
  }
  const listMatch = path.match(/^\/v1\/llm\/conversations\/([^/]+)\/messages$/)
  if (listMatch && method === 'GET') {
    return json(res, 200, { messages: messages.get(listMatch[1]) ?? [] })
  }
  const archiveMatch = path.match(/^\/v1\/llm\/conversations\/([^/]+)\/archive$/)
  if (archiveMatch && method === 'POST') {
    const conv = conversations.get(archiveMatch[1])
    if (conv) conv.archived = true
    return json(res, 200, { status: 'success' })
  }

  if (path === '/v1/llm/chatstream' && method === 'GET') {
    const conversationId = url.searchParams.get('conversationId') ?? ''
    const content = url.searchParams.get('content') ?? ''
    const rows = messages.get(conversationId) ?? []
    rows.push({ id: `m-${++messageSeq}`, seq: rows.length + 1, role: 'user', content })
    messages.set(conversationId, rows)

    res.writeHead(200, {
      'content-type': 'application/json',
      'cache-control': 'no-cache',
    })
    // 需求①的对话筛选场景：消息含「筛/找/符合」时演示 screenMarkets 全流程
    // （真实后端由模型自主调用工具；mock 无法跑模型，用关键词分叉演示两种回复形态）
    const screening = /筛|找|符合|screen|scan/i.test(content)
    // docs/llm-dev/16：配置提案场景（真实后端由模型调用 proposeAction 产出草案）
    const proposalKind = /取消/.test(content) ? 'CANCEL_BATCH_JOB'
      : /复核/.test(content) ? 'START_POSITION_REVIEW'
        : /启用|停用/.test(content) ? 'SET_MARKET_TRACKED'
          : /批量|开仓决策/.test(content) ? 'START_OPEN_DECISION_BATCH' : ''
    const proposal = proposalKind === '' ? undefined : (
      proposalKind === 'CANCEL_BATCH_JOB' ? { action: proposalKind, summary: '取消批量作业 job-1', jobId: 'job-1', actor: 'webui-operator' }
        : proposalKind === 'START_POSITION_REVIEW' ? { action: proposalKind, summary: '复核全部当前持仓', allPositions: true, strategyVersionId: 'version-live', actor: 'webui-operator' }
          : proposalKind === 'SET_MARKET_TRACKED' ? { action: proposalKind, summary: '启用 SOL_USDT', marketId: 'Binance:usdtmarginedfutures:SOL_USDT', tracked: true, actor: 'webui-operator' }
            : { action: proposalKind, summary: '对 BTC/ETH 生成开仓决策', marketIds: ['Binance:usdtmarginedfutures:BTC_USDT', 'Binance:usdtmarginedfutures:ETH_USDT'], strategyVersionId: 'version-live', actor: 'webui-operator' }
    )
    const deltas = proposal ? [
      '已按你的要求生成**配置提案**：请在下方确认卡片核对参数后点「确认执行」。\n\n',
      '提案本身不会自动执行——确认后调用与手动页面相同的端点（写闸与审批绑定照旧生效）。\n',
    ] : screening ? [
      '## 全宇宙筛选完成（宇宙 588 · 命中 2 · 条件不符 4 · 缺席排除 9）\n\n',
      '- 条件：`change24h ≥ 5`、`change4h ≥ 0`（AND）\n',
      '- 排序：`quotevolume24h` 降序 · 返回前 20（缺席指标的市场被排除并计数，不以 0 冒充）\n\n',
      '| 合约 | change24h | change4h | MACD |\n|---|---|---|---|\n',
      '| Binance:usdtmarginedfutures:BTC_USDT | +6.2% | +0.8% | BULL |\n',
      '| Binance:usdtmarginedfutures:ETH_USDT | +5.1% | +0.3% | BULL |\n\n',
      '下一步：可在「筛选与开仓决策」页对选定市场发起批量开仓决策（只记录不下单）。证据如下。',
    ] : [
      '## BTC_USDT 1h 结构\n\n',
      '- **价格区间**：62,000–64,500 USDT（震荡）\n',
      '- **资金费**：0.0042%／8h（中性）\n',
      '- 建议：观察，等待 `MACD` 柱转正。\n\n',
      '| 指标 | 读数 | 判定 |\n|---|---|---|\n| RSI(14) | 55.2 | 中性 |\n| MACD 柱 | -12.4 | 偏空 |\n\n',
      '证据如下。',
    ]
    let index = 0
    const timer = setInterval(() => {
      if (index < deltas.length) {
        res.write(JSON.stringify({ delta: deltas[index++] }) + '\n')
        return
      }
      clearInterval(timer)
      const reply = deltas.join('')
      rows.push({ id: `m-${++messageSeq}`, seq: rows.length + 1, role: 'assistant', content: reply })
      if (screening) {
        res.write(JSON.stringify({ progress: { tool: 'screenMarkets', phase: 'CANDLES', done: 588, total: 588, message: '已扫描 588/588 个合约（mock）' } }) + '\n')
      }
      const conv = conversations.get(conversationId)
      if (conv) conv.messageCount = rows.length
      res.write(JSON.stringify({
        final: {
          messageId: rows[rows.length - 1].id,
          assistantMessage: reply,
          inputTokens: 120,
          outputTokens: 30,
          latencyMs: 900,
          evidence: screening ? [{
            ref: 'tool:screenMarkets:call-1', tool: 'screenMarkets',
            exchange: 'Binance', quality: 'DATABASE_CACHED', sourceTime: new Date().toISOString(),
            sha256: '9f2c1a0b3d4e5f60718293a4b5c6d7e8f9012345678abcdef0123456789abcd',
          }] : [{
            ref: 'tool:getCandleWindow:call-1', tool: 'getCandleWindow',
            exchange: 'Binance', marketId: 'Binance:usdtmarginedfutures:BTC_USDT',
            quality: 'EXCHANGE_REST', sourceTime: new Date().toISOString(),
            sha256: 'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789',
          }],
          ...(proposal ? { proposals: [proposal] } : {}),
        },
      }) + '\n')
      res.end()
    }, 180)
    return
  }

  json(res, 404, { code: 5, message: `not found: ${path}` })
})

server.listen(PORT, '127.0.0.1', () => {
  console.log(`[mock-gateway] https://127.0.0.1:${PORT}/v1/ (server-streaming 形态)`)
})
