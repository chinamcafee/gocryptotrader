import { atom } from 'jotai'

/** 当前选中的合约（终端页与所有 LLM 页面共享） */
export const selectedMarketAtom = atom<{ base: string; quote: string }>({ base: 'BTC', quote: 'USDT' })

/** 图表周期（秒）。默认 1h —— Q5b 定案：1h 扫描 + 4h 确认 */
export const intervalAtom = atom<number>(3600)

/** 图表保留根数 */
export const barsAtom = atom<number>(200)

/** 指标叠加/副图开关（Q16 定案：EMA20 + SMA200 + MACD + RSI + 布林 默认可用） */
export interface IndicatorToggles {
  ema20: boolean
  sma200: boolean
  macd: boolean
  rsi: boolean
  boll: boolean
}
export const indicatorsAtom = atom<IndicatorToggles>({
  ema20: true, sma200: true, macd: true, rsi: false, boll: false,
})

/** 右侧面板布局（终端页） */
export const panelLayoutAtom = atom<'chart' | 'orderbook' | 'account'>('chart')

/** 对话草稿（策略工作区，仅前端内存，显式保存才落库 —— docs/llm-dev/05 §8.3） */
export const chatDraftAtom = atom<string>('')
