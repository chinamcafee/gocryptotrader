import type { ThemeConfig } from 'antd'
import { theme } from 'antd'

/**
 * 设计 token，取自 docs/llm-dev/08-对话与接口层.md §8（沿用原 hyperliquid-auto-trader 的视觉规范）。
 * 自定义 canvas 图表与工具组件直接引用本对象，保证与 antd 主题一致。
 */
export const TOKENS = {
  bg: '#0B111B',
  panel: '#111B28',
  panel2: '#0E1723',
  line: '#1E2C3D',
  up: '#25B49A',
  down: '#EC6A70',
  focus: '#5B9CFF',
  text: '#D7E0EA',
  dim: '#7C8B9D',
  warn: '#E8B44C',
} as const

export const antdTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: TOKENS.focus,
    colorBgBase: TOKENS.bg,
    colorBgContainer: TOKENS.panel,
    colorBgElevated: TOKENS.panel2,
    colorBorder: TOKENS.line,
    colorBorderSecondary: TOKENS.line,
    colorText: TOKENS.text,
    colorTextSecondary: TOKENS.dim,
    colorSuccess: TOKENS.up,
    colorError: TOKENS.down,
    colorWarning: TOKENS.warn,
    borderRadius: 6,
    fontSize: 13,
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", Menlo, monospace',
    wireframe: false,
  },
  components: {
    Layout: { bodyBg: TOKENS.bg, headerBg: TOKENS.panel, siderBg: TOKENS.panel },
    Menu: { darkItemBg: TOKENS.panel, darkItemSelectedBg: 'rgba(91,156,255,0.16)' },
    Table: { headerBg: TOKENS.panel2, rowHoverBg: 'rgba(91,156,255,0.08)' },
    Card: { colorBgContainer: TOKENS.panel },
  },
}

/** 涨跌配色（涨=绿，跌=红，与原项目一致） */
export const pnlClass = (v: number | null | undefined): string =>
  v == null || Number.isNaN(v) ? 'gct-dim' : v > 0 ? 'gct-up' : v < 0 ? 'gct-down' : 'gct-dim'
