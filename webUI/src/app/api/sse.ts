/**
 * grpc-gateway server-streaming 的分帧解析器（M3-09，Q21=C）。
 *
 * 网关把每个 message 作为一个 JSON 对象、以换行分隔写进 chunked 响应里
 * （不是标准 SSE 的 `data:` 帧）；但 TCP 分块与消息边界无关——一个 message
 * 可能横跨两个网络 chunk。解析器因此按「累积 + 换行切分」工作：只有完整的
 * 一行才 decode，半行 JSON 不是数据。与 Go 侧流式客户端同一条纪律。
 */

export interface GatewayStreamEvent {
  delta?: string
  final?: Record<string, unknown>
  error?: unknown
  /** 工具运行进度（全量扫描等），与正文增量并列、不进入正文。 */
  progress?: Record<string, unknown>
}

export interface GatewayStreamParser {
  push: (chunk: string) => GatewayStreamEvent[]
  flush: () => GatewayStreamEvent[]
}

export function createGatewayStreamParser(): GatewayStreamParser {
  let buffer = ''

  const parseLine = (line: string): GatewayStreamEvent | null => {
    const trimmed = line.trim()
    if (!trimmed) return null
    let parsed: unknown
    try {
      parsed = JSON.parse(trimmed)
    } catch {
      throw new Error(`流式响应包含无法解析的分片：${trimmed.slice(0, 120)}`)
    }
    if (typeof parsed !== 'object' || parsed === null) return null
    const envelope = parsed as Record<string, unknown>
    // 网关 v2 默认直接输出 message JSON；某些配置下包一层 result。
    const body = (envelope.result as Record<string, unknown> | undefined) ?? envelope
    const event: GatewayStreamEvent = {}
    if (typeof body.delta === 'string') event.delta = body.delta
    if (body.final) event.final = body.final as Record<string, unknown>
    if (body.progress) event.progress = body.progress as Record<string, unknown>
    if (envelope.error) event.error = envelope.error
    return event
  }

  const drain = (whole: boolean): GatewayStreamEvent[] => {
    const out: GatewayStreamEvent[] = []
    let index: number
    while ((index = buffer.indexOf('\n')) >= 0) {
      const line = buffer.slice(0, index)
      buffer = buffer.slice(index + 1)
      const event = parseLine(line)
      if (event) out.push(event)
    }
    if (whole && buffer.trim()) {
      const event = parseLine(buffer)
      buffer = ''
      if (event) out.push(event)
    }
    return out
  }

  return {
    push: (chunk: string) => {
      buffer += chunk
      return drain(false)
    },
    flush: () => drain(true),
  }
}
