/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

/**
 * Lightweight bilingual helper for extension-side UI labels.
 *
 * Extension-side DOM components (ViewPane, chatViewPane, headerTabs) cannot
 * use the webview's react-i18next / wesui locale system.  VSCode's nls.localize()
 * is static (set at process start).  This module bridges the gap by providing a
 * dynamic `t(key)` resolver that reads from a shared locale ref.
 *
 * Usage:
 *   import { wescodeL, setWescodeLocale, type WescodeLocale } from './wescodeLocale.js';
 *   const label = wescodeL('new_session');
 */

export type WescodeLocale = 'zh-CN' | 'en';

let _locale: WescodeLocale = 'zh-CN';

export function setWescodeLocale(l: WescodeLocale): void { _locale = l; }
export function getWescodeLocale(): WescodeLocale { return _locale; }

const STRINGS: Record<string, Record<WescodeLocale, string>> = {
	// ── Chat panel ──
	new_session:          { 'zh-CN': '新会话',          en: 'New Chat' },
	search_sessions:      { 'zh-CN': '搜索会话…',       en: 'Search chats…' },
	chat_placeholder:     { 'zh-CN': '输入消息… (Enter 发送, Shift+Enter 换行)', en: 'Type a message… (Enter to send, Shift+Enter for newline)' },
	send:                 { 'zh-CN': '发送 (Enter)',     en: 'Send (Enter)' },
	stop_generate:        { 'zh-CN': '停止生成',         en: 'Stop generating' },
	generating:           { 'zh-CN': '生成中…',          en: 'Generating…' },
	collab_mode:          { 'zh-CN': '智能路由',         en: 'Smart Routing' },
	execution_plan:       { 'zh-CN': '执行计划',         en: 'Execution Plan' },
	close_tab:            { 'zh-CN': '关闭',             en: 'Close' },
	workspace_label:      { 'zh-CN': '工作区：',         en: 'Workspace: ' },
	pick_files:           { 'zh-CN': '选择文件或目录',   en: 'Pick files or directories' },
	loading:              { 'zh-CN': '消息区域加载中…',  en: 'Loading messages…' },
	empty_message:        { 'zh-CN': '消息内容为空',     en: 'Message is empty' },
	no_model:             { 'zh-CN': '未配置 LLM 模型，请先在设置中添加或登录 weisyn 账号',
	                        en: 'No LLM model configured. Please add one in Settings or sign in to weisyn.' },
	auth_required:        { 'zh-CN': '请先登录 weisyn 账号使用 AI 模型，或在设置中配置自有 API Key',
	                        en: 'Please sign in to weisyn to use AI models, or configure your own API Key in Settings.' },
	backend_failed:       { 'zh-CN': '后端连接失败',     en: 'Backend connection failed' },
	backend_restarting:   { 'zh-CN': '后端正在重启，请稍后重试', en: 'Backend is restarting, please try again shortly' },
	no_history:           { 'zh-CN': '暂无历史会话可切换', en: 'No conversation history' },
	pick_session:         { 'zh-CN': '选择历史会话',     en: 'Pick a conversation' },
	no_agents:            { 'zh-CN': '当前没有可选助手',  en: 'No agents available' },
	resume_plan:          { 'zh-CN': '继续执行计划',     en: 'Resume plan' },
	switch_session:       { 'zh-CN': '切换历史会话',     en: 'Switch conversation' },

	collab_mode_desc:     { 'zh-CN': 'AI 自动选择最合适的助手（默认）',
	                        en: 'AI automatically picks the best agent (default)' },
	plan_resume_label:    { 'zh-CN': '{0} ({1}/{2}) — 继续',
	                        en: '{0} ({1}/{2}) — resume' },

	// ── Toolbar ──
	browse_history:       { 'zh-CN': '浏览历史会话',     en: 'Browse history' },
	more:                 { 'zh-CN': '更多',             en: 'More' },

	// ── ViewContainer ──
	wes_manage:           { 'zh-CN': 'WES 管理',        en: 'WES Manager' },
};

/**
 * Resolve a bilingual key to the current locale's string.
 * Falls back to zh-CN if key exists but locale doesn't, or returns the key
 * itself if completely unknown (development guard).
 */
export function wescodeL(key: string, ...args: string[]): string {
	const entry = STRINGS[key];
	if (!entry) { return key; }
	let text = entry[_locale] ?? entry['zh-CN'] ?? key;
	for (let i = 0; i < args.length; i++) {
		text = text.replace(`{${i}}`, args[i]);
	}
	return text;
}
