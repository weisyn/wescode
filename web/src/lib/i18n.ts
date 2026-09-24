import i18next from 'i18next'
import { initReactI18next, useTranslation as useI18nTranslation } from 'react-i18next'
import en from '../locales/en.json'

// 只有一个语言包。中文写在调用点的 defaultValue 里，不再有 zh-CN.json——
// 两份书写点意味着同一句中文有两个来源，改了一处另一处继续显示旧文案，
// 而缺键时 i18next 渲染 key 字面量（`agent.detail`）而不是中文。
// 因此 `zh-CN` 命名空间刻意留空：查不到就落回 defaultValue，那正是中文。
// 闸门 `wesui.git/scripts/locale-audit.mjs` 保证每个 t() 都带非空 defaultValue。

declare global {
  interface Window {
    __WESCODE_LOCALE__?: string
  }
}

const STORAGE_KEY = 'wescode-locale'

function getInitialLocale(): string {
  if (window.__WESCODE_LOCALE__ === 'en' || window.__WESCODE_LOCALE__ === 'zh-CN') {
    return window.__WESCODE_LOCALE__
  }
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'en' || stored === 'zh-CN') return stored
  } catch { /* webview localStorage may fail */ }
  return 'zh-CN'
}

i18next.use(initReactI18next).init({
  resources: {
    'zh-CN': { translation: {} },
    en: { translation: en },
  },
  lng: getInitialLocale(),
  // 不设 fallbackLng：英文缺键必须落回 defaultValue（中文），而不是去查另一个语言包
  // 再落回同一个 defaultValue——多绕一层只会让「英文缺了什么」不可观测。
  fallbackLng: false,
  interpolation: { escapeValue: false },
  react: { useSuspense: false },
})

export function changeLanguage(lng: string) {
  i18next.changeLanguage(lng)
  try {
    localStorage.setItem(STORAGE_KEY, lng)
  } catch { /* best-effort */ }
}

export function getCurrentLanguage(): string {
  return i18next.language || 'zh-CN'
}

export function toggleLanguage(): string {
  const next = getCurrentLanguage() === 'zh-CN' ? 'en' : 'zh-CN'
  changeLanguage(next)
  return next
}

export const t = i18next.t.bind(i18next)
export function useTranslation() {
  return useI18nTranslation()
}

export default i18next
