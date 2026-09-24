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
import { IWescodeWebviewEditorService, type WebviewPage } from '../editors/agentEditorService.js';
import { IWorkbenchLayoutService, Parts } from '../../../../services/layout/browser/layoutService.js';

export const AGENTS_PANE_ID = 'wescode.agentsQuickPane';
export const SKILLS_PANE_ID = 'wescode.skillsQuickPane';
export const KNOWLEDGE_PANE_ID = 'wescode.knowledgeQuickPane';
export const PROJECT_OVERVIEW_PANE_ID = 'wescode.projectOverviewQuickPane';
export const CRON_PANE_ID = 'wescode.cronQuickPane';
export const MEMORY_PANE_ID = 'wescode.memoryQuickPane';

class FeatureQuickPane extends ViewPane {
	private _opened = false;
	constructor(
		private readonly _page: WebviewPage,
		options: IViewPaneOptions,
		@IKeybindingService k: IKeybindingService,
		@IContextMenuService cm: IContextMenuService,
		@IConfigurationService c: IConfigurationService,
		@IContextKeyService ck: IContextKeyService,
		@IViewDescriptorService v: IViewDescriptorService,
		@IInstantiationService i: IInstantiationService,
		@IOpenerService o: IOpenerService,
		@IThemeService t: IThemeService,
		@ITelemetryService te: ITelemetryService,
		@IHoverService h: IHoverService,
		@IWescodeWebviewEditorService private readonly _wes: IWescodeWebviewEditorService,
		@IWorkbenchLayoutService private readonly _layoutService: IWorkbenchLayoutService,
	) {
		super(options, k, cm, c, ck, v, i, o, t, te, h);

		this._register(this.onDidChangeBodyVisibility(visible => {
			if (visible) {
				this._wes.openPage(this._page);
				this._layoutService.setPartHidden(true, Parts.SIDEBAR_PART);
			}
		}));
	}

	protected override renderBody(container: HTMLElement): void {
		super.renderBody(container);
		if (!this._opened) {
			this._opened = true;
			this._wes.openPage(this._page);
			this._layoutService.setPartHidden(true, Parts.SIDEBAR_PART);
		}
	}

	protected override layoutBody(height: number, width: number): void {
		super.layoutBody(height, width);
	}
}

export class WescodeAgentsQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('contacts', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}

export class WescodeSkillsQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('skills', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}

export class WescodeKnowledgeQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('knowledge', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}

export class WescodeProjectOverviewQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('projectOverview', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}

export class WescodeCronQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('cron', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}

export class WescodeMemoryQuickPane extends FeatureQuickPane {
	constructor(options: IViewPaneOptions, @IKeybindingService k: IKeybindingService, @IContextMenuService cm: IContextMenuService, @IConfigurationService c: IConfigurationService, @IContextKeyService ck: IContextKeyService, @IViewDescriptorService v: IViewDescriptorService, @IInstantiationService i: IInstantiationService, @IOpenerService o: IOpenerService, @IThemeService t: IThemeService, @ITelemetryService te: ITelemetryService, @IHoverService h: IHoverService, @IWescodeWebviewEditorService wes: IWescodeWebviewEditorService, @IWorkbenchLayoutService layout: IWorkbenchLayoutService) { super('pageMemory', options, k, cm, c, ck, v, i, o, t, te, h, wes, layout); }
}
