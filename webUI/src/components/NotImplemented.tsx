import { Alert, Typography } from 'antd'
import type { ReactNode } from 'react'
import { errorText, isNotImplemented } from '@/app/api/gctApi'

const { Paragraph } = Typography

/**
 * 统一的「后端未实现」降级组件。
 *
 * 为什么需要它：`docs/llm-dev` 的 LLM 子系统（M0/M1/M4）尚未落地，前端先按契约实现 UI。
 * 代理对未注册的 `/v1/llm/*` 返回 501 NOT_IMPLEMENTED，前端据此展示"待实现"而非崩溃。
 * 依据：docs/llm-dev/08-对话与接口层.md §1、taskProgress/TASKS.md W1-08
 */
export function NotImplemented({
  title,
  error,
  hint,
}: {
  title: string
  error?: unknown
  hint?: ReactNode
}) {
  const notImpl = error == null || isNotImplemented(error)
  return (
    <Alert
      type={notImpl ? 'warning' : 'error'}
      showIcon
      message={notImpl ? `${title}：后端尚未实现` : `${title}：请求失败`}
      description={
        <div>
          {error != null && !notImpl ? (
            <Paragraph className="gct-mono" style={{ marginBottom: 8, fontSize: 12 }}>
              {errorText(error)}
            </Paragraph>
          ) : null}
          {notImpl ? (
            <Paragraph style={{ marginBottom: 0, fontSize: 12 }}>
              前端已按接口契约完成，等待 Go 侧落地。对应任务见
              <code> docs/llm-dev/taskProgress/TASKS.md</code> 的
              <code> M0</code> / <code> M1</code> / <code> M4</code>。
            </Paragraph>
          ) : null}
          {hint}
        </div>
      }
    />
  )
}

/** 把任意查询错误渲染成一致的提示（有 501 就走未实现语义） */
export function QueryError({ title, error }: { title: string; error: unknown }) {
  return <NotImplemented title={title} error={error} />
}
