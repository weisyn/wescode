import { useCallback } from 'react'
import { Toast as WesUIToast, useToast as useWesUIToast } from '@wesui/primitives/Toast'

export function Toast({ msg, ok }: { msg: string; ok: boolean }) {
  return <WesUIToast msg={msg} type={ok ? 'success' : 'error'} />
}

export function useToast(durationMs = 3500) {
  const { toast, showToast: _show } = useWesUIToast(durationMs)

  const showToast = useCallback((msg: string, ok = true) => {
    _show(msg, ok ? 'success' : 'error')
  }, [_show])

  const mapped = toast ? { msg: toast.msg, ok: toast.type === 'success' } : null
  return { toast: mapped, showToast }
}
