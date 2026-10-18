import { useEffect, useRef, useState } from 'react'
import { Alert, Button, Card, Input, Progress, Select, Space, Switch, Typography } from 'antd'
import { Bubble, Conversations, Sender, Sources } from '@ant-design/x'
import { PlusOutlined } from '@ant-design/icons'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  archiveConversation, createConversation, getConversationMessages, listConversations,
  streamAssistantMessage,
  type LlmActionProposal, type LlmChatMessage, type LlmConversation, type LlmEvidenceCard, type LlmToolProgress,
} from '@/app/api/chat'
import {
  errorText, useCancelLlmBatchDecisionMutation, useGetLlmActiveApprovalsQuery, useGetManagedPositionsQuery,
  useSetLlmMarketTrackedMutation, useStartLlmBatchDecisionMutation, type ManagedPosition,
} from '@/app/api/gctApi'
import { TOKENS } from '@/theme'

const { Text, Title } = Typography

/** 期货持仓 → 运行市场号（与持仓决策页同一词汇表）。 */
const positionMarketID = (p: ManagedPosition): string => {
  const base = p.pair?.base ?? ''
  const quote = p.pair?.quote ?? ''
  if (!base || !quote) return ''
  return `Binance:usdtmarginedfutures:${base.toUpperCase()}_${quote.toUpperCase()}`
}

interface TurnView {
  key: string
  role: 'user' | 'assistant'
  content: string
  streaming?: boolean
  error?: string
  evidence?: LlmEvidenceCard[]
  proposals?: LlmActionProposal[]
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
  const [toolProgress, setToolProgress] = useState<LlmToolProgress>()
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
    setToolProgress(undefined)
    const controller = new AbortController()
    abortRef.current = controller
    try {
      const final = await streamAssistantMessage(conversationId, text, {
        signal: controller.signal,
        onProgress: setToolProgress,
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
                proposals: final.proposals,
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
      setToolProgress(undefined)
      abortRef.current = undefined
      // 侧栏展示每个会话的消息数：回合落定后刷新它，不让计数滞后于正文
      void refreshConversations()
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
          <Text type="secondary">只读分析 + 配置提案：模型只出草稿，确认卡片执行后才生效；不下单。</Text>
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
                {conversationId ? '还没有消息，向下方发送第一条分析请求。也可以直接让我筛合约，如“24h 涨幅大于 5%、4h 也上涨、MACD 为 BULL 的前 10 个合约”。' : '创建一个会话开始对话。'}
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

        {loading && toolProgress && <ToolProgressCard progress={toolProgress} />}

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
  const clean = turn.role === 'assistant'
    ? stripToolMarkup(turn.content)
    : { text: turn.content, stripped: false }
  return (
    <div>
      {turn.role === 'assistant' ? (
        <div>
          {clean.text ? (
            <ReactMarkdown remarkPlugins={[remarkGfm]} components={MARKDOWN_COMPONENTS}>
              {clean.text}
            </ReactMarkdown>
          ) : (
            <Text type="secondary" style={{ fontSize: 12 }}>
              {turn.streaming
                ? '正在调用工具并整理结果…'
                : clean.stripped
                  ? '（本回合的工具调用标记已隐藏，模型未返回正文；可让它「继续」或用更明确的条件重问）'
                  : '（本回合没有文本回复）'}
            </Text>
          )}
        </div>
      ) : (
        <div style={{ whiteSpace: 'pre-wrap' }}>{turn.content}</div>
      )}
      {turn.streaming && clean.text && <Text type="secondary" style={{ fontSize: 12 }}>（生成中）</Text>}
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
                <span>{toolLabel(card.tool)}｜{shortMarketID(card.marketId) || card.ref}</span>
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
      {turn.proposals && turn.proposals.length > 0 && (
        <Space direction="vertical" style={{ width: '100%', marginTop: 8 }} size={8}>
          {turn.proposals.map((proposal, index) => (
            <ProposalCard key={`${turn.key}-proposal-${index}`} proposal={proposal} />
          ))}
        </Space>
      )}
    </div>
  )
}

const PROPOSAL_LABELS: Record<string, string> = {
  START_OPEN_DECISION_BATCH: '批量开仓决策',
  START_POSITION_REVIEW: '持仓复核',
  SET_MARKET_TRACKED: '合约启用/停用',
  CANCEL_BATCH_JOB: '取消批量作业',
}

// 模型偶发把工具调用写成 DSML 文本（引擎会把完整调用转回工具调用并继续执行；
// 这里是气泡层的防御性清理：原始标记永不展示，包括流式途中的半截标记）。
const DSML_TAG = /<\s*[｜|]{1,2}\s*DSML\s*[｜|]{1,2}/i

function stripToolMarkup(text: string): { text: string; stripped: boolean } {
  if (!DSML_TAG.test(text)) return { text: text.trim(), stripped: false }
  let out = text.replace(
    /<\s*[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*calls\s*>[\s\S]*?<\s*\/\s*[｜|]{1,2}\s*DSML\s*[｜|]{1,2}\s*calls\s*>/g,
    '',
  )
  out = out.replace(/<\s*\/?\s*[｜|]{1,2}\s*DSML\s*[｜|]{1,2}[^>]*>/g, '')
  const cut = out.search(DSML_TAG)
  if (cut >= 0) out = out.slice(0, cut)
  return { text: out.trim(), stripped: true }
}

const TOOL_LABELS: Record<string, string> = {  listMarkets: '合约列表',
  getCandleWindow: 'K 线窗口',
  getIndicators: '技术指标',
  getOrderBookSummary: '盘口摘要',
  getFunding: '资金费率',
  getAccountState: '账户与持仓',
  getStrategyHistory: '策略历史',
  getRiskBudget: '风险预算',
  screenMarkets: '全市场筛选',
  proposeAction: '配置提案',
  assessOpeningOpportunities: 'Jev 开盘机会评估',
}

const toolLabel = (tool: string): string => (TOOL_LABELS[tool] ? `${TOOL_LABELS[tool]}（${tool}）` : tool)

/** 证据卡只显示合约名（去掉 <exchange>:<asset>: 前缀）；引用里保留全 id。 */
const shortMarketID = (marketID?: string): string => {
  if (!marketID) return ''
  const parts = marketID.split(':')
  return parts.length === 3 ? (parts[2] ?? marketID) : marketID
}

const PHASE_LABELS: Record<string, string> = {
  START: '调用工具',
  UNIVERSE: '解析扫描范围',
  CANDLES: '扫描 K 线',
  FLOW: '读取资金流（T2）',
}

/**
 * 工具进度卡（全量扫描）：让长任务的等待有预期。进度帧与正文增量并列，
 * 不进入正文；回合结束即消失。
 */
function ToolProgressCard({ progress }: { progress: LlmToolProgress }) {
  const total = progress.total ?? 0
  const done = progress.done ?? 0
  const percent = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0
  return (
    <Card size="small" style={{ borderColor: TOKENS.focus }}>
      <Space direction="vertical" style={{ width: '100%' }} size={4}>
        <Space size={8} wrap>
          <Text strong>{progress.message || '正在执行…'}</Text>
          {total > 0 && <Text type="secondary" style={{ fontSize: 12 }}>{done}/{total}</Text>}
        </Space>
        {total > 0 && <Progress percent={percent} status="active" size="small" showInfo={false} />}
        <Text type="secondary" style={{ fontSize: 12 }}>
          {toolLabel(progress.tool ?? '')}
          {progress.phase ? ` · ${PHASE_LABELS[progress.phase] ?? progress.phase}` : ''}
          {' · 只读扫描，不会下单'}
        </Text>
      </Space>
    </Card>
  )
}

const splitMarkets = (raw: string): string[] =>
  raw.split(/[\n,，]/).map((s) => s.trim()).filter(Boolean)

/**
 * 配置提案确认卡（docs/llm-dev 16）：模型只产出草稿，卡片上的「确认执行」才是
 * 唯一写入口，且调用与手动页面完全相同的治理 RPC。字段可就地编辑，与手动线路
 * 保持 1:1；取消只丢弃这张草稿，不产生任何副作用。
 */
function ProposalCard({ proposal }: { proposal: LlmActionProposal }) {
  const [markets, setMarkets] = useState((proposal.marketIds ?? []).join('\n'))
  const [version, setVersion] = useState(proposal.strategyVersionId ?? '')
  const [actor, setActor] = useState(proposal.actor || 'webui-operator')
  const [marketID, setMarketID] = useState(proposal.marketId ?? '')
  const [tracked, setTracked] = useState(proposal.tracked ?? true)
  const [jobID, setJobID] = useState(proposal.jobId ?? '')
  const [busy, setBusy] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; text: string }>()

  const approvals = useGetLlmActiveApprovalsQuery()
  const positions = useGetManagedPositionsQuery(undefined, { skip: !proposal.allPositions })
  const [startBatch] = useStartLlmBatchDecisionMutation()
  const [setMarketTracked] = useSetLlmMarketTrackedMutation()
  const [cancelBatch] = useCancelLlmBatchDecisionMutation()

  if (dismissed) {
    return (
      <Text type="secondary" style={{ fontSize: 12 }}>
        已忽略配置提案：{PROPOSAL_LABELS[proposal.action] ?? proposal.action}
      </Text>
    )
  }

  const isBatch = proposal.action === 'START_OPEN_DECISION_BATCH' || proposal.action === 'START_POSITION_REVIEW'

  const onConfirm = async () => {
    setBusy(true)
    setResult(undefined)
    try {
      switch (proposal.action) {
        case 'START_OPEN_DECISION_BATCH':
        case 'START_POSITION_REVIEW': {
          let ids = splitMarkets(markets)
          if (proposal.action === 'START_POSITION_REVIEW' && proposal.allPositions) {
            ids = (positions.data?.positions ?? []).map(positionMarketID).filter(Boolean)
          }
          if (!version.trim()) throw new Error('请填写策略版本 id（该版本必须处于活跃审批中）')
          if (ids.length === 0) throw new Error('没有可执行的市场')
          const kind = proposal.action === 'START_OPEN_DECISION_BATCH' ? 'OPEN_DECISION' : 'POSITION_REVIEW'
          const res = await startBatch({
            strategyVersionId: version.trim(), marketIds: ids, kind, actor: actor.trim() || 'webui-operator',
          }).unwrap()
          setResult({ ok: true, text: `作业已启动：${res.jobId}（到${kind === 'POSITION_REVIEW' ? '「持仓决策」' : '「筛选与开仓决策」'}页查看进度与结果）` })
          break
        }
        case 'SET_MARKET_TRACKED': {
          if (!marketID.trim()) throw new Error('市场 id 不能为空')
          const res = await setMarketTracked({
            marketId: marketID.trim(), tracked, actor: actor.trim() || 'webui-operator',
          }).unwrap()
          setResult({ ok: true, text: `已${res.tracked ? '启用' : '停用'} ${res.marketId}` })
          break
        }
        case 'CANCEL_BATCH_JOB': {
          if (!jobID.trim()) throw new Error('作业 id 不能为空')
          const res = await cancelBatch({ jobId: jobID.trim(), actor: actor.trim() || 'webui-operator' }).unwrap()
          setResult({ ok: true, text: `作业 ${res.jobId} 已取消（${res.cancelled} 个待跑市场转为 CANCELLED）` })
          break
        }
        default:
          throw new Error(`未知动作 ${proposal.action}`)
      }
    } catch (err) {
      setResult({ ok: false, text: errorText(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card size="small" title={`配置提案 · ${PROPOSAL_LABELS[proposal.action] ?? proposal.action}`}>
      <Space direction="vertical" style={{ width: '100%' }} size={8}>
        <Text>{proposal.summary}</Text>
        {isBatch && (
          <>
            {proposal.action === 'START_POSITION_REVIEW' && proposal.allPositions ? (
              <Text type="secondary">将复核全部当前持仓（确认时按最新持仓解析市场列表）。</Text>
            ) : (
              <Input.TextArea
                rows={2} value={markets} onChange={(e) => setMarkets(e.target.value)}
                placeholder="市场 id，每行一个，例如 Binance:usdtmarginedfutures:BTC_USDT"
              />
            )}
            {(approvals.data?.approvals?.length ?? 0) > 0 ? (
              <Select
                style={{ width: '100%' }} value={version || undefined} onChange={setVersion}
                placeholder="选择活跃审批版本"
                options={(approvals.data?.approvals ?? []).map((a) => ({
                  value: a.strategyVersionId ?? a.id,
                  label: `${a.strategyVersionId ?? a.id}（${a.executionMode ?? '—'} · epoch ${a.pairEpoch ?? '—'}）`,
                }))}
              />
            ) : (
              <Input value={version} onChange={(e) => setVersion(e.target.value)} placeholder="策略版本 id（需处于活跃审批）" />
            )}
          </>
        )}
        {proposal.action === 'SET_MARKET_TRACKED' && (
          <Space size={8} wrap>
            <Input value={marketID} onChange={(e) => setMarketID(e.target.value)} style={{ width: 320 }} placeholder="市场 id" />
            <Space size={4}>
              <Switch checked={tracked} onChange={setTracked} checkedChildren="启用" unCheckedChildren="停用" />
            </Space>
          </Space>
        )}
        {proposal.action === 'CANCEL_BATCH_JOB' && (
          <Input value={jobID} onChange={(e) => setJobID(e.target.value)} placeholder="作业 id" />
        )}
        <Space size={8} wrap>
          <Input value={actor} onChange={(e) => setActor(e.target.value)} style={{ width: 180 }} placeholder="操作者" />
          <Button type="primary" size="small" loading={busy} onClick={() => void onConfirm()}>确认执行</Button>
          <Button size="small" onClick={() => setDismissed(true)}>忽略</Button>
          <Text type="secondary" style={{ fontSize: 12 }}>提案不会自动执行；确认后走与手动页面相同的端点与审批绑定。</Text>
        </Space>
        {result && (
          <Alert type={result.ok ? 'success' : 'error'} showIcon message={result.text} />
        )}
      </Space>
    </Card>
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
    content: message.role === 'user' ? message.content : stripToolMarkup(message.content).text,
    error: message.errorCode ? `回合未完成：${message.errorCode}` : undefined,
  }
}
