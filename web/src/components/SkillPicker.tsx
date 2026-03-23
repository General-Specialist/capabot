import { useState } from 'react'
import { Search, Check } from 'lucide-react'
import type { Skill } from '@/lib/api'

interface SkillPickerProps {
  skills: Skill[]
  value: string
  onChange: (name: string) => void
}

export default function SkillPicker({ skills, value, onChange }: SkillPickerProps) {
  const [query, setQuery] = useState('')

  const filtered = query
    ? skills.filter(s =>
        s.name.toLowerCase().includes(query.toLowerCase()) ||
        s.description?.toLowerCase().includes(query.toLowerCase())
      )
    : skills

  const toggle = (name: string) => onChange(value === name ? '' : name)

  return (
    <div className="rounded-lg border border-border-white overflow-hidden">
      {/* Search */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border-white bg-sidebar-white">
        <Search size={12} className="text-normal-black shrink-0" />
        <input
          value={query}
          onChange={e => setQuery(e.target.value)}
          placeholder="Search skills…"
          className="flex-1 text-sm bg-transparent text-hover-black outline-none placeholder:text-normal-black"
        />
      </div>

      {/* List */}
      <div className="max-h-48 overflow-y-auto">
        {filtered.length === 0 ? (
          <p className="px-3 py-3 text-xs text-normal-black">No skills found.</p>
        ) : (
          filtered.map(s => {
            const checked = value === s.name
            return (
              <button
                key={s.name}
                type="button"
                onClick={() => toggle(s.name)}
                className={`w-full flex items-center gap-3 px-3 py-2.5 text-left transition-colors hover:bg-sidebar-white ${checked ? 'bg-sidebar-white' : ''}`}
              >
                {/* Custom checkbox */}
                <span className={`shrink-0 w-4 h-4 rounded flex items-center justify-center border transition-colors ${
                  checked
                    ? 'bg-brand-primary border-brand-primary'
                    : 'border-border-white bg-transparent'
                }`}>
                  {checked && <Check size={10} strokeWidth={3} className="text-white" />}
                </span>

                <span className="flex-1 min-w-0">
                  <span className="text-sm text-hover-black">{s.name}</span>
                  {s.description && (
                    <span className="ml-2 text-xs text-normal-black truncate">{s.description}</span>
                  )}
                </span>

                {s.tier >= 2 && (
                  <span className="text-[10px] text-brand-primary shrink-0">executable</span>
                )}
              </button>
            )
          })
        )}
      </div>
    </div>
  )
}
