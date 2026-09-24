/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *
 *  v1.0 PROD-1 — workspace-aware AI status.
 *  Renders a compact StatusBar entry indicating the AI engine's readiness
 *  for the currently open workspace. The engine internal name for a
 *  workspace is "Cell" (see wesgine AGENTS.md); that abstraction is
 *  deliberately NOT surfaced in the UI — users only see workspace names
 *  and human-friendly readiness state.
 *
 *  Behaviour:
 *    - Loading    → "$(sync~spin) AI 就绪中"
 *    - No folder  → entry is disposed (VS Code's native workspace title
 *                    already communicates that no folder is open)
 *    - Ready      → "$(check) AI 就绪"  with tooltip showing the
 *                    workspace folder name (never the internal cellID hash).
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IStatusbarService, StatusbarAlignment, IStatusbarEntryAccessor } from '../../../../services/statusbar/browser/statusbar.js';
import { IWescodeBackendService, IWescodeCellInfo } from '../../../../../platform/wescode/common/wescode.js';
import { basename } from '../../../../../base/common/path.js';

// Localisation strings are inlined (Chinese default matches wescode chat
// UI convention) to avoid a nls channel round trip in the status bar.
const L = {
	loadingLabel: '$(sync~spin) AI 就绪中',
	readyLabel: '$(check) AI 就绪',
	tooltipTitle: 'WES Code AI 助手',
	tooltipWorkspace: (name: string) => `工作区: ${name}`,
	tooltipPolicy: (name: string) => `治理策略: ${policyLabel(name)}`,
	tooltipReady: '打开文件夹后 AI 已就绪，可开始对话',
};

// Map internal engine TrustPolicy identifiers to human-friendly labels
// used in tooltip and QuickPick. Keeps the raw engine string out of any
// user-visible surface (PROD-1 alignment).
function policyLabel(name: string): string {
	switch ((name || '').toLowerCase()) {
		case 'coding': return '编程模式';
		case 'assistant': return '助手模式';
		case 'locked': return '锁定模式';
		case 'bench': return '基准模式';
		default: return name || '默认';
	}
}

export class WescodeWorkspaceCellBar extends Disposable {
	static readonly ID = 'wescode.workspaceCellBar';

	private readonly _statusbarService: IStatusbarService;
	private _entry: IStatusbarEntryAccessor | undefined;
	private _refreshTimer: ReturnType<typeof setTimeout> | undefined;

	private _initialized = false;

	constructor(
		@IStatusbarService statusbarService: IStatusbarService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
	) {
		super();
		this._statusbarService = statusbarService;

		// Pure event-driven refresh (INV-WV-02: no polling/timers for init).
		// The first probe fires ONLY after the engine reports healthy,
		// guaranteeing initialize() has returned and Config Mode is resolved.
		this._register(this.backend.onDidEngineHealthChange((e) => {
			if (e.healthy && !this._initialized) {
				this._initialized = true;
				this._refresh();
			} else if (e.healthy) {
				this._scheduleRefresh(300);
			} else {
				this._disposeEntry();
			}
		}));
	this._register(this.backend.onDidStreamEvent(ev => {
		if (ev.type === 'done' || ev.type === 'interrupted' || ev.type === 'error') {
			this._scheduleRefresh(3000);
		}
	}));

		// Show a brief loading indicator until the engine health event
		// fires. No RPC call here — we wait for the event.
		this._entry = this._createEntry(this._buildLoading());
	}

	private _createEntry(props: ReturnType<typeof this._buildReady> | ReturnType<typeof this._buildLoading>): IStatusbarEntryAccessor {
		return this._statusbarService.addEntry(
			props,
			WescodeWorkspaceCellBar.ID,
			StatusbarAlignment.LEFT,
			-999,
		);
	}

	private _scheduleRefresh(delayMs: number): void {
		if (this._refreshTimer) {
			clearTimeout(this._refreshTimer);
		}
		this._refreshTimer = setTimeout(() => {
			this._refreshTimer = undefined;
			this._refresh();
		}, delayMs);
	}

	private _disposeEntry(): void {
		if (this._entry) {
			this._entry.dispose();
			this._entry = undefined;
		}
	}

	private async _refresh(): Promise<void> {
		let info: IWescodeCellInfo | undefined;
		try {
			info = await this.backend.getCellInfo();
		} catch {
			info = undefined;
		}
		if (!info || !info.cellId) {
			this._disposeEntry();
			return;
		}
		const props = this._buildReady(info);
		if (this._entry) {
			this._entry.update(props);
		} else {
			this._entry = this._createEntry(props);
		}
	}

	private _buildLoading() {
		return {
			name: 'WES Code AI 状态',
			text: L.loadingLabel,
			ariaLabel: L.loadingLabel,
			tooltip: L.tooltipTitle,
			kind: 'standard' as const,
		};
	}

	private _buildReady(info: IWescodeCellInfo) {
		const wsName = info.workDir ? basename(info.workDir) : '(unknown)';
		const policy = policyLabel(info.trustPolicy || '');
		const tooltip = [
			L.tooltipTitle,
			L.tooltipWorkspace(wsName),
			L.tooltipPolicy(info.trustPolicy || ''),
			'',
			L.tooltipReady,
		].join('\n');
		return {
			name: 'WES Code AI 状态',
			text: L.readyLabel,
			ariaLabel: `AI 就绪 · 工作区 ${wsName} · 治理策略 ${policy}`,
			tooltip,
			kind: 'standard' as const,
			command: 'wescode.showWorkspaceCellDetails',
		};
	}

	override dispose(): void {
		if (this._refreshTimer) {
			clearTimeout(this._refreshTimer);
		}
		this._disposeEntry();
		super.dispose();
	}
}
