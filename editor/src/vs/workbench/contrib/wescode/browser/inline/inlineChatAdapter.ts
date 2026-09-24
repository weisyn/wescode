/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWorkbenchContribution } from '../../../../common/contributions.js';
import { IChatAgentService, IChatAgentData, IChatAgentImplementation, IChatAgentRequest, IChatAgentResult, IChatAgentHistoryEntry, ChatAgentLocation } from '../../../chat/common/chatAgents.js';
import { IChatProgress, IChatTextEdit } from '../../../chat/common/chatService.js';
import { IWescodeBackendService, IWescodeStreamEvent } from '../../../../../platform/wescode/common/wescode.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { IEditorService } from '../../../../services/editor/common/editorService.js';
import { isCodeEditor } from '../../../../../editor/browser/editorBrowser.js';
import { URI } from '../../../../../base/common/uri.js';
import { ExtensionIdentifier } from '../../../../../platform/extensions/common/extensions.js';
import { ILanguageModelsService, ILanguageModelChat, ILanguageModelChatMetadata, ILanguageModelChatResponse } from '../../../chat/common/languageModels.js';
import { IWorkspaceContextService } from '../../../../../platform/workspace/common/workspace.js';

const WESCODE_AGENT_ID = 'wescode.inlineEdit';
const WESCODE_EXTENSION_ID = new ExtensionIdentifier('wescode.wescode');
const WESCODE_MODEL_ID = 'wescode-default';

/**
 * Registers a dynamic ChatAgent for the Editor location (Cmd+K InlineChat)
 * and a LanguageModel backed by the wescode Go backend.
 */
export class WescodeInlineChatAdapter extends Disposable implements IWorkbenchContribution {

	static readonly ID = 'workbench.contrib.wescodeInlineChatAdapter';

	constructor(
		@IChatAgentService private readonly chatAgentService: IChatAgentService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IEditorService private readonly editorService: IEditorService,
		@ILanguageModelsService private readonly languageModelsService: ILanguageModelsService,
		@IWorkspaceContextService private readonly workspaceService: IWorkspaceContextService,
	) {
		super();
		this._registerLanguageModel();
		this._registerEditorAgent();
	}

	private _registerLanguageModel(): void {
		const metadata: ILanguageModelChatMetadata = {
			extension: WESCODE_EXTENSION_ID,
			name: 'WES Code',
			id: WESCODE_MODEL_ID,
			vendor: 'wescode',
			version: '1.0.0',
			family: 'wescode',
			maxInputTokens: 64000,
			maxOutputTokens: 8192,
			isDefault: true,
			isUserSelectable: true,
		};

		const provider: ILanguageModelChat = {
			metadata,
			sendChatRequest: async (_messages, _from, _options, _token) => {
				const stream = (async function* () {
					yield { index: 0, part: { type: 'text' as const, value: '' } };
				})();
				return {
					stream,
					result: Promise.resolve({}),
				} as ILanguageModelChatResponse;
			},
			provideTokenCount: async (_message, _token) => {
				return 0;
			},
		};

		this._register(this.languageModelsService.registerLanguageModelChat(WESCODE_MODEL_ID, provider));
	}

	private _registerEditorAgent(): void {
		const agentData: IChatAgentData = {
			id: WESCODE_AGENT_ID,
			name: 'WES Code',
			fullName: 'WES Code Inline Editor',
			description: 'AI-powered inline code editing',
			extensionId: WESCODE_EXTENSION_ID,
			extensionPublisherId: 'wescode',
			extensionDisplayName: 'WES Code',
			isDefault: true,
			isDynamic: true,
			metadata: {},
			slashCommands: [],
			locations: [ChatAgentLocation.Editor],
			disambiguation: [],
		};

		const agentImpl: IChatAgentImplementation = {
			invoke: async (request: IChatAgentRequest, progress: (part: IChatProgress) => void, _history: IChatAgentHistoryEntry[], token: CancellationToken): Promise<IChatAgentResult> => {
				return this._handleInlineEditRequest(request, progress, token);
			},
		};

		this._register(this.chatAgentService.registerDynamicAgent(agentData, agentImpl));
	}

	private async _handleInlineEditRequest(
		request: IChatAgentRequest,
		progress: (part: IChatProgress) => void,
		token: CancellationToken,
	): Promise<IChatAgentResult> {
		const editor = this.editorService.activeTextEditorControl;
		if (!editor || !isCodeEditor(editor)) {
			return {};
		}

		const model = editor.getModel();
		if (!model) {
			return {};
		}

		const uri = model.uri;
		const selection = editor.getSelection();

		let codeSnippets: { filePath: string; startLine: number; endLine: number; code: string; language: string }[] | undefined;
		if (selection && !selection.isEmpty()) {
			const selectedText = model.getValueInRange(selection);
			codeSnippets = [{
				filePath: uri.fsPath,
				startLine: selection.startLineNumber,
				endLine: selection.endLineNumber,
				code: selectedText,
				language: model.getLanguageId(),
			}];
		}

		// Resolve workspace folder root (not file parent dir) for correct boundary checks
		const folder = this.workspaceService.getWorkspaceFolder(uri);
		const workDir = folder?.uri.fsPath;
		const folders = this.workspaceService.getWorkspace().folders;
		const allowPaths = folders
			.filter(f => f.uri.fsPath !== workDir)
			.map(f => f.uri.fsPath);

		return new Promise<IChatAgentResult>((resolve) => {
			let settled = false;
			const collectedTxIds: string[] = [];
			const settle = (result: IChatAgentResult) => {
				if (settled) { return; }
				settled = true;
				clearTimeout(inactivityTimer);
				disposable.dispose();
				// Successful completion → accept edits (clean backup, keep disk content).
				// Error/timeout/cancel → reject edits (restore backup to disk).
				const isSuccess = !result.errorDetails;
				for (const txId of collectedTxIds) {
					if (isSuccess) {
						this.backend.editAccept(txId).catch(() => {});
					} else {
						this.backend.editReject(txId).catch(() => {});
					}
				}
				resolve(result);
			};

			// Inactivity timeout: if no stream event arrives within 60s, assume
			// the backend crashed or the RPC channel is broken.
			const INACTIVITY_TIMEOUT_MS = 60_000;
			let inactivityTimer = setTimeout(() => settle({ errorDetails: { message: 'Inline edit timed out (no response from backend)' } }), INACTIVITY_TIMEOUT_MS);
			const resetInactivityTimer = () => {
				clearTimeout(inactivityTimer);
				inactivityTimer = setTimeout(() => settle({ errorDetails: { message: 'Inline edit timed out (backend stopped responding)' } }), INACTIVITY_TIMEOUT_MS);
			};

			const seenTxIds = new Set<string>();

			// Register stream listener BEFORE sending chat request to avoid missing events
			const disposable = this.backend.onDidStreamEvent((event: IWescodeStreamEvent) => {
				if (settled) { return; }
				resetInactivityTimer();

				if (token.isCancellationRequested) {
					settle({});
					return;
				}

				if (event.type === 'edit.preview' && event.editPreview) {
					const preview = event.editPreview;
					if (seenTxIds.has(preview.txId)) {
						return;
					}
					seenTxIds.add(preview.txId);
					collectedTxIds.push(preview.txId);
					if (preview.hunks && preview.hunks.length > 0) {
						// Use the active model's URI directly to ensure InlineChat applies the edit
						const fileUri = preview.path === uri.fsPath ? uri : URI.file(preview.path);
						for (const hunk of preview.hunks) {
							const endLine = Math.min(hunk.oldEnd, model.getLineCount());
							const textEdit: IChatTextEdit = {
								uri: fileUri,
								edits: [{
									range: {
										startLineNumber: hunk.oldStart,
										startColumn: 1,
										endLineNumber: endLine,
										endColumn: model.getLineMaxColumn(endLine),
									},
									text: hunk.newText,
								}],
								kind: 'textEdit',
								done: preview.status === 'applied',
							};
							progress(textEdit);
						}
					}
				}

				if (event.type === 'text.delta' && event.text) {
					progress({
						kind: 'markdownContent',
						content: { value: event.text },
					});
				}

			if (event.type === 'done' || event.type === 'interrupted') {
				settle({});
			}

			if (event.type === 'error') {
					settle({
						errorDetails: {
							message: event.error?.kind || '未知错误',
						},
					});
				}
			});

			token.onCancellationRequested(() => {
				settle({});
			});

			// Fire-and-forget: chat() returns when the run starts streaming,
			// events arrive via onDidStreamEvent registered above.
			this.backend.chat({
				message: request.message,
				agentId: 'builtin-coder',
				codeSnippets,
				workDir,
				allowPaths: allowPaths.length > 0 ? allowPaths : undefined,
			}).catch(() => {
				settle({ errorDetails: { message: '无法启动内联编辑' } });
			});
		});
	}
}
