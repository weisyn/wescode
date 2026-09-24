import { useLocale } from '@wesui'

export function DesktopSettings() {
  const { t } = useLocale()

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-h2 text-text mb-1">{t('engine_settings.desktop_title', '桌面自动化')}</h2>
        <p className="text-small text-dim">{t('engine_settings.desktop_desc', '通过 wesgine Desktop adapter 实现屏幕截图、键鼠操控等 Computer Use 能力')}</p>
      </div>
      <div className="rounded-md border border-border bg-surface p-6 text-center space-y-3">
        <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center mx-auto">
          <span className="text-h2 text-dim">🖥</span>
        </div>
        <p className="text-body text-text-secondary">{t('engine_settings.desktop_dev', '桌面自动化功能正在开发中')}</p>
        <p className="text-small text-dim">{t('engine_settings.desktop_dev_hint', '该功能需要 wesgine Desktop adapter 二进制支持，当前版本尚未集成。\n浏览器自动化（Playwright MCP）已可用，可在「连接 → 浏览器自动化」中配置。')}</p>
      </div>
    </div>
  )
}
