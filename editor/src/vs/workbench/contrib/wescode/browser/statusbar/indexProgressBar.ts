/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *
 *  StatusBar entry showing CKG index progress.
 *
 *  State machine:
 *    Hidden    → no workspace open, or index completed (auto-hide after 4s)
 *    Indexing  → "$(sync~spin) 索引 120/342"
 *    Done      → "$(check) 索引完成" (auto-hide after 4s)
 *    Error     → "$(warning) 索引失败" (persists until click → retry)
 *    Summary   → "$(zap) AI 理解度: 高" (persists when coverage data available)
 *
 *  Click action: opens Project Overview panel (or retries on error).
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IStatusbarService, StatusbarAlignment, IStatusbarEntryAccessor } from '../../../../services/statusbar/browser/statusbar.js';
import { IWescodeBackendService, IIndexProgressState } from '../../../../../platform/wescode/common/wescode.js';

const AUTO_HIDE_DELAY = 4000;

export class WescodeIndexProgressBar extends Disposable {
	static readonly ID = 'wescode.indexProgressBar';

	private readonly _statusbarService: IStatusbarService;
	private _entry: IStatusbarEntryAccessor | undefined;
	private _hideTimer: ReturnType<typeof setTimeout> | undefined;

	constructor(
		@IStatusbarService statusbarService: IStatusbarService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
	) {
		super();
		this._statusbarService = statusbarService;

		this._register(this.backend.onDidIndexProgress(state => this._onProgress(state)));
	}

	private _onProgress(state: IIndexProgressState): void {
		this._clearHideTimer();

		if (state.error) {
			const text = `$(warning) 索引失败`;
			const tooltip = `索引异常: ${state.error}\n\n点击重试`;
			this._updateEntry(text, tooltip, 'wescode.codeintel.reindex');
			return;
		}

		if (state.indexing) {
			const text = `$(sync~spin) 索引 ${state.indexed_files}/${state.total_files}`;
			const tooltip = state.current_file
				? `正在索引: ${state.current_file}\n${state.indexed_files}/${state.total_files} 文件`
				: `索引中 ${state.indexed_files}/${state.total_files} 文件`;
			this._updateEntry(text, tooltip);
		} else {
			this._showDone(state);
		}
	}

	private _showDone(state: IIndexProgressState): void {
		const cov = state as IIndexProgressState & { edge_coverage?: number; symbols_count?: number; blind_spot_count?: number };
		const hasCoverage = typeof cov.edge_coverage === 'number';

		if (hasCoverage) {
			const level = coverageLevel(cov.edge_coverage!);
			const syms = formatCount(cov.symbols_count ?? state.total_files);
			const blindCount = cov.blind_spot_count ?? 0;
			const text = `$(zap) AI 理解度: ${level.label}  $(symbol-numeric) ${syms} 符号  $(eye-closed) ${blindCount} 盲区`;
			const tooltipParts = [
				`AI 代码理解度: ${level.label}`,
				`索引文件: ${state.indexed_files}`,
				`符号总数: ${cov.symbols_count ?? state.total_files}`,
				`调用关系覆盖: ${Math.round(cov.edge_coverage! * 100)}%`,
			];
			if (blindCount > 0) {
				tooltipParts.push(`盲区: ${blindCount} 个函数/方法无调用关系`);
			}
			tooltipParts.push('', '点击打开项目概览');
			this._updateEntry(text, tooltipParts.join('\n'));
		} else {
			const text = `$(check) 索引完成 · ${state.indexed_files} 文件`;
			this._updateEntry(text, `代码索引完成\n${state.indexed_files} 文件\n\n点击打开项目概览`);
			this._scheduleHide();
		}
	}

	private _scheduleHide(): void {
		this._hideTimer = setTimeout(() => {
			this._hideEntry();
		}, AUTO_HIDE_DELAY);
	}

	private _clearHideTimer(): void {
		if (this._hideTimer !== undefined) {
			clearTimeout(this._hideTimer);
			this._hideTimer = undefined;
		}
	}

	private _hideEntry(): void {
		if (this._entry) {
			this._entry.dispose();
			this._entry = undefined;
		}
	}

	private _updateEntry(text: string, tooltip: string, command?: string): void {
		const props = {
			name: '代码索引',
			text,
			ariaLabel: text.replace(/\$\([^)]+\)\s*/g, ''),
			tooltip,
			kind: 'standard' as const,
			command: command ?? 'workbench.view.wescode.knowledge.resetViewContainerLocation',
		};
		if (this._entry) {
			this._entry.update(props);
		} else {
			this._entry = this._statusbarService.addEntry(
				props,
				WescodeIndexProgressBar.ID,
				StatusbarAlignment.LEFT,
				-998,
			);
		}
	}

	override dispose(): void {
		this._clearHideTimer();
		if (this._entry) {
			this._entry.dispose();
			this._entry = undefined;
		}
		super.dispose();
	}
}

function coverageLevel(coverage: number): { label: string } {
	if (coverage > 0.7) { return { label: '高' }; }
	if (coverage >= 0.3) { return { label: '中' }; }
	return { label: '低' };
}

function formatCount(n: number): string {
	if (n >= 1_000_000) { return `${(n / 1_000_000).toFixed(1)}M`; }
	if (n >= 1_000) { return `${(n / 1_000).toFixed(1)}K`; }
	return `${n}`;
}
