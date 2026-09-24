/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { IViewPaneOptions, ViewPane } from '../../../../browser/parts/views/viewPane.js';
import { IKeybindingService } from '../../../../../platform/keybinding/common/keybinding.js';
import { IContextMenuService } from '../../../../../platform/contextview/browser/contextView.js';
import { IConfigurationService } from '../../../../../platform/configuration/common/configuration.js';
import { IInstantiationService } from '../../../../../platform/instantiation/common/instantiation.js';
import { IViewDescriptorService } from '../../../../common/views.js';
import { IOpenerService } from '../../../../../platform/opener/common/opener.js';
import { IThemeService } from '../../../../../platform/theme/common/themeService.js';
import { ITelemetryService } from '../../../../../platform/telemetry/common/telemetry.js';
import { IHoverService } from '../../../../../platform/hover/browser/hover.js';
import { IContextKeyService } from '../../../../../platform/contextkey/common/contextkey.js';
import { IWescodeBackendService, IAgentInfo, ISkillInfo } from '../../../../../platform/wescode/common/wescode.js';
import { IWescodeWebviewEditorService } from '../editors/agentEditorService.js';
import { DisposableStore } from '../../../../../base/common/lifecycle.js';
import { $, append, addDisposableListener, clearNode } from '../../../../../base/browser/dom.js';

export const AGENT_LIST_PANE_ID = 'wescode.agentListPane';
export const SKILL_LIST_PANE_ID = 'wescode.skillListPane';

export class WescodeAgentListPane extends ViewPane {

	private _container: HTMLElement | undefined;
	private _listEl: HTMLElement | undefined;
	private readonly _itemListeners = this._register(new DisposableStore());

	constructor(
		options: IViewPaneOptions,
		@IKeybindingService keybindingService: IKeybindingService,
		@IContextMenuService contextMenuService: IContextMenuService,
		@IConfigurationService configurationService: IConfigurationService,
		@IContextKeyService contextKeyService: IContextKeyService,
		@IViewDescriptorService viewDescriptorService: IViewDescriptorService,
		@IInstantiationService instantiationService: IInstantiationService,
		@IOpenerService openerService: IOpenerService,
		@IThemeService themeService: IThemeService,
		@ITelemetryService telemetryService: ITelemetryService,
		@IHoverService hoverService: IHoverService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IWescodeWebviewEditorService private readonly webviewEditor: IWescodeWebviewEditorService,
	) {
		super(options, keybindingService, contextMenuService, configurationService, contextKeyService, viewDescriptorService, instantiationService, openerService, themeService, telemetryService, hoverService);
	}

	protected override renderBody(container: HTMLElement): void {
		super.renderBody(container);
		this._container = container;
		this._container.style.padding = '8px';

		this._listEl = append(container, $('div.wescode-agent-list'));
		this._loadAgents();
	}

	private async _loadAgents(): Promise<void> {
		if (!this._listEl) { return; }
		try {
			const agents = await this.backend.listAgents();
			this._renderAgents(agents);
		} catch {
			this._listEl.textContent = 'Loading...';
		}
	}

	private _renderAgents(agents: IAgentInfo[]): void {
		if (!this._listEl) { return; }
		clearNode(this._listEl);
		this._itemListeners.clear();

		for (const agent of agents) {
			const row = append(this._listEl, $('div.wescode-agent-item'));
			row.style.cssText = 'display:flex;align-items:center;gap:8px;padding:4px 8px;cursor:pointer;border-radius:4px;';
			this._itemListeners.add(addDisposableListener(row, 'mouseenter', () => { row.style.background = 'var(--vscode-list-hoverBackground)'; }));
			this._itemListeners.add(addDisposableListener(row, 'mouseleave', () => { row.style.background = ''; }));
			this._itemListeners.add(addDisposableListener(row, 'click', () => {
				this.backend.setActiveAgent(agent);
				this.webviewEditor.openPage('newAgent');
			}));

			const emoji = append(row, $('span'));
			emoji.textContent = agent.emoji || '🤖';
			emoji.style.fontSize = '16px';

			const info = append(row, $('div'));
			info.style.cssText = 'flex:1;min-width:0;';

			const name = append(info, $('div'));
			name.textContent = agent.name;
			name.style.cssText = 'font-size:13px;font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';

			const role = append(info, $('div'));
			role.textContent = agent.role;
			role.style.cssText = 'font-size:11px;opacity:0.7;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';

			if (agent.isBuiltin) {
				const badge = append(row, $('span'));
				badge.textContent = '内置';
				badge.style.cssText = 'font-size:10px;opacity:0.5;padding:1px 4px;border:1px solid currentColor;border-radius:3px;';
			}
		}

		const addBtn = append(this._listEl, $('div.wescode-agent-add'));
		addBtn.style.cssText = 'display:flex;align-items:center;gap:8px;padding:8px;cursor:pointer;opacity:0.7;border-radius:4px;margin-top:4px;';
		this._itemListeners.add(addDisposableListener(addBtn, 'mouseenter', () => { addBtn.style.background = 'var(--vscode-list-hoverBackground)'; }));
		this._itemListeners.add(addDisposableListener(addBtn, 'mouseleave', () => { addBtn.style.background = ''; }));
		this._itemListeners.add(addDisposableListener(addBtn, 'click', () => this.webviewEditor.openPage('newAgent')));
		addBtn.textContent = '+ New Agent';
	}

	protected override layoutBody(height: number, width: number): void {
		super.layoutBody(height, width);
	}
}

export class WescodeSkillListPane extends ViewPane {

	private _listEl: HTMLElement | undefined;
	private readonly _itemListeners = this._register(new DisposableStore());

	constructor(
		options: IViewPaneOptions,
		@IKeybindingService keybindingService: IKeybindingService,
		@IContextMenuService contextMenuService: IContextMenuService,
		@IConfigurationService configurationService: IConfigurationService,
		@IContextKeyService contextKeyService: IContextKeyService,
		@IViewDescriptorService viewDescriptorService: IViewDescriptorService,
		@IInstantiationService instantiationService: IInstantiationService,
		@IOpenerService openerService: IOpenerService,
		@IThemeService themeService: IThemeService,
		@ITelemetryService telemetryService: ITelemetryService,
		@IHoverService hoverService: IHoverService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@IWescodeWebviewEditorService private readonly webviewEditor: IWescodeWebviewEditorService,
	) {
		super(options, keybindingService, contextMenuService, configurationService, contextKeyService, viewDescriptorService, instantiationService, openerService, themeService, telemetryService, hoverService);
	}

	protected override renderBody(container: HTMLElement): void {
		super.renderBody(container);
		container.style.padding = '8px';
		this._listEl = append(container, $('div.wescode-skill-list'));
		this._loadSkills();
	}

	private async _loadSkills(): Promise<void> {
		if (!this._listEl) { return; }
		try {
			const skills = await this.backend.listSkills();
			this._renderSkills(skills.filter(s => !s.slug.match(/^draft-[0-9a-f]{6,}$/)));
		} catch {
			this._listEl.textContent = 'Loading...';
		}
	}

	private _renderSkills(skills: ISkillInfo[]): void {
		if (!this._listEl) { return; }
		clearNode(this._listEl);
		this._itemListeners.clear();

		for (const skill of skills) {
			const row = append(this._listEl, $('div.wescode-skill-item'));
			row.style.cssText = 'display:flex;align-items:center;gap:8px;padding:4px 8px;cursor:pointer;border-radius:4px;';
			this._itemListeners.add(addDisposableListener(row, 'mouseenter', () => { row.style.background = 'var(--vscode-list-hoverBackground)'; }));
			this._itemListeners.add(addDisposableListener(row, 'mouseleave', () => { row.style.background = ''; }));
			this._itemListeners.add(addDisposableListener(row, 'click', () => this.webviewEditor.openPage('skills')));

			const icon = append(row, $('span'));
			icon.textContent = skill.enabled ? '✓' : '○';
			icon.style.cssText = `font-size:14px;width:18px;text-align:center;color:${skill.enabled ? 'var(--vscode-terminal-ansiGreen)' : 'var(--vscode-disabledForeground)'};`;

			const info = append(row, $('div'));
			info.style.cssText = 'flex:1;min-width:0;';

			const name = append(info, $('div'));
			name.textContent = skill.name;
			name.style.cssText = 'font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';

			if (skill.description) {
				const desc = append(info, $('div'));
				desc.textContent = skill.description;
				desc.style.cssText = 'font-size:11px;opacity:0.6;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';
			}
		}
	}

	protected override layoutBody(height: number, width: number): void {
		super.layoutBody(height, width);
	}
}
