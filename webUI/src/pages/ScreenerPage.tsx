import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Alert, Button, Card, Col, Divider, Empty, Input, InputNumber, Row, Select, Space, Switch, Table, Tag, Typography, message } from 'antd'
import {
  errorText, isNotImplemented,
  useCancelLlmBatchDecisionMutation, useGetLlmActiveApprovalsQuery, useGetLlmBatchDecisionQuery, useGetLlmScreenerMetricsQuery,
  useRunLlmScreenMutation, useStartLlmBatchDecisionMutation,
  type LlmBatchDecisionRow, type LlmRunScreenResponse, type LlmScreenerMetric,
} from '@/app/api/gctApi'

const { Text, Paragraph } = Typography

interface CriterionDraft { metric: string; op: string; value?: number; value2?: number; state?: string }

const NUMERIC_OPS = [
  { value: 'gte', label: '≥' }, { value: 'lte', label: '≤' },
  { value: 'gt', label: '>' }, { value: 'lt', label: '<' },
  { value: 'between', label: '区间' },
]
const CATEGORY_OPS = [{ value: 'eq', label: '等于' }]

const parseJson = (raw?: string): Record<string, unknown> => {
  if (!raw) return {}
  try { return JSON.parse(raw) as Record<string, unknown> } catch { return {} }
}

/** 筛选与开仓决策（docs/llm-dev/13 §3.4，需求①②）：左侧条件构建器（指标来自后端注册表），
 *  结果表勾选后按批量作业逐市场跑完整决策链（分析→Jev→风控→落账，只记录不下单）。 */
export default function ScreenerPage() {
  const metrics = useGetLlmScreenerMetricsQuery()
  const [runScreen, runState] = useRunLlmScreenMutation()
  const [startBatch, batchState] = useStartLlmBatchDecisionMutation()
  const [cancelBatch, cancelState] = useCancelLlmBatchDecisionMutation()

  const [criteria, setCriteria] = useState<CriterionDraft[]>([{ metric: 'change24h', op: 'gte', value: 0 }])
  const [sortMetric, setSortMetric] = useState('quotevolume24h')
  const [sortDesc, setSortDesc] = useState(true)
  const [limit, setLimit] = useState(20)
  const [result, setResult] = useState<LlmRunScreenResponse>()
  const [selected, setSelected] = useState<string[]>([])
  const [versionID, setVersionID] = useState(() => localStorage.getItem('gct.batch.strategyVersionId') ?? '')
  const [actor, setActor] = useState('webui-operator')
  const [jobID, setJobID] = useState('')
  const approvals = useGetLlmActiveApprovalsQuery()

  // A5: the batch workbench defaults to the active approval.
  useEffect(() => {
    if (!versionID && approvals.data?.approvals?.length) {
      setVersionID(approvals.data.approvals[0]?.strategyVersionId ?? '')
    }
  }, [approvals.data, versionID])

  const catalogue = metrics.data?.metrics ?? []
  const catalogueByName = useMemo(() => {
    const map = new Map<string, LlmScreenerMetric>()
    for (const m of catalogue) map.set(m.name, m)
    return map
  }, [catalogue])

  const metricOptions = useMemo(
    () => ['TICKER_CACHED', 'KLINES_CACHED', 'VENUE_READ'].map((tier) => ({
      label: tier,
      options: catalogue.filter((m) => m.tier === tier).map((m) => ({ value: m.name, label: `${m.name}${m.unit ? ` (${m.unit})` : ''}` })),
    })).filter((g) => g.options.length > 0),
    [catalogue],
  )

  const job = useGetLlmBatchDecisionQuery({ jobId: jobID }, { skip: !jobID, pollingInterval: jobID ? 2000 : 0 })

  const metricOf = (name: string) => catalogueByName.get(name)

  const applyMetric = (index: number, metric: string) => {
    const info = metricOf(metric)
    setCriteria((prev) => prev.map((c, i) => i === index
      ? { metric, op: info?.kind === 'CATEGORY' ? 'eq' : 'gte', state: info?.kind === 'CATEGORY' ? info.categories?.[0] : undefined, value: info?.kind === 'CATEGORY' ? undefined : 0 }
      : c))
  }

  const run = async () => {
    const missingVersion = criteria.filter((c) => !metricOf(c.metric))
    if (missingVersion.length > 0) { message.warning('有指标不在目录中'); return }
    const res = await runScreen({
      criteria: criteria.map((c) => ({ metric: c.metric, op: c.op, value: c.value, value2: c.value2, state: c.state })),
      sort: sortMetric ? [{ metric: sortMetric, desc: sortDesc }] : [],
      limit,
    }).unwrap()
    setResult(res)
    setSelected(res.rows?.map((r) => r.marketId) ?? [])
  }

  const startDecisions = async (marketIds: string[]) => {
    if (!versionID.trim()) { message.warning('请先填写策略版本 id（该版本必须处于活跃审批中）'); return }
    localStorage.setItem('gct.batch.strategyVersionId', versionID.trim())
    try {
      const res = await startBatch({ strategyVersionId: versionID.trim(), marketIds, kind: 'OPEN_DECISION', actor }).unwrap()
      setJobID(res.jobId)
      message.success(`批量作业已启动：${res.jobId}`)
    } catch (err) {
      message.error(isNotImplemented(err) ? '写操作被代理闸门拦截（GCT_ALLOW_WRITES=false）' : errorText(err))
    }
  }

  const cancelJob = async () => {
    if (!jobID) return
    try {
      const res = await cancelBatch({ jobId: jobID, actor }).unwrap()
      message.success(`作业已取消（${res.cancelled} 个待跑市场）`)
    } catch (err) {
      message.error(errorText(err))
    }
  }

  const valueMetrics = useMemo(() => {
    const names = new Set<string>(['change24h', 'quotevolume24h'])
    criteria.forEach((c) => names.add(c.metric))
    if (sortMetric) names.add(sortMetric)
    return [...names]
  }, [criteria, sortMetric])

  const columns = [
    { title: '合约', dataIndex: 'marketId', fixed: 'left' as const, width: 260 },
    ...valueMetrics.map((name) => ({
      title: metricOf(name)?.unit ? `${name} (${metricOf(name)?.unit})` : name,
      key: name,
      width: 130,
      render: (_: unknown, row: { statesJson?: string; numbersJson?: string }) => {
        const info = metricOf(name)
        if (info?.kind === 'CATEGORY') {
          const state = parseJson(row.statesJson)[name]
          return state ? <Tag>{String(state)}</Tag> : <Text type="secondary">—</Text>
        }
        const value = parseJson(row.numbersJson)[name]
        return value === null || value === undefined ? <Text type="secondary">—</Text> : <Text className="gct-mono">{String(value)}</Text>
      },
    })),
  ]

  const batchRows: LlmBatchDecisionRow[] = job.data?.rows ?? []

  return (
    <Row gutter={[12, 12]}>
      <Col xs={24} xl={9}>
        <Card size="small" title="筛选条件（指标目录与后端同源）"
          extra={<Tag color="purple">{catalogue.length} 个指标</Tag>}>
          {metrics.isError && <Alert type="error" showIcon message={errorText(metrics.error)} style={{ marginBottom: 8 }} />}
          <Alert type="info" showIcon style={{ marginBottom: 8 }}
            message={<span>也可以直接用自然语言筛：去 <Link to="/chat">「对话」</Link> 页说需求（例：“24h 涨幅 &gt;5%、4h 也上涨、MACD 为 BULL 的前 10 个合约”），模型会调用 <Text code>screenMarkets</Text> 全宇宙筛选并把列表与证据一起给你。</span>} />
          <Space direction="vertical" style={{ width: '100%' }} size={8}>
            {criteria.map((c, i) => {
              const info = metricOf(c.metric)
              const isCategory = info?.kind === 'CATEGORY'
              return (
                <Row gutter={6} key={i} align="middle">
                  <Col flex="auto">
                    <Select style={{ width: '100%' }} showSearch value={c.metric} options={metricOptions}
                      onChange={(v) => applyMetric(i, v)} placeholder="指标" />
                  </Col>
                  <Col>
                    <Select style={{ width: 76 }} value={c.op}
                      options={isCategory ? CATEGORY_OPS : NUMERIC_OPS}
                      onChange={(v) => setCriteria((prev) => prev.map((x, xi) => xi === i ? { ...x, op: v } : x))} />
                  </Col>
                  <Col>
                    {isCategory ? (
                      <Select style={{ width: 150 }} value={c.state}
                        options={(info?.categories ?? []).map((v) => ({ value: v, label: v }))}
                        onChange={(v) => setCriteria((prev) => prev.map((x, xi) => xi === i ? { ...x, state: v } : x))} />
                    ) : (
                      <Space size={4}>
                        <InputNumber value={c.value} onChange={(v) => setCriteria((prev) => prev.map((x, xi) => xi === i ? { ...x, value: v ?? undefined } : x))} style={{ width: 96 }} />
                        {c.op === 'between' && <InputNumber value={c.value2} placeholder="上限" style={{ width: 96 }}
                          onChange={(v) => setCriteria((prev) => prev.map((x, xi) => xi === i ? { ...x, value2: v ?? undefined } : x))} />}
                      </Space>
                    )}
                  </Col>
                  <Col>
                    <Button size="small" danger disabled={criteria.length <= 1}
                      onClick={() => setCriteria((prev) => prev.filter((_, xi) => xi !== i))}>删</Button>
                  </Col>
                </Row>
              )
            })}
            <Button size="small" onClick={() => setCriteria((prev) => [...prev, { metric: 'change4h', op: 'gte', value: 0 }])}>+ 条件</Button>
            <Divider style={{ margin: '4px 0' }} />
            <Row gutter={6} align="middle">
              <Col flex="auto">
                <Select style={{ width: '100%' }} value={sortMetric} options={catalogue.filter((m) => m.kind === 'NUMERIC').map((m) => ({ value: m.name, label: `排序：${m.name}` }))} onChange={setSortMetric} />
              </Col>
              <Col><Switch checkedChildren="降序" unCheckedChildren="升序" checked={sortDesc} onChange={setSortDesc} /></Col>
              <Col><InputNumber min={1} max={metrics.data?.maxLimit ?? 50} value={limit} onChange={(v) => setLimit(v ?? 20)} style={{ width: 80 }} /></Col>
            </Row>
            <Button type="primary" block loading={runState.isLoading} onClick={run}>运行筛选</Button>
            <Paragraph type="secondary" style={{ fontSize: 12, margin: 0 }}>
              指标"—"= 该市场数据缺席（K 线未入缓存或 T2 未覆盖），缺席市场被排除并计数，不以 0 冒充。
              净流入排序建议用 takernetflowratio24h（跨市场可比）。
            </Paragraph>
          </Space>
        </Card>
      </Col>
      <Col xs={24} xl={15}>
        <Card size="small" title="筛选结果"
          extra={result && <Space>
            <Text type="secondary">宇宙 {result.universeSize} · 命中 {result.matched} · 条件不符 {result.filteredOut} · 缺席排除 {result.excludedForMissing}</Text>
            <Button size="small" type="primary" disabled={selected.length === 0} loading={batchState.isLoading}
              onClick={() => startDecisions(selected)}>对选中 {selected.length} 个生成开仓决策</Button>
          </Space>}>
          {(result?.notes ?? []).map((n, i) => <Alert key={i} type="info" showIcon message={n} style={{ marginBottom: 6 }} />)}
          <Table
            size="small" rowKey="marketId" columns={columns} dataSource={result?.rows ?? []}
            scroll={{ x: 700 }} pagination={false}
            rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }}
            locale={{ emptyText: <Empty description="尚未运行筛选（或后端筛选器未装配）" /> }}
          />
        </Card>
        <Card size="small" style={{ marginTop: 12 }} title="开仓决策（批量作业 · 只记录不下单）"
          extra={job.data && (
            <Space>
              {!job.data.done && (
                <Button size="small" danger loading={cancelState.isLoading} onClick={() => void cancelJob()}>取消作业</Button>
              )}
              <Tag color={job.data.done ? 'green' : 'processing'}>{job.data.finished}/{job.data.total}</Tag>
            </Space>
          )}>
          <Row gutter={8} style={{ marginBottom: 8 }}>
            <Col flex="auto">
              {(approvals.data?.approvals?.length ?? 0) > 0 ? (
                <Select style={{ width: '100%' }} value={versionID || undefined} onChange={setVersionID}
                  options={(approvals.data?.approvals ?? []).map((a) => ({
                    value: a.strategyVersionId ?? a.id,
                    label: `${a.strategyVersionId ?? a.id}（${a.executionMode ?? '—'} · epoch ${a.pairEpoch ?? '—'}）`,
                  }))}
                  placeholder="选择活跃审批版本（默认已选）" />
              ) : (
                <Input value={versionID} onChange={(e) => setVersionID(e.target.value)}
                  placeholder="策略版本 id（无活跃审批列表时手输；服务端强校验，缺失即拒）" />
              )}
            </Col>
            <Col><Input value={actor} onChange={(e) => setActor(e.target.value)} style={{ width: 150 }} placeholder="操作者" /></Col>
          </Row>
          <Paragraph type="secondary" style={{ fontSize: 12 }}>
            每个市场串行跑完整决策链（分析 → Jev → 合成 → 硬风控 → 落账）；去抖/门禁/预算原样继承；
            多市场请用上方"对选中生成"批量发起。单市场也可：
            <Space size={4} style={{ marginLeft: 6 }}>
              <Input size="small" placeholder="市场 id" id="single-market" style={{ width: 260 }} />
              <Button size="small" onClick={() => {
                const el = document.getElementById('single-market') as HTMLInputElement | null
                if (el?.value) void startDecisions([el.value.trim()])
              }}>生成单个决策</Button>
            </Space>
          </Paragraph>
          <Table size="small" rowKey="marketId" loading={job.isFetching && !job.data} pagination={false}
            dataSource={batchRows}
            columns={[
              { title: '合约', dataIndex: 'marketId', width: 260 },
              { title: '状态', dataIndex: 'status', width: 100, render: (v: string) => <Tag color={v === 'CANCELLED' ? 'red' : undefined}>{v}</Tag> },
              { title: '裁决', dataIndex: 'decision', width: 100 },
              { title: '理由', dataIndex: 'reason', ellipsis: true },
              { title: '决运行', dataIndex: 'decisionRunId', width: 180, render: (v?: string) => v ? <Text copyable className="gct-mono">{v.slice(0, 8)}</Text> : <Text type="secondary">—</Text> },
              { title: '错误', dataIndex: 'error', ellipsis: true },
            ]}
            locale={{ emptyText: <Empty description="尚未发起批量决策" /> }}
          />
        </Card>
      </Col>
    </Row>
  )
}
