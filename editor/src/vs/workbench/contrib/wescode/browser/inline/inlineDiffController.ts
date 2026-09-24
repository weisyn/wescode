/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { ICodeEditor, isCodeEditor } from '../../../../../editor/browser/editorBrowser.js';
import { IEditorService } from '../../../../services/editor/common/editorService.js';
import { IModelDecorationOptions, ITextModel, TrackedRangeStickiness } from '../../../../../editor/common/model.js';
import { IViewZone } from '../../../../../editor/browser/editorBrowser.js';
import { URI } from '../../../../../base/common/uri.js';
import { Range } from '../../../../../editor/common/core/range.js';
import { IEditPreview, IEditPreviewHunk } from '../../../../../platform/wescode/common/wescode.js';
import { Emitter, Event } from '../../../../../base/common/event.js';
import { IFileService } from '../../../../../platform/files/common/files.js';
import { ITextFileService } from '../../../../services/textfile/common/textfiles.js';

const ADDED_LINE_DECORATION: IModelDecorationOptions = {
	description: 'wescode-diff-added',
	isWholeLine: true,
	className: 'wescode-diff-added',
	glyphMarginClassName: 'wescode-diff-glyph-added',
};

const PENDING_LINE_DECORATION: IModelDecorationOptions = {
	description: 'wescode-diff-pending',
	isWholeLine: true,
	className: 'wescode-diff-pending',
	glyphMarginClassName: 'wescode-diff-glyph-pending',
};

export interface DiffActionEvent {
	txId: string;
	action: 'accept' | 'reject';
}

interface PendingEdit {
	txId: string;
	path: string;
	preview: IEditPreview;
	decorationIds: string[];
	viewZoneIds: string[];
	editor: ICodeEditor;
	phase: 'pending' | 'confirmed';
	animationHandle?: number;
}

const SPECULATIVE_LINE_DELAY_MS = 30;

export class InlineDiffController extends Disposable {

	private readonly pendingEdits = new Map<string, PendingEdit>();

	private readonly _onDiffAction = this._register(new Emitter<DiffActionEvent>());
	readonly onDiffAction: Event<DiffActionEvent> = this._onDiffAction.event;

	constructor(
		@IEditorService private readonly editorService: IEditorService,
		@IFileService private readonly fileService: IFileService,
		@ITextFileService private readonly textFileService: ITextFileService,
	) {
		super();
	}

	showPendingDiff(preview: IEditPreview): void {
		if (!preview || !preview.txId) {
			return;
		}

		let editor = this._findEditorForPath(preview.path);
		if (!editor && preview.path) {
			this._openAndShowPending(preview);
			return;
		}
		if (!editor) {
			return;
		}
		this._applyPendingDiff(editor, preview);
	}

	private async _openAndShowPending(preview: IEditPreview): Promise<void> {
		// Guard against phantom paths: when the preview path does not exist on
		// disk (and is not a new-file write), skip opening it entirely —
		// editorService.openEditor surfaces an un-catchable "file not found"
		// notification to the user. The path may still be legitimately absent
		// for write previews whose file the agent is about to create.
		if (!preview.isNew) {
			try {
				if (!await this.fileService.exists(URI.file(preview.path))) {
					return;
				}
			} catch {
				return;
			}
		}
		try {
			const editorPane = await this.editorService.openEditor({ resource: URI.file(preview.path) });
			const control = editorPane?.getControl();
			if (control && isCodeEditor(control)) {
				this._applyPendingDiff(control as ICodeEditor, preview);
			}
		} catch { /* file may not exist yet for write */ }
	}

	private _applyPendingDiff(editor: ICodeEditor, preview: IEditPreview): void {

		const model = editor.getModel();
		if (!model) {
			return;
		}

		this._clearEdit(preview.txId);

		const useAnimation = (preview as any).streamMode === 'speculative' && preview.hunks && preview.hunks.length > 0;

		if (useAnimation) {
			this.pendingEdits.set(preview.txId, {
				txId: preview.txId,
				path: preview.path,
				preview,
				decorationIds: [],
				viewZoneIds: [],
				editor,
				phase: 'pending',
			});
			this._animateDiffLines(editor, preview.hunks!, preview.txId);
			return;
		}

		const decorations: { range: Range; options: IModelDecorationOptions }[] = [];

		if (preview.hunks && preview.hunks.length > 0) {
			for (const hunk of preview.hunks) {
				const startLine = Math.max(1, this._resolveHunkLine(model, hunk));
				const lineCount = hunk.oldEnd - hunk.oldStart;
				for (let i = 0; i < Math.max(lineCount, 1); i++) {
					const lineNum = startLine + i;
					if (lineNum <= model.getLineCount()) {
						decorations.push({
							range: new Range(lineNum, 1, lineNum, 1),
							options: PENDING_LINE_DECORATION,
						});
					}
				}
			}
		} else if (preview.isNew) {
			decorations.push({
				range: new Range(1, 1, 1, 1),
				options: PENDING_LINE_DECORATION,
			});
		}

		const decorationIds = editor.deltaDecorations([], decorations.map(d => ({
			range: d.range,
			options: { ...d.options, stickiness: TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges },
		})));

		this.pendingEdits.set(preview.txId, {
			txId: preview.txId,
			path: preview.path,
			preview,
			decorationIds,
			viewZoneIds: [],
			editor,
			phase: 'pending',
		});
	}

	private _animateDiffLines(editor: ICodeEditor, hunks: IEditPreviewHunk[], txId: string): void {
		const model = editor.getModel();
		if (!model) {
			return;
		}

		const allLines: { lineNum: number }[] = [];
		for (const hunk of hunks) {
			const startLine = Math.max(1, this._resolveHunkLine(model, hunk));
			const lineCount = Math.max(hunk.oldEnd - hunk.oldStart, 1);
			for (let i = 0; i < lineCount; i++) {
				const lineNum = startLine + i;
				if (lineNum <= model.getLineCount()) {
					allLines.push({ lineNum });
				}
			}
		}

		let idx = 0;
		const addNextLine = () => {
			const entry = this.pendingEdits.get(txId);
			if (!entry || idx >= allLines.length) {
				return;
			}

			const { lineNum } = allLines[idx++];
			const newIds = editor.deltaDecorations([], [{
				range: new Range(lineNum, 1, lineNum, 1),
				options: { ...PENDING_LINE_DECORATION, stickiness: TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges },
			}]);
			entry.decorationIds.push(...newIds);

			if (idx < allLines.length) {
				entry.animationHandle = requestAnimationFrame(() => {
					setTimeout(() => addNextLine(), SPECULATIVE_LINE_DELAY_MS);
				});
			}
		};

		addNextLine();
	}

	async confirmDiff(preview: IEditPreview): Promise<void> {
		// INV-EDIT-01: revert editor buffer from disk BEFORE decorating.
		// wesgine has already written the new content to disk via atomicWrite;
		// revert() atomically: reads disk → updates buffer → clears dirty flag.
		if (preview.path) {
			await this._syncFromDisk(preview.path);
		}

		const existing = this.pendingEdits.get(preview.txId);
		if (!existing) {
			this._showConfirmedFromScratch(preview);
			return;
		}

		if (existing.animationHandle !== undefined) {
			cancelAnimationFrame(existing.animationHandle);
			existing.animationHandle = undefined;
		}

		const editor = existing.editor;
		const model = editor.getModel();
		if (!model) {
			this._clearEdit(preview.txId);
			return;
		}

		editor.deltaDecorations(existing.decorationIds, []);
		this._removeViewZones(editor, existing.viewZoneIds);

		const decorations: { range: Range; options: IModelDecorationOptions }[] = [];
		const viewZoneIds: string[] = [];

		const hunks = preview.hunks ?? existing.preview.hunks ?? [];
		const txId = preview.txId;

		for (const hunk of hunks) {
			const newLineCount = hunk.newText ? hunk.newText.split('\n').length : 0;
			const insertLine = Math.max(1, this._resolveHunkLine(model, hunk));

			for (let i = 0; i < newLineCount; i++) {
				const lineNum = insertLine + i;
				if (lineNum <= model.getLineCount()) {
					decorations.push({
						range: new Range(lineNum, 1, lineNum, 1),
						options: ADDED_LINE_DECORATION,
					});
				}
			}

			if (hunk.oldText) {
				editor.changeViewZones(accessor => {
					const domNode = this._buildOldContentViewZone(hunk, txId);
					const zone: IViewZone = {
						afterLineNumber: Math.max(0, insertLine - 1),
						heightInLines: hunk.oldText.split('\n').length + 1,
						domNode,
						suppressMouseDown: true,
					};
					const id = accessor.addZone(zone);
					viewZoneIds.push(id);
				});
			}
		}

		const decorationIds = editor.deltaDecorations([], decorations.map(d => ({
			range: d.range,
			options: { ...d.options, stickiness: TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges },
		})));

		existing.decorationIds = decorationIds;
		existing.viewZoneIds = viewZoneIds;
		existing.phase = 'confirmed';
		existing.preview = { ...existing.preview, ...preview, status: 'applied' };
	}

	failDiff(txId: string): void {
		this._clearEdit(txId);
	}

	acceptDiff(txId: string): void {
		this._clearEdit(txId);
	}

	async rejectDiff(txId: string): Promise<void> {
		const entry = this.pendingEdits.get(txId);
		this._clearEdit(txId);
		if (entry?.path) {
			await this._syncFromDisk(entry.path);
		}
	}

	acceptAll(): void {
		for (const txId of [...this.pendingEdits.keys()]) {
			this._clearEdit(txId);
		}
	}

	async rejectAll(): Promise<void> {
		const paths = new Set<string>();
		for (const [txId, entry] of this.pendingEdits) {
			if (entry.path) { paths.add(entry.path); }
			this._clearEdit(txId);
		}
		for (const path of paths) {
			await this._syncFromDisk(path);
		}
	}

	/**
	 * INV-EDIT-01: Atomically reload editor buffer from disk and clear dirty flag.
	 * Uses ITextFileService.revert() which is the single correct sync primitive —
	 * unlike model.setValue() which leaves the dirty flag set.
	 */
	private async _syncFromDisk(path: string): Promise<void> {
		const uri = URI.file(path);
		try {
			await this.textFileService.revert(uri, { soft: false });
		} catch {
			// File may have been deleted or is not open in an editor — both are fine.
		}
	}

	hasPendingDiffs(): boolean {
		return this.pendingEdits.size > 0;
	}

	getPendingTxIds(): string[] {
		return [...this.pendingEdits.keys()];
	}

	private _resolveHunkLine(model: ITextModel, hunk: IEditPreviewHunk): number {
		if (hunk.oldStart > 0) {
			return hunk.oldStart;
		}
		if (hunk.oldText) {
			const content = model.getValue();
			const idx = content.indexOf(hunk.oldText);
			if (idx >= 0) {
				return content.substring(0, idx).split('\n').length;
			}
		}
		return 1;
	}

	private _buildOldContentViewZone(hunk: IEditPreviewHunk, txId: string): HTMLDivElement {
		const container = document.createElement('div');
		container.className = 'wescode-viewzone-old-content';
		container.style.cssText = 'padding: 2px 0; font-family: var(--vscode-editor-font-family); font-size: var(--vscode-editor-font-size);';

		// Old content lines (gray background, strikethrough)
		const oldLines = hunk.oldText.split('\n');
		for (const line of oldLines) {
			if (line === '' && oldLines.indexOf(line) === oldLines.length - 1) {
				continue; // skip trailing empty line from split
			}
			const lineDiv = document.createElement('div');
			lineDiv.className = 'wescode-diff-old-line';
			lineDiv.style.cssText = 'padding: 0 12px; color: var(--vscode-diffEditor-removedTextForeground, rgba(255, 0, 0, 0.7)); background: var(--vscode-diffEditor-removedLineBackground, rgba(255, 0, 0, 0.1)); text-decoration: line-through; opacity: 0.8; line-height: var(--vscode-editor-line-height);';
			lineDiv.textContent = `- ${line}`;
			container.appendChild(lineDiv);
		}

		// Action bar
		const actionBar = document.createElement('div');
		actionBar.className = 'wescode-diff-actions';
		actionBar.style.cssText = 'display: flex; gap: 6px; padding: 3px 12px; justify-content: flex-end; pointer-events: auto; position: relative; z-index: 100;';

		const acceptBtn = document.createElement('button');
		acceptBtn.className = 'wescode-diff-btn wescode-diff-btn-accept';
		acceptBtn.textContent = '✓ Accept';
		acceptBtn.style.cssText = 'padding: 2px 8px; border: none; border-radius: 3px; cursor: pointer; font-size: 11px; background: var(--vscode-button-background, #0e639c); color: var(--vscode-button-foreground, #fff); pointer-events: auto; position: relative; z-index: 100;';
		const onBtnEvent = (action: 'accept' | 'reject', eventType: string, e: MouseEvent | PointerEvent) => {
			console.log(`[DIFF-BTN] ${eventType} on ${action} button, txId=${txId}, target=${(e.target as HTMLElement)?.tagName}`);
			e.preventDefault();
			e.stopPropagation();
			e.stopImmediatePropagation();
			console.log(`[DIFF-BTN] firing _onDiffAction: ${action} ${txId}`);
			this._onDiffAction.fire({ txId, action });
		};
		acceptBtn.addEventListener('mousedown', (e) => onBtnEvent('accept', 'mousedown', e));
		acceptBtn.addEventListener('click', (e) => onBtnEvent('accept', 'click', e));
		acceptBtn.addEventListener('pointerdown', (e) => onBtnEvent('accept', 'pointerdown', e));

		const rejectBtn = document.createElement('button');
		rejectBtn.className = 'wescode-diff-btn wescode-diff-btn-reject';
		rejectBtn.textContent = '✗ Reject';
		rejectBtn.style.cssText = 'padding: 2px 8px; border: none; border-radius: 3px; cursor: pointer; font-size: 11px; background: var(--vscode-button-secondaryBackground, #3a3d41); color: var(--vscode-button-secondaryForeground, #fff); pointer-events: auto; position: relative; z-index: 100;';
		rejectBtn.addEventListener('mousedown', (e) => onBtnEvent('reject', 'mousedown', e));
		rejectBtn.addEventListener('click', (e) => onBtnEvent('reject', 'click', e));
		rejectBtn.addEventListener('pointerdown', (e) => onBtnEvent('reject', 'pointerdown', e));

		actionBar.appendChild(acceptBtn);
		actionBar.appendChild(rejectBtn);
		container.appendChild(actionBar);

		return container;
	}

	private _showConfirmedFromScratch(preview: IEditPreview): void {
		// Buffer is already synced from disk by confirmDiff() before this call.
		const editor = this._findEditorForPath(preview.path);
		if (!editor) {
			return;
		}
		const model = editor.getModel();
		if (!model) {
			return;
		}

		const decorations: { range: Range; options: IModelDecorationOptions }[] = [];
		const viewZoneIds: string[] = [];
		const hunks = preview.hunks ?? [];

		for (const hunk of hunks) {
			const newLineCount = hunk.newText ? hunk.newText.split('\n').length : 0;
			const startLine = Math.max(1, this._resolveHunkLine(model, hunk));
			for (let i = 0; i < newLineCount; i++) {
				const lineNum = startLine + i;
				if (lineNum <= model.getLineCount()) {
					decorations.push({
						range: new Range(lineNum, 1, lineNum, 1),
						options: ADDED_LINE_DECORATION,
					});
				}
			}

			if (hunk.oldText) {
				editor.changeViewZones(accessor => {
					const domNode = this._buildOldContentViewZone(hunk, preview.txId);
					const zone: IViewZone = {
						afterLineNumber: Math.max(0, startLine - 1),
						heightInLines: hunk.oldText.split('\n').length + 1,
						domNode,
						suppressMouseDown: true,
					};
					const id = accessor.addZone(zone);
					viewZoneIds.push(id);
				});
			}
		}

		const decorationIds = editor.deltaDecorations([], decorations.map(d => ({
			range: d.range,
			options: { ...d.options, stickiness: TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges },
		})));

		this.pendingEdits.set(preview.txId, {
			txId: preview.txId,
			path: preview.path,
			preview,
			decorationIds,
			viewZoneIds,
			editor,
			phase: 'confirmed',
		});
	}

	private _removeViewZones(editor: ICodeEditor, zoneIds: string[]): void {
		if (zoneIds.length === 0) {
			return;
		}
		editor.changeViewZones(accessor => {
			for (const id of zoneIds) {
				accessor.removeZone(id);
			}
		});
	}

	private _clearEdit(txId: string): void {
		const entry = this.pendingEdits.get(txId);
		if (!entry) {
			return;
		}
		if (entry.animationHandle !== undefined) {
			cancelAnimationFrame(entry.animationHandle);
		}
		if (entry.decorationIds.length > 0) {
			entry.editor.deltaDecorations(entry.decorationIds, []);
		}
		this._removeViewZones(entry.editor, entry.viewZoneIds);
		this.pendingEdits.delete(txId);
	}

	private _findEditorForPath(filePath: string): ICodeEditor | undefined {
		if (!filePath) {
			const control = this.editorService.activeTextEditorControl;
			return control as ICodeEditor | undefined;
		}

		for (const control of this.editorService.visibleTextEditorControls) {
			const editor = control as ICodeEditor | undefined;
			const model = editor?.getModel?.();
			if (model && model.uri.fsPath === filePath) {
				return editor;
			}
		}

		// Don't fallback to active editor — drawing diff on an unrelated file
		// produces confusing results (decorations at wrong lines).
		return undefined;
	}

	override dispose(): void {
		for (const txId of [...this.pendingEdits.keys()]) {
			this._clearEdit(txId);
		}
		super.dispose();
	}
}
