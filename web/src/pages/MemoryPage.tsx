import { useEffect, useState } from 'react'
import { Trash2, Plus, Check, X } from 'lucide-react'
import { api, type MemoryEntry } from '@/lib/api'

function formatRelative(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  return `${Math.floor(diff / 86_400_000)}d ago`
}

type EditState = { key: string; value: string }

export function MemoryPage() {
  const [entries, setEntries] = useState<MemoryEntry[]>([])
  const [editing, setEditing] = useState<EditState | null>(null)
  const [adding, setAdding] = useState(false)
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api.memory().then(setEntries).catch(e => setError(e.message))
  }, [])

  async function saveEdit() {
    if (!editing) return
    try {
      await api.memorySet(editing.key, editing.value)
      setEntries(prev => prev.map(e => e.key === editing.key ? { ...e, value: editing.value, updated_at: new Date().toISOString() } : e))
      setEditing(null)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to save')
    }
  }

  async function deleteEntry(key: string) {
    try {
      await api.memoryDelete(key)
      setEntries(prev => prev.filter(e => e.key !== key))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to delete')
    }
  }

  async function addEntry() {
    if (!newKey.trim()) return
    try {
      await api.memorySet(newKey.trim(), newValue)
      const entry: MemoryEntry = { id: Date.now(), tenant_id: 'default', key: newKey.trim(), value: newValue, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
      setEntries(prev => [...prev.filter(e => e.key !== entry.key), entry].sort((a, b) => a.key.localeCompare(b.key)))
      setNewKey('')
      setNewValue('')
      setAdding(false)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to add')
    }
  }

  return (
    <div className="p-8 max-w-3xl">
      <div className="flex items-center justify-between mb-6">
<button
          onClick={() => { setAdding(true); setEditing(null) }}
          className="flex items-center gap-1.5 text-sm text-normal-black hover:text-hover-black cursor-pointer"
        >
          <Plus size={15} /> New
        </button>
      </div>

      {error && (
        <div className="mb-4 text-sm text-terminal-red">{error}</div>
      )}

      {adding && (
        <div className="mb-4 border border-icon-white rounded-xl p-4 space-y-3">
          <input
            autoFocus
            placeholder="Key"
            value={newKey}
            onChange={e => setNewKey(e.target.value)}
            onKeyDown={e => e.key === 'Escape' && setAdding(false)}
            className="w-full bg-transparent text-sm text-hover-black outline-none placeholder:text-normal-black font-mono"
          />
          <textarea
            placeholder="Value"
            value={newValue}
            onChange={e => setNewValue(e.target.value)}
            rows={3}
            className="w-full bg-transparent text-sm text-hover-black outline-none placeholder:text-normal-black resize-none font-mono"
          />
          <div className="flex gap-2">
            <button
              onClick={addEntry}
              className="flex items-center gap-1 text-xs text-terminal-green cursor-pointer"
            >
              <Check size={13} /> Save
            </button>
            <button
              onClick={() => { setAdding(false); setNewKey(''); setNewValue('') }}
              className="flex items-center gap-1 text-xs text-normal-black hover:text-hover-black cursor-pointer"
            >
              <X size={13} /> Cancel
            </button>
          </div>
        </div>
      )}

      {entries.length === 0 && !adding ? (
        <p className="text-sm text-normal-black">Nothing stored yet. The agent saves key-value pairs here during conversations.</p>
      ) : (
        <div className="space-y-px">
          {entries.map(entry => (
            <div key={entry.key} className="group rounded-xl px-4 py-3 hover:bg-sidebar-hover-white transition-colors">
              {editing?.key === entry.key ? (
                <div className="space-y-2">
                  <div className="text-xs font-mono text-brand-primary">{entry.key}</div>
                  <textarea
                    autoFocus
                    value={editing.value}
                    onChange={e => setEditing({ ...editing, value: e.target.value })}
                    rows={4}
                    className="w-full bg-transparent text-sm text-hover-black outline-none resize-none font-mono"
                  />
                  <div className="flex gap-2">
                    <button onClick={saveEdit} className="flex items-center gap-1 text-xs text-terminal-green cursor-pointer">
                      <Check size={13} /> Save
                    </button>
                    <button onClick={() => setEditing(null)} className="flex items-center gap-1 text-xs text-normal-black hover:text-hover-black cursor-pointer">
                      <X size={13} /> Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <div className="flex items-start gap-4">
                  <div className="flex-1 min-w-0 cursor-pointer" onClick={() => setEditing({ key: entry.key, value: entry.value })}>
                    <div className="text-xs font-mono text-brand-primary mb-0.5">{entry.key}</div>
                    <div className="text-sm text-hover-black font-mono whitespace-pre-wrap break-all line-clamp-3">{entry.value || <span className="text-normal-black italic">empty</span>}</div>
                  </div>
                  <div className="flex items-center gap-3 shrink-0 pt-0.5">
                    <span className="text-xs text-normal-black opacity-0 group-hover:opacity-100 transition-opacity">{formatRelative(entry.updated_at)}</span>
                    <button
                      onClick={() => deleteEntry(entry.key)}
                      className="text-normal-black hover:text-terminal-red cursor-pointer opacity-0 group-hover:opacity-100 transition-opacity"
                    >
                      <Trash2 size={13} />
                    </button>
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
