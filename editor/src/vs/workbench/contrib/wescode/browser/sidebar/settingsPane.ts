/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { IViewPaneOptions, ViewPane } from '../../../../browser/parts/views/viewPane.js';
import { IKeybindingService } from '../../../../../platform/keybinding/common/keybinding.js';
import { IContextMenuService } from '../../../../../platform/contextview/browser/contextView.js';
import { IConfigurationService } from '../../../../../platform/configuration/common/configuration.js';
import { IInstantiationService } from '../../../../../platform/instantiation/common/instantiation.js';
import { IViewDescriptorService } from '../../../../common/views.js';
import { IOpenerService } from '../../../../../platform/opener/common/opener.js';
import { IThemeService } from '../../../../../platform/theme/common/themeService.js';
import { ITelemetryService } from '../../../../../platform/telemetry/common/telemetry.js';
import { IHoverService } from '../../../../../platform/hover/browser/hover.js';
import { IContextKeyService } from '../../../../../platform/contextkey/common/contextkey.js';
import { ICommandService } from '../../../../../platform/commands/common/commands.js';
import { IWescodeWebviewEditorService, WebviewPage } from '../editors/agentEditorService.js';
import { DisposableStore } from '../../../../../base/common/lifecycle.js';
import { $, append, addDisposableListener, clearNode } from '../../../../../base/browser/dom.js';

export const SETTINGS_PANE_ID = 'wescode.settingsPane';

type MenuEntry =
	| { kind: 'item'; label: string; action: () => void }
	| { kind: 'separator' }
	| { kind: 'section'; label: string }
	| { kind: 'subitem'; label: string; action: () => void };

const LABELS: Record<string, Record<string, string>> = {
	settings: { 'zh-CN': '设置', en: 'Settings' },
	account: { 'zh-CN': '账号', en: 'Account' },
	appearance: { 'zh-CN': '外观', en: 'Appearance' },
	model_config: { 'zh-CN': '模型配置', en: 'Model Config' },
	tool_policy: { 'zh-CN': '工具与配额', en: 'Tools & Quota' },
	ai_engine: { 'zh-CN': 'AI 引擎', en: 'AI Engine' },
	governance: { 'zh-CN': '安全与边界', en: 'Safety & Boundaries' },
	memory_policy: { 'zh-CN': '记忆偏好', en: 'Memory Policy' },
	system_instructions: { 'zh-CN': '系统指令', en: 'System Instructions' },
	advanced: { 'zh-CN': '高级设置', en: 'Advanced' },
	cycle_detect: { 'zh-CN': '循环检测', en: 'Cycle Detect' },
	resilience: { 'zh-CN': '韧性与重试', en: 'Resilience' },
	connections: { 'zh-CN': '连接', en: 'Connections' },
	browser_auto: { 'zh-CN': '浏览器自动化', en: 'Browser Automation' },
	desktop_auto: { 'zh-CN': '桌面自动化', en: 'Desktop Automation' },
	im_channels: { 'zh-CN': 'IM 渠道', en: 'IM Channels' },
	email: { 'zh-CN': '邮件', en: 'Email' },
	mcp_tools: { 'zh-CN': 'MCP 工具', en: 'MCP Tools' },
	plugins: { 'zh-CN': '插件管理', en: 'Plugins' },
	data: { 'zh-CN': '数据', en: 'Data' },
	usage: { 'zh-CN': '用量统计', en: 'Usage' },
	run_logs: { 'zh-CN': '运行日志', en: 'Run Logs' },
	editor: { 'zh-CN': '编辑器', en: 'Editor' },
	preferences: { 'zh-CN': '偏好设置', en: 'Preferences' },
	keyboard_shortcuts: { 'zh-CN': '键盘快捷方式', en: 'Keyboard Shortcuts' },
	command_palette: { 'zh-CN': '命令面板', en: 'Command Palette' },
	snippets: { 'zh-CN': '代码片段', en: 'Snippets' },
	tasks: { 'zh-CN': '任务', en: 'Tasks' },
	theme: { 'zh-CN': '主题', en: 'Theme' },
	about: { 'zh-CN': '关于 WES Code', en: 'About WES Code' },
	developer_profile: { 'zh-CN': '开发者画像', en: 'Developer Profile' },
};

export class WescodeSettingsPane extends ViewPane {

	private _listEl: HTMLElement | undefined;
	private readonly _itemListeners = this._register(new DisposableStore());

	constructor(
		options: IViewPaneOptions,
		@IKeybindingService keybindingService: IKeybindingService,
		@IContextMenuService contextMenuService: IContextMenuService,
		@IConfigurationService configurationService: IConfigurationService,
		@IContextKeyService contextKeyService: IContextKeyService,
		@IViewDescriptorService viewDescriptorService: IViewDescriptorService,
		@IInstantiationService instantiationService: IInstantiationService,
		@IOpenerService openerService: IOpenerService,
		@IThemeService themeService: IThemeService,
		@ITelemetryService telemetryService: ITelemetryService,
		@IHoverService hoverService: IHoverService,
		@ICommandService private readonly commandService: ICommandService,
		@IWescodeWebviewEditorService private readonly webviewEditor: IWescodeWebviewEditorService,
	) {
		super(options, keybindingService, contextMenuService, configurationService, contextKeyService, viewDescriptorService, instantiationService, openerService, themeService, telemetryService, hoverService);

		this._register(this.webviewEditor.onDidLocaleChange(() => {
			this._renderItems();
		}));
	}

	private _t(key: string): string {
		const locale = this.webviewEditor.locale;
		return LABELS[key]?.[locale] ?? LABELS[key]?.['zh-CN'] ?? key;
	}

	private _openPage(page: WebviewPage, params?: Record<string, unknown>): void {
		this.webviewEditor.openPage(page, params);
	}

	private _cmd(id: string): () => void {
		return () => this.commandService.executeCommand(id);
	}

	private _buildMenu(): MenuEntry[] {
		return [
			{ kind: 'section', label: this._t('settings') },
			{ kind: 'subitem', label: this._t('account'), action: () => this._openPage('settings', { tab: 'account' }) },
			{ kind: 'subitem', label: this._t('appearance'), action: () => this._openPage('settings', { tab: 'appearance' }) },
			{ kind: 'subitem', label: this._t('model_config'), action: () => this._openPage('provider') },
			{ kind: 'subitem', label: this._t('tool_policy'), action: () => this._openPage('toolPolicy') },

			{ kind: 'section', label: this._t('ai_engine') },
			{ kind: 'subitem', label: this._t('governance'), action: () => this._openPage('engineGovernance') },
			{ kind: 'subitem', label: this._t('memory_policy'), action: () => this._openPage('engineMemoryPolicy') },
			{ kind: 'subitem', label: this._t('system_instructions'), action: () => this._openPage('engineContextBudget') },
			{ kind: 'subitem', label: this._t('advanced'), action: () => this._openPage('engineRunLimits') },
			{ kind: 'subitem', label: this._t('cycle_detect'), action: () => this._openPage('engineCycleDetect') },
			{ kind: 'subitem', label: this._t('resilience'), action: () => this._openPage('engineResilience') },

			{ kind: 'section', label: this._t('connections') },
			{ kind: 'subitem', label: this._t('browser_auto'), action: () => this._openPage('pageBrowser') },
			{ kind: 'subitem', label: this._t('desktop_auto'), action: () => this._openPage('pageDesktop') },
			{ kind: 'subitem', label: this._t('im_channels'), action: () => this._openPage('pageChannels') },
			{ kind: 'subitem', label: this._t('email'), action: () => this._openPage('pageEmail') },
			{ kind: 'subitem', label: this._t('mcp_tools'), action: () => this._openPage('pageMcp') },
			{ kind: 'subitem', label: this._t('plugins'), action: () => this._openPage('pagePlugins') },

			{ kind: 'section', label: this._t('data') },
			{ kind: 'subitem', label: this._t('developer_profile'), action: () => this._openPage('settings', { tab: 'profile' }) },
			{ kind: 'subitem', label: this._t('usage'), action: () => this._openPage('pageTokens') },
			{ kind: 'subitem', label: this._t('run_logs'), action: () => this._openPage('pageLogs') },

			{ kind: 'separator' },

			{ kind: 'section', label: this._t('editor') },
			{ kind: 'subitem', label: this._t('preferences'), action: this._cmd('workbench.action.openSettings') },
			{ kind: 'subitem', label: this._t('keyboard_shortcuts'), action: this._cmd('workbench.action.openGlobalKeybindings') },
			{ kind: 'subitem', label: this._t('command_palette'), action: this._cmd('workbench.action.showCommands') },
			{ kind: 'subitem', label: this._t('snippets'), action: this._cmd('workbench.action.openSnippets') },
			{ kind: 'subitem', label: this._t('tasks'), action: this._cmd('workbench.action.tasks.configureTaskRunner') },
			{ kind: 'subitem', label: this._t('theme'), action: this._cmd('workbench.action.selectTheme') },

			{ kind: 'separator' },

			{ kind: 'item', label: this._t('about'), action: () => this._openPage('about') },
		];
	}

	protected override renderBody(container: HTMLElement): void {
		super.renderBody(container);
		container.style.cssText = 'padding:4px 0;height:100%;overflow:hidden;';
		this._listEl = append(container, $('div.wescode-manage-list'));
		this._listEl.style.cssText = 'height:100%;overflow-y:auto;';
		this._renderItems();
	}

	private _renderItems(): void {
		if (!this._listEl) { return; }
		clearNode(this._listEl);
		this._itemListeners.clear();

		for (const entry of this._buildMenu()) {
			if (entry.kind === 'separator') {
				const sep = append(this._listEl, $('div'));
				sep.style.cssText = 'height:1px;margin:6px 12px;background:var(--vscode-menu-separatorBackground, var(--vscode-widget-border));';
			} else if (entry.kind === 'section') {
				const sec = append(this._listEl, $('div'));
				sec.textContent = entry.label;
				sec.style.cssText = 'padding:8px 12px 2px;font-size:11px;font-weight:600;text-transform:uppercase;letter-spacing:0.5px;color:var(--vscode-descriptionForeground);';
			} else {
				const indent = entry.kind === 'subitem';
				const row = append(this._listEl, $('div'));
				row.style.cssText = `display:flex;align-items:center;padding:5px ${indent ? '24px' : '12px'};cursor:pointer;border-radius:4px;margin:0 4px;`;
				this._itemListeners.add(addDisposableListener(row, 'mouseenter', () => { row.style.background = 'var(--vscode-list-hoverBackground)'; }));
				this._itemListeners.add(addDisposableListener(row, 'mouseleave', () => { row.style.background = ''; }));
				this._itemListeners.add(addDisposableListener(row, 'click', () => entry.action()));

				const label = append(row, $('span'));
				label.textContent = entry.label;
				label.style.cssText = 'font-size:13px;color:var(--vscode-foreground);';
			}
		}
	}

	protected override layoutBody(height: number, width: number): void {
		super.layoutBody(height, width);
	}
}
