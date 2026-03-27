import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Sidebar } from '@/components/Sidebar'
import { ChatPage } from '@/pages/ChatPage'
import { SkillsPage } from '@/pages/SkillsPage'
import { PluginsPage } from '@/pages/PluginsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { AutomationsPage } from '@/pages/AutomationsPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { PeoplePage } from '@/pages/PeoplePage'
import { CostsPage } from '@/pages/CostsPage'
import { MemoryPage } from '@/pages/MemoryPage'
import { ChannelsPage } from '@/pages/ChannelsPage'

export default function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen bg-white">
        <Sidebar />
        <main className="pl-[78px]">
          <Routes>
            <Route path="/" element={<ChatPage />} />
            <Route path="/chat" element={<ChatPage />} />
            <Route path="/skills" element={<SkillsPage />} />
            <Route path="/plugins" element={<PluginsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/automations" element={<AutomationsPage />} />
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/people" element={<PeoplePage />} />
            <Route path="/costs" element={<CostsPage />} />
            <Route path="/memory" element={<MemoryPage />} />
            <Route path="/channels" element={<ChannelsPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
