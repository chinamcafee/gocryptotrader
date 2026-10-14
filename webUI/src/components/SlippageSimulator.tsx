import { Button, Card, Col, InputNumber, Row, Select, Statistic, Tag } from 'antd'
import { useState } from 'react'
import { useAtomValue } from 'jotai'
import { selectedMarketAtom } from '@/state/atoms'
import { useGetOrderbookQuery } from '@/app/api/gctApi'
import { TOKENS } from '@/theme'

/**
 * 滑点模拟器（W3-06）。纯前端按当前盘口逐档累加，不发额外请求、不下单。
 * 依据：docs/llm-dev/07 §5.2 D（成交保护类检查）与 04 §4.4。
 */
export function SlippageSimulator() {
  const market = useAtomValue(selectedMarketAtom)
  const { data: book } = useGetOrderbookQuery(market, { pollingInterval: 15_000 })
  const [amount, setAmount] = useState(1000)
  const [side, setSide] = useState<'buy' | 'sell'>('buy')

  const levels = side === 'buy' ? (book?.asks ?? []) : (book?.bids ?? [])
  const best = side === 'buy' ? book?.asks?.[0]?.price : book?.bids?.[0]?.price
  const opposite = side === 'buy' ? book?.bids?.[0]?.price : book?.asks?.[0]?.price
  const mid = best != null && opposite != null ? (Number(best) + Number(opposite)) / 2 : null

  let spent = 0, got = 0, used = 0, first: number | null = null, lastLevel: number | null = null
  for (const lv of levels) {
    if (spent >= amount) break
    const price = Number(lv.price)
    const avail = Number(lv.amount)
    if (!Number.isFinite(price) || !Number.isFinite(avail) || price <= 0) continue
    if (first === null) first = price
    const cost = price * avail
    const take = cost <= amount - spent ? avail : (amount - spent) / price
    spent += take * price
    got += take
    lastLevel = price
    used++
  }
  const avg = got > 0 ? spent / got : null
  const slipBps = avg != null && mid != null && mid > 0 ? (Math.abs(avg - mid) / mid) * 1e4 : null

  return (
    <Card size="small" title={<span className="gct-panel-title">滑点模拟（本地按盘口计算）</span>}>
      <Row gutter={8} align="middle">
        <Col flex="120px">
          <InputNumber
            size="small" min={1} step={100} value={amount} style={{ width: '100%' }}
            onChange={(v) => setAmount(Number(v) || 0)}
          />
        </Col>
        <Col flex="none"><span className="gct-dim">USDT</span></Col>
        <Col flex="130px">
          <Select
            size="small" value={side} style={{ width: '100%' }}
            onChange={setSide}
            options={[
              { value: 'buy', label: '买入（吃卖单）' },
              { value: 'sell', label: '卖出（吃买单）' },
            ]}
          />
        </Col>
        <Col flex="none"><Button size="small" disabled>自动计算</Button></Col>
      </Row>

      <div style={{ marginTop: 10 }}>
        <div className="gct-kv"><span>可成交数量</span><span className="gct-num">{got > 0 ? got.toPrecision(6) : '—'} {market.base}</span></div>
        <div className="gct-kv"><span>成交均价</span><span className="gct-num">{avg != null ? avg.toFixed(2) : '—'}</span></div>
        <div className="gct-kv"><span>起始价 → 结束价</span>
          <span className="gct-num">{first != null ? first.toFixed(2) : '—'} → {lastLevel != null ? lastLevel.toFixed(2) : '—'}</span>
        </div>
        <div className="gct-kv"><span>估价基准（中间价）</span><span className="gct-num">{mid != null ? mid.toFixed(2) : '—'}</span></div>
        <div className="gct-kv">
          <span>滑点</span>
          <span className="gct-num" style={{ color: slipBps == null ? undefined : slipBps > 20 ? TOKENS.down : slipBps > 5 ? TOKENS.warn : TOKENS.up }}>
            {slipBps != null ? `${slipBps.toFixed(2)} bps` : '—'}
          </span>
        </div>
        <div className="gct-kv"><span>吃掉的档位</span><span className="gct-num">{used} / {levels.length}</span></div>
      </div>

      <div style={{ marginTop: 8 }}>
        <Statistic
          title={<span className="gct-dim" style={{ fontSize: 11 }}>风控上限（小额档）</span>}
          value={20} suffix="bps"
          styles={{ content: { fontSize: 14 } }}
        />
        {slipBps != null ? (
          <Tag color={slipBps > 20 ? 'red' : 'green'} style={{ marginTop: 4 }}>
            {slipBps > 20 ? '将触发 RISK_SLIPPAGE_EXCEEDED' : '通过滑点检查'}
          </Tag>
        ) : null}
      </div>

      <div className="gct-dim" style={{ fontSize: 11, marginTop: 8 }}>
        纯前端计算，不产生任何写操作。真实提交前风控会在服务端按同一盘口重新复核（≤5s 新鲜度）。
      </div>
    </Card>
  )
}
