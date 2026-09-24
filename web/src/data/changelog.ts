import { tk, type TLabel } from '@wesui'

/**
 * 更新日志。曾是 changelog.json，条目形如 `{ key }` 或 `{ text }` 二选一——
 * 渲染处因此要写 `'key' in item ? t(item.key) : item.text`，而带 key 的那一支是裸字面量：
 * 语言包闸门看不见它被用了（判成死键），也看不见兜底在哪。四条里有一条走 text 分支，
 * 它对应的键 about.changelogChat 就这样在语言包里躺成了死键。
 *
 * tk 把键与中文绑在一起，两个分支塌成一个。
 *
 * 这里只留最近若干个版本（渲染侧 VISIBLE_RELEASES 之外的要点一下才出现，再往前
 * 就没人翻了）。更早的版本不往下堆，归 releases/CHANGELOG.md——那份带构建状态与
 * 产物 SHA256，是发布记录；这份是给用户看"这次装的是什么"，两者受众不同。
 */
export interface Release {
  readonly version: string
  readonly date: string
  readonly items: readonly TLabel[]
}

export const changelog: readonly Release[] = [
  {
    version: '0.1.0-preview.4',
    date: '2026-09-24',
    items: [
      tk('about.changelogNetworkPolicy', '网页抓取遵守网络策略（「允许全部」/「仅内网」下可访问 localhost 与内网地址）'),
      tk('about.changelogMessageRetry', '会话消息写入冲突时自动重试（修复历史记录偶发缺消息）'),
    ],
  },
  {
    version: '0.1.0-preview.3',
    date: '2026-09-22',
    items: [
      tk('about.changelogReversibility', 'AI 编辑可回退（恢复点存在影子仓库，不污染项目 git）'),
      tk('about.changelogPresentation', '工具结果结构化呈现（调用图卡片替代纯文本）'),
      tk('about.changelogReachability', 'CKG 可达性分类（孤儿代码判定更准）'),
      tk('about.changelogDeveloperProfile', '开发者 AI 编程画像（设置页六维评分）'),
      tk('about.changelogDbMigration', '应用数据库迁移机制（升级不再清空本地配置）'),
    ],
  },
  {
    version: '0.1.0-preview.2',
    date: '2026-09-11',
    items: [
      tk('about.changelogPathNormalize', 'EditorState 路径归一化（修复 CKG 焦点加权失效）'),
      tk('about.changelogCrossPlatformShell', '跨平台 Shell 抽象（Windows cmd /c + POSIX sh -c）'),
      tk('about.changelogWindowsPath', 'Windows 三种绝对路径判定修正'),
      tk('about.changelogBuildFixes', '构建修正（Chat 输入 / benchmark / workspace 探测）'),
    ],
  },
  {
    version: '0.1.0-preview.1',
    date: '2026-09-07',
    items: [
      tk('about.changelogFirstPreview', '首个体验版（签名 + 公证 + DMG 安装界面）'),
      tk('about.changelogChat', 'AI Chat + Inline Diff + Ghost Text'),
      tk('about.changelogPlan', 'Plan 认知架构 + 多 Agent 委派'),
      tk('about.changelogSkills', '26 个预装编程技能'),
    ],
  },
]
