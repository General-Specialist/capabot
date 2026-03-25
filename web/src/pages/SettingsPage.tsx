import { useEffect, useRef, useState } from 'react'
import { api, type ProviderKeys, type ProviderInfo } from '@/lib/api'

function useDarkMode() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem('darkMode')
    return stored !== null ? stored === 'true' : window.matchMedia('(prefers-color-scheme: dark)').matches
  })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('darkMode', String(dark))
  }, [dark])

  const toggle = () => setDark(d => !d)
  return { dark, toggle }
}

const PROVIDERS: { key: 'anthropic' | 'openai' | 'gemini' | 'openrouter'; label: string; placeholder: string }[] = [
  { key: 'anthropic',  label: 'Anthropic',   placeholder: 'sk-ant-...' },
  { key: 'openai',     label: 'OpenAI',       placeholder: 'sk-...' },
  { key: 'gemini',     label: 'Gemini',       placeholder: 'AIza...' },
  { key: 'openrouter', label: 'OpenRouter',   placeholder: 'sk-or-...' },
]

const EMPTY: ProviderKeys = { anthropic: '', openai: '', gemini: '', openrouter: '' }

export function SettingsPage() {
  const [keys, setKeys] = useState<ProviderKeys>(EMPTY)
  const [executeFallback, setExecuteFallback] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [providers, setProviders] = useState<ProviderInfo[]>([])
  const [defaultModel, setDefaultModel] = useState('')
  const [summarizationModel, setSummarizationModel] = useState('')
  const [modes, setModes] = useState<Record<string, ProviderKeys>>({})
  const [activeMode, setActiveMode] = useState('default')
  const [selectedMode, setSelectedMode] = useState('default')
  const { dark, toggle } = useDarkMode()
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const initialLoad = useRef(true)
  const modelInitial = useRef(true)
  const sumModelInitial = useRef(true)
  const modeTimers = useRef<Record<string, ReturnType<typeof setTimeout>>>({})
  const modesInitial = useRef(true)

  useEffect(() => {
    api.configKeys().then(setKeys).catch(() => {})
    api.executeFallbackGet().then(r => setExecuteFallback(r.enabled)).catch(() => {})
    api.providers().then(setProviders).catch(() => {})
    api.defaultModelGet().then(r => setDefaultModel(r.default_model)).catch(() => {})
    api.summarizationModelGet().then(r => setSummarizationModel(r.summarization_model)).catch(() => {})
    api.modes().then(r => {
      setModes(r.modes)
      setActiveMode(r.active)
    }).catch(() => {})
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

  useEffect(() => {
    if (sumModelInitial.current) { sumModelInitial.current = false; return }
    api.summarizationModelSet(summarizationModel).catch(() => {})
  }, [summarizationModel])

  const saveModeKeys = (name: string, modeKeys: ProviderKeys) => {
    if (modesInitial.current) return
    if (modeTimers.current[name]) clearTimeout(modeTimers.current[name])
    modeTimers.current[name] = setTimeout(() => {
      api.modeSet(name, modeKeys).catch(() => {})
    }, 600)
  }

  const updateModeKey = (modeName: string, key: 'anthropic' | 'openai' | 'gemini' | 'openrouter', value: string) => {
    setModes(prev => {
      const updated = { ...prev, [modeName]: { ...(prev[modeName] || EMPTY), [key]: value } }
      saveModeKeys(modeName, updated[modeName])
      return updated
    })
  }

  const updateModeModel = (modeName: string, model: string) => {
    setModes(prev => {
      const updated = { ...prev, [modeName]: { ...(prev[modeName] || EMPTY), model } }
      saveModeKeys(modeName, updated[modeName])
      return updated
    })
  }

  const clearModeKeys = (modeName: string) => {
    const empty = { ...EMPTY }
    setModes(prev => ({ ...prev, [modeName]: empty }))
    api.modeSet(modeName, empty).catch(() => {})
  }

  const switchMode = (mode: string) => {
    setActiveMode(mode)
    api.activeModeSet(mode).catch(() => {})
  }

  // Mark initial load complete after modes load
  useEffect(() => {
    if (Object.keys(modes).length > 0) {
      modesInitial.current = false
    }
  }, [modes])

  const allModels = providers.flatMap(p => p.models.map(m => ({ ...m, provider: p.name })))
  const builtIn = ['default', 'chat', 'execute']
  const custom = Object.keys(modes).filter(n => !builtIn.includes(n)).sort()
  const modeNames = [...builtIn, ...custom]
  const currentModeKeys = modes[selectedMode] || EMPTY

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

        <div className="flex items-center justify-between">
          <div>
            <span className="text-sm text-normal-black">Execute key fallback</span>
            <p className="text-xs text-normal-black opacity-60 mt-0.5">When chat key is rate-limited, retry with execute mode keys</p>
          </div>
          <button
            onClick={() => {
              const next = !executeFallback
              setExecuteFallback(next)
              api.executeFallbackSet(next).catch(() => setExecuteFallback(!next))
            }}
            className={`relative w-10 h-5 rounded-full transition-colors ${executeFallback ? 'bg-brand-primary' : 'bg-sidebar-hover-white'}`}
          >
            <span className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${executeFallback ? 'translate-x-5' : 'translate-x-0'}`} />
          </button>
        </div>

        {allModels.length > 0 && (
          <>
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

            <div>
              <label className="block text-xs text-normal-black mb-1">Summarization model</label>
              <select
                value={summarizationModel}
                onChange={e => setSummarizationModel(e.target.value)}
                className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
              >
                <option value="">None (simple truncation)</option>
                {allModels.map(m => (
                  <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
                ))}
              </select>
              <p className="text-xs text-normal-black mt-1 opacity-60">
                Cheap model for condensing old tool outputs (e.g. Haiku, GPT-4o Mini, Gemini Flash)
              </p>
            </div>
          </>
        )}

        <div>
          <label className="block text-xs text-normal-black mb-2">Mode</label>
          <div className="flex gap-2 mb-3">
            {modeNames.map(name => (
              <button
                key={name}
                onClick={() => { setSelectedMode(name); switchMode(name) }}
                className={`px-3 py-1.5 text-sm rounded-lg transition-colors ${
                  activeMode === name
                    ? 'bg-brand-primary text-white'
                    : selectedMode === name
                      ? 'bg-sidebar-hover-white text-hover-black'
                      : 'bg-sidebar-white text-normal-black hover:bg-sidebar-hover-white'
                }`}
              >
                {name}
              </button>
            ))}
          </div>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs text-normal-black opacity-60">
                {selectedMode === 'default' ? 'Main API keys' : `API keys for ${selectedMode} mode (blank = use main keys)`}
              </span>
              {selectedMode !== 'default' && (
                <button
                  onClick={() => clearModeKeys(selectedMode)}
                  className="text-xs text-normal-black opacity-60 hover:opacity-100 underline"
                >
                  Clear
                </button>
              )}
            </div>

            {allModels.length > 0 && selectedMode !== 'default' && (
              <div>
                <label className="block text-xs text-normal-black mb-1">Default model for this mode</label>
                <select
                  value={currentModeKeys.model || ''}
                  onChange={e => updateModeModel(selectedMode, e.target.value)}
                  className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black outline-none"
                >
                  <option value="">Use global default</option>
                  {allModels.map(m => (
                    <option key={m.id} value={m.id}>{m.name} ({m.provider})</option>
                  ))}
                </select>
              </div>
            )}

            {selectedMode === 'default' ? (
              PROVIDERS.map(({ key, label, placeholder }) => (
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
              ))
            ) : (
              PROVIDERS.map(({ key, label, placeholder }) => (
                <div key={key}>
                  <label className="block text-xs text-normal-black mb-1">{label}</label>
                  <input
                    type="password"
                    value={currentModeKeys[key] || ''}
                    onChange={e => updateModeKey(selectedMode, key, e.target.value)}
                    placeholder={placeholder}
                    className="w-full text-sm px-3 py-2 rounded-xl border border-border-white bg-sidebar-white text-hover-black font-mono outline-none"
                  />
                </div>
              ))
            )}
          </div>
        </div>

        {error && <p className="text-xs text-red">{error}</p>}
      </div>
    </div>
  )
}
