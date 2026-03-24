import { useEffect, useRef, useState } from 'react'
import { api, type ProviderKeys, type ProviderInfo } from '@/lib/api'

function useDarkMode() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('darkMode')
    if (stored !== null) return stored === 'true'
    document.documentElement.classList.add('dark')
    return true
  })
  const toggle = () => {
    const next = !dark
    setDark(next)
    document.documentElement.classList.toggle('dark', next)
    localStorage.setItem('darkMode', String(next))
  }
  return { dark, toggle }
}

const PROVIDERS: { key: keyof ProviderKeys; label: string; placeholder: string }[] = [
  { key: 'anthropic',  label: 'Anthropic',   placeholder: 'sk-ant-...' },
  { key: 'openai',     label: 'OpenAI',       placeholder: 'sk-...' },
  { key: 'gemini',     label: 'Gemini',       placeholder: 'AIza...' },
  { key: 'openrouter', label: 'OpenRouter',   placeholder: 'sk-or-...' },
]

const EMPTY: ProviderKeys = { anthropic: '', openai: '', gemini: '', openrouter: '' }

export function SettingsPage() {
  const [keys, setKeys] = useState<ProviderKeys>(EMPTY)
  const [error, setError] = useState<string | null>(null)
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [defaultModel, setDefaultModel] = useState('')
  const { dark, toggle } = useDarkMode()
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const initialLoad = useRef(true)
  const modelInitial = useRef(true)

  useEffect(() => {
    api.configKeys().then(setKeys).catch(() => {})
    api.providers().then(setProviders).catch(() => {})
    api.defaultModelGet().then(r => setDefaultModel(r.default_model)).catch(() => {})
  }, [])

  useEffect(() => {
    if (initialLoad.current) { initialLoad.current = false; return }
    if (saveTimer.current) clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(() => {
      api.configKeysSave(keys)
        .catch(err => setError(err instanceof Error ? err.message : 'Save failed'))
    }, 600)
    return () => { if (saveTimer.current) clearTimeout(saveTimer.current) }
  }, [keys])

  useEffect(() => {
    if (modelInitial.current) { modelInitial.current = false; return }
    api.defaultModelSet(defaultModel).catch(() => {})
  }, [defaultModel])

  const allModels = providers.flatMap(p => p.models.map(m => ({ ...m, provider: p.name })))

  return (
    <div className="w-full min-h-screen bg-white px-6 py-6">
      <div className="max-w-3xl mx-auto space-y-5">

        <div className="flex items-center justify-between">
          <span className="text-sm text-normal-black">Dark mode</span>
          <button
            onClick={toggle}
            className={`relative w-10 h-5 rounded-full transition-colors ${dark ? 'bg-brand-primary' : 'bg-sidebar-hover-white'}`}
          >
            <span className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${dark ? 'translate-x-5' : 'translate-x-0'}`} />
          </button>
        </div>

        {allModels.length > 0 && (
          <div>
            <label className="block text-xs text-normal-black mb-1">Default model</label>
            <select
              value={defaultModel}
              onChange={e => setDefaultModel(e.target.value)}
              className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
            >
              <option value="">Auto (provider default)</option>
              {allModels.map(m => (
                <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
              ))}
            </select>
            <p className="text-xs text-normal-black mt-1 opacity-60">
              Override per-message with @model-id in chat
            </p>
          </div>
        )}

        <div className="space-y-3">
          {PROVIDERS.map(({ key, label, placeholder }) => (
            <div key={key}>
              <label className="block text-xs text-normal-black mb-1">{label}</label>
              <input
                type="password"
                value={keys[key]}
                onChange={e => setKeys(k => ({ ...k, [key]: e.target.value }))}
                placeholder={placeholder}
                className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black font-mono outline-none"
              />
            </div>
          ))}
        </div>

        {error && <p className="text-xs text-red">{error}</p>}
      </div>
    </div>
  )
}
