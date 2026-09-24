import { useState, useEffect } from 'react'
import { onContextSnapshot, openFile, type ContextSnapshotData } from '../../bridge'
import { Search, ChevronDown, ChevronRight, FileCode2 } from 'lucide-react'
import { fmtCount, useLocale } from '@wesui'

export function ContextSnapshotBar() {
  const { t, locale } = useLocale()
  const [snapshot, setSnapshot] = useState<ContextSnapshotData | null>(null)
  const [expanded, setExpanded] = useState(false)

  useEffect(() => {
    return onContextSnapshot((s) => {
      setSnapshot(s)
      setExpanded(false)
    })
  }, [])

  // INV-CHAT-05: do not replay user @-mentions here. Those chips belong in
  // ChatInputCard.topSlot (while composing) and the sent user bubble (after
  // send). This bar only surfaces engine-assembled CKG fragments.
  if (!snapshot || snapshot.fragments.length === 0) return null

  const pct = snapshot.token_budget > 0
    ? Math.round((snapshot.token_used / snapshot.token_budget) * 100)
    : 0

  return (
    <div className="mx-5 mb-2 rounded-md border border-border bg-surface overflow-hidden">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full px-3 py-1.5 text-left hover:bg-surface-3 transition-colors"
      >
        <Search size={12} className="text-accent shrink-0" />
        <span className="text-caption text-dim">
          {t('codeintel.referenced_prefix', '参考了')} {snapshot.fragments.length} {t('codeintel.referenced_suffix', '个代码片段')}
        </span>
        <span className="text-caption text-muted tabular-nums ml-auto">
          {fmtCount(snapshot.token_used, locale)} / {fmtCount(snapshot.token_budget, locale)} tokens ({pct}%)
        </span>
        {expanded
          ? <ChevronDown size={12} className="text-dim shrink-0" />
          : <ChevronRight size={12} className="text-dim shrink-0" />
        }
      </button>
      {expanded && (
        <div className="border-t border-border max-h-48 overflow-y-auto" style={{ scrollbarWidth: 'thin' }}>
          {snapshot.fragments.map((f, i) => {
            const shortPath = f.path.split('/').slice(-3).join('/')
            return (
              <button
                key={i}
                onClick={() => openFile(f.path)}
                className="flex items-center gap-2 w-full px-3 py-1 text-left hover:bg-surface-3 transition-colors"
              >
                <FileCode2 size={11} className="text-accent/50 shrink-0" />
                <span className="text-caption text-text font-mono truncate">{shortPath}</span>
                {f.symbol && (
                  <span className="text-caption text-accent font-mono">
                    {f.symbol}
                  </span>
                )}
                <span className="text-caption text-muted ml-auto shrink-0">{f.kind}</span>
                <span className="text-caption text-muted tabular-nums shrink-0">{f.token_cost}t</span>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
