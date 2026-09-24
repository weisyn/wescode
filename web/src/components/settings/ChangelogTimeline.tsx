import { useState } from 'react'
import { ChevronRight, ExternalLink } from 'lucide-react'
import { cn } from '@wesui'
import { useTranslation } from '@/lib/i18n'
import { changelog } from '@/data/changelog'

// 版本只会增加。此前每个版本都把全部条目摊平渲染，第 3 个版本时关于页已经要滚两屏，
// 而滚出去的那些条目没人读——用户来这里是想知道"这次装的是什么"。
// 所以高度按版本数线性增长而非按条目数：最新版展开，历史版一行一个。
//
// 全量展开不分批：灌 300 个版本实测一次性渲染 38.8ms（2845 个 DOM 节点），
// 而条目总数另有硬上限 10（scripts/check-changelog-size.cjs），真实规模比这小一个量级。
// 曾按批加载过，在上限 10 之下那个分支点一次就走完，是为不存在的问题留的状态。
const VISIBLE_RELEASES = 5
const FULL_CHANGELOG_URL = 'https://github.com/weisyn/wescode/releases'

export function ChangelogTimeline({ openExternal }: { openExternal: (url: string) => void }) {
  const { t } = useTranslation()
  const [showOlder, setShowOlder] = useState(false)
  const [open, setOpen] = useState<ReadonlySet<string>>(
    () => new Set(changelog[0] ? [changelog[0].version] : []),
  )

  const visible = showOlder ? changelog : changelog.slice(0, VISIBLE_RELEASES)
  const olderCount = changelog.length - visible.length

  const toggle = (version: string) =>
    setOpen(prev => {
      const next = new Set(prev)
      if (!next.delete(version)) next.add(version)
      return next
    })

  return (
    <div className="border-t border-border pt-8 mt-8">
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-h2 font-semibold text-text">{t('about.changelog', '更新日志')}</h2>
        <button
          type="button"
          onClick={() => openExternal(FULL_CHANGELOG_URL)}
          className="flex items-center gap-1.5 px-2 py-1 rounded text-caption text-dim
                     hover:bg-surface-3 hover:text-text active:bg-surface-hover transition-colors"
          style={{ transitionDuration: 'var(--duration-fast)' }}
        >
          {t('about.changelogFullLog', '完整日志')}
          <ExternalLink size={12} />
        </button>
      </div>

      <ol className="relative">
        {visible.map((release, idx) => {
          const isLatest = idx === 0
          const isOpen = open.has(release.version)
          const isLast = idx === visible.length - 1 && olderCount === 0

          return (
            <li key={release.version} className="relative pl-6">
              {!isLast && (
                <span aria-hidden className="absolute left-[3px] top-5 bottom-0 w-px bg-border" />
              )}
              <span
                aria-hidden
                className={cn(
                  'absolute left-0 top-[10px] w-[7px] h-[7px] rounded-full',
                  isLatest ? 'bg-accent' : 'bg-surface-3 ring-1 ring-border-hi',
                )}
              />

              <button
                type="button"
                aria-expanded={isOpen}
                onClick={() => toggle(release.version)}
                className="w-full flex items-center gap-2 -ml-2 px-2 py-1.5 rounded text-left
                           hover:bg-surface-3 active:bg-surface-hover transition-colors"
                style={{ transitionDuration: 'var(--duration-fast)' }}
              >
                <ChevronRight
                  size={12}
                  className={cn('shrink-0 text-muted transition-transform', isOpen && 'rotate-90')}
                  style={{ transitionDuration: 'var(--duration-fast)' }}
                />
                <span className="text-body font-medium text-text">v{release.version}</span>
                {isLatest && (
                  <span className="px-1.5 py-px rounded-sm text-caption text-accent bg-accent-soft">
                    {t('about.changelogLatest', '最新')}
                  </span>
                )}
                <span className="flex-1" />
                <span className="text-caption text-dim tabular-nums">{release.date}</span>
              </button>

              {isOpen && (
                <ul className="mt-0.5 mb-3 ml-5 list-disc space-y-1 text-small text-dim marker:text-muted">
                  {release.items.map((item, i) => (
                    <li key={i}>{t(item.k, item.zh)}</li>
                  ))}
                </ul>
              )}
            </li>
          )
        })}

        {/* 省略号节点：时间线的竖线得有个终点，否则最后一段线悬在半空断掉 */}
        {olderCount > 0 && (
          <li className="relative pl-6">
            <span
              aria-hidden
              className="absolute left-[1px] top-[11px] w-[5px] h-[5px] rounded-full bg-border-hi"
            />
            <button
              type="button"
              onClick={() => setShowOlder(true)}
              className="-ml-2 px-2 py-1 rounded text-caption text-dim
                         hover:bg-surface-3 hover:text-text active:bg-surface-hover transition-colors"
              style={{ transitionDuration: 'var(--duration-fast)' }}
            >
              {t('about.changelogShowOlder', '显示更早版本')}
              <span className="ml-1 text-muted tabular-nums">{olderCount}</span>
            </button>
          </li>
        )}
      </ol>
    </div>
  )
}
