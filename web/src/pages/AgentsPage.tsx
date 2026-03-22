import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, type Agent } from '@/lib/api'

function providerColor(provider: string): { background: string; color: string } {
  switch (provider.toLowerCase()) {
    case 'anthropic':
      return { background: 'rgba(168,85,247,0.12)', color: '#a855f7' }
    case 'openai':
      return { background: 'rgba(34,197,94,0.12)', color: '#16a34a' }
    case 'gemini':
    case 'google':
      return { background: 'rgba(56,189,248,0.12)', color: '#0284c7' }
    default:
      return { background: 'var(--color-border-white)', color: 'var(--color-dark-text-normal)' }
  }
}

export function AgentsPage() {
  const [agents, setAgents] = useState<Agent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const navigate = useNavigate()

  useEffect(() => {
    let cancelled = false
    api.agents()
      .then(data => { if (!cancelled) setAgents(data) })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load agents')
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  return (
    <div className="p-6 max-w-4xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Agents
      </h1>
      <p className="text-sm mb-6" style={{ color: 'var(--color-dark-text-normal)' }}>
        Configured agent definitions
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

      {loading ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <div
              key={i}
              className="p-4 rounded-lg border h-28 animate-pulse"
              style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
            />
          ))}
        </div>
      ) : agents.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
          No agents configured.
        </p>
      ) : (
        <div className="space-y-3">
          {agents.map(agent => {
            const pColor = providerColor(agent.provider)
            return (
              <div
                key={agent.id}
                className="p-4 rounded-lg border"
                style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1 flex-wrap">
                      <span className="font-medium text-sm" style={{ color: 'var(--color-text-hover-black)' }}>
                        {agent.name}
                      </span>
                      <span
                        className="text-xs px-2 py-0.5 rounded-md font-medium"
                        style={pColor}
                      >
                        {agent.provider}
                      </span>
                    </div>
                    <p className="text-xs mb-2" style={{ color: 'var(--color-dark-text-normal)' }}>
                      {agent.model} &middot; ID: <span className="font-mono">{agent.id}</span>
                    </p>
                    {agent.skills.length > 0 && (
                      <div className="flex flex-wrap gap-1">
                        {agent.skills.map(skill => (
                          <span
                            key={skill}
                            className="text-xs px-2 py-0.5 rounded border font-mono"
                            style={{
                              borderColor: 'var(--color-border-white)',
                              color: 'var(--color-dark-text-normal)',
                            }}
                          >
                            {skill}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                  <button
                    onClick={() => navigate(`/chat?agent=${agent.id}`)}
                    className="shrink-0 px-3 py-1.5 rounded-md text-xs font-medium transition-opacity hover:opacity-90"
                    style={{ background: 'var(--color-brand-primary)', color: '#ffffff' }}
                  >
                    Chat
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
