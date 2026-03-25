import { useEffect, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { Blocks, Settings, CalendarSync, LayoutDashboard, Plus, Users, DollarSign, Brain, type LucideIcon } from 'lucide-react'
import { api, type Conversation } from '@/lib/api'

type NavItem = { Icon: LucideIcon; label: string; route: string }

const TOP_NAV: NavItem[] = [
  { Icon: LayoutDashboard, label: 'Dashboard', route: '/dashboard' },
  { Icon: Blocks, label: 'Skills', route: '/skills' },
  { Icon: CalendarSync, label: 'Automations', route: '/automations' },
  { Icon: Users, label: 'Personas', route: '/personas' },
  { Icon: DollarSign, label: 'Costs', route: '/costs' },
  { Icon: Brain, label: 'Memory', route: '/memory' },
]

function NavButton({ Icon, label, active, expanded, onClick }: {
  Icon: LucideIcon; label: string; active: boolean; expanded: boolean; onClick: () => void
}) {
  const [hovered, setHovered] = useState(false)
  const cls = [
    'flex items-center cursor-pointer h-[30px] transition-colors border-none outline-none bg-transparent',
    expanded ? 'w-full px-4 rounded-xl' : 'w-[30px] justify-center rounded-full',
    active || hovered ? 'bg-sidebar-hover-white' : '',
  ].join(' ')

  return (
    <button
      type="button"
      aria-label={label}
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      className={cls}
    >
      <Icon className={`w-[15px] h-[15px] flex-shrink-0 ${active ? 'text-brand-primary' : 'text-normal-black'}`} />
      {expanded && (
        <span className={`text-14 ml-3 whitespace-nowrap ${active ? 'text-hover-black' : 'text-normal-black'}`}>
          {label}
        </span>
      )}
    </button>
  )
}


export function Sidebar() {
  const location = useLocation()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [expanded, setExpanded] = useState(false)
  const [conversations, setConversations] = useState<Conversation[]>([])

  const isOnChat = location.pathname === '/' || location.pathname === '/chat'
  const activeSession = searchParams.get('session')

  useEffect(() => {
    if (!expanded) return
    api.conversations().then(setConversations).catch(() => {})
  }, [expanded])

  const isActive = (item: NavItem) => location.pathname.startsWith(item.route)

  return (
    <aside
      onMouseEnter={() => setExpanded(true)}
      onMouseLeave={() => setExpanded(false)}
      className={`flex flex-col gap-[12px] p-[12px] bg-sidebar-white rounded-2xl m-4 fixed top-0 left-0 h-[calc(100vh-32px)] z-50 transition-all duration-200 ${
        expanded ? 'w-[200px]' : 'w-[54px]'
      }`}
    >
      {/* Top nav items */}
      <nav className="flex flex-col gap-1">
        {TOP_NAV.map(item => (
          <NavButton
            key={item.route}
            Icon={item.Icon}
            label={item.label}
            active={isActive(item)}
            expanded={expanded}
            onClick={() => navigate(item.route)}
          />
        ))}
      </nav>

      {/* Chats section */}
      <div className="flex flex-col gap-1 flex-1 min-h-0">
        {/* New Chat button */}
        <button
          type="button"
          onClick={() => navigate('/')}
          onMouseEnter={e => (e.currentTarget.style.background = '')}
          className={[
            'flex items-center h-[30px] cursor-pointer transition-colors border-none outline-none bg-transparent',
            expanded ? 'w-full px-4 rounded-xl' : 'w-[30px] justify-center rounded-full',
            isOnChat && !activeSession ? 'bg-sidebar-hover-white' : 'hover:bg-sidebar-hover-white',
          ].join(' ')}
        >
          <Plus className="w-[15px] h-[15px] flex-shrink-0 text-normal-black" />
          {expanded && (
            <span className="text-14 ml-3 whitespace-nowrap text-normal-black">New chat</span>
          )}
        </button>

        {/* Conversation history */}
        {expanded && (
          <div className="flex-1 min-h-0 overflow-y-auto space-y-0.5 scrollbar-hide">
            {conversations.map(c => (
              <button
                key={c.id}
                type="button"
                onClick={() => navigate(`/?session=${c.id}`)}
                className={[
                  'w-full text-left px-4 py-1.5 rounded-xl transition-colors',
                  activeSession === c.id ? 'bg-sidebar-hover-white' : 'hover:bg-sidebar-hover-white',
                ].join(' ')}
              >
                <p className="text-sm text-hover-black truncate">{c.title || c.channel}</p>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Settings pinned to bottom */}
      <NavButton
        Icon={Settings}
        label="Settings"
        active={location.pathname === '/settings'}
        expanded={expanded}
        onClick={() => navigate('/settings')}
      />
    </aside>
  )
}
