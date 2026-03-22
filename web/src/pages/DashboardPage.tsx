import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bot, History, MessageSquare, Zap } from 'lucide-react'
import { api } from '@/lib/api'

interface Stats {
  agents: number
  conversations: number
  skills: number
  providers: number
}

function StatCard({
  title,
  value,
  icon: Icon,
  loading,
}: {
  title: string
  value: number
  icon: React.ElementType
  loading: boolean
}) {
  return (
    <div
      className="p-4 rounded-lg border flex items-center justify-between"
      style={{
        background: 'var(--color-sidebar-white)',
        borderColor: 'var(--color-border-white)',
      }}
    >
      <div>
        <p className="text-xs mb-1" style={{ color: 'var(--color-dark-text-normal)' }}>
          {title}
        </p>
        {loading ? (
          <div
            className="h-7 w-12 rounded animate-pulse"
            style={{ background: 'var(--color-border-white)' }}
          />
        ) : (
          <p className="text-2xl font-bold" style={{ color: 'var(--color-text-hover-black)' }}>
            {value}
          </p>
        )}
      </div>
      <Icon size={20} style={{ color: 'var(--color-dark-text-normal)' }} />
    </div>
  )
}

export function DashboardPage() {
  const navigate = useNavigate()
  const [stats, setStats] = useState<Stats>({ agents: 0, conversations: 0, skills: 0, providers: 0 })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  useEffect(() => {
    let cancelled = false
    Promise.all([
      api.agents().catch(() => [] as Awaited<ReturnType<typeof api.agents>>),
      api.conversations().catch(() => [] as Awaited<ReturnType<typeof api.conversations>>),
      api.skills().catch(() => [] as Awaited<ReturnType<typeof api.skills>>),
      api.providers().catch(() => [] as Awaited<ReturnType<typeof api.providers>>),
    ])
      .then(([agents, conversations, skills, providers]) => {
        if (!cancelled) {
          setStats({
            agents: agents.length,
            conversations: conversations.length,
            skills: skills.length,
            providers: providers.length,
          })
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load stats')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [])

  const handleQuickChat = (e: React.FormEvent) => {
    e.preventDefault()
    if (query.trim()) {
      navigate(`/chat?q=${encodeURIComponent(query.trim())}`)
    }
  }

  return (
    <div className="p-6 max-w-4xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Dashboard
      </h1>
      <p className="text-sm mb-6" style={{ color: 'var(--color-dark-text-normal)' }}>
        Overview of your Capabot instance
      </p>

      {error && (
        <div
          className="mb-4 p-3 rounded-md text-sm border"
          style={{
            background: 'rgba(239,68,68,0.08)',
            borderColor: 'var(--color-red)',
            color: 'var(--color-red)',
          }}
        >
          {error}
        </div>
      )}

      <div className="grid grid-cols-2 gap-3 mb-8 sm:grid-cols-4">
        <StatCard title="Active Agents" value={stats.agents} icon={Bot} loading={loading} />
        <StatCard title="Conversations" value={stats.conversations} icon={History} loading={loading} />
        <StatCard title="Installed Skills" value={stats.skills} icon={Zap} loading={loading} />
        <StatCard title="LLM Providers" value={stats.providers} icon={MessageSquare} loading={loading} />
      </div>

      <div
        className="p-4 rounded-lg border"
        style={{
          background: 'var(--color-sidebar-white)',
          borderColor: 'var(--color-border-white)',
        }}
      >
        <h2 className="text-sm font-medium mb-3" style={{ color: 'var(--color-text-hover-black)' }}>
          Quick Chat
        </h2>
        <form onSubmit={handleQuickChat} className="flex gap-2">
          <input
            type="text"
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="Ask anything..."
            className="flex-1 px-3 py-2 rounded-md border text-sm"
            style={{
              background: 'var(--color-white)',
              borderColor: 'var(--color-border-white)',
              color: 'var(--color-text-hover-black)',
            }}
          />
          <button
            type="submit"
            className="px-4 py-2 rounded-md text-sm font-medium transition-opacity hover:opacity-90"
            style={{
              background: 'var(--color-brand-primary)',
              color: '#ffffff',
            }}
          >
            Chat
          </button>
        </form>
      </div>
    </div>
  )
}
