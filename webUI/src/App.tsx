import { ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import { Provider as ReduxProvider } from 'react-redux'
import { Navigate, Route, Routes } from 'react-router-dom'
import { store } from '@/app/store'
import { AppShell } from '@/components/AppShell'
import { antdTheme } from '@/theme'
import TerminalPage from '@/pages/TerminalPage'
import MarketsPage from '@/pages/MarketsPage'
import ChatPage from '@/pages/ChatPage'
import StrategiesPage from '@/pages/StrategiesPage'
import ProvidersPage from '@/pages/ProvidersPage'
import DecisionsPage from '@/pages/DecisionsPage'
import HealthPage from '@/pages/HealthPage'
import SettingsPage from '@/pages/SettingsPage'
import ScreenerPage from '@/pages/ScreenerPage'
import PositionsReviewPage from '@/pages/PositionsReviewPage'

export default function App() {
  return (
    <ReduxProvider store={store}>
      <ConfigProvider theme={antdTheme} locale={zhCN}>
        <Routes>
          <Route element={<AppShell />}>
            <Route index element={<Navigate to="/terminal" replace />} />
            <Route path="/terminal" element={<TerminalPage />} />
            <Route path="/markets" element={<MarketsPage />} />
            <Route path="/chat" element={<ChatPage />} />
            <Route path="/screener" element={<ScreenerPage />} />
            <Route path="/positions-review" element={<PositionsReviewPage />} />
            <Route path="/strategies" element={<StrategiesPage />} />
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/decisions" element={<DecisionsPage />} />
            <Route path="/health" element={<HealthPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="*" element={<Navigate to="/terminal" replace />} />
          </Route>
        </Routes>
      </ConfigProvider>
    </ReduxProvider>
  )
}
