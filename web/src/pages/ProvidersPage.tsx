import { useEffect, useState } from 'react'
import { Cpu, ChevronDown, ChevronUp } from 'lucide-react'
import { api, type ProviderInfo } from '@/lib/api'

const PROVIDER_COLORS: Record<string, string> = {
  anthropic: 'var(--color-terminal-purple)',
  openai: 'var(--color-terminal-green)',
  gemini: 'var(--color-terminal-blue)',
  router: 'var(--color-terminal-yellow)',
}

function ProviderCard({ provider }: { provider: ProviderInfo }) {
  const [expanded, setExpanded] = useState(false)
  const color = PROVIDER_COLORS[provider.name] ?? 'var(--color-dark-text-normal)'

  return (
    <div
      className="rounded-lg border p-4"
      style={{ borderColor: 'var(--color-border-white)', background: 'var(--color-sidebar-white)' }}
    >
      <button
        className="w-full flex items-center justify-between"
        onClick={() => setExpanded(e => !e)}
      >
        <div className="flex items-center gap-3">
          <div
            className="w-2 h-2 rounded-full shrink-0"
            style={{ background: color }}
          />
          <span className="font-mono text-sm font-medium" style={{ color }}>
            {provider.name}
          </span>
          <span
            className="text-xs px-1.5 py-0.5 rounded border font-mono"
            style={{ color: 'var(--color-dark-text-normal)', borderColor: 'var(--color-border-white)' }}
          >
            {provider.models.length} model{provider.models.length !== 1 ? 's' : ''}
          </span>
        </div>
        {expanded
          ? <ChevronUp size={14} style={{ color: 'var(--color-dark-text-normal)' }} />
          : <ChevronDown size={14} style={{ color: 'var(--color-dark-text-normal)' }} />
        }
      </button>

      {expanded && (
        <div className="mt-3 space-y-1.5">
          {provider.models.map(m => (
            <div
              key={m.id}
              className="flex items-center justify-between px-3 py-2 rounded-md"
              style={{ background: 'var(--color-icon-white)' }}
            >
              <div>
                <p className="text-xs font-mono" style={{ color: 'var(--color-text-hover-black)' }}>{m.id}</p>
                <p className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>{m.name}</p>
              </div>
              <span
                className="text-xs font-mono"
                style={{ color: 'var(--color-dark-text-normal)' }}
              >
                {(m.context_window / 1000).toFixed(0)}K ctx
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export function ProvidersPage() {
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.providers()
      .then(setProviders)
      .catch(e => setError(e instanceof Error ? e.message : 'Failed to load providers'))
      .finally(() => setLoading(false))
  }, [])

  return (
    <div className="flex flex-col h-full">
      <div
        className="flex items-center px-4 h-14 border-b shrink-0"
        style={{ borderColor: 'var(--color-border-white)' }}
      >
        <Cpu size={15} className="mr-2" style={{ color: 'var(--color-brand-primary)' }} />
        <h1 className="text-sm font-semibold" style={{ color: 'var(--color-text-hover-black)' }}>
          Providers
        </h1>
      </div>

      <div className="flex-1 overflow-y-auto p-4 scrollbar-hide">
        {loading && (
          <div className="space-y-3">
            {[0, 1, 2].map(i => (
              <div
                key={i}
                className="h-16 rounded-lg animate-pulse"
                style={{ background: 'var(--color-sidebar-hover-white)' }}
              />
            ))}
          </div>
        )}

        {error && (
          <p className="text-sm" style={{ color: 'var(--color-red)' }}>{error}</p>
        )}

        {!loading && !error && providers.length === 0 && (
          <div className="flex flex-col items-center justify-center h-64 gap-2">
            <Cpu size={32} style={{ color: 'var(--color-dark-text-normal)' }} />
            <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
              No providers configured
            </p>
            <p className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>
              Add API keys via <code className="font-mono">capabot config set</code>
            </p>
          </div>
        )}

        <div className="space-y-3">
          {providers.map(p => (
            <ProviderCard key={p.name} provider={p} />
          ))}
        </div>
      </div>
    </div>
  )
}
