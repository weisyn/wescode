/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService, IDebugRequest } from '../../../../../platform/wescode/common/wescode.js';
import { IDebugService } from '../../../../contrib/debug/common/debug.js';
import { URI } from '../../../../../base/common/uri.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';
import { ILogService } from '../../../../../platform/log/common/log.js';

/**
 * WescodeDebugActionHandler — handles debug/breakpoint, debug/evaluate, debug/continue
 * notifications from the Go backend and dispatches to the VSCode debug service.
 *
 * Follows the same pattern as WescodeLSPBridge: listen → dispatch → respond.
 */
export class WescodeDebugActionHandler extends Disposable {
	static readonly ID = 'wescode.debugActionHandler';

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IDebugService private readonly debugService: IDebugService,
		@ILogService private readonly logService: ILogService,
	) {
		super();
		this._register(this.backend.onDidDebugRequest(req => this._handleRequest(req)));
	}

	private async _handleRequest(req: IDebugRequest): Promise<void> {
		try {
			const result = await this._dispatch(req.method, req.params);
			await this.backend.debugResponse(req.requestId, result);
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			this.logService.warn(`[wescode-debug] ${req.method} failed: ${message}`);
			await this.backend.debugResponse(req.requestId, null, message);
		}
	}

	private async _dispatch(method: string, params: any): Promise<any> {
		switch (method) {
			case 'debug/breakpoint':
				return this._handleBreakpoint(params);
			case 'debug/evaluate':
				return this._handleEvaluate(params);
			case 'debug/continue':
				return this._handleContinue(params);
			default:
				throw new Error(`Unknown debug method: ${method}`);
		}
	}

	private async _handleBreakpoint(params: { action: string; file: string; line: number; condition?: string }): Promise<{ ok: boolean }> {
		const uri = URI.file(params.file);

		if (params.action === 'remove') {
			const existing = this.debugService.getModel().getBreakpoints({
				uri,
				lineNumber: params.line,
			});
			if (existing.length > 0) {
				await this.debugService.removeBreakpoints(existing[0].getId());
			}
			return { ok: true };
		}

		// action === 'set'
		await this.debugService.addBreakpoints(uri, [{
			lineNumber: params.line,
			enabled: true,
			condition: params.condition || undefined,
		}]);
		return { ok: true };
	}

	private async _handleEvaluate(params: { expression: string; frame_index?: number }): Promise<{ result: string; type: string }> {
		const session = this.debugService.getViewModel().focusedSession;
		if (!session) {
			throw new Error('No active debug session');
		}

		const thread = this.debugService.getViewModel().focusedThread;
		if (!thread) {
			throw new Error('No focused thread (debugger not paused)');
		}

		const callStack = thread.getCallStack();
		const frameIndex = params.frame_index ?? 0;
		if (callStack.length === 0) {
			throw new Error('No stack frames available');
		}
		const frame = callStack[Math.min(frameIndex, callStack.length - 1)];

		const response = await session.evaluate(params.expression, frame.frameId, 'repl');
		if (!response) {
			throw new Error('Evaluate returned no response');
		}
		return {
			result: response.body.result,
			type: response.body.type ?? '',
		};
	}

	private async _handleContinue(params: { action: string }): Promise<{ ok: boolean }> {
		const thread = this.debugService.getViewModel().focusedThread;
		if (!thread) {
			throw new Error('No focused thread (debugger not paused)');
		}

		switch (params.action) {
			case 'continue':
				await thread.continue();
				break;
			case 'step_over':
				await thread.next();
				break;
			case 'step_into':
				await thread.stepIn();
				break;
			case 'step_out':
				await thread.stepOut();
				break;
			default:
				throw new Error(`Unknown debug action: ${params.action}`);
		}
		return { ok: true };
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeDebugActionHandler, LifecyclePhase.Restored);
