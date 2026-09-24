import { useLocale } from '@wesui'

export function PluginsSettings() {
  const { t } = useLocale()

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.plugins_title', '插件管理')}</h2>
        <p className="text-small text-dim">{t('engine_settings.plugins_desc', '扩展 AI 助手的能力')}</p>
      </div>
      <div className="rounded-md border border-border bg-surface p-6 text-center space-y-3">
        <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center mx-auto">
          <span className="text-h2 text-dim">🧩</span>
        </div>
        <p className="text-body text-text-secondary">{t('engine_settings.plugins_use_mcp', '插件能力已整合至 MCP 工具')}</p>
        <p className="text-small text-dim">{t('engine_settings.plugins_use_mcp_hint', '通过「连接 → MCP 工具」管理外部工具和服务集成。\nMCP 协议支持 stdio / HTTP / SSE 三种传输方式，可连接任何兼容的工具服务器。')}</p>
      </div>
    </div>
  )
}
