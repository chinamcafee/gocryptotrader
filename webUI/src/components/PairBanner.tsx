import { Tag, Typography } from 'antd'
import { Link } from 'react-router-dom'
import { useGetActiveModelPairQuery, useGetLlmProvidersQuery } from '@/app/api/gctApi'
import type { LlmProviderProfile } from '@/app/api/types'
import { TOKENS } from '@/theme'

const { Text } = Typography

/** 从档案表解析活化修订版的显示名；找不到（已归档/已清理）回退到短引用。 */
function revisionLabel(profiles: LlmProviderProfile[] | undefined, revisionId?: string): string {
  if (!revisionId) return '未挂'
  for (const p of profiles ?? []) {
    if (p.revision?.id === revisionId) return `${p.displayName}（${p.revision.modelId}）`
  }
  return revisionId.slice(0, 8)
}

function toneOf(pair: { found: boolean; readiness?: string }): 'ok' | 'warn' | 'bad' {
  if (!pair.found) return 'warn'
  if (pair.readiness === 'READY') return 'ok'
  if (pair.readiness === 'INCOMPLETE') return 'warn'
  // DEGRADED 与未知 readiness 一视同仁：不认识的 readiness 不是就绪的证据。
  return 'bad'
}

const TONE_STYLE = {
  ok: { background: 'rgba(37,180,154,0.10)', borderColor: 'rgba(37,180,154,0.35)' },
  warn: { background: 'rgba(216,150,20,0.12)', borderColor: 'rgba(216,150,20,0.4)' },
  bad: { background: 'rgba(236,106,112,0.12)', borderColor: 'rgba(236,106,112,0.4)' },
} as const

/**
 * 「当前运行配对」横幅（W4-04，08 §8 必留 UI 规则）：
 * `LLM × Jev | READY/INCOMPLETE/DEGRADED + pair_epoch`。
 * 未就绪时写明后果（自动决策在硬门禁处被阻断）与去向——fail-closed 的可见面。
 * 后端未装配（501/不可达）时整条隐藏：没有数据就不画。
 */
export function PairBanner() {
  const { data: pair, error: pairErr } = useGetActiveModelPairQuery(undefined, { pollingInterval: 30_000 })
  const { data: providers } = useGetLlmProvidersQuery({ includeArchived: true }, { pollingInterval: 60_000 })
  if (pairErr || !pair) return null

  const tone = toneOf(pair)
  const readiness = pair.readiness ?? 'INCOMPLETE'
  const llmLabel = revisionLabel(providers?.profiles, pair.llmProfileRevisionId)
  const jevLabel = revisionLabel(providers?.profiles, pair.jevProfileRevisionId)

  return (
    <div
      style={{
        display: 'flex', alignItems: 'center', gap: 10, paddingInline: 16, height: 30,
        fontSize: 12, whiteSpace: 'nowrap', overflow: 'hidden',
        borderBottom: `1px solid ${TOKENS.line}`,
        ...TONE_STYLE[tone],
      }}
    >
      <Text className="gct-dim" style={{ fontSize: 12 }}>当前运行配对</Text>
      {pair.found ? (
        <>
          <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>
            LLM <Text strong style={{ fontSize: 12 }}>{llmLabel}</Text>
            <Text className="gct-dim" style={{ marginInline: 6 }}>×</Text>
            Jev <Text strong style={{ fontSize: 12 }}>{jevLabel}</Text>
          </span>
          <Tag
            color={tone === 'ok' ? 'green' : tone === 'warn' ? 'orange' : 'red'}
            style={{ marginInline: 0 }}
          >
            {readiness}
          </Tag>
          <Text className="gct-mono" style={{ fontSize: 11 }}>epoch {pair.pairEpoch}</Text>
          {tone !== 'ok' ? (
            <Text className="gct-dim" style={{ fontSize: 12, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}>
              —— 自动决策在硬门禁处被阻断（
              {readiness === 'INCOMPLETE' ? '有一边未挂上配对槽位' : '至少一边最新探针未通过或已过期'}
              ），
            </Text>
          ) : null}
          {tone !== 'ok' ? <Link to="/providers">前往「模型与配对」处理</Link> : null}
        </>
      ) : (
        <>
          <Tag color="orange" style={{ marginInline: 0 }}>未激活</Tag>
          <Text className="gct-dim" style={{ fontSize: 12 }}>尚无 LLM × Jev 配对——自动决策不可用，</Text>
          <Link to="/providers">前往「模型与配对」激活</Link>
        </>
      )}
    </div>
  )
}
