import { NavLink, useLocation } from 'react-router-dom'
import { LayoutDashboard, MessageSquare, History, Zap, Bot, Cpu, Terminal, Settings, Sun, Moon, ChevronLeft, ChevronRight } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useTheme } from '@/hooks/useTheme'
import { useState } from 'react'

const navItems = [
  { to: '/', icon: LayoutDashboard, label: 'Dashboard', exact: true },
  { to: '/chat', icon: MessageSquare, label: 'Chat' },
  { to: '/conversations', icon: History, label: 'Conversations' },
  { to: '/skills', icon: Zap, label: 'Skills' },
  { to: '/agents', icon: Bot, label: 'Agents' },
  { to: '/providers', icon: Cpu, label: 'Providers' },
  { to: '/logs', icon: Terminal, label: 'Logs' },
]

export function Sidebar() {
  const { theme, toggle } = useTheme()
  const [collapsed, setCollapsed] = useState(false)
  const location = useLocation()

  return (
    <aside
      className={cn(
        'flex flex-col h-screen border-r transition-all duration-200 shrink-0',
        collapsed ? 'w-12' : 'w-60'
      )}
      style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
    >
      {/* Header */}
      <div
        className={cn('flex items-center h-14 px-3 border-b', collapsed && 'justify-center')}
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        {!collapsed && (
          <span className="font-semibold text-sm tracking-tight" style={{ color: 'var(--color-brand-primary)' }}>
            capabot
          </span>
        )}
        <button
          onClick={() => setCollapsed(c => !c)}
          className={cn(
            'ml-auto p-1 rounded hover:bg-[var(--color-sidebar-hover-white)] transition-colors',
            collapsed && 'ml-0'
          )}
          style={{ color: 'var(--color-dark-text-normal)' }}
        >
          {collapsed ? <ChevronRight size={14} /> : <ChevronLeft size={14} />}
        </button>
      </div>

      {/* Nav */}
      <nav className="flex-1 py-2 space-y-0.5 px-1.5 overflow-y-auto scrollbar-hide">
        {navItems.map(({ to, icon: Icon, label, exact }) => {
          const active = exact ? location.pathname === to : location.pathname.startsWith(to)
          return (
            <NavLink
              key={to}
              to={to}
              className={cn(
                'flex items-center gap-2.5 px-2 py-1.5 rounded-md text-sm transition-colors',
                active
                  ? 'font-medium'
                  : 'hover:bg-[var(--color-sidebar-hover-white)]',
                collapsed && 'justify-center px-0'
              )}
              style={{
                color: active ? 'var(--color-text-hover-black)' : 'var(--color-dark-text-normal)',
                background: active ? 'var(--color-sidebar-hover-white)' : undefined,
              }}
              title={collapsed ? label : undefined}
            >
              <Icon
                size={15}
                style={{ color: active ? 'var(--color-brand-primary)' : undefined }}
              />
              {!collapsed && <span>{label}</span>}
            </NavLink>
          )
        })}
      </nav>

      {/* Footer */}
      <div
        className={cn('border-t p-1.5 flex flex-col gap-0.5', collapsed && 'items-center')}
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <button
          onClick={toggle}
          className={cn(
            'flex items-center gap-2.5 px-2 py-1.5 rounded-md text-sm w-full transition-colors hover:bg-[var(--color-sidebar-hover-white)]',
            collapsed && 'justify-center px-0 w-auto'
          )}
          style={{ color: 'var(--color-dark-text-normal)' }}
          title={collapsed ? 'Toggle theme' : undefined}
        >
          {theme === 'dark' ? <Sun size={15} /> : <Moon size={15} />}
          {!collapsed && <span>{theme === 'dark' ? 'Light mode' : 'Dark mode'}</span>}
        </button>
        <NavLink
          to="/settings"
          className={cn(
            'flex items-center gap-2.5 px-2 py-1.5 rounded-md text-sm transition-colors hover:bg-[var(--color-sidebar-hover-white)]',
            collapsed && 'justify-center px-0'
          )}
          style={{ color: 'var(--color-dark-text-normal)' }}
          title={collapsed ? 'Settings' : undefined}
        >
          <Settings size={15} />
          {!collapsed && <span>Settings</span>}
        </NavLink>
      </div>
    </aside>
  )
}
