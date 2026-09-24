/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import '../media/wescode.css';
import { $, append } from '../../../../../base/browser/dom.js';
import { IViewPaneOptions, ViewPane } from '../../../../browser/parts/views/viewPane.js';
import { IKeybindingService } from '../../../../../platform/keybinding/common/keybinding.js';
import { IContextMenuService } from '../../../../../platform/contextview/browser/contextView.js';
import { IConfigurationService } from '../../../../../platform/configuration/common/configuration.js';
import { IContextKeyService } from '../../../../../platform/contextkey/common/contextkey.js';
import { ICommandService } from '../../../../../platform/commands/common/commands.js';
import { IViewDescriptorService } from '../../../../common/views.js';
import { IInstantiationService } from '../../../../../platform/instantiation/common/instantiation.js';
import { IOpenerService } from '../../../../../platform/opener/common/opener.js';
import { IThemeService } from '../../../../../platform/theme/common/themeService.js';
import { ITelemetryService } from '../../../../../platform/telemetry/common/telemetry.js';
import { IHoverService } from '../../../../../platform/hover/browser/hover.js';
import { WESCODE_VIEW_PANE_ID, WESCODE_HAS_PENDING_DIFFS } from '../../common/types.js';
import { IAgentInfo, IConversationItem, IWescodeBackendService, IWescodeStreamEvent, IProviderItem, IWescodeDiagEntry, IEngineHealthState, isIdentityAuthenticated, isLoggedOut, isTerminalStreamEvent } from '../../../../../platform/wescode/common/wescode.js';
import { failureError, failureReasonOf, rpcErrorPayload } from '../../../../../platform/wescode/common/failure.js';
import { URI } from '../../../../../base/common/uri.js';
import { IWebviewService, IWebviewElement } from '../../../../contrib/webview/browser/webview.js';
import { asWebviewUri } from '../../../../contrib/webview/common/webview.js';
import { INativeWorkbenchEnvironmentService } from '../../../../services/environment/electron-sandbox/environmentService.js';
import { ILogService } from '../../../../../platform/log/common/log.js';
import { INotificationService } from '../../../../../platform/notification/common/notification.js';
import { mainWindow } from '../../../../../base/browser/window.js';
import { IQuickInputService, IQuickPickItem, QuickPickInput } from '../../../../../platform/quickinput/common/quickInput.js';
import { IFileDialogService } from '../../../../../platform/dialogs/common/dialogs.js';
import { IWescodeWebviewEditorService } from '../editors/agentEditorService.js';
import { IWorkspaceContextService, IWorkspaceFolder } from '../../../../../platform/workspace/common/workspace.js';
import { cellWorkDirForWorkspace } from '../../common/cellIdentity.js';
import { IEditorService } from '../../../../services/editor/common/editorService.js';
import { ICodeEditor, isCodeEditor } from '../../../../../editor/browser/editorBrowser.js';
import { IFileService } from '../../../../../platform/files/common/files.js';
import { InlineDiffController } from '../inline/inlineDiffController.js';
import { WescodeBufferSync } from '../bufferSync.js';
import { localize } from '../../../../../nls.js';
import { wescodeL } from '../../common/wescodeLocale.js';
import { wescodeChatTimestamp } from '../../common/wescodeFormat.js';
import { filePathFromUriString, isAbsoluteFilePath } from '../../common/wescodePath.js';
import { buildTopToolbar, type IMoreMenuItem } from './wescodeToolbar.js';
import { getPathForFile } from '../../../../../platform/dnd/browser/dnd.js';


export class WescodeChatViewPane extends ViewPane {

	static readonly ID = WESCODE_VIEW_PANE_ID;

	private searchInput!: HTMLInputElement;
	private _toolbarUpdateLocale?: () => void;
	private webviewContainer!: HTMLElement;

	private messagesWebview: IWebviewElement | undefined;
	private webviewReady = false;
	private pendingMessages: unknown[] = [];
	private conversations: IConversationItem[] = [];
	private activeSessionId = '';
	private _activeAgent: IAgentInfo | null = null;
	private isStreaming = false;
	private chatInFlight = false;
	private _activeStreamRequestId = '';
	private _lastSentText = '';
	private _lastSentFileIds: string[] | undefined;
	private _lastSentCodeContext: Array<{ filePath: string; startLine: number; endLine: number; code: string; language: string }> | undefined;
	private _lastSentMedia: Array<{ name: string; mimeType: string; dataUrl: string }> | undefined;
	private _lastSentContextItems: Array<{ id: string; sourceId: string; label?: string; detail?: string; data?: Record<string, unknown> }> | undefined;
	private _lastSentActivatedSkills: string[] | undefined;
	private _lastSentAgentId: string | null | undefined;
	private _lastSentGroupId: string | undefined;
	private _lastSentModelBinding: '' | 'preferred' | 'strict' | undefined;
	private _lastSentThinkingLevel: string | undefined;
	private selectedProviderId = '';
	private _selectedModel: string | undefined;
	private _locale: 'zh-CN' | 'en' = 'zh-CN';
	private _detailMode = false;

	private selectedWorkspaceFolder: IWorkspaceFolder | null = null;
	private _lastInitializedWorkDir: string = '';
	private _backendResolvedWorkDir: string = '';
	private _sessionWorkDir: string | undefined;
	private inlineDiffController: InlineDiffController;
	private readonly _hasPendingDiffsKey: import('../../../../../platform/contextkey/common/contextkey.js').IContextKey<boolean>;
	private _streamAgentName = '';
	private _agentNameCache = new Map<string, string>();
	private _messageCache = new Map<string, unknown[]>();
	private _loadGeneration = 0;
	private _resumeBanner!: HTMLElement;
	private _resumeBannerText!: HTMLSpanElement;
	private _incompletePlan: { total: number; completed: number; title: string } | null = null;
	private _planReferencedThisRun = false;
	/** INV-WS-11: bumped synchronously at send-entry so a stale `_sendChat`
	 *  finally cannot `_resetToIdle()` a newer turn that started after `done`. */
	private _sendGen = 0;


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
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IWebviewService private readonly webviewService: IWebviewService,
		@INativeWorkbenchEnvironmentService private readonly environmentService: INativeWorkbenchEnvironmentService,
		@ILogService private readonly logService: ILogService,
		@INotificationService private readonly notificationService: INotificationService,
		@IQuickInputService private readonly quickInputService: IQuickInputService,
		@IFileDialogService private readonly fileDialogService: IFileDialogService,
		@IWescodeWebviewEditorService private readonly webviewEditorService: IWescodeWebviewEditorService,
		@IWorkspaceContextService private readonly workspaceService: IWorkspaceContextService,
		@IEditorService private readonly editorService: IEditorService,
		@IFileService private readonly fileService: IFileService,
		@ICommandService private readonly _commandService: ICommandService,
	) {
		super(options, keybindingService, contextMenuService, configurationService, contextKeyService, viewDescriptorService, instantiationService, openerService, themeService, telemetryService, hoverService);

		this.inlineDiffController = this._register(instantiationService.createInstance(InlineDiffController));
		this._hasPendingDiffsKey = WESCODE_HAS_PENDING_DIFFS.bindTo(contextKeyService);

		this._register(this.webviewEditorService.onDidLocaleChange((locale) => {
			this._toolbarUpdateLocale?.();
			this.messagesWebview?.postMessage({ type: 'setLocale', locale });
		}));

		this._register(this.onDidChangeBodyVisibility(visible => {
			if (visible && this.pendingMessages.length > 0) {
				this._flushPendingMessages();
			}
		}));

		this._register(this.inlineDiffController.onDiffAction(async (e) => {
			try {
				if (e.action === 'accept') {
					this.inlineDiffController.acceptDiff(e.txId);
					await this.backend.editAccept(e.txId);
				} else if (e.action === 'reject') {
					const result = await this.backend.editReject(e.txId) as any;
					if (result && result.conflict) {
						this._postToWebview({ type: 'error', message: `Reject blocked: file was modified after agent edit. ${result.error ?? ''}` });
					} else {
						await this.inlineDiffController.rejectDiff(e.txId);
					}
				}
			} catch (err) {
				console.error('[wescode-chat] diff action failed', e.action, e.txId, err);
				if (e.action === 'reject') {
					await this.inlineDiffController.rejectDiff(e.txId);
				}
			}
			this._hasPendingDiffsKey.set(this.inlineDiffController.hasPendingDiffs());
		}));

		this._initWorkspaceFolder();

		this._register(this.workspaceService.onDidChangeWorkspaceFolders(() => {
			const folders = this.workspaceService.getWorkspace().folders;
			if (this.selectedWorkspaceFolder && !folders.some(f => f.uri.toString() === this.selectedWorkspaceFolder!.uri.toString())) {
				this.selectedWorkspaceFolder = this._detectActiveFolder() ?? null;
			}
			this._postToWebview({
				type: 'workspaceState',
				ready: true,
				hasWorkspace: folders.length > 0,
				configMode: folders.length === 0,
			});
			// Config→Cell transition changes RPC method reachability.
			// Reset the authChanged dedup so the webview re-runs checkAuth
			// even if the auth status string hasn't changed (e.g. the
			// new Cell-mode backend returns the same "authenticated" status
			// but availableModels is now reachable).
			_lastAuthStatus = '';
			this._postToWebview({ type: 'authChanged' });
		}));

		this._register(this.editorService.onDidActiveEditorChange(() => {
			if (this.isStreaming) { return; }
			const detected = this._detectActiveFolder();
			if (detected && detected.uri.toString() !== this.selectedWorkspaceFolder?.uri.toString()) {
				this.selectedWorkspaceFolder = detected;
			}
		}));

		this._register(backend.onDidActiveConversationChange(sessionId => {
			const prevSessionId = this.activeSessionId;
			// Dedup only for real sessions. An empty-session broadcast means the
			// user pressed "新建" (or switched to the empty tab) — always run the
			// reset path so the webview gets a visible clear even when we were
			// already on the empty tab (HeaderTabs no-ops no longer silently
			// swallow the click).
			if (sessionId !== '' && sessionId === prevSessionId) { return; }
			const wasStreaming = this.isStreaming && !prevSessionId && !!sessionId;
			this.activeSessionId = sessionId;
			if (this.searchInput) {
				this.searchInput.value = '';
			}
			if (wasStreaming) {
				// Session was just created by the backend during an active run.
				// Messages are already visible via stream events; skip reload.
				// Notify webview of the new sessionId so its session isolation check
				// doesn't reject stream events from this session.
				this._postToWebview({ type: 'sessionChange', sessionId });
	} else if (!sessionId) {
		if (this.isStreaming) {
			this._activeStreamRequestId = '';
			this._resetToIdle();
			this._postToWebview({ type: 'stream', event: { type: 'done' } as any });
			this.backend.cancelChat().catch(() => {});
		}
		this._incompletePlan = null;
		this._sessionWorkDir = undefined;
		this._updateResumeBanner();
		++this._loadGeneration; // D-RACE: invalidate in-flight loads
		this._postToWebview({ type: 'clearMessages' });
	} else {
			if (this.isStreaming) {
				this._activeStreamRequestId = '';
				this._resetToIdle();
				this._postToWebview({ type: 'stream', event: { type: 'done' } as any });
				this.backend.cancelChat().catch(() => {});
			}
			this._incompletePlan = null;
			this._updateResumeBanner();
		// Stale-while-revalidate: show cached messages instantly, then refresh.
		const cached = this._messageCache.get(sessionId);
			if (cached) {
				this._postToWebview({ type: 'loadMessages', messages: cached, sessionId });
			} else {
				this._postToWebview({ type: 'clearMessages' }); // D-STALE: clear old messages before loading
				this._postToWebview({ type: 'loading' });
			}
				void this._loadConversationMessages(sessionId);
			}
			// listConversations is handled by HeaderTabs._refreshFromBackend;
			// no need to duplicate the RPC here.
		}));

		this._register(backend.onDidActiveAgentChange(agent => {
			this._activeAgent = agent;
			this._postAgentInfo(agent);
		}));

		let _lastAuthStatus = '';
		this._register(backend.onDidAuthStateChange(state => {
			if (state.status !== _lastAuthStatus) {
				_lastAuthStatus = state.status;
				this._postToWebview({ type: 'authChanged' });
			}
			if (isIdentityAuthenticated(state.status)) {
				this._postToWebview({ type: 'providersChanged' });
			} else if (isLoggedOut(state.status)) {
				if (this.chatInFlight || this.isStreaming) {
					this.backend.cancelChat().catch(() => {});
					const reqId = this._activeStreamRequestId || undefined;
					this._activeStreamRequestId = '';
					this._resetToIdle();
					this._postToWebview({ type: 'stream', event: { type: 'done', requestId: reqId } as any });
				}
				// Clear chat messages on logout — the next user must not see
				// the previous user's conversation history.
				this._postToWebview({ type: 'clearMessages' });
			}
		}));

		this._register(backend.onDidProviderChange(() => {
			this._postToWebview({ type: 'providersChanged' });
		}));

		this._register(backend.onDidContextSnapshot((snapshot: any) => {
			this._postToWebview({ type: 'contextSnapshot', snapshot });
		}));

		this._register(backend.onDidStreamEvent((event: IWescodeStreamEvent) => {
			if (event.type === 'edit.preview' && event.editPreview) {
				this.inlineDiffController.showPendingDiff(event.editPreview);
				this._hasPendingDiffsKey.set(true);
			} else if (event.type === 'edit.applied' && event.editPreview) {
				if (event.editPreview.status === 'applied') {
					// INV-EDIT-01: confirmDiff is async (revert → decorate).
					void this.inlineDiffController.confirmDiff(event.editPreview);
				} else {
					this.inlineDiffController.failDiff(event.editPreview.txId);
				}
			}
			// Track requestId from the engine for synthetic done events.
			if (event.requestId && !this._activeStreamRequestId) {
				this._activeStreamRequestId = event.requestId;
			}
			// Resolve agentId → agentName once per stream (first event with agentId)
			if (event.agentId && !this._streamAgentName) {
				this._resolveStreamAgentName(event.agentId);
			}

			// ── Plan state tracking for Resume Banner ──
			this._trackPlanFromEvent(event);

			if (isTerminalStreamEvent(event)) {
				this._streamAgentName = '';
				this._activeStreamRequestId = '';
				// INV-WS-11: the user-visible turn ends at the terminal
				// event. Release the Pane streaming state immediately so
				// the user can send the next message while the backend
				// chat/send RPC tail (exploration paths / settlement) is
				// still finishing — previously only the RPC finally reset
				// idle, keeping "处理中…" and the send guard active for
				// minutes after `done` (2026-08-16 incident).
				this._resetToIdle();
				// Forward changeset summary to webview for bird's-eye view rendering.
				if ((event as any).changesetSummary) {
					this._postToWebview({
						type: 'changeset_summary',
						changeset: (event as any).changesetSummary,
					});
				}
			}
			const enriched = this._streamAgentName
				? { ...event, agentName: this._streamAgentName }
				: event;
			this._postToWebview({ type: 'stream', event: enriched });
		}));
	}

	protected override renderBody(container: HTMLElement): void {
		super.renderBody(container);

		container.classList.add('wescode-chat-panel');

		const activeAgent = this._activeAgent;

		// ─── Top Toolbar (wescodeToolbar.ts) ───
		const toolbarResult = buildTopToolbar(container, {
			onHistory: () => void this._pickConversation(),
			onSearch: (q) => this._searchInConversation(q),
			onClearSearch: () => this._clearSearch(),
			onMoreMenu: () => this._buildMoreMenuItems(),
		});
		this.searchInput = toolbarResult.searchInput;
		this._toolbarUpdateLocale = toolbarResult.updateLocale;
		this._toolbarUpdateLocale();

		const isConfigMode = () => !this.workspaceService.getWorkspace().folders.length;
		if (isConfigMode()) {
			toolbarResult.setDisabled(true);
		}
		this._register(this.backend.onDidEngineHealthChange((health: IEngineHealthState) => {
			toolbarResult.setDisabled(isConfigMode());
			if (health.healthy) {
				const cellWorkDir = this._cellWorkDir();
				if (cellWorkDir) {
					this._lastInitializedWorkDir = cellWorkDir;
				}
				// firstCheck is the watchdog's initial confirmation 60s after
				// boot, NOT a recovery from a real outage. The engine was
				// already serving normally for a full minute; replaying the
				// full initialization here would scroll the user back to the
				// bottom, clear any active search, and fire a burst of
				// redundant RPCs. Only a genuine unhealthy → healthy
				// transition needs the expensive reload path.
				if (health.firstCheck) {
					return;
				}
				// Real recovery: re-arm providers + auth (boot-race
				// GateGuard might have sampled stale state).
				this._postToWebview({ type: 'providersChanged' });
				this._postToWebview({ type: 'authChanged' });
				if (this.activeSessionId && this.webviewReady) {
					void this._loadConversationMessages(this.activeSessionId);
				}
			}
		}));
		// A scheduled run appends to a conversation with nobody streaming it, so
		// nothing tells an already-open panel that its transcript grew. The
		// messages were on disk and the panel kept showing the state from before
		// the task fired — indistinguishable, to the user, from the task never
		// having run. Only the displayed conversation is reloaded; a task that
		// wrote somewhere else is already covered by the notification.
		this._register(this.backend.onDidCronRunFinished(e => {
			if (e.sessionKey && e.sessionKey === this.activeSessionId && this.webviewReady) {
				void this._loadConversationMessages(this.activeSessionId);
			}
			// Forward to the webview so that CronPage (React SPA) can auto-refresh
			// without polling. The bridge dispatches on `type: 'notification'` +
			// `method`, same as codeintel/index-progress and engine/health.
			if (this.webviewReady) {
				this._postToWebview({ type: 'notification', method: 'cron/run-finished', params: e } as any);
			}
		}));

		this._register(this.workspaceService.onDidChangeWorkspaceFolders(() => {
			toolbarResult.setDisabled(isConfigMode());
		}));

		// Messages Webview (embedded)
		this.webviewContainer = append(container, $('.wescode-chat-webview'));
		this._createMessagesWebview();
		this._registerDropHandlers();

		// Pre-warm agent name cache
		void this.backend.listAgents().then(agents => {
			for (const a of agents) { this._agentNameCache.set(a.id, a.name); }
		}).catch(() => {});

		// Billing banner is now mounted inside the webview (above WescodeChatInput),
		// driven by `useBillingStore`. The native element has been removed.

		this._postAgentInfo(activeAgent);

		// If a session was already loaded before renderBody (race with HeaderTabs restore),
		// refresh banner state now that DOM is ready.
		if (this.activeSessionId && !this.isStreaming) {
			this._refreshPlanBannerFromBackend();
		}
	}

	protected override layoutBody(_height: number, _width: number): void {
		// WebviewElement is mounted via mountTo and participates in flex layout.
	}

	// ── Webview management ────────────────────────────────────────────────────

	private _createMessagesWebview(): void {
		try {
			this._tryCreateWebview();
		} catch (err) {
			this.logService.error('[wescode-chat] webview creation failed:', err);
			this._createFallbackMessages();
		}
	}

	private _createFallbackMessages(): void {
		this.webviewContainer.classList.add('wescode-chat-fallback');
		const notice = append(this.webviewContainer, $('div.wescode-chat-fallback-notice'));
		notice.textContent = wescodeL('loading');
	}

	private _tryCreateWebview(): void {
		const webDistUri = this._resolveWebDist();
		if (!webDistUri) {
			throw new Error('web dist not found');
		}

		this.messagesWebview = this.webviewService.createWebviewElement({
			title: 'wescode chat messages',
			options: {},
			contentOptions: {
				allowScripts: true,
				localResourceRoots: [webDistUri],
			},
			extension: undefined,
		});

		this.messagesWebview.mountTo(this.webviewContainer, mainWindow);

		this.messagesWebview.onMessage(e => {
			this._handleWebviewMessage(e.message);
		});

		const cssFileUri = URI.joinPath(webDistUri, 'assets', 'index.css');
		const jsFileUri = URI.joinPath(webDistUri, 'assets', 'index.js');
		const cssUri = asWebviewUri(cssFileUri, { isRemote: false, authority: '' });
		const jsUri = asWebviewUri(jsFileUri, { isRemote: false, authority: '' });

		const hasFolders = this.workspaceService.getWorkspace().folders.length > 0;
		const initData = JSON.stringify({ page: 'chatMessages', params: { workspaceReady: true, hasWorkspace: hasFolders, configMode: !hasFolders } });
		const currentLocale = this.webviewEditorService.locale;

		this.messagesWebview.setHtml(/* html */`<!DOCTYPE html>
<html lang="${currentLocale === 'en' ? 'en' : 'zh-CN'}" class="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${cssUri.toString()}">
<style>
html, body, #root { height: 100%; margin: 0; padding: 0; overflow: hidden; }
html {
	-webkit-font-smoothing: antialiased;
	-moz-osx-font-smoothing: grayscale;
	text-rendering: optimizeLegibility;
}
body {
	font-family: var(--vscode-font-family, -apple-system, BlinkMacSystemFont, sans-serif);
	font-size: var(--vscode-font-size, 13px);
	background: var(--vscode-editor-background, #1e1e1e);
	color: var(--vscode-editor-foreground);
}
/* Reset VS Code injected global code/pre styles — .prose-chat rules in globals.css
   already have higher specificity (.prose-chat pre) vs plain (pre), so they naturally win.
   Only override style variable injection that VS Code may add via editor.main.css. */
:not(.prose-chat) > code,
:not(.prose-chat) > pre {
	background: none;
	border: none;
}
/* Ensure .prose-chat rules are not contaminated by VS Code variable-backed defaults */
.prose-chat code { background-color: initial; }
.prose-chat pre { background-color: initial; }
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
</html>`);
	}

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	private _postToWebview(msg: any): void {
		if (!this.webviewReady) {
			
			this.pendingMessages.push(msg);
			return;
		}
		if (msg.type === 'stream') {
			
		}
		this.messagesWebview?.postMessage(msg);
	}

	private _flushPendingMessages(): void {
		const msgs = this.pendingMessages.splice(0);
		for (const msg of msgs) {
			this.messagesWebview?.postMessage(msg);
		}
	}

	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	private async _handleWebviewMessage(msg: any): Promise<void> {
		if (msg.type === 'ready') {
			this._diag('webview.ready', {});
			const wasReady = this.webviewReady;
			this.webviewReady = true;
			this._flushPendingMessages();
			if (this.activeSessionId && this.workspaceService.getWorkspace().folders.length > 0) {
				if (wasReady) {
					// Second ready: ChatMessages mounted after main.tsx (lazy
					// Suspense). The RPC already ran; re-post from the cache
					// that _loadConversationMessages populated so the newly
					// registered hostMessageListener gets the data without a
					// second round-trip. Without this, removing the duplicate
					// notifyReady() loses messages (hostMessageListeners has
					// no buffering), and keeping both calls hammers the backend
					// 3× in 0.7 ms.
					const cached = this._messageCache.get(this.activeSessionId);
					if (cached) {
						this._postToWebview({ type: 'loadMessages', messages: cached, sessionId: this.activeSessionId });
					}
				} else {
					this._loadConversationMessages(this.activeSessionId);
				}
			}
			return;
		}
		if (msg.type === 'diag') {
			// Forward webview-side diagnostic entries to the persistent log
			// file so they interleave with chatViewPane / backend service /
			// Go backend logs under a single timeline for cross-layer grep.
			const entry = msg.entry as IWescodeDiagEntry | undefined;
			if (entry) {
				try { this.backend.logDiag(entry); } catch { /* ignore */ }
			}
			return;
		}
		if (msg.type === 'rpc') {
			const { id, method, params } = msg;
			try {
				let result: unknown;
				if (method === 'hitlRespond') {
					await this.backend.hitlRespond(params);
					result = { ok: true };
				} else if (method === 'cancelChat') {
					await this.backend.cancelChat();
					result = { ok: true };
				} else if (method === 'editAccept') {
					const txId = params?.txId;
					if (txId) {
						this.inlineDiffController.acceptDiff(txId);
						await this.backend.editAccept(txId);
					}
					result = { ok: true };
			} else if (method === 'editReject') {
				const txId = params?.txId;
				if (txId) {
					const rpcResult = await this.backend.editReject(txId) as any;
					if (rpcResult && rpcResult.conflict) {
						this._postToWebview({ type: 'error', message: `Reject blocked: ${rpcResult.error ?? 'file modified by user'}` });
					} else {
						await this.inlineDiffController.rejectDiff(txId);
					}
				}
				result = { ok: true };
			} else if (method === 'editAcceptAll') {
				this.inlineDiffController.acceptAll();
				await this.backend.editAccept('*');
				result = { ok: true };
			} else if (method === 'editRejectAll') {
				const rpcResult = await this.backend.editReject('*') as any;
				if (rpcResult && rpcResult.conflict) {
					this._postToWebview({ type: 'error', message: `Reject blocked: ${rpcResult.error ?? 'file modified by user'}` });
				} else {
					await this.inlineDiffController.rejectAll();
				}
				result = { ok: true };
				} else if (method === 'insertCode') {
					const code = params?.code;
					if (typeof code === 'string' && code) {
						this._insertCodeAtCursor(code);
					}
					result = { ok: true };
			} else if (method === 'availableModels') {
				result = await this.backend.sidebarRpc('sidebar/availableModels', {});
			} else if (method === 'listProviders') {
				result = await this.backend.listProviders();
			} else if (method === 'listWesProviders') {
				result = await this.backend.listWesProviders();
			} else if (method === 'pickFiles') {
					// Honour the caller's request. The paperclip asks for files
					// only; a dialog that still offers folders returns a path
					// the attachment store cannot hash or copy.
					//
					// Cross-OS: these flags become Electron `properties`
					// verbatim (fileDialogService.showOpenDialog), and Electron
					// cannot combine `openFile` with `openDirectory` on Windows
					// or Linux — given both it shows a *directory* selector. So
					// `canSelectFolders: true` does not mean "files and folders"
					// there, it means "no files", and the paperclip would be
					// unusable on Windows while looking fine on macOS. Files-only
					// is the one request that means the same thing everywhere.
					const allowFolders = params?.allowFolders !== false;
					const uris = await this.fileDialogService.showOpenDialog({
						canSelectFiles: true,
						canSelectFolders: allowFolders,
						canSelectMany: params?.multiple !== false,
						title: typeof params?.title === 'string' && params.title ? params.title : wescodeL('pick_files'),
					});
					result = uris ? uris.map(u => u.fsPath) : [];
				} else if (method === 'importFiles') {
					// Webview paperclip / dropped-file rich import. Every file
					// (image or not) returns with a real fileId + MIME type; the
					// webview renders all of them as attachments (file_ref).
					// There is no image/non-image split — a PDF and a PNG are
					// both files first; vision expansion is engine-side.
					const paths: string[] = Array.isArray(params?.paths) ? params.paths : [];
					const items: Array<{ path: string; fileId: string; fileName: string; mimeType?: string; previewUrl?: string }> = [];
					const failed: Array<{ path: string; reason?: string }> = [];
					for (const p of paths) {
						try {
							const imported = await this.backend.importLocalFile(p);
							// A response without a fileId is a contract breach,
							// not an empty result: `fileIds` is the only thing
							// the send carries, so an item without one would
							// travel as a chip that attaches nothing. Fail here
							// where the path is still in hand and nameable.
							if (!imported?.fileId) {
								throw new Error(`files/importLocal returned no fileId (keys: ${Object.keys(imported ?? {}).join(',') || 'none'})`);
							}
							const isImage = WescodeChatViewPane._isImageAttachment(p);
							const mimeType = imported.mimeType || (isImage ? this._resolveImageMime(p) : undefined);
							const previewUrl = isImage ? await this._loadPreview(imported.fileId) : undefined;
							items.push({ path: p, fileId: imported.fileId, fileName: imported.fileName, mimeType, previewUrl });
						} catch (err) {
							failed.push({ path: p, reason: failureReasonOf(err) });
							this.logService.error('[wescode-chat] importFiles failed', p, err);
						}
					}
					// Attach gesture audit: every paperclip import lands one
					// structured event with the exact fileIds the send will
					// carry — this is the observable meeting point for
					// "image and PDF travel the same file_ref path".
					//
					// `failedCount` must be derived from the failures, not from
					// "did anything throw": the original defect produced items
					// that were counted as imported while carrying no id, so
					// this event reported failedCount 0 for a gesture that
					// attached nothing.
					this._diag('attach.importFiles', {
						count: items.length,
						files: items.map(it => ({ fileId: it.fileId, name: it.fileName, mimeType: it.mimeType ?? null })),
						failedCount: failed.length,
						failed: failed.map(f => ({ path: f.path, reason: f.reason ?? null })),
					});
					// Nothing imported and the backend named the condition: the
					// gesture failed as a whole, so hand the reason over instead
					// of an empty list. An empty list is read as "these files
					// could not be attached", which is true but useless when the
					// real answer is "the engine is still starting" — the store
					// is created in the async phase after `initialize` returns,
					// so a click in the first second legitimately lands early.
					const namedFailure = failed.find(f => f.reason)?.reason;
					if (items.length === 0 && namedFailure) {
						throw failureError(namedFailure, `files/importLocal failed: ${namedFailure}`);
					}
					result = items;
				} else if (method === 'openLocalFile') {
					// Audit #6: non-image attachments expose localPath from
					// history; clicking the chip opens the original file in
					// the system handler (IOpenerService → default app).
					const p: unknown = params?.path;
					if (typeof p === 'string' && isAbsoluteFilePath(p)) {
						await this.openerService.open(URI.file(p));
						result = { ok: true };
					} else {
						result = { ok: false, error: 'invalid local path' };
					}
				} else if (method === 'context/sources') {
					result = await this.backend.contextSources();
				} else if (method === 'context/search') {
					result = await this.backend.contextSearch(params);
				} else if (method === 'context/resolve') {
					result = await this.backend.contextResolve(params);
				} else if (method === 'authMe') {
					result = await this.backend.authMe();
				} else if (method === 'getWesBilling') {
					// Forwards to backend which proxies weisyn /api/v1/internal/status
					// + /api/billing/debts (see backend handler_provider.go).
					result = await this.backend.getWesBilling();
			} else if (method === 'visibleSkills') {
				result = await this.backend.sidebarRpc('sidebar/visibleSkills', params ?? {});
				} else if (method === 'listAgents') {
					result = await this.backend.listAgents();
				} else if (method === 'sidebar/listGroups') {
					result = await this.backend.sidebarRpc('sidebar/listGroups', {});
				} else if (method === 'setActiveAgent') {
					const agentId = params?.agentId;
					if (agentId) {
						const agents = await this.backend.listAgents() as IAgentInfo[];
						const target = agents.find(a => a.id === agentId);
						if (target) {
							this.backend.setActiveAgent(target);
						}
					} else {
						this.backend.setActiveAgent(null);
					}
					result = { ok: true };
				}
				this.messagesWebview?.postMessage({ id, result });
			} catch (err) {
				this.messagesWebview?.postMessage({ id, ...rpcErrorPayload(err) });
			}
		}
		if (msg.type === 'openFolder') {
			void this._commandService.executeCommand('workbench.action.files.openFolder');
			return;
		}
		if (msg.type === 'navigate') {
			if (msg.target === 'retryLastMessage') {
				if (!this.chatInFlight && this._lastSentText) {
					this._beginVisibleTurn();
					this._planReferencedThisRun = false;
					void this._sendChat(
						this._lastSentText,
						this._lastSentFileIds,
						this._lastSentCodeContext,
						undefined,
						undefined,
						this._lastSentActivatedSkills,
						this._lastSentContextItems,
						this._lastSentMedia,
						this._lastSentAgentId,
						this._lastSentModelBinding,
						undefined,
						this._lastSentGroupId,
						this._lastSentThinkingLevel,
					);
				}
			} else if (msg.target === 'provider' || msg.target === 'settings') {
				this.webviewEditorService.openPage('provider');
			} else if (msg.target === 'contacts') {
				this.webviewEditorService.openPage('contacts');
			} else if (msg.page) {
				this.webviewEditorService.openPage(msg.page, msg.params);
			}
			return;
		}
		if (msg.type === 'sendChat') {
			const payload = msg.payload;
			const traceId = payload?._traceId ?? this._newTraceId();
			this._diag('send.received', {
				textType: typeof payload?.text,
				textLen: typeof payload?.text === 'string' ? payload.text.length : 0,
				hasPayload: !!payload,
				isStreaming: this.isStreaming,
				chatInFlight: this.chatInFlight,
				activeSessionId: this.activeSessionId,
				hasAgentId: payload?.agentId !== undefined,
				providerId: payload?.providerId,
				modelBinding: payload?.modelBinding,
				mediaCount: payload?.media?.length ?? 0,
				contextItemsCount: payload?.contextItems?.length ?? 0,
			}, traceId);
			if (!payload || typeof payload.text !== 'string') {
				this._diag('send.REJECTED.bad_payload', {}, traceId);
				return;
			}
			// INV-WS-11 guard: a user-visible turn is still generating. The
			// webview may re-send before this Pane has seen the terminal
			// event (e.g. done delayed behind a slow RPC tail). Do not stack
			// a second _sendChat — its optimistic bubble would be orphaned
			// (backend / Electron would reject it). The legitimate path is:
			// terminal event → _resetToIdle → webview re-enables send.
			if (this.isStreaming) {
				this._diag('send.REJECTED.already_streaming', {
					textLen: payload.text.length,
				}, traceId);
				return;
			}
			this.selectedProviderId = payload.providerId ?? undefined;
			this._selectedModel = payload.model ?? undefined;
			this._beginVisibleTurn();
			this._planReferencedThisRun = false;
			this._lastSentText = payload.text;
			this._lastSentFileIds = payload.fileIds;
			this._lastSentCodeContext = payload.codeSnippets;
			this._lastSentMedia = payload.media;
			this._lastSentContextItems = payload.contextItems;
			this._lastSentActivatedSkills = payload.activatedSkills;
			this._lastSentAgentId = payload.agentId;
			this._lastSentGroupId = payload.groupId;
			this._lastSentModelBinding = payload.modelBinding;
			this._lastSentThinkingLevel = payload.thinkingLevel;
			// userMessage is added optimistically by the webview (WescodeChatInput
			// onSendOptimistic); do NOT post a second userMessage here — that
			// caused duplicate user bubbles (P0 fix-1).
			void this._sendChat(
				payload.text,
				payload.fileIds,
				payload.codeSnippets,
				payload.filePaths,
				undefined,
				payload.activatedSkills,
				// ContextPicker items from the webview
				payload.contextItems,
				// Inline media from paste/drop (images + non-image files).
				// Forwarded through to chat/send → MediaPayloadsToBlocks.
				payload.media,
				// Webview-authoritative agent selection (audit gap #3).
				// When the payload carries `agentId` (including explicit
				// null = collab mode), the run MUST use it instead of the
				// implicit `_activeAgent` state so a dropped setActiveAgent
				// RPC cannot cause UI/host divergence.
				payload.agentId,
				// Legacy observability tag (wesgine v1.0 removed the
				// tri-state fallback gate). Forwarded to backend RPC for
				// run-tag logging only.
				payload.modelBinding,
				traceId,
				// Group target (INV-ROUTE-01/02). Same authority argument as
				// agentId above: the webview owns the picker, so the run must
				// use what it sent rather than any host-side echo of it.
				payload.groupId,
				// Per-request thinking level override.
				payload.thinkingLevel,
			);
			return;
		}
		if (msg.type === 'stopChat') {
			if (this.chatInFlight) {
				void this.backend.cancelChat();
			} else {
				this._postToWebview({ type: 'stream', event: { type: 'done' } });
			}
			return;
		}
		if (msg.type === 'planState') {
			const plan = msg.plan as { total: number; completed: number; title: string } | null;
			this._incompletePlan = plan;
			this._updateResumeBanner();
		}
		if (msg.type === 'prefillInput') {
			const text = typeof msg.text === 'string' ? msg.text.trim() : '';
			if (!text) { return; }
			this._postToWebview({ type: 'prefillInput', text });
		}
		if (msg.type === 'openFile') {
			const filePath = typeof msg.path === 'string' ? msg.path : '';
			if (filePath) {
				void this._openFileInEditor(filePath, typeof msg.line === 'number' ? msg.line : undefined);
			}
		}
	}

	private async _openFileInEditor(filePath: string, line?: number): Promise<void> {
		try {
			let resource: URI | undefined;

			if (isAbsoluteFilePath(filePath)) {
				resource = URI.file(filePath);
			} else {
				// Ensure we have the backend's resolved workDir (project root).
				// This may not yet be set if the user clicks a file link before sending
				// the first message (e.g. viewing a historical session).
				if (!this._backendResolvedWorkDir) {
					try {
						const allFolders = this.workspaceService.getWorkspace().folders.map(f => f.uri.fsPath);
						const initResult = await this.backend.initialize(this._cellWorkDir(), allFolders);
						if (initResult?.workDir) {
							this._backendResolvedWorkDir = initResult.workDir;
						}
					} catch { /* ignore — will fall through to recursive search */ }
				}

				// Build search candidates: backend's resolved workDir first (the
				// actual project root the AI operates on), then IDE workspace folders.
				const candidates: URI[] = [];
				const backendWorkDir = this._backendResolvedWorkDir;
				if (backendWorkDir) {
					candidates.push(URI.file(backendWorkDir));
				}
				const ideWorkDir = this._getWorkDir();
				if (ideWorkDir && ideWorkDir !== backendWorkDir) {
					candidates.push(URI.file(ideWorkDir));
				}
				for (const f of this.workspaceService.getWorkspace().folders) {
					const fPath = f.uri.fsPath;
					if (fPath !== backendWorkDir && fPath !== ideWorkDir) {
						candidates.push(f.uri);
					}
				}
				for (const base of candidates) {
					const candidate = URI.joinPath(base, filePath);
					try {
						await this.fileService.stat(candidate);
						resource = candidate;
						break;
					} catch {
						// not found, try next
					}
				}
				// If simple join didn't find it and the path has no directory component,
				// search recursively in candidate roots for a matching filename.
				if (!resource && !filePath.includes('/')) {
					for (const base of candidates) {
						const found = await this._findFileRecursive(base, filePath);
						if (found) {
							resource = found;
							break;
						}
					}
				}

				if (!resource) {
					this.logService.warn('[wescode-chat] openFile: file not found in any workspace folder', filePath);
					return;
				}
			}

			try {
				await this.fileService.stat(resource);
			} catch {
				this.logService.warn('[wescode-chat] openFile: file does not exist', resource.fsPath);
				return;
			}
			await this.editorService.openEditor({
				resource,
				options: line ? { selection: { startLineNumber: line, startColumn: 1 } } : undefined,
			});
		} catch (err) {
			this.logService.warn('[wescode-chat] openFile failed:', err);
		}
	}

	private async _findFileRecursive(base: URI, fileName: string, maxDepth = 8): Promise<URI | undefined> {
		const queue: { uri: URI; depth: number }[] = [{ uri: base, depth: 0 }];
		const skip = /^(node_modules|\.git|out|dist|build|vendor|__pycache__)$/;
		while (queue.length > 0) {
			const item = queue.shift()!;
			if (item.depth > maxDepth) { continue; }
			try {
				const stat = await this.fileService.resolve(item.uri);
				if (!stat.children) { continue; }
				for (const child of stat.children) {
					if (child.isDirectory) {
						const name = child.name;
						if (!skip.test(name)) {
							queue.push({ uri: child.resource, depth: item.depth + 1 });
						}
					} else if (child.name === fileName) {
						return child.resource;
					}
				}
			} catch {
				// directory unreadable, skip
			}
		}
		return undefined;
	}

	private _insertCodeAtCursor(code: string): void {
		const control = this.editorService.activeTextEditorControl;
		if (!control || !isCodeEditor(control)) {
			return;
		}
		const editor = control as ICodeEditor;
		const selection = editor.getSelection();
		if (!selection) {
			return;
		}
		editor.executeEdits('wescode-chat-insert', [{
			range: selection,
			text: code,
			forceMoveMarkers: true,
		}]);
	}

	private _resolveWebDist(): URI | undefined {
		try {
			const appRoot = this.environmentService.appRoot;
			const editorRoot = URI.file(appRoot);
			return URI.joinPath(editorRoot, '..', 'web', 'dist');
		} catch (err) {
			this.logService.error('[wescode-chat] resolveWebDist failed:', err);
			return undefined;
		}
	}

	// ── Native DOM helpers ────────────────────────────────────────────────────

	private async _loadConversationMessages(sessionId: string): Promise<void> {
		const gen = ++this._loadGeneration;
		try {
			const rows = await this.backend.listConversationMessages(sessionId);
			if (gen !== this._loadGeneration) { return; }

			// Use cached agent names instead of a redundant listAgents() RPC.
			// _agentNameCache is populated by listAgents() calls elsewhere
			// (stream start, pickConversation, pickAgent, etc.).
			const agentNameById = this._agentNameCache;

			type ContentPart = { kind: string; text?: string; toolName?: string; status?: string; params?: unknown; result?: string; fileId?: string; fileName?: string };
			type Msg = { role: 'user' | 'assistant'; text: string; id?: string; timestamp?: number; agentName?: string; contentParts?: ContentPart[] };
			const messages: Msg[] = rows
				.filter(r => r.role === 'user' || r.role === 'assistant')
				.map(r => ({
					role: r.role as 'user' | 'assistant',
					text: r.content || '',
					id: r.id,
					timestamp: r.createdAt ? Date.parse(r.createdAt) : undefined,
					agentName: r.agentId ? agentNameById.get(r.agentId) : undefined,
					contentParts: r.contentParts as ContentPart[] | undefined,
				}));

		// Do not clobber a hydrated cache with an empty backend result.
		// A window that briefly rebound to a different Cell (INV-WS-05
		// regression) returns 0 messages for the original session ids;
		// overwriting here made every tab look empty after the first send.
		const cached = this._messageCache.get(sessionId);
		if (messages.length === 0 && Array.isArray(cached) && cached.length > 0) {
			this.logService.warn('[wescode-chat] ignoring empty reload; keeping cached messages', sessionId);
			if (gen !== this._loadGeneration) { return; }
			this._postToWebview({ type: 'loadMessages', messages: cached, sessionId });
			return;
		}

		this._messageCache.set(sessionId, messages);
		if (gen !== this._loadGeneration) { return; }
		this._postToWebview({ type: 'loadMessages', messages, sessionId });

			// Load authoritative plan state from .plans/ artifact (cross-run accurate).
			this.backend.getSessionPlan(sessionId).then(plan => {
				if (gen !== this._loadGeneration || !plan || !plan.steps?.length) { return; }
				this._postToWebview({ type: 'sessionPlan', plan });

				// Update Resume Banner from authoritative plan state
				const steps = plan.steps;
				const completedCount = steps.filter(s => s.status === 'completed' || s.status === 'done' || s.status === 'skipped').length;
				const total = steps.length;
				if (plan.status === 'completed' || completedCount >= total) {
					this._incompletePlan = null;
				} else {
					this._incompletePlan = { total, completed: completedCount, title: plan.title ?? wescodeL('execution_plan') };
				}
				this._updateResumeBanner();
			}).catch(() => { /* plan fetch is best-effort */ });

			// Load image previews asynchronously and patch them in.
			const previewTasks: Promise<void>[] = [];
			for (const msg of messages) {
				if (!msg.contentParts) { continue; }
				for (const p of msg.contentParts) {
					if (p.kind !== 'file_ref' || !p.fileId) { continue; }
					const isImage = /\.(png|jpg|jpeg|gif|webp|svg)$/i.test(p.fileName || '');
					if (!isImage) { continue; }
					const fid = p.fileId;
					previewTasks.push(
						this._loadPreview(fid).then(url => {
							if (url) {
								this._postToWebview({ type: 'imagePreview', fileId: fid, url });
							}
						}).catch(() => { /* skip */ })
					);
				}
			}
			void Promise.all(previewTasks);
	} catch (err) {
		if (gen !== this._loadGeneration) { return; }
		this.logService.warn('[wescode-chat] loadConversationMessages failed:', err);
		this._postToWebview({ type: 'loadError', error: err instanceof Error ? err.message : String(err) });
	}
	}

	private _resolveStreamAgentName(agentId: string): void {
		const cached = this._agentNameCache.get(agentId);
		if (cached) { this._streamAgentName = cached; return; }
		// Synchronous fallback: use active agent name or strip "builtin-" prefix
		const active = this.backend.getActiveAgent();
		if (active?.id === agentId) { this._streamAgentName = active.name; return; }
		this._streamAgentName = agentId.replace(/^builtin-/, '');
		// Async refresh for next time
		void this.backend.listAgents().then(agents => {
			for (const a of agents) { this._agentNameCache.set(a.id, a.name); }
			const resolved = this._agentNameCache.get(agentId);
			if (resolved) { this._streamAgentName = resolved; }
		});
	}

	private _postAgentInfo(agent: IAgentInfo | null): void {
		this._postToWebview({
			type: 'agentInfo',
			agent: agent ? {
				id: agent.id,
				name: agent.name,
				emoji: agent.emoji,
				role: agent.role,
				goal: agent.goal,
				suggestions: agent.suggestions ?? [],
			} : null,
		});
	}

	// ── Finder / OS drop on the webview container (native DOM) ───────────────
	// The chat webview is a cross-origin iframe: it cannot receive a non-empty
	// FileList for OS (Finder) drags, and Electron webviewElement cannot bubble
	// drag events to the parent. The container is the only layer that can
	// resolve real paths (webUtils.getPathForFile), so the drop is received
	// here and handed to the webview as DroppedFile[] — the webview turns every
	// file into an attachment (file_ref), never a context chip.

	private _registerDropHandlers(): void {
		this.webviewContainer.addEventListener('dragenter', this._onContainerDragEnter);
		this.webviewContainer.addEventListener('dragover', this._onContainerDragOver);
		this.webviewContainer.addEventListener('dragleave', this._onContainerDragLeave);
		this.webviewContainer.addEventListener('drop', this._onContainerDrop);
	}

	private _onContainerDragEnter = (e: DragEvent): void => {
		if (!e.dataTransfer) { return; }
		if (Array.from(e.dataTransfer.types).includes('Files')) {
			// OS file drag: disable the iframe so the drop lands on this
			// container (the layer that can resolve real paths).
			this._setWebviewPointerEvents(false);
			e.dataTransfer.dropEffect = 'copy';
			e.preventDefault();
		}
	};

	private _onContainerDragOver = (e: DragEvent): void => {
		if (!e.dataTransfer) { return; }
		if (Array.from(e.dataTransfer.types).includes('Files')) {
			e.dataTransfer.dropEffect = 'copy';
			e.preventDefault();
		}
	};

	private _onContainerDragLeave = (): void => {
		this._setWebviewPointerEvents(true);
	};

	private _setWebviewPointerEvents(enabled: boolean): void {
		const el = this.webviewContainer.querySelector('iframe') as HTMLElement | null;
		if (el) { el.style.pointerEvents = enabled ? '' : 'none'; }
	}

	private async _onContainerDrop(e: DragEvent): Promise<void> {
		this._setWebviewPointerEvents(true);
		if (!e.dataTransfer) { return; }
		e.preventDefault();

		const dropped: Array<{ name: string; mimeType?: string; path?: string; fileId?: string }> = [];

		// 1. OS files (Finder / desktop): resolve real paths.
		const files = Array.from(e.dataTransfer.files);
		for (const file of files) {
			const p = getPathForFile(file);
			if (p) {
				dropped.push({ name: file.name, mimeType: file.type || undefined, path: p });
			} else {
				// No path available — persist the body now and hand over the ID.
				try {
					const fileId = await this._persistDroppedFile(file);
					if (fileId) { dropped.push({ name: file.name, mimeType: file.type || undefined, fileId }); }
				} catch (err) {
					this.logService.warn('[wescode-chat] drop upload failed:', err);
				}
			}
		}

		// 2. URI list (workspace file dragged from explorer or editor tab).
		const uriList = e.dataTransfer.getData('text/uri-list');
		if (uriList) {
			for (const uri of uriList.split('\n').map(u => u.trim()).filter(u => u && !u.startsWith('#'))) {
				// filePathFromUriString also drops the slash the URI form leaves in
				// front of a drive letter: `file:///F:/x.go` decodes to `/F:/x.go`,
				// which passes any absolute-path test yet names nothing, so the
				// attachment reached the backend as an unopenable path.
				const filePath = filePathFromUriString(uri);
				if (isAbsoluteFilePath(filePath)) {
					dropped.push({ name: filePath.split(/[\\/]/).pop() ?? filePath, path: filePath });
				}
			}
		}

		if (dropped.length > 0) {
			this._postToWebview({ type: 'droppedFiles', files: dropped });
			this._diag('attach.drop', {
				count: dropped.length,
				files: dropped.map(d => ({
					name: d.name,
					mimeType: d.mimeType ?? null,
					fileId: d.fileId ?? null,
					hasPath: !!d.path,
				})),
			});
		}
	}

	private async _persistDroppedFile(file: File): Promise<string | undefined> {
		const buffer = await file.arrayBuffer();
		const bytes = new Uint8Array(buffer);
		let binary = '';
		for (let i = 0; i < bytes.length; i++) {
			binary += String.fromCharCode(bytes[i]);
		}
		const base64 = btoa(binary);
		const uploaded = await this.backend.uploadFile(file.name, base64);
		if (!uploaded?.fileId) {
			// Same contract as importLocal: without an id there is nothing for
			// `fileIds` to carry, so returning undefined here would hand the
			// webview a chip that attaches nothing.
			throw new Error(`files/upload returned no fileId (keys: ${Object.keys(uploaded ?? {}).join(',') || 'none'})`);
		}
		return uploaded.fileId;
	}

	// ── Plan Resume Banner ───────────────────────────────────────────────────

	private _trackPlanFromEvent(event: IWescodeStreamEvent): void {
		const type = event.type as string;
		const plan = event.plan as any;

		if (type.startsWith('plan_')) {
			this._planReferencedThisRun = true;
		}

		if (type === 'plan_created' || type === 'plan_replan') {
			const steps: any[] = plan?.steps ?? [];
			this._incompletePlan = {
				total: steps.length,
				completed: 0,
				title: plan?.title ?? this._incompletePlan?.title ?? wescodeL('execution_plan'),
			};
		} else if (type === 'plan_updated') {
			if (plan) {
				const status = plan.status as string;
				if (!this._incompletePlan) {
					// plan_created may have had 0 steps; create state on first step event
					this._incompletePlan = { total: 1, completed: 0, title: wescodeL('execution_plan') };
				}
				if (status === 'in_progress') {
					// A new step is running — at minimum, total >= completed + 1
					if (this._incompletePlan.total <= this._incompletePlan.completed) {
						this._incompletePlan.total = this._incompletePlan.completed + 1;
					}
				} else if (status === 'done' || status === 'completed' || status === 'skipped') {
					this._incompletePlan.completed++;
					// Ensure total is at least completed (may get more steps later)
					if (this._incompletePlan.total < this._incompletePlan.completed) {
						this._incompletePlan.total = this._incompletePlan.completed;
					}
				}
			}
		} else if (type === 'plan_completed') {
			this._incompletePlan = null;
			this._updateResumeBanner();
		} else if (type === 'plan_interrupted') {
			// Plan interrupted — keep state; done event will reveal the banner
		} else if (type === 'done') {
			// Run ended — fetch authoritative plan state to show accurate banner
			this._refreshPlanBannerFromBackend();
		}
	}

	private _updateResumeBanner(): void {
		if (!this._resumeBanner) { return; }

		const plan = this._incompletePlan;
		if (plan && plan.completed < plan.total && !this.isStreaming) {
			this._resumeBannerText.textContent = wescodeL('plan_resume_label', plan.title, String(plan.completed), String(plan.total));
			this._resumeBanner.style.display = 'flex';
		} else {
			this._resumeBanner.style.display = 'none';
		}
	}

	private _refreshPlanBannerFromBackend(): void {
		const sessionId = this.activeSessionId;
		if (!sessionId) { return; }
		// If the current Run didn't reference the plan (no plan_* events
		// received), clear the banner instead of re-loading the stale plan
		// from a previous Run. This prevents "0/6 old plan" from lingering
		// when the user has moved on to a different topic.
		if (!this._planReferencedThisRun) {
			this._incompletePlan = null;
			this._updateResumeBanner();
			return;
		}
		this.backend.getSessionPlan(sessionId).then(plan => {
			if (this.activeSessionId !== sessionId) { return; }
			if (!plan || !plan.steps?.length) {
				this._incompletePlan = null;
				this._updateResumeBanner();
				return;
			}
			const steps = plan.steps;
			const completedCount = steps.filter(s => s.status === 'completed' || s.status === 'done' || s.status === 'skipped').length;
			const total = steps.length;
			if (plan.status === 'completed' || completedCount >= total) {
				this._incompletePlan = null;
			} else {
				this._incompletePlan = { total, completed: completedCount, title: plan.title ?? wescodeL('execution_plan') };
			}
			this._updateResumeBanner();
		}).catch(() => { /* best-effort */ });
	}

	/** INV-WS-11: start a user-visible turn. Must run synchronously at the
	 *  send entry (before `void this._sendChat(...)`) so a stale finally
	 *  cannot observe the old `_sendGen` after the user already sent again. */
	private _beginVisibleTurn(): number {
		this._sendGen++;
		this.isStreaming = true;
		this.chatInFlight = true;
		return this._sendGen;
	}

	private _resetToIdle(): void {
		this.isStreaming = false;
		this.chatInFlight = false;
	}

	/**
	 * Chat pipeline diagnostic sink. Records into
	 * `~/.wescode/data/logs/wescode-chat.log` via the backend service.
	 * All chatViewPane / _sendChat / stream event handlers call this at
	 * every choke point to trace a message across layers.
	 */
	private _diag(event: string, data: Record<string, unknown> = {}, traceId?: string): void {
		try {
			const entry: IWescodeDiagEntry = {
				source: 'EXT-HOST',
				event,
				trace_id: traceId,
				...data,
			};
			this.backend.logDiag(entry);
		} catch { /* ignore */ }
	}

	private _newTraceId(): string {
		return `trc-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
	}

	/**
	 * Group inline media payloads by MIME category for the send.enter
	 * audit event — image / document(pdf) / other. Answers "what did the
	 * user attach" without persisting base64 payloads.
	 */
	private _groupMediaByType(media: Array<{ name: string; mimeType: string; dataUrl: string }>): Record<string, number> {
		const groups: Record<string, number> = { image: 0, document: 0, other: 0 };
		for (const m of media) {
			const mt = (m.mimeType || '').toLowerCase();
			if (mt.startsWith('image/')) {
				groups.image++;
			} else if (mt === 'application/pdf' || mt.includes('pdf')) {
				groups.document++;
			} else {
				groups.other++;
			}
		}
		return groups;
	}

	private async _reportCurrentEditorState(): Promise<void> {
		// Try active editor first; if the Chat panel is focused, fall back
		// to visible text editors (the code file is usually still visible
		// in a split or adjacent tab).
		let editor: ICodeEditor | undefined;
		const control = this.editorService.activeTextEditorControl;
		if (control && isCodeEditor(control)) {
			editor = control as ICodeEditor;
		}
		if (!editor) {
			for (const c of this.editorService.visibleTextEditorControls) {
				if (isCodeEditor(c)) {
					editor = c as ICodeEditor;
					break;
				}
			}
		}
		if (!editor) { return; }
		const model = editor.getModel();
		if (!model || model.uri.scheme !== 'file') { return; }
		const position = editor.getPosition();
		const selection = editor.getSelection();
		let selectionData: { startLine: number; startCol: number; endLine: number; endCol: number; text: string } | undefined;
		if (selection && !selection.isEmpty()) {
			selectionData = {
				startLine: selection.startLineNumber - 1,
				startCol: selection.startColumn - 1,
				endLine: selection.endLineNumber - 1,
				endCol: selection.endColumn - 1,
				text: model.getValueInRange(selection),
			};
		}
		await this.backend.reportEditorState({
			focusFile: model.uri.fsPath,
			cursorLine: position ? position.lineNumber - 1 : 0,
			cursorCol: position ? position.column - 1 : 0,
			selection: selectionData,
		}).catch(() => { /* ignore */ });
	}

	private async _sendChat(
		text: string,
		fileIds?: string[],
		codeContext?: Array<{ filePath: string; startLine: number; endLine: number; code: string; language: string }>,
		filePaths?: string[],
		resume?: boolean,
		activatedSkills?: string[],
		contextItems?: Array<{ id: string; sourceId: string; label?: string; detail?: string; data?: Record<string, unknown> }>,
		/**
		 * Inline media payloads (paste/drop images + files) from the
		 * webview. Forwarded to backend.chat → chat/send → ChatRequest.Media.
		 */
		media?: Array<{ name: string; mimeType: string; dataUrl: string }>,
		/**
		 * Explicit agent override from the webview send payload
		 * (audit gap #3). When defined it takes precedence over the implicit
		 * `_activeAgent` state so a dropped `setActiveAgent` RPC cannot cause
		 * the run to use a stale agent. Special values:
		 *   - `undefined` → no override, fall back to prior behaviour.
		 *   - `null`      → explicit collab mode (no agent).
		 *   - `<id>`      → force this agent for the current turn.
		 */
		explicitAgentId?: string | null,
		/**
		 * Legacy observability tag (wesgine v1.0 removed the tri-state
		 * fallback gate, T-10; brand-honoring lives on CellSpec
		 * AllowedModels + LogicalModelGroups). Written to run tags for
		 * request-level logging; never gates fallback.
		 */
		modelBinding?: '' | 'preferred' | 'strict',
		traceId?: string,
		/**
		 * Group target for this turn. Non-empty selects the engine's
		 * multi-agent group path; the backend resolves the member list and
		 * ignores `explicitAgentId`, because a group run has no single agent
		 * identity (INV-ROUTE-01/02).
		 */
		groupId?: string,
		/**
		 * Per-request thinking level override. When set, Go backend uses this
		 * instead of RunSettings global fallback ('low'|'medium'|'high'|'max'|'').
		 */
		thinkingLevel?: string,
	): Promise<void> {
		const trace = traceId ?? this._newTraceId();
		const gen = this._sendGen;
		this._diag('send.enter', {
			textLen: text.length,
			hasFileIds: !!fileIds?.length,
			fileIds,
			hasCodeContext: !!codeContext?.length,
			hasFilePaths: !!filePaths?.length,
			resume: !!resume,
			activatedSkills: activatedSkills?.length ?? 0,
			contextItemsCount: contextItems?.length ?? 0,
			mediaCount: media?.length ?? 0,
			mediaByType: media?.length ? this._groupMediaByType(media) : undefined,
			explicitAgentId,
			groupId,
			modelBinding,
		}, trace);
		try {
			// Flush pending buffer syncs before sending — ensures the backend
			// sees the latest editor content even during the debounce window.
			WescodeBufferSync.instance?.flushAll();
			this._diag('send.buffer_sync.done', {}, trace);

			// INV-WS-05: Cell identity is folders[0] (same lock as
			// WescodeEnginePreheater). Never initialize with the active
			// editor folder or a per-session workDir — those are exec CWD
			// hints, not a new Cell. Doing so in a multi-root workspace
			// spawned a second Go process and wiped every tab's history.
			const cellWorkDir = this._cellWorkDir();
			if (cellWorkDir && cellWorkDir !== this._lastInitializedWorkDir) {
				this._diag('send.initialize.needed', {
					currentWorkDir: cellWorkDir, lastInitializedWorkDir: this._lastInitializedWorkDir,
				}, trace);
				const allFolders = this.workspaceService.getWorkspace().folders.map(f => f.uri.fsPath);
				const initResult = await this.backend.initialize(cellWorkDir, allFolders);
				this._lastInitializedWorkDir = cellWorkDir;
				if (initResult?.error) {
					this._diag('send.initialize.ERROR', { error: initResult.error }, trace);
					this._postToWebview({
						type: 'stream',
						event: { type: 'error', text: initResult.error },
					});
					this._postToWebview({ type: 'stream', event: { type: 'done' } });
					return;
				}
				if (initResult?.workDir) {
					this._backendResolvedWorkDir = initResult.workDir;
				}
				this._diag('send.initialize.done', { resolvedWorkDir: initResult?.workDir }, trace);
			} else {
				this._diag('send.initialize.skip', { currentWorkDir: cellWorkDir }, trace);
			}
			await this._reportCurrentEditorState();
			this._diag('send.editor_state.reported', {}, trace);

			const providers = await this.backend.listProviders();
			const hasUsableProvider = providers.some((p: IProviderItem) => p.hasApiKey && p.name !== 'unconfigured');
			this._diag('send.providers.fetched', {
				providerCount: providers.length, hasUsableProvider,
			}, trace);
			if (!hasUsableProvider) {
				const wesProviders = await this.backend.listWesProviders();
				this._diag('send.wes_providers.fetched', {
					wesProviderCount: wesProviders.length,
				}, trace);
				if (wesProviders.length === 0) {
					this._diag('send.no_provider.EXIT', {}, trace);
					this._postToWebview({
						type: 'stream',
						event: { type: 'error', text: wescodeL('no_model') },
					});
					this._postToWebview({ type: 'stream', event: { type: 'done' } });
					this.webviewEditorService.openPage('provider');
					return;
				}
				const authStatus = await this.backend.authMe();
				const isAuth = isIdentityAuthenticated(authStatus?.status);
				this._diag('send.wes_auth_check', {
					authStatus: authStatus?.status, isAuth,
				}, trace);
				if (!isAuth) {
					this._diag('send.auth_required.defense_layer', {}, trace);
					this.logService.warn('[wescode] _sendChat: auth not passed but GateGuard should have blocked — proceeding as defense layer');
					this._postToWebview({
						type: 'stream',
						event: {
							type: 'error',
							text: wescodeL('auth_required'),
							error: { kind: 'auth_required', recoverable: false },
						},
					});
					this._postToWebview({ type: 'stream', event: { type: 'done' } });
					return;
				}
				if (!this.selectedProviderId) {
					const defaultWes = wesProviders.find(p => p.isDefault) || wesProviders[0];
					this.selectedProviderId = defaultWes.id;
					this._diag('send.wes_provider.fallback_selected', {
						providerId: defaultWes.id,
					}, trace);
				}
			}

			// WES billing pre-flight removed: the webview BillingDebtBanner gives
			// the user proactive notice, and the backend Run already returns a
			// surfaced error when billing is blocked. No need to round-trip RPC
			// here for every send.

			const mention = await this._resolveMentionAgent(text);
			const messageText = mention.text.trim();
			this._diag('send.mention.resolved', {
				hasMentionAgent: !!mention.agentId, messageTextLen: messageText.length,
			}, trace);
			if (!messageText) {
				this._diag('send.empty_message.EXIT', {}, trace);
				// P0 fix-4: webview already added optimistic userMessage; send
				// error+done so the UI doesn't hang with no response.
				this._postToWebview({
					type: 'stream',
					event: { type: 'error', text: wescodeL('empty_message') },
				});
				this._postToWebview({ type: 'stream', event: { type: 'done' } });
				this._resetToIdle();
				return;
			}
			// Agent resolution priority:
			//   1. Inline @mention in the text (mention.agentId) — always wins.
			//   2. Explicit payload override from the webview (audit gap #3).
			//   3. Implicit host state (this._activeAgent).
			// `explicitAgentId === null` = collab mode (no agent), distinct
			// from `undefined` = no override.
			const activeAgentId = this._activeAgent?.id;
			const explicitFromPayload = explicitAgentId === undefined
				? activeAgentId
				: (explicitAgentId ?? undefined);
			// A group turn names no single agent. Sending both lets the run's
			// target depend on which of the engine's two disagreeing
			// precedence rules the linked build implements
			// (engine/params.go doc vs internal/cell/boot.go dispatch order).
			// An inline @mention still wins — the engine's group coordinator
			// honours it by running only that member.
			const agentId = groupId ? mention.agentId : (mention.agentId || explicitFromPayload);
			const providerId = this.selectedProviderId || undefined;
			const fullMessage = messageText;
			const workDir = this._sessionWorkDir ?? this._getWorkDir();
			if (!this._sessionWorkDir && this.activeSessionId) {
				this._sessionWorkDir = workDir;
			}
			const folders = this.workspaceService.getWorkspace().folders;
			const allowPaths = folders
				.filter(f => f.uri.fsPath !== workDir)
				.map(f => f.uri.fsPath);

			this._diag('send.backend.chat.calling', {
				sessionId: this.activeSessionId,
				agentId,
				groupId,
				providerId,
				workDir,
				allowPathsCount: allowPaths.length,
			}, trace);
			const chatResult: any = await this.backend.chat({
				message: fullMessage,
				sessionId: this.activeSessionId || undefined,
				agentId,
				groupId,
				providerId,
				model: this._selectedModel || undefined,
				modelBinding,
				filePaths: filePaths && filePaths.length > 0 ? filePaths : undefined,
				fileIds,
				workDir,
				allowPaths: allowPaths.length > 0 ? allowPaths : undefined,
				codeSnippets: codeContext,
				resume,
				activatedSkills: activatedSkills && activatedSkills.length > 0 ? activatedSkills : undefined,
				contextItems: contextItems && contextItems.length > 0 ? contextItems : undefined,
				media: media && media.length > 0 ? media : undefined,
				thinkingLevel: thinkingLevel || undefined,
				_traceId: trace,
			});
			this._diag('send.backend.chat.returned', {
				sessionId: chatResult?.sessionId, runId: chatResult?.runId,
				ok: chatResult?.ok,
			}, trace);
			// INV-ERROR-ATTRIBUTION: user-visible errors arrive only via
			// stream.error (MapEvent → StreamError). ChatSendResult no longer
			// carries Error/ErrorHint — do not synthesize a second UI path.
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			this._diag('send.backend.chat.ERROR', { err: msg }, trace);
			// Backend process crash/restart (SIGTERM, SIGKILL, etc.) is transient —
			// the process manager will respawn it. Show a brief, friendly message
			// instead of raw technical details like "signal=SIGTERM".
			const isProcessExit = msg.includes('backend exited') || msg.includes('SIGTERM') ||
				msg.includes('SIGKILL') || msg.includes('code=null') || msg.includes('process died');
			const userMessage = isProcessExit
				? wescodeL('backend_restarting')
				: wescodeL('backend_failed') + ': ' + msg;
			this._postToWebview({
				type: 'stream',
				event: { type: 'error', text: userMessage, error: { text: userMessage, recoverable: true } },
			});
			this._postToWebview({
				type: 'stream',
				event: { type: 'done' },
			});
		} finally {
			this._diag('send.finally.reset', { gen, current: this._sendGen }, trace);
			// INV-WS-11: idle is owned by the stream terminal event. This
			// finally only resets if THIS call still owns the visible turn —
			// a newer send after `done` has already bumped `_sendGen`. An
			// unconditional reset here is the Pane sibling of the Electron
			// chatInFlight-finally race (stale RPC tail wiping the next turn).
			if (gen === this._sendGen) {
				this._resetToIdle();
			}
		}
	}

	private async _resolveMentionAgent(text: string): Promise<{ agentId?: string; text: string }> {
		if (!text.startsWith('@')) {
			return { text };
		}
		const agents = await this.backend.listAgents();
		const sorted = [...agents].sort((a, b) => b.name.length - a.name.length);
		for (const agent of sorted) {
			const prefix = `@${agent.name} `;
			if (!text.startsWith(prefix)) { continue; }
			const current = this._activeAgent;
			if (current?.id === agent.id) {
				return { text: text.slice(prefix.length) };
			}
			return { agentId: agent.id, text: text.slice(prefix.length) };
		}
		return { text };
	}

	// ── QuickPick: Conversation History ───────────────────────────────────────

	private _searchInConversation(query: string): void {
		this._postToWebview({ type: 'search', query });
	}

	private _clearSearch(): void {
		this._postToWebview({ type: 'search', query: '' });
	}

	private async _pickConversation(): Promise<void> {
		try {
			this.conversations = await this.backend.listConversations();
			if (this.conversations.length === 0) {
				this._notifyInteractionInfo(wescodeL('no_history'));
				return;
			}
			// Refresh agent name cache in background if stale; use existing cache for display.
			if (this._agentNameCache.size === 0) {
				const agents = await this.backend.listAgents();
				for (const a of agents) { this._agentNameCache.set(a.id, a.name); }
			}
			const agentNameByID = this._agentNameCache;

			// Group conversations by agent (or "智能路由" for no-agent sessions).
			const grouped = new Map<string, typeof this.conversations>();
			for (const c of this.conversations) {
				const key = c.agentId || '';
				if (!grouped.has(key)) { grouped.set(key, []); }
				grouped.get(key)!.push(c);
			}

			type PickItem = IQuickPickItem & { _sessionId?: string; _agentId?: string };
			const items: Array<QuickPickInput<PickItem>> = [];

			// Collaboration mode (no agent) first
			const collabConvs = grouped.get('');
			if (collabConvs && collabConvs.length > 0) {
				items.push({ type: 'separator', label: wescodeL('collab_mode') });
				for (const c of collabConvs) {
					items.push({
						label: `$(comment-discussion) ${c.title || wescodeL('new_session')}`,
						description: wescodeChatTimestamp(c.lastTime),
						detail: c.lastMessage || undefined,
						_sessionId: c.sessionId,
						_agentId: c.agentId,
						picked: this.activeSessionId === c.sessionId,
					});
				}
			}

			// Then each agent group
			for (const [agentId, convs] of grouped) {
				if (!agentId) { continue; }
				const agentName = agentNameByID.get(agentId) ?? agentId;
				items.push({ type: 'separator', label: `● ${agentName}` });
				for (const c of convs) {
					items.push({
						label: `$(comment-discussion) ${c.title || wescodeL('new_session')}`,
						description: wescodeChatTimestamp(c.lastTime),
						detail: c.lastMessage || undefined,
						_sessionId: c.sessionId,
						_agentId: c.agentId,
						picked: this.activeSessionId === c.sessionId,
					});
				}
			}

			const pick = await this.quickInputService.pick(items, {
				placeHolder: wescodeL('pick_session'),
			});
			if (!pick) { return; }
			const found = pick as PickItem;
			if (found._sessionId) {
				this.backend.switchConversation(found._sessionId, found._agentId);
			}
		} catch (err) {
			this._notifyInteractionError(wescodeL('switch_session'), err);
		}
	}

	// ── QuickPick: Agent (global switch) ──────────────────────────────────────

	private static readonly _EXT_MIME: Record<string, string> = {
		jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif',
		webp: 'image/webp', bmp: 'image/bmp', svg: 'image/svg+xml', avif: 'image/avif',
		ico: 'image/x-icon',
	};

	/**
	 * Host-side copy of @wesui/chat isImageAttachment (single source of
	 * truth; the host cannot import @wesui). MIME prefix is authoritative,
	 * well-known image extension is the fallback.
	 */
	private static _isImageAttachment(name: string, mimeType?: string): boolean {
		if (mimeType && mimeType.toLowerCase().startsWith('image/')) {
			return true;
		}
		return /\.(png|jpe?g|gif|webp|bmp|svg|avif|ico)$/i.test(name);
	}

	private _resolveImageMime(filePath: string): string | undefined {
		const ext = filePath.split('.').pop()?.toLowerCase();
		if (!ext) { return undefined; }
		return WescodeChatViewPane._EXT_MIME[ext] || `image/${ext}`;
	}

	private async _loadPreview(fileId: string): Promise<string | undefined> {
		try {
			const content = await this.backend.getFileContent(fileId);
			// Cap aligned with store.go localImageDataURL (10 MB): oversized
			// previews would balloon the postMessage payload to the webview.
			if (content.data.length * 3 / 4 > 10 * 1024 * 1024) {
				return undefined;
			}
			return `data:${content.mimeType};base64,${content.data}`;
		} catch {
			return undefined;
		}
	}

	// Native billing banner removed — the canonical implementation lives in
	// the webview (BillingDebtBanner from @wesui/billing, driven by
	// useBillingStore). Native pane only renders the toolbar + webview now.

	// ── Workspace Folder ─────────────────────────────────────────────────────

	private _initWorkspaceFolder(): void {
		this.selectedWorkspaceFolder = this._detectActiveFolder() ?? null;
	}

	/**
	 * Resolves the workspace folder from the currently active editor, falling
	 * back to folders[0]. This ensures the AI agent operates in the directory
	 * the user is actually working in, not an arbitrary first folder.
	 */
	private _detectActiveFolder(): IWorkspaceFolder | null {
		const folders = this.workspaceService.getWorkspace().folders;
		if (folders.length === 0) { return null; }
		if (folders.length === 1) { return folders[0]; }
		const activeUri = this.editorService.activeEditor?.resource;
		if (activeUri) {
			const match = this.workspaceService.getWorkspaceFolder(activeUri);
			if (match) { return match; }
		}
		return folders[0];
	}

	private _getWorkDir(): string | undefined {
		return this.selectedWorkspaceFolder?.uri.fsPath;
	}

	/**
	 * Cell identity for this window. Must match WescodeEnginePreheater:
	 * folders[0] from the workspace definition, never the active editor.
	 * Active-editor paths are exec CWD (`_getWorkDir` / `_sessionWorkDir`).
	 */
	private _cellWorkDir(): string {
		// INV-WS-09: identity logic lives in the common cellIdentity module so
		// it is unit-testable (cellIdentity.test.ts locks folders[0] behavior).
		return cellWorkDirForWorkspace(this.workspaceService.getWorkspace());
	}

	private _menuLabel(zh: string, en: string): string {
		return this._locale === 'en' ? en : zh;
	}

	private _buildMoreMenuItems(): IMoreMenuItem[] {
		return [
			{ label: this._menuLabel('关于当前助手', 'Current Agent'), icon: 'robot', action: () => {
				const agent = this._activeAgent;
				if (agent) {
					this.webviewEditorService.openPage('agentDetail', { agentId: agent.id });
				} else {
					this.webviewEditorService.openPage('contacts');
				}
			} },
			{ label: this._menuLabel('搜索聊天内容', 'Search Chat'), icon: 'search', disabled: true, disabledHint: this._menuLabel('即将推出', 'Coming soon'), action: () => {} },
			{ label: '', separator: true, action: () => {} },
			{ label: this._menuLabel('详细模式', 'Detail Mode'), icon: 'list-tree', checked: this._detailMode, action: () => this._toggleDetailMode() },
			{ label: '', separator: true, action: () => {} },
			{ label: this._menuLabel('清空聊天记录', 'Clear Chat History'), icon: 'trash', action: () => void this._clearConversation() },
		];
	}

	private _toggleDetailMode(): void {
		this._detailMode = !this._detailMode;
		this.messagesWebview?.postMessage({ type: 'setDetailMode', detailMode: this._detailMode });
	}


	private async _clearConversation(): Promise<void> {
		if (!this.activeSessionId) { return; }
		const confirm = await this.quickInputService.pick(
			[{ label: this._menuLabel('确认清空', 'Confirm clear'), description: this._menuLabel('将删除当前会话的所有消息', 'All messages in this session will be deleted') }],
			{ title: this._menuLabel('清空聊天记录', 'Clear Chat History'), canPickMany: false }
		);
		if (confirm) {
			this.backend.newConversation();
		}
	}


	private _notifyInteractionInfo(message: string): void {
		this.notificationService.info(message);
	}

	private _notifyInteractionError(action: string, err: unknown): void {
		const detail = err instanceof Error ? err.message : String(err);
		this.logService.warn(`[wescode-chat] ${action} failed:`, err);
		this.notificationService.error(localize('interactionError', "{0} failed: {1}", action, detail));
	}

	// ── Public diff actions (called by keybinding commands) ──────────────────

	async acceptAllDiffs(): Promise<void> {
		if (!this.inlineDiffController.hasPendingDiffs()) {
			return;
		}
		this.inlineDiffController.acceptAll();
		await this.backend.editAccept('*');
		this._hasPendingDiffsKey.set(false);
	}

	async rejectAllDiffs(): Promise<void> {
		if (!this.inlineDiffController.hasPendingDiffs()) {
			return;
		}
		const rpcResult = await this.backend.editReject('*') as any;
		if (rpcResult && rpcResult.conflict) {
			this._postToWebview({ type: 'error', message: `Reject blocked: ${rpcResult.error ?? 'file modified by user'}` });
		} else {
			await this.inlineDiffController.rejectAll();
			this._hasPendingDiffsKey.set(false);
		}
	}

	async rejectLastDiff(): Promise<void> {
		const txIds = this.inlineDiffController.getPendingTxIds();
		if (txIds.length === 0) {
			return;
		}
		const lastTxId = txIds[txIds.length - 1];
		const rpcResult = await this.backend.editReject(lastTxId) as any;
		if (rpcResult && rpcResult.conflict) {
			this._postToWebview({ type: 'error', message: `Reject blocked: ${rpcResult.error ?? 'file modified by user'}` });
		} else {
			await this.inlineDiffController.rejectDiff(lastTxId);
			this._hasPendingDiffsKey.set(this.inlineDiffController.hasPendingDiffs());
		}
	}
}
