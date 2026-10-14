import { Card, Empty, Tag } from 'antd'
import { useMemo } from 'react'
import { useAtomValue } from 'jotai'
import { barsAtom, indicatorsAtom, intervalAtom, selectedMarketAtom } from '@/state/atoms'
import { useGetKlinesQuery, useGetTechnicalAnalysisQuery } from '@/app/api/gctApi'

/**
 * 指标派生事实（W3-08）。
 *
 * 这些是**确定性代码算出的"读数"**，不是模型的"结论" —— 与后端 M2-04 将采用同一套规则，
 * 以便前端展示与 LLM 看到的证据一致。规则见 docs/llm-dev/04 §3.3：
 *   CrossState = 柱符号翻转（负→正 GOLDEN / 正→负 DEATH / 同号 NONE）
 *   HistogramTrend = |H[i]| vs |H[i-1]|（容差 1e-9 → FLAT）
 *   PriceVsMA = 收盘 vs 均线；Slope = MA[i] − MA[i-3]（<0.01% → FLAT）
 * ⚠ 严禁把它当成买卖指令（docs/llm-dev/04 §8：指标是证据，不是权威）。
 */
const TOL = 1e-9

const tail = (a: number[], n: number) => a.slice(Math.max(0, a.length - n))
const nz = (a: number[]) => a.filter((x) => x !== 0)

function crossState(hist: number[]): { state: string; barsSince: number } {
  const h = nz(hist)
  if (h.length < 2) return { state: 'NONE', barsSince: -1 }
  for (let i = h.length - 1; i >= 1; i--) {
    const a = h[i - 1]!, b = h[i]!
    if (a < 0 && b > 0) return { state: 'GOLDEN', barsSince: h.length - 1 - i }
    if (a > 0 && b < 0) return { state: 'DEATH', barsSince: h.length - 1 - i }
  }
  return { state: 'NONE', barsSince: -1 }
}

function histTrend(hist: number[]): string {
  const h = nz(hist)
  if (h.length < 2) return '—'
  const a = h[h.length - 2]!, b = h[h.length - 1]!
  if (Math.abs(Math.abs(b) - Math.abs(a)) <= TOL) return 'FLAT'
  return Math.abs(b) > Math.abs(a) ? 'EXPANDING' : 'CONTRACTING'
}

function slope(ma: number[]): string {
  const v = nz(ma)
  if (v.length < 4) return '—'
  const last = v[v.length - 1]!, prev = v[v.length - 4]!
  const d = last - prev
  if (Math.abs(d) < Math.abs(last) * 0.0001) return 'FLAT'
  return d > 0 ? 'UP' : 'DOWN'
}

const stateTag = (s: string) =>
  s === 'GOLDEN' ? <Tag color="green">金叉 GOLDEN</Tag>
    : s === 'DEATH' ? <Tag color="red">死叉 DEATH</Tag>
      : <Tag>无交叉 NONE</Tag>

const trendTag = (s: string) =>
  s === 'EXPANDING' ? <Tag color="green">柱放大</Tag>
    : s === 'CONTRACTING' ? <Tag color="orange">柱收缩</Tag>
      : <Tag>走平</Tag>

export function IndicatorFacts() {
  const market = useAtomValue(selectedMarketAtom)
  const interval = useAtomValue(intervalAtom)
  const bars = useAtomValue(barsAtom)
  const ind = useAtomValue(indicatorsAtom)

  const args = { ...market, intervalSeconds: interval, bars }
  const { data: kl } = useGetKlinesQuery(args, { pollingInterval: 120_000 })
  const { data: taMacd } = useGetTechnicalAnalysisQuery({ ...args, algorithm: 'MACD', fast: 12, slow: 26, period: 9 })
  const { data: taEma } = useGetTechnicalAnalysisQuery({ ...args, algorithm: 'EMA', period: 20 })
  const { data: taSma } = useGetTechnicalAnalysisQuery({ ...args, algorithm: 'SMA', period: 200 })
  const { data: taRsi } = useGetTechnicalAnalysisQuery({ ...args, algorithm: 'RSI', period: 14 })
  const { data: taAtr } = useGetTechnicalAnalysisQuery({ ...args, algorithm: 'ATR', period: 14 })

  const facts = useMemo(() => {
    const candles = (kl?.candle ?? []).filter((c) => !c.isPartial)
    const close = candles.length ? candles[candles.length - 1]!.close : null
    const get = (r: typeof taMacd, k: string) => tail(r?.signals?.[k]?.signals ?? [], candles.length)
    const hist = get(taMacd, 'HISTOGRAM')
    const dif = nz(get(taMacd, 'MACD'))
    const dea = nz(get(taMacd, 'SIGNAL'))
    const ema = nz(tail(taEma?.signals?.EMA?.signals ?? [], candles.length))
    const sma = nz(tail(taSma?.signals?.SMA?.signals ?? [], candles.length))
    const rsi = nz(tail(taRsi?.signals?.RSI?.signals ?? [], candles.length))
    const atr = nz(tail(taAtr?.signals?.ATR?.signals ?? [], candles.length))
    const cross = crossState(hist)
    const eLast = ema.length ? ema[ema.length - 1]! : null
    const sLast = sma.length ? sma[sma.length - 1]! : null
    return {
      count: candles.length, close, cross,
      trend: histTrend(hist),
      dif: dif.length ? dif[dif.length - 1]! : null,
      dea: dea.length ? dea[dea.length - 1]! : null,
      hist: nz(hist).length ? nz(hist)[nz(hist).length - 1]! : null,
      ema: eLast, sma: sLast, rsi: rsi.length ? rsi[rsi.length - 1]! : null,
      atr: atr.length ? atr[atr.length - 1]! : null,
      emaSlope: slope(ema), smaSlope: slope(sma),
      aboveEma: close != null && eLast != null ? close > eLast : null,
      aboveSma: close != null && sLast != null ? close > sLast : null,
    }
  }, [kl, taMacd, taEma, taSma, taRsi, taAtr])

  if (!facts.count) {
    return (
      <Card size="small" title={<span className="gct-panel-title">指标派生事实</span>}>
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">加载中</span>} />
      </Card>
    )
  }

  const kv = (k: string, v: React.ReactNode) => (
    <div className="gct-kv" key={k}><span>{k}</span><span className="gct-num">{v}</span></div>
  )

  return (
    <Card size="small" title={<span className="gct-panel-title">指标派生事实（确定性读数）</span>}>
      {kv('MACD 交叉', <>{stateTag(facts.cross.state)}{facts.cross.barsSince >= 0 ? <span className="gct-dim"> {facts.cross.barsSince} 根前</span> : null}</>)}
      {kv('柱趋势', trendTag(facts.trend))}
      {kv('DIF / DEA', `${facts.dif?.toFixed(2) ?? '—'} / ${facts.dea?.toFixed(2) ?? '—'}`)}
      {kv('柱值', facts.hist?.toFixed(2) ?? '—')}
      {ind.ema20 ? kv('收盘 vs EMA20', <>{facts.ema != null ? facts.ema.toFixed(2) : '—'}
        {facts.aboveEma != null ? <Tag color={facts.aboveEma ? 'green' : 'red'} style={{ marginLeft: 6 }}>{facts.aboveEma ? '上方' : '下方'}</Tag> : null}
        <Tag style={{ marginLeft: 4 }}>斜率 {facts.emaSlope}</Tag></>) : null}
      {ind.sma200 ? kv('收盘 vs SMA200', facts.sma != null
        ? <>{facts.sma.toFixed(2)}
          {facts.aboveSma != null ? <Tag color={facts.aboveSma ? 'green' : 'red'} style={{ marginLeft: 6 }}>{facts.aboveSma ? '上方' : '下方'}</Tag> : null}
          <Tag style={{ marginLeft: 4 }}>斜率 {facts.smaSlope}</Tag></>
        : <span className="gct-dim">数据不足 200 根</span>) : null}
      {ind.rsi ? kv('RSI(14)', facts.rsi?.toFixed(2) ?? '—') : null}
      {kv('ATR(14)', facts.atr?.toFixed(2) ?? '—')}
      <div className="gct-dim" style={{ fontSize: 11, marginTop: 8 }}>
        以上为<b>确定性代码算出的读数</b>，不是买类建议。本系统不写死"金叉就买"这类规则 ——
        指标是证据之一，买卖假设由你的策略与当时数据驱动（docs/llm-dev/04 §8）。
      </div>
    </Card>
  )
}
