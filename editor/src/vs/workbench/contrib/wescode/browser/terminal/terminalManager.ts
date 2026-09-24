/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService, IWescodeTerminalCreateRequest } from '../../../../../platform/wescode/common/wescode.js';
import { ITerminalInstance, ITerminalService } from '../../../terminal/browser/terminal.js';
import { IWorkbenchContribution } from '../../../../common/contributions.js';
import { ITerminalBackend, ITerminalLaunchError } from '../../../../../platform/terminal/common/terminal.js';
import { ILogService } from '../../../../../platform/log/common/log.js';

const PTY_HOST_RESTART_WAIT_MS = 8000;
const MIRROR_DISPOSE_DELAY_MS = 3000;
const MIRROR_SAFETY_TIMEOUT_MS = 60_000;
const INTERACTIVE_IDLE_TIMEOUT_MS = 120_000;

/**
 * Manages VSCode integrated terminals created by the wesgine Agent.
 * Listens for `terminal/create` notifications from the Go backend and
 * creates real IDE terminals, forwarding output back to the backend
 * so the Agent can observe command results.
 *
 * Monitors ptyHost health: when ptyHost becomes unresponsive, actively
 * restarts it and waits for recovery before creating terminals.
 *
 * Every TerminalInstance created here MUST be disposed when no longer
 * needed — undisposed instances leak event listeners on global singleton
 * services (ContextKeyService, ThemeService, LifecycleService, etc.),
 * eventually exhausting file descriptors and crashing ptyHost.
 */
export class WescodeTerminalManager extends Disposable implements IWorkbenchContribution {

	static readonly ID = 'workbench.contrib.wescodeTerminalManager';

	private _isPtyHostResponsive = true;
	private _hasAttemptedRestart = false;
	private _backend: ITerminalBackend | undefined;
	private readonly _responsiveWaiters: (() => void)[] = [];
	private readonly _mirrorTerminals = new Map<string, ITerminalInstance>();
	private readonly _interactiveTerminals = new Map<string, ITerminalInstance>();
	private readonly _mirrorSafetyTimers = new Map<string, NodeJS.Timeout>();

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ITerminalService private readonly terminalService: ITerminalService,
		@ILogService private readonly logService: ILogService,
	) {
		super();

		this._register(this.backend.onDidTerminalCreate((req) => {
			this._handleTerminalCreate(req).catch(err => {
				this.logService.error(`[wescode-terminal] unhandled error in _handleTerminalCreate for session ${req.sessionId}`, err);
				this.backend.terminalExited(req.sessionId, -1).catch(() => {});
			});
		}));

		this._register(this.backend.onDidTerminalOutput((ev) => {
			const instance = this._mirrorTerminals.get(ev.sessionId);
			if (instance) {
				instance.sendText(ev.data, false);
			}
		}));

		this._register(this.backend.onDidTerminalExited((ev) => {
			this._disposeMirrorTerminal(ev.sessionId, ev.exitCode);
		}));

		this._watchPtyHostHealth().catch(err => {
			this.logService.error('[wescode-terminal] failed to initialize pty host health monitoring', err);
		});
	}

	private _disposeMirrorTerminal(sessionId: string, exitCode: number): void {
		const instance = this._mirrorTerminals.get(sessionId);
		if (!instance) {
			return;
		}
		const timer = this._mirrorSafetyTimers.get(sessionId);
		if (timer) {
			clearTimeout(timer);
			this._mirrorSafetyTimers.delete(sessionId);
		}
		instance.sendText(`\r\n[Process exited with code ${exitCode}]\r\n`, false);
		this._mirrorTerminals.delete(sessionId);
		setTimeout(() => {
			instance.dispose();
		}, MIRROR_DISPOSE_DELAY_MS);
	}

	private _scheduleMirrorSafetyTimeout(sessionId: string, instance: ITerminalInstance): void {
		const timer = setTimeout(() => {
			this._mirrorSafetyTimers.delete(sessionId);
			const live = this._mirrorTerminals.get(sessionId);
			if (live !== instance) {
				return; // already disposed or replaced
			}
			this.logService.warn(`[wescode-terminal] mirror session ${sessionId} still alive after ${MIRROR_SAFETY_TIMEOUT_MS}ms without exit notification, disposing`);
			this._mirrorTerminals.delete(sessionId);
			instance.dispose();
			this.backend.terminalExited(sessionId, -1).catch(() => {});
		}, MIRROR_SAFETY_TIMEOUT_MS);
		if (typeof timer.unref === 'function') {
			timer.unref();
		}
		this._mirrorSafetyTimers.set(sessionId, timer);
	}

	private _disposeInteractiveTerminal(sessionId: string): void {
		const instance = this._interactiveTerminals.get(sessionId);
		if (!instance) {
			return;
		}
		this._interactiveTerminals.delete(sessionId);
		setTimeout(() => {
			instance.dispose();
		}, MIRROR_DISPOSE_DELAY_MS);
	}

	override dispose(): void {
		for (const instance of this._mirrorTerminals.values()) {
			instance.dispose();
		}
		this._mirrorTerminals.clear();
		for (const instance of this._interactiveTerminals.values()) {
			instance.dispose();
		}
		this._interactiveTerminals.clear();
		super.dispose();
	}

	private async _watchPtyHostHealth(): Promise<void> {
		await this.terminalService.whenConnected;
		this._backend = this.terminalService.getPrimaryBackend();
		if (!this._backend) {
			this.logService.warn('[wescode-terminal] no primary backend available, pty health monitoring disabled');
			return;
		}

		this._isPtyHostResponsive = this._backend.isResponsive;

		this._register(this._backend.onPtyHostUnresponsive(() => {
			this._isPtyHostResponsive = false;
			this._hasAttemptedRestart = false;
			this.logService.warn('[wescode-terminal] pty host unresponsive');
		}));

		this._register(this._backend.onPtyHostResponsive(() => {
			this.logService.info('[wescode-terminal] pty host responsive');
			this._resolvePtyHostWaiters();
		}));

		this._register(this._backend.onPtyHostRestart(() => {
			this.logService.info('[wescode-terminal] pty host restarted');
			this._resolvePtyHostWaiters();
		}));

		if (!this._isPtyHostResponsive) {
			this.logService.warn('[wescode-terminal] pty host unresponsive at startup, restarting proactively');
			this._hasAttemptedRestart = true;
			try {
				this._backend.restartPtyHost();
			} catch (err) {
				this.logService.error('[wescode-terminal] proactive restartPtyHost failed', err);
			}
		}
	}

	private _resolvePtyHostWaiters(): void {
		this._isPtyHostResponsive = true;
		this._hasAttemptedRestart = false;
		const waiters = this._responsiveWaiters.splice(0);
		for (const resolve of waiters) {
			resolve();
		}
	}

	private _waitForPtyHost(timeoutMs: number): Promise<boolean> {
		if (this._isPtyHostResponsive) {
			return Promise.resolve(true);
		}
		return new Promise<boolean>(resolve => {
			const timer = setTimeout(() => {
				const idx = this._responsiveWaiters.indexOf(onReady);
				if (idx >= 0) {
					this._responsiveWaiters.splice(idx, 1);
				}
				resolve(false);
			}, timeoutMs);
			const onReady = () => {
				clearTimeout(timer);
				resolve(true);
			};
			this._responsiveWaiters.push(onReady);
		});
	}

	private async _tryRecoverPtyHost(): Promise<boolean> {
		if (this._isPtyHostResponsive) {
			return true;
		}

		if (!this._hasAttemptedRestart && this._backend) {
			this._hasAttemptedRestart = true;
			this.logService.info('[wescode-terminal] restarting pty host...');
			try {
				this._backend.restartPtyHost();
			} catch (err) {
				this.logService.error('[wescode-terminal] restartPtyHost threw', err);
			}
		}

		return this._waitForPtyHost(PTY_HOST_RESTART_WAIT_MS);
	}

	private async _handleTerminalCreate(req: IWescodeTerminalCreateRequest): Promise<void> {
		if (!this._isPtyHostResponsive) {
			this.logService.warn(`[wescode-terminal] pty host unresponsive, attempting recovery (session ${req.sessionId})`);
			const recovered = await this._tryRecoverPtyHost();
			if (!recovered) {
				this.logService.error(`[wescode-terminal] pty host recovery failed, failing session ${req.sessionId}`);
				this.backend.terminalExited(req.sessionId, -1).catch(() => {});
				return;
			}
			this.logService.info(`[wescode-terminal] pty host recovered, proceeding with session ${req.sessionId}`);
		}

		try {
			const label = req.label || `Agent: ${req.command.slice(0, 40)}`;
			const instance = await this.terminalService.createTerminal({
				config: {
					name: req.readOnly ? `📡 ${label}` : label,
					cwd: req.workDir,
					isTransient: true,
				},
			});

			if (req.readOnly) {
				instance.sendText('cat', true);
				this._mirrorTerminals.set(req.sessionId, instance);
				this._scheduleMirrorSafetyTimeout(req.sessionId, instance);
				return;
			}

			this._interactiveTerminals.set(req.sessionId, instance);
			instance.sendText(req.command, true);

			let idleTimer: NodeJS.Timeout | undefined;
			const armIdleTimer = () => {
				if (idleTimer) {
					clearTimeout(idleTimer);
				}
				idleTimer = setTimeout(() => {
					if (this._interactiveTerminals.has(req.sessionId)) {
						this.logService.warn(`[wescode-terminal] interactive session ${req.sessionId} idle for ${INTERACTIVE_IDLE_TIMEOUT_MS}ms without exit, disposing`);
						this._interactiveTerminals.delete(req.sessionId);
						dataListener.dispose();
						exitListener.dispose();
						instance.dispose();
						this.backend.terminalExited(req.sessionId, -1).catch(() => {});
					}
				}, INTERACTIVE_IDLE_TIMEOUT_MS);
				if (typeof idleTimer.unref === 'function') {
					idleTimer.unref();
				}
			};

			const dataListener = instance.onData((data: string) => {
				this.backend.terminalOutput(req.sessionId, data).catch(() => {});
				armIdleTimer();
			});

			const exitListener = instance.onExit((e: number | ITerminalLaunchError | undefined) => {
				if (idleTimer) {
					clearTimeout(idleTimer);
				}
				const exitCode = typeof e === 'number' ? e : -1;
				this.backend.terminalExited(req.sessionId, exitCode).catch(() => {});
				dataListener.dispose();
				exitListener.dispose();
				this._disposeInteractiveTerminal(req.sessionId);
			});

			armIdleTimer();
		} catch (err) {
			this.logService.error(`[wescode-terminal] createTerminal failed for session ${req.sessionId}`, err);
			this.backend.terminalExited(req.sessionId, -1).catch(() => {});
		}
	}
}
