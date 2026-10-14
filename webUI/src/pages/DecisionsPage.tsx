import { useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Empty, Form, Input, Modal, Row, Select, Space, Steps, Table, Tag, Tooltip, Typography, message } from 'antd'
import {
  errorText,
  useGetLlmAuditEventsQuery, useGetLlmDecisionLineageQuery, useGetLlmDecisionRunQuery, useGetLlmDecisionRunsQuery, useGetLlmStatusQuery,
  usePreviewDecisionMutation, useTriggerDecisionAnalysisMutation,
  type LlmAuditEvent, type LlmDecisionRun, type LlmRiskEvidence,
} from '@/app/api/gctApi'
import { NotImplemented } from '@/components/NotImplemented'

const { Paragraph, Text } = Typography

/** 决策链路的十个阶段（docs/llm-dev/03 §6 / 08 §3.5），血缘视图（W6-02）将按此渲染 */
const LINEAGE_STAGES = [
  '观察 snapshot', '工具转录', '候选 Candidate', 'Jev 评估', '决策合成',
  '硬风控', 'OrderIntent', '订单', '成交', '仓位',
]

const REJECT_CODES = [
  ['RISK_ORDER_NOTIONAL_EXCEEDED', '单笔名义额', 'min(模型建议上限, 批准上限)'],
  ['RISK_TOTAL_EXPOSURE_EXCEEDED', '统一敞口', '已有仓位 + 未成交开仓委托 + 拟下委托'],
  ['RISK_DAILY_LOSS_EXCEEDED', 'UTC 日损', '基线 ± 外部划转 − 当前权益'],
  ['RISK_NEW_RISK_WINDOW_EXCEEDED', '频率窗口', '默认 2 个 / 300min（硬上限 5）'],
  ['RISK_SLIPPAGE_EXCEEDED', '滑点', '按当前盘口估算 vs maxSlippageBps=20'],
  ['RISK_MARKET_NOT_GRANTED', '市场授权', '逐市场、≤30 天'],
]

const DECISION_COLORS: Record<string, string> = {
  OPEN: 'green', CLOSE: 'blue', HOLD: 'default', REVIEW: 'orange',
}

/** 决策与审计（W6-01 列表；血缘渲染 W6-02，风控证据表 W6-03） */
export default function DecisionsPage() {
  const [versionFilter, setVersionFilter] = useState('')
  const [appliedVersion, setAppliedVersion] = useState('')
  const [decisionFilter, setDecisionFilter] = useState<string>()
  const [limit, setLimit] = useState(50)
  const [auditKind, setAuditKind] = useState<string>()
  const [auditLimit, setAuditLimit] = useState(100)
  const [lineageId, setLineageId] = useState<string>()

  const llm = useGetLlmStatusQuery(undefined, { pollingInterval: 30_000 })
  const runs = useGetLlmDecisionRunsQuery(
    { strategyVersionId: appliedVersion || undefined, limit },
    { pollingInterval: 30_000 },
  )
  const audit = useGetLlmAuditEventsQuery({ kind: auditKind, limit: auditLimit }, { pollingInterval: 30_000 })
  const detail = useGetLlmDecisionRunQuery({ id: lineageId ?? '' }, { skip: !lineageId })
  const lineage = useGetLlmDecisionLineageQuery({ id: lineageId ?? '' }, { skip: !lineageId })

  // W6-06：过期窗口随配置下发（未配置回退 12h，与扫描器同源）——列表按它派生「已过期·未执行」
  const reviewExpireHours = llm.data?.reviewExpireHours ?? 12
  const reviewWindowMs = reviewExpireHours * 3_600_000
  const rows = (runs.data?.runs ?? []).filter((r) => !decisionFilter || r.decision === decisionFilter)

  // 十阶段的第一步到「合成」由运行本身证明；「风控」以已落库的评估为证；
  // 执行段（意图/订单/成交/仓位）在 PAPER/TESTNET 前不产生记录——这里如实显示为未到。
  const stageReached = (index: number): boolean => {
    if (index <= 4) return !!detail.data?.run
    if (index === 5) return (lineage.data?.riskEvaluations?.length ?? 0) > 0
    return false
  }

  // 手动触发与预览（W6-04）：写路径；触发走完整门禁，预览永远 HOLD/REVIEW 收尾
  const [manualOpen, setManualOpen] = useState(false)
  const [manualBusy, setManualBusy] = useState<'preview' | 'trigger'>()
  const [manualForm] = Form.useForm()
  const [previewDecision] = usePreviewDecisionMutation()
  const [triggerAnalysis] = useTriggerDecisionAnalysisMutation()

  const openManual = () => {
    manualForm.setFieldsValue({
      strategyVersionId: appliedVersion || '',
      marketId: '',
      actor: 'webui-operator',
    })
    setManualOpen(true)
  }

  const runManual = async (mode: 'preview' | 'trigger') => {
    try {
      const v = await manualForm.validateFields()
      setManualBusy(mode)
      const call = mode === 'preview' ? previewDecision : triggerAnalysis
      const run = await call({ strategyVersionId: v.strategyVersionId, marketId: v.marketId, actor: v.actor }).unwrap()
      message.success(`${mode === 'preview' ? '预览' : '触发'}完成：${run.decision ?? '—'}（${run.reason ?? '—'}）——已写入决策列表`)
      setManualOpen(false)
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) {
        message.error(errorText(err))
      }
    } finally {
      setManualBusy(undefined)
    }
  }

  // 导出是客户端下载：不改动任何服务端状态（审计流是只读的）
  const exportAudit = () => {
    const blob = new Blob([JSON.stringify(audit.data?.events ?? [], null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `llm-audit-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.json`
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Row gutter={[12, 12]}>
      <Col span={24}>
        <Alert
          type="info" showIcon
          message="决策血缘：每次决策都可追溯到「观察 → 候选 → Jev → 风控 → 订单 → 成交 → 仓位」"
          description={<div className="gct-dim" style={{ fontSize: 12 }}>{LINEAGE_STAGES.join('  →  ')}</div>}
        />
      </Col>

      <Col span={24}>
        <Card
          size="small"
          title={<span className="gct-panel-title">决策列表（llm/decisions · M4-15）</span>}
          extra={<Button size="small" onClick={openManual}>手动触发/预览</Button>}
        >
          <Space wrap style={{ marginBottom: 8 }}>
            <Input
              style={{ width: 320 }}
              placeholder="按策略修订版 id 筛选（服务端过滤）"
              value={versionFilter}
              onChange={(e) => setVersionFilter(e.target.value)}
              onPressEnter={() => setAppliedVersion(versionFilter.trim())}
              allowClear
            />
            <Button onClick={() => setAppliedVersion(versionFilter.trim())}>应用</Button>
            <Select
              style={{ width: 140 }}
              placeholder="决策"
              allowClear
              value={decisionFilter}
              onChange={setDecisionFilter}
              options={['OPEN', 'CLOSE', 'HOLD', 'REVIEW'].map((d) => ({ value: d, label: d }))}
            />
            <Select
              style={{ width: 140 }}
              value={limit}
              onChange={setLimit}
              options={[50, 200, 500].map((n) => ({ value: n, label: `最近 ${n} 条` }))}
            />
          </Space>
          {runs.error ? (
            <NotImplemented title="决策列表与血缘" error={runs.error} />
          ) : (
            <Table<LlmDecisionRun>
              size="small"
              rowKey="id"
              loading={runs.isLoading}
              dataSource={rows}
              pagination={false}
              columns={[
                { title: '时间', dataIndex: 'createdAt', width: 190, render: (v?: string) => v ?? '—' },
                { title: '触发', dataIndex: 'trigger', width: 130, render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v ?? '—'}</Text> },
                {
                  title: '决策', dataIndex: 'decision', width: 190,
                  render: (v: string | undefined, r: { createdAt?: string }) => {
                    const expired = v === 'REVIEW' && r.createdAt != null && Date.now() - Date.parse(r.createdAt) > reviewWindowMs
                    return (
                      <Space size={4}>
                        <Tag color={DECISION_COLORS[v ?? ''] ?? 'default'} style={{ marginInline: 0 }}>{v ?? '—'}</Tag>
                        {expired ? (
                          <Tooltip title={`超时未处理 → 自动转 EXPIRED（审计 REVIEW_EXPIRED + 通知）。自动过期 = 不执行：堆积的待办会诱人草率批量放行，比过期更危险。`}>
                            <Tag style={{ marginInline: 0 }}>已过期·未执行</Tag>
                          </Tooltip>
                        ) : null}
                      </Space>
                    )
                  },
                },
                { title: '原因码', dataIndex: 'reason', render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v || '—'}</Text> },
                {
                  title: '策略修订版', dataIndex: 'strategyVersionId', width: 150,
                  render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v ? v.slice(0, 8) + '…' : '—'}</Text>,
                },
                {
                  title: '模型', width: 220,
                  render: (_, r) => (
                    <span className="gct-dim" style={{ fontSize: 11 }}>
                      {r.llmModel ?? '—'} × {r.jevModel ?? '—'}
                    </span>
                  ),
                },
                { title: '延迟', dataIndex: 'latencyMs', width: 80, render: (v?: number) => (v != null ? `${v}ms` : '—') },
                {
                  title: '轮次/工具', width: 90,
                  render: (_, r) => `${r.llmRoundCount ?? 0}/${r.toolCallCount ?? 0}`,
                },
                {
                  title: '输入哈希', dataIndex: 'inputHash', width: 140,
                  render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v ? v.slice(0, 12) + '…' : '—'}</Text>,
                },
                {
                  title: '血缘', width: 80,
                  render: (_, r) => <Button size="small" onClick={() => setLineageId(r.id)}>血缘</Button>,
                },
              ]}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">暂无决策记录（触发/预览产生的 SHADOW 运行会出现在这里）</span>} /> }}
            />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            列表只读：SHADOW 链永不成单；每次运行的血缘（转录/候选/Jev 原文/风控逐项判定）在行详情（W6-02/W6-03），
            触发与预览入口见 gctcli <code>llm decisions trigger|preview</code>。
          </Paragraph>
        </Card>
      </Col>

      <Col span={24}>
        <Card
          size="small"
          title={<span className="gct-panel-title">审计事件流（llm/audit · append-only）</span>}
          extra={<Button size="small" onClick={exportAudit} disabled={!audit.data}>导出 JSON</Button>}
        >
          <Space wrap style={{ marginBottom: 8 }}>
            <Select
              style={{ width: 220 }}
              placeholder="按事件类型筛选"
              allowClear
              value={auditKind}
              onChange={setAuditKind}
              options={['PAIR_SWITCH', 'APPROVAL_GRANTED', 'APPROVAL_REVOKED', 'RISK_SWITCH_SET', 'CRASH_RECOVERY', 'STRATEGY_PAUSED']
                .map((k) => ({ value: k, label: k }))}
            />
            <Select
              style={{ width: 140 }}
              value={auditLimit}
              onChange={setAuditLimit}
              options={[100, 500, 2000].map((n) => ({ value: n, label: `最近 ${n} 条` }))}
            />
          </Space>
          {audit.error ? (
            <NotImplemented title="审计事件流" error={audit.error} />
          ) : (
            <Table<LlmAuditEvent>
              size="small"
              rowKey="id"
              loading={audit.isLoading}
              dataSource={audit.data?.events ?? []}
              pagination={false}
              columns={[
                { title: '时间', dataIndex: 'createdAt', width: 190, render: (v?: string) => v ?? '—' },
                { title: '类型', dataIndex: 'kind', width: 200, render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v ?? '—'}</Text> },
                { title: '操作者', dataIndex: 'actor', width: 100, render: (v?: string) => v ?? '—' },
                { title: '对象', dataIndex: 'subject', width: 220, render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v ?? '—'}</Text> },
                {
                  title: '详情', dataIndex: 'detailJson',
                  render: (v?: string) => (
                    <Text className="gct-mono gct-dim" style={{ fontSize: 11 }} ellipsis={{ tooltip: v }} title={undefined}>
                      {v ?? '—'}
                    </Text>
                  ),
                },
              ]}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">暂无审计事件</span>} /> }}
            />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            审计流只增不改：配对切换、审批授予/撤销、风控开关、崩溃恢复等治理动作都留痕；导出为客户端下载，不触碰服务端。
          </Paragraph>
        </Card>
      </Col>

      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">决策统计</span>}>
          {llm.data ? (
            <div>
              <div className="gct-kv"><span>今日决策</span><span className="gct-num">{llm.data.decisionsToday ?? '—'}</span></div>
              <div className="gct-kv"><span>窗口内新增风险意图</span>
                <span className="gct-num">{llm.data.newRiskIntentsInWindow ?? 0} / {llm.data.newRiskWindowLimit ?? '—'}</span></div>
              <div className="gct-kv"><span>24h 预约剩余</span><span className="gct-num">{llm.data.reservationsRemaining24h ?? '—'}</span></div>
            </div>
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">运行态统计（llm/status）暂不可达</span>} />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 12, marginBottom: 0 }}>
            <Tag color="orange">REVIEW {reviewExpireHours}h 自动过期</Tag>
            用户盯盘频率为「每天看几次」，故超时未处理的 REVIEW 自动转 EXPIRED（审计 <code>REVIEW_EXPIRED</code> + 通知）并<b>不执行</b>——防止堆积。
          </Paragraph>
        </Card>
      </Col>

      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">风控拒绝码（实际值 vs 允许值）</span>}>
          <Table
            size="small" pagination={false}
            dataSource={REJECT_CODES.map(([code, name, rule]) => ({ key: code, code, name, rule }))}
            columns={[
              { title: '检查码', dataIndex: 'code', render: (v: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span> },
              { title: '维度', dataIndex: 'name', width: 90 },
              { title: '判定依据', dataIndex: 'rule', render: (v: string) => <span className="gct-dim">{v}</span> },
            ]}
          />
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            完整清单 7 类约 45 项 + 18 个可开关项，见
            <code> docs/llm-dev/07-策略与自动决策引擎.md</code> §5.2 / §5.6。
          </Paragraph>
        </Card>
      </Col>

      <Drawer
        title={`决策血缘：${detail.data?.run?.id ?? lineageId ?? ''}`}
        width={860}
        open={!!lineageId}
        onClose={() => setLineageId(undefined)}
        forceRender
      >
        <Steps
          direction="vertical"
          size="small"
          style={{ marginBottom: 16 }}
          items={LINEAGE_STAGES.map((title, index) => ({
            title,
            status: stageReached(index) ? 'finish' : 'wait',
            description: stageReached(index) ? undefined : (
              index >= 6 ? '执行段：PAPER/TESTNET 前不产生记录' : '本运行无此段数据'
            ),
          }))}
        />

        <Card size="small" title="运行摘要" style={{ marginBottom: 12 }}>
          {detail.data?.run ? (
            <Descriptions size="small" column={2}>
              <Descriptions.Item label="决策">
                <Tag color={DECISION_COLORS[detail.data.run.decision ?? ''] ?? 'default'}>{detail.data.run.decision ?? '—'}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="原因码">{detail.data.run.reason ?? '—'}</Descriptions.Item>
              <Descriptions.Item label="触发">{detail.data.run.trigger ?? '—'}</Descriptions.Item>
              <Descriptions.Item label="模式">{detail.data.run.runMode ?? '—'}</Descriptions.Item>
              <Descriptions.Item label="模型">{detail.data.run.llmModel ?? '—'} × {detail.data.run.jevModel ?? '—'}</Descriptions.Item>
              <Descriptions.Item label="快照">
                <Text className="gct-mono" style={{ fontSize: 12 }}>{detail.data.run.snapshotId ?? '—'}</Text>
              </Descriptions.Item>
            </Descriptions>
          ) : <Text type="secondary">读取中…</Text>}
        </Card>

        <Card size="small" title="候选 Candidate（模型输出原文）" style={{ marginBottom: 12 }}>
          <pre style={{ background: 'rgba(127,127,127,0.12)', padding: 8, borderRadius: 6, overflowX: 'auto', fontSize: 11, maxHeight: 260, margin: 0 }}>{prettyJson(detail.data?.candidateJson)}</pre>
        </Card>

        <Card size="small" title="Jev 评估（请求哈希 + 响应原文）" style={{ marginBottom: 12 }}>
          <pre style={{ background: 'rgba(127,127,127,0.12)', padding: 8, borderRadius: 6, overflowX: 'auto', fontSize: 11, maxHeight: 260, margin: 0 }}>{prettyJson(detail.data?.jevJson)}</pre>
        </Card>

        <Card size="small" title="工具转录（参数 / 结果 / 哈希）" style={{ marginBottom: 12 }}>
          <pre style={{ background: 'rgba(127,127,127,0.12)', padding: 8, borderRadius: 6, overflowX: 'auto', fontSize: 11, maxHeight: 260, margin: 0 }}>{prettyJson(detail.data?.toolTranscriptJson)}</pre>
        </Card>

        <Card size="small" title="风控证据（实际值 vs 允许值）" style={{ marginBottom: 12 }}>
          <Table<LlmRiskEvidence>
            size="small"
            rowKey="riskEvaluationId"
            pagination={false}
            loading={lineage.isLoading}
            dataSource={lineage.data?.riskEvaluations ?? []}
            columns={[
              { title: '判定', dataIndex: 'verdict', width: 100, render: (v?: string) => <Tag color={v === 'PASSED' ? 'green' : 'red'}>{v ?? '—'}</Tag> },
              { title: '检查版本', dataIndex: 'checkVersion', width: 90 },
              { title: '数量', dataIndex: 'computedQty', width: 90 },
              { title: '名义额 USD', dataIndex: 'computedNotionalUsd', width: 110 },
              { title: '参考价', dataIndex: 'referencePrice', width: 100 },
              { title: '信封哈希', dataIndex: 'envelopeHash', render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v ? v.slice(0, 12) + '…' : '—'}</Text> },
              { title: '评估于', dataIndex: 'evaluatedAt', width: 190 },
            ]}
            expandable={{
              expandedRowRender: (r) => (
                <div>
                  <Text strong style={{ fontSize: 12 }}>checks（检查码 + 实际值 vs 允许值）</Text>
                  {(() => {
                    const checks = parseChecks(r.checksJson)
                    if (!checks) {
                      return <pre style={{ background: 'rgba(127,127,127,0.12)', padding: 8, borderRadius: 6, overflowX: 'auto', fontSize: 11, maxHeight: 200, margin: '4px 0 8px' }}>{prettyJson(r.checksJson)}</pre>
                    }
                    return (
                      <Table
                        size="small"
                        rowKey="check"
                        pagination={false}
                        style={{ margin: '4px 0 8px' }}
                        dataSource={checks}
                        columns={[
                          { title: '检查码', dataIndex: 'check', width: 220, render: (v: string) => <Text className="gct-mono" style={{ fontSize: 11 }}>{v}</Text> },
                          { title: '实际值', dataIndex: 'actual', width: 130 },
                          { title: '允许值', dataIndex: 'allowed', width: 130 },
                          { title: '单位', dataIndex: 'unit', width: 80 },
                          { title: '判定', dataIndex: 'pass', width: 80, render: (v?: boolean) => (v == null ? '—' : <Tag color={v ? 'green' : 'red'}>{v ? '通过' : '拒绝'}</Tag>) },
                        ]}
                      />
                    )
                  })()}
                  <Text strong style={{ fontSize: 12 }}>rejections</Text>
                  <pre style={{ background: 'rgba(127,127,127,0.12)', padding: 8, borderRadius: 6, overflowX: 'auto', fontSize: 11, maxHeight: 200, margin: '4px 0 0' }}>{prettyJson(r.rejectionsJson)}</pre>
                </div>
              ),
            }}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">该运行没有风控评估记录</span>} /> }}
          />
        </Card>

        <Card size="small" title="关联审计事件">
          <Table<LlmAuditEvent>
            size="small"
            rowKey="id"
            pagination={false}
            loading={lineage.isLoading}
            dataSource={lineage.data?.auditEvents ?? []}
            columns={[
              { title: '时间', dataIndex: 'createdAt', width: 190 },
              { title: '类型', dataIndex: 'kind', width: 160, render: (v?: string) => <Text className="gct-mono" style={{ fontSize: 12 }}>{v ?? '—'}</Text> },
              { title: '操作者', dataIndex: 'actor', width: 90 },
              { title: '详情', dataIndex: 'detailJson', render: (v?: string) => <Text className="gct-mono gct-dim" style={{ fontSize: 11 }} ellipsis={{ tooltip: v }}>{v ?? '—'}</Text> },
            ]}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">无关联审计事件</span>} /> }}
          />
        </Card>
      </Drawer>

      <Modal
        title="手动触发与预览（MANUAL_PREVIEW 只产出 HOLD/REVIEW）"
        open={manualOpen}
        onCancel={() => setManualOpen(false)}
        footer={null}
        width={600}
      >
        <Alert
          type="info" showIcon style={{ marginBottom: 12 }}
          message="触发走完整门禁；预览永远不下单"
          description="两者都写 SHADOW 决策运行记录并可在下方列表查看。触发要过 pre-gate（配对/探针/风控开关/预算/运行模式等），未过即拒；预览只跑分析链并以 HOLD/REVIEW 收尾。写操作受代理 GCT_ALLOW_WRITES 闸门。"
        />
        <Form form={manualForm} layout="vertical" size="small">
          <Form.Item name="strategyVersionId" label="策略修订版 id（须为已冻结且带活审批的版本）" rules={[{ required: true }]}>
            <Input placeholder="ver-…" />
          </Form.Item>
          <Form.Item name="marketId" label="市场 id" rules={[{ required: true }]}>
            <Input placeholder="Binance:usdtmarginedfutures:BTC_USDT" />
          </Form.Item>
          <Form.Item name="actor" label="操作者（审计留痕）" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
        </Form>
        <Space>
          <Button loading={manualBusy === 'trigger'} onClick={() => void runManual('trigger')}>触发分析（过门禁）</Button>
          <Button type="primary" loading={manualBusy === 'preview'} onClick={() => void runManual('preview')}>仅预览（HOLD/REVIEW）</Button>
        </Space>
      </Modal>
    </Row>
  )
}

/** prettyJson：能解析就缩进，解析不了原样展示（原文永不丢） */
function prettyJson(raw?: string): string {
  if (!raw) return '（无）'
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

/** parseChecks：把 checks_json 的 {检查码:{actual,allowed,unit?,pass}} 解析成逐项表；形态不符回退原文 */
function parseChecks(raw?: string): Array<{ check: string; actual: string; allowed: string; unit: string; pass?: boolean }> | undefined {
  if (!raw) return undefined
  try {
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return undefined
    return Object.entries(parsed as Record<string, unknown>).map(([check, value]) => {
      const item = (value ?? {}) as Record<string, unknown>
      return {
        check,
        actual: item.actual != null ? String(item.actual) : JSON.stringify(value),
        allowed: item.allowed != null ? String(item.allowed) : '—',
        unit: item.unit != null ? String(item.unit) : '—',
        pass: typeof item.pass === 'boolean' ? item.pass : undefined,
      }
    })
  } catch {
    return undefined
  }
}
