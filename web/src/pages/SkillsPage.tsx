import { useEffect, useRef, useState } from 'react'
import { api, type CatalogSkill, type Skill } from '@/lib/api'

type Tab = 'installed' | 'browse'

export function SkillsPage() {
  const [tab, setTab] = useState<Tab>('installed')

  return (
    <div className="p-6 max-w-5xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Skills
      </h1>
      <p className="text-sm mb-5" style={{ color: 'var(--color-dark-text-normal)' }}>
        Manage and browse skill modules
      </p>

      {/* Tab bar */}
      <div className="flex gap-1 mb-6 border-b" style={{ borderColor: 'var(--color-border-white)' }}>
        {(['installed', 'browse'] as Tab[]).map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className="px-4 py-2 text-sm font-medium capitalize transition-colors"
            style={{
              borderBottom: tab === t ? '2px solid var(--color-brand-primary)' : '2px solid transparent',
              color: tab === t ? 'var(--color-brand-primary)' : 'var(--color-dark-text-normal)',
              background: 'transparent',
            }}
          >
            {t === 'browse' ? 'Browse ClawHub' : 'Installed'}
          </button>
        ))}
      </div>

      {tab === 'installed' ? <InstalledTab /> : <BrowseTab />}
    </div>
  )
}

// --- Installed tab ---

function InstalledTab() {
  const [skills, setSkills] = useState<Skill[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    api.skills()
      .then(data => { if (!cancelled) setSkills(data) })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : 'Failed to load skills')
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  if (loading) return <SkeletonGrid />
  if (error) return <ErrorBanner msg={error} />
  if (skills.length === 0) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
        No skills installed. Use the <strong>Browse ClawHub</strong> tab to install one.
      </p>
    )
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {skills.map(skill => (
        <SkillCard key={skill.name} name={skill.name} version={skill.version} description={skill.description}>
          {skill.has_instructions && (
            <span
              className="w-2 h-2 rounded-full shrink-0"
              style={{ background: '#22c55e' }}
              title="Has instructions"
            />
          )}
        </SkillCard>
      ))}
    </div>
  )
}

// --- Browse tab ---

function BrowseTab() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<CatalogSkill[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [installing, setInstalling] = useState<string | null>(null)
  const [installed, setInstalled] = useState<Set<string>>(new Set())
  const [installError, setInstallError] = useState<string | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const search = (q: string) => {
    setLoading(true)
    setError(null)
    api.skillsCatalog(q || undefined)
      .then(data => setResults(data))
      .catch((err: unknown) => setError(err instanceof Error ? err.message : 'Search failed'))
      .finally(() => setLoading(false))
  }

  // Load all on mount
  useEffect(() => { search('') }, [])

  const handleQueryChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const q = e.target.value
    setQuery(q)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => search(q), 400)
  }

  const handleInstall = async (name: string) => {
    setInstalling(name)
    setInstallError(null)
    try {
      await api.skillsInstall(name)
      setInstalled(prev => new Set(prev).add(name))
    } catch (err) {
      setInstallError(err instanceof Error ? err.message : 'Install failed')
    } finally {
      setInstalling(null)
    }
  }

  return (
    <div>
      {/* Search input */}
      <input
        type="text"
        value={query}
        onChange={handleQueryChange}
        placeholder="Search skills…"
        className="w-full max-w-md rounded-md border px-3 py-2 text-sm mb-4 outline-none transition-colors"
        style={{
          background: 'var(--color-sidebar-white)',
          borderColor: 'var(--color-border-white)',
          color: 'var(--color-text-hover-black)',
        }}
      />

      {installError && <ErrorBanner msg={installError} />}
      {error && <ErrorBanner msg={error} />}

      {loading ? (
        <SkeletonGrid />
      ) : results && results.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
          No skills found{query ? ` for "${query}"` : ''}.
        </p>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {(results ?? []).map(skill => (
            <SkillCard key={skill.name} name={skill.name} version={skill.version} description={skill.description}>
              <InstallButton
                name={skill.name}
                installing={installing === skill.name}
                done={installed.has(skill.name)}
                onInstall={handleInstall}
              />
            </SkillCard>
          ))}
        </div>
      )}
    </div>
  )
}

// --- shared sub-components ---

function SkillCard({
  name,
  version,
  description,
  children,
}: {
  name: string
  version: string
  description: string
  children?: React.ReactNode
}) {
  return (
    <div
      className="p-4 rounded-lg border flex flex-col gap-2"
      style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
    >
      <div className="flex items-start justify-between gap-2">
        <span
          className="font-mono text-sm font-medium truncate"
          style={{ color: 'var(--color-brand-primary)' }}
        >
          {name}
        </span>
        <div className="flex items-center gap-1.5 shrink-0">
          {children}
          {version && (
            <span
              className="text-xs px-2 py-0.5 rounded border"
              style={{ color: 'var(--color-dark-text-normal)', borderColor: 'var(--color-border-white)' }}
            >
              v{version}
            </span>
          )}
        </div>
      </div>
      <p className="text-xs leading-relaxed flex-1" style={{ color: 'var(--color-dark-text-normal)' }}>
        {description || <em>No description</em>}
      </p>
    </div>
  )
}

function InstallButton({
  name,
  installing,
  done,
  onInstall,
}: {
  name: string
  installing: boolean
  done: boolean
  onInstall: (name: string) => void
}) {
  if (done) {
    return (
      <span className="text-xs px-2 py-0.5 rounded" style={{ background: 'rgba(34,197,94,0.12)', color: '#22c55e' }}>
        Installed
      </span>
    )
  }
  return (
    <button
      disabled={installing}
      onClick={() => onInstall(name)}
      className="text-xs px-2 py-0.5 rounded border transition-opacity disabled:opacity-50"
      style={{
        borderColor: 'var(--color-brand-primary)',
        color: 'var(--color-brand-primary)',
        background: 'transparent',
      }}
    >
      {installing ? 'Installing…' : 'Install'}
    </button>
  )
}

function SkeletonGrid() {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      {Array.from({ length: 6 }).map((_, i) => (
        <div
          key={i}
          className="p-4 rounded-lg border h-32 animate-pulse"
          style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
        />
      ))}
    </div>
  )
}

function ErrorBanner({ msg }: { msg: string }) {
  return (
    <div
      className="mb-4 p-3 rounded-md text-sm border"
      style={{
        background: 'rgba(239,68,68,0.08)',
        borderColor: 'var(--color-red)',
        color: 'var(--color-red)',
      }}
    >
      {msg}
    </div>
  )
}
