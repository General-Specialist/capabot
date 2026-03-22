import { useEffect, useState } from 'react'
import { api, type Skill } from '@/lib/api'

export function SkillsPage() {
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

  return (
    <div className="p-6 max-w-5xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Skills
      </h1>
      <p className="text-sm mb-6" style={{ color: 'var(--color-dark-text-normal)' }}>
        Installed skill modules
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
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div
              key={i}
              className="p-4 rounded-lg border h-32 animate-pulse"
              style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
            />
          ))}
        </div>
      ) : skills.length === 0 ? (
        <p className="text-sm" style={{ color: 'var(--color-dark-text-normal)' }}>
          No skills installed.
        </p>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {skills.map(skill => (
            <div
              key={skill.name}
              className="p-4 rounded-lg border"
              style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
            >
              <div className="flex items-start justify-between gap-2 mb-2">
                <span
                  className="font-mono text-sm font-medium"
                  style={{ color: 'var(--color-brand-primary)' }}
                >
                  {skill.name}
                </span>
                <div className="flex items-center gap-1.5 shrink-0">
                  {skill.has_instructions && (
                    <span
                      className="w-2 h-2 rounded-full shrink-0"
                      style={{ background: '#22c55e' }}
                      title="Has instructions"
                    />
                  )}
                  <span
                    className="text-xs px-2 py-0.5 rounded border"
                    style={{
                      color: 'var(--color-dark-text-normal)',
                      borderColor: 'var(--color-border-white)',
                    }}
                  >
                    v{skill.version}
                  </span>
                </div>
              </div>
              <p className="text-xs leading-relaxed" style={{ color: 'var(--color-dark-text-normal)' }}>
                {skill.description}
              </p>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
