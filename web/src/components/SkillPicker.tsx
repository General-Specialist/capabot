import { useState } from 'react'
import { X } from 'lucide-react'
import type { Skill } from '@/lib/api'

interface SkillPickerProps {
  skills: Skill[]
  value: string[]
  onChange: (names: string[]) => void
}

export default function SkillPicker({ skills, value, onChange }: SkillPickerProps) {
  const [query, setQuery] = useState('')
  const [focused, setFocused] = useState(false)

  const selected = new Set(value)

  const filtered = skills.filter(s =>
    !selected.has(s.name) && (
      !query ||
      s.name.toLowerCase().includes(query.toLowerCase()) ||
      s.description?.toLowerCase().includes(query.toLowerCase())
    )
  )

  const add = (name: string) => {
    onChange([...value, name])
    setQuery('')
  }

  const remove = (name: string) => onChange(value.filter(n => n !== name))

  return (
    <div className="relative">
      <div className="flex items-center gap-1.5 flex-wrap px-3 py-2 rounded-xl border border-border-white bg-sidebar-white min-h-[38px]">
        {value.map(name => (
          <span key={name} className="flex items-center gap-1 px-2 py-0.5 rounded-full bg-icon-hover-white text-xs text-hover-black font-medium">
            {name}
            <button type="button" onClick={() => remove(name)} className="text-normal-black hover:text-hover-black">
              <X size={10} strokeWidth={2.5} />
            </button>
          </span>
        ))}
        <input
          value={query}
          onChange={e => setQuery(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setTimeout(() => setFocused(false), 150)}
          placeholder={value.length === 0 ? 'Add a skill…' : ''}
          className="flex-1 text-sm bg-transparent text-hover-black outline-none placeholder:text-normal-black min-w-[80px]"
        />
      </div>

      {focused && (query || filtered.length > 0) && (
        <div className="absolute z-10 mt-1 w-full rounded-xl border border-border-white bg-white shadow-sm overflow-hidden">
          <div className="max-h-48 overflow-y-auto">
            {filtered.length === 0 ? (
              <p className="px-3 py-3 text-xs text-normal-black">No skills found.</p>
            ) : (
              filtered.map(s => (
                <button
                  key={s.name}
                  type="button"
                  onMouseDown={() => add(s.name)}
                  className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-sidebar-white transition-colors"
                >
                  <span className="text-sm text-hover-black">{s.name}</span>
                  {s.description && (
                    <span className="text-xs text-normal-black truncate">{s.description}</span>
                  )}
                  {s.tier >= 2 && (
                    <span className="ml-auto text-[10px] text-normal-black shrink-0">executable</span>
                  )}
                </button>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}
