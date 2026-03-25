import { useEffect, useState, useRef } from 'react'
import { Download, Check, Search, Star, ArrowDownToLine, Trash2, ChevronDown, ChevronUp, Plus, X } from 'lucide-react'
import { Markdown } from '@/components/Markdown'
import { api, type Skill, type CatalogSkill } from '@/lib/api'

function formatCount(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

interface InstalledSkill extends Skill {
  instructions: string
  removable: boolean
}

export function SkillsPage() {
  const [tab, setTab] = useState<'custom' | 'clawhub' | 'browse'>('custom')
  const [allSkills, setAllSkills] = useState<InstalledSkill[]>([])
  const [catalog, setCatalog] = useState<CatalogSkill[]>([])
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [catalogError, setCatalogError] = useState<string | null>(null)
  const [installing, setInstalling] = useState<Record<string, boolean>>({})
  const [removing, setRemoving] = useState<Record<string, boolean>>({})
  const [installResults, setInstallResults] = useState<Record<string, { success: boolean; message: string }>>({})
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const searchTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Create form
  const [showCreate, setShowCreate] = useState(false)
  const [createName, setCreateName] = useState('')
  const [createDesc, setCreateDesc] = useState('')
  const [createInstructions, setCreateInstructions] = useState('')
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string | null>(null)

  const loadSkills = () =>
    api.skills().then(res => setAllSkills(res.filter(s => s.tier === 1) as InstalledSkill[]))

  useEffect(() => {
    let cancelled = false
    api.skills()
      .then(res => { if (!cancelled) setAllSkills(res.filter(s => s.tier === 1) as InstalledSkill[]) })
      .catch(() => {})
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    let cancelled = false
    api.skillsCatalog(undefined, 200)
      .then(cat => { if (!cancelled) setCatalog(cat) })
      .catch((err: unknown) => { if (!cancelled) setCatalogError(err instanceof Error ? err.message : 'Failed to load') })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (searchTimer.current) clearTimeout(searchTimer.current)
    if (!query.trim()) return
    searchTimer.current = setTimeout(() => {
      setSearching(true)
      api.skillsCatalog(query, 100)
        .then(res => setCatalog(res))
        .catch((err: unknown) => setCatalogError(err instanceof Error ? err.message : 'Search failed'))
        .finally(() => setSearching(false))
    }, 300)
    return () => { if (searchTimer.current) clearTimeout(searchTimer.current) }
  }, [query])

  useEffect(() => {
    if (query.trim()) return
    api.skillsCatalog(undefined, 200).then(res => setCatalog(res)).catch(() => {})
  }, [query])

  const install = async (skill: CatalogSkill) => {
    setInstalling(prev => ({ ...prev, [skill.name]: true }))
    setInstallResults(prev => { const n = { ...prev }; delete n[skill.name]; return n })
    try {
      const res = await api.skillsInstall(skill.path)
      setInstallResults(prev => ({ ...prev, [skill.name]: { success: res.success, message: res.success ? `Installed as "${res.skill_name}"` : 'Install failed' } }))
      if (res.success) await loadSkills()
    } catch (err) {
      setInstallResults(prev => ({ ...prev, [skill.name]: { success: false, message: err instanceof Error ? err.message : 'Install failed' } }))
    } finally {
      setInstalling(prev => ({ ...prev, [skill.name]: false }))
    }
  }

  const uninstall = async (name: string) => {
    setRemoving(prev => ({ ...prev, [name]: true }))
    try {
      await fetch(`/api/skills/${encodeURIComponent(name)}`, { method: 'DELETE' })
      await loadSkills()
    } catch {
      // silently fail
    } finally {
      setRemoving(prev => ({ ...prev, [name]: false }))
    }
  }

  const createSkill = async () => {
    setCreateError(null)
    if (!createName.trim()) { setCreateError('Name is required'); return }
    if (!createInstructions.trim()) { setCreateError('Instructions are required'); return }
    setCreating(true)
    try {
      const res = await api.skillCreateMarkdown({
        name: createName.trim(),
        description: createDesc.trim() || undefined,
        instructions: createInstructions,
      })
      if (res.success) {
        setCreateName('')
        setCreateDesc('')
        setCreateInstructions('')
        setShowCreate(false)
        await loadSkills()
      }
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Creation failed')
    } finally {
      setCreating(false)
    }
  }

  const custom = allSkills.filter(s => s.source === 'custom')
  const clawhub = allSkills.filter(s => s.source === 'clawhub')
  const installedNames = new Set(allSkills.map(s => s.name))

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          <div className="flex gap-1 text-sm">
            {(['custom', 'clawhub', 'browse'] as const).map(t => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={`px-3 py-1 rounded-md transition-colors ${tab === t ? 'bg-sidebar-white text-hover-black font-medium' : 'text-normal-black hover:text-hover-black'}`}
              >
                {t === 'custom' && <>Custom {custom.length > 0 && <span className="ml-1 text-xs text-normal-black">({custom.length})</span>}</>}
                {t === 'clawhub' && <>ClawHub {clawhub.length > 0 && <span className="ml-1 text-xs text-normal-black">({clawhub.length})</span>}</>}
                {t === 'browse' && 'Browse'}
              </button>
            ))}
          </div>
          {tab === 'custom' && (
            <button
              onClick={() => { setShowCreate(s => !s); setCreateError(null) }}
              className="flex items-center gap-1 text-xs text-normal-black hover:text-hover-black transition-colors"
            >
              {showCreate ? <X size={13} /> : <Plus size={13} />}
              {showCreate ? 'Cancel' : 'New'}
            </button>
          )}
        </div>

        <p className="text-xs text-normal-black mb-5">Markdown instructions injected into the agent's context to guide its behaviour. No code required.</p>

        {tab === 'custom' && showCreate && (
          <div className="mb-6 space-y-3 p-4 rounded-2xl border border-border-white bg-sidebar-white">
            <input
              value={createName}
              onChange={e => setCreateName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ''))}
              placeholder="skill-name"
              className="w-full px-4 py-2 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none font-mono"
            />
            <input
              value={createDesc}
              onChange={e => setCreateDesc(e.target.value)}
              placeholder="Description (optional)"
              className="w-full px-4 py-2 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none"
            />
            <textarea
              value={createInstructions}
              onChange={e => setCreateInstructions(e.target.value)}
              placeholder="Markdown instructions for the agent…"
              rows={10}
              className="w-full px-4 py-3 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none resize-y leading-relaxed"
              spellCheck={false}
            />
            {createError && <p className="text-sm text-red">{createError}</p>}
            <button
              onClick={() => void createSkill()}
              disabled={creating}
              className="px-4 py-2 text-sm rounded-xl bg-brand-primary text-white hover:opacity-80 disabled:opacity-40 transition-opacity"
            >
              {creating ? 'Creating…' : 'Create'}
            </button>
          </div>
        )}

        {tab === 'custom' && !showCreate && (
          <SkillList skills={custom} loading={loading} expanded={expanded} setExpanded={setExpanded} removing={removing} onRemove={uninstall} empty="No custom skills yet. Skills are instructions for how your agent interacts with something — have an agent create one for you, or click New to create one." />
        )}

        {tab === 'clawhub' && (
          <SkillList skills={clawhub} loading={loading} expanded={expanded} setExpanded={setExpanded} removing={removing} onRemove={uninstall} empty="No ClawHub skills installed. Browse and install some." />
        )}

        {tab === 'browse' && (
          <>
            <div className="relative mb-6">
              <Search size={13} className="absolute left-3 top-1/2 -translate-y-1/2 text-normal-black" />
              <input
                value={query}
                onChange={e => setQuery(e.target.value)}
                placeholder="Search ClawHub…"
                className="w-full pl-8 pr-4 py-2 text-sm rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
              />
              {searching && (
                <div className="absolute right-3 top-1/2 -translate-y-1/2 w-3 h-3 border border-normal-black border-t-transparent rounded-full animate-spin" />
              )}
            </div>

            {catalogError && <p className="text-sm text-red mb-4">{catalogError}</p>}

            {catalog.length === 0 ? (
              <p className="text-sm text-normal-black">No skills found.</p>
            ) : (
              <div className="space-y-1">
                {catalog.map(skill => {
                  const isInstalled = installedNames.has(skill.name)
                  const isInstalling = installing[skill.name]
                  const result = installResults[skill.name]
                  return (
                    <div key={skill.name} className="flex items-center gap-4 px-4 py-3 rounded-xl hover:bg-sidebar-white transition-colors">
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-hover-black truncate">{skill.name}</p>
                        {skill.description && (
                          <p className="text-xs text-normal-black truncate mt-0.5">{skill.description}</p>
                        )}
                        {result && (
                          <p className={`text-xs mt-0.5 ${result.success ? 'text-terminal-green' : 'text-red'}`}>
                            {result.message}
                          </p>
                        )}
                      </div>
                      <div className="flex items-center gap-4 shrink-0 text-xs text-normal-black">
                        {skill.downloads > 0 && (
                          <span className="flex items-center gap-1">
                            <ArrowDownToLine size={11} />
                            {formatCount(skill.downloads)}
                          </span>
                        )}
                        {skill.stars > 0 && (
                          <span className="flex items-center gap-1">
                            <Star size={11} />
                            {formatCount(skill.stars)}
                          </span>
                        )}
                        {skill.version && <span className="font-mono">{skill.version}</span>}
                      </div>
                      <button
                        onClick={() => void install(skill)}
                        disabled={isInstalled || isInstalling}
                        className={`shrink-0 h-7 w-7 rounded-full flex items-center justify-center transition-colors ${
                          isInstalled
                            ? 'bg-terminal-green'
                            : 'bg-brand-primary hover:opacity-80 disabled:opacity-40'
                        }`}
                      >
                        {isInstalled ? (
                          <Check size={12} className="text-white" />
                        ) : isInstalling ? (
                          <div className="w-3 h-3 border border-white border-t-transparent rounded-full animate-spin" />
                        ) : (
                          <Download size={12} className="text-white" />
                        )}
                      </button>
                    </div>
                  )
                })}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}

function SkillList({ skills, loading, expanded, setExpanded, removing, onRemove, empty }: {
  skills: InstalledSkill[]
  loading: boolean
  expanded: Record<string, boolean>
  setExpanded: React.Dispatch<React.SetStateAction<Record<string, boolean>>>
  removing: Record<string, boolean>
  onRemove: (name: string) => void
  empty: string
}) {
  if (!loading && skills.length === 0) {
    return <p className="text-sm text-normal-black">{empty}</p>
  }

  return (
    <div className="space-y-2">
      {skills.map(skill => {
        const isExpanded = expanded[skill.name]
        const isRemoving = removing[skill.name]
        return (
          <div key={skill.name} className="rounded-2xl border border-border-white overflow-hidden">
            <div className="flex items-center gap-4 px-4 py-3">
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <p className="text-sm font-medium text-hover-black truncate">{skill.name}</p>
                  {skill.version && (
                    <span className="text-xs text-normal-black font-mono shrink-0">{skill.version}</span>
                  )}
                </div>
                {skill.description && (
                  <p className="text-xs text-normal-black truncate mt-0.5">{skill.description}</p>
                )}
              </div>
              <div className="flex items-center gap-2 shrink-0">
                {skill.instructions?.trim() && (
                  <button
                    onClick={() => setExpanded(prev => ({ ...prev, [skill.name]: !isExpanded }))}
                    className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors"
                  >
                    {isExpanded ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
                  </button>
                )}
                {skill.removable && (
                  <button
                    onClick={() => onRemove(skill.name)}
                    disabled={isRemoving}
                    className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-red hover:bg-sidebar-white transition-colors disabled:opacity-40"
                  >
                    {isRemoving
                      ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                      : <Trash2 size={13} />
                    }
                  </button>
                )}
              </div>
            </div>
            {isExpanded && skill.instructions?.trim() && (
              <div className="border-t border-border-white px-4 py-3 bg-sidebar-white max-h-64 overflow-y-auto">
                <div className="text-sm leading-relaxed text-hover-black prose prose-sm max-w-none [&_*]:text-inherit [&_p]:my-1 [&_pre]:bg-icon-white [&_pre]:rounded-lg [&_pre]:p-3 [&_code]:text-xs [&_p:last-child]:mb-0">
                  <Markdown>{skill.instructions}</Markdown>
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
