/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';
import { ILanguageFeaturesService } from '../../../../../editor/common/services/languageFeatures.js';
import { IMarkerService, MarkerSeverity } from '../../../../../platform/markers/common/markers.js';
import { CodeActionProvider, CodeAction } from '../../../../../editor/common/languages.js';
import { ITextModel } from '../../../../../editor/common/model.js';
import { Range } from '../../../../../editor/common/core/range.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { CodeActionList } from '../../../../../editor/common/languages.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';

const WESCODE_MARKER_OWNER = 'wescode-agent';

export class WescodeAIQuickFixProvider extends Disposable {
	static readonly ID = 'wescode.aiQuickFix';

	constructor(
		@ILanguageFeaturesService private readonly langFeatures: ILanguageFeaturesService,
		@IMarkerService private readonly markerService: IMarkerService,
		@IWescodeBackendService _backend: IWescodeBackendService,
	) {
		super();

		const provider: CodeActionProvider = {
			provideCodeActions: (
				model: ITextModel,
				range: Range,
				_context: any,
				_token: CancellationToken,
			): CodeActionList | undefined => {
				const uri = model.uri;
				const markers = this.markerService.read({ resource: uri }).filter(
					m => m.source === WESCODE_MARKER_OWNER &&
						m.severity === MarkerSeverity.Error &&
						Range.areIntersectingOrTouching(range, new Range(
							m.startLineNumber, m.startColumn,
							m.endLineNumber, m.endColumn
						))
				);

				if (markers.length === 0) {
					return undefined;
				}

				const actions: CodeAction[] = markers.map(marker => ({
					title: `AI 修复: ${marker.message}`,
					kind: 'quickfix',
					diagnostics: [marker],
					command: {
						id: 'wescode.fixWithAI',
						title: 'AI 修复',
						arguments: [uri.fsPath, marker.message, marker.startLineNumber],
					},
				}));

				return { actions, dispose: () => { } };
			},
		};

		this._register(this.langFeatures.codeActionProvider.register('*', provider));
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeAIQuickFixProvider, LifecyclePhase.Eventually);
