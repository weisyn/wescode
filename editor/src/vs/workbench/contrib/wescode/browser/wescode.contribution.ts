/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import './media/wescode.css';
import { localize, localize2 } from '../../../../nls.js';
import { Registry } from '../../../../platform/registry/common/platform.js';
import { IViewContainersRegistry, ViewContainerLocation, Extensions as ViewContainerExtensions, IViewsRegistry } from '../../../common/views.js';
import { SyncDescriptor } from '../../../../platform/instantiation/common/descriptors.js';
import { ViewPaneContainer } from '../../../browser/parts/views/viewPaneContainer.js';
import { Codicon } from '../../../../base/common/codicons.js';
import { registerIcon } from '../../../../platform/theme/common/iconRegistry.js';
import {
	WESCODE_VIEWLET_ID, WESCODE_VIEW_PANE_ID,
} from '../common/types.js';
import { fileBasename } from '../common/wescodePath.js';
import { WescodeChatViewPane } from './chat/chatViewPane.js';
import { WescodeInlineCompletionProvider } from './inline/inlineCompletionProvider.js';
import { WescodeCallgraphHoverProvider, WescodeCallgraphCodeLensProvider } from './codeintel/callgraphProviders.js';
import { WescodeInlineChatAdapter } from './inline/inlineChatAdapter.js';
import { WescodeLanguageModelRegistry } from './models/languageModelRegistry.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../common/contributions.js';
import { LifecyclePhase } from '../../../services/lifecycle/common/lifecycle.js';
import { InstantiationType, registerSingleton } from '../../../../platform/instantiation/common/extensions.js';
import { IWescodeWebviewEditorService, WescodeWebviewEditorService } from './editors/agentEditorService.js';
import { Action2, MenuId, registerAction2 } from '../../../../platform/actions/common/actions.js';
import { ServicesAccessor } from '../../../../platform/instantiation/common/instantiation.js';
import { IWescodeBackendService, IWescodeAuthState, IEditorStateReport, IRecentEditReport, IProviderItem, isIdentityAuthenticated, isLoggedOut } from '../../../../platform/wescode/common/wescode.js';
import { Disposable } from '../../../../base/common/lifecycle.js';
import { ILogService } from '../../../../platform/log/common/log.js';
import { IQuickInputService, IQuickPickItem, IQuickPickSeparator } from '../../../../platform/quickinput/common/quickInput.js';
import { INotificationService } from '../../../../platform/notification/common/notification.js';
import { IEditorService } from '../../../services/editor/common/editorService.js';
import { IEditorGroupsService } from '../../../services/editor/common/editorGroupsService.js';
import { ICodeEditor, isCodeEditor } from '../../../../editor/browser/editorBrowser.js';
import { WescodeBufferSync } from './bufferSync.js';
import './inline/editFeedbackTracker.js';

// Register the IPC proxy service for the renderer (electron-sandbox → main process)
import '../electron-sandbox/wescodeService.js';

// --- Icons ---

const wescodeViewIcon = registerIcon('wescode-view-icon', Codicon.sparkle, localize('wescodeViewIcon', 'View icon of the wescode AI panel.'));

// --- Right Auxiliary Bar: Chat ---

const viewContainer = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry).registerViewContainer({
	id: WESCODE_VIEWLET_ID,
	title: localize2('wescode', 'WES Code'),
	ctorDescriptor: new SyncDescriptor(ViewPaneContainer, [WESCODE_VIEWLET_ID, { mergeViewWithContainerWhenSingleView: true }]),
	icon: wescodeViewIcon,
	order: 10,
	hideIfEmpty: false,
}, ViewContainerLocation.AuxiliaryBar);

const viewsRegistry = Registry.as<IViewsRegistry>(ViewContainerExtensions.ViewsRegistry);

viewsRegistry.registerViews([{
	id: WESCODE_VIEW_PANE_ID,
	name: localize2('wescodeChat', '对话'),
	containerIcon: wescodeViewIcon,
	ctorDescriptor: new SyncDescriptor(WescodeChatViewPane),
	canToggleVisibility: false,
	canMoveView: true,
	order: 0,
}], viewContainer);

// --- Left Sidebar: Agent & Skills Panel ---

import { SETTINGS_PANE_ID, WescodeSettingsPane } from './sidebar/settingsPane.js';
import {
	AGENTS_PANE_ID, WescodeAgentsQuickPane,
	SKILLS_PANE_ID, WescodeSkillsQuickPane,
	KNOWLEDGE_PANE_ID, WescodeKnowledgeQuickPane,
	PROJECT_OVERVIEW_PANE_ID, WescodeProjectOverviewQuickPane,
	CRON_PANE_ID, WescodeCronQuickPane,
	MEMORY_PANE_ID, WescodeMemoryQuickPane,
} from './sidebar/quickOpenPane.js';

const WESCODE_SIDEBAR_ID = 'workbench.view.wescode.sidebar';
const wescodeManageHiddenIcon = registerIcon('wescode-manage-hidden-icon', Codicon.settingsGear, localize('wescodeManageIcon', 'Hidden icon for WES manage panel.'));

const sidebarContainer = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry).registerViewContainer({
	id: WESCODE_SIDEBAR_ID,
	title: localize2('wescodeManage', 'WES 管理'),
	ctorDescriptor: new SyncDescriptor(ViewPaneContainer, [WESCODE_SIDEBAR_ID, { mergeViewWithContainerWhenSingleView: true }]),
	icon: wescodeManageHiddenIcon,
	order: 10000,
	hideIfEmpty: true,
}, ViewContainerLocation.Sidebar);

viewsRegistry.registerViews([
	{
		id: SETTINGS_PANE_ID,
		name: localize2('wescodeSettings', '设置'),
		containerIcon: Codicon.gear,
		ctorDescriptor: new SyncDescriptor(WescodeSettingsPane),
		canToggleVisibility: false,
		canMoveView: false,
		order: 0,
	},
], sidebarContainer);

// --- Activity Bar: Workspace feature icons (open dedicated Editor Tabs) ---

function registerFeatureContainer(id: string, title: ReturnType<typeof localize2>, icon: typeof Codicon.robot, order: number, paneId: string, paneName: ReturnType<typeof localize2>, PaneCtor: any) {
	const c = Registry.as<IViewContainersRegistry>(ViewContainerExtensions.ViewContainersRegistry).registerViewContainer({
		id, title, icon, order, hideIfEmpty: false,
		ctorDescriptor: new SyncDescriptor(ViewPaneContainer, [id, { mergeViewWithContainerWhenSingleView: true }]),
	}, ViewContainerLocation.Sidebar);
	viewsRegistry.registerViews([{ id: paneId, name: paneName, containerIcon: icon, ctorDescriptor: new SyncDescriptor(PaneCtor), canToggleVisibility: false, canMoveView: false, order: 0 }], c);
}

registerFeatureContainer('workbench.view.wescode.agents', localize2('wescodeAgents2', '助手'), Codicon.robot, 6, AGENTS_PANE_ID, localize2('wescodeAgentsPane', '助手'), WescodeAgentsQuickPane);
registerFeatureContainer('workbench.view.wescode.skills', localize2('wescodeSkills', '技能'), Codicon.sparkle, 7, SKILLS_PANE_ID, localize2('wescodeSkillsPane', '技能'), WescodeSkillsQuickPane);
registerFeatureContainer('workbench.view.wescode.knowledge', localize2('wescodeKnowledge', '知识库'), Codicon.book, 8, KNOWLEDGE_PANE_ID, localize2('wescodeKnowledgePane', '知识库'), WescodeKnowledgeQuickPane);
registerFeatureContainer('workbench.view.wescode.projectOverview', localize2('wescodeProjectOverview', '知识图谱'), Codicon.graph, 9, PROJECT_OVERVIEW_PANE_ID, localize2('wescodeProjectOverviewPane', '知识图谱'), WescodeProjectOverviewQuickPane);
registerFeatureContainer('workbench.view.wescode.cron', localize2('wescodeCron', '定时任务'), Codicon.clock, 10, CRON_PANE_ID, localize2('wescodeCronPane', '定时任务'), WescodeCronQuickPane);
registerFeatureContainer('workbench.view.wescode.memory', localize2('wescodeMemory', '记忆中心'), Codicon.lightbulbSparkle, 11, MEMORY_PANE_ID, localize2('wescodeMemoryPane', '记忆中心'), WescodeMemoryQuickPane);

// --- Inline Completion Provider ---

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeInlineCompletionProvider, LifecyclePhase.Eventually);

// --- Inline Chat Adapter (Cmd+K → wesgine via upstream InlineChatController) ---

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeInlineChatAdapter, LifecyclePhase.Eventually);

// --- Language Model Registry (sync wesgine providers → upstream ILanguageModelsService) ---

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeLanguageModelRegistry, LifecyclePhase.Eventually);

// --- Editor State Tracker: report focus file / cursor to backend ---

class WescodeEditorStateTracker extends Disposable {
	static readonly ID = 'wescode.editorStateTracker';

	private _debounceTimer: ReturnType<typeof setTimeout> | undefined;
	private _cursorDisposable: Disposable | undefined;
	private _contentDisposable: Disposable | undefined;
	private _lastState: IEditorStateReport | undefined;
	private _reported = false;

	// Recent edits LRU ring buffer (CE-11: max 20 entries, 10min TTL)
	private readonly _recentEdits: IRecentEditReport[] = [];
	private static readonly MAX_RECENT_EDITS = 20;
	private static readonly RECENT_EDIT_TTL_MS = 10 * 60 * 1000;

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IEditorService private readonly editorService: IEditorService,
		@IEditorGroupsService private readonly editorGroupsService: IEditorGroupsService,
		@IMarkerService private readonly markerService: IMarkerService,
	) {
		super();

		this._register(this.editorService.onDidActiveEditorChange(() => {
			this._snapshot();
			this._trackCursor();
			this._trackContent();
			this._scheduleReport();
		}));

		this._snapshot();
	}

	private _trackCursor(): void {
		this._cursorDisposable?.dispose();
		const control = this.editorService.activeTextEditorControl;
		if (control && isCodeEditor(control)) {
			const d = control.onDidChangeCursorPosition(() => {
				this._snapshot();
				this._scheduleReport();
			});
			this._cursorDisposable = { dispose: () => d.dispose() } as Disposable;
		}
	}

	private _trackContent(): void {
		this._contentDisposable?.dispose();
		const control = this.editorService.activeTextEditorControl;
		if (control && isCodeEditor(control)) {
			const editor = control as ICodeEditor;
			const model = editor.getModel();
			if (model && model.uri.scheme === 'file') {
				const filePath = model.uri.fsPath;
				const d = model.onDidChangeContent((e) => {
					for (const change of e.changes) {
						this._recordRecentEdit(filePath, change.range.startLineNumber - 1, change.range.endLineNumber - 1);
					}
				});
				this._contentDisposable = { dispose: () => d.dispose() } as Disposable;
			}
		}
	}

	private _recordRecentEdit(path: string, startLine: number, endLine: number): void {
		const now = Date.now();
		// Evict expired entries
		while (this._recentEdits.length > 0 &&
			(now - this._recentEdits[0].timestamp) > WescodeEditorStateTracker.RECENT_EDIT_TTL_MS) {
			this._recentEdits.shift();
		}
		// Merge with last entry if same file and close in time (<2s) and adjacent lines
		const last = this._recentEdits.length > 0 ? this._recentEdits[this._recentEdits.length - 1] : undefined;
		if (last && last.path === path && (now - last.timestamp) < 2000) {
			last.startLine = Math.min(last.startLine, startLine);
			last.endLine = Math.max(last.endLine, endLine);
			last.timestamp = now;
			return;
		}
		this._recentEdits.push({ path, timestamp: now, startLine, endLine });
		if (this._recentEdits.length > WescodeEditorStateTracker.MAX_RECENT_EDITS) {
			this._recentEdits.shift();
		}
	}

	private _scheduleReport(): void {
		if (this._debounceTimer) {
			clearTimeout(this._debounceTimer);
		}
		this._debounceTimer = setTimeout(() => this._send(), 300);
	}

	private _snapshot(): void {
		const control = this.editorService.activeTextEditorControl;
		if (!control || !isCodeEditor(control)) {
			return;
		}
		const editor = control as ICodeEditor;
		const model = editor.getModel();
		if (!model || model.uri.scheme !== 'file') {
			return;
		}
		const position = editor.getPosition();
		const selection = editor.getSelection();

		let selectionReport: { startLine: number; startCol: number; endLine: number; endCol: number; text: string } | undefined;
		if (selection && !selection.isEmpty()) {
			selectionReport = {
				startLine: selection.startLineNumber - 1,
				startCol: selection.startColumn - 1,
				endLine: selection.endLineNumber - 1,
				endCol: selection.endColumn - 1,
				text: model.getValueInRange(selection),
			};
		}

		// CE-14: collect ALL open tabs across all editor groups, not just visible
		const openFiles: string[] = [];
		const seen = new Set<string>();
		for (const group of this.editorGroupsService.groups) {
			for (const editorInput of group.editors) {
				const uri = editorInput.resource;
				if (uri && uri.scheme === 'file' && !seen.has(uri.fsPath)) {
					seen.add(uri.fsPath);
					openFiles.push(uri.fsPath);
				}
			}
		}

		// Visible range of the active editor
		const ranges = editor.getVisibleRanges();
		let visibleRange: { startLine: number; endLine: number } | undefined;
		if (ranges.length > 0) {
			visibleRange = {
				startLine: ranges[0].startLineNumber - 1,
				endLine: ranges[ranges.length - 1].endLineNumber - 1,
			};
		}

		// Prune expired recent edits
		const now = Date.now();
		while (this._recentEdits.length > 0 &&
			(now - this._recentEdits[0].timestamp) > WescodeEditorStateTracker.RECENT_EDIT_TTL_MS) {
			this._recentEdits.shift();
		}

		this._lastState = {
			focusFile: model.uri.fsPath,
			cursorLine: position ? position.lineNumber - 1 : 0,
			cursorCol: position ? position.column - 1 : 0,
			selection: selectionReport,
			openFiles,
			visibleRange,
			recentEdits: this._recentEdits.length > 0 ? [...this._recentEdits] : undefined,
			globalErrors: this._collectGlobalErrors(),
			gitStagedFiles: this._collectGitStagedFiles(),
			visibleEditors: this._collectVisibleEditors(),
		};
		this._reported = false;
	}

	private _collectGlobalErrors(): Array<{ path: string; line: number; column: number; message: string }> | undefined {
		const markers = this.markerService.read({ severities: MarkerSeverity.Error, take: 20 });
		if (markers.length === 0) { return undefined; }
		return markers
			.filter(m => m.resource.scheme === 'file')
			.slice(0, 20)
			.map(m => ({
				path: m.resource.fsPath,
				line: m.startLineNumber - 1,
				column: m.startColumn - 1,
				message: m.message,
			}));
	}

	private _collectGitStagedFiles(): string[] | undefined {
		// SCM staged files are not easily accessible without ISCMService DI.
		// For now, return undefined — the backend's `git diff --cached --name-only`
		// in WsIntel probe already provides this at workspace-level context.
		// Future: inject ISCMService and read staged changes from SCM providers.
		return undefined;
	}

	private _collectVisibleEditors(): string[] | undefined {
		const visible: string[] = [];
		for (const group of this.editorGroupsService.groups) {
			const active = group.activeEditor;
			if (active?.resource && active.resource.scheme === 'file') {
				visible.push(active.resource.fsPath);
			}
		}
		return visible.length > 1 ? visible : undefined;
	}

	private _send(): void {
		if (!this._lastState || this._reported) {
			return;
		}
		this.backend.reportEditorState(this._lastState).then(() => {
			this._reported = true;
		}).catch(() => {
			// Backend not ready — _lastState is retained, will retry on next trigger.
		});
	}

	override dispose(): void {
		this._cursorDisposable?.dispose();
		this._contentDisposable?.dispose();
		if (this._debounceTimer) {
			clearTimeout(this._debounceTimer);
		}
		super.dispose();
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeEditorStateTracker, LifecyclePhase.Restored);

// --- Buffer Sync: push dirty editor buffers to backend for AI read tool ---

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeBufferSync, LifecyclePhase.Restored);

// --- Force AuxiliaryBar visible on startup ---

import { IWorkbenchLayoutService, Parts } from '../../../services/layout/browser/layoutService.js';

class WescodeAuxiliaryBarEnsurer extends Disposable {
	static readonly ID = 'wescode.auxiliaryBarEnsurer';
	constructor(
		@IWorkbenchLayoutService private readonly layoutService: IWorkbenchLayoutService,
	) {
		super();
		if (!this.layoutService.isVisible(Parts.AUXILIARYBAR_PART)) {
			this.layoutService.setPartHidden(false, Parts.AUXILIARYBAR_PART);
		}
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeAuxiliaryBarEnsurer, LifecyclePhase.Restored);

// --- Engine Preheater: background-initialize engine on startup ---

import { IWorkspaceContextService } from '../../../../platform/workspace/common/workspace.js';

class WescodeEnginePreheater extends Disposable {
	static readonly ID = 'wescode.enginePreheater';

	// INV-WS-05: Primary workspace root locked for this window's Go process
	// lifetime. Multi-root folder additions update the roots list but never
	// change the Cell ID. The workspace root is derived from the workspace
	// definition (stable across restarts), NOT from the active editor at
	// startup — the old activeEditor-based heuristic caused Cell ID drift
	// when the user's last-edited file happened to be in a different folder.
	private _primaryFolder: string = '';

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IWorkspaceContextService private readonly workspaceService: IWorkspaceContextService,
	) {
		super();
		this._syncWorkspace();

		this._register(this.workspaceService.onDidChangeWorkspaceFolders(() => {
			this._syncWorkspace();
		}));
	}

	private _syncWorkspace(): void {
		const folders = this.workspaceService.getWorkspace().folders;
		if (folders.length === 0) {
			void this.backend.initialize('', []).catch(err => {
				console.warn('[wescode] Preheater initialize (config mode) failed:', err?.message);
			});
			return;
		}

		// First call: derive a STABLE workspace root and lock it for the
		// process lifetime. Subsequent calls keep the locked root.
		//
		// Stability contract: the workspace root must be deterministic across
		// restarts so the Go backend always maps to the SAME Cell ID.
		//
		// folders[0] is used unconditionally because:
		//  1. It's a DIRECTORY path (Go backend uses it as CWD + file resolution
		//     root — a file path like .code-workspace causes spawn ENOTDIR).
		//  2. It's deterministic: folder order comes from the workspace definition
		//     (.code-workspace file or VS Code internal state), not from
		//     activeEditor restoration. The old activeEditor heuristic was the
		//     root cause of Cell ID drift across restarts.
		//  3. It's stable: the user rarely reorders workspace folders.
		if (!this._primaryFolder) {
			this._primaryFolder = folders[0].uri.fsPath;
		}

		const allFolderPaths = folders.map(f => f.uri.fsPath);
		void this.backend.initialize(this._primaryFolder, allFolderPaths).catch(err => {
			console.warn('[wescode] Preheater initialize failed:', err?.message);
		});
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeEnginePreheater, LifecyclePhase.Restored);

// --- Terminal Manager: Agent commands in VSCode integrated terminal ---

import { WescodeTerminalManager } from './terminal/terminalManager.js';

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeTerminalManager, LifecyclePhase.Restored);

// --- Cron notifier: a finished scheduled run is otherwise invisible ---

import { WescodeCronNotifier } from './cron/cronNotifier.js';

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeCronNotifier, LifecyclePhase.Restored);

// --- Source Control: track AI-modified files in SCM panel ---

import { WescodeSourceControl } from './scm/wescodeSourceControl.js';

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeSourceControl, LifecyclePhase.Restored);

// --- Auth Gate: prime auth cache + auto-open login for anonymous users ---
//
// History:
//   f158071c (2026-07-29): removed auto-open — login webview was blank
//     because openPage('login') ran at LifecyclePhase.Restored when the
//     Go backend was not yet ready (RPC calls in the webview failed).
//   61fa7a17 (2026-08-07): re-introduced _checkAuth + _openLoginIfNoBYOK
//     but the catch block unconditionally opened login → blank page again.
//   b16173b0 (2026-08-07): deleted everything, leaving only authMe() cache.
//   (2026-09-01): removed the 10s setInterval recheck. It called authMe +
//     listProviders + openPage('login') forever while anonymous; openPage
//     revealed with preserveFocus=false, so it yanked the cursor out of the
//     editor every 10s — 286 times in one 47min session — and reopened the
//     tab if the user closed it. Violated INV-WV-02 (AGENTS.md). The poll
//     only existed to notice a login performed in *another* window; that is
//     now covered by the main process broadcasting auth state to all windows
//     (wescodeBackendService._broadcastAuthState), since the session is
//     global in wescode_auth.db.
//
// Current shape: one prime at startup plus event-driven rechecks, with the
// RPC itself as the readiness probe. catch blocks NEVER open login — they
// schedule a bounded backoff on the step that failed. Anonymous is a
// definitive answer, never retried, and the auto-opened tab stays closed once
// the user closes it.
//
// Deliberately NOT gated on a cached `engineHealthy` flag: this contribution
// starts at LifecyclePhase.Restored, and onDidEngineHealthChange only fires
// on *transitions*. An engine that was already healthy by then never fires,
// so such a flag stays false forever and the gate degrades from "opens login
// every 10s" to "never opens login". listProviders is in the main process's
// ENGINE_BOOT_SAFE set and throws while the backend is unbound, which is the
// same signal without the staleness.

class WescodeAuthGate extends Disposable {
	static readonly ID = 'wescode.authGate';

	/** Backoff for RPC *failures* only. An 'anonymous' answer is a definitive
	 *  answer, not a failure — retrying it is what produced the 10s storm. */
	private static readonly RETRY_DELAYS_MS = [1_000, 2_000, 4_000, 8_000, 16_000];

	private _checkInFlight = false;
	private _openInFlight = false;
	/** Auto-open is once per logged-out spell. Cleared on authentication, so a
	 *  later logout opens again — but an engine crash-restart does not reopen
	 *  a tab the user deliberately closed. */
	private _loginAutoOpened = false;
	private _retryAttempt = 0;
	private _retryTimer: ReturnType<typeof setTimeout> | undefined;

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IWescodeWebviewEditorService private readonly webviewEditor: IWescodeWebviewEditorService,
		@ILogService private readonly logService: ILogService,
	) {
		super();

		this._register(this.backend.onDidEngineHealthChange(health => {
			// Recovery re-runs the whole chain: whatever failed during the outage
			// gets one clean attempt. A dead engine cannot answer, so stop
			// burning backoff attempts on it.
			this._clearRetry();
			if (health.healthy) {
				void this._checkAuthAndOpenLogin();
			}
		}));

		// The cross-window trigger. The main process broadcasts auth changes to
		// every window (the session is global, in wescode_auth.db), so logging
		// in elsewhere reaches us here — that case is the sole reason the 10s
		// poll existed, and it cost 286 focus steals in 47min.
		this._register(this.backend.onDidAuthStateChange(state => this._applyAuthState(state.status)));

		// Must come after the subscription above, not before. The renderer only
		// sends the channel's `listen` message when the event gets its first
		// listener, and the main process creates the per-window emitter on
		// receiving it; IPC preserves order, so priming first would let the
		// broadcast fire into a window that has no emitter yet and vanish.
		void this._checkAuthAndOpenLogin();
	}

	private _applyAuthState(status: string): void {
		if (isIdentityAuthenticated(status)) {
			this._clearRetry();
			this._loginAutoOpened = false;
			this.webviewEditor.closePage('login');
		} else if (isLoggedOut(status)) {
			void this._openLoginIfNoBYOK();
		}
	}

	private _clearRetry(): void {
		if (this._retryTimer) {
			clearTimeout(this._retryTimer);
			this._retryTimer = undefined;
		}
		this._retryAttempt = 0;
	}

	/** Bounded, one-shot-per-failure. `retry` re-runs the step that failed —
	 *  a generic "recheck auth" retry would not do, because the broadcast for
	 *  an unchanged status is deduped in the main process, so a successful
	 *  authMe after a listProviders failure delivers no event and the login
	 *  page would never open. Exhausting the budget leaves no login page,
	 *  which is why it must not be silent: the Accounts menu is the way in. */
	private _scheduleRetry(reason: string, retry: () => void): void {
		if (this._retryTimer) { return; }
		const delay = WescodeAuthGate.RETRY_DELAYS_MS[this._retryAttempt];
		if (delay === undefined) {
			this.logService.warn(`[wescode] AuthGate: ${reason} — retries exhausted, use the Accounts menu to sign in`);
			return;
		}
		this._retryAttempt++;
		this._retryTimer = setTimeout(() => {
			this._retryTimer = undefined;
			retry();
		}, delay);
	}

	/** Acts on the returned status rather than waiting for the broadcast it
	 *  also triggers: the broadcast is deduped per window, so the very first
	 *  'anonymous' can be swallowed if some other path already reported it.
	 *  Both paths landing is harmless — _openLoginIfNoBYOK is idempotent. */
	private async _checkAuthAndOpenLogin(): Promise<void> {
		if (this._checkInFlight) { return; }
		this._checkInFlight = true;
		try {
			const state = await this.backend.authMe();
			this._clearRetry();
			this._applyAuthState(state.status);
		} catch {
			this._scheduleRetry('authMe failed', () => void this._checkAuthAndOpenLogin());
		} finally {
			this._checkInFlight = false;
		}
	}

	private async _openLoginIfNoBYOK(): Promise<void> {
		if (this._loginAutoOpened || this._openInFlight) { return; }
		this._openInFlight = true;
		try {
			const providers: IProviderItem[] = await this.backend.listProviders();
			this._clearRetry();
			const hasBYOK = providers.some(p => p.hasApiKey && p.name !== 'unconfigured');
			if (!hasBYOK) {
				this._loginAutoOpened = true;
				// preserveFocus: nobody asked for this tab. Stealing the cursor
				// out of the editor is only acceptable when the user clicked.
				this.webviewEditor.openPage('login', undefined, { preserveFocus: true });
			}
		} catch {
			// Also covers "engine not ready yet" — listProviders throws while the
			// backend is unbound, which is what makes the health flag unnecessary.
			this._scheduleRetry('listProviders failed', () => void this._openLoginIfNoBYOK());
		} finally {
			this._openInFlight = false;
		}
	}

	override dispose(): void {
		this._clearRetry();
		super.dispose();
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeAuthGate, LifecyclePhase.Restored);

// --- Activity Bar Accounts: weisyn account menu ---

registerAction2(class WescodeOpenUserMenuAction extends Action2 {
	constructor() {
		super({
			id: 'wescode.openUserMenu',
			title: localize2('wescode.openUserMenu', "weisyn Account"),
			menu: [{
				id: MenuId.AccountsContext,
				group: '0_wescode',
				order: 1,
			}],
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		const webviewEditor = accessor.get(IWescodeWebviewEditorService);

		const log = accessor.get(ILogService);
		const state: IWescodeAuthState = await backend.authMe().catch(() => ({ status: 'anonymous' }));
		if (!isIdentityAuthenticated(state.status)) {
			log.info('[wescode] openUserMenu: not authenticated → login');
			webviewEditor.openPage('login');
			return;
		}
		// Logged-in: open account management directly. Do not interpose a
		// QuickPick whose first row used to be a no-op (id: 'info').
		// Logout lives on the account page.
		log.info('[wescode] openUserMenu: opening settings tab=account');
		webviewEditor.openPage('settings', { tab: 'account' });
	}
});

// --- Accept All / Reject All Diff Commands ---

import { KeyMod, KeyCode } from '../../../../base/common/keyCodes.js';
import { KeybindingWeight } from '../../../../platform/keybinding/common/keybindingsRegistry.js';
import { IViewsService } from '../../../services/views/common/viewsService.js';

registerAction2(class WescodeAcceptAllDiffs extends Action2 {
	constructor() {
		super({
			id: 'wescode.acceptAllDiffs',
			title: localize2('wescodeAcceptAllDiffs', "接受所有 AI 修改"),
			keybinding: {
				weight: KeybindingWeight.WorkbenchContrib,
				primary: KeyMod.CtrlCmd | KeyMod.Shift | KeyCode.Enter,
			},
		});
	}
	async run(accessor: ServicesAccessor) {
		const viewsService = accessor.get(IViewsService);
		const view = viewsService.getActiveViewWithId(WESCODE_VIEW_PANE_ID) as WescodeChatViewPane | undefined;
		if (view) {
			view.acceptAllDiffs();
		}
	}
});

registerAction2(class WescodeRejectAllDiffs extends Action2 {
	constructor() {
		super({
			id: 'wescode.rejectAllDiffs',
			title: localize2('wescodeRejectAllDiffs', "拒绝所有 AI 修改"),
			keybinding: {
				weight: KeybindingWeight.WorkbenchContrib,
				primary: KeyMod.CtrlCmd | KeyMod.Shift | KeyCode.Backspace,
			},
		});
	}
	async run(accessor: ServicesAccessor) {
		const viewsService = accessor.get(IViewsService);
		const view = viewsService.getActiveViewWithId(WESCODE_VIEW_PANE_ID) as WescodeChatViewPane | undefined;
		if (view) {
			view.rejectAllDiffs();
		}
	}
});

// --- Open Side-by-Side Diff Editor for AI edits ---

registerAction2(class WescodeOpenDiffView extends Action2 {
	constructor() {
		super({
			id: 'wescode.openDiffView',
			title: localize2('wescodeOpenDiffView', "在并排编辑器中打开 AI 差异"),
			keybinding: {
				weight: KeybindingWeight.WorkbenchContrib,
				primary: KeyMod.CtrlCmd | KeyMod.Shift | KeyCode.KeyD,
			},
		});
	}
	async run(accessor: ServicesAccessor) {
		const editorService = accessor.get(IEditorService);
		const backend = accessor.get(IWescodeBackendService);

		const active = editorService.activeTextEditorControl;
		if (!active || !isCodeEditor(active)) { return; }
		const model = (active as ICodeEditor).getModel();
		if (!model || model.uri.scheme !== 'file') { return; }

		const filePath = model.uri.fsPath;

		try {
			const metrics = await backend.editMetrics();
			if (metrics.totalEdits === 0) { return; }
		} catch { /* backend not ready */ }

		const currentUri = model.uri;
		const gitUri = currentUri.with({ scheme: 'git', query: 'HEAD' });

		await editorService.openEditor({
			original: { resource: gitUri },
			modified: { resource: currentUri },
			label: `AI 修改: ${fileBasename(filePath)}`,
		});
	}
});

// --- Undo AI Edit: Cmd+Z intercept when pending diffs exist ---

import { ContextKeyExpr } from '../../../../platform/contextkey/common/contextkey.js';
import { WESCODE_HAS_PENDING_DIFFS } from '../common/types.js';

registerAction2(class WescodeUndoDiff extends Action2 {
	constructor() {
		super({
			id: 'wescode.undoDiff',
			title: localize2('wescodeUndoDiff', "撤销上一次 AI 编辑"),
			keybinding: {
				weight: KeybindingWeight.WorkbenchContrib + 10,
				primary: KeyMod.CtrlCmd | KeyCode.KeyZ,
				when: ContextKeyExpr.has(WESCODE_HAS_PENDING_DIFFS.key),
			},
		});
	}
	async run(accessor: ServicesAccessor) {
		const viewsService = accessor.get(IViewsService);
		const view = viewsService.getActiveViewWithId(WESCODE_VIEW_PANE_ID) as WescodeChatViewPane | undefined;
		if (view) {
			view.rejectLastDiff();
		}
	}
});

// --- Edit Metrics Command ---

registerAction2(class WescodeShowEditMetrics extends Action2 {
	constructor() {
		super({
			id: 'wescode.showEditMetrics',
			title: localize2('wescodeEditMetrics', "显示编辑指标"),
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		const quickInput = accessor.get(IQuickInputService);
		try {
			const m = await backend.editMetrics();
			const totalFallback = m.tier2Hits + m.tier3Hits + m.allMiss;
			const tier1 = m.totalEdits - totalFallback;
			const items: IQuickPickItem[] = [
				{ label: `总编辑数: ${m.totalEdits}`, description: `写入: ${m.totalWrites}, 补丁: ${m.totalApplies}` },
				{ label: `Tier 1 (精确匹配): ${tier1}`, description: tier1 > 0 ? `${((tier1 / m.totalEdits) * 100).toFixed(1)}%` : '-' },
				{ label: `Tier 2 (归一化匹配): ${m.tier2Hits}`, description: m.totalEdits > 0 ? `${((m.tier2Hits / m.totalEdits) * 100).toFixed(1)}%` : '-' },
				{ label: `Tier 3 (结构化匹配): ${m.tier3Hits}`, description: m.totalEdits > 0 ? `${((m.tier3Hits / m.totalEdits) * 100).toFixed(1)}%` : '-' },
				{ label: `全部未命中: ${m.allMiss}`, description: m.totalEdits > 0 ? `${((m.allMiss / m.totalEdits) * 100).toFixed(1)}%` : '-' },
			];
			await quickInput.pick(items, { title: '编辑引擎指标', canPickMany: false });
		} catch {
			await quickInput.pick([{ label: '后端不可用' }], { title: '编辑引擎指标' });
		}
	}
});

// --- Webview Editor Service ---

registerSingleton(IWescodeWebviewEditorService, WescodeWebviewEditorService, InstantiationType.Delayed);

// --- Header Tabs Service ---

import { IWescodeHeaderTabsService, WescodeHeaderTabsService } from './chat/wescodeHeaderTabs.js';
registerSingleton(IWescodeHeaderTabsService, WescodeHeaderTabsService, InstantiationType.Eager);

// --- Command Center: AI Toggle + WES Code brand page (INV-TITLE-01) ---

registerAction2(class WescodeToggleChat extends Action2 {
	constructor() {
		super({
			id: 'wescode.toggleChat',
			title: localize2('wescodeToggleChat', "切换 AI 对话"),
			icon: Codicon.sparkle,
			menu: [{ id: MenuId.CommandCenter, order: 10001 }],
			keybinding: {
				weight: KeybindingWeight.WorkbenchContrib,
				primary: KeyMod.CtrlCmd | KeyCode.KeyL,
			},
		});
	}
	run(accessor: ServicesAccessor) {
		const layoutService = accessor.get(IWorkbenchLayoutService);
		const isVisible = layoutService.isVisible(Parts.AUXILIARYBAR_PART);
		layoutService.setPartHidden(isVisible, Parts.AUXILIARYBAR_PART);
	}
});

registerAction2(class WescodeOpenProviderPage extends Action2 {
	constructor() {
		super({
			id: 'wescode.openProvider',
			title: localize2('wescodeOpenProvider', "配置 LLM 供应商"),
		});
	}
	run(accessor: ServicesAccessor) {
		accessor.get(IWescodeWebviewEditorService).openPage('provider');
	}
});

registerAction2(class WescodeOpenAboutPage extends Action2 {
	constructor() {
		super({
			id: 'wescode.openAbout',
			title: localize2('wescodeAbout', "WES Code"),
			icon: Codicon.info,
			menu: [{ id: MenuId.CommandCenter, order: 10002 }],
		});
	}
	run(accessor: ServicesAccessor) {
		accessor.get(IWescodeWebviewEditorService).openPage('about');
	}
});

// --- Plan Custom Editor ---

import { EditorPaneDescriptor, IEditorPaneRegistry } from '../../../browser/editor.js';
import { EditorExtensions } from '../../../common/editor.js';
import { PlanEditorPane } from './editors/planEditorPane.js';
import { PlanEditorInput } from './editors/planEditorInput.js';

Registry.as<IEditorPaneRegistry>(EditorExtensions.EditorPane).registerEditorPane(
	EditorPaneDescriptor.create(
		PlanEditorPane,
		PlanEditorPane.ID,
		'Plan'
	),
	[new SyncDescriptor(PlanEditorInput)]
);

registerAction2(class WescodeOpenPlan extends Action2 {
	constructor() {
		super({
			id: 'wescode.openPlan',
			title: localize2('wescodeOpenPlan', "打开计划"),
		});
	}
	async run(accessor: ServicesAccessor, sessionId?: string) {
		if (!sessionId) { return; }
		const backend = accessor.get(IWescodeBackendService);
		const editorService = accessor.get(IEditorService);

		const plan = await backend.getSessionPlan(sessionId);
		if (!plan || !plan.id) { return; }

		const planUri = URI.file(plan.id);
		const input = new PlanEditorInput(planUri);
		await editorService.openEditor(input);
	}
});

// --- Status Bar AI Indicator ---

import { WescodeAIStatusBar } from './statusbar/aiStatusBar.js';

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeAIStatusBar, LifecyclePhase.Restored);

// --- Status Bar AI readiness badge (workspace-scoped, PROD-1 aligned) ---
//
// Shows "$(check) AI 就绪" once the backend reports a valid Cell for the
// current workspace. No cellID hash is surfaced to the user — the
// tooltip carries only the workspace folder name. When no folder is
// open the entry is disposed entirely (VS Code's native workspace title
// already communicates that state).
//
// See wescode/AGENTS.md §九补 "UI 术语契约（PROD-1）".

import { WescodeWorkspaceCellBar } from './statusbar/workspaceCellBar.js';
import { WescodeIndexProgressBar } from './statusbar/indexProgressBar.js';

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeWorkspaceCellBar, LifecyclePhase.Restored);

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeIndexProgressBar, LifecyclePhase.Restored);

// --- CKG-backed Hover + CodeLens (P3 渗透点) ---

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeCallgraphHoverProvider, LifecyclePhase.Eventually);

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeCallgraphCodeLensProvider, LifecyclePhase.Eventually);

// --- AI status + run-policy switcher ---
//
// v1.0 PROD-1: this QuickPick surfaces AI readiness and lets the user
// swap the run policy (aka wesgine TrustPolicy — engine term never
// shown in UI) without restarting anything. The switch flows through
// cell.Policy().Update on the backend and takes effect on the next Run.

// Human-friendly labels for the four run-policy presets shipped by
// wesgine. Order matches the picker rows below.
const RUN_POLICY_OPTIONS: Array<{ id: string; label: string; description: string }> = [
	{ id: 'coding', label: '编程模式', description: '默认策略：常规操作放行，重要变更前询问' },
	{ id: 'assistant', label: '助手模式', description: '更谨慎：常规操作也会先询问，适合探索性任务' },
	{ id: 'locked', label: '锁定模式', description: '最严格：拒绝常规写入，适合企业合规场景' },
	{ id: 'bench', label: '基准模式', description: '全部放行，仅用于 CI 基准测试' },
];

function policyDisplayLabel(id: string): string {
	const opt = RUN_POLICY_OPTIONS.find(o => o.id === id);
	return opt ? opt.label : id || '默认';
}

registerAction2(class WescodeShowWorkspaceCellDetails extends Action2 {
	constructor() {
		super({
			id: 'wescode.showWorkspaceCellDetails',
			title: localize2('wescodeCellDetails', "AI: 显示运行状态 / 切换治理策略"),
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		const quickInput = accessor.get(IQuickInputService);
		const notification = accessor.get(INotificationService);
		let info;
		try {
			info = await backend.getCellInfo();
		} catch {
			notification.warn('AI 助手尚未就绪 —— 请先打开一个文件夹，或等待后端完成初始化。');
			return;
		}
		if (!info || !info.cellId) {
			notification.warn('AI 助手尚未就绪 —— 请先打开一个文件夹。');
			return;
		}
		// Use the workspace folder basename in the picker title instead
		// of the raw wesgine cellID hash (PROD-1 alignment). Keep the
		// full path in the info row so power users can copy it.
		const wsPath = info.workDir || '(未打开)';
		const wsName = wsPath === '(未打开)' ? '(未打开)' : (wsPath.split('/').filter(Boolean).pop() || wsPath);
		const currentPolicy = policyDisplayLabel(info.trustPolicy || '');
		const separator: IQuickPickSeparator = { type: 'separator', label: '' };
		const items: Array<IQuickPickItem | IQuickPickSeparator> = [
			{ label: `$(folder) 工作区`, description: wsPath },
			{ label: `$(law) 当前治理策略`, description: currentPolicy },
			separator,
			{ label: '$(gear) 切换治理策略 →' },
			...RUN_POLICY_OPTIONS.map(o => ({
				label: `  ${o.label}${o.id === info.trustPolicy ? ' $(check)' : ''}`,
				description: o.description,
				id: `set:${o.id}`,
			})),
		];
		const picked = await quickInput.pick(items as IQuickPickItem[], {
			title: `WES Code · 工作区 ${wsName}`,
			canPickMany: false,
			placeHolder: 'ESC 关闭，或选择一个治理策略切换',
		});
		if (!picked || !('id' in picked) || typeof (picked as any).id !== 'string') {
			return;
		}
		const pickedID = (picked as any).id as string;
		if (!pickedID.startsWith('set:')) {
			return;
		}
		const target = pickedID.substring(4);
		if (target === info.trustPolicy) {
			notification.info(`治理策略已是「${policyDisplayLabel(target)}」，无需切换。`);
			return;
		}
		try {
			const res = await backend.setTrustPolicy(target);
			notification.info(`治理策略已切换到「${policyDisplayLabel(res.applied)}」（下次对话生效）。`);
		} catch (err: unknown) {
			const msg = err instanceof Error ? err.message : String(err);
			notification.error(`切换治理策略失败: ${msg}`);
		}
	}
});

// --- Diagnostics Bridge: push QualityGate results to Problems panel ---

import { IMarkerService, IMarkerData, MarkerSeverity } from '../../../../platform/markers/common/markers.js';
import { URI } from '../../../../base/common/uri.js';

class WescodeDiagnosticsBridge extends Disposable {
	static readonly ID = 'wescode.diagnosticsBridge';
	private static readonly MARKER_OWNER = 'wescode-agent';

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IMarkerService private readonly markerService: IMarkerService,
	) {
		super();
		this._register(this.backend.onDidDiagnosticsChange((diagnostics) => {
			const byFile = new Map<string, IMarkerData[]>();
			for (const d of diagnostics) {
				const markers = byFile.get(d.path) ?? [];
				markers.push({
					severity: MarkerSeverity.Error,
					message: d.message,
					startLineNumber: d.line,
					startColumn: d.column,
					endLineNumber: d.line,
					endColumn: d.column + 1,
					source: WescodeDiagnosticsBridge.MARKER_OWNER,
				});
				byFile.set(d.path, markers);
			}
			if (byFile.size === 0) {
				this.markerService.changeAll(WescodeDiagnosticsBridge.MARKER_OWNER, []);
			} else {
				for (const [path, markers] of byFile) {
					this.markerService.changeOne(WescodeDiagnosticsBridge.MARKER_OWNER, URI.file(path), markers);
				}
			}
		}));
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeDiagnosticsBridge, LifecyclePhase.Restored);

// --- IDE Diagnostics Reporter: push IMarkerService changes to Backend ---

class WescodeDiagnosticsReporter extends Disposable {
	static readonly ID = 'wescode.diagnosticsReporter';
	private _pushTimer: ReturnType<typeof setTimeout> | undefined;

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IMarkerService private readonly markerService: IMarkerService,
	) {
		super();
		this._register(this.markerService.onMarkerChanged(() => this._schedulePush()));
		// Initial push on activation
		this._schedulePush();
	}

	private _schedulePush(): void {
		if (this._pushTimer !== undefined) {
			clearTimeout(this._pushTimer);
		}
		this._pushTimer = setTimeout(() => {
			this._pushTimer = undefined;
			this._doPush();
		}, 1500);
	}

	private _doPush(): void {
		const allMarkers = this.markerService.read({ severities: MarkerSeverity.Error | MarkerSeverity.Warning });
		const diagnostics: Array<{
			path: string; line: number; column: number;
			endLine?: number; endColumn?: number;
			severity: number; message: string; source?: string; code?: string;
		}> = [];

		for (const marker of allMarkers) {
			if (marker.resource.scheme !== 'file') {
				continue;
			}
			// Skip wescode-agent's own markers (from QualityGate) to avoid feedback loop
			if (marker.source === 'wescode-agent') {
				continue;
			}
			diagnostics.push({
				path: marker.resource.fsPath,
				line: marker.startLineNumber,
				column: marker.startColumn,
				endLine: marker.endLineNumber,
				endColumn: marker.endColumn,
				severity: this._mapSeverity(marker.severity),
				message: marker.message,
				source: marker.source ?? undefined,
				code: typeof marker.code === 'string' ? marker.code
					: marker.code?.value ?? undefined,
			});
		}

		this.backend.reportDiagnostics(diagnostics).catch(() => {
			// best-effort; backend may not be ready yet
		});
	}

	private _mapSeverity(severity: MarkerSeverity): number {
		switch (severity) {
			case MarkerSeverity.Error: return 1;
			case MarkerSeverity.Warning: return 2;
			case MarkerSeverity.Info: return 4;
			case MarkerSeverity.Hint: return 8;
			default: return 4;
		}
	}

	override dispose(): void {
		if (this._pushTimer !== undefined) {
			clearTimeout(this._pushTimer);
		}
		super.dispose();
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeDiagnosticsReporter, LifecyclePhase.Restored);

// --- Background Agent Command ---

registerAction2(class WescodeStartBackgroundChat extends Action2 {
	constructor() {
		super({
			id: 'wescode.startBackgroundChat',
			title: localize2('wescodeStartBgChat', "启动后台 Agent 对话"),
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		const quickInput = accessor.get(IQuickInputService);
		const input = await quickInput.input({
			title: '后台 Agent 对话',
			placeHolder: '输入要发送给后台 Agent 的消息...',
		});
		if (!input) { return; }
		try {
			const result = await backend.startBackgroundChat({ message: input });
			if (result.runId) {
				// Notification is handled by onDidBackgroundComplete in status bar
			}
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			await quickInput.pick([{ label: `错误: ${msg}` }], { title: '后台对话失败' });
		}
	}
});

// --- SCM Commands: stage and commit AI changes ---

registerAction2(class WescodeGitStageAll extends Action2 {
	constructor() {
		super({
			id: 'wescode.gitStageAll',
			title: localize2('wescodeGitStageAll', "暂存所有 AI 修改"),
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		try {
			const info = await backend.getGitInfo();
			const files = [...(info.changedFiles ?? []), ...(info.untrackedFiles ?? [])];
			if (files.length === 0) { return; }
			await backend.gitStage(files);
		} catch { /* backend not ready */ }
	}
});

registerAction2(class WescodeGitCommit extends Action2 {
	constructor() {
		super({
			id: 'wescode.gitCommit',
			title: localize2('wescodeGitCommit', "提交 AI 修改"),
		});
	}
	async run(accessor: ServicesAccessor) {
		const backend = accessor.get(IWescodeBackendService);
		const quickInput = accessor.get(IQuickInputService);
		const message = await quickInput.input({
			title: '提交 AI 修改',
			placeHolder: '输入提交消息...',
		});
		if (!message) { return; }
		try {
			await backend.gitCommit(message);
		} catch (err) {
			const msg = err instanceof Error ? err.message : String(err);
			await quickInput.pick([{ label: `错误: ${msg}` }], { title: '提交失败' });
		}
	}
});

// --- LSP Bridge: forward backend LSP requests to IDE language servers ---

import './lsp/lspBridge.js';

// --- AI Quick Fix: code action provider for wescode-agent diagnostics ---

import './codeActions/aiQuickFix.js';

// --- Debug Bridge: forward breakpoint hits to backend ---

import './debug/debugBridge.js';
import './debug/debugActionHandler.js';

// --- Test Bridge: forward test results to backend ---

import './tests/testBridge.js';

// --- Context Snapshot Logger: record context assembly details for debugging ---

class WescodeContextSnapshotLogger extends Disposable {
	static readonly ID = 'wescode.contextSnapshotLogger';

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ILogService private readonly logService: ILogService,
	) {
		super();
		this._register(this.backend.onDidContextSnapshot((snapshot: any) => {
			const fragments = snapshot?.fragments?.length ?? 0;
			const tokensUsed = snapshot?.tokensUsed ?? snapshot?.tokenUsed ?? 0;
			this.logService.info(`[wescode-context] snapshot: ${fragments} fragments, ${tokensUsed} tokens`);
		}));
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeContextSnapshotLogger, LifecyclePhase.Restored);
