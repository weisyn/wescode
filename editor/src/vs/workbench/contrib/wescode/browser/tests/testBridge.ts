/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';
import { ITestService } from '../../../../contrib/testing/common/testService.js';
import { ITestResultService } from '../../../../contrib/testing/common/testResultService.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';
import { ILogService } from '../../../../../platform/log/common/log.js';
import { IMarkdownString } from '../../../../../base/common/htmlContent.js';
import { TestMessageType, TestResultState } from '../../../../contrib/testing/common/testTypes.js';
import { ITestFailureDetail } from '../../../../../platform/wescode/common/wescode.js';

export class WescodeTestBridge extends Disposable {
	static readonly ID = 'wescode.testBridge';

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ITestService _testService: ITestService,
		@ITestResultService private readonly testResultService: ITestResultService,
		@ILogService private readonly logService: ILogService,
	) {
		super();

		this._register(this.testResultService.onResultsChanged(evt => {
			if ('completed' in evt) {
				this._onTestRunComplete(evt.completed.id);
			}
		}));
	}

	private _onTestRunComplete(resultId: string): void {
		const result = this.testResultService.getResult(resultId);
		if (!result) {
			return;
		}

		let passed = 0;
		let failed = 0;
		let skipped = 0;
		const failures: ITestFailureDetail[] = [];

		for (const test of result.tests) {
			const state = test.ownComputedState;
			if (state === TestResultState.Passed) {
				passed++;
			} else if (state === TestResultState.Failed || state === TestResultState.Errored) {
				failed++;
				failures.push(this._extractFailureDetail(test));
			} else if (state === TestResultState.Skipped) {
				skipped++;
			}
		}

		this.logService.info(
			`[wescode-test] run complete: ${passed} passed, ${failed} failed, ${skipped} skipped`
		);

		this.backend.testResult({ passed, failed, skipped, failures }).catch(() => {});
	}

	private _extractFailureDetail(test: import('../../../../contrib/testing/common/testTypes.js').TestResultItem): ITestFailureDetail {
		const label = test.item.label;
		const file = test.item.uri?.fsPath ?? '';
		const line = test.item.range?.startLineNumber ?? 0;

		let message = '';
		if (test.tasks.length > 0) {
			for (const msg of test.tasks[0].messages) {
				if (msg.type === TestMessageType.Error) {
					const text = msg.message;
					message = typeof text === 'string' ? text : (text as IMarkdownString).value ?? '';
					break;
				}
			}
		}

		return { name: label, file, line, message };
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeTestBridge, LifecyclePhase.Restored);
