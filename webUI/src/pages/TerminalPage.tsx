import { Card, Col, Row, Segmented, Space, Statistic, Tag } from 'antd'
import { useAtom } from 'jotai'
import { barsAtom, intervalAtom, selectedMarketAtom } from '@/state/atoms'
import { useGetLatestFundingRateQuery, useGetTickerQuery } from '@/app/api/gctApi'
import { MarketList } from '@/components/MarketList'
import { PriceChart } from '@/components/PriceChart'
import { OrderbookPanel } from '@/components/OrderbookPanel'
import { AccountPanel } from '@/components/AccountPanel'
import { IndicatorFacts } from '@/components/IndicatorFacts'
import { SlippageSimulator } from '@/components/SlippageSimulator'
import { QueryError } from '@/components/NotImplemented'
import { TOKENS, pnlClass } from '@/theme'

const n = (v: unknown) => { const x = Number(v); return Number.isFinite(x) ? x : null }
const fmt = (v: unknown, d = 2) => { const x = n(v); return x === null ? '—' : x.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d }) }
const fmtS = (v: unknown) => { const x = n(v); return x === null ? '—' : Math.abs(x) < 1 ? x.toPrecision(4) : fmt(x, 2) }

const INTERVALS = [
  { label: '15m', value: 900 },
  { label: '1h', value: 3600 },
  { label: '4h', value: 14400 },
  { label: '1d', value: 86400 },
]
const BAR_OPTIONS = [
  { label: '120', value: 120 },
  { label: '200', value: 200 },
  { label: '300', value: 300 },
]

function PriceHeader() {
  const [market] = useAtom(selectedMarketAtom)
  const { data: t, error } = useGetTickerQuery(market, { pollingInterval: 15_000 })
  const { data: f } = useGetLatestFundingRateQuery(market, { pollingInterval: 300_000 })

  const last = n(t?.last), open = n(t?.open24Hour) ?? n(t?.open)
  const chg = n(t?.percentChange24Hour) || (last && open ? ((last - open) / open) * 100 : null)
  const lr = n(f?.rate?.latestRate?.rate)
  const bb = n(t?.bid), ba = n(t?.ask)
  const spreadBps = bb != null && ba != null && bb > 0 ? ((ba - bb) / bb) * 1e4 : null

  if (error) return <QueryError title="行情" error={error} />

  return (
    <Card size="small" styles={{ body: { paddingTop: 10 } }}>
      <Row gutter={[16, 8]} align="middle">
        <Col flex="none">
          <div className="gct-dim" style={{ fontSize: 11 }}>最新价 · {market.base}-{market.quote}</div>
          <Space align="baseline" size={10}>
            <span className="gct-mono" style={{ fontSize: 26, lineHeight: 1.1 }}>{fmt(last)}</span>
            <span className={pnlClass(chg)} style={{ fontSize: 13 }}>
              {chg === null ? '—' : `${chg >= 0 ? '+' : ''}${chg.toFixed(2)}%`}
            </span>
          </Space>
        </Col>
        <Col flex="auto">
          <Row gutter={[20, 4]}>
            {[
              ['24h 高', fmt(t?.high)],
              ['24h 低', fmt(t?.low)],
              ['买一', fmt(bb)],
              ['卖一', fmt(ba)],
              ['价差', spreadBps != null ? `${spreadBps.toFixed(2)} bps` : '—'],
              ['24h 量', fmtS(t?.baseVolume)],
              ['成交额', fmtS(t?.quoteVolume)],
            ].map(([k, v]) => (
              <Col key={k} xs={8} xl={3}>
                <div className="gct-dim" style={{ fontSize: 11 }}>{k}</div>
                <div className="gct-mono">{v}</div>
              </Col>
            ))}
          </Row>
        </Col>
        <Col flex="none" style={{ minWidth: 150, textAlign: 'right' }}>
          <Statistic
            title={<span className="gct-dim" style={{ fontSize: 11 }}>资金费率</span>}
            value={lr === null ? '—' : lr * 100}
            precision={4}
            suffix="%"
            styles={{ content: { fontSize: 18, color: lr === null ? undefined : lr > 0 ? TOKENS.down : TOKENS.up } }}
          />
          <div className="gct-dim" style={{ fontSize: 10 }}>
            {lr === null ? '—' : `年化 ${(lr * 3 * 365 * 100).toFixed(2)}%`} · 下次 {f?.rate?.timeOfNextRate ?? '—'}
          </div>
        </Col>
      </Row>
    </Card>
  )
}

/** 交易台（W3）：合约列表 + 价格条 + K 线/指标 + 盘口 + 账户 + 派生事实 + 滑点模拟 */
export default function TerminalPage() {
  const [interval, setInterval] = useAtom(intervalAtom)
  const [bars, setBars] = useAtom(barsAtom)

  return (
    <Row gutter={[12, 12]}>
      <Col span={24}><PriceHeader /></Col>

      <Col xs={24} xl={5}>
        <MarketList />
      </Col>

      <Col xs={24} xl={13}>
        <Space size={8} style={{ marginBottom: 8 }} wrap>
          <span className="gct-dim" style={{ fontSize: 11 }}>周期</span>
          <Segmented size="small" value={interval} onChange={(v) => setInterval(Number(v))} options={INTERVALS} />
          <span className="gct-dim" style={{ fontSize: 11 }}>K 线数</span>
          <Segmented size="small" value={bars} onChange={(v) => setBars(Number(v))} options={BAR_OPTIONS} />
          <Tag style={{ fontSize: 11 }}>1h 扫描 + 4h 确认（Q5b）</Tag>
          <Tag style={{ fontSize: 11 }}>新鲜度窗口 60s（Q9）</Tag>
        </Space>
        <PriceChart />
        <div style={{ marginTop: 12 }}><AccountPanel /></div>
      </Col>

      <Col xs={24} xl={6}>
        <OrderbookPanel />
        <div style={{ marginTop: 12 }}><IndicatorFacts /></div>
        <div style={{ marginTop: 12 }}><SlippageSimulator /></div>
      </Col>
    </Row>
  )
}
