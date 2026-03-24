import { useState, useRef } from 'react'
import { Plus, X } from 'lucide-react'

interface TagPickerProps {
  allTags: string[]
  value: string[]
  onChange: (tags: string[]) => void
}

export default function TagPicker({ allTags, value, onChange }: TagPickerProps) {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const selected = new Set(value)

  const filtered = allTags.filter(t =>
    !selected.has(t) && (!query || t.toLowerCase().includes(query.toLowerCase()))
  )

  const canCreateNew = query.trim() && !allTags.includes(query.trim().toLowerCase()) && !selected.has(query.trim().toLowerCase())

  const add = (tag: string) => {
    const t = tag.trim().toLowerCase().replace(/\s/g, '')
    if (t && !selected.has(t)) {
      onChange([...value, t])
    }
    setQuery('')
    setOpen(false)
  }

  const remove = (tag: string) => onChange(value.filter(t => t !== tag))

  return (
    <div className="relative">
      <div className="flex items-center gap-1.5 flex-wrap">
        {value.map(tag => (
          <span key={tag} className="flex items-center gap-1.5 px-3 py-1 rounded-full bg-icon-hover-white text-xs text-brand-primary font-medium">
            {tag}
            <button type="button" onClick={() => remove(tag)} className="text-normal-black hover:text-hover-black">
              <X size={10} strokeWidth={2.5} />
            </button>
          </span>
        ))}
        <button
          type="button"
          onClick={() => { setOpen(!open); setTimeout(() => inputRef.current?.focus(), 0) }}
          className="flex items-center gap-1 px-3 py-1 rounded-full bg-icon-hover-white text-xs text-normal-black hover:text-hover-black transition-colors"
        >
          <Plus size={10} strokeWidth={2.5} /> tag
        </button>
      </div>

      {open && (
        <div className="absolute z-10 mt-1 w-56 rounded-xl border border-border-white bg-white shadow-sm overflow-hidden">
          <div className="px-3 py-2 border-b border-border-white">
            <input
              ref={inputRef}
              value={query}
              onChange={e => setQuery(e.target.value)}
              onBlur={() => setTimeout(() => setOpen(false), 150)}
              onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(query) } }}
              placeholder="Search or create…"
              className="w-full text-sm bg-transparent text-hover-black outline-none placeholder:text-normal-black"
            />
          </div>
          <div className="max-h-48 overflow-y-auto">
            {canCreateNew && (
              <button
                type="button"
                onMouseDown={() => add(query)}
                className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-sidebar-white transition-colors"
              >
                <span className="text-sm text-hover-black">Create "<strong>{query.trim().toLowerCase()}</strong>"</span>
              </button>
            )}
            {filtered.map(tag => (
              <button
                key={tag}
                type="button"
                onMouseDown={() => add(tag)}
                className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-sidebar-white transition-colors"
              >
                <span className="text-sm text-hover-black">{tag}</span>
              </button>
            ))}
            {!canCreateNew && filtered.length === 0 && (
              <p className="px-3 py-2 text-xs text-normal-black">No tags available</p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
