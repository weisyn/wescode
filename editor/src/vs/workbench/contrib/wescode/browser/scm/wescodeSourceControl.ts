/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { ISCMService, ISCMRepository } from '../../../../contrib/scm/common/scm.js';
import { IWescodeBackendService, IWescodeStreamEvent } from '../../../../../platform/wescode/common/wescode.js';
import { ILogService } from '../../../../../platform/log/common/log.js';

/**
 * WescodeSourceControl tracks files modified by the AI agent and provides
 * stage/commit convenience methods. Rather than registering a custom
 * ISCMProvider (which requires ITextModel/IObservable plumbing), it reads
 * the built-in Git extension's existing SCM repository and exposes
 * stage/commit via the backend RPC.
 */
export class WescodeSourceControl extends Disposable {
	static readonly ID = 'wescode.sourceControl';

	private _changedFiles = new Set<string>();

	constructor(
		@ISCMService private readonly scmService: ISCMService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ILogService private readonly logService: ILogService,
	) {
		super();

		this._register(this.backend.onDidStreamEvent((ev: IWescodeStreamEvent) => {
			if (ev.type === 'edit.applied' && ev.editPreview?.path && ev.editPreview.status === 'applied') {
				this._changedFiles.add(ev.editPreview.path);
				this.logService.debug(`[wescode-scm] tracked: ${ev.editPreview.path} (${this._changedFiles.size} total)`);
			}
		if (ev.type === 'done' || ev.type === 'interrupted') {
			if (this._changedFiles.size > 0) {
				this.logService.info(`[wescode-scm] run complete, ${this._changedFiles.size} files changed`);
			}
		}
		}));
	}

	get changedFiles(): string[] {
		return [...this._changedFiles];
	}

	getGitRepository(): ISCMRepository | undefined {
		for (const repo of this.scmService.repositories) {
			if (repo.provider.id === 'git') {
				return repo;
			}
		}
		return undefined;
	}

	async stage(paths: string[]): Promise<void> {
		await this.backend.gitStage(paths);
		this.logService.info(`[wescode-scm] staged ${paths.length} files`);
	}

	async stageAll(): Promise<void> {
		const files = this.changedFiles;
		if (files.length === 0) { return; }
		await this.stage(files);
	}

	async commit(message: string): Promise<void> {
		await this.backend.gitCommit(message);
		this.logService.info(`[wescode-scm] committed: ${message}`);
		this._changedFiles.clear();
	}

	clearTracking(): void {
		this._changedFiles.clear();
	}
}
