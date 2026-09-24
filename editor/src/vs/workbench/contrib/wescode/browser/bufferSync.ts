/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../base/common/lifecycle.js';
import { URI } from '../../../../base/common/uri.js';
import { ICodeEditor, isCodeEditor } from '../../../../editor/browser/editorBrowser.js';
import { IWescodeBackendService } from '../../../../platform/wescode/common/wescode.js';
import { IEditorService } from '../../../services/editor/common/editorService.js';
import { ITextFileService } from '../../../services/textfile/common/textfiles.js';
import { IFileService } from '../../../../platform/files/common/files.js';
import { IWorkspaceContextService } from '../../../../platform/workspace/common/workspace.js';

/** Push dirty editor buffers to backend for AI read tool overlay. */
export class WescodeBufferSync extends Disposable {
	static readonly ID = 'wescode.bufferSync';
	static instance: WescodeBufferSync | undefined;

	private _debounceTimers = new Map<string, ReturnType<typeof setTimeout>>();
	private _trackedModels = new Map<string, { dispose(): void }>();
	private _fsChangeBuffer: string[] = [];
	private _fsFlushTimer: ReturnType<typeof setTimeout> | undefined;

	private static readonly MAX_BUFFER_SIZE = 5 << 20; // 5 MB
	private static readonly DEBOUNCE_MS = 200;
	private static readonly FS_BATCH_MS = 300;

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IEditorService private readonly editorService: IEditorService,
		@ITextFileService private readonly textFileService: ITextFileService,
		@IFileService private readonly fileService: IFileService,
		@IWorkspaceContextService private readonly workspaceService: IWorkspaceContextService,
	) {
		super();
		WescodeBufferSync.instance = this;

		this._setupFileSystemWatcher();

		this._register(this.textFileService.files.onDidChangeDirty(model => {
			const uri = model.resource;
			if (!this._isTrackableScheme(uri.scheme)) { return; }
			const path = this._resolvePath(uri);
			if (model.isDirty()) {
				this._trackModel(path, model);
			} else {
				this._untrackModel(path);
			}
		}));

		this._register(this.editorService.onDidActiveEditorChange(() => {
			this._syncActiveBuffer();
		}));

		this._register(this.textFileService.files.onDidSave(e => {
			const path = this._resolvePath(e.model.resource);
			this.backend.editorDidSave(path).catch(err => {
				this._logRPCError('editorDidSave', err);
			});
		}));

		this._register(this.editorService.onDidCloseEditor(e => {
			const uri = e.editor.resource;
			if (uri && this._isTrackableScheme(uri.scheme)) {
				const path = this._resolvePath(uri);
				this._untrackModel(path);
				this.backend.editorDidClose(path).catch(err => {
					this._logRPCError('editorDidClose', err);
				});
			}
		}));

		this._syncAllDirtyModels();
	}

	private _isTrackableScheme(scheme: string): boolean {
		return scheme === 'file' || scheme === 'untitled';
	}

	private _resolvePath(uri: URI): string {
		if (uri.scheme === 'untitled') {
			return `untitled:${uri.path || uri.fsPath}`;
		}
		return uri.fsPath;
	}

	private _syncAllDirtyModels(): void {
		for (const model of this.textFileService.files.models) {
			if (model.isDirty() && this._isTrackableScheme(model.resource.scheme)) {
				const path = this._resolvePath(model.resource);
				this._trackModel(path, model);
			}
		}
	}

	private _trackModel(path: string, model: { resource: URI; isDirty(): boolean }): void {
		if (this._trackedModels.has(path)) { return; }

		const control = this.editorService.activeTextEditorControl;
		if (control && isCodeEditor(control)) {
			const editorModel = (control as ICodeEditor).getModel();
			if (editorModel && this._resolvePath(editorModel.uri) === path) {
				const content = editorModel.getValue();
				this._scheduleSync(path, content);
				const d = editorModel.onDidChangeContent(() => {
					this._scheduleSync(path, editorModel.getValue());
				});
				this._trackedModels.set(path, { dispose: () => d.dispose() });
				return;
			}
		}

		this._pushDirtyContent(path);
		const textModel = (model as { textEditorModel?: { onDidChangeContent?(listener: () => void): { dispose(): void }; getValue(): string } }).textEditorModel;
		if (textModel && typeof textModel.onDidChangeContent === 'function') {
			const d = textModel.onDidChangeContent(() => {
				this._scheduleSync(path, textModel.getValue());
			});
			this._trackedModels.set(path, { dispose: () => d.dispose() });
		} else {
			this._trackedModels.set(path, { dispose: () => { /* no-op */ } });
		}
	}

	private _untrackModel(path: string): void {
		const tracked = this._trackedModels.get(path);
		if (tracked) {
			tracked.dispose();
			this._trackedModels.delete(path);
		}
	}

	private _pushDirtyContent(path: string): void {
		for (const model of this.textFileService.files.models) {
			if (this._resolvePath(model.resource) === path && model.isDirty()) {
				const textModel = model.textEditorModel;
				if (textModel) {
					const content = textModel.getValue();
					this._scheduleSync(path, content);
				}
				break;
			}
		}
	}

	private _syncActiveBuffer(): void {
		const control = this.editorService.activeTextEditorControl;
		if (!control || !isCodeEditor(control)) { return; }
		const editor = control as ICodeEditor;
		const model = editor.getModel();
		if (!model || !this._isTrackableScheme(model.uri.scheme)) { return; }

		const path = this._resolvePath(model.uri);

		this._flushSync(path);
		this._scheduleSync(path, model.getValue());

		this._untrackModel(path);
		const d = model.onDidChangeContent(() => {
			this._scheduleSync(path, model.getValue());
		});
		this._trackedModels.set(path, { dispose: () => d.dispose() });
	}

	private _scheduleSync(path: string, content: string): void {
		const existing = this._debounceTimers.get(path);
		if (existing) { clearTimeout(existing); }
		if (content.length > WescodeBufferSync.MAX_BUFFER_SIZE) { return; }
		this._debounceTimers.set(path, setTimeout(() => {
			this._debounceTimers.delete(path);
			this.backend.editorDidChange(path, content).catch(err => {
				this._logRPCError('editorDidChange', err);
			});
		}, WescodeBufferSync.DEBOUNCE_MS));
	}

	private _flushSync(path: string): void {
		const timer = this._debounceTimers.get(path);
		if (timer) {
			clearTimeout(timer);
			this._debounceTimers.delete(path);
		}
	}

	/** Flush ALL pending syncs immediately (call before Agent Run). */
	flushAll(): void {
		for (const [path, timer] of this._debounceTimers) {
			clearTimeout(timer);
			this._debounceTimers.delete(path);
		}
		for (const model of this.textFileService.files.models) {
			if (!model.isDirty()) { continue; }
			const textModel = model.textEditorModel;
			if (!textModel) { continue; }
			const path = this._resolvePath(model.resource);
			if (!path) { continue; }
			const content = textModel.getValue();
			if (content.length > WescodeBufferSync.MAX_BUFFER_SIZE) { continue; }
			this.backend.editorDidChange(path, content).catch(err => {
				this._logRPCError('flushAll', err);
			});
		}
	}

	private _logRPCError(method: string, err: unknown): void {
		if (err instanceof Error && err.message.includes('not started')) {
			return;
		}
		console.warn(`[WescodeBufferSync] ${method} failed:`, err);
	}

	private _setupFileSystemWatcher(): void {
		for (const folder of this.workspaceService.getWorkspace().folders) {
			const watcher = this.fileService.watch(folder.uri);
			this._register(watcher);
		}
		this._register(this.fileService.onDidFilesChange(e => {
			const paths: string[] = [];
			for (const uri of e.rawAdded) {
				if (uri.scheme === 'file') { paths.push(uri.fsPath); }
			}
			for (const uri of e.rawUpdated) {
				if (uri.scheme === 'file') { paths.push(uri.fsPath); }
			}
			if (paths.length > 0) {
				this._batchFSChanges(paths);
			}
		}));
	}

	private _batchFSChanges(paths: string[]): void {
		this._fsChangeBuffer.push(...paths);
		if (this._fsFlushTimer) { return; }
		this._fsFlushTimer = setTimeout(() => {
			this._fsFlushTimer = undefined;
			const batch = [...new Set(this._fsChangeBuffer)];
			this._fsChangeBuffer = [];
			if (batch.length > 0) {
				this.backend.editorFileChanged(batch).catch(err => {
					this._logRPCError('editor/fileChanged', err);
				});
			}
		}, WescodeBufferSync.FS_BATCH_MS);
	}

	override dispose(): void {
		for (const tracked of this._trackedModels.values()) { tracked.dispose(); }
		this._trackedModels.clear();
		for (const timer of this._debounceTimers.values()) { clearTimeout(timer); }
		this._debounceTimers.clear();
		if (this._fsFlushTimer) { clearTimeout(this._fsFlushTimer); }
		super.dispose();
	}
}
