import { Card, Col, Row, Tag } from 'antd'
import { useAtomValue } from 'jotai'
import { selectedMarketAtom } from '@/state/atoms'
import {
  useGetAccountBalancesQuery, useGetCollateralModeQuery,
  useGetLeverageQuery, useGetPositionSummaryQuery,
} from '@/app/api/gctApi'
import { pnlClass } from '@/theme'

const n = (v: unknown) => { const x = Number(v); return Number.isFinite(x) ? x : null }
const fmt = (v: unknown, d = 2) => { const x = n(v); return x === null ? '—' : x.toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d }) }
const fmtS = (v: unknown) => { const x = n(v); return x === null ? '—' : Math.abs(x) < 1 ? x.toPrecision(4) : fmt(x, 2) }

/** 账户与仓位面板（W3-07）。数据源：getaccountbalances / getfuturespositionssummary / getleverage / getcollateralmode。 */
export function AccountPanel() {
  const market = useAtomValue(selectedMarketAtom)
  const { data: bal } = useGetAccountBalancesQuery(undefined, { pollingInterval: 60_000 })
  const { data: pos } = useGetPositionSummaryQuery(market, { pollingInterval: 30_000 })
  const { data: lev } = useGetLeverageQuery(market, { pollingInterval: 300_000 })
  const { data: mode } = useGetCollateralModeQuery(undefined, { pollingInterval: 300_000 })

  const usdt = (bal?.accounts?.[0]?.currencies ?? []).find((c) => c.currency === 'USDT')
  const st = pos?.positionStats
  const size = n(st?.currentSize)

  return (
    <Card size="small" title={<span className="gct-panel-title">账户与仓位（USDT-M）</span>}>
      <Row gutter={12}>
        <Col span={12}>
          <div className="gct-kv"><span>可用余额</span><span className="gct-num">{fmtS(usdt?.free)}</span></div>
          <div className="gct-kv"><span>冻结 hold</span><span className="gct-num">{fmtS(usdt?.hold)}</span></div>
          <div className="gct-kv"><span>总额 total</span><span className="gct-num">{fmtS(usdt?.totalValue)}</span></div>
          <div className="gct-kv"><span>总抵押</span><span className="gct-num">{fmtS(st?.totalCollateral)}</span></div>
          <div className="gct-kv"><span>可用抵押</span><span className="gct-num">{fmtS(st?.freeCollateral)}</span></div>
          <div className="gct-kv"><span>已用抵押</span><span className="gct-num">{fmtS(st?.collateralUsed)}</span></div>
          <div className="gct-kv"><span>杠杆</span><span className="gct-num">{lev?.leverage != null ? `${lev.leverage}×` : '—'}</span></div>
          <div className="gct-kv">
            <span>持仓模式</span>
            <span>{mode?.collateralMode === 'single'
              ? <Tag color="green">单向 one-way</Tag>
              : <Tag color="red">{mode?.collateralMode ?? '—'}</Tag>}</span>
          </div>
        </Col>
        <Col span={12}>
          <div className="gct-kv"><span>当前仓位</span>
            <span className={`gct-num ${size != null && size !== 0 ? (size > 0 ? 'gct-up' : 'gct-down') : ''}`}>
              {size != null && size !== 0 ? `${fmtS(size)} ${market.base}` : '无持仓'}
            </span>
          </div>
          <div className="gct-kv"><span>开仓均价</span><span className="gct-num">{fmt(st?.averageOpenPrice)}</span></div>
          <div className="gct-kv"><span>标记价</span><span className="gct-num">{fmt(st?.markPrice)}</span></div>
          <div className="gct-kv"><span>盈亏平衡</span><span className="gct-num">{fmt(st?.breakEvenPrice)}</span></div>
          <div className="gct-kv"><span>预估强平</span><span className="gct-num">{fmt(st?.estimatedLiquidationPrice)}</span></div>
          <div className="gct-kv"><span>近期盈亏</span><span className={`gct-num ${pnlClass(n(st?.recentPnl))}`}>{fmtS(st?.recentPnl)}</span></div>
          <div className="gct-kv"><span>名义杠杆</span><span className="gct-num">{n(st?.notionalLeverage) != null ? `${fmt(st?.notionalLeverage)}×` : '—'}</span></div>
          <div className="gct-kv"><span>保证金率</span><span className="gct-num">{n(st?.marginFraction) != null ? `${(n(st?.marginFraction)! * 100).toFixed(2)}%` : '—'}</span></div>
        </Col>
      </Row>
      <div className="gct-dim" style={{ fontSize: 11, marginTop: 8 }}>
        合约类型 {st?.contractSettlementType ?? '—'} · 账户权益 {fmtS(st?.totalEquity ?? st?.equityUsd)} USDT
        {mode?.collateralMode === 'single'
          ? ' · 单向持仓已确认（币安 wrapper 不传 positionSide，只有单向模式能正常下单）'
          : ' · ⚠ 非单向持仓，下单会因缺 positionSide 被拒'}
      </div>
    </Card>
  )
}
