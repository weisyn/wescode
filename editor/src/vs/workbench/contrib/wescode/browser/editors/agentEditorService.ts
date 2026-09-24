/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable, DisposableStore } from '../../../../../base/common/lifecycle.js';
import { Emitter, Event } from '../../../../../base/common/event.js';
import { URI } from '../../../../../base/common/uri.js';
import { setWescodeLocale, type WescodeLocale } from '../../common/wescodeLocale.js';
import { isAbsoluteFilePath } from '../../common/wescodePath.js';

import { IWebviewWorkbenchService } from '../../../webviewPanel/browser/webviewWorkbenchService.js';
import { ACTIVE_GROUP, IEditorService } from '../../../../services/editor/common/editorService.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';
import { rpcErrorPayload } from '../../../../../platform/wescode/common/failure.js';
import { WebviewInput } from '../../../webviewPanel/browser/webviewEditorInput.js';
import { asWebviewUri } from '../../../webview/common/webview.js';
import { createDecorator } from '../../../../../platform/instantiation/common/instantiation.js';
import { INativeWorkbenchEnvironmentService } from '../../../../services/environment/electron-sandbox/environmentService.js';
import { ILogService } from '../../../../../platform/log/common/log.js';
import { IFileService } from '../../../../../platform/files/common/files.js';
import { IWorkspaceContextService } from '../../../../../platform/workspace/common/workspace.js';
import { IOpenerService } from '../../../../../platform/opener/common/opener.js';
import { ICommandService } from '../../../../../platform/commands/common/commands.js';
import { IStorageService, StorageScope, StorageTarget } from '../../../../../platform/storage/common/storage.js';

export const IWescodeWebviewEditorService = createDecorator<IWescodeWebviewEditorService>('wescodeWebviewEditorService');

export type WebviewPage = 'contacts' | 'newAgent' | 'agentDetail' | 'provider' | 'skills' | 'skillWorkshop' | 'settings' | 'chat' | 'login' | 'about' | 'connections' | 'toolPolicy' | 'data' | 'files' | 'cron' | 'cronNew' | 'pageMcp' | 'pageBrowser' | 'pageDesktop' | 'pageChannels' | 'pageEmail' | 'pagePlugins' | 'pageMemory' | 'pageTokens' | 'pageLogs' | 'groupConversation' | 'projectOverview' | 'knowledge' | 'engineGovernance' | 'engineMemoryPolicy' | 'engineRunLimits' | 'engineCycleDetect' | 'engineContextBudget' | 'engineResilience';

export interface IWescodeOpenPageOptions {
	/** Open the tab without moving focus to it. Automatic opens (auth gate)
	 *  must set this: a page the user did not ask for must never pull them
	 *  out of the file they are typing in. User-initiated opens leave it off. */
	readonly preserveFocus?: boolean;
}

export interface IWescodeWebviewEditorService {
	readonly _serviceBrand: undefined;
	openPage(page: WebviewPage, params?: Record<string, unknown>, options?: IWescodeOpenPageOptions): void;
	closePage(page: WebviewPage): void;
	broadcastLocale(locale: string): void;
	readonly locale: string;
	readonly onDidLocaleChange: Event<string>;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type RpcHandler = (backend: IWescodeBackendService, params: any) => Promise<unknown>;

const RPC_REGISTRY: Record<string, RpcHandler> = {
	// Auth
	authMe:             (b)       => b.authMe(),
	authLogin:          (b, p)    => b.authLogin(p.email, p.password),
	authRegister:       (b, p)    => b.authRegister(p.email, p.password, p.code),
	authSendCode:       (b, p)    => b.authSendCode(p.email, p.purpose),
	authResetPassword:  (b, p)    => b.authResetPassword(p.email, p.code, p.newPassword),
	authLogout:         (b)       => b.authLogout(),
	authUpdateProfile:  (b, p)    => b.authUpdateProfile(p.displayName),
	authChangePassword: (b, p)    => b.authChangePassword(p.currentPassword, p.newPassword),
	authDeleteAccount:  (b, p)    => b.authDeleteAccount(p.currentPassword),
	// Agent
	listAgents:         (b)       => b.listAgents(),
	createAgent:        (b, p)    => b.createAgent(p),
	updateAgent:        (b, p)    => b.updateAgent(p.id, p),
	deleteAgent:        (b, p)    => b.deleteAgent(p.id),
	// Provider
	// availableModels is the single unified model list RPC (INV-PROVIDER-VIEW-01),
	// consumed by GateGuard.checkAuth and model pickers in Editor-tab pages.
	// It MUST be registered here — otherwise these pages hit
	// "Unknown RPC method: availableModels" (seen during the boot race) and
	// BYOK detection silently degrades to false.
	availableModels:    (b)       => b.sidebarRpc('sidebar/availableModels', {}),
	providerCatalog:    (b)       => b.sidebarRpc('providerCatalog', {}),
	listProviders:      (b)       => b.listProviders(),
	listWesProviders:   (b)       => b.listWesProviders(),
	getWesBilling:      (b)       => b.getWesBilling(),
	// Context
	'context/sources':  (b)       => b.contextSources(),
	'context/search':   (b, p)    => b.contextSearch(p),
	'context/resolve':  (b, p)    => b.contextResolve(p),
	addProvider:        (b, p)    => b.addProvider(p),
	updateProvider:     (b, p)    => b.updateProvider(p),
	deleteProvider:     (b, p)    => b.deleteProvider(p.name),
	setDefaultProvider: (b, p)    => b.setDefaultProvider(p.name),
	testProvider:       (b, p)    => b.testProvider(p),
	testProviderByName: (b, p)    => b.testProviderByName(p.name),
	testWesProvider:    (b, p)    => b.testWesProvider(p.name),
	// Skills
	listSkills:         (b)       => b.listSkills(),
	createSkill:        (b, p)    => b.createSkill(p.name),
	toggleSkill:        (b, p)    => b.toggleSkill(p.slug, p.enabled),
	deleteSkill:        (b, p)    => b.deleteSkill(p.slug),
	reloadSkills:       (b)       => b.reloadSkills(),
	// Conversations
	listConversations:  (b)       => b.listConversations(),
	// Presets
	listPresetAgents:   (b)       => b.listPresetAgents(),
	// Chat
	chat:               (b, p)    => b.chat(p),
	cancelChat:         (b)       => b.cancelChat(),
	hitlRespond:        (b, p)    => b.hitlRespond(p),
	// Run settings
	getRunSettings:     (b)       => b.getRunSettings(),
	updateRunSettings:  (b, p)    => b.updateRunSettings(p),
	// Memory has no entry here: the pages call `sidebar/*` names, which take the
	// generic passthrough in _dispatchRpc. A registry entry would only be needed
	// to rename or reshape params, and the layer-native surface needs neither.
};

export class WescodeWebviewEditorService extends Disposable implements IWescodeWebviewEditorService {
	declare readonly _serviceBrand: undefined;

	private readonly _editors = new Map<string, { input: WebviewInput; disposables: DisposableStore }>();

	private static readonly LOCALE_KEY = 'wescode.locale';
	private _locale: string = 'zh-CN';
	private readonly _onDidLocaleChange = this._register(new Emitter<string>());
	readonly onDidLocaleChange: Event<string> = this._onDidLocaleChange.event;

	get locale(): string { return this._locale; }

	constructor(
		@IWebviewWorkbenchService private readonly _webviewWorkbenchService: IWebviewWorkbenchService,
		@IWescodeBackendService private readonly _backend: IWescodeBackendService,
		@INativeWorkbenchEnvironmentService private readonly _environmentService: INativeWorkbenchEnvironmentService,
		@ILogService private readonly _logService: ILogService,
		@IEditorService private readonly _editorService: IEditorService,
		@IFileService private readonly _fileService: IFileService,
		@IWorkspaceContextService private readonly _workspaceService: IWorkspaceContextService,
		@IOpenerService private readonly _openerService: IOpenerService,
		@ICommandService private readonly _commandService: ICommandService,
		@IStorageService private readonly _storageService: IStorageService,
	) {
		super();
		const stored = this._storageService.get(WescodeWebviewEditorService.LOCALE_KEY, StorageScope.APPLICATION);
		if (stored === 'en' || stored === 'zh-CN') {
			this._locale = stored;
			setWescodeLocale(stored as WescodeLocale);
		}
	}

	private _tabKey(page: WebviewPage, params?: Record<string, unknown>): string {
		if (page === 'agentDetail' && params?.agentId) { return `agentDetail:${params.agentId}`; }
		if (page === 'projectOverview') { return 'projectOverview'; }
		if (page === 'skillWorkshop' && params?.draftId) { return `skillWorkshop:${params.draftId}`; }
		return page;
	}

	broadcastLocale(locale: string): void {
		for (const { input } of this._editors.values()) {
			input.webview.postMessage({ type: 'setLocale', locale });
		}
	}

	closePage(page: WebviewPage): void {
		const key = this._tabKey(page);
		const existing = this._editors.get(key);
		if (existing) {
			existing.input.dispose();
		}
	}

	openPage(page: WebviewPage, params?: Record<string, unknown>, options?: IWescodeOpenPageOptions): void {
		const title = this._pageTitle(page, params);
		const key = this._tabKey(page, params);
		const preserveFocus = options?.preserveFocus ?? false;

		const existing = this._editors.get(key);
		if (existing) {
			existing.input.setName(title);
			existing.input.webview.postMessage({ type: 'navigate', page, params: params ?? {} });
			this._webviewWorkbenchService.revealWebview(existing.input, ACTIVE_GROUP, preserveFocus);
			return;
		}

		const webDistUri = this._resolveWebDist();

		const input = this._webviewWorkbenchService.openWebview(
			{
				title,
				options: {
					tryRestoreScrollPosition: false,
					enableFindWidget: false,
				},
				contentOptions: {
					allowScripts: true,
					localResourceRoots: webDistUri ? [webDistUri] : [],
				},
				extension: undefined,
			},
			'wescode.webviewEditor',
			title,
			{ group: ACTIVE_GROUP, preserveFocus },
		);

		const disposables = new DisposableStore();

		disposables.add(input.webview.onMessage(e => {
			this._handleMessage(e.message, input);
		}));

		const agentNameCache = new Map<string, string>();
		let editorStreamAgentName = '';
		let listAgentsInflight: Promise<void> | undefined;
		disposables.add(this._backend.onDidStreamEvent(event => {
			if (event.agentId && !editorStreamAgentName) {
				const cached = agentNameCache.get(event.agentId);
				if (cached) {
					editorStreamAgentName = cached;
				} else {
					editorStreamAgentName = event.agentId.replace(/^builtin-/, '');
					if (!listAgentsInflight) {
						listAgentsInflight = this._backend.listAgents().then(agents => {
							for (const a of agents) { agentNameCache.set(a.id, a.name); }
							editorStreamAgentName = agentNameCache.get(event.agentId!) || editorStreamAgentName;
						}).finally(() => { listAgentsInflight = undefined; });
					}
				}
			}
			if (event.type === 'done' || event.type === 'interrupted') { editorStreamAgentName = ''; }
			const enriched = editorStreamAgentName
				? { ...event, agentName: editorStreamAgentName }
				: event;
			input.webview.postMessage({ type: 'stream', event: enriched });
		}));

		if (key === 'projectOverview') {
			disposables.add(this._backend.onDidIndexProgress(state => {
				input.webview.postMessage({ type: 'notification', method: 'codeintel/index-progress', params: state });
			}));
			disposables.add(this._backend.onDidIndexComplete(() => {
				input.webview.postMessage({ type: 'notification', method: 'codeintel/index-complete', params: {} });
			}));
			disposables.add(this._backend.onDidIndexError(state => {
				input.webview.postMessage({ type: 'notification', method: 'codeintel/index-error', params: state });
			}));
		}

		const broadcastWorkspaceState = () => {
			const hasFolders = this._workspaceService.getWorkspace().folders.length > 0;
			input.webview.postMessage({
				type: 'workspaceState',
				ready: true,
				hasWorkspace: hasFolders,
				configMode: !hasFolders,
			});
		};
		disposables.add(this._workspaceService.onDidChangeWorkspaceFolders(() => {
			broadcastWorkspaceState();
		}));
		disposables.add(this._backend.onDidEngineHealthChange((health) => {
			broadcastWorkspaceState();
			// Engine readiness must also re-arm Editor-tab webviews: their
			// GateGuard may have sampled authMe/availableModels during the boot
			// race and be stuck on Connecting (authKnown=false). authChanged
			// triggers a fresh checkAuth in every page — same recovery the chat
			// pane gets via chatViewPane.
			if (health && health.healthy) {
				input.webview.postMessage({ type: 'authChanged' });
			}
		}));
		// Not gated on the page key the way index progress is: the settings page
		// navigates between tabs inside this one webview, so a key test would be
		// a second copy of "which tab shows channels" and would drop the QR
		// payload the moment that answer changed. The webview only dispatches to
		// listeners the channels tab registered, and the backend only pushes
		// after that tab subscribed, so an idle page receives nothing.
		disposables.add(this._backend.onDidChannelEvent(event => {
			input.webview.postMessage({ type: 'channelEvent', event });
		}));

		let _lastAuthStatus = '';
		disposables.add(this._backend.onDidAuthStateChange((state) => {
			if (state.status !== _lastAuthStatus) {
				_lastAuthStatus = state.status;
				input.webview.postMessage({ type: 'authChanged' });
			}
		}));
		broadcastWorkspaceState();

		disposables.add(input.onWillDispose(() => {
			disposables.dispose();
			this._editors.delete(key);
		}));

		this._editors.set(key, { input, disposables });
		input.webview.setHtml(this._buildHtml(page, params));
	}

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	private async _handleMessage(msg: any, editor: WebviewInput): Promise<void> {
		const webview = editor.webview;

		if (msg.type === 'rpc') {
			const { id, method, params } = msg;
			try {
				const result = await this._dispatchRpc(method, params);
				webview.postMessage({ id, result });
			} catch (err) {
				webview.postMessage({ id, ...rpcErrorPayload(err) });
			}
			return;
		}

		if (msg.type === 'navigate') {
			if (msg.target === 'startChatWithAgent' && msg.params) {
				const agentId = (msg.params as Record<string, unknown>).agentId as string | undefined;
				const agentName = (msg.params as Record<string, unknown>).agentName as string | undefined;
				if (agentId) {
					this._backend.newConversation();
					const agents = await this._backend.listAgents();
					const agent = agents.find(a => a.id === agentId) ?? (agentName ? { id: agentId, name: agentName, role: '', goal: '', emoji: '', isBuiltin: false } : null);
					if (agent) {
						this._backend.setActiveAgent(agent);
					}
				}
				void this._commandService.executeCommand('workbench.panel.wescode.chat.focus');
			} else if (msg.target === 'openChatSession' && msg.params) {
				const sessionId = (msg.params as Record<string, unknown>).sessionId as string | undefined;
				const agentId = (msg.params as Record<string, unknown>).agentId as string | undefined;
				if (sessionId) {
					this._backend.switchConversation(sessionId, agentId);
				}
				void this._commandService.executeCommand('workbench.panel.wescode.chat.focus');
			} else if (msg.target === 'skillsChanged') {
				// Skill install/enable/disable notification — no-op on the editor side.
				// Chat pane refreshes visibleSkills on visibilitychange.
			} else if (msg.target) {
				this.openPage(msg.target as WebviewPage, msg.params as Record<string, unknown>);
			}
			return;
		}

		if (msg.type === 'openFile') {
			const filePath = typeof msg.path === 'string' ? msg.path : '';
			if (filePath) {
				void this._openFileInEditor(filePath, typeof msg.line === 'number' ? msg.line : undefined);
			}
			return;
		}

		if (msg.type === 'openFolder') {
			void this._commandService.executeCommand('workbench.action.files.openFolder');
			return;
		}

		if (msg.type === 'openExternal') {
			const url = typeof msg.url === 'string' ? msg.url : '';
			if (url && (url.startsWith('https://') || url.startsWith('http://'))) {
				void this._openerService.open(URI.parse(url), { openExternal: true });
			}
			return;
		}

		if (msg.type === 'localeChanged' && typeof msg.locale === 'string') {
			this._locale = msg.locale;
			setWescodeLocale(msg.locale as WescodeLocale);
			this._storageService.store(WescodeWebviewEditorService.LOCALE_KEY, msg.locale, StorageScope.APPLICATION, StorageTarget.USER);
			this._onDidLocaleChange.fire(msg.locale);
			this.broadcastLocale(msg.locale);
			this._refreshAllTabTitles();
			return;
		}

		if (msg.type === 'close') {
			editor.dispose();
			return;
		}
	}

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	private async _dispatchRpc(method: string, params: any): Promise<unknown> {
		const handler = RPC_REGISTRY[method];
		if (!handler) {
			// Generic passthrough for sidebar/* methods added by new pages
			// (memory, cron, MCP, files, data, etc.) without individual registry entries.
			if (method.startsWith('sidebar/') || method.startsWith('codeintel/') || method.startsWith('context/')) {
				return this._backend.sidebarRpc(method, params ?? {});
			}
			throw new Error(`Unknown RPC method: ${method}`);
		}
		return handler(this._backend, params);
	}

	private async _openFileInEditor(filePath: string, line?: number): Promise<void> {
		try {
			let resource: URI | undefined;

			if (isAbsoluteFilePath(filePath)) {
				resource = URI.file(filePath);
			} else {
				const candidates: URI[] = [];
				for (const f of this._workspaceService.getWorkspace().folders) {
					candidates.push(f.uri);
				}
				for (const base of candidates) {
					const candidate = URI.joinPath(base, filePath);
					try {
						await this._fileService.stat(candidate);
						resource = candidate;
						break;
					} catch {
						// not found, try next
					}
				}
				if (!resource && !filePath.includes('/')) {
					for (const base of candidates) {
						const found = await this._findFileRecursive(base, filePath);
						if (found) {
							resource = found;
							break;
						}
					}
				}
			}

			if (!resource) {
				this._logService.warn('[wescode-webview] openFile: file not found', filePath);
				return;
			}

			try {
				await this._fileService.stat(resource);
			} catch {
				this._logService.warn('[wescode-webview] openFile: file does not exist', resource.fsPath);
				return;
			}

			await this._editorService.openEditor({
				resource,
				options: line ? { selection: { startLineNumber: line, startColumn: 1 } } : undefined,
			});
		} catch (err) {
			this._logService.warn('[wescode-webview] openFile failed:', err);
		}
	}

	private async _findFileRecursive(base: URI, fileName: string, maxDepth = 8): Promise<URI | undefined> {
		const queue: { uri: URI; depth: number }[] = [{ uri: base, depth: 0 }];
		const skip = /^(node_modules|\.git|out|dist|build|vendor|__pycache__)$/;
		while (queue.length > 0) {
			const item = queue.shift()!;
			if (item.depth > maxDepth) { continue; }
			try {
				const stat = await this._fileService.resolve(item.uri);
				if (!stat.children) { continue; }
				for (const child of stat.children) {
					if (child.isDirectory) {
						if (!skip.test(child.name)) {
							queue.push({ uri: child.resource, depth: item.depth + 1 });
						}
					} else if (child.name === fileName) {
						return child.resource;
					}
				}
			} catch {
				// directory unreadable
			}
		}
		return undefined;
	}

	private static readonly PAGE_TITLES: Record<string, Record<string, string>> = {
		contacts:             { 'zh-CN': '我的助手',       en: 'My Agents' },
		newAgent:             { 'zh-CN': '创建助手',       en: 'Create Agent' },
		provider:             { 'zh-CN': 'LLM 模型配置',   en: 'LLM Model Config' },
		skills:               { 'zh-CN': '技能管理',       en: 'Skills' },
		skillWorkshop:        { 'zh-CN': '技能工作台',     en: 'Skill Workshop' },
		settings:             { 'zh-CN': '设置',           en: 'Settings' },
		chat:                 { 'zh-CN': '对话',           en: 'Chat' },
		login:                { 'zh-CN': '登录 WES Code',  en: 'Sign in to WES Code' },
		about:                { 'zh-CN': 'WES Code',       en: 'WES Code' },
		connections:          { 'zh-CN': '连接',           en: 'Connections' },
		toolPolicy:           { 'zh-CN': '工具与配额',     en: 'Tools & Quota' },
		data:                 { 'zh-CN': '记忆中心',       en: 'Data Center' },
		files:                { 'zh-CN': '知识图谱',       en: 'Code Graph' },
		projectOverview:      { 'zh-CN': '知识图谱',       en: 'Code Graph' },
		knowledge:            { 'zh-CN': '知识库',         en: 'Knowledge Base' },
		cron:                 { 'zh-CN': '定时任务',       en: 'Scheduled Tasks' },
		cronNew:              { 'zh-CN': '新建任务',       en: 'New Task' },
		pageMcp:              { 'zh-CN': 'MCP 工具',       en: 'MCP Tools' },
		pageBrowser:          { 'zh-CN': '浏览器自动化',   en: 'Browser Automation' },
		pageDesktop:          { 'zh-CN': '桌面自动化',     en: 'Desktop Automation' },
		pageChannels:         { 'zh-CN': 'IM 渠道',        en: 'IM Channels' },
		pageEmail:            { 'zh-CN': '邮件',           en: 'Email' },
		pagePlugins:          { 'zh-CN': '插件管理',       en: 'Plugins' },
		pageMemory:           { 'zh-CN': '记忆中心',       en: 'Data Center' },
		pageTokens:           { 'zh-CN': '用量统计',       en: 'Usage Stats' },
		pageLogs:             { 'zh-CN': '运行日志',       en: 'Run Logs' },
		engineGovernance:     { 'zh-CN': '治理策略',       en: 'Governance' },
		engineMemoryPolicy:   { 'zh-CN': '记忆策略',       en: 'Memory Policy' },
		engineRunLimits:      { 'zh-CN': '运行限制',       en: 'Run Limits' },
		engineCycleDetect:    { 'zh-CN': '循环检测',       en: 'Cycle Detection' },
		engineContextBudget:  { 'zh-CN': '上下文预算',     en: 'Context Budget' },
		engineResilience:     { 'zh-CN': '韧性与重试',     en: 'Resilience & Retry' },
		groupConversation:    { 'zh-CN': '群组对话',       en: 'Group Chat' },
		agentDetail:          { 'zh-CN': '助手详情',       en: 'Agent Detail' },
	};

	private _pageTitle(page: WebviewPage, params?: Record<string, unknown>): string {
		if (page === 'settings' && params?.tab === 'account') {
			return this._locale === 'en' ? 'Account' : '账号管理';
		}
		const entry = WescodeWebviewEditorService.PAGE_TITLES[page];
		if (!entry) { return 'wescode'; }
		return entry[this._locale] ?? entry['zh-CN'] ?? 'wescode';
	}

	private _refreshAllTabTitles(): void {
		for (const [key, { input }] of this._editors) {
			const page = key.includes(':') ? key.split(':')[0] : key;
			const title = this._pageTitle(page as WebviewPage);
			input.setName(title);
		}
	}

	private _resolveWebDist(): URI | undefined {
		// appRoot is the editor/ directory (e.g. /path/to/wescode.git/editor).
		// web/dist/ is at ../web/dist relative to editor/.
		try {
			const appRoot = this._environmentService.appRoot;
			this._logService.info(`[wescode-webview] appRoot: ${appRoot}`);

			const editorRoot = URI.file(appRoot);
			const distUri = URI.joinPath(editorRoot, '..', 'web', 'dist');
			this._logService.info(`[wescode-webview] distUri: ${distUri.fsPath}`);
			return distUri;
		} catch (err) {
			this._logService.error(`[wescode-webview] _resolveWebDist failed:`, err);
			return undefined;
		}
	}

	private _buildHtml(page: WebviewPage, params?: Record<string, unknown>): string {
		const webDistUri = this._resolveWebDist();
		if (!webDistUri) {
			this._logService.warn(`[wescode-webview] fallback: webDistUri not found`);
			return this._buildFallbackHtml();
		}

		const cssFileUri = URI.joinPath(webDistUri, 'assets', 'index.css');
		const jsFileUri = URI.joinPath(webDistUri, 'assets', 'index.js');
		const cssUri = asWebviewUri(cssFileUri, { isRemote: false, authority: '' });
		const jsUri = asWebviewUri(jsFileUri, { isRemote: false, authority: '' });

		this._logService.info(`[wescode-webview] cssUri: ${cssUri.toString()}`);
		this._logService.info(`[wescode-webview] jsUri: ${jsUri.toString()}`);

		const hasFolders = this._workspaceService.getWorkspace().folders.length > 0;
		const initData = JSON.stringify({ page, params: { ...(params ?? {}), workspaceReady: true, hasWorkspace: hasFolders, configMode: !hasFolders } });
		const currentLocale = this._locale;
		this._logService.info(`[wescode-webview] initData: ${initData}`);

		return /* html */`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${cssUri.toString()}">
<style>
html {
	-webkit-font-smoothing: antialiased;
	-moz-osx-font-smoothing: grayscale;
	text-rendering: optimizeLegibility;
}
body {
	font-family: var(--vscode-font-family, -apple-system, BlinkMacSystemFont, sans-serif);
	font-size: var(--vscode-font-size, 13px);
	background: var(--vscode-editor-background);
	color: var(--vscode-editor-foreground);
	margin: 0; padding: 0;
}
</style>
</head>
<body>
<div id="root"></div>
<script>
window.__WESCODE_INIT__=${initData};
window.__WESCODE_LOCALE__=${JSON.stringify(currentLocale)};
window.__VSCODE_API__=typeof acquireVsCodeApi==='function'?acquireVsCodeApi():undefined;
</script>
<script type="module" src="${jsUri.toString()}"></script>
</body>
</html>`;
	}

	private _buildFallbackHtml(): string {
		return /* html */`<!DOCTYPE html>
<html><body style="font-family:sans-serif;padding:24px;color:var(--vscode-editor-foreground);background:var(--vscode-editor-background)">
<p>Webview assets not found. Run <code>cd web && npm run build</code> first.</p>
</body></html>`;
	}
}
