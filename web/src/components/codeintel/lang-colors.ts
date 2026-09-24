import { Code2, Box, type LucideIcon } from 'lucide-react'

export const LANG_COLORS: Record<string, string> = {
  go: '#00ADD8', typescript: '#3178C6', javascript: '#F7DF1E', python: '#3776AB',
  rust: '#DEA584', java: '#ED8B00', cpp: '#00599C', c: '#A8B9CC',
  ruby: '#CC342D', swift: '#F05138', kotlin: '#7F52FF',
}

export function langColor(lang: string): string {
  return LANG_COLORS[lang] ?? '#6B7280'
}

export const SYMBOL_STYLE: Record<string, { icon: LucideIcon; color: string }> = {
  function:  { icon: Code2, color: 'text-[#DCDCAA]' },
  method:    { icon: Code2, color: 'text-[#DCDCAA]' },
  type:      { icon: Box,   color: 'text-[#4EC9B0]' },
  struct:    { icon: Box,   color: 'text-[#4EC9B0]' },
  interface: { icon: Box,   color: 'text-[#B8D7A3]' },
  class:     { icon: Box,   color: 'text-[#4FC1FF]' },
}

export const DEFAULT_SYMBOL_STYLE = { icon: Code2, color: 'text-dim' }
