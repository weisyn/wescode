/*---------------------------------------------------------------------------------------------
 *  WES Code — scheduled task completion notice.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { INotificationService, Severity } from '../../../../../platform/notification/common/notification.js';
import { ICommandService } from '../../../../../platform/commands/common/commands.js';
import { IWescodeBackendService, ICronRunFinished } from '../../../../../platform/wescode/common/wescode.js';

// Inlined Chinese matches the rest of the wescode chrome (status bar, cell bar).
const L = {
	ok: (name: string) => `定时任务「${name}」已完成`,
	failed: (name: string) => `定时任务「${name}」执行失败`,
	open: '查看结果',
	detail: (msg: string) => msg,
};

/**
 * Turns a finished scheduled run into something the user can see.
 *
 * A cron run is the one agent turn with nobody watching it: no request, no
 * stream, no open panel it belongs to. Everything worked and the product was
 * still silent — the task list said "executed once" and the answer sat in a
 * conversation there was no reason to open. This is the part that says so, and
 * the action puts the user in the conversation holding the reply.
 *
 * A workbench contribution rather than a webview subscription on purpose: the
 * notice must not depend on the task page being open, which is precisely when
 * it is least likely to be.
 */
export class WescodeCronNotifier extends Disposable {
	static readonly ID = 'wescode.cronNotifier';

	constructor(
		@IWescodeBackendService private readonly _backend: IWescodeBackendService,
		@INotificationService private readonly _notificationService: INotificationService,
		@ICommandService private readonly _commandService: ICommandService,
	) {
		super();
		this._register(this._backend.onDidCronRunFinished(e => this._show(e)));
	}

	private _show(e: ICronRunFinished): void {
		const failed = e.status !== 'ok';
		const message = failed
			? `${L.failed(e.jobName)}${e.errorMsg ? `：${L.detail(e.errorMsg)}` : ''}`
			: L.ok(e.jobName);

		const openResult = e.sessionKey
			? async () => {
				this._backend.switchConversation(e.sessionKey!);
				await this._commandService.executeCommand('workbench.panel.wescode.chat.focus');
			}
			: undefined;

		// One-shot tasks (kind=at or delete-after-run) auto-open the result
		// conversation: the user explicitly scheduled this for a specific time,
		// so they want to see the answer, not hunt for it. Recurring tasks only
		// notify — auto-opening every hour would be a productivity wrecker.
		if (e.isOneShot && !failed && openResult) {
			void openResult();
		}

		const actions = openResult
			? [{
				id: 'wescode.cron.openResult',
				label: L.open,
				tooltip: L.open,
				class: undefined,
				enabled: true,
				run: openResult,
			}]
			: [];

		this._notificationService.notify({
			severity: failed ? Severity.Warning : Severity.Info,
			message,
			actions: { primary: actions },
		});
	}
}
