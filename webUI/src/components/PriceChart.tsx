import { Card, Empty, Space, Switch, Tag } from 'antd'
import { useEffect, useMemo, useRef } from 'react'
import { CandlestickSeries, HistogramSeries, LineSeries, createChart, type IChartApi } from 'lightweight-charts'
import { useAtom } from 'jotai'
import { barsAtom, indicatorsAtom, intervalAtom, selectedMarketAtom } from '@/state/atoms'
import { useGetKlinesQuery, useGetTechnicalAnalysisQuery, type Candle } from '@/app/api/gctApi'
import { TOKENS } from '@/theme'
import { QueryError } from './NotImplemented'

/** "2026-10-10 13:00:00 UTC" → UTCTimestamp（秒） */
const ts = (s: string): number => Math.floor(Date.parse(s.replace(' ', 'T').replace(' UTC', 'Z')) / 1000)

const CHART_OPTS = {
  layout: { background: { color: TOKENS.panel }, textColor: TOKENS.dim, fontSize: 10 },
  grid: { vertLines: { color: TOKENS.line }, horzLines: { color: TOKENS.line } },
  rightPriceScale: { borderColor: TOKENS.line },
  timeScale: { borderColor: TOKENS.line, timeVisible: true, secondsVisible: false },
  crosshair: { mode: 1 as const },
  autoSize: true,
}

/** 尾部对齐：K 线与指标分别请求，长度可能差几根，从末尾对齐 */
const tail = <T,>(a: T[], n: number): T[] => a.slice(Math.max(0, a.length - n))

type TaResp = { signals?: Record<string, { signals?: number[] }> } | undefined

/** 从 TA 响应按键名（大小写不敏感）取序列 */
const pickSeries = (r: TaResp, key: string): number[] => {
  const s = r?.signals ?? {}
  const k = Object.keys(s).find((x) => x.toUpperCase() === key)
  return k ? (s[k]?.signals ?? []) : []
}

/**
 * K 线图 + 指标（W3-02 / W3-03）。
 *
 * 实现要点（踩过的坑，改动前务必读）：
 *   1) 每个图表用**「单个 effect 完整重建」**：先 remove 旧图再 createChart。
 *      改成「创建一次 + 后续 addSeries」会在依赖变化时重复 addSeries 造成序列叠加泄漏；
 *      把 createChart 放进 [] 依赖的 effect 则首屏容器未挂载（ref=null）导致永不创建。
 *   2) 因此**图表 div 必须无条件渲染**，不要用 early return 把它们藏起来。
 *   3) 指标来自引擎 GetTechnicalAnalysis（与 gctcli technicalanalysis 同源）。
 *      默认参数 MACD(12,26,9) / EMA20 / SMA200 / RSI14 / BBANDS(20,2,2) —— Q16 定案。
 *   4) ⚠ BBANDS 必须同时传 standardDeviationUp/Down，否则端点 400（见 gctApi.ts 注释）。
 */
export function PriceChart() {
  const [market] = useAtom(selectedMarketAtom)
  const [interval] = useAtom(intervalAtom)
  const [bars] = useAtom(barsAtom)
  const [ind, setInd] = useAtom(indicatorsAtom)

  const mainRef = useRef<HTMLDivElement | null>(null)
  const macdRef = useRef<HTMLDivElement | null>(null)
  const rsiRef = useRef<HTMLDivElement | null>(null)

  const { data: kl, error: klErr } = useGetKlinesQuery(
    { ...market, intervalSeconds: interval, bars }, { pollingInterval: 120_000 },
  )
  const taArgs = { ...market, intervalSeconds: interval, bars }
  const { data: taEma } = useGetTechnicalAnalysisQuery({ ...taArgs, algorithm: 'EMA', period: 20 })
  const { data: taSma } = useGetTechnicalAnalysisQuery({ ...taArgs, algorithm: 'SMA', period: 200 })
  const { data: taMacd } = useGetTechnicalAnalysisQuery({ ...taArgs, algorithm: 'MACD', fast: 12, slow: 26, period: 9 })
  const { data: taRsi } = useGetTechnicalAnalysisQuery({ ...taArgs, algorithm: 'RSI', period: 14 })
  const { data: taBoll } = useGetTechnicalAnalysisQuery({ ...taArgs, algorithm: 'BBANDS', period: 20, stdUp: 2, stdDown: 2 })

  const candles = useMemo<Candle[]>(() => (kl?.candle ?? []).filter((c) => !c.isPartial), [kl])

  // ── 主图：蜡烛 + 成交量 + 均线/布林 ──────────────────────────────────────
  useEffect(() => {
    const el = mainRef.current
    if (!el || !candles.length) return
    const chart: IChartApi = createChart(el, CHART_OPTS)

    const cs = chart.addSeries(CandlestickSeries, {
      upColor: TOKENS.up, downColor: TOKENS.down, borderVisible: false,
      wickUpColor: TOKENS.up, wickDownColor: TOKENS.down,
    })
    cs.setData(candles.map((c) => ({ time: ts(c.time) as never, open: c.open, high: c.high, low: c.low, close: c.close })))

    const vol = chart.addSeries(HistogramSeries, {
      priceScaleId: 'vol', priceFormat: { type: 'volume' }, priceLineVisible: false, lastValueVisible: false,
    })
    chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
    vol.setData(candles.map((c) => ({
      time: ts(c.time) as never, value: c.volume,
      color: c.close >= c.open ? 'rgba(37,180,154,0.35)' : 'rgba(236,106,112,0.35)',
    })))

    const addLine = (vals: number[], color: string) => {
      const v = tail(vals, candles.length)
      const off = candles.length - v.length
      const pts = v
        .map((val, i) => ({ time: ts(candles[off + i]!.time) as never, value: val }))
        .filter((d) => d.value !== 0)
      if (pts.length < 2) return
      const ser = chart.addSeries(LineSeries, { color, lineWidth: 1, priceLineVisible: false, lastValueVisible: false })
      ser.setData(pts)
    }
    if (ind.ema20) addLine(pickSeries(taEma, 'EMA'), TOKENS.warn)
    if (ind.sma200) addLine(pickSeries(taSma, 'SMA'), TOKENS.focus)
    if (ind.boll) {
      addLine(pickSeries(taBoll, 'UPPER'), 'rgba(91,156,255,0.6)')
      addLine(pickSeries(taBoll, 'LOWER'), 'rgba(91,156,255,0.6)')
    }

    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [candles, ind.ema20, ind.sma200, ind.boll, taEma, taSma, taBoll])

  // ── MACD 副图 ───────────────────────────────────────────────────────────
  useEffect(() => {
    const el = macdRef.current
    if (!el || !candles.length || !ind.macd) return
    const chart = createChart(el, CHART_OPTS)
    const get = (k: string) => tail(pickSeries(taMacd, k), candles.length)
    const dif = get('MACD'), dea = get('SIGNAL'), hist = get('HISTOGRAM')
    const off = candles.length - hist.length

    if (hist.length) {
      const h = chart.addSeries(HistogramSeries, { priceLineVisible: false, lastValueVisible: false })
      h.setData(hist.map((v, i) => ({
        time: ts(candles[off + i]!.time) as never, value: v,
        color: v >= 0 ? 'rgba(37,180,154,0.6)' : 'rgba(236,106,112,0.6)',
      })))
    }
    const addLine = (v: number[], color: string) => {
      if (v.length < 2) return
      const o = candles.length - v.length
      const ser = chart.addSeries(LineSeries, { color, lineWidth: 1, priceLineVisible: false, lastValueVisible: false })
      ser.setData(v.map((val, i) => ({ time: ts(candles[o + i]!.time) as never, value: val })))
    }
    addLine(dif, TOKENS.warn)
    addLine(dea, TOKENS.focus)
    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [candles, taMacd, ind.macd])

  // ── RSI 副图 ────────────────────────────────────────────────────────────
  useEffect(() => {
    const el = rsiRef.current
    if (!el || !candles.length || !ind.rsi) return
    const chart = createChart(el, CHART_OPTS)
    const v = tail(pickSeries(taRsi, 'RSI'), candles.length)
    if (v.length >= 2) {
      const off = candles.length - v.length
      const ser = chart.addSeries(LineSeries, { color: '#b48cf2', lineWidth: 1, priceLineVisible: false })
      ser.setData(v.map((val, i) => ({ time: ts(candles[off + i]!.time) as never, value: val })))
    }
    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [candles, taRsi, ind.rsi])

  const toggle = (k: 'ema20' | 'sma200' | 'boll' | 'macd' | 'rsi', label: string, color?: string) => (
    <span className="gct-dim" key={k}>
      {color ? <span style={{ color }}>●</span> : null} {label}
      <Switch size="small" checked={ind[k]} onChange={(v) => setInd({ ...ind, [k]: v })} style={{ marginLeft: 4 }} />
    </span>
  )

  return (
    <Card
      size="small"
      title={
        <span className="gct-panel-title" style={{ marginBottom: 0 }}>
          K 线图 · {market.base}-{market.quote}
          <Tag style={{ marginLeft: 6 }}>{candles.length} 根已收盘</Tag>
        </span>
      }
      extra={
        <Space size={10} style={{ fontSize: 11 }}>
          {toggle('ema20', 'EMA20', TOKENS.warn)}
          {toggle('sma200', 'SMA200', TOKENS.focus)}
          {toggle('boll', '布林')}
          {toggle('macd', 'MACD')}
          {toggle('rsi', 'RSI')}
        </Space>
      }
    >
      {klErr ? <QueryError title="K 线" error={klErr} /> : null}

      {/* 主图容器无条件渲染（见组件注释的坑位 2） */}
      <div style={{ position: 'relative' }}>
        <div ref={mainRef} style={{ height: 300 }} />
        {!candles.length && !klErr ? (
          <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center' }}>
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span className="gct-dim">K 线加载中…</span>} />
          </div>
        ) : null}
      </div>

      {ind.macd ? (
        <div style={{ marginTop: 8 }}>
          <div className="gct-dim" style={{ fontSize: 11, marginBottom: 2 }}>MACD (12/26/9) —— DIF / DEA / 柱</div>
          <div ref={macdRef} style={{ height: 110 }} />
        </div>
      ) : null}

      {ind.rsi ? (
        <div style={{ marginTop: 8 }}>
          <div className="gct-dim" style={{ fontSize: 11, marginBottom: 2 }}>RSI (14)</div>
          <div ref={rsiRef} style={{ height: 90 }} />
        </div>
      ) : null}

      <div className="gct-dim" style={{ fontSize: 11, marginTop: 6 }}>
        指标由引擎 <code>GetTechnicalAnalysis</code> 计算，与 <code>gctcli technicalanalysis</code> 同源；
        未收盘的最后一根已排除（Q16）。BBANDS 需同时传 standardDeviationUp/Down（实测）。
      </div>
    </Card>
  )
}
