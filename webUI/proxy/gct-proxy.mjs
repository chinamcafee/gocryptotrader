#!/usr/bin/env node
/**
 * GoCryptoTrader REST 网关的本地代理。
 *
 * 存在理由（三条，缺一不可）：
 *   1) 安全：REST 网关用 Basic Auth。凭据只能留在本进程内存，绝不能进浏览器。
 *   2) TLS：网关是自签证书，浏览器直连会被拦。
 *   3) 治理：代理层实施「只读白名单」与「写操作默认关闭」，并给尚未实现的 LLM 端点
 *      返回确定的 501，让前端能稳定地展示「后端待实现」而不是随机报错。
 *
 * 依据：docs/llm-dev/08-对话与接口层.md §7、docs/llm-dev/10-安全与风控.md §3
 *      docs/localDeploy/README.md「实测验证记录」（camelCase / pair 对象 / POST-GET / 时间格式）
 */

import http from 'node:http'
import https from 'node:https'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const ROOT = path.resolve(HERE, '..')

// ── 读取 .env（极简实现，避免额外依赖） ────────────────────────────────────────
function loadEnvFile(file) {
  if (!fs.existsSync(file)) return {}
  const out = {}
  for (const raw of fs.readFileSync(file, 'utf8').split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith('#') || line.startsWith('>')) continue
    const i = line.indexOf('=')
    if (i < 0) continue
    out[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return out
}
const fileEnv = { ...loadEnvFile(path.join(ROOT, '.env.example')), ...loadEnvFile(path.join(ROOT, '.env')) }
const env = (k, dflt) => process.env[k] ?? fileEnv[k] ?? dflt

const PROXY_HOST = env('GCT_PROXY_HOST', '127.0.0.1')
const PROXY_PORT = Number(env('GCT_PROXY_PORT', '8788'))
const GATEWAY_BASE = env('GCT_GATEWAY_BASE', 'https://127.0.0.1:9053/v1/')
const CONFIG_PATH = env('GCT_CONFIG_PATH', '~/.gocryptotrader/config.json').replace(/^~/, os.homedir())
const ALLOW_WRITES = String(env('GCT_ALLOW_WRITES', 'false')).toLowerCase() === 'true'
const TIMEOUT_MS = Number(env('GCT_UPSTREAM_TIMEOUT_MS', '45000'))
const TLS_INSECURE = String(env('GCT_TLS_INSECURE', 'true')).toLowerCase() === 'true'

// ── 白名单 ────────────────────────────────────────────────────────────────────
// 语义只读的端点。实测确认：getexchangepairs 是 POST 但只是查询。
const READ_GET = new Set([
  'getinfo', 'getsubsystems', 'getexchanges', 'getexchangeinfo', 'getrpcendpoints',
  'getorderbooks', 'getorderbookamountbynominal', 'getorderbookamountbyimpact', 'getorderbookmovement',
  'gethistoriccandles', 'gethistoriccandlesextended', 'gettechnicalanalysis',
  'getaccountbalances', 'getcollateral', 'getcollateralmode', 'getleverage',
  'getfuturespositionssummary', 'getfuturespositionsorders', 'getfundingrates', 'getlatestfundingrate',
  'getopeninterest', 'getallmanagedpositions', 'getmanagedposition', 'getmanagedorders',
  'getexchangeassets', 'findmissingsavedcandleintervals', 'getexchangepairs',
])
const READ_POST = new Set(['getticker', 'getorderbook', 'getexchangepairs'])

// 会话的本地数据写操作：创建/归档会话、追加消息发生在本地数据库里，
// 与交易无关（不触碰订单、审批、配对、预算）。它们默认放行，而
// extractpolicy / strategies / approvals / providers / activepair / halt /
// runmode 这类会影响交易治理的端点仍受 GCT_ALLOW_WRITES 闸门约束。
const LLM_LOCAL_WRITES = 'llm/conversations'
const isLlmLocalWrite = (endpoint) => endpoint === LLM_LOCAL_WRITES || endpoint.startsWith(LLM_LOCAL_WRITES + '/')

// 计划中的 LLM 端点（docs/llm-dev/08 §3）。已实现的部分（M3-05/M3-07 的
// conversations 与 chatstream）正常转发；其余仍是网关 404 → 代理 501。
const LLM_PREFIX = 'llm'
const LLM_PLANNED = [
  'llm/conversations', 'llm/chatstream', 'llm/strategies', 'llm/extractpolicy', 'llm/markets',
  'llm/providers', 'llm/activepair', 'llm/decisions', 'llm/audit', 'llm/status', 'llm/runmode',
  'llm/halt', 'llm/budget', 'llm/reconcile', 'llm/riskswitches', 'llm/readiness', 'llm/vault',
]

// ── 凭据 ──────────────────────────────────────────────────────────────────────
function loadCredentials() {
  const cfg = JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf8'))
  const rc = cfg.remoteControl ?? {}
  if (!rc.username || !rc.password) throw new Error('config.json 缺少 remoteControl.username/password')
  return { user: rc.username, pass: rc.password }
}
let AUTH = ''
try {
  const { user, pass } = loadCredentials()
  AUTH = 'Basic ' + Buffer.from(`${user}:${pass}`).toString('base64')
} catch (e) {
  console.error(`[proxy] 读取凭据失败：${e.message}`)
  console.error(`[proxy] 配置文件路径：${CONFIG_PATH}`)
  process.exit(1)
}

const agent = new https.Agent({ rejectUnauthorized: !TLS_INSECURE, keepAlive: true })

function send(res, status, body, ctype = 'application/json; charset=utf-8') {
  const raw = typeof body === 'string' ? body : JSON.stringify(body)
  res.writeHead(status, {
    'Content-Type': ctype,
    'Content-Length': Buffer.byteLength(raw),
    'Access-Control-Allow-Origin': '*',
    'Cache-Control': 'no-store',
  })
  res.end(raw)
}
const fail = (res, status, code, message, detail) =>
  send(res, status, { code, message, ...(detail ? { detail } : {}) })

function isPlannedLlmPath(endpoint) {
  return endpoint === LLM_PREFIX || endpoint.startsWith(LLM_PREFIX + '/') ||
    LLM_PLANNED.some((p) => endpoint.startsWith(p))
}

function forward(method, endpoint, query, res, body) {
  const url = GATEWAY_BASE.replace(/\/?$/, '/') + endpoint + (query ? '?' + query : '')
  const req = https.request(url, { method, agent, headers: { Authorization: AUTH, Accept: 'application/json', ...(body?.length ? { 'Content-Type': 'application/json' } : {}) } }, (up) => {
    // 流式直通（M3-09 的 SSE 桥接）：网关对 server-streaming 用 chunked 且不带
    // Content-Length——这种响应边收边转；缓冲会把真流式变成"等完再一次性给出"。
    const streaming = (up.statusCode ?? 0) >= 200 && (up.statusCode ?? 0) <= 299 && !up.headers['content-length']
    if (streaming) {
      res.writeHead(up.statusCode ?? 200, {
        'Content-Type': up.headers['content-type'] ?? 'application/json; charset=utf-8',
        'Cache-Control': 'no-cache, no-transform',
        'X-Accel-Buffering': 'no',
        'Access-Control-Allow-Origin': '*',
      })
      up.on('data', (c) => res.write(c))
      up.on('end', () => res.end())
      up.on('error', () => res.destroy())
      return
    }
    const chunks = []
    up.on('data', (c) => chunks.push(c))
    up.on('end', () => {
      const text = Buffer.concat(chunks).toString('utf8')
      // 网关对未注册路由返回 404；若是规划中的 LLM 端点，转成确定的 501
      if (up.statusCode === 404 && isPlannedLlmPath(endpoint)) {
        return fail(res, 501, 'NOT_IMPLEMENTED',
          `LLM 端点 /v1/${endpoint} 尚未在后端实现`,
          { endpoint, seeTasks: 'docs/llm-dev/taskProgress/TASKS.md 的 M0 / M1 / M4' })
      }
      if (up.statusCode === 405 && method === 'POST') {
        return fail(res, 405, 'METHOD_NOT_ALLOWED', `上游拒绝 POST /v1/${endpoint}（该端点可能是 GET）`, text.slice(0, 400))
      }
      send(res, up.statusCode ?? 502, text)
    })
  })
  req.setTimeout(TIMEOUT_MS, () => req.destroy(new Error(`上游超时 ${TIMEOUT_MS}ms`)))
  req.on('error', (e) => fail(res, 502, 'UPSTREAM_ERROR', `上游请求失败：${e.message}`, { endpoint, url }))
  if (body?.length) req.write(body)
  req.end()
}

const server = http.createServer((req, res) => {
  const u = new URL(req.url ?? '/', `http://${PROXY_HOST}:${PROXY_PORT}`)
  if (!u.pathname.startsWith('/api/')) return fail(res, 404, 'NOT_FOUND', '只服务 /api/*')

  const endpoint = u.pathname.startsWith('/api/v1/')
    ? u.pathname.slice('/api/v1/'.length)
    : u.pathname.slice('/api/'.length)
  const query = u.searchParams.toString()

  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type',
      'Access-Control-Max-Age': '600',
    })
    return res.end()
  }

  if (endpoint === 'health') {
    return send(res, 200, {
      ok: true, gateway: GATEWAY_BASE, allowWrites: ALLOW_WRITES,
      credentialsLoaded: AUTH.length > 0, plannedLlmEndpoints: LLM_PLANNED.length,
    })
  }

  const isPlanned = isPlannedLlmPath(endpoint)

  if (req.method === 'GET') {
    if (!isPlanned && !READ_GET.has(endpoint)) {
      return fail(res, 403, 'READ_ONLY', `端点 '${endpoint}' 不在只读白名单内`, { hint: '见 webUI/proxy/gct-proxy.mjs 的 READ_GET' })
    }
    return forward('GET', endpoint, query, res)
  }

  if (req.method === 'POST') {
    const body = []
    req.on('data', (c) => body.push(c))
    req.on('end', () => {
      const raw = Buffer.concat(body)
      // 只读的 POST（getticker / getorderbook / getexchangepairs）不受写开关限制 —— 实测确认它们是查询
      if (READ_POST.has(endpoint)) {
        return forward('POST', endpoint, query, res, raw)
      }
      // 会话的本地数据写操作（创建会话）默认放行：它写的是本地会话表，不是交易
      if (isLlmLocalWrite(endpoint)) {
        return forward('POST', endpoint, query, res, raw)
      }
      if (!ALLOW_WRITES) {
        return fail(res, 501, 'WRITES_DISABLED',
          `写操作默认关闭（GCT_ALLOW_WRITES=false），已拦截 POST /v1/${endpoint}`,
          { hint: '这是安全默认值；需要写操作时在 webUI/.env 设 GCT_ALLOW_WRITES=true 并重启代理' })
      }
      if (!isPlanned) {
        return fail(res, 403, 'READ_ONLY', `POST 端点 '${endpoint}' 不在白名单内`)
      }
      forward('POST', endpoint, query, res, raw)
    })
    return
  }

  return fail(res, 405, 'METHOD_NOT_ALLOWED', `不支持的方法 ${req.method}`)
})

server.listen(PROXY_PORT, PROXY_HOST, () => {
  console.log('[proxy] GoCryptoTrader 本地代理已启动')
  console.log(`[proxy]   监听      http://${PROXY_HOST}:${PROXY_PORT}/api/`)
  console.log(`[proxy]   上游      ${GATEWAY_BASE}`)
  console.log(`[proxy]   凭据      ${path.basename(CONFIG_PATH)}（已加载，不回显）`)
  console.log(`[proxy]   写操作    ${ALLOW_WRITES ? '⚠  已开启' : '关闭（默认，安全）'}`)
  console.log(`[proxy]   只读白名单 GET ${READ_GET.size} 个 / POST ${READ_POST.size} 个`)
  console.log(`[proxy]   自检      curl -s http://${PROXY_HOST}:${PROXY_PORT}/api/health`)
})
