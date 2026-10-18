import { Alert, Card, Col, Input, Row, Segmented, Space, Switch, Table, Tag, Tooltip, Typography, message } from 'antd'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useSetAtom } from 'jotai'
import { errorText, isNotImplemented, useGetExchangePairsQuery, useSetLlmMarketTrackedMutation } from '@/app/api/gctApi'
import {
  EXCHANGE_WEIGHT_PER_MINUTE, EXCLUSION_LABEL, buildRotationPlan, classifySymbols, planFits,
  type SymbolRow,
} from '@/app/api/coverage'
import { selectedMarketAtom } from '@/state/atoms'

const { Paragraph, Text } = Typography

type Filter = 'IN_SCOPE' | 'ENABLED' | 'EXCLUDED'

const FILTERS: Array<{ value: Filter; label: string }> = [
  { value: 'IN_SCOPE', label: '在范围内' },
  { value: 'ENABLED', label: '已启用' },
  { value: 'EXCLUDED', label: '范围外' },
]

const REASON_COLOR: Record<string, string> = {
  DATED_FUTURE: 'purple',
  OTHER_QUOTE: 'default',
  EMPTY: 'default',
}

const num = (v: number, digits = 1) =>
  Number.isFinite(v) ? v.toLocaleString('en-US', { maximumFractionDigits: digits }) : '—'

/**
 * 合约覆盖（W7-04 的数据面 + Q2 全量覆盖的可视化）。
 *
 * 数据全部来自**真实端点** `getexchangepairs`（实测为 POST，返回
 * `supportedAssets.usdtmarginedfutures.{availablePairs,enabledPairs}`）。
 * 轮询进度/权重占用等"运行态"字段仍需 `LLMGetCoverageStatus` RPC（被 proto 工具链阻塞），
 * 因此本页显示的是**计划值**（由 llm/coverage 的算术镜像算出），不冒充运行值。
 */
export default function MarketsPage() {
  const { data, error, isFetching } = useGetExchangePairsQuery()
  const [setTracked] = useSetLlmMarketTrackedMutation()
  const [pendingTrack, setPendingTrack] = useState<string>()
  const setMarket = useSetAtom(selectedMarketAtom)
  const nav = useNavigate()
  const [filter, setFilter] = useState<Filter>('IN_SCOPE')
  const [query, setQuery] = useState('')

  // N11 对话/手动同源：启用=进入 SyncManager 轮询集与覆盖轮；停用=离开。
  const onToggleTracked = async (row: SymbolRow, next: boolean) => {
    setPendingTrack(row.symbol)
    try {
      const res = await setTracked({
        marketId: `Binance:usdtmarginedfutures:${row.base}_${row.quote}`,
        tracked: next,
        actor: 'webui-operator',
      }).unwrap()
      message.success(`${res.tracked ? '已启用' : '已停用'} ${res.marketId}`)
    } catch (err) {
      message.error(isNotImplemented(err) ? '写操作被代理闸门拦截（GCT_ALLOW_WRITES=false）' : errorText(err))
    } finally {
      setPendingTrack(undefined)
    }
  }

  const uf = data?.supportedAssets?.usdtmarginedfutures
  const { rows, report } = useMemo(
    () => classifySymbols(uf?.availablePairs, uf?.enabledPairs),
    [uf?.availablePairs, uf?.enabledPairs],
  )
  const plan = useMemo(() => buildRotationPlan(report.selected), [report.selected])

  const enabledCount = useMemo(() => rows.filter((r) => r.enabled).length, [rows])
  const shown = useMemo(() => {
    const q = query.trim().toUpperCase()
    return rows.filter((r) => {
      if (filter === 'IN_SCOPE' && !r.inScope) return false
      if (filter === 'ENABLED' && !r.enabled) return false
      if (filter === 'EXCLUDED' && r.inScope) return false
      if (q && !r.symbol.toUpperCase().includes(q)) return false
      return true
    })
  }, [rows, filter, query])

  const openMarket = (row: SymbolRow) => {
    setMarket({ base: row.base, quote: row.quote })
    nav('/terminal')
  }

  return (
    <Row gutter={[12, 12]}>
      <Col span={24}>
        <Card size="small" title={<span className="gct-panel-title">合约覆盖范围（Q2 定案：全量 USDT 本位永续）</span>}>
          {error ? (
            <Alert
              type="warning" showIcon
              message="无法读取交易对列表"
              description="本地代理不可达或网关未响应。请运行 `pnpm proxy`（或 `pnpm dev:all`）。"
            />
          ) : (
            <>
              <Row gutter={[12, 12]}>
                <Col xs={12} md={6}>
                  <div className="gct-kv"><span>交易所提供</span><span className="gct-num">{report.received}</span></div>
                  <Paragraph className="gct-dim" style={{ fontSize: 11, margin: 0 }}>
                    含 USDC/USD1/法币计价与代币化股票代码形状
                  </Paragraph>
                </Col>
                <Col xs={12} md={6}>
                  <div className="gct-kv">
                    <span>在范围内</span>
                    <span className="gct-num gct-up">{report.selected}</span>
                  </div>
                  <Paragraph className="gct-dim" style={{ fontSize: 11, margin: 0 }}>
                    尾部 `_USDT` 且非交割合约
                  </Paragraph>
                </Col>
                <Col xs={12} md={6}>
                  <div className="gct-kv">
                    <span>已启用</span>
                    <span className="gct-num">{enabledCount}</span>
                  </div>
                  <Paragraph className="gct-dim" style={{ fontSize: 11, margin: 0 }}>
                    其余按需 <code>pair enable</code>
                  </Paragraph>
                </Col>
                <Col xs={12} md={6}>
                  <div className="gct-kv">
                    <span>范围外</span>
                    <span className="gct-num">{report.excluded}</span>
                  </div>
                  <Paragraph className="gct-dim" style={{ fontSize: 11, margin: 0 }}>
                    交割 {report.datedFutures} · 非 USDT 计价 {report.otherQuote} · 空 {report.empty}
                  </Paragraph>
                </Col>
              </Row>

              {report.examples.length > 0 && (
                <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 10, marginBottom: 0 }}>
                  范围外示例：
                  {report.examples.map((s) => (
                    <Tag key={s} style={{ marginInlineStart: 4 }} className="gct-mono">{s}</Tag>
                  ))}
                  {report.excluded > report.examples.length ? `…等共 ${report.excluded} 个` : ''}
                </Paragraph>
              )}

              <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 8, marginBottom: 0 }}>
                <b>为什么范围外也要计数</b>：「看了 789 个里的 588 个」是个可核对的陈述；
                静默丢掉 201 个则没人能发现范围定错了。
              </Paragraph>
            </>
          )}
        </Card>
      </Col>

      <Col xs={24} xl={14}>
        <Card
          size="small"
          title={<span className="gct-panel-title">合约列表</span>}
          extra={
            <Space size={8}>
              <Input.Search
                size="small" allowClear placeholder="搜索，如 SOL / 1000PEPE"
                style={{ width: 190 }} value={query} onChange={(e) => setQuery(e.target.value)}
              />
              <Segmented
                size="small" value={filter} options={FILTERS}
                onChange={(v) => setFilter(v as Filter)}
              />
            </Space>
          }
        >
          <Table<SymbolRow>
            size="small" rowKey="symbol"
            // 列宽合计 556px > 窄屏可用宽度。scroll.x 让表格在卡片内部横向滚动，
            // 而不是把整页撑出横向滚动条（这是 390px 实测溢出的直接原因之一）。
            scroll={{ x: 560 }}
            dataSource={shown}
            loading={isFetching}
            // 分页给 20/页：588 行全渲染会拖慢表格，且逐行滚动不看
            pagination={{ pageSize: 20, size: 'small', showSizeChanger: false, showTotal: (t) => `共 ${t} 个` }}
            onRow={(r) => ({ onClick: () => openMarket(r), style: { cursor: 'pointer' } })}
            columns={[
              {
                title: '合约', dataIndex: 'symbol', width: 210,
                render: (v: string, r) => (
                  <Space size={6}>
                    <span className="gct-mono">{v}</span>
                    {!r.inScope ? <Tag color={REASON_COLOR[r.reason ?? ''] ?? 'default'}>{EXCLUSION_LABEL[r.reason!]}</Tag> : null}
                  </Space>
                ),
              },
              {
                title: 'base / quote', width: 150,
                render: (_, r) => <span className="gct-dim gct-mono" style={{ fontSize: 11 }}>{r.base} / {r.quote}</span>,
              },
              {
                title: '状态', width: 170,
                render: (_, r) =>
                  r.inScope ? (
                    <Space size={6} onClick={(e) => e.stopPropagation()}>
                      <Switch
                        size="small"
                        checked={r.enabled}
                        loading={pendingTrack === r.symbol}
                        onChange={(next) => void onToggleTracked(r, next)}
                      />
                      {r.enabled ? (
                        <Tag color="green">已启用</Tag>
                      ) : (
                        <Tooltip
                          title={
                            <span>
                              启用后该合约进入 SyncManager 轮询集与覆盖轮，可查询、可筛选；
                              停用的合约在 N10 按需取数落地前无法取数。
                            </span>
                          }
                        >
                          <Tag color="orange">未启用</Tag>
                        </Tooltip>
                      )}
                    </Space>
                  ) : (
                    <span className="gct-dim">—</span>
                  ),
              },
              {
                title: '操作', width: 76, align: 'right',
                render: (_, r) => (r.inScope ? <a onClick={(e) => { e.stopPropagation(); openMarket(r) }}>看盘</a> : null),
              },
            ]}
          />
        </Card>
      </Col>

      <Col xs={24} xl={10}>
        <Card size="small" title={<span className="gct-panel-title">第一层：全量保温轮询预算</span>}>
          <div className="gct-kv"><span>覆盖合约</span><span className="gct-num">{plan.markets}</span></div>
          <div className="gct-kv"><span>轮询周期</span><span className="gct-num">{plan.cycleSeconds / 60} 分钟</span></div>
          <div className="gct-kv">
            <span>单周期总权重</span>
            <span className="gct-num">{num(plan.rotationTotal, 0)}</span>
          </div>
          <div className="gct-kv"><span>轮询速率</span><span className="gct-num">{num(plan.rotationPerMinute)} /min</span></div>
          <div className="gct-kv">
            <span>整点重载</span>
            <span className="gct-num">{num(plan.reloadTotal, 0)} → {num(plan.reloadPerMinute)} /min</span>
          </div>
          <div className="gct-kv">
            <span>峰值（轮询+重载）</span>
            <span className={`gct-num ${planFits(plan) ? 'gct-up' : 'gct-down'}`}>{num(plan.peakPerMinute)} /min</span>
          </div>
          <div className="gct-kv">
            <span>预算（{Math.round(plan.budgetFraction * 100)}% × {EXCHANGE_WEIGHT_PER_MINUTE}）</span>
            <span className="gct-num">{plan.budgetPerMinute} /min</span>
          </div>
          <div className="gct-kv">
            <span>留给决策取鲜</span>
            <span className={`gct-num ${plan.headroomPerMinute > 0 ? 'gct-up' : 'gct-down'}`}>
              {num(plan.headroomPerMinute)} /min
            </span>
          </div>
          <div className="gct-kv"><span>增量匀速间隔</span><span className="gct-num">{plan.incrementSpacingMs} ms</span></div>

          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 10, marginBottom: 0 }}>
            <b>这是计划值，不是运行值。</b>运行态的轮询进度、最旧数据年龄、实际权重占用需要
            <code>LLMGetCoverageStatus</code> RPC（被 proto 工具链阻塞，见 TASKS.md §0.1）；
            执行器本身是 <code>M2-14</code>。
            算术语义与 <code>llm/coverage</code> 一致：单周期 = 批量 ticker 40 + 批量资金费率 10 + 每合约增量 1；
            整点重载 = 每合约 weight 2（<code>limit=250</code>）摊到 5 分钟。
          </Paragraph>
        </Card>

        <Card size="small" style={{ marginTop: 12 }} title={<span className="gct-panel-title">范围与授权的三道闸</span>}>
          <Paragraph style={{ fontSize: 12, marginBottom: 8 }}>
            <b>1. available</b> — 交易所给的全集。本页的 588 个在范围内的永续。
          </Paragraph>
          <Paragraph style={{ fontSize: 12, marginBottom: 8 }}>
            <b>2. enabled</b> — SyncManager 实际轮询的集合。<code>enabled</code> 越长权重消耗越大，
            所以默认只常驻少数主流币，其余按需 <code>gctcli pair enable</code>（写操作，需显式触发）。
          </Paragraph>
          <Paragraph style={{ fontSize: 12, marginBottom: 8 }}>
            <b>3. market_trade_grants</b> — 市场授权，只有被授权的市场才能下单。
            <Text className="gct-warn">「能看」远不等于「能交易」。</Text>
          </Paragraph>
          <Alert
            type="warning" showIcon
            message="未启用的合约目前查不了（实测）"
            description={
              <span style={{ fontSize: 11 }}>
                在本页选一个未启用的合约会走到交易台并报错 <code>currency not enabled</code>——
                网关的取数路径会校验 <code>enabled</code> 列表。
                Q4=B 定案的「按需只读」需要 <code>M2-01</code> 提供绕过该校验的取数路径；
                届时快照标记 <code>onDemand = true</code> 且<b>永远不进决策流水线</b>——
                风控要求市场在批准列表里。
              </span>
            }
          />
        </Card>
      </Col>
    </Row>
  )
}
