import { Layout, Menu, Typography } from 'antd'
import {
  AreaChartOutlined, RobotOutlined, BranchesOutlined, AuditOutlined,
  HeartOutlined, SettingOutlined, AppstoreOutlined, CommentOutlined, FundProjectionScreenOutlined, HistoryOutlined,
} from '@ant-design/icons'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { StatusBar } from './StatusBar'
import { PairBanner } from './PairBanner'
import { TOKENS } from '@/theme'

const { Header, Sider, Content } = Layout
const { Text } = Typography

const NAV = [
  { key: '/terminal', icon: <AreaChartOutlined />, label: '交易台' },
  { key: '/markets', icon: <AppstoreOutlined />, label: '合约覆盖' },
  { key: '/chat', icon: <CommentOutlined />, label: '对话' },
  { key: '/screener', icon: <FundProjectionScreenOutlined />, label: '筛选与开仓决策' },
  { key: '/positions-review', icon: <HistoryOutlined />, label: '持仓决策' },
  { key: '/strategies', icon: <BranchesOutlined />, label: '策略工作区' },
  { key: '/providers', icon: <RobotOutlined />, label: '模型与配对' },
  { key: '/decisions', icon: <AuditOutlined />, label: '决策与审计' },
  { key: '/health', icon: <HeartOutlined />, label: '健康与治理' },
  { key: '/settings', icon: <SettingOutlined />, label: '设置' },
]

/** 应用外壳（W2-01/02）：顶栏环境状态 + 侧边导航 + 主内容区 */
export function AppShell() {
  const nav = useNavigate()
  const loc = useLocation()
  const selected = NAV.find((n) => loc.pathname.startsWith(n.key))?.key ?? '/terminal'

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Header
        style={{
          display: 'flex', alignItems: 'center', gap: 16, paddingInline: 16,
          borderBottom: `1px solid ${TOKENS.line}`, height: 48, lineHeight: '48px',
        }}
      >
        <Text strong style={{ fontSize: 14, letterSpacing: 0.3, whiteSpace: 'nowrap' }}>
          GCT<span className="gct-brand-long"> · LLM/Jev 决策平台</span>
        </Text>
        <div style={{ flex: 1, minWidth: 0, overflow: 'hidden' }}>
          <StatusBar />
        </div>
        <Text className="gct-dim gct-mono gct-version" style={{ fontSize: 11 }}>v{__APP_VERSION__}</Text>
      </Header>
      <PairBanner />
      <Layout>
        {/*
          窄屏必须收起侧栏。固定 176px 的 Sider 在 390px 视口下只剩 ~210px 给内容，
          加上表格的最小宽度就会把整页撑出横向滚动条（实测溢出 191px）。
          breakpoint="lg" + collapsedWidth={0} 让 antd 在 <992px 时把侧栏收成 0 宽，
          只留一个悬浮触发器；收起后内容拿到全部宽度。
        */}
        <Sider
          width={176} theme="dark" breakpoint="lg" collapsedWidth={0}
          style={{ borderRight: `1px solid ${TOKENS.line}` }}
        >
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[selected]}
            items={NAV}
            onClick={(e) => nav(e.key)}
            style={{ borderInlineEnd: 0, paddingTop: 8 }}
          />
        </Sider>
        <Content style={{ padding: 14, minWidth: 0, overflowX: 'hidden' }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}
