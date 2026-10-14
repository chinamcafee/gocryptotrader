import { Card, Input, Segmented, Table, Tag } from 'antd'
import { useMemo, useState } from 'react'
import { useGetExchangePairsQuery, ASSET } from '@/app/api/gctApi'
import { useAtom } from 'jotai'
import { selectedMarketAtom } from '@/state/atoms'
import { QueryError } from './NotImplemented'

const num = (v: number) => v.toLocaleString('en-US', { maximumFractionDigits: 2 })

/**
 * 合约列表（W3-01）。数据源：getexchangepairs（POST，返回 availablePairs / enabledPairs 两个逗号串）。
 * 券安 USDT-M 实测 available ≈ 789、其中 PERPETUAL ≈ 525（见 docs/llm-dev/04 §7.2）。
 */
interface MarketRow { key: string; base: string; quote: string; enabled: boolean }

export function MarketList() {
  const { data, error, isLoading } = useGetExchangePairsQuery(undefined, { pollingInterval: 300_000 })
  const [market, setMarket] = useAtom(selectedMarketAtom)
  const [kw, setKw] = useState('')
  const [scope, setScope] = useState<'enabled' | 'available' | 'top'>('enabled')

  const node = data?.supportedAssets?.[ASSET]
  const enabled = useMemo(() => (node?.enabledPairs ?? '').split(',').filter(Boolean), [node])
  const available = useMemo(() => (node?.availablePairs ?? '').split(',').filter(Boolean), [node])

  /** 列表只显示 USDT 计价的 PERPETUAL（过滤美元币本位与交割合约日期后缀） */
  const rows = useMemo(() => {
    const src = scope === 'enabled' ? enabled : available
    const list = (src.length ? src : enabled)
      .filter((s) => s.endsWith('_USDT'))
      .map((s) => {
        const [base] = s.split('_')
        return { key: s, base: base ?? s, quote: 'USDT', enabled: enabled.includes(s) }
      })
    const filtered = kw
      ? list.filter((r) => r.base.toLowerCase().includes(kw.toLowerCase()))
      : list
    // 「流动性前 N」在此仅作前缀优先展示；真实流动性排序需 quoteVolume（见 W7-04 / M2）
    return filtered.slice(0, scope === 'top' ? 50 : 600)
  }, [scope, enabled, available, kw])

  if (error) return <QueryError title="合约列表" error={error} />

  return (
    <Card
      size="small"
      title={<span className="gct-panel-title">合约列表 · {rows.length} 个</span>}
      styles={{ body: { paddingTop: 8 } }}
    >
      <Input
        size="small" allowClear placeholder="搜索，如 BTC / SOL"
        value={kw} onChange={(e) => setKw(e.target.value)} style={{ marginBottom: 8 }}
      />
      <Segmented
        size="small" block value={scope}
        onChange={(v) => setScope(v as typeof scope)}
        options={[
          { label: `已启用 ${enabled.length}`, value: 'enabled' },
          { label: `全部 ${available.length}`, value: 'available' },
          { label: '前 50', value: 'top' },
        ]}
        style={{ marginBottom: 8 }}
      />
      <Table<MarketRow>
        size="small" loading={isLoading} pagination={false}
        scroll={{ y: 420 }}
        dataSource={rows}
        rowClassName={(r) => (r.base === market.base ? 'gct-row-active' : '')}
        onRow={(r) => ({ onClick: () => setMarket({ base: r.base, quote: r.quote }), style: { cursor: 'pointer' } })}
        columns={[
          {
            title: '合约', dataIndex: 'base',
            render: (v: string, r: { quote: string; enabled: boolean }) => (
              <span>
                <span className="gct-mono">{v}</span>
                <span className="gct-dim">/{r.quote}</span>
                {r.enabled ? <Tag color="green" style={{ marginLeft: 6, transform: 'scale(0.85)' }}>启用</Tag> : null}
              </span>
            ),
          },
          {
            title: '状态', dataIndex: 'enabled', width: 70, align: 'right',
            render: (v: boolean) => (v ? <span className="gct-up">可交易</span> : <span className="gct-dim">仅追踪</span>),
          },
        ]}
      />
      <div className="gct-dim" style={{ fontSize: 11, marginTop: 8 }}>
        点击行切换当前合约（全局共享）。共 {rows.length} 行 · 数字格式示例 {num(525)}
      </div>
    </Card>
  )
}
