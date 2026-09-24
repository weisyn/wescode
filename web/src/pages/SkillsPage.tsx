import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from '@/lib/i18n'
import { Sparkle, RefreshCw, Plus, Store, ShieldCheck } from 'lucide-react'
import { SearchInput } from '@wesui/forms'
import { PageShell } from '@wesui/layout'
import type { TabDef } from '@wesui/layout'
import { SkillCard, CatalogSkillCard, HealthCheckResult } from '@wesui/skill'
import type { SkillSummary, CatalogSkill, SkillHealthItem } from '@wesui/skill'
import { asSkillChannel } from '@wesui/skill'
import { Button, Toast, useToast } from '@wesui/primitives'
import { listSkills, deleteSkill, toggleSkill, reloadSkills, skillHealth } from '@/lib/api/agents'
import type { SkillItem } from '@/lib/api/agents'
import { request, navigate as bridgeNavigate } from '@/bridge'
import { useNav } from '@/lib/nav'

function notifySkillsChanged(): void {
  bridgeNavigate('skillsChanged')
}

function mapToSkillSummary(s: SkillItem): SkillSummary {
  return {
    name: s.name,
    slug: s.slug,
    description: s.description,
    enabled: s.enabled,
    tags: s.tags ?? [],
    operators: s.operators ?? [],
    category: s.category,
    // Narrowed, not cast. The cast this replaced told the compiler an
    // engine string was one of the UI's own values; every label lookup
    // then missed and the badge printed the raw engine term.
    channel: asSkillChannel(s.channel),
    usageCount: s.usageCount ?? 0,
  }
}

type Tab = 'installed' | 'market' | 'health'

export function SkillsPage() {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()

  const [tab, setTab] = useState<Tab>('installed')
  const [skills, setSkills] = useState<SkillSummary[]>([])
  const [catalogRaw, setCatalogRaw] = useState<CatalogSkill[]>([])
  const [loading, setLoading] = useState(true)
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [installingIds, setInstallingIds] = useState<Set<string>>(new Set())
  const [search, setSearch] = useState('')
  const [healthItems, setHealthItems] = useState<SkillHealthItem[] | null>(null)
  const [healthLoading, setHealthLoading] = useState(false)

  const catalog = catalogRaw.map(s => ({
    ...s,
    installed: skills.some(sk => sk.slug === s.id),
  }))

  const [loadOk, setLoadOk] = useState(false)

  const tabs: TabDef<Tab>[] = [
    { id: 'installed', label: t('skills.installed', '已安装'), badge: skills.length },
    { id: 'market', label: t('skills.market', '技能市场'), icon: <Store size={13} /> },
    { id: 'health', label: t('skills.healthCheck', '健康检查'), icon: <ShieldCheck size={13} /> },
  ]

  const load = useCallback(async () => {
    try {
      const data = await listSkills()
      setSkills(data.map(mapToSkillSummary))
      setLoadOk(true)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (!msg.includes('no workspace') && !msg.includes('not initialized') && !msg.includes('backend not')) {
        showToast(t('skills.loadFailed', '加载技能列表失败'), 'error')
      }
      console.error(e)
    } finally {
      setLoading(false)
    }
  }, [t, showToast])

  const loadCatalog = useCallback(async () => {
    setCatalogLoading(true)
    try {
      const data = await request<CatalogSkill[]>('sidebar/skillCatalog') ?? []
      setCatalogRaw(data)
    } catch {
      showToast(t('skills.catalogFailed', '加载技能市场失败'), 'error')
    } finally {
      setCatalogLoading(false)
    }
  }, [t, showToast])

  useEffect(() => {
    load()
    if (!loadOk) {
      const timer = setTimeout(() => { void load() }, 3000)
      return () => clearTimeout(timer)
    }
  }, [load, loadOk])
  useEffect(() => { if (tab === 'market' && catalogRaw.length === 0) loadCatalog() }, [tab, catalogRaw.length, loadCatalog])

  const handleReload = async () => {
    try {
      await reloadSkills()
      await load()
      showToast(t('skills.reloaded', '已刷新'))
    } catch {
      showToast(t('skills.reloadFailed', '刷新失败'), 'error')
    }
  }

  const handleToggle = async (slug: string, enabled: boolean) => {
    try {
      await toggleSkill(slug, enabled)
      setSkills(prev => prev.map(s => s.slug === slug ? { ...s, enabled } : s))
      showToast(enabled ? t('skills.enabled', '已启用') : t('skills.disabled', '已禁用'))
      notifySkillsChanged()
    } catch {
      showToast(t('skills.toggleFailed', '切换失败，请重试'), 'error')
    }
  }

  const handleDelete = async (slug: string) => {
    try {
      await deleteSkill(slug)
      setSkills(prev => prev.filter(s => s.slug !== slug))
      showToast(t('skills.deleted', '已删除'))
      notifySkillsChanged()
    } catch {
      showToast(t('skills.deleteFailed', '删除失败，请重试'), 'error')
    }
  }

  const handleEdit = (slug: string) => {
    navigate({ page: 'skillWorkshop', params: { draftId: slug } })
  }

  const handleHealthCheck = async () => {
    setHealthLoading(true)
    try {
      const result = await skillHealth()
      const issueMap = new Map((result.issues ?? []).map(i => [i.slug, i]))
      const items: SkillHealthItem[] = skills.map(s => {
        const issue = issueMap.get(s.slug)
        if (issue) {
          return {
            id: s.slug,
            name: s.name,
            compat: {
              status: 'incompatible' as const,
              issues: (issue.problems ?? []).map(msg => ({
                category: 'general',
                severity: 'error' as const,
                title: msg,
                description: msg,
              })),
            },
          }
        }
        return { id: s.slug, name: s.name, compat: { status: 'compatible' as const, issues: [] } }
      })
      setHealthItems(items)
    } catch {
      setHealthItems(skills.map(s => ({
        id: s.slug,
        name: s.name,
        compat: { status: 'compatible' as const, issues: [] },
      })))
    } finally {
      setHealthLoading(false)
    }
  }

  const handleInstall = async (id: string) => {
    setInstallingIds(prev => new Set(prev).add(id))
    try {
      await request('sidebar/skillHubInstall', { slug: id })
      showToast(t('skills.installed', '已安装'))
      await load()
      notifySkillsChanged()
    } catch {
      showToast(t('skills.installFailed', '安装失败'), 'error')
    } finally {
      setInstallingIds(prev => { const n = new Set(prev); n.delete(id); return n })
    }
  }

  const filtered = search
    ? skills.filter(s =>
        s.name.toLowerCase().includes(search.toLowerCase()) ||
        (s.description ?? '').toLowerCase().includes(search.toLowerCase()) ||
        (s.tags ?? []).some(tag => tag.toLowerCase().includes(search.toLowerCase()))
      )
    : skills

  const filteredCatalog = search
    ? catalog.filter(s =>
        s.name.toLowerCase().includes(search.toLowerCase()) ||
        s.description.toLowerCase().includes(search.toLowerCase()) ||
        s.tags.some(tag => tag.toLowerCase().includes(search.toLowerCase()))
      )
    : catalog

  const headerActions = (
    <div className="flex items-center gap-2">
      <Button variant="ghost" size="icon-sm" onClick={handleReload} title={t('skills.reload', '刷新')}>
        <RefreshCw size={14} />
      </Button>
      <Button variant="secondary" size="sm" onClick={() => navigate({ page: 'skillWorkshop' })}>
        <Plus size={14} className="mr-1" />
        {t('skills.newSkill', '新建技能')}
      </Button>
    </div>
  )

  return (
    <PageShell
      icon={<Sparkle size={16} />}
      title={t('skills.title', '技能管理')}
      subtitle={t('skills.subtitle', '管理 AI 助手的技能知识')}
      helpContent={{
        title: '技能',
        description: '技能是 AI 的操作手册——教 AI "如何做某件事"的标准流程和专业知识。',
        items: [
          { label: '技能类型', values: ['引擎技能（内置）：plan / delegation / retrieval', '自定义技能：用户编写的工作流指导'] },
          { label: '适用场景', values: ['代码审查规范 / 重构方法论', '测试策略 / 文档编写规范', '项目特定的编码惯例'] },
        ],
      }}
      actions={headerActions}
      tabs={tabs}
      activeTab={tab}
      onTabChange={id => {
        setTab(id as Tab)
        if (id === 'health' && !healthItems) handleHealthCheck()
      }}
      tabLayoutId="skills-tabs"
    >
      <div className="mb-4">
        <SearchInput
          value={search}
          onChange={setSearch}
          placeholder={t('skills.searchPlaceholder', '搜索技能...')}
        />
      </div>

      {tab === 'installed' && (
        <>
          {loading ? (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {[1, 2].map(i => (
                <div key={i} className="h-32 rounded-md bg-surface-2 animate-pulse" />
              ))}
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-16 text-center">
              <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center">
                <Sparkle size={20} className="text-dim" />
              </div>
              <p className="text-body text-text-secondary">
                {search ? t('skills.noMatch', '没有匹配的技能') : t('skills.noSkills', '暂无技能')}
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {filtered.map((skill, i) => (
                <SkillCard
                  key={skill.slug}
                  skill={skill}
                  index={i}
                  onToggle={handleToggle}
                  onEdit={handleEdit}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          )}
        </>
      )}

      {tab === 'market' && (
        <>
          {catalogLoading ? (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {[1, 2].map(i => (
                <div key={i} className="h-32 rounded-md bg-surface-2 animate-pulse" />
              ))}
            </div>
          ) : filteredCatalog.length === 0 ? (
            <div className="flex flex-col items-center gap-3 py-16 text-center">
              <div className="w-12 h-12 rounded-md bg-surface-2 flex items-center justify-center">
                <Store size={20} className="text-dim" />
              </div>
              <p className="text-body text-text-secondary">
                {search ? t('skills.noMatch', '没有匹配的技能') : t('skills.marketEmpty', '技能市场暂无内容')}
              </p>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {filteredCatalog.map((skill, i) => (
                <CatalogSkillCard
                  key={skill.id}
                  skill={skill}
                  installed={skill.installed}
                  isInstalling={installingIds.has(skill.id)}
                  onInstall={handleInstall}
                  onView={(id) => navigate({ page: 'skillWorkshop', params: { draftId: id } })}
                  index={i}
                />
              ))}
            </div>
          )}
        </>
      )}

      {tab === 'health' && (
        <>
          {healthLoading ? (
            <div className="py-8 text-center text-small text-dim">{t('skills.checking', '检查中...')}</div>
          ) : healthItems ? (
            <HealthCheckResult items={healthItems} />
          ) : null}
        </>
      )}

      {toast && <Toast msg={toast.msg} type={toast.type} />}
    </PageShell>
  )
}
