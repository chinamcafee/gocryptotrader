import { Card, Table } from 'antd'
import { useState } from 'react'
import { useAtomValue } from 'jotai'
import { selectedMarketAtom } from '@/state/atoms'
import { useGetOrderbookQuery } from '@/app/api/gctApi'
import { TOKENS } from '@/theme'

const n = (v: unknown) => { const x = Number(v); return Number.isFinite(x) ? x : null }
const fmt = (v: unknown, d = 2) => { const x = n(v); return x === null ? '—' : x.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d }) }
const fmtS = (v: unknown) => { const x = n(v); return x === null ? '—' : Math.abs(x) < 1 ? x.toPrecision(4) : fmt(x, 2) }

function LevelTable({ levels, side, max, header }: { levels: Array<{ price?: number; amount?: number }>; side: 'ask' | 'bid'; max: number; header: boolean }) {
  const color = side === 'ask' ? TOKENS.down : TOKENS.up
  let cum = 0
  return (
    <Table
      size="small" pagination={false} showHeader={header}
      dataSource={levels.map((l, i) => { cum += n(l.amount) ?? 0; return { key: `${side}${i}`, ...l, cum } })}
      columns={[
        { title: header ? '价格' : '', dataIndex: 'price', align: 'right', width: 100, render: (v: number) => <span className="gct-mono" style={{ color }}>{fmt(v)}</span> },
        {
          title: header ? '数量' : '', dataIndex: 'amount', align: 'right', className: 'gct-depth-cell',
          render: (v: number) => (
            <>
              <span className="gct-mono">{fmtS(v)}</span>
              <i className="gct-depth-bar" style={{ width: `${((n(v) ?? 0) / max) * 100}%`, background: color }} />
            </>
          ),
        },
        { title: header ? '累计' : '', dataIndex: 'cum', align: 'right', width: 90, render: (v: number) => <span className="gct-mono gct-dim">{fmtS(v)}</span> },
      ]}
    />
  )
}

/** 盘口面板（W3-04）。币安永续 REST 实测可拿 1000 档。 */
export function OrderbookPanel() {
  const market = useAtomValue(selectedMarketAtom)
  const [depth, setDepth] = useState(15)
  const { data: book } = useGetOrderbookQuery(market, { pollingInterval: 15_000 })

  const bids = (book?.bids ?? []).slice(0, depth)
  const asks = (book?.asks ?? []).slice(0, depth)
  const max = Math.max(...[...bids, ...asks].map((l) => n(l.amount) ?? 0), 1e-9)
  const bb = n(bids[0]?.price), ba = n(asks[0]?.price)
  const spread = bb !== null && ba !== null ? ba - bb : null
  const mid = bb !== null && ba !== null ? (ba + bb) / 2 : null

  return (
    <Card
      size="small"
      title={<span className="gct-panel-title">盘口 · 前 {depth} 档</span>}
      extra={
        <span className="gct-dim" style={{ fontSize: 11 }}>
          {[5, 15, 30].map((d) => (
            <a key={d} onClick={() => setDepth(d)} style={{ marginLeft: 8, color: d === depth ? TOKENS.focus : undefined }}>{d}</a>
          ))}
        </span>
      }
    >
      <LevelTable levels={asks.slice().reverse()} side="ask" max={max} header={false} />
      <div className="gct-kv" style={{ padding: '6px 0' }}>
        <span>价差</span>
        <span className="gct-num">{spread === null || mid === null ? '—' : `${fmt(spread)} (${((spread / mid) * 1e4).toFixed(2)} bps)`}</span>
      </div>
      <LevelTable levels={bids} side="bid" max={max} header={false} />
      <div className="gct-dim" style={{ fontSize: 11, marginTop: 8 }}>
        深度 买 {(book?.bids ?? []).length} 档 / 卖 {(book?.asks ?? []).length} 档（币安永续 REST 实测可达 1000 档，非 20 档限制）
      </div>
    </Card>
  )
}
