import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Col, Empty, Input, Row, Select, Space, Table, Tag, Typography, message } from 'antd'
import {
  errorText, isNotImplemented,
  useCancelLlmBatchDecisionMutation, useGetLlmActiveApprovalsQuery, useGetLlmBatchDecisionQuery, useGetManagedPositionsQuery, useStartLlmBatchDecisionMutation,
  type LlmBatchDecisionRow, type ManagedPosition,
} from '@/app/api/gctApi'

const { Text, Paragraph } = Typography

/** 期货持仓 → 运行市场号（Binance:usdtmarginedfutures:BASE_QUOTE） */
const toMarketID = (p: ManagedPosition): string => {
  const base = p.pair?.base ?? ''
  const quote = p.pair?.quote ?? ''
  if (!base || !quote) return ''
  return `Binance:usdtmarginedfutures:${base.toUpperCase()}_${quote.toUpperCase()}`
}

/** 持仓决策（docs/llm-dev/13 §3.3，需求③）：对已开仓合约生成"继续持有 / 平仓"决策。
 *  自开仓窗口（开仓时刻、窗口涨跌、最大不利偏移）由后端装配进分析消息；
 *  无持仓的市场在后端起批/运行期即拒。 */
export default function PositionsReviewPage() {
  const positions = useGetManagedPositionsQuery(undefined, { pollingInterval: 30_000 })
  const [startBatch, batchState] = useStartLlmBatchDecisionMutation()
  const [cancelBatch, cancelState] = useCancelLlmBatchDecisionMutation()
  const [selected, setSelected] = useState<string[]>([])
  const [versionID, setVersionID] = useState(() => localStorage.getItem('gct.batch.strategyVersionId') ?? '')
  const [actor, setActor] = useState('webui-operator')
  const [jobID, setJobID] = useState('')
  const [singleMarket, setSingleMarket] = useState('')
  const approvals = useGetLlmActiveApprovalsQuery()
  const job = useGetLlmBatchDecisionQuery({ jobId: jobID }, { skip: !jobID, pollingInterval: 2000 })

  const rows = useMemo(() => {
    const list: ManagedPosition[] = positions.data?.positions ?? []
    return list
      .map((p) => ({ key: toMarketID(p), position: p }))
      .filter((r) => r.key !== '')
  }, [positions.data])

  const start = async (marketIds: string[]) => {
    if (!versionID.trim()) { message.warning('请先填写策略版本 id（需处于活跃审批）'); return }
    localStorage.setItem('gct.batch.strategyVersionId', versionID.trim())
    try {
      const res = await startBatch({ strategyVersionId: versionID.trim(), marketIds, kind: 'POSITION_REVIEW', actor }).unwrap()
      setJobID(res.jobId)
      message.success(`持仓复核作业已启动：${res.jobId}`)
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

  // A5: default to the active approval.
  useEffect(() => {
    if (!versionID && approvals.data?.approvals?.length) {
      setVersionID(approvals.data.approvals[0]?.strategyVersionId ?? '')
    }
  }, [approvals.data, versionID])

  const batchRows: LlmBatchDecisionRow[] = job.data?.rows ?? []

  return (
    <Row gutter={[12, 12]}>
      <Col xs={24} xl={13}>
        <Card size="small" title="期货持仓（venue + 本地对账口径）"
          extra={<Text type="secondary">{rows.length} 个市场有仓</Text>}>
          {positions.isError && <Alert type="error" showIcon message={errorText(positions.error)} style={{ marginBottom: 8 }} />}
          <Table
            size="small" rowKey="key" loading={positions.isLoading} dataSource={rows} pagination={false}
            scroll={{ x: 520 }}
            columns={[
              { title: '市场', dataIndex: 'key', width: 300 },
              { title: '数量', width: 120, render: (_: unknown, r) => <Text className="gct-mono">{r.position.currentSize ?? '—'}</Text> },
              { title: '方向', width: 100, render: (_: unknown, r) => {
                const dir = r.position.currentDirection ?? r.position.openingDirection
                if (dir !== 'BUY' && dir !== 'SELL') return <Text type="secondary">—</Text>
                return <Tag color={dir === 'BUY' ? 'green' : 'red'}>{dir === 'BUY' ? '多' : '空'}</Tag>
              } },
            ]}
            rowSelection={{ selectedRowKeys: selected, onChange: (keys) => setSelected(keys as string[]) }}
            locale={{ emptyText: <Empty description="无持仓" /> }}
          />
          <Space style={{ marginTop: 10 }} wrap>
            <Button type="primary" disabled={selected.length === 0 && rows.length === 0} loading={batchState.isLoading}
              onClick={() => start(selected.length > 0 ? selected : rows.map((r) => r.key))}>
              {selected.length > 0 ? `对选中 ${selected.length} 个生成持有/平仓决策` : `对全部 ${rows.length} 个生成持有/平仓决策`}
            </Button>
            <Input value={singleMarket} onChange={(e) => setSingleMarket(e.target.value)}
              placeholder="单个市场 id（未列出的持仓也可直接复核）" style={{ width: 300 }} />
            <Button disabled={!singleMarket.trim()} loading={batchState.isLoading}
              onClick={() => start([singleMarket.trim()])}>生成单个持有/平仓决策</Button>
          </Space>
        </Card>
      </Col>
      <Col xs={24} xl={11}>
        <Card size="small" title="持有 / 平仓决策（批量作业 · 只记录不下单）"
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
                <Input value={versionID} onChange={(e) => setVersionID(e.target.value)} placeholder="策略版本 id（需处于活跃审批）" />
              )}
            </Col>
            <Col><Input value={actor} onChange={(e) => setActor(e.target.value)} style={{ width: 150 }} placeholder="操作者" /></Col>
          </Row>
          <Paragraph type="secondary" style={{ fontSize: 12 }}>
            复核语境由后端装配：开仓时刻（本地 intents 最近一笔成交）、窗口涨跌、最大不利偏移、区间振幅；
            开仓时刻读不到时以 48h 兜底并如实标注。候选动作限 HOLD / REDUCE（平仓数量由退出政策决定）。
          </Paragraph>
          <Table size="small" rowKey="marketId" pagination={false} dataSource={batchRows}
            columns={[
              { title: '市场', dataIndex: 'marketId', width: 280 },
              { title: '状态', dataIndex: 'status', width: 100, render: (v: string) => <Tag color={v === 'CANCELLED' ? 'red' : undefined}>{v}</Tag> },
              { title: '裁决', dataIndex: 'decision', width: 100 },
              { title: '理由', dataIndex: 'reason', ellipsis: true },
              { title: '决运行', dataIndex: 'decisionRunId', width: 160, render: (v?: string) => v ? <Text copyable className="gct-mono">{v.slice(0, 8)}</Text> : <Text type="secondary">—</Text> },
              { title: '错误', dataIndex: 'error', ellipsis: true },
            ]}
            locale={{ emptyText: <Empty description="尚未发起持仓复核" /> }}
          />
        </Card>
      </Col>
    </Row>
  )
}
