import { useState, useEffect, useCallback, type ReactNode } from 'react'
import { useTranslation } from '@/lib/i18n'
import { Sparkle, Save } from 'lucide-react'
import {
  SkillWorkshopLayout, SkillMdEditor, CompletenessBar, SkillSafetyPreview,
  DraftFileTree, CapabilityConstellation, PermissionOrbits, DraftCompatBanner,
} from '@wesui/skill'
import { parseSkillMd, patchMdPermissions } from '@wesui/skill'
import type { CompletenessData, CompatReport, DraftFile, SkillPermission } from '@wesui/skill'
import { Button, Toast, useToast } from '@wesui/primitives'
import { PageShell } from '@wesui/layout'
import { request, navigate as bridgeNavigate } from '@/bridge'
import { useNav } from '@/lib/nav'

async function fetchSkillContent(name: string): Promise<string> {
  const result = await request<{ content?: string }>('sidebar/getSkill', { name })
  return result?.content ?? ''
}

async function saveSkillContent(name: string, content: string): Promise<void> {
  await request<void>('sidebar/updateSkill', { name, content })
}

async function createNewSkill(name: string, content: string): Promise<string> {
  const result = await request<{ name?: string }>('sidebar/createSkillFull', { name, content })
  return result?.name ?? name
}

async function validateSkill(name: string, content?: string): Promise<CompatReport> {
  try {
    const result = await request<CompatReport>('sidebar/validateSkill', { name, content })
    return result ?? { status: 'compatible', issues: [] }
  } catch {
    return { status: 'compatible', issues: [] }
  }
}

async function fetchSkillResources(name: string): Promise<DraftFile[]> {
  try {
    const result = await request<Array<{ path: string; size: number; is_dir?: boolean }>>('sidebar/listSkillResources', { name })
    if (!Array.isArray(result)) return []
    return result
      .filter(r => !r.is_dir)
      .map(r => {
        const parts = r.path.split('/')
        const dir = parts.length > 1 ? parts[0] : ''
        let type: DraftFile['type'] = 'asset'
        if (dir === 'scripts') type = 'script'
        else if (dir === 'references') type = 'reference'
        return {
          name: parts[parts.length - 1],
          path: r.path,
          type,
          size: r.size > 1024 ? `${(r.size / 1024).toFixed(1)}KB` : `${r.size}B`,
        }
      })
  } catch {
    return []
  }
}

async function uploadSkillFile(name: string, subDir: string, fileName: string, fileContent: ArrayBuffer): Promise<void> {
  const base64 = btoa(String.fromCharCode(...new Uint8Array(fileContent)))
  await request('sidebar/uploadSkillFile', { name, subDir, fileName, content: base64 })
}

async function repairSkill(name: string, content: string, issues: CompatReport['issues']): Promise<string> {
  const result = await request<{ repairedContent: string }>('sidebar/repairSkill', { name, content, issues })
  return result?.repairedContent ?? ''
}

function notifySkillsChanged(): void {
  bridgeNavigate('skillsChanged')
}

const DEFAULT_SKILL_MD = `---
name: new-skill
description: "描述该技能的目标和价值"
operators:
  - read
  - exec
capabilities: []
enabled: true
---

# 技能名称

> 一句话描述该技能的核心价值。

## 核心能力

- 能力一：具体描述
- 能力二：具体描述

## 使用场景

描述该技能适用的典型工作场景...

## 操作步骤

1. 第一步
2. 第二步
3. 第三步
`

function renderMarkdown(text: string): ReactNode {
  const lines = text.split('\n')
  const elements: ReactNode[] = []
  let i = 0
  let key = 0

  while (i < lines.length) {
    const line = lines[i]

    if (line.startsWith('```')) {
      const codeLines: string[] = []
      i++
      while (i < lines.length && !lines[i].startsWith('```')) {
        codeLines.push(lines[i])
        i++
      }
      i++
      elements.push(
        <pre key={key++} className="my-2 p-3 rounded bg-surface-2 overflow-x-auto">
          <code className="text-caption font-mono text-text-secondary">{codeLines.join('\n')}</code>
        </pre>
      )
      continue
    }

    if (line.startsWith('### ')) {
      elements.push(<h3 key={key++} className="text-h3 text-text mt-4 mb-1">{inlineFormat(line.slice(4))}</h3>)
    } else if (line.startsWith('## ')) {
      elements.push(<h2 key={key++} className="text-h2 text-text mt-5 mb-2">{inlineFormat(line.slice(3))}</h2>)
    } else if (line.startsWith('# ')) {
      elements.push(<h1 key={key++} className="text-h1 text-text mt-6 mb-2">{inlineFormat(line.slice(2))}</h1>)
    } else if (line.startsWith('> ')) {
      elements.push(
        <blockquote key={key++} className="border-l-2 border-accent pl-3 my-2 text-body text-text-secondary italic">
          {inlineFormat(line.slice(2))}
        </blockquote>
      )
    } else if (/^[-*] /.test(line)) {
      const listItems: string[] = [line.replace(/^[-*] /, '')]
      while (i + 1 < lines.length && /^[-*] /.test(lines[i + 1])) {
        i++
        listItems.push(lines[i].replace(/^[-*] /, ''))
      }
      elements.push(
        <ul key={key++} className="list-disc list-inside my-2 space-y-0.5 text-body text-text">
          {listItems.map((item, j) => <li key={j}>{inlineFormat(item)}</li>)}
        </ul>
      )
    } else if (/^\d+\. /.test(line)) {
      const listItems: string[] = [line.replace(/^\d+\. /, '')]
      while (i + 1 < lines.length && /^\d+\. /.test(lines[i + 1])) {
        i++
        listItems.push(lines[i].replace(/^\d+\. /, ''))
      }
      elements.push(
        <ol key={key++} className="list-decimal list-inside my-2 space-y-0.5 text-body text-text">
          {listItems.map((item, j) => <li key={j}>{inlineFormat(item)}</li>)}
        </ol>
      )
    } else if (line.trim() === '') {
      elements.push(<div key={key++} className="h-2" />)
    } else {
      elements.push(<p key={key++} className="text-body text-text leading-relaxed">{inlineFormat(line)}</p>)
    }
    i++
  }
  return <div className="space-y-0.5">{elements}</div>
}

function inlineFormat(text: string): ReactNode {
  const parts: ReactNode[] = []
  let remaining = text
  let k = 0
  const re = /(\*\*(.+?)\*\*)|(`([^`]+)`)|(\*(.+?)\*)/g
  let match: RegExpExecArray | null
  let lastIndex = 0

  while ((match = re.exec(remaining)) !== null) {
    if (match.index > lastIndex) {
      parts.push(remaining.slice(lastIndex, match.index))
    }
    if (match[2]) {
      parts.push(<strong key={k++}>{match[2]}</strong>)
    } else if (match[4]) {
      parts.push(<code key={k++} className="px-1 py-0.5 rounded bg-surface-2 text-caption font-mono">{match[4]}</code>)
    } else if (match[6]) {
      parts.push(<em key={k++}>{match[6]}</em>)
    }
    lastIndex = match.index + match[0].length
  }
  if (lastIndex < remaining.length) {
    parts.push(remaining.slice(lastIndex))
  }
  return parts.length === 1 && typeof parts[0] === 'string' ? parts[0] : <>{parts}</>
}

export function SkillWorkshopPage({ draftId }: { draftId?: string }) {
  const { t } = useTranslation()
  const navigate = useNav()
  const { toast, showToast } = useToast()

  const [content, setContent] = useState('')
  const [originalContent, setOriginalContent] = useState('')
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(!!draftId)
  const [compat, setCompat] = useState<CompatReport>({ status: 'compatible', issues: [] })
  const [rechecking, setRechecking] = useState(false)
  const [repairing, setRepairing] = useState(false)
  const [resourceFiles, setResourceFiles] = useState<DraftFile[]>([])
  const isNew = !draftId

  useEffect(() => {
    if (!draftId) {
      setContent(DEFAULT_SKILL_MD)
      setOriginalContent(DEFAULT_SKILL_MD)
      return
    }
    setLoading(true)
    fetchSkillContent(draftId)
      .then(c => { setContent(c); setOriginalContent(c) })
      .catch(() => showToast(t('skills.loadFailed', '加载失败'), 'error'))
      .finally(() => setLoading(false))
    fetchSkillResources(draftId).then(setResourceFiles)
  }, [draftId, t, showToast])

  useEffect(() => {
    if (draftId && !isNew) {
      validateSkill(draftId, content).then(setCompat)
    }
  }, [draftId, isNew])

  const handleRecheck = useCallback(async () => {
    if (!draftId) return
    setRechecking(true)
    try {
      const result = await validateSkill(draftId, content)
      setCompat(result)
    } finally {
      setRechecking(false)
    }
  }, [draftId, content])

  const handleSave = useCallback(async () => {
    setSaving(true)
    try {
      const parsed = parseSkillMd(content)
      const name = parsed?.name || draftId || 'new-skill'
      if (isNew) {
        const slug = await createNewSkill(name, content)
        showToast(t('skills.created', '技能已创建'))
        notifySkillsChanged()
        navigate({ page: 'skillWorkshop', params: { draftId: slug } })
      } else {
        await saveSkillContent(draftId!, content)
        setOriginalContent(content)
        showToast(t('skills.saved', '已保存'))
        notifySkillsChanged()
        handleRecheck()
      }
    } catch {
      showToast(t('skills.saveFailed', '保存失败'), 'error')
    } finally {
      setSaving(false)
    }
  }, [content, draftId, isNew, navigate, t, showToast, handleRecheck])

  const handleCopy = useCallback(async () => {
    await navigator.clipboard.writeText(content)
    showToast(t('skills.copied', '已复制'))
  }, [content, t, showToast])

  const handlePermToggle = useCallback((perm: SkillPermission) => {
    const parsed = parseSkillMd(content)
    const current = parsed?.permissions ?? []
    const next = current.includes(perm)
      ? current.filter(p => p !== perm)
      : [...current, perm]
    setContent(patchMdPermissions(content, next))
  }, [content])

  const handleUpload = useCallback(async (type: DraftFile['type']) => {
    if (!draftId || isNew) {
      showToast(t('skills.saveFirst', '请先保存技能后再上传文件'), 'error')
      return
    }
    const subDir = type === 'script' ? 'scripts' : type === 'reference' ? 'references' : 'assets'
    try {
      const input = document.createElement('input')
      input.type = 'file'
      input.multiple = true
      input.onchange = async () => {
        const files = input.files
        if (!files || files.length === 0) return
        for (const file of Array.from(files)) {
          const buffer = await file.arrayBuffer()
          await uploadSkillFile(draftId, subDir, file.name, buffer)
        }
        showToast(t('skills.uploaded', '文件已上传'))
        const updated = await fetchSkillResources(draftId)
        setResourceFiles(updated)
      }
      input.click()
    } catch {
      showToast(t('skills.uploadFailed', '上传失败'), 'error')
    }
  }, [draftId, isNew, t, showToast])

  const handleAIRepair = useCallback(async () => {
    if (!draftId) return
    setRepairing(true)
    try {
      const repaired = await repairSkill(draftId, content, compat.issues)
      if (repaired) {
        setContent(repaired)
        showToast(t('skills.repaired', 'AI 修复完成，请检查后保存'))
        handleRecheck()
      }
    } catch {
      showToast(t('skills.repairFailed', 'AI 修复失败'), 'error')
    } finally {
      setRepairing(false)
    }
  }, [draftId, content, compat.issues, t, showToast, handleRecheck])

  const parsed = parseSkillMd(content)
  const allFiles: DraftFile[] = [
    { name: 'SKILL.md', path: 'SKILL.md', type: 'md', size: `${(content.length / 1024).toFixed(1)}KB` },
    ...resourceFiles,
  ]
  const completeness: CompletenessData = {
    name: parsed?.name ?? '',
    description: parsed?.description ?? '',
    capabilities: parsed?.capabilities ?? [],
    permissions: (parsed?.permissions ?? []) as CompletenessData['permissions'],
    fileCount: allFiles.length,
    hue: 220,
  }

  const hasChanges = content !== originalContent

  const editor = (
    <div className="flex flex-col h-full">
      <SkillMdEditor
        skillMd={content}
        onChange={setContent}
        onCopy={handleCopy}
        renderMarkdown={renderMarkdown}
        className="flex-1 min-h-0"
      />
    </div>
  )

  const preview = (
    <div className="flex flex-col gap-4 p-4 overflow-y-auto">
      <CompletenessBar data={completeness} />

      <PermissionOrbits
        permissions={(parsed?.permissions ?? []) as SkillPermission[]}
        hue={220}
        onToggle={handlePermToggle}
      />

      {(parsed?.capabilities ?? []).length > 0 && (
        <CapabilityConstellation capabilities={parsed.capabilities} hue={220} />
      )}

      <DraftFileTree
        draftPath={draftId ?? 'new-skill'}
        files={allFiles}
        skillMdSize={`${(content.length / 1024).toFixed(1)}KB`}
        onUpload={handleUpload}
      />

      <SkillSafetyPreview compat={compat} content={content} />
    </div>
  )

  if (loading) {
    return (
      <div className="h-full flex items-center justify-center">
        <div className="w-8 h-8 rounded bg-surface-2 animate-pulse" />
      </div>
    )
  }

  return (
    <PageShell
      icon={<Sparkle size={16} />}
      title={isNew ? t('skills.newSkill', '新建技能') : (parsed?.name ?? draftId)}
      subtitle={isNew ? t('skills.workshop_desc', '创建和编辑自定义技能') : t('skills.editing', '编辑中')}
      onBack={() => navigate({ page: 'skills' })}
      contentWidth="standard"
      actions={
        <Button
          variant="primary"
          size="sm"
          onClick={handleSave}
          disabled={saving || (!hasChanges && !isNew)}
        >
          <Save size={14} className="mr-1" />
          {saving ? t('skills.saving', '保存中...') : t('skills.save', '保存')}
        </Button>
      }
    >
      {!isNew && (
        <DraftCompatBanner
          compat={compat}
          onAIRepair={handleAIRepair}
          onRecheck={handleRecheck}
          rechecking={rechecking || repairing}
          className="shrink-0"
        />
      )}

      <div className="min-h-[calc(100dvh-12rem)]">
        <SkillWorkshopLayout editor={editor} preview={preview} />
      </div>

      {toast && <Toast msg={toast.msg} type={toast.type} />}
    </PageShell>
  )
}
