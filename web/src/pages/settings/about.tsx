import { MessageSquare, Bug, BookOpen, Mail, ExternalLink } from 'lucide-react'
import { AboutSettingsPanel } from '@wesui/settings'
import { useTranslation } from '@/lib/i18n'
import { ChangelogTimeline } from '@/components/settings/ChangelogTimeline'

// Build-injected from package.json (vite.config.ts `define`). The literal that
// used to live here read '0.1.0-beta' while the manifest said '0.1.0' — a
// hand-maintained copy of a value the build already knows.
const VERSION = __APP_VERSION__

function openExternal(url: string) {
  const vscode = (window as any).__VSCODE_API__
  if (vscode) {
    vscode.postMessage({ type: 'openExternal', url })
  } else {
    window.open(url, '_blank')
  }
}

export function AboutSettings() {
  const { t } = useTranslation()

  const LINKS = [
    {
      key: 'feedback',
      icon: MessageSquare,
      label: t('about.feedback', '反馈建议'),
      desc: t('about.feedbackDesc', '功能建议、体验反馈'),
      url: 'https://github.com/weisyn/wescode/issues/new?template=feature_request.md',
      color: 'hsl(220 60% 65%)',
    },
    {
      key: 'bug',
      icon: Bug,
      label: t('about.reportBug', '报告问题'),
      desc: t('about.reportBugDesc', '描述问题，帮助我们改进'),
      url: 'https://github.com/weisyn/wescode/issues/new?template=bug_report.md',
      color: 'hsl(0 60% 60%)',
    },
    {
      key: 'docs',
      icon: BookOpen,
      label: t('about.viewDocs', '查看文档'),
      desc: t('about.viewDocsDesc', '使用指南、API 文档'),
      url: 'https://docs.weisyn.com/wescode',
      color: 'hsl(150 50% 55%)',
    },
    {
      key: 'contact',
      icon: Mail,
      label: t('about.contactUs', '联系我们'),
      desc: t('about.contactUsDesc', '邮件、微信、社区'),
      url: 'https://www.weisyn.com/contact',
      color: 'hsl(30 60% 60%)',
    },
  ]

  const extra = (
    <>
      <div className="text-center mb-12">
        <h1 className="text-h1 text-text">WES Code</h1>
        <span className="inline-block mt-3 px-3 py-1 rounded text-caption text-dim bg-surface-2 border border-border">
          v{VERSION}
        </span>
      </div>

      <div className="grid grid-cols-2 gap-3 mb-12">
        {LINKS.map(item => {
          const Icon = item.icon
          return (
            <button
              key={item.key}
              onClick={() => openExternal(item.url)}
              className="flex flex-col items-start gap-3 px-5 py-5 rounded-md border border-border bg-surface
                         hover:bg-surface-2 hover:border-border-hi transition-all group text-left"
              style={{ transitionDuration: 'var(--duration-fast)' }}
            >
              <div className="flex items-center gap-3 w-full">
                <div
                  className="w-9 h-9 rounded flex items-center justify-center shrink-0"
                  style={{
                    backgroundColor: item.color.replace(')', ' / 0.12)'),
                    color: item.color,
                  }}
                >
                  <Icon size={17} />
                </div>
                <span className="flex-1 text-h3 text-text">{item.label}</span>
                <ExternalLink size={13} className="text-muted opacity-0 group-hover:opacity-100 transition-opacity shrink-0" style={{ transitionDuration: 'var(--duration-fast)' }} />
              </div>
              <p className="text-small text-dim">{item.desc}</p>
            </button>
          )
        })}
      </div>

      <ChangelogTimeline openExternal={openExternal} />

      <div className="mt-12 pt-6 border-t border-border text-center">
        <p className="text-caption text-muted">
          Powered by{' '}
          <button
            onClick={() => openExternal('https://www.weisyn.com')}
            className="text-dim hover:text-text underline underline-offset-2 transition-colors"
            style={{ transitionDuration: 'var(--duration-fast)' }}
          >
            Weisyn
          </button>
        </p>
      </div>
    </>
  )

  return <AboutSettingsPanel version={VERSION} extra={extra} />
}
