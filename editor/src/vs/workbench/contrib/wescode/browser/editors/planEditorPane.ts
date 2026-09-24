/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import * as dom from '../../../../../base/browser/dom.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { IDisposable } from '../../../../../base/common/lifecycle.js';
import { IStorageService } from '../../../../../platform/storage/common/storage.js';
import { ITelemetryService } from '../../../../../platform/telemetry/common/telemetry.js';
import { IThemeService } from '../../../../../platform/theme/common/themeService.js';
import { EditorPane } from '../../../../browser/parts/editor/editorPane.js';
import { IEditorOpenContext } from '../../../../common/editor.js';
import { IEditorGroup } from '../../../../services/editor/common/editorGroupsService.js';
import { IWescodeBackendService, IWescodeStreamEvent } from '../../../../../platform/wescode/common/wescode.js';
import { IFileService } from '../../../../../platform/files/common/files.js';
import { PlanEditorInput } from './planEditorInput.js';

const $ = dom.$;

interface PlanFrontmatter {
	title: string;
	session_id: string;
	status: string;
	analysis?: string;
	steps: Array<{
		id: string;
		description: string;
		status: string;
		result?: string;
	}>;
}

export class PlanEditorPane extends EditorPane {

	static readonly ID = 'wescode.planEditorPane';

	private _container!: HTMLElement;
	private _titleEl!: HTMLElement;
	private _statusEl!: HTMLElement;
	private _analysisSection!: HTMLElement;
	private _stepsContainer!: HTMLElement;
	private _streamListener: IDisposable | undefined;

	private _planData: PlanFrontmatter | undefined;
	private _sessionId: string | undefined;

	constructor(
		group: IEditorGroup,
		@ITelemetryService telemetryService: ITelemetryService,
		@IThemeService themeService: IThemeService,
		@IStorageService storageService: IStorageService,
		@IWescodeBackendService private readonly _backend: IWescodeBackendService,
		@IFileService private readonly _fileService: IFileService,
	) {
		super(PlanEditorPane.ID, group, telemetryService, themeService, storageService);
	}

	protected override createEditor(parent: HTMLElement): void {
		this._container = dom.append(parent, $('.plan-editor'));
		this._container.style.padding = '16px 24px';
		this._container.style.overflowY = 'auto';
		this._container.style.height = '100%';
		this._container.style.fontFamily = 'var(--vscode-font-family)';

		const header = dom.append(this._container, $('.plan-editor-header'));
		header.style.marginBottom = '16px';

		this._titleEl = dom.append(header, $('h2.plan-title'));
		this._titleEl.style.margin = '0 0 4px 0';
		this._titleEl.style.fontSize = '18px';
		this._titleEl.style.fontWeight = '600';

		this._statusEl = dom.append(header, $('span.plan-status'));
		this._statusEl.style.fontSize = '12px';
		this._statusEl.style.opacity = '0.7';

		this._analysisSection = dom.append(this._container, $('.plan-analysis'));
		this._analysisSection.style.marginBottom = '16px';
		this._analysisSection.style.display = 'none';

		this._stepsContainer = dom.append(this._container, $('.plan-steps'));
	}

	override async setInput(input: PlanEditorInput, options: undefined, context: IEditorOpenContext, token: CancellationToken): Promise<void> {
		await super.setInput(input, options, context, token);

		const content = await this._fileService.readFile(input.resource);
		if (token.isCancellationRequested) { return; }

		const text = content.value.toString();
		this._planData = this._parseFrontmatter(text);
		this._sessionId = this._planData?.session_id;

		this._render();
		this._listenToStream();
	}

	private _parseFrontmatter(text: string): PlanFrontmatter | undefined {
		const match = text.match(/^---\n([\s\S]*?)\n---/);
		if (!match) { return undefined; }

		const yaml = match[1];
		const result: PlanFrontmatter = { title: '', session_id: '', status: 'in_progress', steps: [] };

		for (const line of yaml.split('\n')) {
			const kv = line.match(/^(\w+):\s*(.*)$/);
			if (kv) {
				const [, key, value] = kv;
				if (key === 'title') { result.title = value.trim(); }
				if (key === 'session_id') { result.session_id = value.trim(); }
				if (key === 'status') { result.status = value.trim(); }
			}
		}

		const stepsMatch = yaml.match(/steps:\n([\s\S]*?)(?=\n\w|\n*$)/);
		if (stepsMatch) {
			const stepsBlock = stepsMatch[1];
			const stepEntries = stepsBlock.split(/\n\s*- /);
			for (const entry of stepEntries) {
				if (!entry.trim()) { continue; }
				const step: PlanFrontmatter['steps'][0] = { id: '', description: '', status: 'pending' };
				for (const sl of entry.split('\n')) {
					const skv = sl.match(/^\s*(\w+):\s*(.*)$/);
					if (skv) {
						const [, sk, sv] = skv;
						if (sk === 'id') { step.id = sv.trim(); }
						if (sk === 'description') { step.description = sv.trim(); }
						if (sk === 'status') { step.status = sv.trim(); }
						if (sk === 'result') { step.result = sv.trim(); }
					}
				}
				if (step.id) { result.steps.push(step); }
			}
		}

		return result;
	}

	private _render(): void {
		if (!this._planData) {
			this._titleEl.textContent = '无法解析计划';
			return;
		}

		this._titleEl.textContent = this._planData.title || '未命名计划';
		this._statusEl.textContent = this._statusLabel(this._planData.status);

		if (this._planData.analysis) {
			this._analysisSection.style.display = '';
			dom.clearNode(this._analysisSection);
			const toggle = dom.append(this._analysisSection, $('details'));
			const summary = dom.append(toggle, $('summary'));
			summary.textContent = '分析';
			summary.style.cursor = 'pointer';
			summary.style.fontWeight = '500';
			summary.style.marginBottom = '4px';
			const body = dom.append(toggle, $('pre'));
			body.textContent = this._planData.analysis;
			body.style.whiteSpace = 'pre-wrap';
			body.style.fontSize = '12px';
			body.style.opacity = '0.85';
		} else {
			this._analysisSection.style.display = 'none';
		}

		this._renderSteps();
	}

	private _renderSteps(): void {
		dom.clearNode(this._stepsContainer);
		if (!this._planData) { return; }

		for (const step of this._planData.steps) {
			const row = dom.append(this._stepsContainer, $('.plan-step'));
			row.style.display = 'flex';
			row.style.alignItems = 'flex-start';
			row.style.gap = '8px';
			row.style.padding = '6px 0';
			row.style.borderBottom = '1px solid var(--vscode-widget-border, rgba(128,128,128,0.2))';

			const icon = dom.append(row, $('span.plan-step-icon'));
			icon.textContent = this._stepIcon(step.status);
			icon.style.flexShrink = '0';
			icon.style.width = '20px';
			icon.style.textAlign = 'center';

			const info = dom.append(row, $('span.plan-step-info'));
			info.style.flex = '1';

			const desc = dom.append(info, $('span.plan-step-desc'));
			desc.textContent = step.description;
			desc.style.display = 'block';

			if (step.result) {
				const result = dom.append(info, $('span.plan-step-result'));
				result.textContent = step.result;
				result.style.display = 'block';
				result.style.fontSize = '12px';
				result.style.opacity = '0.7';
				result.style.marginTop = '2px';
			}
		}
	}

	private _listenToStream(): void {
		this._streamListener?.dispose();
		this._streamListener = this._register(this._backend.onDidStreamEvent(event => {
			this._handleStreamEvent(event);
		}));
	}

	private _handleStreamEvent(event: IWescodeStreamEvent): void {
		if (!this._planData) { return; }

		if (event.sessionId && this._sessionId && event.sessionId !== this._sessionId) {
			return;
		}

		if (event.type === 'plan_updated' && event.stepId) {
			const step = this._planData.steps.find(s => s.id === event.stepId);
			if (step) {
				if (event.plan?.status) { step.status = event.plan.status; }
				if (event.plan?.result) { step.result = event.plan.result; }
				this._renderSteps();
			}
		}

		if (event.type === 'plan_completed') {
			this._planData.status = 'completed';
			this._statusEl.textContent = this._statusLabel('completed');
		}

		if (event.type === 'plan_interrupted') {
			this._planData.status = 'interrupted';
			this._statusEl.textContent = this._statusLabel('interrupted');
		}
	}

	private _statusLabel(status: string): string {
		switch (status) {
			case 'in_progress': return '⏳ 进行中';
			case 'completed': return '✅ 已完成';
			case 'interrupted': return '❌ 已中断';
			default: return status;
		}
	}

	private _stepIcon(status: string): string {
		switch (status) {
			case 'pending': return '⏳';
			case 'in_progress': return '⏳';
			case 'done': return '✅';
			case 'skipped': return '⏸';
			default: return '⏳';
		}
	}

	override layout(dimension: dom.Dimension): void {
		if (this._container) {
			this._container.style.height = `${dimension.height}px`;
		}
	}

	override dispose(): void {
		this._streamListener?.dispose();
		super.dispose();
	}
}
