/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWorkbenchContribution } from '../../../../common/contributions.js';
import { ILanguageFeaturesService } from '../../../../../editor/common/services/languageFeatures.js';
import { InlineCompletionContext, InlineCompletionsProvider } from '../../../../../editor/common/languages.js';
import { ITextModel } from '../../../../../editor/common/model.js';
import { Position } from '../../../../../editor/common/core/position.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';

export class WescodeInlineCompletionProvider extends Disposable implements IWorkbenchContribution {

	static readonly ID = 'workbench.contrib.wescodeInlineCompletion';

	constructor(
		@ILanguageFeaturesService languageFeaturesService: ILanguageFeaturesService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
	) {
		super();

		const provider: InlineCompletionsProvider = {
			groupId: 'wescode',
			yieldsToGroupIds: [],

			handlePartialAccept: (_completions, _item, acceptedCharacters) => {
				// Telemetry: track how much of the completion the user accepted.
				// Future: feed this back to tune completion length/quality.
				console.debug(`[wescode-completion] partial accept: ${acceptedCharacters} chars`);
			},

			provideInlineCompletions: async (model: ITextModel, position: Position, context: InlineCompletionContext, token: CancellationToken) => {
				const lineContent = model.getLineContent(position.lineNumber);
				const prefix = lineContent.substring(0, position.column - 1);

				if (prefix.trim().length < 3) {
					return { items: [] };
				}

				// Build multi-line prefix (up to 50 lines before cursor)
				const startLine = Math.max(1, position.lineNumber - 50);
				const prefixLines: string[] = [];
				for (let i = startLine; i < position.lineNumber; i++) {
					prefixLines.push(model.getLineContent(i));
				}
				prefixLines.push(prefix);
				const fullPrefix = prefixLines.join('\n');

				// Build suffix (up to 20 lines after cursor)
				const endLine = Math.min(model.getLineCount(), position.lineNumber + 20);
				const suffixLines: string[] = [];
				suffixLines.push(lineContent.substring(position.column - 1));
				for (let i = position.lineNumber + 1; i <= endLine; i++) {
					suffixLines.push(model.getLineContent(i));
				}
				const fullSuffix = suffixLines.join('\n');

				if (token.isCancellationRequested) {
					return { items: [] };
				}

				// Use local AbortController wired to CancellationToken
				const ac = new AbortController();
				const onCancel = token.onCancellationRequested(() => ac.abort());

				try {
					const result = await this.backend.completion({
						path: model.uri.fsPath,
						line: position.lineNumber,
						col: position.column,
						prefix: fullPrefix,
						suffix: fullSuffix,
					});

					if (token.isCancellationRequested || !result || !result.completions || result.completions.length === 0) {
						return { items: [] };
					}

					const text = result.completions[0].text;
					if (!text) {
						return { items: [] };
					}

					return {
						items: [{
							insertText: text,
						}],
						enableForwardStability: true,
					};
				} catch {
					return { items: [] };
				} finally {
					onCancel.dispose();
				}
			},
			freeInlineCompletions: () => { },
		};

		this._register(languageFeaturesService.inlineCompletionsProvider.register('*', provider));
	}
}
