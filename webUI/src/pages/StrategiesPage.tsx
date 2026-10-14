import {
  Alert, Button, Card, Col, Descriptions, Drawer, Empty, Form, Input, InputNumber,
  Modal, Row, Select, Space, Switch, Table, Tag, Typography, message,
} from 'antd'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  useApproveLlmStrategyVersionMutation, useCreateLlmStrategyMutation,
  useExtractLlmPolicyMutation, useFreezeLlmStrategyVersionMutation,
  useGetActiveModelPairQuery, useGetLlmStrategiesQuery, useGetLlmStrategyApprovalQuery,
  useGetLlmMarketGrantsQuery, useGetLlmStrategyVersionQuery, useGrantLlmMarketMutation,
  useLazyDiffLlmStrategyVersionsQuery, useListLlmStrategyVersionsQuery,
  usePauseLlmStrategyMutation, useResumeLlmStrategyMutation, useRevokeLlmApprovalMutation,
  useRevokeLlmMarketGrantMutation, useSaveLlmStrategyDraftMutation, errorText,
} from '@/app/api/gctApi'
import type {
  LlmExtractPolicyResponse, LlmMarketGrant, LlmPolicyChange, LlmPolicyProposal,
  LlmRiskEnvelope, LlmStrategy, LlmStrategyVersion,
} from '@/app/api/types'
import { listConversations, type LlmConversation } from '@/app/api/chat'
import { NotImplemented } from '@/components/NotImplemented'

const { Paragraph, Text } = Typography

const STEP_TAG: Record<string, string> = {
  DRAFT: 'default', FROZEN: 'blue', PAUSED: 'orange', RUNNING: 'green',
  EXPIRED: 'default', REVOKED: 'red',
}
const PROPOSAL_TAG: Record<string, string> = {
  NEEDS_CLARIFICATION: 'orange', READY_FOR_REVIEW: 'green',
}

function parseJson<T>(raw?: string | null): T | null {
  if (!raw) return null
  try { return JSON.parse(raw) as T } catch { return null }
}

const prettyJson = (raw?: string) => {
  const parsed = parseJson<unknown>(raw)
  return parsed ? JSON.stringify(parsed, null, 2) : (raw ?? '')
}

const shortId = (id?: string) => (id ? id.slice(0, 18) + (id.length > 18 ? '…' : '') : '—')

/** 审批倒计时（08 §8 必留 UI 规则：审批倒计时） */
function useCountdownTick(active: boolean) {
  const [, setTick] = useState(0)
  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => setTick((v) => v + 1), 1000)
    return () => clearInterval(timer)
  }, [active])
}

function countdownText(until?: string): { text: string; expired: boolean } {
  if (!until) return { text: '—', expired: false }
  const ms = new Date(until).getTime() - Date.now()
  if (Number.isNaN(ms)) return { text: '—', expired: false }
  if (ms <= 0) return { text: '已过期', expired: true }
  const hours = Math.floor(ms / 3600000)
  const minutes = Math.floor((ms % 3600000) / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  return { text: `${hours}h ${minutes}m ${seconds}s`, expired: false }
}

/** 策略工作区（W5）。生命周期依据：docs/llm-dev 07 §1；读路径直连仓储，写路径经服务。 */
export default function StrategiesPage() {
  const [includeArchived, setIncludeArchived] = useState(false)
  const { data: list, error } = useGetLlmStrategiesQuery({ includeArchived }, { pollingInterval: 30_000 })
  const strategies = useMemo(() => list?.strategies ?? [], [list])
  const [selectedStrategyId, setSelectedStrategyId] = useState<string>()
  const selected = strategies.find((s) => s.id === selectedStrategyId) ?? strategies[0]

  const { data: versionList, isLoading: versionsLoading } = useListLlmStrategyVersionsQuery(selected?.id ?? '', { skip: !selected?.id, pollingInterval: 30_000 })
  const versions = versionList?.versions ?? []
  const [selectedVersionId, setSelectedVersionId] = useState<string>()
  const selectedVersion = versions.find((v) => v.id === selectedVersionId) ?? versions[versions.length - 1]

  // 弹窗状态
  const [createOpen, setCreateOpen] = useState(false)
  const [extractOpen, setExtractOpen] = useState(false)
  const [diffOpen, setDiffOpen] = useState(false)
  const [freezeOpen, setFreezeOpen] = useState(false)
  const [approveOpen, setApproveOpen] = useState(false)
  const [detailOpen, setDetailOpen] = useState(false)

  const [createForm] = Form.useForm()
  const [createStrategy, createState] = useCreateLlmStrategyMutation()

  const openExtract = useCallback(() => setExtractOpen(true), [])

  return (
    <Row gutter={[12, 12]}>
      <Col span={24}>
        <Alert
          type="info" showIcon
          message="策略生命周期（Q1/Q6 定案：逐版限时审批）"
          description={
            <Paragraph style={{ marginBottom: 0 }}>
              用户逐版、限时审批策略；授权期内，符合范围和硬风控的订单自动执行；越权、约束变化或模型不确定时进入人工复核。
              <b>可交易市场须逐个审批，市场发现本身不授予交易权限。</b>
              审批默认 <Tag>4 小时</Tag>（上限 7 天），窗口内新增风险意图默认 <Tag>2 个 / 300min</Tag>（硬上限 5）。
              草稿仅存服务端版本行——<b>「保存为草案」永远追加新版本，没有就地图改的入口</b>。
            </Paragraph>
          }
        />
      </Col>

      {error ? <Col span={24}><NotImplemented title="策略列表" error={error} /></Col> : null}

      <Col xs={24} lg={10}>
        <Card
          size="small"
          title={<span className="gct-panel-title">策略</span>}
          extra={
            <Space size={8}>
              <Switch
                size="small" checked={includeArchived} onChange={setIncludeArchived}
                checkedChildren="含归档" unCheckedChildren="含归档"
              />
              <Button size="small" type="primary" onClick={() => setCreateOpen(true)}>新建策略</Button>
            </Space>
          }
        >
          <Table
            size="small" rowKey="id" pagination={false} scroll={{ x: 420 }}
            dataSource={strategies}
            loading={!list && !error}
            rowClassName={(r: LlmStrategy) => (r.id === selected?.id ? 'ant-table-row-selected' : '')}
            onRow={(r: LlmStrategy) => ({ onClick: () => { setSelectedStrategyId(r.id); setSelectedVersionId(undefined); setDetailOpen(false) } })}
            columns={[
              { title: '名称', dataIndex: 'name', render: (v: string, r: LlmStrategy) => (<Space size={6}><span>{v}</span>{r.archived ? <Tag>已归档</Tag> : null}</Space>) },
              { title: '建立人', dataIndex: 'createdBy', width: 110, render: (v?: string) => v ?? '—' },
              { title: '建立时间', dataIndex: 'createdAt', width: 165, render: (v?: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v ? v.replace('T', ' ').slice(0, 19) : '—'}</span> },
            ]}
          />
        </Card>
      </Col>

      <Col xs={24} lg={14}>
        <Card
          size="small"
          title={<span className="gct-panel-title">版本{selected ? ` · ${selected.name}` : ''}</span>}
          extra={selected ? (
            <Space size={8}>
              <Button size="small" onClick={openExtract}>从对话提取</Button>
              <Button size="small" onClick={() => setDiffOpen(true)} disabled={versions.filter((v) => v.frozenAt).length < 2}>版本 diff</Button>
            </Space>
          ) : null}
        >
          {!selected ? (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">先建立或选择一个策略</span>} />
          ) : (
            <Table
              size="small" rowKey="id" pagination={false} loading={versionsLoading}
              dataSource={versions}
              scroll={{ x: 620 }}
              onRow={(r: LlmStrategyVersion) => ({ onClick: () => { setSelectedVersionId(r.id); setDetailOpen(true) } })}
              columns={[
                {
                  title: '版本', dataIndex: 'id', width: 170,
                  render: (v: string, r: LlmStrategyVersion) => (
                    <Space size={6}>
                      <span className="gct-mono" style={{ fontSize: 11 }}>{shortId(v)}</span>
                      {r.parentVersionId ? <Tag style={{ marginInline: 0 }}>续</Tag> : <Tag color="blue" style={{ marginInline: 0 }}>根</Tag>}
                    </Space>
                  ),
                },
                { title: '状态', dataIndex: 'state', width: 96, render: (v: string) => <Tag color={STEP_TAG[v] ?? 'default'} style={{ marginInline: 0 }}>{v}</Tag> },
                { title: '提案', dataIndex: 'proposalState', width: 150, render: (v?: string) => v ? <Tag color={PROPOSAL_TAG[v] ?? 'default'} style={{ marginInline: 0 }}>{v}</Tag> : <span className="gct-dim">—</span> },
                { title: '创建', dataIndex: 'createdAt', width: 150, render: (v?: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v ? v.replace('T', ' ').slice(0, 19) : '—'}</span> },
                { title: '冻结', dataIndex: 'frozenAt', width: 150, render: (v?: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v ? v.replace('T', ' ').slice(0, 19) : '—'}</span> },
              ]}
            />
          )}
        </Card>
      </Col>

      <Col span={24}>
        <GrantsCard />
      </Col>

      <CreateStrategyModal
        open={createOpen} form={createForm} loading={createState.isLoading}
        onCancel={() => setCreateOpen(false)}
        onSubmit={async () => {
          const values = await createForm.validateFields()
          try {
            const res = await createStrategy({ name: values.name, actor: values.actor }).unwrap()
            message.success(`策略已建立：${res.strategy?.name ?? values.name}`)
            if (res.strategy?.id) setSelectedStrategyId(res.strategy.id)
            setCreateOpen(false)
            createForm.resetFields()
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      />

      {selected ? (
        <ExtractModal
          open={extractOpen} strategy={selected} onClose={() => setExtractOpen(false)}
          latestVersion={versions[versions.length - 1]}
        />
      ) : null}

      {selected ? (
        <VersionDetailDrawer
          open={detailOpen} version={selectedVersion} tree={versions}
          onClose={() => setDetailOpen(false)}
          onFreeze={() => setFreezeOpen(true)}
          onApprove={() => setApproveOpen(true)}
        />
      ) : null}

      {selectedVersion ? (
        <FreezeModal open={freezeOpen} version={selectedVersion} onClose={() => setFreezeOpen(false)} />
      ) : null}

      {selectedVersion ? (
        <ApproveModal open={approveOpen} version={selectedVersion} onClose={() => setApproveOpen(false)} />
      ) : null}

      {selected ? <DiffModal open={diffOpen} versions={versions} onClose={() => setDiffOpen(false)} /> : null}
    </Row>
  )
}

function CreateStrategyModal({ open, form, loading, onCancel, onSubmit }: {
  open: boolean
  form: ReturnType<typeof Form.useForm>[0]
  loading: boolean
  onCancel: () => void
  onSubmit: () => void
}) {
  return (
    <Modal title="新建策略" open={open} onCancel={onCancel} onOk={onSubmit} confirmLoading={loading} destroyOnHidden>
      <Form form={form} layout="vertical" initialValues={{ actor: 'webui-operator' }}>
        <Form.Item name="name" label="策略名称" rules={[{ required: true, message: '请输入名称' }]}>
          <Input placeholder="例如：BTC 动量跟随" maxLength={80} />
        </Form.Item>
        <Form.Item name="actor" label="操作者" rules={[{ required: true, message: '请输入操作者' }]}>
          <Input placeholder="webui-operator" maxLength={64} />
        </Form.Item>
        <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>
          建立策略只写一行骨架 + 一条 <code>STRATEGY_CREATED</code> 审计；条款内容通过「从对话提取」产出。
        </Paragraph>
      </Form>
    </Modal>
  )
}

function ExtractModal({ open, strategy, latestVersion, onClose }: {
  open: boolean
  strategy: LlmStrategy
  latestVersion?: LlmStrategyVersion
  onClose: () => void
}) {
  const [conversations, setConversations] = useState<LlmConversation[]>([])
  const [conversationId, setConversationId] = useState<string>()
  const [markets, setMarkets] = useState('Binance:usdtmarginedfutures:BTC_USDT')
  const [actor, setActor] = useState('webui-operator')
  const [result, setResult] = useState<LlmExtractPolicyResponse>()
  const { data: pair } = useGetActiveModelPairQuery()
  const [extract, extractState] = useExtractLlmPolicyMutation()
  const [saveDraft, saveState] = useSaveLlmStrategyDraftMutation()

  useEffect(() => {
    if (!open) return
    listConversations(true).then(setConversations).catch(() => setConversations([]))
  }, [open])

  const proposal = parseJson<LlmPolicyProposal>(result?.proposalJson)
  const needsClarification = result?.state === 'NEEDS_CLARIFICATION'

  return (
    <Modal
      title={`从对话提取条款 · ${strategy.name}`} open={open} onCancel={onClose} width={720}
      footer={[
        <Button key="close" onClick={onClose}>关闭</Button>,
        <Button
          key="run" type="primary" loading={extractState.isLoading}
          disabled={!conversationId || !pair?.llmProfileRevisionId}
          onClick={async () => {
            try {
              const res = await extract({
                conversationId: conversationId ?? '',
                profileRevisionId: pair?.llmProfileRevisionId ?? '',
                markets: markets.split(',').map((m) => m.trim()).filter(Boolean),
              }).unwrap()
              setResult(res)
            } catch (err) {
              message.error(errorText(err))
            }
          }}
        >
          运行提取
        </Button>,
        <Button
          key="save" type="primary" ghost loading={saveState.isLoading} disabled={!result?.proposalJson}
          onClick={async () => {
            try {
              const res = await saveDraft({
                strategyId: strategy.id,
                parentVersionId: latestVersion?.id,
                proposalJson: result?.proposalJson ?? '',
                actor,
              }).unwrap()
              message.success(`已保存为草案版本 ${shortId(res.version?.id)}（${res.version?.proposalState}）`)
              setResult(undefined)
              onClose()
            } catch (err) {
              message.error(errorText(err))
            }
          }}
        >
          保存为草案
        </Button>,
      ]}
    >
      <Space direction="vertical" style={{ width: '100%' }} size={10}>
        <Space size={8} wrap>
          <Select
            style={{ width: 300 }} placeholder="选择一段对话" value={conversationId} onChange={setConversationId}
            options={conversations.map((c) => ({ value: c.id, label: `${c.title} · ${c.messageCount} 条` }))}
            notFoundContent={<span className="gct-dim">暂无对话（先去「对话」页与模型讨论策略）</span>}
          />
          <Input style={{ width: 260 }} value={actor} onChange={(e) => setActor(e.target.value)} placeholder="操作者" />
        </Space>
        <Input
          addonBefore="市场" value={markets} onChange={(e) => setMarkets(e.target.value)}
          placeholder="Binance:usdtmarginedfutures:BTC_USDT,Binance:usdtmarginedfutures:ETH_USDT"
        />
        <Paragraph className="gct-dim" style={{ fontSize: 11, marginBottom: 0 }}>
          提取使用活化配对的 LLM 修订版（<span className="gct-mono">{shortId(pair?.llmProfileRevisionId)}</span>）；
          提取本身<b>不持久化</b>，审阅后「保存为草案」才成为版本行。缺硬边界时产出澄清问题，<b>不替你填值</b>。
        </Paragraph>
        {result ? (
          <>
            <Alert
              type={needsClarification ? 'warning' : 'success'} showIcon
              message={needsClarification ? 'NEEDS_CLARIFICATION：有硬边界未给出' : 'READY_FOR_REVIEW：可以保存为草案'}
              description={
                needsClarification ? (
                  <div style={{ fontSize: 12 }}>
                    <div>请回到对话把缺失的信息补全后重新提取（界面不提供手工改值入口）：</div>
                    <ul style={{ margin: '6px 0 0', paddingInlineStart: 18 }}>
                      {(result.clarifyingQuestions ?? []).map((q) => <li key={q}>{q}</li>)}
                    </ul>
                  </div>
                ) : null
              }
            />
            {proposal ? (
              <Descriptions size="small" column={1} bordered>
                <Descriptions.Item label="目标">{proposal.objective ?? '—'}</Descriptions.Item>
                <Descriptions.Item label="市场范围">
                  <Space size={6} wrap>
                    {(proposal.marketScopes ?? []).map((s) => <Tag key={s.marketId} className="gct-mono">{s.marketId}</Tag>)}
                  </Space>
                </Descriptions.Item>
                <Descriptions.Item label="有效期（分钟）">{proposal.validMinutes ?? <Text type="warning">未设</Text>}</Descriptions.Item>
                <Descriptions.Item label="日损上限">{proposal.risk?.maxDailyLossUsd ?? <Text type="warning">未设</Text>}</Descriptions.Item>
                <Descriptions.Item label="退出策略">{proposal.exitPolicy ?? <Text type="warning">未设</Text>}</Descriptions.Item>
                <Descriptions.Item label="用量">
                  <span className="gct-mono">{result.inputTokens ?? '—'} in / {result.outputTokens ?? '—'} out · {result.latencyMs ?? '—'}ms</span>
                </Descriptions.Item>
              </Descriptions>
            ) : null}
          </>
        ) : null}
      </Space>
    </Modal>
  )
}

function VersionDetailDrawer({ open, version, tree, onClose, onFreeze, onApprove }: {
  open: boolean
  version?: LlmStrategyVersion
  tree: LlmStrategyVersion[]
  onClose: () => void
  onFreeze: () => void
  onApprove: () => void
}) {
  const { data: detail } = useGetLlmStrategyVersionQuery(version?.id ?? '', { skip: !version?.id || !open })
  const { data: approvalRes } = useGetLlmStrategyApprovalQuery(version?.id ?? '', { skip: !version?.id || !open || !version?.frozenAt })
  const approval = approvalRes?.found ? approvalRes.approval : undefined
  const [pause, pauseState] = usePauseLlmStrategyMutation()
  const [resume, resumeState] = useResumeLlmStrategyMutation()
  const [revoke, revokeState] = useRevokeLlmApprovalMutation()
  const [revokeOpen, setRevokeOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [actor, setActor] = useState('webui-operator')
  useCountdownTick(open && Boolean(approval?.validUntil) && !approval?.revokedAt)

  if (!version) return null
  const policy = parseJson<LlmPolicyProposal>(detail?.policyJson)
  const isFrozen = Boolean(version.frozenAt)
  const reviewable = isFrozen
    ? parseJson<{
        accountId?: string; network?: string; executionMode?: string
        validFrom?: string; validUntil?: string; pairEpoch?: number
        llmRevisionId?: string; jevRevisionId?: string; tradeAuthorized?: boolean
        marketScopes?: Array<{ marketId?: string; actions?: string[] }>
        risk?: LlmRiskEnvelope
      }>(detail?.policyJson)
    : null
  const liveApproval = approval && !approval.revokedAt && (!approval.validUntil || new Date(approval.validUntil).getTime() > Date.now())
  const countdown = countdownText(approval?.validUntil)
  const canFreeze = !isFrozen && version.proposalState === 'READY_FOR_REVIEW'
  const canApprove = isFrozen && !liveApproval

  const runState = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      message.success(ok)
    } catch (err) {
      message.error(errorText(err))
    }
  }

  return (
    <Drawer
      title={<Space size={8}>版本 <span className="gct-mono" style={{ fontSize: 12 }}>{version.id}</span><Tag color={STEP_TAG[version.state] ?? 'default'}>{version.state}</Tag>{version.proposalState ? <Tag color={PROPOSAL_TAG[version.proposalState] ?? 'default'}>{version.proposalState}</Tag> : null}</Space>}
      width={860} open={open} onClose={onClose}
      extra={
        <Space size={8}>
          {canFreeze ? <Button type="primary" onClick={onFreeze}>冻结（审阅确认）</Button> : null}
          {canApprove ? <Button type="primary" onClick={onApprove}>审批（限时）</Button> : null}
          {version.state === 'FROZEN' ? (
            <Button loading={pauseState.isLoading} onClick={() => runState(() => pause({ strategyVersionId: version.id, actor, reason: reason || 'operator pause' }).unwrap(), '已暂停')}>暂停</Button>
          ) : null}
          {version.state === 'PAUSED' ? (
            <Button loading={resumeState.isLoading} onClick={() => runState(() => resume({ strategyVersionId: version.id, actor, reason: reason || 'operator resume' }).unwrap(), '已恢复')}>恢复</Button>
          ) : null}
          {liveApproval ? <Button danger loading={revokeState.isLoading} onClick={() => setRevokeOpen(true)}>撤销审批</Button> : null}
        </Space>
      }
    >
      <Descriptions size="small" column={2} bordered style={{ marginBottom: 12 }}>
        <Descriptions.Item label="父版本">{version.parentVersionId ? <span className="gct-mono">{shortId(version.parentVersionId)}</span> : '（根）'}</Descriptions.Item>
        <Descriptions.Item label="创建时间">{version.createdAt?.replace('T', ' ').slice(0, 19) ?? '—'}</Descriptions.Item>
        <Descriptions.Item label="冻结时间">{version.frozenAt?.replace('T', ' ').slice(0, 19) ?? '—'}</Descriptions.Item>
        <Descriptions.Item label="树规模">{tree.length} 个版本</Descriptions.Item>
        {version.strategyHash ? <Descriptions.Item label="strategy_hash" span={2}><span className="gct-mono" style={{ fontSize: 11 }}>{version.strategyHash}</span></Descriptions.Item> : null}
        {version.riskEnvelopeHash ? <Descriptions.Item label="risk_envelope_hash" span={2}><span className="gct-mono" style={{ fontSize: 11 }}>{version.riskEnvelopeHash}</span></Descriptions.Item> : null}
      </Descriptions>

      {!isFrozen && version.proposalState === 'NEEDS_CLARIFICATION' ? (
        <Alert
          type="warning" showIcon style={{ marginBottom: 12 }}
          message="草案带未答澄清问题，不能冻结"
          description={
            <div style={{ fontSize: 12 }}>
              <div>硬边界缺位时系统提问而不静默选值；请补全对话后重新提取并保存新草案：</div>
              <ul style={{ margin: '6px 0 0', paddingInlineStart: 18 }}>
                {(parseJson<LlmPolicyProposal>(detail?.policyJson)?.clarifyingQuestions ?? []).map((q) => <li key={q}>{q}</li>)}
              </ul>
              <Link to="/chat" onClick={onClose}>前往「对话」页补齐 →</Link>
            </div>
          }
        />
      ) : null}

      {isFrozen && reviewable ? (
        <Descriptions size="small" column={1} bordered style={{ marginBottom: 12 }} title="冻结信封（reviewable-v1）">
          <Descriptions.Item label="绑定">
            {reviewable.accountId} · {reviewable.network} · {reviewable.executionMode} · epoch {reviewable.pairEpoch}
          </Descriptions.Item>
          <Descriptions.Item label="有效期">
            {reviewable.validFrom?.replace('T', ' ').slice(0, 19)} → {reviewable.validUntil?.replace('T', ' ').slice(0, 19)}
          </Descriptions.Item>
          <Descriptions.Item label="模型修订版">
            <span className="gct-mono" style={{ fontSize: 11 }}>LLM {shortId(reviewable.llmRevisionId)} · Jev {shortId(reviewable.jevRevisionId)}</span>
          </Descriptions.Item>
          <Descriptions.Item label="市场范围">
            <Space size={6} wrap>
              {(reviewable.marketScopes ?? []).map((s) => (
                <Tag key={s.marketId} className="gct-mono">{s.marketId}{s.actions?.length ? ` · ${s.actions.join('/')}` : ''}</Tag>
              ))}
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="风控信封">
            <Space size={10} wrap>
              <span>单笔 {reviewable.risk?.maxOrderNotionalUsd ?? '—'} USD</span>
              <span>总敞口 {reviewable.risk?.maxTotalExposureUsd ?? '—'} USD</span>
              <span>日损 {reviewable.risk?.maxDailyLossUsd ?? '—'} USD</span>
              <span>滑点 {reviewable.risk?.maxSlippageBps ?? '—'} bps</span>
              <span>杠杆 ≤{reviewable.risk?.maxLeverage ?? '—'}</span>
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="tradeAuthorized">
            {reviewable.tradeAuthorized ? <Tag color="red">true（不应出现）</Tag> : <Tag color="green">false</Tag>}
          </Descriptions.Item>
        </Descriptions>
      ) : policy ? (
        <Descriptions size="small" column={1} bordered style={{ marginBottom: 12 }} title="条款（提案视图）">
          <Descriptions.Item label="目标">{policy.objective ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="市场范围">
            <Space size={6} wrap>
              {(policy.marketScopes ?? []).map((s) => (
                <Tag key={s.marketId} className="gct-mono">
                  {s.marketId}{s.actions?.length ? ` · ${s.actions.join('/')}` : ''}
                </Tag>
              ))}
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="风控信封">
            <Space size={10} wrap>
              <span>单笔 {policy.risk?.maxOrderNotionalUsd ?? <Text type="warning">未设</Text>} USD</span>
              <span>总敞口 {policy.risk?.maxTotalExposureUsd ?? <Text type="warning">未设</Text>} USD</span>
              <span>日损 {policy.risk?.maxDailyLossUsd ?? <Text type="warning">未设</Text>} USD</span>
              <span>滑点 {policy.risk?.maxSlippageBps ?? <Text type="warning">未设</Text>} bps</span>
              <span>杠杆 ≤{policy.risk?.maxLeverage ?? <Text type="warning">未设</Text>}</span>
              <span>新增风险 {policy.risk?.maxNewRiskIntents ?? '—'} / {policy.risk?.riskWindowMinutes ?? '—'}min</span>
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="退出策略">{policy.exitPolicy ?? <Text type="warning">未设</Text>}</Descriptions.Item>
          <Descriptions.Item label="有效期">{policy.validMinutes ?? '—'} 分钟</Descriptions.Item>
        </Descriptions>
      ) : null}

      {isFrozen ? (
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="已冻结：reviewable-v1 不可变"
          description={<span style={{ fontSize: 12 }}>冻结把审阅确认过的文档与两个哈希钉进版本行；运行时每次决策前重算比对，任何路径改限额都会以 RISK_ENVELOPE_MISMATCH 暴露（07 §2.2）。</span>}
        />
      ) : null}

      {approval ? (
        <Descriptions size="small" column={2} bordered style={{ marginBottom: 12 }} title="审批（逐版限时）">
          <Descriptions.Item label="审批号"><span className="gct-mono">{approval.id}</span></Descriptions.Item>
          <Descriptions.Item label="状态">
            {approval.revokedAt ? <Tag color="red">已撤销 · {approval.revokedBy} · {approval.revokeReason}</Tag>
              : countdown.expired ? <Tag color="default">已过期</Tag> : <Tag color="green">有效</Tag>}
          </Descriptions.Item>
          <Descriptions.Item label="剩余时间">
            {approval.revokedAt ? '—' : <span className="gct-mono">{countdown.text}</span>}
          </Descriptions.Item>
          <Descriptions.Item label="有效期至">{approval.validUntil?.replace('T', ' ').slice(0, 19) ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="绑定">{approval.network} · {approval.executionMode} · epoch {approval.pairEpoch}</Descriptions.Item>
          <Descriptions.Item label="市场数">{(approval.approvedMarketIds ?? []).length}</Descriptions.Item>
          <Descriptions.Item label="审批人">{approval.approvedBy ?? '—'}（{approval.approvedAt?.replace('T', ' ').slice(0, 19) ?? '—'}）</Descriptions.Item>
        </Descriptions>
      ) : isFrozen ? (
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="此冻结版本尚无有效审批——自动决策不会调度它" />
      ) : null}

      {detail?.policyJson ? (
        <>
          <Text strong style={{ fontSize: 12 }}>原始文档（{isFrozen ? 'reviewable-v1 冻结件' : '提案 JSON'}）</Text>
          <pre className="gct-mono" style={{
            fontSize: 11, background: 'rgba(255,255,255,0.03)', padding: 10, borderRadius: 6,
            maxHeight: 320, overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
          }}>
            {prettyJson(detail.policyJson)}
          </pre>
        </>
      ) : null}

      <Modal
        title="撤销审批" open={revokeOpen} onCancel={() => setRevokeOpen(false)}
        onOk={() => {
          if (!approval) return
          runState(() => revoke({ id: approval.id, actor, reason: reason || 'operator revoke' }).unwrap(), '审批已撤销').then(() => setRevokeOpen(false))
        }}
        confirmLoading={revokeState.isLoading}
      >
        <Paragraph style={{ fontSize: 12 }}>
          撤销后该版本不会自动恢复——恢复需要重新走审批（重新认证）。配对切换会级联撤销全部有效审批。
        </Paragraph>
        <Input placeholder="撤销原因（必录进审计）" value={reason} onChange={(e) => setReason(e.target.value)} />
        <Input style={{ marginTop: 8 }} placeholder="操作者" value={actor} onChange={(e) => setActor(e.target.value)} />
      </Modal>
    </Drawer>
  )
}

function FreezeModal({ open, version, onClose }: { open: boolean; version: LlmStrategyVersion; onClose: () => void }) {
  const { data: detail } = useGetLlmStrategyVersionQuery(version.id, { skip: !open })
  const { data: pair } = useGetActiveModelPairQuery()
  const [freeze, freezeState] = useFreezeLlmStrategyVersionMutation()
  const [actor, setActor] = useState('webui-operator')
  const [accountId, setAccountId] = useState('binance-usdtm')
  const [network, setNetwork] = useState('binance-testnet')
  const [mode, setMode] = useState('SHADOW')
  const [validMinutes, setValidMinutes] = useState<number>(240)
  const proposal = parseJson<LlmPolicyProposal>(detail?.policyJson)

  useEffect(() => {
    if (proposal?.validMinutes) setValidMinutes(proposal.validMinutes)
  }, [proposal?.validMinutes])

  const composed = useMemo(() => {
    if (!proposal) return ''
    const now = new Date()
    return JSON.stringify({
      schemaVersion: 'reviewable-v1',
      state: 'READY_FOR_APPROVAL',
      sourceVersionId: version.id,
      accountId,
      network,
      executionMode: mode,
      marketScopes: proposal.marketScopes ?? [],
      validFrom: now.toISOString(),
      validUntil: new Date(now.getTime() + validMinutes * 60000).toISOString(),
      risk: proposal.risk,
      llmRevisionId: pair?.llmProfileRevisionId,
      jevRevisionId: pair?.jevProfileRevisionId,
      pairEpoch: pair?.pairEpoch ?? 0,
      tradeAuthorized: false,
    }, null, 2)
  }, [proposal, accountId, network, mode, validMinutes, pair, version.id])

  return (
    <Modal
      title="冻结（审阅确认）" open={open} onCancel={onClose} width={720}
      okText="确认冻结" confirmLoading={freezeState.isLoading}
      onOk={async () => {
        try {
          const res = await freeze({ id: version.id, actor, policyJson: composed }).unwrap()
          message.success(`已冻结 · strategy_hash ${res.strategyHash?.slice(0, 16)}…`)
          onClose()
        } catch (err) {
          message.error(errorText(err))
        }
      }}
    >
      <Space direction="vertical" style={{ width: '100%' }} size={10}>
        <Alert
          type="warning" showIcon
          message="冻结把下面这份 reviewable-v1 文档原样钉成不可变版本"
          description={<span style={{ fontSize: 12 }}>冻结后 tradeAuthorized 恒为 false；审批是另一件独立、经重新认证的事。配对代次（epoch {pair?.pairEpoch ?? '—'}）随文档钉住——配对切换后这份版本会因 STALE_PAIR 而失效。</span>}
        />
        <Space size={8} wrap>
          <Input style={{ width: 170 }} addonBefore="账号" value={accountId} onChange={(e) => setAccountId(e.target.value)} />
          <Select
            style={{ width: 170 }} value={network} onChange={setNetwork}
            options={[{ value: 'binance-testnet', label: 'binance-testnet' }, { value: 'binance-mainnet', label: 'binance-mainnet' }]}
          />
          <Select
            style={{ width: 130 }} value={mode} onChange={setMode}
            options={['SHADOW', 'PAPER', 'TESTNET'].map((v) => ({ value: v, label: v }))}
          />
          <InputNumber addonAfter="分钟" min={1} max={10080} value={validMinutes} onChange={(v) => setValidMinutes(Number(v ?? 240))} />
          <Input style={{ width: 150 }} addonBefore="操作者" value={actor} onChange={(e) => setActor(e.target.value)} />
        </Space>
        <pre className="gct-mono" style={{
          fontSize: 11, background: 'rgba(255,255,255,0.03)', padding: 10, borderRadius: 6,
          maxHeight: 300, overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all',
        }}>
          {composed}
        </pre>
      </Space>
    </Modal>
  )
}

function ApproveModal({ open, version, onClose }: { open: boolean; version: LlmStrategyVersion; onClose: () => void }) {
  const { data: detail } = useGetLlmStrategyVersionQuery(version.id, { skip: !open })
  const { data: pair } = useGetActiveModelPairQuery()
  const [approve, approveState] = useApproveLlmStrategyVersionMutation()
  const frozen = parseJson<Record<string, unknown>>(detail?.policyJson)
  const [actor, setActor] = useState('webui-operator')
  const [accountId, setAccountId] = useState('binance-usdtm')
  const [network, setNetwork] = useState('binance-testnet')
  const [mode, setMode] = useState('SHADOW')
  const [validMinutes, setValidMinutes] = useState<number>(240)
  const [password, setPassword] = useState('')
  const idempotencyKey = useMemo(() => crypto.randomUUID(), [open])

  useEffect(() => {
    if (!frozen) return
    if (typeof frozen.accountId === 'string') setAccountId(frozen.accountId)
    if (typeof frozen.network === 'string') setNetwork(frozen.network)
    if (typeof frozen.executionMode === 'string') setMode(frozen.executionMode)
    const from = Date.parse(String(frozen.validFrom ?? ''))
    const until = Date.parse(String(frozen.validUntil ?? ''))
    if (!Number.isNaN(from) && !Number.isNaN(until) && until > from) {
      setValidMinutes(Math.max(1, Math.min(10080, Math.round((until - from) / 60000))))
    }
  }, [frozen])

  const markets = useMemo(() => {
    const scopes = frozen?.marketScopes
    if (!Array.isArray(scopes)) return [] as string[]
    return scopes.map((s) => (s as { marketId?: string }).marketId).filter((m): m is string => Boolean(m))
  }, [frozen])

  return (
    <Modal
      title="审批（逐版限时）" open={open} onCancel={onClose} width={640}
      okText="重新认证并审批" confirmLoading={approveState.isLoading}
      onOk={async () => {
        try {
          const res = await approve({
            strategyVersionId: version.id,
            accountId, network, executionMode: mode,
            approvedMarketIds: markets,
            validMinutes,
            operatorPassword: password,
            idempotencyKey,
            expectedPairEpoch: pair?.pairEpoch ?? 0,
            actor,
          }).unwrap()
          message.success(res.idempotentReplay ? '审批已存在（幂等重放）' : `已审批 · 有效至 ${res.validUntil?.replace('T', ' ').slice(0, 19)}`)
          setPassword('')
          onClose()
        } catch (err) {
          message.error(errorText(err))
        }
      }}
    >
      <Space direction="vertical" style={{ width: '100%' }} size={10}>
        <Alert
          type="info" showIcon
          message="审批只是接受冻结信封——没有在此时改限额的输入面"
          description={<span style={{ fontSize: 12 }}>必须在审批窗口内：逐版、限时（≤7 天）。市场授权（W5-07）是第二道闸——授权缺失的市场即使被审批也不会放行。</span>}
        />
        <Descriptions size="small" column={1}>
          <Descriptions.Item label="绑定市场">
            <Space size={6} wrap>{(markets.length ? markets : ['（冻结文档未列出）']).map((m) => <Tag key={m} className="gct-mono">{m}</Tag>)}</Space>
          </Descriptions.Item>
        </Descriptions>
        <Space size={8} wrap>
          <Input style={{ width: 170 }} addonBefore="账号" value={accountId} onChange={(e) => setAccountId(e.target.value)} />
          <Select style={{ width: 170 }} value={network} onChange={setNetwork}
            options={[{ value: 'binance-testnet', label: 'binance-testnet' }, { value: 'binance-mainnet', label: 'binance-mainnet' }]} />
          <Select style={{ width: 130 }} value={mode} onChange={setMode}
            options={['SHADOW', 'PAPER', 'TESTNET', 'LIVE'].map((v) => ({ value: v, label: v }))} />
          <InputNumber addonAfter="分钟" min={1} max={10080} value={validMinutes} onChange={(v) => setValidMinutes(Number(v ?? 240))} />
        </Space>
        <Input.Password placeholder="操作者口令（重新认证，必填）" value={password} onChange={(e) => setPassword(e.target.value)} />
        <Space size={8} wrap>
          <Input style={{ width: 180 }} addonBefore="操作者" value={actor} onChange={(e) => setActor(e.target.value)} />
          <Text className="gct-dim" style={{ fontSize: 11 }}>expected_pair_epoch {pair?.pairEpoch ?? '—'} · 幂等键 {idempotencyKey.slice(0, 8)}…</Text>
        </Space>
      </Space>
    </Modal>
  )
}

function DiffModal({ open, versions, onClose }: {
  open: boolean
  versions: LlmStrategyVersion[]
  onClose: () => void
}) {
  const frozen = versions.filter((v) => v.frozenAt)
  const [fromId, setFromId] = useState<string>()
  const [toId, setToId] = useState<string>()
  const [run, { data, isFetching }] = useLazyDiffLlmStrategyVersionsQuery()
  const options = frozen.map((v) => ({ value: v.id, label: `${shortId(v.id)} · ${v.createdAt?.slice(0, 10)}` }))

  return (
    <Modal title="版本 diff（字段级）" open={open} onCancel={onClose} footer={null} width={760}>
      <Space size={8} style={{ marginBottom: 10 }} wrap>
        <Select style={{ width: 230 }} placeholder="旧版本" value={fromId} onChange={setFromId} options={options} />
        <span>→</span>
        <Select style={{ width: 230 }} placeholder="新版本" value={toId} onChange={setToId} options={options} />
        <Button type="primary" disabled={!fromId || !toId || fromId === toId} loading={isFetching} onClick={() => run({ fromId: fromId ?? '', toId: toId ?? '' })}>
          运行 diff
        </Button>
      </Space>
      {data ? (
        (data.changes ?? []).length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">两版逐字段一致</span>} />
        ) : (
          <Table
            size="small" rowKey={(r: LlmPolicyChange) => r.field} pagination={false}
            dataSource={data.changes}
            columns={[
              { title: '字段', dataIndex: 'field', width: 220, render: (v: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span> },
              { title: '旧值', dataIndex: 'from', render: (v?: string) => <span className="gct-dim">{v === '' ? '（空）' : v ?? '—'}</span> },
              { title: '新值', dataIndex: 'to', render: (v?: string) => <span style={{ color: '#5B9CFF' }}>{v === '' ? '（空）' : v ?? '—'}</span> },
            ]}
          />
        )
      ) : (
        <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>
          只有冻结版本可以 diff（草案没有可比对的稳定文档）；字段级差异按结构体声明序输出。
        </Paragraph>
      )}
    </Modal>
  )
}

/** 市场授权（W5-07）：第二道闸——授权独立于 enabled；撤销级联撤销含该市场的有效审批。 */
function GrantsCard() {
  const [accountId, setAccountId] = useState('binance-usdtm')
  const { data, error } = useGetLlmMarketGrantsQuery(accountId, { pollingInterval: 30_000 })
  const grants = data?.grants ?? []
  const [grantOpen, setGrantOpen] = useState(false)
  const [grant, grantState] = useGrantLlmMarketMutation()
  const [revoke, revokeState] = useRevokeLlmMarketGrantMutation()
  const [revokeTarget, setRevokeTarget] = useState<LlmMarketGrant>()
  const [revokeReason, setRevokeReason] = useState('')
  const [form] = Form.useForm()

  const live = (g: LlmMarketGrant) => !g.revokedAt && Boolean(g.validUntil) && new Date(g.validUntil ?? 0).getTime() > Date.now()

  return (
    <Card
      size="small"
      title={<span className="gct-panel-title">市场授权（第二道闸：看盘 ≠ 能交易）</span>}
      extra={
        <Space size={8}>
          <Input size="small" addonBefore="账号" style={{ width: 250 }} value={accountId} onChange={(e) => setAccountId(e.target.value)} />
          <Button size="small" type="primary" onClick={() => { form.setFieldsValue({ accountId }); setGrantOpen(true) }}>授权市场</Button>
        </Space>
      }
    >
      {error ? <NotImplemented title="市场授权" error={error} /> : (
        <Table
          size="small" rowKey="id" pagination={false} loading={!data && !error} dataSource={grants}
          scroll={{ x: 720 }}
          columns={[
            { title: '市场', dataIndex: 'marketId', render: (v: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span> },
            { title: '网络', dataIndex: 'network', width: 150 },
            {
              title: '窗口', dataIndex: 'validUntil', width: 230,
              render: (v: string, r: LlmMarketGrant) => r.revokedAt
                ? <Tag color="red" style={{ marginInline: 0 }}>已撤销 · {r.revokedBy ?? '—'}</Tag>
                : new Date(v).getTime() <= Date.now()
                  ? <Tag style={{ marginInline: 0 }}>已过期</Tag>
                  : <span className="gct-mono" style={{ fontSize: 11 }}>{v.replace('T', ' ').slice(0, 19)} 前有效</span>,
            },
            { title: '授予人', dataIndex: 'grantedBy', width: 110, render: (v?: string) => v ?? '—' },
            {
              title: '操作', width: 90,
              render: (_: unknown, r: LlmMarketGrant) => live(r)
                ? <Button size="small" danger onClick={() => { setRevokeTarget(r); setRevokeReason('') }}>撤销</Button>
                : <span className="gct-dim">—</span>,
            },
          ]}
        />
      )}
      <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
        授权逐市场、最长 <b>30 天</b>、活体唯一（同账号+网络+市场至多一条有效）。<b>撤销会级联撤销所有引用该市场的有效审批</b>——
        授权不在，则批准也不在；相关策略的暂停与挂单处置属运行面。
      </Paragraph>

      <Modal
        title="授权市场" open={grantOpen} onCancel={() => setGrantOpen(false)} confirmLoading={grantState.isLoading}
        onOk={async () => {
          const values = await form.validateFields()
          try {
            const res = await grant(values).unwrap()
            message.success(`已授权 ${res.grant?.marketId}（至 ${res.grant?.validUntil?.slice(0, 10)}）`)
            setGrantOpen(false)
            form.resetFields()
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      >
        <Form form={form} layout="vertical" initialValues={{ accountId, network: 'binance-testnet', validDays: 7, grantedBy: 'webui-operator' }}>
          <Form.Item name="accountId" label="账号" rules={[{ required: true, message: '请输入账号' }]}><Input /></Form.Item>
          <Form.Item name="network" label="网络" rules={[{ required: true, message: '请选择网络' }]}>
            <Select options={[{ value: 'binance-testnet', label: 'binance-testnet' }, { value: 'binance-mainnet', label: 'binance-mainnet' }]} />
          </Form.Item>
          <Form.Item name="marketId" label="市场" rules={[{ required: true, message: '请输入市场 id' }]}>
            <Input placeholder="Binance:usdtmarginedfutures:BTC_USDT" />
          </Form.Item>
          <Form.Item name="validDays" label="有效期（天，1..30）" rules={[{ required: true, message: '请输入天数' }]}>
            <InputNumber min={1} max={30} style={{ width: '100%' }} />
          </Form.Item>
          <Form.Item name="grantedBy" label="授予人" rules={[{ required: true, message: '请输入授予人' }]}><Input /></Form.Item>
        </Form>
      </Modal>

      <Modal
        title="撤销授权（级联）" open={Boolean(revokeTarget)} onCancel={() => setRevokeTarget(undefined)}
        confirmLoading={revokeState.isLoading}
        onOk={async () => {
          try {
            const res = await revoke({ id: revokeTarget?.id ?? '', actor: 'webui-operator', reason: revokeReason || 'operator revoke' }).unwrap()
            const count = res.revokedApprovalIds?.length ?? 0
            message.success(count > 0 ? `已撤销授权，级联撤销 ${count} 个审批` : '已撤销授权（无级联审批）')
            setRevokeTarget(undefined)
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      >
        <Paragraph style={{ fontSize: 12 }}>
          撤销 <span className="gct-mono">{revokeTarget?.marketId}</span> 会
          <b>级联撤销所有引用该市场的有效审批</b>——这是「授权不在，则批准也不在」的因果点。
        </Paragraph>
        <Input placeholder="撤销原因（进审计）" value={revokeReason} onChange={(e) => setRevokeReason(e.target.value)} />
      </Modal>
    </Card>
  )
}
