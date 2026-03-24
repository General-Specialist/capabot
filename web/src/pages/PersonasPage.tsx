import { useEffect, useState } from 'react'
import { Plus, Pencil, Trash2, Check, X } from 'lucide-react'
import { api, type Persona } from '@/lib/api'

function PersonaForm({
  initial,
  onSave,
  onCancel,
}: {
  initial?: { name: string; prompt: string }
  onSave: (name: string, prompt: string) => Promise<void>
  onCancel: () => void
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [prompt, setPrompt] = useState(initial?.prompt ?? '')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const handleSave = async () => {
    if (!name.trim()) { setError('Name is required'); return }
    setSaving(true)
    try {
      await onSave(name.trim(), prompt)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save')
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <input
        className="border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none font-mono"
        placeholder="Name (e.g. ProductManager)"
        value={name}
        onChange={e => setName(e.target.value)}
        disabled={!!initial}
      />
      <textarea
        className="border border-border-white rounded-xl px-3 py-2 text-sm bg-sidebar-white text-hover-black outline-none font-mono resize-none"
        placeholder="System prompt…"
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
  )
}

export function PersonasPage() {
  const [personas, setPersonas] = useState<Persona[]>([])
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<number | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const load = () => api.personas().then(setPersonas).catch(() => {})

  useEffect(() => { load() }, [])

  const handleCreate = async (name: string, prompt: string) => {
    await api.personaCreate(name, prompt)
    setCreating(false)
    load()
  }

  const handleUpdate = async (id: number, name: string, prompt: string) => {
    await api.personaUpdate(id, name, prompt)
    setEditing(null)
    load()
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
      <div className="max-w-3xl mx-auto">
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
        </div>

        {creating && (
          <div className="border border-border-white rounded-xl p-4 mb-4">
            <PersonaForm onSave={handleCreate} onCancel={() => setCreating(false)} />
          </div>
        )}

        {personas.length === 0 && !creating ? (
          <p className="text-sm text-normal-black">No personas yet. Create one and tag them with @PersonaName to get started.</p>
        ) : (
          <div className="flex flex-col gap-3">
            {personas.map(p => (
              <div key={p.id} className="border border-border-white rounded-xl p-4">
                {editing === p.id ? (
                  <PersonaForm
                    initial={{ name: p.name, prompt: p.prompt }}
                    onSave={(name, prompt) => handleUpdate(p.id, name, prompt)}
                    onCancel={() => setEditing(null)}
                  />
                ) : (
                  <>
                    <div className="flex items-center justify-between mb-2">
                      <span className="font-mono text-sm font-medium text-hover-black">{p.name}</span>
                      <div className="flex gap-1">
                        <button
                          type="button"
                          onClick={() => setEditing(p.id)}
                          className="p-1.5 rounded-lg hover:bg-sidebar-white text-normal-black hover:text-hover-black transition-colors"
                        >
                          <Pencil className="w-3.5 h-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => handleDelete(p.id)}
                          disabled={deleting === p.id}
                          className="p-1.5 rounded-lg hover:bg-sidebar-white text-normal-black hover:text-red disabled:opacity-50 transition-colors"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </div>
                    <p className="text-sm text-normal-black whitespace-pre-wrap line-clamp-3 font-mono">
                      {p.prompt || <span className="italic">No prompt set</span>}
                    </p>
                  </>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
