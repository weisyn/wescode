/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService, IEditFeedbackEvent } from '../../../../../platform/wescode/common/wescode.js';
import { IEditorService } from '../../../../services/editor/common/editorService.js';
import { ICodeEditor, isCodeEditor } from '../../../../../editor/browser/editorBrowser.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';

interface AIEditRecord {
	path: string;
	line: number;
	content: string;
	timestamp: number;
	accepted: boolean;
}

/**
 * Tracks implicit feedback signals from user editing behavior:
 * - Immediate undo after AI edit → "undo_immediate"
 * - User tweaks shortly after accepting → "post_accept_tweak"
 * - User writes own code after rejecting → "reject_then_write"
 *
 * All signals are pushed to the backend as Memory L4 corrections.
 */
export class WescodeEditFeedbackTracker extends Disposable {
	static readonly ID = 'wescode.editFeedbackTracker';

	private readonly _recentAIEdits: AIEditRecord[] = [];
	private readonly _recentRejects: AIEditRecord[] = [];
	private _contentListener: { dispose(): void } | undefined;
	private _undoDebounce: ReturnType<typeof setTimeout> | undefined;

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IEditorService private readonly editorService: IEditorService,
	) {
		super();

		this._register(this.backend.onDidStreamEvent(ev => {
			if (ev.type === 'edit.applied' && ev.editPreview?.path && ev.editPreview.status === 'applied') {
				this._recordAIEdit(ev.editPreview.path, ev.editPreview.startLine ?? 0, ev.editPreview.content ?? '');
			}
			if (ev.type === 'edit.rejected' && ev.editPreview?.path) {
				this._recordReject(ev.editPreview.path, ev.editPreview.startLine ?? 0, ev.editPreview.content ?? '');
			}
		}));

		this._register(this.editorService.onDidActiveEditorChange(() => {
			this._watchContent();
		}));
		this._watchContent();
	}

	private _recordAIEdit(path: string, line: number, content: string): void {
		this._recentAIEdits.push({ path, line, content, timestamp: Date.now(), accepted: true });
		if (this._recentAIEdits.length > 10) {
			this._recentAIEdits.shift();
		}
	}

	private _recordReject(path: string, line: number, content: string): void {
		this._recentRejects.push({ path, line, content, timestamp: Date.now(), accepted: false });
		if (this._recentRejects.length > 10) {
			this._recentRejects.shift();
		}
	}

	private _watchContent(): void {
		this._contentListener?.dispose();
		const control = this.editorService.activeTextEditorControl;
		if (!control || !isCodeEditor(control)) { return; }
		const editor = control as ICodeEditor;
		const model = editor.getModel();
		if (!model || model.uri.scheme !== 'file') { return; }

		const filePath = model.uri.fsPath;

		const d = model.onDidChangeContent((e) => {
			const now = Date.now();

			for (let i = this._recentAIEdits.length - 1; i >= 0; i--) {
				const aiEdit = this._recentAIEdits[i];
				if (aiEdit.path !== filePath) { continue; }
				const elapsed = now - aiEdit.timestamp;
				if (elapsed > 2000) { break; }

				for (const change of e.changes) {
					if (change.text === '' && Math.abs(change.range.startLineNumber - 1 - aiEdit.line) <= 3) {
						this._debounceUndo(aiEdit, filePath);
						break;
					}
				}
			}

			for (let i = this._recentAIEdits.length - 1; i >= 0; i--) {
				const aiEdit = this._recentAIEdits[i];
				if (aiEdit.path !== filePath || !aiEdit.accepted) { continue; }
				const elapsed = now - aiEdit.timestamp;
				if (elapsed > 10000) { break; }

				for (const change of e.changes) {
					const changeLine = change.range.startLineNumber - 1;
					if (Math.abs(changeLine - aiEdit.line) <= 5 && change.text.length > 0) {
						this._sendFeedback({
							type: 'post_accept_tweak',
							file: filePath,
							lang: this._detectLang(filePath),
							aiVersion: aiEdit.content,
							userVersion: change.text,
							line: changeLine,
							timestamp: now,
						});
						this._recentAIEdits.splice(i, 1);
						break;
					}
				}
			}

			for (let i = this._recentRejects.length - 1; i >= 0; i--) {
				const reject = this._recentRejects[i];
				if (reject.path !== filePath) { continue; }
				const elapsed = now - reject.timestamp;
				if (elapsed > 30000) { break; }

				for (const change of e.changes) {
					if (change.text.trim().length > 5) {
						this._sendFeedback({
							type: 'reject_then_write',
							file: filePath,
							lang: this._detectLang(filePath),
							aiVersion: reject.content,
							userVersion: change.text,
							line: change.range.startLineNumber - 1,
							timestamp: now,
						});
						this._recentRejects.splice(i, 1);
						break;
					}
				}
			}
		});
		this._contentListener = { dispose: () => d.dispose() };
	}

	private _debounceUndo(aiEdit: AIEditRecord, filePath: string): void {
		if (this._undoDebounce) {
			clearTimeout(this._undoDebounce);
		}
		this._undoDebounce = setTimeout(() => {
			this._sendFeedback({
				type: 'undo_immediate',
				file: filePath,
				lang: this._detectLang(filePath),
				aiVersion: aiEdit.content,
				userVersion: '',
				line: aiEdit.line,
				timestamp: Date.now(),
			});
			const idx = this._recentAIEdits.indexOf(aiEdit);
			if (idx >= 0) { this._recentAIEdits.splice(idx, 1); }
		}, 2000);
	}

	private _sendFeedback(event: IEditFeedbackEvent): void {
		this.backend.reportEditFeedback(event).catch(() => {
			// Backend not ready — silently drop
		});
	}

	private _detectLang(path: string): string {
		if (path.endsWith('.go')) { return 'go'; }
		if (path.endsWith('.ts') || path.endsWith('.tsx')) { return 'typescript'; }
		if (path.endsWith('.js') || path.endsWith('.jsx')) { return 'javascript'; }
		if (path.endsWith('.py')) { return 'python'; }
		if (path.endsWith('.rs')) { return 'rust'; }
		if (path.endsWith('.java')) { return 'java'; }
		if (path.endsWith('.cpp') || path.endsWith('.cc') || path.endsWith('.h')) { return 'cpp'; }
		return 'unknown';
	}

	override dispose(): void {
		this._contentListener?.dispose();
		if (this._undoDebounce) { clearTimeout(this._undoDebounce); }
		super.dispose();
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeEditFeedbackTracker, LifecyclePhase.Restored);
