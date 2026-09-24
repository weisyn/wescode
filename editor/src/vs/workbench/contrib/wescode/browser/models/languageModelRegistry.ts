/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable, DisposableStore } from '../../../../../base/common/lifecycle.js';
import { IWorkbenchContribution } from '../../../../common/contributions.js';
import { ILanguageModelsService, ILanguageModelChat, ILanguageModelChatMetadata, ILanguageModelChatResponse, IChatResponseFragment, IChatMessage, ChatMessageRole } from '../../../chat/common/languageModels.js';
import { IWescodeBackendService, IProviderItem, IWescodeStreamEvent } from '../../../../../platform/wescode/common/wescode.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { ExtensionIdentifier } from '../../../../../platform/extensions/common/extensions.js';

const WESCODE_EXTENSION_ID = new ExtensionIdentifier('wescode.wescode');

/**
 * Registers all wescode-configured LLM providers into VSCode's ILanguageModelsService.
 * This makes them appear in upstream model pickers and available to InlineChat.
 */
export class WescodeLanguageModelRegistry extends Disposable implements IWorkbenchContribution {

	static readonly ID = 'workbench.contrib.wescodeLanguageModelRegistry';

	private readonly _registrations = this._register(new DisposableStore());

	constructor(
		@ILanguageModelsService private readonly languageModelsService: ILanguageModelsService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
	) {
		super();

		// Register models when providers change
		this._register(this.backend.onDidProviderChange((providers) => {
			this._syncModels(providers);
		}));

		// Initial registration
		this._initialSync();
	}

	private async _initialSync(): Promise<void> {
		try {
			const providers = await this.backend.listProviders();
			this._syncModels(providers);
		} catch {
			// Backend may not be ready yet; will sync on provider change event
		}
	}

	private _createChatStream(backend: IWescodeBackendService, providerId: string, message: string, token: CancellationToken): AsyncIterable<IChatResponseFragment> {
		let index = 0;
		// Inactivity timeout: terminate the stream if no event arrives within 60s
		const INACTIVITY_TIMEOUT_MS = 60_000;
		return {
			[Symbol.asyncIterator]() {
				let done = false;
				let resolveNext: ((value: IteratorResult<IChatResponseFragment>) => void) | undefined;
				const buffer: IChatResponseFragment[] = [];
				let inactivityTimer: ReturnType<typeof setTimeout> | undefined;

				const finish = () => {
					if (done) { return; }
					done = true;
					clearTimeout(inactivityTimer);
					disposable.dispose();
					if (resolveNext) {
						const r = resolveNext;
						resolveNext = undefined;
						r({ value: undefined as any, done: true });
					}
				};

				const resetInactivityTimer = () => {
					clearTimeout(inactivityTimer);
					inactivityTimer = setTimeout(finish, INACTIVITY_TIMEOUT_MS);
				};

				resetInactivityTimer();

				const disposable = backend.onDidStreamEvent((event: IWescodeStreamEvent) => {
					if (done) { return; }
					resetInactivityTimer();

					if (event.type === 'text.delta' && event.text) {
						const fragment: IChatResponseFragment = { index: index++, part: { type: 'text', value: event.text } };
						if (resolveNext) {
							const r = resolveNext;
							resolveNext = undefined;
							r({ value: fragment, done: false });
						} else {
							buffer.push(fragment);
						}
					}

				if (event.type === 'done' || event.type === 'interrupted' || event.type === 'error') {
					finish();
				}
				});

				token.onCancellationRequested(finish);

				backend.chat({ message, providerId }).catch(finish);

				return {
					next(): Promise<IteratorResult<IChatResponseFragment>> {
						if (buffer.length > 0) {
							return Promise.resolve({ value: buffer.shift()!, done: false });
						}
						if (done) {
							return Promise.resolve({ value: undefined as any, done: true });
						}
						return new Promise(resolve => { resolveNext = resolve; });
					},
					return(): Promise<IteratorResult<IChatResponseFragment>> {
						finish();
						return Promise.resolve({ value: undefined as any, done: true });
					},
					throw(): Promise<IteratorResult<IChatResponseFragment>> {
						finish();
						return Promise.resolve({ value: undefined as any, done: true });
					}
				};
			}
		};
	}

	private _syncModels(providers: IProviderItem[]): void {
		this._registrations.clear();

		for (const provider of providers) {
			if (!provider.name || !provider.model) {
				continue;
			}

			const identifier = `wescode-${provider.name}`;
			const metadata: ILanguageModelChatMetadata = {
				extension: WESCODE_EXTENSION_ID,
				name: `${provider.name} (${provider.model})`,
				id: identifier,
				vendor: 'wescode',
				version: '1.0.0',
				family: provider.model.split('-')[0] || 'unknown',
				maxInputTokens: 64000,
				maxOutputTokens: 8192,
				isDefault: provider.isDefault,
				isUserSelectable: true,
			};

			const backend = this.backend;
			const providerName = provider.name;

			const model: ILanguageModelChat = {
				metadata,
				sendChatRequest: async (messages: IChatMessage[], _from, _options, token: CancellationToken): Promise<ILanguageModelChatResponse> => {
					const userMessage = messages
						.filter(m => m.role === ChatMessageRole.User)
						.map(m => m.content.map(p => 'value' in p ? p.value : '').join(''))
						.join('\n');

					const stream = this._createChatStream(backend, providerName, userMessage, token);
					return {
						stream,
						result: Promise.resolve({}),
					};
				},
				provideTokenCount: async (message, _token: CancellationToken): Promise<number> => {
					if (typeof message === 'string') {
						return Math.ceil(message.length / 4);
					}
					const text = message.content.map(p => 'value' in p ? p.value : '').join('');
					return Math.ceil(text.length / 4);
				},
			};

			try {
				this._registrations.add(
					this.languageModelsService.registerLanguageModelChat(identifier, model)
				);
			} catch {
				// Registration can fail if identifier is duplicate; skip silently
			}
		}
	}
}
