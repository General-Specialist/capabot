import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { MessageSquare, History, Terminal, Package, Settings, type LucideIcon } from 'lucide-react'

type NavItem = { Icon: LucideIcon; label: string; route: string; exact?: boolean }

const NAV_ITEMS: NavItem[] = [
  { Icon: MessageSquare, label: 'Chat', route: '/', exact: true },
  { Icon: History, label: 'Conversations', route: '/conversations' },
  { Icon: Terminal, label: 'Logs', route: '/logs' },
  { Icon: Package, label: 'Skills', route: '/skills' },
  { Icon: Settings, label: 'Settings', route: '/settings' },
]

function NavButton({ Icon, label, active, expanded, onClick }: {
  Icon: LucideIcon; label: string; active: boolean; expanded: boolean; onClick: () => void
}) {
  const [hovered, setHovered] = useState(false)
  const cls = [
    'flex items-center cursor-pointer h-[30px] transition-colors border-none outline-none bg-transparent',
    expanded ? 'w-full px-4 rounded-2xl' : 'w-[30px] justify-center rounded-full',
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
  const [expanded, setExpanded] = useState(false)

  const isActive = (item: NavItem) =>
    item.exact ? location.pathname === item.route : location.pathname.startsWith(item.route)

  return (
    <aside
      onMouseEnter={() => setExpanded(true)}
      onMouseLeave={() => setExpanded(false)}
      className={`flex flex-col gap-[12px] p-[12px] bg-sidebar-white rounded-3xl m-4 fixed top-0 left-0 h-[calc(100vh-32px)] z-50 transition-all duration-200 ${
        expanded ? 'w-[200px]' : 'w-[54px]'
      }`}
    >
      <div className={`flex items-center h-[30px] shrink-0 ${expanded ? 'px-4 justify-start' : 'justify-center'}`}>
        <span className="text-sm font-bold text-brand-primary">{expanded ? 'capabot' : 'C'}</span>
      </div>

      <nav className="flex flex-col gap-1 flex-1">
        {NAV_ITEMS.map(item => (
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
    </aside>
  )
}
