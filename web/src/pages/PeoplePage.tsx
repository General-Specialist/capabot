import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import { Plus, Trash2, Check, Save, X, Camera, Search, ScrollText } from 'lucide-react'
import { api, type Person } from '@/lib/api'
import TagPicker from '@/components/TagPicker'

function SystemPromptModal({ onClose }: { onClose: () => void }) {
  const [value, setValue] = useState('')
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved'>('idle')
  const timerRef = useRef<ReturnType<typeof setTimeout>>()

  useEffect(() => {
    api.systemPromptGet().then(d => setValue(d.system_prompt)).catch(() => {})
    return () => clearTimeout(timerRef.current)
  }, [])

  const handleChange = (v: string) => {
    setValue(v)
    clearTimeout(timerRef.current)
    timerRef.current = setTimeout(async () => {
      setSaveStatus('saving')
      try {
        await api.systemPromptSet(v)
        setSaveStatus('saved')
        setTimeout(() => setSaveStatus(s => s === 'saved' ? 'idle' : s), 1500)
      } catch {
        setSaveStatus('idle')
      }
    }, 800)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-overlay-light" onClick={onClose}>
      <div className="bg-white rounded-2xl p-5 w-[480px] flex flex-col gap-4 shadow-lg" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between">
          <p className="text-sm font-medium text-hover-black">Universal Prompt</p>
          <div className="flex items-center gap-2">
            {saveStatus === 'saving' && <span className="text-[10px] text-normal-black animate-pulse">Saving…</span>}
            {saveStatus === 'saved' && <span className="text-[10px] text-terminal-green">Saved</span>}
            <button type="button" onClick={onClose} className="p-1 rounded-lg hover:bg-sidebar-white text-normal-black transition-colors">
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
        <p className="text-xs text-normal-black">Added to every person's prompt.</p>
        <textarea
          className="border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none font-mono resize-none"
          placeholder="e.g. Always respond in formal English."
          rows={6}
          value={value}
          onChange={e => handleChange(e.target.value)}
        />
      </div>
    </div>
  )
}

function PersonForm({
  initial,
  allTags,
  onSave,
  onCancel,
}: {
  initial?: Person
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
  const [cropOpen, setCropOpen] = useState(false)
  const [error, setError] = useState('')

  const handleCropSave = async (cropped: File) => {
    setCropOpen(false)
    setUploading(true)
    try {
      const url = await api.avatarUpload(cropped)
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
    <div className="flex gap-4">
      {cropOpen && <AvatarCropModal initialSrc={avatarUrl || undefined} onSave={handleCropSave} onCancel={() => setCropOpen(false)} />}
      <div className="shrink-0">
        <button
          type="button"
          onClick={() => setCropOpen(true)}
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
        placeholder={"Personality prompt.\n\nEx: Enthusiastic, creative product manager"}
        rows={5}
        value={prompt}
        onChange={e => setPrompt(e.target.value)}
      />
      {error && <p className="text-xs text-red">{error}</p>}
      <div className="flex gap-2 justify-end">
        <button
          type="button"
          onClick={onCancel}
          className="p-1 rounded-lg text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors"
        >
          <X size={14} />
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

function AvatarCropModal({ initialSrc, onSave, onCancel }: {
  initialSrc?: string
  onSave: (cropped: File) => void
  onCancel: () => void
}) {
  const [imgSrc, setImgSrc] = useState(initialSrc ?? '')
  const [blobUrl, setBlobUrl] = useState<string | null>(null)
  const uploadRef = useRef<HTMLInputElement>(null)
  const [scale, setScale] = useState(1)
  const [pos, setPos] = useState({ x: 0, y: 0 })
  const [naturalSize, setNaturalSize] = useState({ w: 0, h: 0 })
  const [baseSize, setBaseSize] = useState({ w: 0, h: 0 })
  const dragRef = useRef<{ startX: number; startY: number; startPos: { x: number; y: number } } | null>(null)
  const posRef = useRef(pos)
  posRef.current = pos
  const scaleRef = useRef(scale)
  scaleRef.current = scale

  const BOX = 360
  const CIRCLE_R = 120

  useEffect(() => {
    return () => { if (blobUrl) URL.revokeObjectURL(blobUrl) }
  }, [blobUrl])

  const handleNewFile = (file: File) => {
    if (blobUrl) URL.revokeObjectURL(blobUrl)
    const url = URL.createObjectURL(file)
    setBlobUrl(url)
    setImgSrc(url)
    // Reset zoom/position — will be set properly in handleImageLoad
    setScale(1)
    setBaseSize({ w: 0, h: 0 })
  }

  const handleImageLoad = (e: React.SyntheticEvent<HTMLImageElement>) => {
    const img = e.currentTarget
    const nw = img.naturalWidth
    const nh = img.naturalHeight
    setNaturalSize({ w: nw, h: nh })
    // Fit entire image inside the box
    const fitScale = Math.min(BOX / nw, BOX / nh)
    const w = nw * fitScale
    const h = nh * fitScale
    setBaseSize({ w, h })
    // Start with image centered and covering the circle
    const minS = Math.max((CIRCLE_R * 2) / w, (CIRCLE_R * 2) / h)
    const initScale = Math.max(1, minS)
    setScale(initScale)
    setPos({ x: (BOX - w * initScale) / 2, y: (BOX - h * initScale) / 2 })
  }

  // Clamp so image always covers the circle
  const clamp = (x: number, y: number, s: number) => {
    const sw = baseSize.w * s
    const sh = baseSize.h * s
    const cLeft = BOX / 2 - CIRCLE_R
    const cTop = BOX / 2 - CIRCLE_R
    const cRight = BOX / 2 + CIRCLE_R
    const cBottom = BOX / 2 + CIRCLE_R
    return {
      x: Math.min(cLeft, Math.max(cRight - sw, x)),
      y: Math.min(cTop, Math.max(cBottom - sh, y)),
    }
  }

  const handleMouseDown = (e: React.MouseEvent) => {
    e.preventDefault()
    const startPos = { ...posRef.current }
    dragRef.current = { startX: e.clientX, startY: e.clientY, startPos }
    const handleMove = (ev: MouseEvent) => {
      if (!dragRef.current) return
      setPos(clamp(
        dragRef.current.startPos.x + (ev.clientX - dragRef.current.startX),
        dragRef.current.startPos.y + (ev.clientY - dragRef.current.startY),
        scaleRef.current,
      ))
    }
    const handleUp = () => {
      document.removeEventListener('mousemove', handleMove)
      document.removeEventListener('mouseup', handleUp)
      dragRef.current = null
    }
    document.addEventListener('mousemove', handleMove)
    document.addEventListener('mouseup', handleUp)
  }

  const minScale = baseSize.w > 0 ? Math.max((CIRCLE_R * 2) / baseSize.w, (CIRCLE_R * 2) / baseSize.h) : 1

  const handleWheel = (e: React.WheelEvent) => {
    e.stopPropagation()
    const prev = scaleRef.current
    const next = Math.max(minScale, Math.min(5, prev - e.deltaY * 0.003))
    const cx = BOX / 2
    const cy = BOX / 2
    setScale(next)
    setPos(clamp(
      cx - (cx - posRef.current.x) * (next / prev),
      cy - (cy - posRef.current.y) * (next / prev),
      next,
    ))
  }

  const handleSave = () => {
    const canvas = document.createElement('canvas')
    canvas.width = 512
    canvas.height = 512
    const ctx = canvas.getContext('2d')!
    const img = new Image()
    img.crossOrigin = 'anonymous'
    img.onload = () => {
      // The circle center in box-space is (BOX/2, BOX/2), radius CIRCLE_R
      // Map to image-space
      const displayW = baseSize.w * scale
      const displayH = baseSize.h * scale
      const imgX = pos.x
      const imgY = pos.y
      // Circle top-left in box-space
      const circleX = BOX / 2 - CIRCLE_R
      const circleY = BOX / 2 - CIRCLE_R
      const circleDiam = CIRCLE_R * 2
      // Source rect in image pixels
      const sx = ((circleX - imgX) / displayW) * img.naturalWidth
      const sy = ((circleY - imgY) / displayH) * img.naturalHeight
      const sw = (circleDiam / displayW) * img.naturalWidth
      const sh = (circleDiam / displayH) * img.naturalHeight
      ctx.beginPath()
      ctx.arc(256, 256, 256, 0, Math.PI * 2)
      ctx.clip()
      ctx.drawImage(img, sx, sy, sw, sh, 0, 0, 512, 512)
      canvas.toBlob(blob => {
        if (blob) onSave(new File([blob], 'avatar.png', { type: 'image/png' }))
      }, 'image/png')
    }
    img.src = imgSrc
  }

  // Circle overlay mask: dark outside, transparent inside
  const maskStyle = {
    background: `radial-gradient(circle ${CIRCLE_R}px at center, transparent ${CIRCLE_R - 1}px, rgba(0,0,0,0.5) ${CIRCLE_R}px)`,
  }

  // suppress unused warning
  void naturalSize

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-overlay" onClick={onCancel}>
      <div className="bg-white rounded-2xl p-6 flex flex-col items-center gap-4" onClick={e => e.stopPropagation()}>
        <p className="text-sm font-medium text-hover-black">Adjust avatar</p>
        <div
          className="relative overflow-hidden cursor-grab active:cursor-grabbing rounded-xl"
          style={{ width: BOX, height: BOX, background: 'var(--color-icon-hover-white)' }}
          onMouseDown={handleMouseDown}
          onWheel={handleWheel}
        >
          {!imgSrc && (
            <div className="absolute inset-0 flex items-center justify-center">
              <button type="button" onClick={() => uploadRef.current?.click()} className="flex flex-col items-center gap-2 text-normal-black hover:text-hover-black transition-colors">
                <Camera className="w-8 h-8" />
                <span className="text-sm">Upload an image</span>
              </button>
            </div>
          )}
          {imgSrc && (
            <img
              src={imgSrc}
              alt=""
              draggable={false}
              onLoad={handleImageLoad}
              style={{
                position: 'absolute',
                left: pos.x,
                top: pos.y,
                width: baseSize.w * scale,
                height: baseSize.h * scale,
                maxWidth: 'none',
              }}
            />
          )}
          {/* Dark overlay with circle cutout */}
          <div className="absolute inset-0 pointer-events-none" style={maskStyle} />
          {/* Circle border */}
          <div
            className="absolute pointer-events-none border-2 border-white/80 rounded-full"
            style={{
              width: CIRCLE_R * 2,
              height: CIRCLE_R * 2,
              left: BOX / 2 - CIRCLE_R,
              top: BOX / 2 - CIRCLE_R,
            }}
          />
        </div>
        <input
          type="range"
          min={minScale}
          max={5}
          step={0.01}
          value={scale}
          onChange={e => {
            const prev = scaleRef.current
            const next = Math.max(minScale, parseFloat(e.target.value))
            const cx = BOX / 2
            const cy = BOX / 2
            setScale(next)
            setPos(clamp(
              cx - (cx - posRef.current.x) * (next / prev),
              cy - (cy - posRef.current.y) * (next / prev),
              next,
            ))
          }}
          className="w-64 accent-[var(--color-brand-primary)]"
        />
        <div className="flex gap-2">
          <input ref={uploadRef} type="file" accept="image/*" className="hidden" onChange={e => { const f = e.target.files?.[0]; if (f) handleNewFile(f); e.target.value = '' }} />
          <button type="button" onClick={() => uploadRef.current?.click()} className="px-4 py-1.5 rounded-capsule text-sm text-normal-black hover:bg-sidebar-white border border-border-white transition-colors">
            Upload new
          </button>
          <div className="flex-1" />
          <button type="button" onClick={onCancel} className="p-1 rounded-lg text-normal-black hover:text-hover-black hover:bg-sidebar-white transition-colors">
            <X size={14} />
          </button>
          <button type="button" onClick={handleSave} disabled={!imgSrc} className="flex items-center gap-1.5 px-4 py-1.5 rounded-capsule text-sm bg-[var(--color-brand-primary)] text-white hover:opacity-80 disabled:opacity-50 transition-opacity">
            <Save size={13} />Save
          </button>
        </div>
      </div>
    </div>
  )
}

function PersonCard({ person: p, allTags, deleting, onSave, onDelete }: {
  person: Person
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
  const [cropOpen, setCropOpen] = useState(false)
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved'>('idle')
  const timerRef = useRef<ReturnType<typeof setTimeout>>()

  const handleCropSave = async (cropped: File) => {
    setCropOpen(false)
    setUploading(true)
    try {
      const url = await api.avatarUpload(cropped)
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

  const buildData = useCallback(() => ({
    name: p.name, prompt, username: username.trim(), avatar_url: avatarUrl, tags,
  }), [p.name, prompt, username, avatarUrl, tags])

  const updateField = useCallback(<K extends 'username' | 'prompt' | 'tags'>(field: K, value: K extends 'tags' ? string[] : string) => {
    const next = buildData()
    if (field === 'username') { setUsername(value as string); next.username = (value as string).trim() }
    else if (field === 'prompt') { setPrompt(value as string); next.prompt = value as string }
    else if (field === 'tags') { setTags(value as string[]); next.tags = value as string[] }
    doSave(next)
  }, [buildData, doSave])

  useEffect(() => () => clearTimeout(timerRef.current), [])

  return (
    <div className="border border-border-white rounded-xl p-5">
      {cropOpen && <AvatarCropModal initialSrc={avatarUrl || undefined} onSave={handleCropSave} onCancel={() => setCropOpen(false)} />}
      <div className="flex items-start gap-4 mb-2">
        <button type="button" onClick={() => setCropOpen(true)} disabled={uploading} className="shrink-0 hover:opacity-80 transition-opacity disabled:opacity-50">
          <div className="w-14 h-14 rounded-full bg-sidebar-white border border-border-white overflow-hidden flex items-center justify-center">
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
              {saveStatus === 'saved' && <span className="text-[10px] text-terminal-green">Saved</span>}
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
        placeholder={"Personality prompt.\n\nEx: Enthusiastic, creative product manager"}
        rows={3}
        className="w-full text-xs text-normal-black font-mono bg-transparent outline-none resize-none border border-transparent focus:border-border-white rounded-lg px-2 py-1.5 transition-colors"
      />
    </div>
  )
}

export function PeoplePage() {
  const [people, setPeople] = useState<Person[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<number | null>(null)
  const [filterTag, setFilterTag] = useState<string | null>(null)
  const [tagQuery, setTagQuery] = useState('')
  const [tagFocused, setTagFocused] = useState(false)
  const [showSystemPrompt, setShowSystemPrompt] = useState(false)

  const allTags = useMemo(() => [...new Set(people.flatMap(p => p.tags || []))].sort(), [people])
  const filtered = filterTag ? people.filter(p => p.tags?.includes(filterTag)) : people
  const tagOptions = allTags.filter(t => t !== filterTag && (!tagQuery || t.toLowerCase().includes(tagQuery.toLowerCase())))

  const load = () => api.people().then(setPeople).catch(() => {})

  useEffect(() => { load().finally(() => setLoading(false)) }, [])

  const handleCreate = async (data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => {
    await api.personCreate(data)
    setCreating(false)
    load()
  }

  const handleUpdate = async (id: number, data: { name: string; prompt: string; username: string; avatar_url: string; tags: string[] }) => {
    await api.personUpdate(id, data)
  }

  const handleDelete = async (id: number) => {
    setDeleting(id)
    try {
      await api.personDelete(id)
      setPeople(ps => ps.filter(p => p.id !== id))
    } finally {
      setDeleting(null)
    }
  }

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-4xl mx-auto">
        {showSystemPrompt && <SystemPromptModal onClose={() => setShowSystemPrompt(false)} />}
        <div className="flex items-center justify-between mb-6">
          <AnimatePresence mode="wait">
            {!creating ? (
              <motion.button
                key="people-btn"
                type="button"
                onClick={() => setCreating(true)}
                className="flex items-center gap-1.5 px-3 py-1.5 bg-[var(--color-brand-primary)] text-white text-sm rounded-capsule hover:opacity-80"
                initial={{ opacity: 0, scale: 0.96 }}
                animate={{ opacity: 1, scale: 1 }}
                exit={{ opacity: 0, scale: 0.96 }}
                transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
              >
                <Plus className="w-3.5 h-3.5" /> New
              </motion.button>
            ) : null}
          </AnimatePresence>
          <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setShowSystemPrompt(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-capsule border border-border-white bg-sidebar-white text-sm text-normal-black hover:text-hover-black hover:bg-icon-hover-white transition-colors"
          >
            <ScrollText className="w-3.5 h-3.5" /> Universal Prompt
          </button>
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
        </div>

        <AnimatePresence>
          {creating && (
            <motion.div
              key="people-form"
              initial={{ opacity: 0, scale: 0.96, y: -4 }}
              animate={{ opacity: 1, scale: 1, y: 0 }}
              exit={{ opacity: 0, scale: 0.96, y: -4 }}
              transition={{ duration: 0.18, ease: [0.4, 0, 0.2, 1] }}
              className="border border-border-white rounded-xl p-4 mb-4"
            >
              <PersonForm allTags={allTags} onSave={handleCreate} onCancel={() => setCreating(false)} />
            </motion.div>
          )}
        </AnimatePresence>

        {!loading && filtered.length === 0 && !creating ? (
          <p className="text-sm text-normal-black">{filterTag ? `No people with tag "${filterTag}".` : 'No people yet. Create one and tag them with @PersonName to get started.'}</p>
        ) : (
          <div className="grid grid-cols-2 gap-4">
            {filtered.map(p => (
              <PersonCard
                key={p.id}
                person={p}
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
