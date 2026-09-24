/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { $, append } from '../../../../../base/browser/dom.js';
import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService, IConversationItem, IAgentInfo } from '../../../../../platform/wescode/common/wescode.js';
import { createDecorator } from '../../../../../platform/instantiation/common/instantiation.js';
import { IStorageService, StorageScope, StorageTarget } from '../../../../../platform/storage/common/storage.js';
import { IWescodeWebviewEditorService } from '../editors/agentEditorService.js';
import { wescodeL } from '../../common/wescodeLocale.js';

export const IWescodeHeaderTabsService = createDecorator<IWescodeHeaderTabsService>('wescodeHeaderTabsService');

export interface IWescodeHeaderTabsService {
	readonly _serviceBrand: undefined;
	mountTo(container: HTMLElement): void;
}

/**
 * Manages the session tab strip rendered in the AuxiliaryBar header.
 *
 * Invariants:
 *   - At most ONE empty tab (sessionId='') exists at any time.
 *   - Empty tab represents "ready to start a new conversation".
 *   - When user sends first message, backend creates a real session and fires
 *     onDidActiveConversationChange(realId) — the empty tab is promoted in-place.
 *   - Tab titles refresh automatically when a stream completes (backend generates title).
 */
export class WescodeHeaderTabsService extends Disposable implements IWescodeHeaderTabsService {
	declare readonly _serviceBrand: undefined;

	private tabStrip!: HTMLElement;
	private openTabs: IConversationItem[] = [];
	private activeSessionId = '';
	/** True between _restoreState() and its deferred switchConversation().
	 *  Cleared by any explicit user action (新建 / tab click / close) so the
	 *  pending restore cannot yank the user back to the restored session. */
	private _restoredSessionOverridePending = false;
	private conversations: IConversationItem[] = [];

	private static readonly MAX_TABS = 5;
	private static readonly STORAGE_KEY = 'wescode.headerTabs.state';
	private static readonly STREAM_TIMEOUT_MS = 60_000;
	private _restored = false;
	private _streamingSessionId = '';
	private _streamingTimeout: ReturnType<typeof setTimeout> | undefined;
	private _terminalProcessedSessions = new Set<string>();

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IStorageService private readonly storageService: IStorageService,
		@IWescodeWebviewEditorService private readonly webviewEditor: IWescodeWebviewEditorService,
	) {
		super();

		this._register(this.webviewEditor.onDidLocaleChange(() => {
			this._render();
		}));

		this._register(this.backend.onDidActiveConversationChange(sessionId => {
			const prevSessionId = this.activeSessionId;
			if (sessionId === prevSessionId) { return; }
			this.activeSessionId = sessionId;
			if (prevSessionId && sessionId && prevSessionId !== sessionId) {
				if (!this._streamingSessionId || this._streamingSessionId === prevSessionId) {
					this._streamingSessionId = sessionId;
				}
			}
			this._onSessionChange(sessionId, prevSessionId);
			void this._refreshFromBackend();
		}));

		this._register(this.backend.onDidStreamEvent(event => {
			if (event.type === 'done' || event.type === 'interrupted' || event.type === 'error') {
				// The engine may emit interrupted + done (or fallback done)
				// back-to-back for the same session. Per-session dedup ensures
				// concurrent sessions each get their terminal event processed
				// while duplicates within the same session are dropped.
				const sid = event.sessionId || this.activeSessionId || '';
				if (this._terminalProcessedSessions.has(sid)) {
					return;
				}
				this._terminalProcessedSessions.add(sid);
				this._clearStreaming();
				void this._refreshFromBackend();
				return;
			}
			// Non-terminal event: clear the dedup guard for this session
			// so its eventual terminal event will be processed.
			const sid = event.sessionId || this.activeSessionId || '';
			this._terminalProcessedSessions.delete(sid);
			if (!this._streamingSessionId && this.activeSessionId) {
				this._streamingSessionId = this.activeSessionId;
				this._render();
			}
			this._resetStreamingTimeout();
		}));

		this._register(this.backend.onDidActiveAgentChange(agent => {
			this._onAgentChange(agent);
		}));

		// INV-TABS-READY-01: Tab titles depend on backend conversation metadata.
		// @inv-require: onDidEngineHealthChange
		// @inv-severity: minor
		// At mount time the backend is usually not ready (_refreshFromBackend
		// fails silently), so titles fall back to "新会话". This listener
		// ensures we re-fetch once the backend becomes healthy.
		//
		// Replay guard: onDidEngineHealthChange only fires on transitions. If
		// the constructor runs AFTER the backend is already healthy (e.g. slow
		// window restore), the event has already passed. mountTo's initial
		// _refreshFromBackend() covers that case — if it also fails, the next
		// onDidActiveConversationChange (user sends a message) will trigger
		// another refresh. Three complementary paths, at least one will hit.
		this._register(this.backend.onDidEngineHealthChange(state => {
			if (state.healthy) {
				void this._refreshFromBackend();
			}
		}));

		this._register({ dispose: () => { if (this._streamingTimeout !== undefined) { clearTimeout(this._streamingTimeout); } } });
	}

	mountTo(container: HTMLElement): void {
		container.style.cssText = 'flex:1; display:flex; align-items:center; gap:1px; min-width:0; overflow:hidden;';

		this.tabStrip = append(container, $('div.wescode-tab-strip'));
		this.tabStrip.style.cssText = 'flex:1; display:flex; align-items:center; gap:1px; overflow-x:auto; min-width:0; scrollbar-width:none;';

		this.tabStrip.addEventListener('wheel', (e) => {
			if (e.deltaX !== 0) {
				return; // native horizontal scroll (trackpad) — let it work
			}
			if (e.deltaY !== 0) {
				e.preventDefault();
				this.tabStrip.scrollLeft += e.deltaY;
			}
		}, { passive: false });

		const newBtn = append(container, $('button.wescode-tab-new')) as HTMLButtonElement;
		newBtn.title = wescodeL('new_session') + ' (Ctrl+T)';
		newBtn.style.cssText = 'flex:none; width:24px; height:24px; border:none; border-radius:4px; background:transparent; color:var(--vscode-descriptionForeground); cursor:pointer; display:flex; align-items:center; justify-content:center; transition:background 0.12s, color 0.12s;';
		const addIcon = append(newBtn, $('span'));
		addIcon.className = 'codicon codicon-add';
		addIcon.style.cssText = 'font-size:14px;';
		newBtn.addEventListener('mouseenter', () => { newBtn.style.background = 'var(--vscode-toolbar-hoverBackground)'; newBtn.style.color = 'var(--vscode-foreground)'; });
		newBtn.addEventListener('mouseleave', () => { newBtn.style.background = 'transparent'; newBtn.style.color = 'var(--vscode-descriptionForeground)'; });
		newBtn.addEventListener('click', () => { this._onNewTabClick(); });

		this._restoreState();
		this._render();
		void this._refreshFromBackend();
	}

	// ── Core logic ────────────────────────────────────────────────────────────

	/**
	 * [+] button clicked. Semantics: "give me a fresh empty conversation".
	 * Always requests a new (empty) conversation — even when already on the
	 * empty tab — so the click re-fires onDidActiveConversationChange('') and
	 * the chat pane always gives visible feedback (clears the message area).
	 */
	private _onNewTabClick(): void {
		// Explicit user action — cancel any pending state restore.
		this._restoredSessionOverridePending = false;
		// Always request a fresh conversation, even when already on the empty
		// tab. backend.newConversation() is idempotent: it re-fires
		// onDidActiveConversationChange(''), which chatViewPane now handles for
		// empty→empty too, so the click always produces visible feedback
		// (clearMessages) instead of being silently swallowed.
		this.backend.newConversation();
	}

	private _onSessionChange(sessionId: string, prevSessionId: string): void {
		// Switching away from a streaming session means the stream was
		// abandoned from the user's perspective — clear the spinner so it
		// doesn't spin indefinitely if the terminal event is lost.
		if (this._streamingSessionId && this._streamingSessionId !== sessionId) {
			this._clearStreaming();
		}
		if (!sessionId) {
			this._ensureEmptyTab();
		} else {
			this._promoteOrAdd(sessionId, prevSessionId);
		}
		this._enforceMaxTabs();
		this._render();
		this._persistState();
	}

	private _onAgentChange(agent: IAgentInfo | null): void {
		// The role has to follow onto whichever tab is active, not just the
		// empty one. tab.agentId is what switchConversation replays when the
		// user returns to this tab, so a session tab left holding the role its
		// first turn ran under quietly reverts the picker to that role.
		// Only the empty tab shows the role as its title; a session tab keeps
		// its conversation title.
		const activeTab = this.openTabs.find(t => this._isActive(t));
		if (!activeTab) { return; }
		activeTab.agentId = agent?.id ?? '';
		if (!activeTab.sessionId) {
			activeTab.title = agent ? agent.name : wescodeL('new_session');
		}
		this._render();
		this._persistState();
	}

	private _clearStreaming(): void {
		this._streamingSessionId = '';
		if (this._streamingTimeout !== undefined) {
			clearTimeout(this._streamingTimeout);
			this._streamingTimeout = undefined;
		}
		this._render();
	}

	private _resetStreamingTimeout(): void {
		if (this._streamingTimeout !== undefined) {
			clearTimeout(this._streamingTimeout);
		}
		this._streamingTimeout = setTimeout(() => {
			if (this._streamingSessionId) {
				this._streamingSessionId = '';
				this._render();
			}
			this._streamingTimeout = undefined;
		}, WescodeHeaderTabsService.STREAM_TIMEOUT_MS);
	}

	private _ensureEmptyTab(): void {
		const hasEmpty = this.openTabs.some(t => !t.sessionId);
		if (!hasEmpty) {
			this.openTabs.push(this._emptyItem());
		}
	}

	private _promoteOrAdd(sessionId: string, prevSessionId: string): void {
		if (this.openTabs.some(t => t.sessionId === sessionId)) {
			return;
		}
		// Empty tab → first message creates real session: replace in-place.
		const emptyIdx = this.openTabs.findIndex(t => !t.sessionId);
		if (emptyIdx >= 0 && !prevSessionId) {
			this.openTabs[emptyIdx] = this._itemFor(sessionId);
			return;
		}
		// INV-SESSION-ID-01: the backend never reassigns a session id mid-run
		// (internal group/sub-session namespaces no longer leak into the public
		// session identity), so an unknown session here is simply a new
		// conversation started elsewhere — append it.
		this.openTabs.push(this._itemFor(sessionId));
	}

	private _enforceMaxTabs(): void {
		while (this.openTabs.length > WescodeHeaderTabsService.MAX_TABS) {
			// Remove the first non-active tab; NEVER remove the empty tab.
			const victimIdx = this.openTabs.findIndex(t =>
				t.sessionId !== this.activeSessionId && t.sessionId !== ''
			);
			if (victimIdx >= 0) {
				this.openTabs.splice(victimIdx, 1);
			} else {
				break;
			}
		}
	}

	private _itemFor(sessionId: string): IConversationItem {
		const found = this.conversations.find(c => c.sessionId === sessionId);
		return found ?? { sessionId, agentId: '', title: '', lastMessage: '', lastTime: '' };
	}

	private _emptyItem(): IConversationItem {
		return { sessionId: '', agentId: '', title: wescodeL('new_session'), lastMessage: '', lastTime: '' };
	}

	// ── Persistence ──────────────────────────────────────────────────────────

	private _restoreState(): void {
		const raw = this.storageService.get(WescodeHeaderTabsService.STORAGE_KEY, StorageScope.WORKSPACE);
		this._restored = true;
		if (!raw) { return; }
		try {
			const state = JSON.parse(raw) as { tabs?: unknown[]; active?: string };
			if (!Array.isArray(state.tabs) || state.tabs.length === 0) { return; }

			// Support both old format (string[]) and new format ({sid,aid}[])
			this.openTabs = state.tabs.map(entry => {
				if (typeof entry === 'string') {
					return entry ? this._itemFor(entry) : this._emptyItem();
				}
				const e = entry as { sid?: string; aid?: string };
				if (!e.sid) { return this._emptyItem(); }
				const item = this._itemFor(e.sid);
				if (e.aid) { item.agentId = e.aid; }
				return item;
			});
			this.activeSessionId = state.active || '';
			if (this.activeSessionId) {
				const activeTab = this.openTabs.find(t => t.sessionId === this.activeSessionId);
				const aid = activeTab?.agentId;
				// Defer one tick so the chat pane can register its listener.
				// Race guard: if the user clicks 新建 / a tab / close before the
				// deferred switchConversation runs, the pending restore is
				// cancelled — otherwise the restore would fire LAST and yank the
				// user back to the restored session ("新建无效").
				this._restoredSessionOverridePending = true;
				setTimeout(() => {
					if (!this._restoredSessionOverridePending) { return; }
					this._restoredSessionOverridePending = false;
					this.backend.switchConversation(this.activeSessionId, aid);
				}, 0);
			}
		} catch {
			// Storage corrupted — fall through to default empty tab
		}
	}

	private _persistState(): void {
		if (!this._restored) { return; }
		this.storageService.store(
			WescodeHeaderTabsService.STORAGE_KEY,
			JSON.stringify({
				tabs: this.openTabs.map(t => ({ sid: t.sessionId, aid: t.agentId })),
				active: this.activeSessionId,
			}),
			StorageScope.WORKSPACE,
			StorageTarget.MACHINE,
		);
	}

	// ── Backend sync ──────────────────────────────────────────────────────────

	private async _refreshFromBackend(): Promise<void> {
		try {
			this.conversations = await this.backend.listConversations();
		} catch (err) {
			console.warn('[wescode-tabs] listConversations failed, keeping current tabs:', err);
			return;
		}
		const backendIds = new Set(this.conversations.map(c => c.sessionId));
		const tabsWithSessions = this.openTabs.filter(t => !!t.sessionId);

		// Guard: if backend returns empty but we have tabs with real sessions,
		// keep current tabs to avoid wiping user state. With cross-Cell queries
		// this should only happen if ALL cells genuinely have zero sessions.
		if (backendIds.size === 0 && tabsWithSessions.length > 0) {
			console.warn('[wescode-tabs] all cells returned 0 conversations but', tabsWithSessions.length, 'tabs have sessions — keeping current tabs');
			return;
		}

		const seen = new Set<string>();
		const deduped: IConversationItem[] = [];
		for (let i = 0; i < this.openTabs.length; i++) {
			const tab = this.openTabs[i];
			if (!tab.sessionId) {
				deduped.push(tab);
				continue;
			}
			if (seen.has(tab.sessionId)) {
				continue;
			}
			seen.add(tab.sessionId);
			// Update metadata (title, lastTime) from backend if available,
			// but keep the tab even if backend doesn't list it — the session
			// may be beyond the 50-item query limit or the backend may be
			// querying a different Cell temporarily.
			const updated = this.conversations.find(c => c.sessionId === tab.sessionId);
			deduped.push(updated ?? tab);
		}
		this.openTabs = deduped;
		this._render();
		this._persistState();
	}

	// ── Rendering ─────────────────────────────────────────────────────────────

	private _render(): void {
		if (!this.tabStrip) { return; }
		this.tabStrip.textContent = '';

		// Guarantee at least one tab
		if (this.openTabs.length === 0) {
			this.openTabs.push(this._emptyItem());
		}

		let activeEl: HTMLElement | undefined;
		for (const tab of this.openTabs) {
			const isActive = this._isActive(tab);
			const el = this._createTabEl(tab, isActive);
			this.tabStrip.appendChild(el);
			if (isActive) { activeEl = el; }
		}

		if (activeEl) {
			requestAnimationFrame(() => activeEl.scrollIntoView({ block: 'nearest', inline: 'nearest' }));
		}
	}

	private _isActive(tab: IConversationItem): boolean {
		if (!tab.sessionId && !this.activeSessionId) { return true; }
		return tab.sessionId === this.activeSessionId;
	}

	private _createTabEl(tab: IConversationItem, isActive: boolean): HTMLElement {
		const isStreaming = !!tab.sessionId && tab.sessionId === this._streamingSessionId;
		const el = document.createElement('div');
		el.className = 'wescode-tab';
		el.style.cssText = `
			flex:0 0 auto; max-width:160px; height:26px; padding:0 6px 0 8px;
			border-radius:6px 6px 0 0; display:flex; align-items:center; gap:4px;
			font-size:11px; cursor:pointer; white-space:nowrap; overflow:hidden;
			transition:background 0.12s;
			background:${isActive ? 'var(--vscode-tab-activeBackground, var(--vscode-editor-background))' : 'transparent'};
			color:${isActive ? 'var(--vscode-tab-activeForeground, var(--vscode-foreground))' : 'var(--vscode-tab-inactiveForeground, var(--vscode-descriptionForeground))'};
		`.replace(/\n\t+/g, ' ');

		// Status icon: spinning for streaming, chat bubble otherwise
		const statusIcon = document.createElement('span');
		statusIcon.style.cssText = 'flex:none; font-size:11px; display:flex; align-items:center;';
		if (isStreaming) {
			statusIcon.className = 'codicon codicon-loading wescode-tab-spinning';
		} else {
			statusIcon.className = 'codicon codicon-comment-discussion';
			statusIcon.style.opacity = isActive ? '1' : '0.6';
		}
		el.appendChild(statusIcon);

		const titleSpan = document.createElement('span');
		titleSpan.style.cssText = 'flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis;';
		titleSpan.textContent = tab.title || wescodeL('new_session');
		titleSpan.title = tab.title || wescodeL('new_session');
		el.appendChild(titleSpan);

		// Cross-workspace history sessions show their source workspace so the
		// user can distinguish which Cell a session belongs to before opening
		// it (INV-CROSSCELL-01 UI surface; backend auto-locates on load).
		if (tab.workspaceLabel && tab.isCurrentWorkspace !== true) {
			const wsSpan = document.createElement('span');
			wsSpan.style.cssText = 'flex:none; font-size:9px; line-height:14px; padding:0 4px; border-radius:3px; background:var(--vscode-badge-background, rgba(127,127,127,.2)); color:var(--vscode-descriptionForeground); overflow:hidden; text-overflow:ellipsis; max-width:80px;';
			wsSpan.textContent = tab.workspaceLabel;
			wsSpan.title = wescodeL('workspace_label') + tab.workspaceLabel;
			el.appendChild(wsSpan);
		}

		const closeBtn = document.createElement('span');
		closeBtn.className = 'codicon codicon-close';
		closeBtn.style.cssText = 'flex:none; font-size:10px; opacity:0; transition:opacity 0.12s; border-radius:3px; padding:2px; cursor:pointer;';
		closeBtn.title = wescodeL('close_tab');
		el.addEventListener('mouseenter', () => { closeBtn.style.opacity = '1'; if (!isActive) { el.style.background = 'var(--vscode-list-hoverBackground)'; } });
		el.addEventListener('mouseleave', () => { closeBtn.style.opacity = '0'; if (!isActive) { el.style.background = 'transparent'; } });
		closeBtn.addEventListener('click', (e) => { e.stopPropagation(); this._closeTab(tab); });
		el.appendChild(closeBtn);

		el.addEventListener('click', () => {
			// Explicit user action — cancel any pending state restore.
			this._restoredSessionOverridePending = false;
			if (!this._isActive(tab)) {
				if (tab.sessionId) {
					this.backend.switchConversation(tab.sessionId, tab.agentId);
				} else {
					this.backend.newConversation();
				}
			}
		});
		el.addEventListener('auxclick', (e) => { if (e.button === 1) { e.preventDefault(); this._closeTab(tab); } });

		return el;
	}

	private _closeTab(tab: IConversationItem): void {
		const idx = this.openTabs.indexOf(tab);
		if (idx < 0) { return; }
		this.openTabs.splice(idx, 1);

		if (this.openTabs.length === 0) {
			this.openTabs.push(this._emptyItem());
		}

		if (this._isActive(tab)) {
			const nextIdx = Math.min(idx, this.openTabs.length - 1);
			const next = this.openTabs[nextIdx];
			if (next.sessionId) {
				this.backend.switchConversation(next.sessionId, next.agentId);
			} else {
				this.backend.newConversation();
			}
		} else {
			this._render();
			this._persistState();
		}
	}
}
