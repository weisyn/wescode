import { useState } from 'react'
import { AppearanceSettingsPanel } from '@wesui/settings'
import type { FontSize as WesUIFontSize } from '@wesui/settings'
import { changeLanguage, getCurrentLanguage } from '@/lib/i18n'
import { notifyLocaleChanged } from '@/bridge'

type LocalFontSize = 'sm' | 'md' | 'lg'

const FONT_MAP: Record<LocalFontSize, WesUIFontSize> = { sm: 'small', md: 'medium', lg: 'large' }
const FONT_REVERSE: Record<WesUIFontSize, LocalFontSize> = { small: 'sm', medium: 'md', large: 'lg' }

// Single source of truth: i18next's wescode-locale storage key. The
// appearance dropdown surfaces the two supported languages using the same
// codes ("zh-CN" / "en") so nothing is lost in translation between the
// picker and the storage layer.
const LANGUAGES = [
  { code: 'zh-CN', label: '中文', nativeLabel: '中文' },
  { code: 'en', label: 'English', nativeLabel: 'English' },
]

export function AppearanceSettings() {
  const [lang, setLang] = useState(() => getCurrentLanguage())
  const [fontSize, setFontSize] = useState<LocalFontSize>(
    () => (localStorage.getItem('wescode-fontSize') as LocalFontSize) || 'md',
  )

  const handleLang = (code: string) => {
    setLang(code)
    changeLanguage(code)
    notifyLocaleChanged(code)
  }

  const handleFontSize = (size: WesUIFontSize) => {
    const local = FONT_REVERSE[size]
    setFontSize(local)
    localStorage.setItem('wescode-fontSize', local)
    document.documentElement.dataset.fontSize = local
  }

  return (
    <AppearanceSettingsPanel
      theme="dark"
      fontSize={FONT_MAP[fontSize]}
      language={lang}
      languages={LANGUAGES}
      onThemeChange={() => {}}
      onFontSizeChange={handleFontSize}
      onLanguageChange={handleLang}
      hideTheme
    />
  )
}
