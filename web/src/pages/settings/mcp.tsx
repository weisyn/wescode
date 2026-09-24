import { useMemo } from 'react'
import { useTranslation } from '@/lib/i18n'
import { MCPSettingsPanel, type MCPSettingsAdapter, type MCPServerConfig, type MCPServerSnapshot, type MCPProbeResult, type MCPTemplate } from '@wesui/connections'
import { request } from '@/bridge'
import { FileText, Globe, Terminal, Wrench } from 'lucide-react'

/** wesgine API 返回 snake_case，wesui 期望 camelCase；显式映射已知字段 */
function mapSnapshot(s: any): MCPServerSnapshot {
  return {
    ...s,
    toolCount: s.tool_count ?? s.toolCount ?? 0,
    lastError: s.last_error ?? s.lastError,
    circuit: s.circuit && {
      ...s.circuit,
      openUntil: s.circuit.open_until ?? s.circuit.openUntil,
    },
    config: s.config && {
      ...s.config,
      connectTimeoutMs: s.config.connect_timeout_ms ?? s.config.connectTimeoutMs,
      toolTimeoutMs: s.config.tool_timeout_ms ?? s.config.toolTimeoutMs,
      toolsInclude: s.config.tools_include ?? s.config.toolsInclude,
      toolsExclude: s.config.tools_exclude ?? s.config.toolsExclude,
      contextVars: s.config.context_vars ?? s.config.contextVars,
    },
  }
}

function mapProbe(p: any): MCPProbeResult {
  return {
    ...p,
    toolCount: p.tool_count ?? p.toolCount ?? 0,
    connectMs: p.connect_ms ?? p.connectMs ?? 0,
    initMs: p.init_ms ?? p.initMs ?? 0,
    listMs: p.list_ms ?? p.listMs,
    totalMs: p.total_ms ?? p.totalMs ?? 0,
  }
}

function useBridgeAdapter(): MCPSettingsAdapter {
  return useMemo(() => ({
    list: async () => {
      const result = await request('sidebar/listMCPServers')
      const raw = (result as any[] ?? [])
      return raw.map(mapSnapshot)
    },
    upsert: (config: MCPServerConfig) => request('sidebar/upsertMCPServer', config),
    delete: (name) => request('sidebar/deleteMCPServer', { name }),
    connect: (name) => request('sidebar/connectMCPServer', { name }),
    disconnect: (name) => request('sidebar/disconnectMCPServer', { name }),
    probe: async (config: MCPServerConfig) => {
      const raw: any = await request('sidebar/probeMCPServer', config)
      return mapProbe(raw)
    },
    toggleEnabled: (name, enabled) =>
      enabled
        ? request('sidebar/enableMCPServer', { name })
        : request('sidebar/disableMCPServer', { name }),
    callTool: (serverName, toolName, args) =>
      request('sidebar/mcpCallTool', { serverName, toolName, args }),
  }), [])
}

function useTemplates(): MCPTemplate[] {
  const { t } = useTranslation()
  return useMemo(() => {
    const wsRoot = (window as any).__WESCODE_INIT__?.params?.workspaceRoot ?? '.'
    return [
      { id: 'filesystem', icon: FileText, name: t('connections.mcp_tpl_filesystem', '文件系统'), desc: t('connections.mcp_tpl_filesystem_desc', '读写本地目录'), preset: { name: 'filesystem', command: 'npx', args: ['-y', '@modelcontextprotocol/server-filesystem', wsRoot] } },
      { id: 'fetch', icon: Globe, name: t('connections.mcp_tpl_fetch', '网页抓取'), desc: t('connections.mcp_tpl_fetch_desc', '抓取和分析网页内容'), preset: { name: 'fetch', command: 'npx', args: ['-y', 'mcp-server-fetch'] } },
      { id: 'github', icon: Terminal, name: 'GitHub', desc: t('connections.mcp_tpl_github_desc', '访问 GitHub 仓库'), preset: { name: 'github', command: 'npx', args: ['-y', '@modelcontextprotocol/server-github'], env: { GITHUB_TOKEN: '' } } },
      { id: 'playwright', icon: Globe, name: 'Playwright', desc: t('connections.mcp_tpl_playwright_desc', '浏览器自动化'), preset: { name: 'playwright', command: 'npx', args: ['-y', '@playwright/mcp@latest'] } },
      { id: 'custom-http', icon: Wrench, name: t('connections.mcp_tpl_custom_http', '自定义 HTTP'), desc: t('connections.mcp_tpl_custom_http_desc', '连接自部署 MCP 服务'), preset: { name: 'my-mcp-server', url: 'http://localhost:8080/mcp' } },
    ]
  }, [t])
}

export function MCPSettings() {
  const adapter = useBridgeAdapter()
  const templates = useTemplates()
  return <MCPSettingsPanel adapter={adapter} templates={templates} />
}
