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
        className="border border-gray-200 rounded-lg px-3 py-2 text-sm outline-none focus:border-gray-400 font-mono"
        placeholder="Name (e.g. ProductManager)"
        value={name}
        onChange={e => setName(e.target.value)}
        disabled={!!initial}
      />
      <textarea
        className="border border-gray-200 rounded-lg px-3 py-2 text-sm outline-none focus:border-gray-400 font-mono resize-none"
        placeholder="System prompt…"
        rows={5}
        value={prompt}
        onChange={e => setPrompt(e.target.value)}
      />
      {error && <p className="text-xs text-red-500">{error}</p>}
      <div className="flex gap-2 justify-end">
        <button
          type="button"
          onClick={onCancel}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm text-gray-500 hover:bg-gray-100 transition-colors"
        >
          <X className="w-3.5 h-3.5" /> Cancel
        </button>
        <button
          type="button"
          onClick={handleSave}
          disabled={saving}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm bg-black text-white hover:bg-gray-800 disabled:opacity-50 transition-colors"
        >
          <Check className="w-3.5 h-3.5" /> {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

export function PersonasPage() {
  const [personas, setPersonas] = useState<Persona[]>([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<number | null>(null)
  const [deleting, setDeleting] = useState<number | null>(null)

  const load = () =>
    api.personas().then(setPersonas).catch(() => {}).finally(() => setLoading(false))

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
    <div className="max-w-2xl mx-auto px-6 py-10">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-lg font-semibold text-gray-900">Personas</h1>
          <p className="text-sm text-gray-500 mt-0.5">
            Named system prompts. Tag with <code className="bg-gray-100 px-1 rounded text-xs">@Name</code> in any message to use.
          </p>
        </div>
        {!creating && (
          <button
            type="button"
            onClick={() => setCreating(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm bg-black text-white hover:bg-gray-800 transition-colors"
          >
            <Plus className="w-3.5 h-3.5" /> New
          </button>
        )}
      </div>

      {creating && (
        <div className="border border-gray-200 rounded-xl p-4 mb-4">
          <PersonaForm onSave={handleCreate} onCancel={() => setCreating(false)} />
        </div>
      )}

      {loading ? (
        <p className="text-sm text-gray-400">Loading…</p>
      ) : personas.length === 0 && !creating ? (
        <p className="text-sm text-gray-400">No personas yet. Create one to get started.</p>
      ) : (
        <div className="flex flex-col gap-3">
          {personas.map(p => (
            <div key={p.id} className="border border-gray-200 rounded-xl p-4">
              {editing === p.id ? (
                <PersonaForm
                  initial={{ name: p.name, prompt: p.prompt }}
                  onSave={(name, prompt) => handleUpdate(p.id, name, prompt)}
                  onCancel={() => setEditing(null)}
                />
              ) : (
                <>
                  <div className="flex items-center justify-between mb-2">
                    <span className="font-mono text-sm font-medium text-gray-900">@{p.name}</span>
                    <div className="flex gap-1">
                      <button
                        type="button"
                        onClick={() => setEditing(p.id)}
                        className="p-1.5 rounded-lg hover:bg-gray-100 text-gray-400 hover:text-gray-700 transition-colors"
                      >
                        <Pencil className="w-3.5 h-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDelete(p.id)}
                        disabled={deleting === p.id}
                        className="p-1.5 rounded-lg hover:bg-red-50 text-gray-400 hover:text-red-500 disabled:opacity-50 transition-colors"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </div>
                  <p className="text-sm text-gray-500 whitespace-pre-wrap line-clamp-3 font-mono">
                    {p.prompt || <span className="italic text-gray-300">No prompt set</span>}
                  </p>
                </>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
