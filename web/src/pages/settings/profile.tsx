import { useState, useEffect, useMemo } from 'react'
import { ProfileSummary } from '@wesui/profile'
import type {
  RadarData,
  TrendPoint,
  Suggestion,
  ProfileSummaryLabels,
} from '@wesui/profile'
import { tk } from '@wesui'
import { request } from '@/bridge'
import { useTranslation } from '@/lib/i18n'

/** weisyn 平台 API 响应信封 */
interface ApiEnvelope<T> {
  code: number
  data: T
}

/** /summary 聚合响应 */
interface SummaryPayload {
  radar: RadarData
  trends: TrendPoint[]
  suggestions: Suggestion[]
}

function unwrap<T>(raw: ApiEnvelope<T>): T {
  return raw.data
}

/** 维度标签（tk 表驱动，闸门可静态发现 key） */
const DIM_LABELS = {
  ai_collaboration: tk('profile.dim.ai_collaboration', 'AI 协作'),
  code_quality: tk('profile.dim.code_quality', '代码质量'),
  problem_definition: tk('profile.dim.problem_definition', '问题定义'),
  engineering_depth: tk('profile.dim.engineering_depth', '工程深度'),
  domain_breadth: tk('profile.dim.domain_breadth', '领域广度'),
  delivery_effectiveness: tk('profile.dim.delivery_effectiveness', '交付效能'),
}

/** 子指标标签，键必须与 Go scorer.go 的 sub-indicator name 一一对齐 */
const SUB_LABELS = {
  // ai_collaboration
  tokens_per_turn: tk('profile.sub.tokens_per_turn', '每轮 Token 量'),
  cache_hit_rate: tk('profile.sub.cache_hit_rate', '缓存命中率'),
  thinking_ratio: tk('profile.sub.thinking_ratio', '思考比率'),
  turns_per_run: tk('profile.sub.turns_per_run', '每次运行轮次'),
  // code_quality
  l1_pass_rate: tk('profile.sub.l1_pass_rate', 'L1 通过率'),
  l2_pass_rate: tk('profile.sub.l2_pass_rate', 'L2 通过率'),
  revision_efficiency: tk('profile.sub.revision_efficiency', '修订效率'),
  error_streak_avoidance: tk('profile.sub.error_streak_avoidance', '错误连续规避'),
  // problem_definition
  cycle_avoidance: tk('profile.sub.cycle_avoidance', '循环规避'),
  plan_completion: tk('profile.sub.plan_completion', '计划完成率'),
  first_turn_success: tk('profile.sub.first_turn_success', '首轮成功率'),
  graceful_end: tk('profile.sub.graceful_end', '正常结束率'),
  // engineering_depth
  tool_diversity: tk('profile.sub.tool_diversity', '工具多样性'),
  edit_precision: tk('profile.sub.edit_precision', '编辑精确度'),
  exploration_ratio: tk('profile.sub.exploration_ratio', '探索比率'),
  delegation: tk('profile.sub.delegation', '委派使用率'),
  // domain_breadth
  model_diversity: tk('profile.sub.model_diversity', '模型多样性'),
  agent_diversity: tk('profile.sub.agent_diversity', 'Agent 多样性'),
  skill_diversity: tk('profile.sub.skill_diversity', '技能多样性'),
  workspace_breadth: tk('profile.sub.workspace_breadth', '工作区广度'),
  // delivery_effectiveness
  runs_per_week: tk('profile.sub.runs_per_week', '每周运行次数'),
  files_per_run: tk('profile.sub.files_per_run', '每次运行文件数'),
  success_rate: tk('profile.sub.success_rate', '成功率'),
  active_day_ratio: tk('profile.sub.active_day_ratio', '活跃天比例'),
}

export function ProfileSettings() {
  const { t } = useTranslation()

  const [radar, setRadar] = useState<RadarData | null>(null)
  const [trends, setTrends] = useState<TrendPoint[]>([])
  const [suggestions, setSuggestions] = useState<Suggestion[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)

    request<ApiEnvelope<SummaryPayload>>('sidebar/developerProfile', { view: 'summary' })
      .then(raw => {
        if (cancelled) return
        const data = unwrap(raw)
        setRadar(data.radar)
        setTrends(data.trends ?? [])
        setSuggestions(data.suggestions ?? [])
      })
      .catch(err => {
        if (cancelled) return
        setError(err?.message ?? t('profile.fetchFailed', '加载画像失败'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => { cancelled = true }
  }, [t])

  const labels = useMemo<ProfileSummaryLabels>(() => {
    const dimLabels: Record<string, string> = {}
    for (const [k, label] of Object.entries(DIM_LABELS)) {
      dimLabels[k] = t(label.k, label.zh)
    }
    const subLabels: Record<string, string> = {}
    for (const [k, label] of Object.entries(SUB_LABELS)) {
      subLabels[k] = t(label.k, label.zh)
    }
    return {
      overallLabel: t('profile.overallLabel', '综合评分'),
      suggestionsTitle: t('profile.suggestionsTitle', '成长建议'),
      trendTitle: t('profile.trendTitle', '评分趋势'),
      dimensionLabels: dimLabels,
      subScoreLabels: subLabels,
      suggestionLabels: {
        empty: t('profile.noSuggestions', '暂无建议'),
        priorityHigh: t('profile.priorityHigh', '高'),
        priorityMedium: t('profile.priorityMedium', '中'),
        priorityLow: t('profile.priorityLow', '低'),
        actionLabel: t('profile.actionLabel', '行动'),
      },
    }
  }, [t])

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-dim text-small">
        {t('profile.loading', '加载中…')}
      </div>
    )
  }

  if (error || !radar) {
    return (
      <div className="flex items-center justify-center py-12 text-dim text-small">
        {error ?? t('profile.empty', '暂无画像数据')}
      </div>
    )
  }

  return (
    <ProfileSummary
      radar={radar}
      suggestions={suggestions}
      trends={trends}
      labels={labels}
    />
  )
}
