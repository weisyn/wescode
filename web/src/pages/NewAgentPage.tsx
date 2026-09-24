import { useState, useCallback } from 'react'
import { useTranslation } from '@/lib/i18n'
import { useNav } from '@/lib/nav'
import { Bot } from 'lucide-react'
import { AgentIdentityFields, SuggestionChipsEditor } from '@wesui/agent'
import { Button } from '@wesui/primitives'
import { PageShell } from '@wesui/layout'
import { Toast, useToast } from '@/components/ui/Toast'
import { createAgent } from '@/lib/api/agents'

export function NewAgentPage() {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()

  const [name, setName] = useState('')
  const [role, setRole] = useState('')
  const [goal, setGoal] = useState('')
  const [description, setDescription] = useState('')
  const [systemPrompt, setSystemPrompt] = useState('')
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)

  const handleCreate = useCallback(async () => {
    if (!name.trim()) {
      showToast(t('agent.name_required', '请输入助手名称'), false)
      return
    }
    setSubmitting(true)
    try {
      const result = await createAgent({
        name: name.trim(),
        role: role.trim(),
        goal: goal.trim(),
        description: description.trim(),
        systemPrompt: systemPrompt.trim(),
        suggestions: suggestions.filter(Boolean),
      })
      navigate({ page: 'agentDetail', params: { agentId: result.id } })
    } catch (err: any) {
      showToast(err?.message ?? t('agent.create_failed', '创建失败'), false)
    } finally {
      setSubmitting(false)
    }
  }, [name, role, goal, description, systemPrompt, suggestions, navigate, showToast, t])

  return (
    <PageShell
      icon={<Bot size={16} />}
      title={t('agent.createAgent', '创建助手')}
      subtitle={t('agent.createAgent_desc', '配置一个新的 AI 助手')}
      onBack={() => navigate({ page: 'contacts' })}
      contentWidth="narrow"
    >
      <div className="space-y-6">
        <AgentIdentityFields
          name={name}
          role={role}
          goal={goal}
          description={description}
          onNameChange={setName}
          onRoleChange={setRole}
          onGoalChange={setGoal}
          onDescriptionChange={setDescription}
          showGoal
        />

        <div className="space-y-2">
          <label className="text-small font-medium text-text">{t('agent.system_prompt', '系统指令')}</label>
          <textarea
            value={systemPrompt}
            onChange={e => setSystemPrompt(e.target.value)}
            placeholder={t('agent.system_prompt_placeholder', '定义助手的行为准则和工作方式...')}
            className="w-full min-h-[120px] px-3 py-2 text-small rounded bg-surface-2 border border-border text-text placeholder:text-muted focus:outline-none focus:border-border-hi transition-colors duration-fast resize-y"
          />
        </div>

        <SuggestionChipsEditor
          suggestions={suggestions}
          onChange={setSuggestions}
        />

        <div className="flex gap-3 pt-4">
          <Button variant="primary" onClick={handleCreate} disabled={submitting || !name.trim()}>
            {submitting ? t('agent.creating', '创建中...') : t('agent.create_btn', '创建助手')}
          </Button>
          <Button variant="outline" onClick={() => navigate({ page: 'contacts' })}>
            {t('common.cancel', '取消')}
          </Button>
        </div>
      </div>
      {toast && <Toast msg={toast.msg} ok={toast.ok} />}
    </PageShell>
  )
}
