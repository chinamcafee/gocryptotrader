import { Alert, Button, Card, Checkbox, Col, Form, Input, Modal, Row, Select, Switch, Table, Tag, Tooltip, Typography, message } from 'antd'
import {
  errorText,
  useClearLlmHaltMutation, useGetCoverageStatusQuery, useGetLlmHaltsQuery, useGetLlmRiskSwitchesQuery,
  useGetLlmNotifyStatusQuery, useGetLlmStatusQuery, useGetLlmUsageQuery, useReconcileLlmNowMutation, useSetLlmRunModeMutation,
  useSetLlmHaltMutation, useSetLlmRiskSwitchMutation, useTestLlmNotifyMutation,
  type LlmRiskSwitch,
} from '@/app/api/gctApi'
import type { LlmHaltRow } from '@/app/api/types'
import { NotImplemented } from '@/components/NotImplemented'
import { useEffect, useState } from 'react'

const { Paragraph } = Typography

/** token 数的紧凑展示（1000 → 1.0k） */
const fmtTokens = (n?: number) => (n == null ? '—' : n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n))

/**
 * 硬风控开关目录（Q8d 定案：18 个独立开关，可运行时切换，优先级高于提示词）。
 * 其中 3 个红色标注的开关**不允许通过 RPC 关闭**（只能改配置并重启 + 审计）。
 * 依据：docs/llm-dev/07-策略与自动决策引擎.md §5.6
 */
const RISK_SWITCHES: Array<[string, string, boolean, boolean]> = [
  // [开关, 说明, 默认开, 可RPC关闭]
  ['haltOnDailyLoss', '日损超限 → FULL halt', true, false],
  ['dailyLossRecoveryManualOnly', '日损 halt 后必须人工解除', true, false],
  ['requireProtectiveExit', '开仓必须同挂止损', true, false],
  ['stopDistanceTighterOfAtrAndPct', '止损取 max(1.5×ATR14, 2%) 较紧者', true, true],
  ['blockOnUnknownOrders', '有未解决 UNKNOWN 订单禁开仓', true, false],
  ['blockOnStaleReconciliation', '对账过期 >10min 禁开仓', true, true],
  ['blockOnClockDrift', '时钟偏差 >1000ms 禁下单', true, false],
  ['blockOnOpposingPosition', '单向持仓下反向开仓需显式确认/改判 REDUCE', true, true],
  ['blockOnWideSpread', '价差 >10bps 禁开仓', true, true],
  ['blockOnThinDepth', '深度不足禁开仓', true, true],
  ['blockOnMarkIndexDeviation', '标记价与指数价偏差 >0.5% 禁开仓', true, true],
  ['blockOnConsecutiveLosses', '连续 3 笔亏损 → 暂停新仓 60min', true, true],
  ['maxOrdersPerMinute', '账户级全订单 ≤6/min（含撤单）', true, true],
  ['liquidityTopNOnly', '仅 quoteVolume 前 50 允许自动交易', true, true],
  ['blockOnExtremeFunding', '|预测资金费率| >0.1% 禁开新仓', false, true],
  ['blockNearFundingSettlement', '距结算 <10min 禁开新仓', false, true],
  ['tradingHoursOnly', '仅 UTC 08:00-20:00 开新仓', false, true],
  ['blockOnHighVolatility', 'ATR(14)/price >3% 禁开仓', false, true],
]

/** 健康与治理（W7）。halt / 对账 / 预算 / 覆盖轮询 / 风险开关 / 通知 */
export default function HealthPage() {
  const { data: llm } = useGetLlmStatusQuery(undefined, { pollingInterval: 15_000 })
  const { data: cov, error: covErr } = useGetCoverageStatusQuery(undefined, { pollingInterval: 30_000 })
  const { data: risks, error: riskErr, isLoading: risksLoading } = useGetLlmRiskSwitchesQuery(undefined, { pollingInterval: 30_000 })
  const [setRiskSwitch] = useSetLlmRiskSwitchMutation()
  const [reconcileNow, reconcileState] = useReconcileLlmNowMutation()
  const { data: usage } = useGetLlmUsageQuery(undefined, { pollingInterval: 30_000 })
  const [toggling, setToggling] = useState<string>()
  const [proxy, setProxy] = useState<Record<string, unknown> | null>(null)

  useEffect(() => {
    fetch('/api/health').then((r) => r.json()).then(setProxy).catch(() => setProxy(null))
  }, [])

  const onReconcile = async () => {
    try {
      const res = await reconcileNow({ actor: 'webui-operator' }).unwrap()
      if (res.reconciled) {
        message.success('对账完成：CLEAN（有效期至今后 5 分钟）')
      } else {
        message.error(`对账发现 ${res.deltas?.length ?? 0} 处差异 → 已 SOFT halt（${res.haltId ?? '—'}），新开仓通道被阻断`)
      }
    } catch (err) {
      message.error(errorText(err))
    }
  }

  const onToggle = async (row: LlmRiskSwitch, next: boolean) => {
    try {
      setToggling(row.name)
      await setRiskSwitch({ name: row.name, enabled: next, actor: 'webui-operator' }).unwrap()
      message.success(`${row.name} → ${next ? '开' : '关'}（已写 CONFIG_CHANGED 审计）`)
    } catch (err) {
      if (err && typeof err === 'object' && 'status' in (err as object)) {
        message.error(errorText(err))
      }
    } finally {
      setToggling(undefined)
    }
  }

  return (
    <Row gutter={[12, 12]}>
      <Col xs={24} lg={8}>
        <HaltCard />
        <ModeCard />

        <Card size="small" style={{ marginTop: 12 }} title={<span className="gct-panel-title">本地代理自检</span>}>
          {proxy ? (
            <div>
              <div className="gct-kv"><span>状态</span><span className="gct-up">ok</span></div>
              <div className="gct-kv"><span>上游</span><span className="gct-mono" style={{ fontSize: 11 }}>{String(proxy.gateway)}</span></div>
              <div className="gct-kv"><span>写操作</span>
                <span>{proxy.allowWrites ? <Tag color="red">已开启</Tag> : <Tag color="green">关闭（安全默认）</Tag>}</span>
              </div>
              <div className="gct-kv"><span>凭据已加载</span><span>{proxy.credentialsLoaded ? '是' : '否'}</span></div>
              <div className="gct-kv"><span>规划中 LLM 端点</span><span className="gct-num">{String(proxy.plannedLlmEndpoints)}</span></div>
            </div>
          ) : (
            <Alert type="warning" showIcon message="代理不可达" description="请运行 `pnpm proxy` 或 `pnpm dev:all`。" />
          )}
        </Card>
      </Col>

      <Col xs={24} lg={8}>
        <Card size="small" title={<span className="gct-panel-title">全量覆盖轮询（Q2 · 运行面 W7-07）</span>}>
          {covErr ? <NotImplemented title="覆盖轮询状态" error={covErr} /> : cov ? (
            !cov.enabled ? (
              <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>
                覆盖轮询未启用（<code>llm.coverage.enabled=false</code> 是合法配置，不是故障）。
                计划算术见「合约覆盖」页；启用后这里显示实测的周期进度与权重占用。
              </Paragraph>
            ) : (
              <div>
                <div className="gct-kv"><span>范围 · 覆盖合约</span><span className="gct-num">{cov.scope || '—'} · {cov.contractCount}</span></div>
                <div className="gct-kv"><span>周期进度</span>
                  <span className="gct-num">{cov.visitedThisCycle ?? '—'} / {cov.contractCount} 合约（
                    {cov.cycleProgressPercent != null ? cov.cycleProgressPercent.toFixed(0) : '—'}%） · {cov.rotationCycleMinutes} 分钟/周期</span>
                </div>
                <div className="gct-kv"><span>最旧合约年龄</span><span className="gct-num">{cov.worstContractAgeSeconds ?? '—'}s</span></div>
                <div className="gct-kv"><span>权重占用</span>
                  <span className="gct-num">{cov.weightUsedPerMinute ?? '—'} / {cov.weightBudgetPerMinute ?? '—'} /min</span>
                </div>
                <div className="gct-kv"><span>周期完成</span>
                  <span className="gct-num">{cov.cyclesCompleted ?? '—'} 次 · 上次 {cov.lastCycleCompletedAt ? cov.lastCycleCompletedAt.replace('T', ' ').slice(0, 19) : '—'}</span>
                </div>
                <div className="gct-kv"><span>失败计数</span>
                  <span className={cov.failures ? 'gct-num gct-down' : 'gct-num'}>{cov.failures ?? '—'}</span>
                </div>
                <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 6, marginBottom: 0 }}>
                  最近 ticker 刷新 {cov.lastTickerRefresh ? cov.lastTickerRefresh.replace('T', ' ').slice(0, 19) : '—'} ·
                  最近整点重载 {cov.lastReload ? cov.lastReload.replace('T', ' ').slice(0, 19) : '—'}（本小时 {cov.reloadsThisHour ?? '—'} 个市场）；
                  增量间距 {cov.incrementSpacingMs ?? '—'}ms。计划值（788 可用 / 588 在范围内）见「合约覆盖」页。
                </Paragraph>
              </div>
            )
          ) : (
            <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>读取中…</Paragraph>
          )}
        </Card>

        <Card size="small" style={{ marginTop: 12 }} title={<span className="gct-panel-title">对账与预算</span>}>
          <div className="gct-kv"><span>账户已对账</span>
            <span>{llm?.accountReconciled ? <Tag color="green">是</Tag> : <Tag color="orange">否</Tag>}</span>
          </div>
          <div className="gct-kv"><span>手动对账（W7-02）</span>
            <span><Button size="small" loading={reconcileState.isLoading} onClick={onReconcile}>立即对账</Button></span>
          </div>
          <div className="gct-kv"><span>24h 预约剩余</span><span className="gct-num">{llm?.reservationsRemaining24h ?? '—'}</span></div>
          <div className="gct-kv"><span>时钟偏差</span><span className="gct-num">{llm?.clockDriftMs ?? '—'} ms</span></div>
          <div className="gct-kv"><span>最旧数据年龄</span><span className="gct-num">{llm?.oldestDataAgeSeconds ?? '—'} s</span></div>
          <div className="gct-kv"><span>tradeAuthorized</span>
            {llm?.tradeAuthorized === undefined
              ? <span className="gct-dim">—</span>
              : <Tag color={llm.tradeAuthorized ? 'red' : 'default'}>{String(llm.tradeAuthorized)}</Tag>}
          </div>
          {(usage?.lines ?? []).map((l) => (
            <div className="gct-kv" key={`${l.kind}:${l.purpose}`}>
              <span>{l.kind === 'JEV' ? 'Jev' : 'LLM'} · {l.purpose}</span>
              <span className="gct-num">
                {l.reservations} 次 · in {fmtTokens(l.inputTokens)} / out {fmtTokens(l.outputTokens)}
                {l.avgLatencyMs ? ` · ⌀${l.avgLatencyMs}ms` : ''}
              </span>
            </div>
          ))}
          {usage ? (
            <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 6, marginBottom: 0 }}>
              {usage.windowHours}h 合计 in {fmtTokens(usage.totalInputTokens)} / out {fmtTokens(usage.totalOutputTokens)} · {usage.totalReservations ?? 0} 次已结算调用
              （口径：窗口内 USED 预约；在途/已释放不计）。<b>自估成本缺席</b>——库中无价目源，不以猜测数字冒充；token 与延迟为实测。
            </Paragraph>
          ) : null}
        </Card>

        <NotifyCard />
      </Col>

      <Col xs={24} lg={8}>
        <Card
          size="small"
          title={<span className="gct-panel-title">风险开关目录（18 项，Q8d）</span>}
          extra={risks?.switches ? <Tag color={risks.switches.length === 18 ? 'green' : 'orange'}>{risks.switches.length} 项</Tag> : null}
        >
          {riskErr ? <NotImplemented title="风险开关" error={riskErr} /> : (
            <Table<LlmRiskSwitch>
              size="small" pagination={false} showHeader={false}
              scroll={{ x: 380 }}
              rowKey="name"
              loading={risksLoading}
              dataSource={risks?.switches ?? []}
              columns={[
                {
                  dataIndex: 'name', width: 215,
                  render: (v: string, r: LlmRiskSwitch) => (
                    <span>
                      <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span>
                      {r.locked ? (
                        <Tooltip title="不允许通过 RPC 关闭（底线开关）：只能改配置并重启 + 审计">
                          <Tag color="red" style={{ marginLeft: 6 }}>锁定</Tag>
                        </Tooltip>
                      ) : null}
                    </span>
                  ),
                },
                {
                  dataIndex: 'name',
                  render: (v: string) => <span className="gct-dim" style={{ fontSize: 11 }}>{NOTE_OF[v] ?? ''}</span>,
                },
                {
                  dataIndex: 'enabled', width: 56, align: 'right',
                  render: (v: boolean, r: LlmRiskSwitch) => (
                    <Switch
                      size="small"
                      checked={v}
                      disabled={!!r.locked}
                      loading={toggling === r.name}
                      onChange={(next) => void onToggle(r, next)}
                    />
                  ),
                },
              ]}
            />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            live 状态来自后端 <code>LLMGetRiskSwitches</code>；每次切换写 `CONFIG_CHANGED` 审计（「决策与审计」页可查）。
            <b>锁定项（红线 4/5）</b>：`haltOnDailyLoss` / `blockOnUnknownOrders` / `blockOnClockDrift`——只能改配置并重启。
            写操作受代理 <code>GCT_ALLOW_WRITES</code> 闸门。
          </Paragraph>
        </Card>
      </Col>
    </Row>
  )
}

/** 目录说明的静态注解（live 行只有机器字段，这里补中文语义） */
const NOTE_OF: Record<string, string> = Object.fromEntries(RISK_SWITCHES.map(([k, note]) => [k, note]))

/** 通知通道（W7-06）：配置就绪度 + operator 测试推送。凭据只报布尔。 */
function NotifyCard() {
  const { data, error } = useGetLlmNotifyStatusQuery(undefined, { pollingInterval: 60_000 })
  const [test, testState] = useTestLlmNotifyMutation()

  const onTest = async () => {
    try {
      const res = await test({ actor: 'webui-operator' }).unwrap()
      if (res.relayRunning) {
        message.success('测试通知已投递进通信中继——若几秒内未在 Telegram/webhook 收到，请查通道配置')
      } else {
        message.warning('通信中继未运行：事件被静默丢弃（设计如此——通知故障不拦执行）；先在 config 启用通道并重启')
      }
    } catch (err) {
      message.error(errorText(err))
    }
  }

  return (
    <Card
      size="small" style={{ marginTop: 12 }}
      title={<span className="gct-panel-title">通知通道（Telegram + webhook · Q19）</span>}
      extra={<Button size="small" loading={testState.isLoading} onClick={onTest}>发送测试通知</Button>}
    >
      {error ? <NotImplemented title="通知通道" error={error} /> : data ? (
        <>
          <Table
            size="small" rowKey="name" pagination={false} dataSource={data.channels ?? []}
            columns={[
              { title: '通道', dataIndex: 'name', width: 90 },
              {
                title: '启用', dataIndex: 'enabled', width: 72,
                render: (v: boolean) => <Tag color={v ? 'green' : 'default'} style={{ marginInline: 0 }}>{v ? '是' : '否'}</Tag>,
              },
              {
                title: '凭据', dataIndex: 'credentialSet', width: 96,
                render: (v: boolean) => v ? <Tag color="blue" style={{ marginInline: 0 }}>已设置</Tag> : <Tag style={{ marginInline: 0 }}>未设置</Tag>,
              },
              { title: '目标数', dataIndex: 'targets', width: 72, render: (v: number) => v || '—' },
              { title: '端点', dataIndex: 'endpointHost', render: (v?: string) => v ? <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span> : <span className="gct-dim">—</span> },
            ]}
          />
          <div className="gct-kv" style={{ marginTop: 8 }}><span>通信中继</span>
            <span>{data.relayRunning ? <Tag color="green" style={{ marginInline: 0 }}>运行中</Tag> : <Tag color="orange" style={{ marginInline: 0 }}>未运行</Tag>}</span>
          </div>
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 6, marginBottom: 0 }}>
            <b>凭据值从不回显</b>（只报是否已设置）；端点只显示 scheme://host——完整 URL 可能带密钥。
            通道在 <code>config.json</code> 的 <code>communications</code> 段配置，改后重启生效；Q19 下高危操作的确认回 Web UI 重新认证
            （<code>requireConfirmation=false</code>），通知只负责「告知」、永不阻塞执行。
          </Paragraph>
        </>
      ) : (
        <Paragraph className="gct-dim" style={{ fontSize: 12, marginBottom: 0 }}>读取中…</Paragraph>
      )}
    </Card>
  )
}

/** 三级停机与显式清除（W7-01 写侧）。清除需重新认证，且不自动恢复任何东西。 */
function HaltCard() {
  const { data, error } = useGetLlmHaltsQuery(undefined, { pollingInterval: 15_000 })
  const halts = data?.halts ?? []
  const [setHalt, setState] = useSetLlmHaltMutation()
  const [clearHalt, clearState] = useClearLlmHaltMutation()
  const [setOpen, setSetOpen] = useState(false)
  const [clearTarget, setClearTarget] = useState<LlmHaltRow>()
  const [form] = Form.useForm()
  const [clearForm] = Form.useForm()
  const levelWatch = Form.useWatch('level', form)

  return (
    <Card
      size="small"
      title={<span className="gct-panel-title">紧急停机（halt · 三级 + 显式清除）</span>}
      extra={
        <Button
          size="small" danger
          onClick={() => { form.setFieldsValue({ level: 'SOFT', actor: 'webui-operator', reason: '', marketId: undefined, flatten: false, confirmFlatten: false }); setSetOpen(true) }}
        >
          设置 halt
        </Button>
      }
    >
      {error ? <NotImplemented title="halt 控制" error={error} /> : (
        <>
          {halts.length === 0 ? (
            <div className="gct-kv"><span>当前级别</span><span><Tag color="green" style={{ marginInline: 0 }}>NONE（无活动停机）</Tag></span></div>
          ) : (
            <Table
              size="small" rowKey="id" pagination={false} dataSource={halts} scroll={{ x: 480 }}
              columns={[
                {
                  title: '级别', dataIndex: 'level', width: 86,
                  render: (v: string) => <Tag color={v === 'FULL' ? 'red' : v === 'MARKET' ? 'volcano' : 'orange'} style={{ marginInline: 0 }}>{v}</Tag>,
                },
                { title: '市场', dataIndex: 'marketId', render: (v?: string) => v ? <span className="gct-mono" style={{ fontSize: 11 }}>{v}</span> : <span className="gct-dim">全部</span> },
                { title: '原因', dataIndex: 'reason', ellipsis: true },
                { title: '设置人', dataIndex: 'setBy', width: 90, render: (v?: string) => v ?? '—' },
                { title: '时刻', dataIndex: 'setAt', width: 158, render: (v?: string) => <span className="gct-mono" style={{ fontSize: 11 }}>{v ? v.replace('T', ' ').slice(0, 19) : '—'}</span> },
                {
                  title: '操作', width: 84,
                  render: (_: unknown, r: LlmHaltRow) => (
                    <Button size="small" onClick={() => { clearForm.setFieldsValue({ actor: 'webui-operator', reason: '', password: '' }); setClearTarget(r) }}>清除</Button>
                  ),
                },
              ]}
            />
          )}
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
            <b>SOFT</b> 阻止新开仓但允许 reduce-only 平仓与保护性止损（挂单保留）；<b>MARKET</b> 撤机器人挂单（仅 <code>gct-llm-</code> 前缀，用户手动单不可撤）；
            <b> FULL</b> 阻止一切模型驱动动作（确定性保护性止损仍可运行，平仓需显式二次确认）。
            清除 halt 需<b>重新认证</b>，且<b>策略不会自动恢复、审批不会自动恢复</b>——恢复是逐个的动作。写操作受代理 <code>GCT_ALLOW_WRITES</code> 闸门。
          </Paragraph>
        </>
      )}

      <Modal
        title="设置停机（落 halt → 停策略 → 撤单 →（FULL）平仓 → 审计）"
        open={setOpen} onCancel={() => setSetOpen(false)} confirmLoading={setState.isLoading} width={560}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            const res = await setHalt({
              level: v.level, marketId: v.marketId, reason: v.reason, actor: v.actor,
              flatten: v.flatten, confirmFlatten: v.confirmFlatten,
            }).unwrap()
            message.success(`已停机 ${res.halt?.level}：暂停 ${res.suspendedStrategies ?? 0} 个策略 / 撤单 ${res.canceledOrders ?? 0}${res.flattenedPositions ? ` / 平仓 ${res.flattenedPositions} 个仓位` : ''}`)
            setSetOpen(false)
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      >
        <Form form={form} layout="vertical" initialValues={{ level: 'SOFT', actor: 'webui-operator' }}>
          <Form.Item name="level" label="级别" rules={[{ required: true }]}>
            <Select options={[{ value: 'SOFT' }, { value: 'MARKET' }, { value: 'FULL' }]} />
          </Form.Item>
          {levelWatch === 'MARKET' ? (
            <Form.Item name="marketId" label="市场（MARKET 必填）" rules={[{ required: true, message: '请输入市场 id' }]}>
              <Input placeholder="Binance:usdtmarginedfutures:BTC_USDT" />
            </Form.Item>
          ) : null}
          <Form.Item name="reason" label="原因（必录进审计）" rules={[{ required: true, message: '请输入原因' }]}><Input /></Form.Item>
          <Form.Item name="actor" label="操作者" rules={[{ required: true }]}><Input /></Form.Item>
          {levelWatch === 'FULL' ? (
            <>
              <Form.Item name="flatten" valuePropName="checked">
                <Checkbox>同时 reduce-only 平掉全部仓位</Checkbox>
              </Form.Item>
              <Form.Item name="confirmFlatten" valuePropName="checked">
                <Checkbox>确认平仓（显式二次确认——不是走过场）</Checkbox>
              </Form.Item>
            </>
          ) : null}
        </Form>
      </Modal>

      <Modal
        title="清除停机（重新认证）" open={Boolean(clearTarget)} onCancel={() => setClearTarget(undefined)}
        confirmLoading={clearState.isLoading}
        onOk={async () => {
          const v = await clearForm.validateFields()
          try {
            await clearHalt({ id: clearTarget?.id ?? '', actor: v.actor, reason: v.reason, operatorPassword: v.password }).unwrap()
            message.success('已清除 halt——策略与审批不会自动恢复，请逐个重新启用')
            setClearTarget(undefined)
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      >
        <Alert
          type="warning" showIcon style={{ marginBottom: 10 }}
          message="清除不自动恢复任何东西"
          description={<span style={{ fontSize: 12 }}>策略保持 PAUSED、被撤销的审批保持撤销——恢复是个别的、有意的动作。</span>}
        />
        <Form form={clearForm} layout="vertical" initialValues={{ actor: 'webui-operator' }}>
          <Form.Item name="reason" label="清除原因（必录）" rules={[{ required: true, message: '请输入原因' }]}><Input /></Form.Item>
          <Form.Item name="actor" label="操作者" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="password" label="操作者口令（重新认证）" rules={[{ required: true, message: '请输入口令' }]}><Input.Password /></Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}

/** 运行模式切换（08 §3.6 · M5-12 切换程序）：重新认证 + （LIVE 另需承认短语）
 * 全由服务端强制；审计/通知/持久化都在切换程序内。写操作受代理 GCT_ALLOW_WRITES 闸门。 */
function ModeCard() {
  const { data, error } = useGetLlmStatusQuery(undefined, { pollingInterval: 15_000 })
  const [setMode, setState] = useSetLlmRunModeMutation()
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()
  const targetWatch = Form.useWatch('runMode', form)
  const current = data?.runMode ?? '—'

  return (
    <Card
      size="small"
      style={{ marginTop: 12 }}
      title={<span className="gct-panel-title">运行模式（重新认证切换）</span>}
      extra={
        <Button
          size="small"
          onClick={() => { form.setFieldsValue({ runMode: current === '—' ? 'SHADOW' : current, actor: 'webui-operator', password: '', acknowledged: '' }); setOpen(true) }}
        >
          切换模式
        </Button>
      }
    >
      {error ? <NotImplemented title="运行模式" error={error} /> : (
        <div className="gct-kv">
          <span>当前模式</span>
          <span><Tag style={{ marginInline: 0 }} color={current === 'LIVE' ? 'red' : current === 'TESTNET' ? 'orange' : 'blue'}>{current}</Tag></span>
        </div>
      )}
      <Modal
        title="切换运行模式（重新认证；LIVE 需承认短语）" open={open} onCancel={() => setOpen(false)}
        confirmLoading={setState.isLoading}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await setMode({ runMode: v.runMode, actor: v.actor, password: v.password, acknowledged: v.acknowledged ?? '' }).unwrap()
            message.success(`已切换到 ${v.runMode}（已审计并通知）`)
            setOpen(false)
          } catch (err) {
            message.error(errorText(err))
          }
        }}
      >
        <Alert
          type="warning" showIcon style={{ marginBottom: 10 }}
          message="降级与升级同为有意动作"
          description={<span style={{ fontSize: 12 }}>切换会重认证、写审计并推送；落定写入配置（持久化失败即回滚）。切到 LIVE 需输入承认短语。</span>}
        />
        <Form form={form} layout="vertical" initialValues={{ runMode: 'SHADOW', actor: 'webui-operator' }}>
          <Form.Item name="runMode" label="目标模式" rules={[{ required: true }]}>
            <Select options={[{ value: 'READ_ONLY' }, { value: 'SHADOW' }, { value: 'PAPER' }, { value: 'TESTNET' }, { value: 'LIVE' }]} />
          </Form.Item>
          {targetWatch === 'LIVE' ? (
            <Form.Item name="acknowledged" label="LIVE 承认短语（输入 I_UNDERSTAND）" rules={[{ required: true, message: '请输入承认短语' }]}>
              <Input placeholder="I_UNDERSTAND" />
            </Form.Item>
          ) : null}
          <Form.Item name="actor" label="操作者（审计留痕）" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="password" label="操作者口令（重新认证）" rules={[{ required: true, message: '请输入口令' }]}><Input.Password /></Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
