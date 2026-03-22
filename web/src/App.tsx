import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Sidebar } from '@/components/Sidebar'
import { DashboardPage } from '@/pages/DashboardPage'
import { ChatPage } from '@/pages/ChatPage'
import { ConversationsPage } from '@/pages/ConversationsPage'
import { ConversationDetailPage } from '@/pages/ConversationDetailPage'
import { SkillsPage } from '@/pages/SkillsPage'
import { AgentsPage } from '@/pages/AgentsPage'
import { ProvidersPage } from '@/pages/ProvidersPage'
import { LogsPage } from '@/pages/LogsPage'
import { SettingsPage } from '@/pages/SettingsPage'

export default function App() {
  return (
    <BrowserRouter>
      <div className="flex h-screen overflow-hidden" style={{ background: 'var(--color-white)' }}>
        <Sidebar />
        <main className="relative flex-1 h-full overflow-hidden">
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/chat" element={<ChatPage />} />
            <Route path="/conversations" element={<ConversationsPage />} />
            <Route path="/conversations/:id" element={<ConversationDetailPage />} />
            <Route path="/skills" element={<SkillsPage />} />
            <Route path="/agents" element={<AgentsPage />} />
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
