import { Space, Tag, Tooltip } from 'antd'
import { useGetEngineInfoQuery, useGetLlmStatusQuery } from '@/app/api/gctApi'
import { errorText } from '@/app/api/gctApi'
import { selectedMarketAtom } from '@/state/atoms'
import { useAtomValue } from 'jotai'

/**
 * 顶栏就绪状态条（W2-04）。
 * 显示：连接 / 子系统健康 / 选中合约 / 运行模式 / 配对 readiness / pair_epoch / halt / 对账
 * 依据：docs/llm-dev/08-对话与接口层.md §3.6、docs/llm-dev/10-安全与风控.md §6.1
 */
export function StatusBar() {
  const { data: info, error: infoErr } = useGetEngineInfoQuery(undefined, { pollingInterval: 60_000 })
  const { data: llm } = useGetLlmStatusQuery(undefined, { pollingInterval: 30_000 })
  const market = useAtomValue(selectedMarketAtom)

  const conn = info ? `已连接 · 运行 ${String(info.uptime ?? '').split('.')[0]}` : null
  const subsystems = info?.subsystemStatus ?? {}
  const downSubsystems = Object.entries(subsystems).filter(([, v]) => !v).map(([k]) => k)

  return (
    <Space size={8} wrap style={{ fontSize: 12 }}>
      {info ? (
        <Tooltip title={`启用交易所 ${info.enabledExchanges ?? '—'} · 已停子系统：${downSubsystems.join(', ') || '无'}`}>
          <Tag color="green">{conn}</Tag>
        </Tooltip>
      ) : (
        <Tooltip title={infoErr ? errorText(infoErr) : '正在连接本地代理'}>
          <Tag color={infoErr ? 'red' : 'default'}>{infoErr ? '连接失败' : '连接中…'}</Tag>
        </Tooltip>
      )}

      <Tag className="gct-mono">{market.base}-{market.quote}</Tag>

      {llm ? (
        <>
          <Tag color={llm.runMode === 'LIVE' ? 'red' : llm.runMode === 'TESTNET' ? 'orange' : 'blue'}>
            {llm.runMode}
          </Tag>
          <Tag color={llm.pairReadiness === 'READY' ? 'green' : llm.pairReadiness === 'DEGRADED' ? 'orange' : 'default'}>
            配对 {llm.pairReadiness} · epoch {llm.pairEpoch}
          </Tag>
          {llm.halted ? <Tag color="red">HALT {llm.haltLevel}</Tag> : null}
          <Tag color={llm.accountReconciled ? 'green' : 'orange'}>
            对账 {llm.accountReconciled ? '已对' : '未对账'}
          </Tag>
        </>
      ) : (
        <Tooltip title="LLM 子系统尚未在后端实现（TASKS.md M0/M1）">
          <Tag color="default">LLM 决策：待实现</Tag>
        </Tooltip>
      )}
    </Space>
  )
}
