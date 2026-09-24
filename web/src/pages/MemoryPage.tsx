import { useMemo, useState } from 'react'
import { Lightbulb, Upload } from 'lucide-react'
import { PageShell } from '@wesui/layout'
import { Button } from '@wesui/primitives'
import { MemoryCenter } from '@wesui/memory'
import type { MemoryAdapter, MemoryLayer, MemoryListOptions, WritableMemoryLayer } from '@wesui/memory'
import {
  clearMemoryLayer,
  deleteMemory,
  importMemory,
  listMemory,
  memoryCounts,
  searchMemory,
} from '@/lib/api/data'
import { listAgents } from '@/lib/api/agents'
import { useTranslation } from '@/lib/i18n'

export function MemoryPage() {
  const { t } = useTranslation()
  const [importOpen, setImportOpen] = useState(false)

  const adapter: MemoryAdapter = useMemo(() => ({
    list: async (opts: MemoryListOptions) => {
      const list = await listMemory(opts)
      return { entries: list, total: list.length }
    },
    search: async (query: string, layer?: MemoryLayer, limit?: number) => {
      const list = await searchMemory(query, layer, limit)
      return { entries: list, total: list.length }
    },
    // 一次请求，引擎按层分桶。旧版发四次 memoryStats() 再把 total_entries 拼成
    // 一张表，那张表最多只能有四个桶——about_me 与 consensus 同属 global scope，
    // 分不开，于是部门共识的条数一直被算进「关于我」。
    counts: () => memoryCounts(),
    delete: (id: string) => deleteMemory(id),
    import: (lines: string[], layer: WritableMemoryLayer, ns?: string) =>
      importMemory(lines, layer, ns).then(() => {}),
    clear: (layer: WritableMemoryLayer, ns?: string) => clearMemoryLayer(layer, ns).then(() => {}),
    listAgents: async () => {
      const agents = await listAgents()
      return agents.map(a => ({ id: a.id, name: a.name }))
    },
  }), [])

  return (
    <PageShell
      icon={<Lightbulb size={16} />}
      title={t('memory.title', '记忆中心')}
      subtitle={t('memory.subtitle', '助手在对话中学到的信息会出现在这里')}
      helpContent={{
        title: '记忆',
        description: 'AI 从交互中积累的长期知识，让 AI 越用越懂你。记忆按工作区隔离，不会跨项目混淆。',
        // 分组名与可删性都由 wesui MemoryCenter 决定（层判据只有一份），这里
        // 只解释每组是什么。写成「可随时编辑或删除」会许下两个界面兑现不了的
        // 承诺：没有编辑入口，删除也只在下面三组里出现。
        items: [
          { label: '你能改的', values: ['关于我：你的编码偏好和习惯', '角色记忆：每个 AI 助手各自积累的经验', '本地共识：这个工作区里所有助手共享的约定'] },
          { label: '只读的', values: ['环境信息：引擎感知到的工具链与项目约定', '会话记忆 / 工作缓存：运行时状态，随任务产生和回收'] },
          { label: '特性', values: ['对话中自动学习，无需手动配置', '空的分组不显示', '按工作区物理隔离'] },
        ],
      }}
      actions={
        <Button size="sm" variant="outline" onClick={() => setImportOpen(true)}>
          <Upload size={13} />
          {t('memory.import_btn', '导入记忆')}
        </Button>
      }
    >
      <MemoryCenter
        adapter={adapter}
        className="h-full overflow-y-auto"
        externalImport
        importOpen={importOpen}
        onImportOpenChange={setImportOpen}
      />
    </PageShell>
  )
}
