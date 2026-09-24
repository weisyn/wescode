/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

/**
 * AI run-state status bar entry (Ready / Thinking / Working / …) plus optional
 * WES billing debt indicator: when `getWesBilling` reports enabled && !active,
 * appends yellow `$(warning) WES 欠费` (kind: warning). Refresh on a 60s timer,
 * provider change, window visibility, and engine health recovery so boot races
 * do not leave `_wesStatus` stuck at `none`. Orthogonal to workspaceCellBar /
 * indexProgressBar (「AI 就绪」/「AI 理解度」).
 */
import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IStatusbarService, StatusbarAlignment, IStatusbarEntryAccessor } from '../../../../services/statusbar/browser/statusbar.js';
import { IWescodeBackendService, IWescodeStreamEvent } from '../../../../../platform/wescode/common/wescode.js';

const enum AIState {
	Ready = 'ready',
	Thinking = 'thinking',
	Working = 'working',
	Verifying = 'verifying',
	Error = 'error',
	Unhealthy = 'unhealthy',
}

interface TokenStats {
	inputTokens: number;
	outputTokens: number;
	cacheTokens: number;
	totalTurns: number;
	elapsedMs: number;
}

const EMPTY_TOKENS: TokenStats = { inputTokens: 0, outputTokens: 0, cacheTokens: 0, totalTurns: 0, elapsedMs: 0 };

export class WescodeAIStatusBar extends Disposable {
	static readonly ID = 'wescode.aiStatusBar';

	private readonly _entry: IStatusbarEntryAccessor;
	private _state: AIState = AIState.Ready;
	private _lastTokens: TokenStats = { ...EMPTY_TOKENS };
	private _activeRequestId: string | undefined;
	private _errorRecoveryTimer: ReturnType<typeof setTimeout> | undefined;
	private _bgTaskCount = 0;
	private _unhealthyReason: string | undefined;
	private _wesStatus: 'ok' | 'debt' | 'none' = 'none';
	/** Retries getWesBilling after boot races; provider/visibility alone miss early debt. */
	private _wesBillingTimer: ReturnType<typeof setInterval> | undefined;

	constructor(
		@IStatusbarService statusbarService: IStatusbarService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
	) {
		super();

		this._entry = this._register(
			statusbarService.addEntry(
				this._buildEntry(),
				WescodeAIStatusBar.ID,
				StatusbarAlignment.LEFT,
				-1000,
			)
		);

		this._register(this.backend.onDidStreamEvent(ev => this._onStreamEvent(ev)));
		this._register(this.backend.onDidVerificationProgress(ev => this._onVerificationProgress(ev)));
		this._register(this.backend.onDidBackgroundComplete(() => this._refreshBgTasks()));
		this._register(this.backend.onDidEngineHealthChange(state => {
			if (!state.healthy) {
				this._unhealthyReason = state.reason || 'Engine offline';
				this._transition(AIState.Unhealthy);
			} else {
				this._unhealthyReason = undefined;
				if (this._state === AIState.Unhealthy) {
					this._transition(AIState.Ready);
				}
				// Backend just became reachable — re-probe billing (boot race).
				this._refreshWesBilling();
			}
		}));
		this._refreshBgTasks();
		this._refreshWesBilling();
		this._wesBillingTimer = setInterval(() => this._refreshWesBilling(), 60_000);
		this._register(this.backend.onDidProviderChange(() => this._refreshWesBilling()));
		const onVisibility = () => {
			if (typeof document !== 'undefined' && document.visibilityState === 'visible') {
				this._refreshWesBilling();
			}
		};
		if (typeof document !== 'undefined') {
			document.addEventListener('visibilitychange', onVisibility);
			this._register({ dispose: () => document.removeEventListener('visibilitychange', onVisibility) });
		}
	}

	private _refreshWesBilling(): void {
		this.backend.getWesBilling().then(b => {
			const prev = this._wesStatus;
			if (!b || !b.enabled) {
				this._wesStatus = 'none';
			} else if (b.active === false) {
				this._wesStatus = 'debt';
			} else {
				this._wesStatus = 'ok';
			}
			if (this._wesStatus !== prev) {
				this._entry.update(this._buildEntry());
			}
		}).catch(() => { /* backend not ready */ });
	}

	private _refreshBgTasks(): void {
		this.backend.listBackgroundTasks().then(tasks => {
			const running = tasks.filter(t => t.status === 'running').length;
			if (running !== this._bgTaskCount) {
				this._bgTaskCount = running;
				this._entry.update(this._buildEntry());
			}
		}).catch(() => { /* backend not ready */ });
	}

	private _onStreamEvent(ev: IWescodeStreamEvent): void {
		// While engine is unhealthy, keep the Unhealthy state pinned even when
		// stray stream events arrive from an in-flight run.
		if (this._state === AIState.Unhealthy) {
			return;
		}
		if (ev.requestId && ev.requestId !== this._activeRequestId) {
			this._activeRequestId = ev.requestId;
			this._lastTokens = { ...EMPTY_TOKENS };
		}

		switch (ev.type) {
			case 'thinking':
				this._transition(AIState.Thinking);
				break;
			case 'thinking.done':
			case 'text.delta':
				this._transition(AIState.Working);
				break;
			case 'tool.start':
			case 'tool.done':
				if (ev.toolCall?.tool === 'quality_gate' || ev.toolCall?.tool === 'compile' || ev.toolCall?.tool === 'test') {
					this._transition(AIState.Verifying);
				} else {
					this._transition(AIState.Working);
				}
				break;
			case 'run.end':
				if (ev.tokenUsage) {
					this._lastTokens = {
						inputTokens: ev.tokenUsage.inputTokens,
						outputTokens: ev.tokenUsage.outputTokens,
						cacheTokens: ev.tokenUsage.cacheTokens ?? 0,
						totalTurns: ev.tokenUsage.totalTurns ?? 0,
						elapsedMs: ev.tokenUsage.elapsedMs ?? 0,
					};
					this._entry.update(this._buildEntry());
				}
				break;
		case 'done':
		case 'interrupted':
			this._transition(AIState.Ready);
			this._activeRequestId = undefined;
			break;
		case 'error':
				if (!ev.error?.recoverable) {
					this._transition(AIState.Error);
					if (this._errorRecoveryTimer) { clearTimeout(this._errorRecoveryTimer); }
					this._errorRecoveryTimer = setTimeout(() => {
						this._errorRecoveryTimer = undefined;
						if (this._state === AIState.Error) {
							this._transition(AIState.Ready);
						}
					}, 5000);
				}
				break;
		}
	}

	private _verifyDetail: string = '';

	private _onVerificationProgress(ev: { phase: string; detail: string }): void {
		if (ev.phase === 'done') {
			this._verifyDetail = '';
			this._transition(AIState.Working);
		} else {
			this._verifyDetail = ev.detail;
			this._transition(AIState.Verifying);
		}
	}

	private _transition(next: AIState): void {
		if (this._state === next) {
			return;
		}
		this._state = next;
		this._entry.update(this._buildEntry());
	}

	private _buildEntry() {
		const { icon, label, showProgress } = statePresentation(this._state);
		const totalTokens = this._lastTokens.inputTokens + this._lastTokens.outputTokens;

		let text = `${icon} ${label}`;
		if (this._state === AIState.Ready && totalTokens > 0) {
			text = `${icon} ${formatTokens(totalTokens)} tokens`;
		} else if (this._state === AIState.Verifying && this._verifyDetail) {
			text = `${icon} ${this._verifyDetail}`;
		} else if (this._state === AIState.Unhealthy && this._unhealthyReason) {
			text = `${icon} ${label}`;
		}

		if (this._bgTaskCount > 0) {
			text += ` $(sync~spin) ${this._bgTaskCount} bg`;
		}
		if (this._wesStatus === 'debt') {
			text += ' $(warning) WES 欠费';
		}

		const tooltipParts = [`WES Code AI: ${label}`];
		if (this._wesStatus === 'ok') {
			tooltipParts.push('WES: OK');
		} else if (this._wesStatus === 'debt') {
			tooltipParts.push('WES: 欠费 — 平台模型已暂停，可切换自有模型继续使用');
		}
		if (this._state === AIState.Unhealthy && this._unhealthyReason) {
			tooltipParts.push(`Reason: ${this._unhealthyReason}`);
			tooltipParts.push('Engine health check timed out — new chats may fail');
		}
		if (this._bgTaskCount > 0) {
			tooltipParts.push(`Background tasks: ${this._bgTaskCount} running`);
		}
		if (totalTokens > 0) {
			tooltipParts.push(`Input: ${formatTokens(this._lastTokens.inputTokens)}, Output: ${formatTokens(this._lastTokens.outputTokens)}`);
			if (this._lastTokens.cacheTokens > 0) {
				tooltipParts.push(`Cache: ${formatTokens(this._lastTokens.cacheTokens)}`);
			}
			if (this._lastTokens.totalTurns > 0) {
				tooltipParts.push(`Turns: ${this._lastTokens.totalTurns}`);
			}
			if (this._lastTokens.elapsedMs > 0) {
				tooltipParts.push(`Time: ${(this._lastTokens.elapsedMs / 1000).toFixed(1)}s`);
			}
		}

		return {
			name: 'WES Code AI',
			text,
			ariaLabel: tooltipParts.join(' | '),
			tooltip: tooltipParts.join('\n'),
			showProgress,
			kind: (this._state === AIState.Error || this._state === AIState.Unhealthy) ? 'error' as const
				: this._wesStatus === 'debt' ? 'warning' as const
				: 'standard' as const,
			command: 'wescode.showEditMetrics',
		};
	}

	override dispose(): void {
		if (this._errorRecoveryTimer) {
			clearTimeout(this._errorRecoveryTimer);
		}
		if (this._wesBillingTimer) {
			clearInterval(this._wesBillingTimer);
			this._wesBillingTimer = undefined;
		}
		super.dispose();
	}
}

function statePresentation(state: AIState): { icon: string; label: string; showProgress: boolean | 'loading' | 'syncing' } {
	switch (state) {
		case AIState.Ready:
			return { icon: '$(sparkle)', label: 'AI Ready', showProgress: false };
		case AIState.Thinking:
			return { icon: '$(sparkle)', label: 'Thinking', showProgress: 'loading' };
		case AIState.Working:
			return { icon: '$(sparkle)', label: 'Working', showProgress: 'syncing' };
		case AIState.Verifying:
			return { icon: '$(shield)', label: 'Verifying', showProgress: 'syncing' };
		case AIState.Error:
			return { icon: '$(error)', label: 'Error', showProgress: false };
		case AIState.Unhealthy:
			return { icon: '$(warning)', label: 'Offline', showProgress: false };
	}
}

function formatTokens(n: number): string {
	if (n >= 1_000_000) { return `${(n / 1_000_000).toFixed(1)}M`; }
	if (n >= 1_000) { return `${(n / 1_000).toFixed(1)}K`; }
	return `${n}`;
}
