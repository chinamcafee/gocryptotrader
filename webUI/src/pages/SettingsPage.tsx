import { Alert, Card, Col, Descriptions, Row, Typography } from 'antd'
import { EXCHANGE, ASSET } from '@/app/api/gctApi'
import { useGetExchangeAssetsQuery, useGetExchangePairsQuery } from '@/app/api/gctApi'

const { Paragraph } = Typography

/** 设置（W2-02 附带页）。展示当前运行参数与前端安全边界说明。 */
export default function SettingsPage() {
  const { data: assets } = useGetExchangeAssetsQuery()
  const { data: pairs } = useGetExchangePairsQuery()
  const enabled = (pairs?.supportedAssets?.[ASSET]?.enabledPairs ?? '').split(',').filter(Boolean)
  const available = (pairs?.supportedAssets?.[ASSET]?.availablePairs ?? '').split(',').filter(Boolean)

  return (
    <Row gutter={[12, 12]}>
      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">运行参数（已定案）</span>}>
          <Descriptions size="small" column={1}>
            <Descriptions.Item label="交易所 / 资产">{EXCHANGE} / {ASSET}</Descriptions.Item>
            <Descriptions.Item label="可用资产类型">{assets?.assets ?? '—'}</Descriptions.Item>
            <Descriptions.Item label="enabled 合约数">{enabled.length}</Descriptions.Item>
            <Descriptions.Item label="available 合约数">{available.length}</Descriptions.Item>
            <Descriptions.Item label="决策节奏">1h 全量扫描 + 4h 决策确认（Q5b）</Descriptions.Item>
            <Descriptions.Item label="新鲜度窗口">60s（Q9）</Descriptions.Item>
            <Descriptions.Item label="指标默认">1h / MACD(12,26,9) / EMA20 + SMA200 / RSI14 / 布林(20,2,2) / ATR14（Q16）</Descriptions.Item>
            <Descriptions.Item label="风控信封档位">小额验证档（Q8）：单笔 20 / 总敞口 100 / 日损 20 / 杠杆 1 / 滑点 20bps</Descriptions.Item>
            <Descriptions.Item label="数据库">PostgreSQL（Q20）</Descriptions.Item>
            <Descriptions.Item label="密钥存放">Provider API Key 明文存于本地数据库修订版行（2026-10-15 简化，用户拍板）</Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col xs={24} lg={12}>
        <Card size="small" title={<span className="gct-panel-title">前端安全边界</span>}>
          <Alert
            type="success" showIcon
            message="前端不持有任何密钥"
            description={
              <ul style={{ margin: 0, paddingLeft: 18, fontSize: 12 }}>
                <li>币安 API Key 存在 <code>~/.gocryptotrader/config.json</code>，只有 Go 侧使用</li>
                <li>模型 API Key 明文存于 provider 修订版行（<code>api_key_ref</code> 列承载；指纹服务端派生），不经过浏览器之外的任何第三方</li>
                <li>REST 网关的 Basic Auth 由<b>本地代理进程</b>注入，不进浏览器</li>
                <li>写操作默认关闭（<code>GCT_ALLOW_WRITES=false</code>），代理返回 501</li>
                <li>批准/撤销/清 halt 需重新认证，不在聊天软件里一键完成</li>
              </ul>
            }
          />
          <Paragraph className="gct-dim" style={{ fontSize: 11, marginTop: 12, marginBottom: 0 }}>
            依据：<code>docs/llm-dev/10-安全与风控.md</code> §3，以及 <code>docs/llm-dev/01-需求与产品目标.md</code> §3 的设计底线。
          </Paragraph>
        </Card>
      </Col>
    </Row>
  )
}
