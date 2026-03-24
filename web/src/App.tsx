import { BrowserRouter, Routes, Route } from 'react-router-dom'
import { Sidebar } from '@/components/Sidebar'
import { ChatPage } from '@/pages/ChatPage'
import { SkillsPage } from '@/pages/SkillsPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { AutomationsPage } from '@/pages/AutomationsPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { PersonasPage } from '@/pages/PersonasPage'

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
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/automations" element={<AutomationsPage />} />
            <Route path="/dashboard" element={<DashboardPage />} />
            <Route path="/personas" element={<PersonasPage />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
