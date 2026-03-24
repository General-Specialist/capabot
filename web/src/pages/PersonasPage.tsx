import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Plus, Trash2, Check, X, Camera, Search } from 'lucide-react'
import { api, type Persona } from '@/lib/api'
import TagPicker from '@/components/TagPicker'

function PersonaForm({
  initial,
  allTags,
  onSave,
  onCancel,
}: {
  initial?: Persona
  allTags: string[]
  onSave: (data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => Promise<void>
  onCancel: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [prompt, setPrompt] = useState(initial?.prompt ?? '')
  const [username, setUsername] = useState(initial?.username ?? '')
  const [avatarUrl, setAvatarUrl] = useState(initial?.avatar_url ?? '')
  const [tags, setTags] = useState<string[]>(initial?.tags ?? [])
  const [usernameTouched, setUsernameTouched] = useState(!!initial?.username)
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const handleAvatarUpload = async (file: File) => {
    setUploading(true)
    try {
      const url = await api.avatarUpload(file)
      setAvatarUrl(url)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Upload failed')
    } finally {
      setUploading(false)
    }
  }

  const handleSave = async () => {
    if (!name.trim()) { setError('Name is required'); return }
    setSaving(true)
    try {
      await onSave({ name: name.trim(), prompt, username: username.trim(), avatar_url: avatarUrl.trim(), tags })
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save')
      setSaving(false)
    }
  }

  return (
    <div className="flex gap-3">
      <div className="shrink-0">
        <input ref={fileRef} type="file" accept="image/*" className="hidden" onChange={e => { const f = e.target.files?.[0]; if (f) handleAvatarUpload(f) }} />
        <button
          type="button"
          onClick={() => fileRef.current?.click()}
          disabled={uploading}
          className="w-14 h-14 rounded-full bg-sidebar-white border border-border-white flex items-center justify-center overflow-hidden hover:opacity-80 transition-opacity disabled:opacity-50"
        >
          {avatarUrl
            ? <img src={avatarUrl} alt="" className="w-full h-full object-cover" />
            : <Camera className="w-5 h-5 text-normal-black" />
          }
        </button>
      </div>
      <div className="flex-1 flex flex-col gap-2">
      <div className="flex gap-2">
        <input
          className="flex-1 border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none"
          placeholder="Name (e.g. Product Manager)"
          value={name}
          onChange={e => {
            const v = e.target.value
            setName(v)
            if (!usernameTouched) setUsername(v.replace(/\s/g, ''))
          }}
          disabled={!!initial}
        />
        <input
          className="flex-1 border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none font-mono"
          placeholder="@username"
          value={username}
          onChange={e => {
            setUsernameTouched(true)
            setUsername(e.target.value.replace(/\s/g, ''))
          }}
        />
      </div>
      <TagPicker allTags={allTags} value={tags} onChange={setTags} />
      <textarea
        className="border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none font-mono resize-none"
        placeholder="Personality prompt…"
        rows={5}
        value={prompt}
        onChange={e => setPrompt(e.target.value)}
      />
      {error && <p className="text-xs text-red">{error}</p>}
      <div className="flex gap-2 justify-end">
        <button
          type="button"
          onClick={onCancel}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-capsule text-sm text-normal-black hover:bg-sidebar-white transition-colors"
        >
          <X className="w-3.5 h-3.5" /> Cancel
        </button>
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-capsule text-sm bg-[var(--color-brand-primary)] text-white hover:opacity-80 disabled:opacity-50 transition-opacity"
        >
          <Check className="w-3.5 h-3.5" /> {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
      </div>
    </div>
  )
}

function PersonaCard({ persona: p, allTags, deleting, onSave, onDelete }: {
  persona: Persona
  allTags: string[]
  deleting: boolean
  onSave: (data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => Promise<void>
  onDelete: () => void
}) {
  const [username, setUsername] = useState(p.username)
  const [prompt, setPrompt] = useState(p.prompt)
  const [avatarUrl, setAvatarUrl] = useState(p.avatar_url)
  const [tags, setTags] = useState<string[]>(p.tags ?? [])
  const [uploading, setUploading] = useState(false)
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved'>('idle')
  const fileRef = useRef<HTMLInputElement>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout>>()

  const handleAvatarUpload = async (file: File) => {
    setUploading(true)
    try {
      const url = await api.avatarUpload(file)
      setAvatarUrl(url)
      doSave({ name: p.name, prompt, username: username.trim(), avatar_url: url, tags })
    } catch { /* ignore */ } finally { setUploading(false) }
  }

  const doSave = useCallback((data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => {
    clearTimeout(timerRef.current)
    timerRef.current = setTimeout(async () => {
      setSaveStatus('saving')
      try {
        await onSave(data)
        setSaveStatus('saved')
        setTimeout(() => setSaveStatus(s => s === 'saved' ? 'idle' : s), 1500)
      } catch {
        setSaveStatus('idle')
      }
    }, 800)
  }, [onSave])

  const updateField = useCallback(<K extends 'username' | 'prompt' | 'tags'>(field: K, value: K extends 'tags' ? string[] : string) => {
    const next = { name: p.name, prompt, username: username.trim(), avatar_url: avatarUrl, tags }
    if (field === 'username') { setUsername(value as string); next.username = (value as string).trim() }
    else if (field === 'prompt') { setPrompt(value as string); next.prompt = value as string }
    else if (field === 'tags') { setTags(value as string[]); next.tags = value as string[] }
    doSave(next)
  }, [p.name, prompt, username, avatarUrl, tags, doSave])

  useEffect(() => () => clearTimeout(timerRef.current), [])

  return (
    <div className="border border-border-white rounded-xl p-5">
      <div className="flex items-start gap-4 mb-2">
        <input ref={fileRef} type="file" accept="image/*" className="hidden" onChange={e => { const f = e.target.files?.[0]; if (f) handleAvatarUpload(f) }} />
        <button type="button" onClick={() => fileRef.current?.click()} disabled={uploading} className="shrink-0 hover:opacity-80 transition-opacity disabled:opacity-50">
          <div className="w-14 h-14 rounded-full bg-sidebar-white border border-border-white shrink-0 overflow-hidden flex items-center justify-center">
            {avatarUrl
              ? <img src={avatarUrl} alt="" className="w-full h-full object-cover" />
              : <span className="text-sm font-medium text-normal-black">{p.name[0]?.toUpperCase()}</span>
            }
          </div>
        </button>
        <div className="flex-1 min-w-0">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5 min-w-0">
              <span className="text-sm font-medium text-hover-black truncate">{p.name}</span>
              <input
                value={username}
                onChange={e => updateField('username', e.target.value.replace(/\s/g, ''))}
                placeholder="@username"
                className="text-xs text-normal-black bg-transparent outline-none border-b border-transparent focus:border-border-white flex-1 min-w-0 font-mono"
              />
            </div>
            <div className="flex items-center gap-1 shrink-0">
              {saveStatus === 'saving' && <span className="text-[10px] text-normal-black animate-pulse">Saving…</span>}
              {saveStatus === 'saved' && <span className="text-[10px] text-green-500">Saved</span>}
              <button type="button" onClick={onDelete} disabled={deleting} className="p-1 rounded-lg hover:bg-sidebar-white text-normal-black hover:text-red disabled:opacity-50 transition-colors">
                <Trash2 className="w-3 h-3" />
              </button>
            </div>
          </div>
          <div className="mt-2">
            <TagPicker allTags={allTags} value={tags} onChange={v => updateField('tags', v)} />
          </div>
        </div>
      </div>
      <textarea
        value={prompt}
        onChange={e => updateField('prompt', e.target.value)}
        placeholder="Personality prompt…"
        rows={3}
        className="w-full text-xs text-normal-black font-mono bg-transparent outline-none resize-none border border-transparent focus:border-border-white rounded-lg px-2 py-1.5 transition-colors"
      />
    </div>
  )
}

export function PersonasPage() {
  const [personas, setPersonas] = useState<Persona[]>([])
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<number | null>(null)
  const [filterTag, setFilterTag] = useState<string | null>(null)
  const [tagQuery, setTagQuery] = useState('')
  const [tagFocused, setTagFocused] = useState(false)

  const allTags = useMemo(() => [...new Set(personas.flatMap(p => p.tags || []))].sort(), [personas])
  const filtered = filterTag ? personas.filter(p => p.tags?.includes(filterTag)) : personas
  const tagOptions = allTags.filter(t => t !== filterTag && (!tagQuery || t.toLowerCase().includes(tagQuery.toLowerCase())))

  const load = () => api.personas().then(setPersonas).catch(() => {})

  useEffect(() => { load() }, [])

  const handleCreate = async (data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => {
    await api.personaCreate(data)
    setCreating(false)
    load()
  }

  const handleUpdate = async (id: number, data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => {
    await api.personaUpdate(id, data)
  }

  const handleDelete = async (id: number) => {
    setDeleting(id)
    try {
      await api.personaDelete(id)
      setPersonas(ps => ps.filter(p => p.id !== id))
    } finally {
      setDeleting(null)
    }
  }

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-4xl mx-auto">
        <div className="flex items-center justify-between mb-6">
          {!creating && (
            <button
              type="button"
              onClick={() => setCreating(true)}
              className="flex items-center gap-1.5 px-3 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80 transition-opacity"
            >
              <Plus className="w-3.5 h-3.5" /> New
            </button>
          )}
          {allTags.length > 0 && (
            <div className="relative">
              <div className="flex items-center gap-1.5 px-3 py-1.5 rounded-capsule border border-border-white bg-sidebar-white min-w-[180px]">
                <Search className="w-3.5 h-3.5 text-normal-black shrink-0" />
                {filterTag && (
                  <span className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-icon-hover-white text-xs text-hover-black font-medium">
                    {filterTag}
                    <button type="button" onClick={() => { setFilterTag(null); setTagQuery('') }} className="text-normal-black hover:text-hover-black">
                      <X size={10} strokeWidth={2.5} />
                    </button>
                  </span>
                )}
                <input
                  value={tagQuery}
                  onChange={e => setTagQuery(e.target.value)}
                  onFocus={() => setTagFocused(true)}
                  onBlur={() => setTimeout(() => setTagFocused(false), 150)}
                  placeholder={filterTag ? '' : 'Filter by tag…'}
                  className="flex-1 text-sm bg-transparent text-hover-black outline-none placeholder:text-normal-black min-w-[60px]"
                />
              </div>
              {tagFocused && (tagQuery || tagOptions.length > 0) && (
                <div className="absolute z-10 mt-1 w-full rounded-xl border border-border-white bg-white shadow-sm overflow-hidden">
                  <div className="max-h-48 overflow-y-auto">
                    {tagOptions.length === 0 ? (
                      <p className="px-3 py-3 text-xs text-normal-black">No tags found.</p>
                    ) : (
                      tagOptions.map(tag => (
                        <button
                          key={tag}
                          type="button"
                          onMouseDown={() => { setFilterTag(tag); setTagQuery('') }}
                          className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-sidebar-white transition-colors"
                        >
                          <span className="text-sm text-hover-black">{tag}</span>
                        </button>
                      ))
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        {creating && (
          <div className="border border-border-white rounded-xl p-4 mb-4">
            <PersonaForm allTags={allTags} onSave={handleCreate} onCancel={() => setCreating(false)} />
          </div>
        )}

        {filtered.length === 0 && !creating ? (
          <p className="text-sm text-normal-black">{filterTag ? `No personas with tag "${filterTag}".` : 'No personas yet. Create one and tag them with @PersonaName to get started.'}</p>
        ) : (
          <div className="grid grid-cols-2 gap-4">
            {filtered.map(p => (
              <PersonaCard
                key={p.id}
                persona={p}
                allTags={allTags}
                deleting={deleting === p.id}
                onSave={(data) => handleUpdate(p.id, data)}
                onDelete={() => handleDelete(p.id)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
