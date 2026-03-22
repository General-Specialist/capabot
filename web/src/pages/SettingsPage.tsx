import { useTheme } from '@/hooks/useTheme'

export function SettingsPage() {
  const { theme, toggle } = useTheme()

  return (
    <div className="p-6 max-w-2xl">
      <h1 className="text-xl font-semibold mb-1" style={{ color: 'var(--color-text-hover-black)' }}>
        Settings
      </h1>
      <p className="text-sm mb-8" style={{ color: 'var(--color-dark-text-normal)' }}>
        Configuration and preferences
      </p>

      <div className="space-y-6">
        {/* API */}
        <section
          className="p-4 rounded-lg border"
          style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
        >
          <h2 className="text-sm font-medium mb-3" style={{ color: 'var(--color-text-hover-black)' }}>
            API
          </h2>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>
                API Endpoint
              </span>
              <span
                className="font-mono text-xs px-2 py-1 rounded border"
                style={{
                  background: 'var(--color-white)',
                  borderColor: 'var(--color-border-white)',
                  color: 'var(--color-text-hover-black)',
                }}
              >
                {window.location.origin}/api
              </span>
            </div>
          </div>
        </section>

        {/* Appearance */}
        <section
          className="p-4 rounded-lg border"
          style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
        >
          <h2 className="text-sm font-medium mb-3" style={{ color: 'var(--color-text-hover-black)' }}>
            Appearance
          </h2>
          <div className="flex items-center justify-between">
            <span className="text-xs" style={{ color: 'var(--color-dark-text-normal)' }}>
              Theme
            </span>
            <button
              onClick={toggle}
              className="px-3 py-1.5 rounded-md text-xs font-medium border transition-colors"
              style={{
                background: 'var(--color-white)',
                borderColor: 'var(--color-border-white)',
                color: 'var(--color-text-hover-black)',
              }}
            >
              {theme === 'dark' ? 'Switch to Light' : 'Switch to Dark'}
            </button>
          </div>
        </section>

        {/* About */}
        <section
          className="p-4 rounded-lg border"
          style={{ background: 'var(--color-sidebar-white)', borderColor: 'var(--color-border-white)' }}
        >
          <h2 className="text-sm font-medium mb-3" style={{ color: 'var(--color-text-hover-black)' }}>
            About Capabot
          </h2>
          <p className="text-xs leading-relaxed" style={{ color: 'var(--color-dark-text-normal)' }}>
            Capabot is an AI agent platform that connects LLMs with skills and tools. It supports streaming
            conversations, multi-agent orchestration, and extensible skill modules.
          </p>
        </section>
      </div>
    </div>
  )
}
