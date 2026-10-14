import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Space, Typography } from 'antd'
import { Bubble, Conversations, Sender, Sources } from '@ant-design/x'
import { PlusOutlined } from '@ant-design/icons'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  archiveConversation, createConversation, getConversationMessages, listConversations,
  streamAssistantMessage,
  type LlmChatMessage, type LlmConversation, type LlmEvidenceCard,
} from '@/app/api/chat'
import { TOKENS } from '@/theme'

const { Text, Title } = Typography

interface TurnView {
  key: string
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  error?: string
  evidence?: LlmEvidenceCard[]
}

let turnSeq = 0
const nextKey = () => `turn-${++turnSeq}`

/**
 * 对话工作区（W5-01）：AntD X Conversations 侧栏 + Bubble 流式 + Sources 证据展开
 * + Markdown 渲染（react-markdown 渲染为 React 元素，不注入原始 HTML——模型输出
 * 是外部数据，不能用 dangerouslySetInnerHTML）。
 *
 * 草稿只存在本页内存（per-conversation）：切换会话保留、发送才交给后端落库、
 * 刷新即丢弃——与「不落半成品」同一原则（05 §1）。
 */
export default function ChatPage() {
  const [conversations, setConversations] = useState<LlmConversation[]>([])
  const [conversationId, setConversationId] = useState<string>()
  const [turns, setTurns] = useState<TurnView[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>()
  const [banner, setBanner] = useState<string>()
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const abortRef = useRef<AbortController | undefined>(undefined)

  const draft = conversationId ? (drafts[conversationId] ?? '') : ''
  const setDraft = (text: string) => {
    if (conversationId) setDrafts((prev) => ({ ...prev, [conversationId]: text }))
  }

  const refreshConversations = async (select?: string) => {
    try {
      const list = await listConversations()
      setConversations(list)
      setConversationId((current) => select ?? current ?? list[0]?.id)
      setBanner(undefined)
    } catch (err) {
      setBanner(err instanceof Error ? err.message : String(err))
    }
  }

  useEffect(() => {
    void refreshConversations()
  }, [])

  useEffect(() => {
    if (!conversationId) {
      setTurns([])
      return
    }
    let cancelled = false
    void (async () => {
      try {
        const messages = await getConversationMessages(conversationId)
        if (!cancelled) setTurns(messages.map(toTurn))
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [conversationId])

  const onNewConversation = async () => {
    try {
      const created = await createConversation(`对话 ${new Date().toLocaleString()}`)
      await refreshConversations(created.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const onArchive = async (id: string) => {
    try {
      await archiveConversation(id)
      if (id === conversationId) setConversationId(undefined)
      setDrafts((prev) => {
        const rest = { ...prev }
        delete rest[id]
        return rest
      })
      await refreshConversations()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const onSend = async (text: string) => {
    if (!conversationId || !text.trim() || loading) return
    setError(undefined)
    const userKey = nextKey()
    const assistantKey = nextKey()
    setTurns((prev) => [
      ...prev,
      { key: userKey, role: 'user', content: text },
      { key: assistantKey, role: 'assistant', content: '', streaming: true },
    ])
    setDraft('')
    setLoading(true)
    const controller = new AbortController()
    abortRef.current = controller
    try {
      const final = await streamAssistantMessage(conversationId, text, {
        signal: controller.signal,
        onDelta: (delta) =>
          setTurns((prev) =>
            prev.map((t) => (t.key === assistantKey ? { ...t, content: t.content + delta } : t)),
          ),
      })
      setTurns((prev) =>
        prev.map((t) =>
          t.key === assistantKey
            ? {
                ...t, streaming: false, content: final.assistantMessage,
                evidence: final.evidence,
                error: final.errorCode ? `回合未完成：${final.errorCode}` : undefined,
              }
            : t,
        ),
      )
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      // 中断的流后端不落半成品行（05 §1），这里的文字只展示为「未完成」
      setTurns((prev) =>
        prev.map((t) => (t.key === assistantKey ? { ...t, streaming: false, error: message } : t)),
      )
    } finally {
      setLoading(false)
      abortRef.current = undefined
    }
  }

  return (
    <div style={{ display: 'flex', height: '100%', gap: 12 }}>
      <div style={{ width: 280, flex: '0 0 280px', display: 'flex', flexDirection: 'column', gap: 8 }}>
        <Button icon={<PlusOutlined />} onClick={() => void onNewConversation()} block>
          新建对话
        </Button>
        <div style={{ flex: 1, overflow: 'auto', background: TOKENS.panel, borderRadius: 8, padding: 4 }}>
          <Conversations
            items={conversations.map((c) => ({
              key: c.id,
              label: (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                  <span style={{ fontWeight: 500 }}>{c.title}</span>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    {c.messageCount} 条{c.archived ? ' · 已归档' : ''}
                  </Text>
                </div>
              ),
            }))}
            activeKey={conversationId}
            onActiveChange={(value) => setConversationId(value)}
            menu={(conversation) => ({
              items: [
                { key: 'archive', label: '归档', danger: true, disabled: conversation.archived },
              ],
              onClick: (info) => {
                info.domEvent.stopPropagation()
                if (info.key === 'archive') void onArchive(conversation.key)
              },
            })}
          />
        </div>
      </div>

      <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: 12 }}>
        <Space align="center" wrap>
          <Title level={4} style={{ margin: 0 }}>对话分析</Title>
          <Text type="secondary">只读分析模式：不下单；要求下单时会指向策略审批流程。</Text>
        </Space>

        {banner && (
          <Alert
            type="warning"
            showIcon
            message="对话后端不可达"
            description={`${banner}——若看板刚启动或会话服务未装配（RPC fail-closed），这是预期中的降级提示。`}
          />
        )}
        {error && <Alert type="error" showIcon message={error} closable onClose={() => setError(undefined)} />}

        <div style={{ flex: 1, overflow: 'auto', padding: '4px 0', background: TOKENS.panel, borderRadius: 8 }}>
          {turns.length === 0 ? (
            <div style={{ padding: 32, textAlign: 'center' }}>
              <Text type="secondary">
                {conversationId ? '还没有消息，向下方发送第一条分析请求。' : '创建一个会话开始对话。'}
              </Text>
            </div>
          ) : (
            <Bubble.List
              items={turns.map((turn) => ({
                key: turn.key,
                role: turn.role,
                placement: turn.role === 'user' ? 'end' : 'start',
                content: <TurnContent turn={turn} />,
              }))}
            />
          )}
        </div>

        <div>
          <Sender
            value={draft}
            onChange={setDraft}
            loading={loading}
            disabled={!conversationId}
            placeholder={conversationId ? '问市场结构、指标、资金费、账户状态…（回车发送）' : '先创建一个会话'}
            onSubmit={(text) => void onSend(text)}
            onCancel={() => abortRef.current?.abort()}
          />
          <Text type="secondary" style={{ fontSize: 12 }}>
            草稿只保存在本页内存里（切换会话保留），确认发送后才写入会话记录。
          </Text>
        </div>
      </div>
    </div>
  )
}

function TurnContent({ turn }: { turn: TurnView }) {
  return (
    <div>
      {turn.role === 'assistant' ? (
        <div>
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={MARKDOWN_COMPONENTS}>
            {turn.content || (turn.streaming ? '…' : '')}
          </ReactMarkdown>
        </div>
      ) : (
        <div style={{ whiteSpace: 'pre-wrap' }}>{turn.content}</div>
      )}
      {turn.streaming && <Text type="secondary" style={{ fontSize: 12 }}>（生成中）</Text>}
      {turn.error && (
        <div style={{ marginTop: 6 }}>
          <Text type="danger" style={{ fontSize: 12 }}>未完成：{turn.error}</Text>
        </div>
      )}
      {turn.evidence && turn.evidence.length > 0 && (
        <Sources
          style={{ marginTop: 8 }}
          expandIconPosition="end"
          title={`证据来源（${turn.evidence.length}）`}
          items={turn.evidence.map((card) => ({
            key: card.ref,
            title: (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span>{card.tool}｜{card.marketId ?? card.ref}</span>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  质量 {card.quality ?? '未知'}
                  {card.sourceTime ? `｜源时间 ${card.sourceTime}` : ''}
                  {card.sha256 ? `｜sha256 ${card.sha256.slice(0, 12)}…` : ''}
                  ｜引用 {card.ref}
                </Text>
              </div>
            ),
          }))}
        />
      )}
    </div>
  )
}

// 模型输出是外部数据：只做元素映射与最小样式，不启用任何 HTML 直通
// （rehype-raw 之类一律不引），避免把注入面从「文本」扩成「DOM」。
const MD_STYLE = { background: 'rgba(127,127,127,0.12)', padding: '2px 5px', borderRadius: 4 } as const
const MARKDOWN_COMPONENTS: Components = {
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer">{children}</a>
  ),
  pre: ({ children }) => (
    <pre style={{ ...MD_STYLE, padding: 10, overflowX: 'auto', fontSize: 12 }}>{children}</pre>
  ),
  code: ({ children }) => (
    <code style={{ ...MD_STYLE, fontSize: 12 }}>{children}</code>
  ),
  table: ({ children }) => (
    <table style={{ borderCollapse: 'collapse', width: '100%', fontSize: 12 }}>{children}</table>
  ),
  th: ({ children }) => (
    <th style={{ border: '1px solid rgba(127,127,127,0.3)', padding: '4px 8px', textAlign: 'left' }}>{children}</th>
  ),
  td: ({ children }) => (
    <td style={{ border: '1px solid rgba(127,127,127,0.3)', padding: '4px 8px' }}>{children}</td>
  ),
}

function toTurn(message: LlmChatMessage): TurnView {
  return {
    key: `msg-${message.id}`,
    role: message.role === 'user' ? 'user' : 'assistant',
    content: message.content,
    error: message.errorCode ? `回合未完成：${message.errorCode}` : undefined,
  }
}
