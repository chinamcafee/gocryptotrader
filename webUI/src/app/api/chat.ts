/**
 * 对话（M3-09 / W5-01 开工）：会话 REST + 真流式通道（Q21=C）。
 *
 * 与 gctApi.ts 同一约定族：`/api/` 前缀走本地代理（凭据只在代理进程内），
 * 非 2xx 归一成可读错误。流式单独走 fetch + ReadableStream——RTK Query 不
 * 适合读 chunked 响应，这里宁可少一层抽象。
 */

import { createGatewayStreamParser } from './sse'

export interface LlmConversation {
  id: string
  title: string
  kind: string
  createdAt: string
  updatedAt: string
  archived: boolean
  messageCount: number
}

export interface LlmChatMessage {
  id: string
  seq: number
  role: string
  content: string
  createdAt?: string
  errorCode?: string
  inputTokens?: number
  outputTokens?: number
  latencyMs?: number
}

export interface LlmEvidenceCard {
  ref: string
  tool: string
  exchange?: string
  marketId?: string
  quality?: string
  sourceTime?: string
  sha256?: string
}

/** 惰性配置提案（docs/llm-dev/16）：模型只产出草稿，执行必须由操作者在卡片上确认。 */
export interface LlmActionProposal {
  action: string
  summary: string
  marketIds?: string[]
  allPositions?: boolean
  strategyVersionId?: string
  actor?: string
  marketId?: string
  tracked?: boolean
  jobId?: string
}

export interface LlmSendMessageFinal {
  messageId: string
  assistantMessage: string
  toolTranscript?: string
  inputTokens?: number
  outputTokens?: number
  latencyMs?: number
  errorCode?: string
  evidence?: LlmEvidenceCard[]
  proposals?: LlmActionProposal[]
}

async function normalizeError(res: Response): Promise<Error> {
  const text = await res.text().catch(() => '')
  let message = text.slice(0, 300)
  try {
    const body = JSON.parse(text) as { message?: string; error?: string }
    message = body.message ?? body.error ?? message
  } catch {
    /* 非 JSON 错误体，保留原文 */
  }
  return new Error(`[${res.status}] ${message || '请求失败'}`)
}

async function gatewayJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/${path}`, init)  // 网关前缀 /v1/ 在代理的 GATEWAY_BASE 里，浏览器侧不带
  if (!res.ok) throw await normalizeError(res)
  const text = await res.text()
  return (text ? JSON.parse(text) : {}) as T
}

const jsonInit = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})

export const listConversations = async (includeArchived = false): Promise<LlmConversation[]> => {
  const body = await gatewayJSON<{ conversations?: LlmConversation[] }>(
    `llm/conversations?includeArchived=${includeArchived}`,
  )
  return body.conversations ?? []
}

export const createConversation = (title: string): Promise<LlmConversation> =>
  gatewayJSON<LlmConversation>('llm/conversations', jsonInit('POST', { title }))

export const getConversationMessages = async (id: string): Promise<LlmChatMessage[]> => {
  const body = await gatewayJSON<{ messages?: LlmChatMessage[] }>(
    `llm/conversations/${encodeURIComponent(id)}/messages`,
  )
  return body.messages ?? []
}

export const archiveConversation = (id: string): Promise<unknown> =>
  gatewayJSON(`llm/conversations/${encodeURIComponent(id)}/archive`, jsonInit('POST', {}))

/** 工具运行进度（例如全量合约扫描）：只驱动加载卡，不进入正文。 */
export interface LlmToolProgress {
  tool?: string
  phase?: string
  done?: number
  total?: number
  message?: string
}

export interface StreamCallbacks {
  onDelta: (delta: string) => void
  onProgress?: (progress: LlmToolProgress) => void
  signal?: AbortSignal
}

/**
 * 发送一轮并消费流式回复。
 *
 * 中断（连接断开、无 final 事件）抛错：后端对中断不落半成品行（05 §1），
 * UI 也必须把这段文字展示为「未完成」而不是一条回复。
 */
export async function streamAssistantMessage(
  conversationId: string,
  content: string,
  cb: StreamCallbacks,
): Promise<LlmSendMessageFinal> {
  const params = new URLSearchParams({ conversationId, content })
  const res = await fetch(`/api/llm/chatstream?${params.toString()}`, {
    headers: { Accept: 'application/json' },
    signal: cb.signal,
  })
  if (!res.ok) throw await normalizeError(res)
  if (!res.body) throw new Error('流式响应没有 body')
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  const parser = createGatewayStreamParser()
  let final: LlmSendMessageFinal | null = null
  for (;;) {
    const { value, done } = await reader.read()
    const events = parser.push(done ? '' : decoder.decode(value, { stream: true }))
    if (done) events.push(...parser.flush())
    for (const event of events) {
      if (event.delta) cb.onDelta(event.delta)
      if (event.progress) cb.onProgress?.(event.progress as unknown as LlmToolProgress)
      if (event.error) throw new Error(`流式中断：${JSON.stringify(event.error).slice(0, 300)}`)
      if (event.final) final = event.final as unknown as LlmSendMessageFinal
    }
    if (done) break
  }
  if (!final) throw new Error('流在 final 事件之前结束——半截回复不是回复，已按中断处理')
  return final
}
