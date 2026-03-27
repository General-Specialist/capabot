import { useEffect, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Plus, Trash2, X, ChevronDown, ChevronUp } from 'lucide-react'
import { api, type ChannelConfig, type Skill, type ProviderInfo, type Person } from '@/lib/api'
import { useAlert } from '@/components/AlertProvider'
import TagPicker from '@/components/TagPicker'

const EMPTY: ChannelConfig = { channel_id: '', tag: '', system_prompt: '', skill_names: [], model: '', memory_isolated: false }
const NAMES_KEY = 'channel-names'

function loadNames(): Record<string, string> {
  try { return JSON.parse(localStorage.getItem(NAMES_KEY) || '{}') } catch { return {} }
}
function saveName(id: string, name: string) {
  const names = loadNames()
  localStorage.setItem(NAMES_KEY, JSON.stringify({ ...names, [id]: name }))
}
function deleteName(id: string) {
  const names = loadNames()
  delete names[id]
  localStorage.setItem(NAMES_KEY, JSON.stringify(names))
}

export function ChannelsPage() {
  const { alert } = useAlert()
  const [channels, setChannels] = useState<ChannelConfig[]>([])
  const [skills, setSkills] = useState<Skill[]>([])
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [people, setPeople] = useState<Person[]>([])
  const [loading, setLoading] = useState(true)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [showCreate, setShowCreate] = useState(false)
  const [newId, setNewId] = useState('')
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<Record<string, boolean>>({})
  const [names, setNames] = useState<Record<string, string>>({})
  const saveTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})
  const initialLoad = useRef(true)

  const load = () => api.channels().then(setChannels).catch((err: unknown) =>
    alert(err instanceof Error ? err.message : 'Failed to load channels', 'error'))

  useEffect(() => {
    let cancelled = false
    Promise.all([api.channels(), api.skills(), api.providers(), api.people()])
      .then(([ch, sk, pr, pp]) => {
        if (cancelled) return
        setChannels(ch)
        setSkills(sk)
        setProviders(pr)
        setPeople(pp)
        // Resolve channel names in the background — best effort.
        ch.forEach(c => {
          api.channelResolve(c.channel_id)
            .then(r => setNames(prev => ({ ...prev, [c.channel_id]: r.name })))
            .catch(() => {})
        })
        initialLoad.current = false
      })
      .catch((err: unknown) => {
        if (!cancelled) alert(err instanceof Error ? err.message : 'Failed to load', 'error')
      })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const allModels = providers.flatMap(p => p.models.map(m => ({ ...m, provider: p.name })))
  const personUsernames = new Set(people.filter(p => p.username).map(p => p.username))
  const allTags = [...new Set([
    ...people.flatMap(p => p.tags || []),
    ...personUsernames,
  ])].sort()

  // The backend stores person bindings as "persona:username"; strip/restore transparently.
  const tagToDisplay = (tag: string) => tag.startsWith('persona:') ? tag.slice('persona:'.length) : tag
  const tagToStore = (tag: string) => personUsernames.has(tag) ? 'persona:' + tag : tag

  const save = (cfg: ChannelConfig) => {
    if (initialLoad.current) return
    if (saveTimers.current[cfg.channel_id]) clearTimeout(saveTimers.current[cfg.channel_id])
    saveTimers.current[cfg.channel_id] = setTimeout(() => {
      api.channelSet(cfg.channel_id, cfg).catch((err: unknown) =>
        alert(err instanceof Error ? err.message : 'Save failed', 'error'))
    }, 800)
  }

  const update = (id: string, patch: Partial<ChannelConfig>) => {
    setChannels(prev => {
      const next = prev.map(c => c.channel_id === id ? { ...c, ...patch } : c)
      const updated = next.find(c => c.channel_id === id)
      if (updated) save(updated)
      return next
    })
  }

  const create = async () => {
    const id = newId.trim()
    if (!id) return
    if (channels.some(c => c.channel_id === id)) {
      alert('Channel already exists', 'warning')
      return
    }
    setCreating(true)
    try {
      await api.channelSet(id, { ...EMPTY, channel_id: id })
      setNewId('')
      setShowCreate(false)
      await load()
      setExpanded(prev => ({ ...prev, [id]: true }))
      api.channelResolve(id).then(r => setNames(prev => ({ ...prev, [id]: r.name }))).catch(() => {})
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Create failed', 'error')
    } finally {
      setCreating(false)
    }
  }

  const remove = async (id: string) => {
    setDeleting(prev => ({ ...prev, [id]: true }))
    try {
      await api.channelDelete(id)
      setChannels(prev => prev.filter(c => c.channel_id !== id))
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Delete failed', 'error')
    } finally {
      setDeleting(prev => ({ ...prev, [id]: false }))
    }
  }

  if (loading) {
    return (
      <div className="w-full min-h-screen bg-white px-6 py-6">
        <div className="max-w-3xl mx-auto">
          <p className="text-sm text-normal-black">Loading…</p>
        </div>
      </div>
    )
  }

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto">
        <div className="flex items-center justify-between mb-4">
          <p className="text-xs text-normal-black">Per-channel configuration for Discord, Slack, and Telegram.</p>
          <button
            onClick={() => { setShowCreate(s => !s); setNewId('') }}
            className="flex items-center gap-1 text-xs text-normal-black hover:text-hover-black transition-colors"
          >
            {showCreate ? <X size={13} /> : <Plus size={13} />}
            {showCreate ? 'Cancel' : 'New'}
          </button>
        </div>

        <AnimatePresence>
          {showCreate && (
            <motion.div
              key="channel-create"
              initial={{ opacity: 0, scale: 0.96, y: -4 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.96, y: -4 }}
              transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
              className="mb-6 p-4 rounded-2xl border border-border-white bg-sidebar-white space-y-3"
            >
              <input
                value={newId}
                onChange={e => setNewId(e.target.value)}
                placeholder="Channel ID (e.g. Discord channel ID)"
                className="w-full px-4 py-2 text-sm rounded-xl border border-border-white bg-white text-hover-black outline-none font-mono"
                onKeyDown={e => e.key === 'Enter' && void create()}
              />
              <button
                onClick={() => void create()}
                disabled={creating || !newId.trim()}
                className="px-4 py-2 text-sm rounded-xl bg-brand-primary text-white hover:opacity-80 disabled:opacity-40 transition-opacity"
              >
                {creating ? 'Creating…' : 'Create'}
              </button>
            </motion.div>
          )}
        </AnimatePresence>

        {channels.length === 0 && !showCreate && (
          <p className="text-sm text-normal-black">No channels configured. Click New to bind a channel.</p>
        )}

        <div className="space-y-2">
          {channels.map(ch => {
            const isExpanded = expanded[ch.channel_id]
            const isDeleting = deleting[ch.channel_id]
            return (
              <div key={ch.channel_id} className="rounded-2xl border border-border-white overflow-hidden">
                <div className="flex items-center gap-4 px-4 py-3">
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-hover-black truncate">
                      {names[ch.channel_id] ?? ch.channel_id}
                    </p>
                    <div className="flex items-center gap-2 mt-0.5">
                      {ch.tag && <span className="text-xs text-normal-black">{tagToDisplay(ch.tag)}</span>}
                      {ch.model && <span className="text-xs text-normal-black">model: {ch.model}</span>}
                      {ch.memory_isolated && <span className="text-[10px] px-1.5 py-0.5 rounded bg-sidebar-white text-normal-black border border-border-white">isolated</span>}
                      {ch.skill_names.length > 0 && <span className="text-xs text-normal-black">{ch.skill_names.length} skill{ch.skill_names.length !== 1 ? 's' : ''}</span>}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      onClick={() => setExpanded(prev => ({ ...prev, [ch.channel_id]: !isExpanded }))}
                      className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors"
                    >
                      {isExpanded ? <ChevronUp size={13} /> : <ChevronDown size={13} />}
                    </button>
                    <button
                      onClick={() => void remove(ch.channel_id)}
                      disabled={isDeleting}
                      className="h-7 w-7 rounded-full flex items-center justify-center text-normal-black hover:text-red hover:bg-sidebar-white transition-colors disabled:opacity-40"
                    >
                      {isDeleting
                        ? <div className="w-3 h-3 border border-current border-t-transparent rounded-full animate-spin" />
                        : <Trash2 size={13} />}
                    </button>
                  </div>
                </div>

                {isExpanded && (
                  <div className="border-t border-border-white px-4 py-4 bg-sidebar-white space-y-4">
                    <div>
                      <label className="block text-xs text-normal-black mb-1">Default role</label>
                      <TagPicker
                        allTags={allTags}
                        value={ch.tag ? [tagToDisplay(ch.tag)] : []}
                        onChange={tags => update(ch.channel_id, { tag: tags.length ? tagToStore(tags[tags.length - 1]) : '' })}
                      />
                    </div>

                    <div>
                      <label className="block text-xs text-normal-black mb-1">System prompt override</label>
                      <textarea
                        value={ch.system_prompt}
                        onChange={e => update(ch.channel_id, { system_prompt: e.target.value })}
                        placeholder="Leave empty to use default"
                        rows={4}
                        className="w-full text-sm px-3 py-2 rounded-lg border border-border-white bg-white text-hover-black outline-none resize-y font-mono"
                      />
                    </div>

                    <div>
                      <label className="block text-xs text-normal-black mb-1">Model override</label>
                      <select
                        value={ch.model}
                        onChange={e => update(ch.channel_id, { model: e.target.value })}
                        className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-white text-hover-black outline-none"
                      >
                        <option value="">Default</option>
                        {allModels.map(m => (
                          <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
                        ))}
                      </select>
                    </div>

                    <div>
                      <label className="block text-xs text-normal-black mb-1">Skills (empty = all)</label>
                      <SkillMultiSelect
                        selected={ch.skill_names}
                        skills={skills}
                        onChange={names => update(ch.channel_id, { skill_names: names })}
                      />
                    </div>

                    <div className="flex items-center justify-between">
                      <div>
                        <span className="text-xs text-normal-black">Isolate memory</span>
                        <p className="text-[11px] text-normal-black opacity-60">Keep this channel's memory separate from others</p>
                      </div>
                      <button
                        onClick={() => update(ch.channel_id, { memory_isolated: !ch.memory_isolated })}
                        className={`relative w-10 h-5 rounded-full transition-colors flex-shrink-0 ${ch.memory_isolated ? 'bg-brand-primary' : 'bg-sidebar-hover-white'}`}
                      >
                        <span className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${ch.memory_isolated ? 'translate-x-5' : 'translate-x-0'}`} />
                      </button>
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

function SkillMultiSelect({ selected, skills, onChange }: {
  selected: string[]
  skills: Skill[]
  onChange: (names: string[]) => void
}) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const available = skills.filter(s => !selected.includes(s.name) && s.name.toLowerCase().includes(filter.toLowerCase()))

  return (
    <div>
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mb-2">
          {selected.map(name => (
            <span key={name} className="inline-flex items-center gap-1 px-2 py-0.5 rounded-lg bg-white text-xs text-hover-black border border-border-white">
              {name}
              <button onClick={() => onChange(selected.filter(n => n !== name))} className="text-normal-black opacity-40 hover:opacity-100">&times;</button>
            </span>
          ))}
        </div>
      )}
      <button
        onClick={() => setOpen(o => !o)}
        className="text-xs text-normal-black hover:text-hover-black transition-colors"
      >
        {open ? '− Close' : '+ Add skill'}
      </button>
      {open && (
        <div className="mt-2 rounded-lg border border-border-white bg-white max-h-48 overflow-y-auto">
          <input
            value={filter}
            onChange={e => setFilter(e.target.value)}
            placeholder="Filter…"
            className="w-full px-3 py-1.5 text-xs border-b border-border-white outline-none text-hover-black"
            autoFocus
          />
          {available.length === 0 ? (
            <p className="px-3 py-2 text-xs text-normal-black">No skills available</p>
          ) : (
            available.map(s => (
              <button
                key={s.name}
                onClick={() => { onChange([...selected, s.name]); setFilter('') }}
                className="w-full text-left px-3 py-1.5 text-xs text-hover-black hover:bg-sidebar-white transition-colors"
              >
                {s.name}
                {s.description && <span className="ml-2 text-normal-black opacity-60">{s.description}</span>}
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}
