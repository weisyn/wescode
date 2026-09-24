/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';
import { IDebugService, IDebugSession, State as DebugState } from '../../../../contrib/debug/common/debug.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';
import { ILogService } from '../../../../../platform/log/common/log.js';

interface DebugEventPayload {
	kind: 'breakpoint_hit' | 'exception' | 'terminated' | 'output' | 'process_exit';
	sessionId: string;
	timestamp: number;
	threadId?: number;
	stopReason?: string;
	frames?: Array<{ name: string; source: string; line: number; column: number; moduleId?: string }>;
	variables?: Array<{ scope: string; name: string; value: string; type: string; childCount: number }>;
	exception?: { id: string; description: string; stackTrace: string };
	output?: { category: string; text: string };
	exitCode?: number;
}

/**
 * WescodeDebugSensor (D1) — unified debug event capture.
 *
 * Replaces the old WescodeDebugBridge. Monitors all VSCode debug lifecycle
 * events and forwards structured DebugEvent payloads to the Go backend via
 * the `debug/event` RPC.
 */
export class WescodeDebugBridge extends Disposable {
	static readonly ID = 'wescode.debugBridge';

	private readonly _trackedSessions = new Set<string>();

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IDebugService private readonly debugService: IDebugService,
		@ILogService private readonly logService: ILogService,
	) {
		super();

		this._register(this.debugService.onDidChangeState(state => {
			if (state === DebugState.Stopped) {
				this._onStopped();
			}
		}));

		this._register(this.debugService.onDidNewSession(session => {
			this._trackSession(session);
		}));

		this._register(this.debugService.onDidEndSession(evt => {
			this._trackedSessions.delete(evt.session.getId());
			this._send({
				kind: 'terminated',
				sessionId: evt.session.getId(),
				timestamp: Date.now(),
			});
		}));
	}

	private _trackSession(session: IDebugSession): void {
		const id = session.getId();
		if (this._trackedSessions.has(id)) { return; }
		this._trackedSessions.add(id);

		// Subscribe to DAP custom events to capture output events.
		this._register(session.onDidCustomEvent(event => {
			if (event.event !== 'output') { return; }
			const body = event.body as { category?: string; output?: string } | undefined;
			const category = body?.category ?? 'console';
			const text = body?.output ?? '';
			if (!text.trim()) { return; }
			this._send({
				kind: 'output',
				sessionId: id,
				timestamp: Date.now(),
				output: { category, text: text.slice(0, 2000) },
			});
		}));
	}

	private async _onStopped(): Promise<void> {
		const thread = this.debugService.getViewModel().focusedThread;
		if (!thread) { return; }

		const session = this.debugService.getViewModel().focusedSession;
		const sessionId = session?.getId() ?? '';

		const callStack = await thread.getCallStack();
		if (callStack.length === 0) { return; }

		const topFrame = callStack[0];
		const frames = callStack.slice(0, 20).map(f => ({
			name: f.name,
			source: f.source?.uri?.fsPath ?? '',
			line: f.range.startLineNumber,
			column: f.range.startColumn,
		}));

		const variables: DebugEventPayload['variables'] = [];
		try {
			const scopes = await topFrame.getScopes();
			for (const scope of scopes) {
				const scopeName = scope.name.toLowerCase();
				if (scopeName !== 'local' && scopeName !== 'locals' && scopeName !== 'closure') {
					continue;
				}
				const scopeLabel = scopeName === 'closure' ? 'closure' : 'local';
				const children = await scope.getChildren();
				for (const child of children.slice(0, 30)) {
					variables.push({
						scope: scopeLabel,
						name: child.name,
						value: child.value,
						type: child.type ?? '',
						childCount: child.hasChildren ? 1 : 0,
					});
				}
			}
		} catch {
			// best-effort variable collection
		}

		// Detect stop reason: check if there's an exception
		let kind: DebugEventPayload['kind'] = 'breakpoint_hit';
		let stopReason = 'breakpoint';
		let exceptionInfo: DebugEventPayload['exception'] | undefined;

		const stoppedDetails = thread.stoppedDetails;
		if (stoppedDetails) {
			stopReason = stoppedDetails.reason ?? 'breakpoint';
			if (stopReason === 'exception' || stopReason === 'error') {
				kind = 'exception';
				exceptionInfo = {
					id: stoppedDetails.reason ?? '',
					description: stoppedDetails.text ?? '',
					stackTrace: frames.map(f => `  ${f.source}:${f.line} ${f.name}`).join('\n'),
				};
			}
		}

		const file = topFrame.source?.uri?.fsPath ?? '';
		const line = topFrame.range.startLineNumber;
		this.logService.info(`[wescode-debug] ${kind} at ${file}:${line} (${variables.length} vars, ${frames.length} frames)`);

		this._send({
			kind,
			sessionId: sessionId,
			timestamp: Date.now(),
			threadId: thread.threadId,
			stopReason: stopReason,
			frames,
			variables: variables.length > 0 ? variables : undefined,
			exception: exceptionInfo,
		});
	}

	private _send(event: DebugEventPayload): void {
		this.backend.debugEvent(event as unknown as Record<string, unknown>).catch(() => {});
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeDebugBridge, LifecyclePhase.Restored);
