/**
 * SSE 分帧解析器的机器化自查（M3-09）。
 *
 * 这个解析器面对的现实是：网络分块与消息边界无关。脚本把同一串事件按
 * 一个字节都不许丢的方式切碎喂进去，验证输出与整段喂入完全一致——
 * 与 Go 侧流式客户端"整帧才解析"是同一条纪律的两个实现。
 *
 * 运行：node scripts/check-sse-parser.mjs
 */

import assert from 'node:assert/strict'
import { createGatewayStreamParser } from '../src/app/api/sse.ts'

const stream = [
  '{"delta":"看"}',
  '{"delta":"在"}',
  '{"delta":"区间。"}',
  JSON.stringify({ final: { assistantMessage: '看在区间。', messageId: 'm-1' } }),
].join('\n') + '\n'

// 1) 整段喂入
{
  const parser = createGatewayStreamParser()
  const events = [...parser.push(stream), ...parser.flush()]
  assert.equal(events.filter((e) => e.delta).map((e) => e.delta).join(''), '看在区间。')
  assert.equal(events.at(-1).final.assistantMessage, '看在区间。')
}

// 2) 逐字节切碎喂入：每个 chunk 都从消息中间断开
{
  const parser = createGatewayStreamParser()
  const deltas = []
  let final = null
  for (const chr of stream) {
    for (const event of parser.push(chr)) {
      if (event.delta) deltas.push(event.delta)
      if (event.final) final = event.final
    }
  }
  for (const event of parser.flush()) {
    if (event.delta) deltas.push(event.delta)
    if (event.final) final = event.final
  }
  assert.equal(deltas.join(''), '看在区间。', '逐字节切碎后增量必须完整且有序')
  assert.equal(final.assistantMessage, '看在区间。')
}

// 3) 网关 result 包装形态
{
  const parser = createGatewayStreamParser()
  const events = parser.push('{"result":{"delta":"x"}}\n')
  assert.equal(events[0].delta, 'x')
}

// 4) error 信封透传而不是当作回复
{
  const parser = createGatewayStreamParser()
  const events = parser.push('{"error":{"code":13,"message":"cut"}}\n')
  assert.ok(events[0].error, 'error 信封必须原样透传给调用方')
}

// 5) 无法解析的残留分片必须报错，不许静默丢弃
{
  const parser = createGatewayStreamParser()
  assert.throws(() => parser.push('{half json\n'), /无法解析/)
}

// 6) 末尾无换行的残余在 flush 时仍按一行处理
{
  const parser = createGatewayStreamParser()
  assert.equal(parser.push('{"delta":"tail"}').length, 0)
  const tail = parser.flush()
  assert.equal(tail[0].delta, 'tail')
}

console.log('sse parser checks: 6/6 ok')
