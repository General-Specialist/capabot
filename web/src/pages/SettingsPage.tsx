import { useEffect, useState } from 'react'
import { api, type ProviderInfo, type HealthStatus } from '@/lib/api'

function useDarkMode() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  const toggle = () => {
    const next = !dark
    setDark(next)
    document.documentElement.classList.toggle('dark', next)
    localStorage.setItem('darkMode', String(next))
  }
  return { dark, toggle }
}

export function SettingsPage() {
  const [health, setHealth] = useState<HealthStatus | null>(null)
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const { dark, toggle } = useDarkMode()

  useEffect(() => {
    let cancelled = false
    Promise.all([api.health(), api.providers()])
      .then(([h, p]) => { if (!cancelled) { setHealth(h); setProviders(p) } })
      .catch((err: unknown) => { if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  function formatUptime(secs: number) {
    const h = Math.floor(secs / 3600)
    const m = Math.floor((secs % 3600) / 60)
    const s = secs % 60
    if (h > 0) return `${h}h ${m}m`
    if (m > 0) return `${m}m ${s}s`
    return `${s}s`
  }

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl space-y-8">
        <h1 className="text-lg font-semibold text-hover-black">Settings</h1>

        {error && <p className="text-sm text-red">{error}</p>}

        <section>
          <h2 className="text-xs font-semibold text-normal-black uppercase tracking-wider mb-3">Appearance</h2>
          <div className="rounded-xl border border-border-white">
            <Row label="Theme">
              <button
                onClick={toggle}
                className={`relative w-10 h-5 rounded-full transition-colors ${dark ? 'bg-brand-primary' : 'bg-sidebar-hover-white'}`}
              >
                <span className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${dark ? 'translate-x-5' : 'translate-x-0'}`} />
              </button>
            </Row>
          </div>
        </section>

        {loading ? (
          <div className="space-y-3">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="h-10 rounded-lg animate-pulse bg-sidebar-hover-white" />
            ))}
          </div>
        ) : (
          <>
            {health && (
              <section>
                <h2 className="text-xs font-semibold text-normal-black uppercase tracking-wider mb-3">System</h2>
                <div className="rounded-xl border border-border-white divide-y divide-border-white">
                  <Row label="Status">
                    <span className={`text-xs px-2 py-0.5 rounded-full font-mono ${health.status === 'ok' ? 'bg-terminal-green text-white' : 'bg-red text-white'}`}>
                      {health.status}
                    </span>
                  </Row>
                  <Row label="Version"><span className="font-mono">{health.version}</span></Row>
                  <Row label="Uptime"><span className="font-mono">{formatUptime(health.uptime_seconds)}</span></Row>
                  <Row label="Skills loaded"><span className="font-mono">{health.skills_loaded}</span></Row>
                  <Row label="Providers"><span className="font-mono">{health.providers_count}</span></Row>
                </div>
              </section>
            )}

            <section>
              <h2 className="text-xs font-semibold text-normal-black uppercase tracking-wider mb-3">Providers</h2>
              {providers.length === 0 ? (
                <p className="text-sm text-normal-black">No providers configured. Add API keys to your config file.</p>
              ) : (
                <div className="space-y-3">
                  {providers.map(p => (
                    <div key={p.name} className="rounded-xl border border-border-white">
                      <div className="px-4 py-3 flex items-center justify-between">
                        <span className="text-sm font-medium text-hover-black capitalize">{p.name}</span>
                        <span className="text-xs text-normal-black">{p.models.length} model{p.models.length !== 1 ? 's' : ''}</span>
                      </div>
                      {p.models.length > 0 && (
                        <div className="border-t border-border-white divide-y divide-border-white">
                          {p.models.map(m => (
                            <div key={m.id} className="px-4 py-2.5 flex items-center justify-between">
                              <span className="text-xs text-hover-black font-mono">{m.id}</span>
                              <span className="text-xs text-normal-black">{(m.context_window / 1000).toFixed(0)}k ctx</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </section>
          </>
        )}
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="px-4 py-3 flex items-center justify-between">
      <span className="text-sm text-normal-black">{label}</span>
      <span className="text-sm text-hover-black">{children}</span>
    </div>
  )
}
