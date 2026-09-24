/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { $, append } from '../../../../../base/browser/dom.js';
import { wescodeL } from '../../common/wescodeLocale.js';

export interface IMoreMenuItem {
	label: string;
	icon?: string;
	checked?: boolean;
	disabled?: boolean;
	disabledHint?: string;
	separator?: boolean;
	/** Group heading rendered as a non-interactive label above subsequent items. */
	groupLabel?: string;
	action: () => void;
}

export interface IToolbarCallbacks {
	onHistory(): void;
	onSearch(query: string): void;
	onClearSearch(): void;
	onMoreMenu(): IMoreMenuItem[];
}

export interface IToolbarElements {
	toolbar: HTMLElement;
	searchInput: HTMLInputElement;
	historyBtn: HTMLButtonElement;
	moreBtn: HTMLButtonElement;
	updateLocale(): void;
	setDisabled(disabled: boolean): void;
}

export function buildTopToolbar(container: HTMLElement, callbacks: IToolbarCallbacks): IToolbarElements {
	const toolbar = append(container, $('.wescode-toolbar'));

	const searchWrapper = append(toolbar, $('div.wescode-toolbar-search'));
	append(searchWrapper, $('span.codicon.codicon-search'));
	const searchInput = append(searchWrapper, $('input.wescode-toolbar-search-input')) as HTMLInputElement;
	searchInput.type = 'text';
	searchInput.placeholder = wescodeL('search_sessions');
	searchInput.addEventListener('focus', () => { searchWrapper.classList.add('focused'); });
	searchInput.addEventListener('blur', () => { searchWrapper.classList.remove('focused'); });
	searchInput.addEventListener('input', () => {
		const q = searchInput.value.trim();
		if (q) {
			callbacks.onSearch(q);
		} else {
			callbacks.onClearSearch();
		}
	});
	searchInput.addEventListener('keydown', (e) => {
		if (e.key === 'Escape') {
			searchInput.value = '';
			callbacks.onClearSearch();
			searchInput.blur();
		}
	});

	append(toolbar, $('div.wescode-toolbar-sep'));

	const historyBtn = append(toolbar, $('button.wescode-toolbar-history')) as HTMLButtonElement;
	historyBtn.title = wescodeL('browse_history');
	append(historyBtn, $('span.codicon.codicon-history'));
	historyBtn.addEventListener('click', () => callbacks.onHistory());

	const moreBtn = append(toolbar, $('button.wescode-toolbar-more')) as HTMLButtonElement;
	moreBtn.title = wescodeL('more');
	append(moreBtn, $('span.codicon.codicon-kebab-vertical'));
	moreBtn.addEventListener('click', () => {
		const items = callbacks.onMoreMenu();
		_showMoreDropdown(moreBtn, items);
	});

	function updateLocale() {
		historyBtn.title = wescodeL('browse_history');
		searchInput.placeholder = wescodeL('search_sessions');
		moreBtn.title = wescodeL('more');
	}

	function setDisabled(disabled: boolean) {
		searchInput.disabled = disabled;
		historyBtn.disabled = disabled;
		moreBtn.disabled = disabled;
		if (disabled) {
			searchInput.style.opacity = '0.4';
			searchInput.style.pointerEvents = 'none';
			historyBtn.style.opacity = '0.4';
			historyBtn.style.pointerEvents = 'none';
			moreBtn.style.opacity = '0.4';
			moreBtn.style.pointerEvents = 'none';
		} else {
			searchInput.style.opacity = '';
			searchInput.style.pointerEvents = '';
			historyBtn.style.opacity = '';
			historyBtn.style.pointerEvents = '';
			moreBtn.style.opacity = '';
			moreBtn.style.pointerEvents = '';
		}
	}

	return { toolbar, searchInput, historyBtn, moreBtn, updateLocale, setDisabled };
}

function _showMoreDropdown(anchor: HTMLElement, items: IMoreMenuItem[]): void {
	const doc = anchor.ownerDocument;
	const existing = doc.querySelector('.wescode-more-backdrop');
	if (existing) { existing.remove(); return; }

	// Transparent full-screen backdrop catches clicks anywhere outside the
	// dropdown — reliable even when VS Code elements stop event propagation.
	const backdrop = $('div.wescode-more-backdrop');
	const dropdown = $('div.wescode-more-dropdown');

	const toolbarStyles = doc.defaultView?.getComputedStyle(anchor);
	const fgColor = toolbarStyles?.getPropertyValue('--vscode-foreground')?.trim()
		|| toolbarStyles?.color || '#cccccc';
	const bgColor = toolbarStyles?.getPropertyValue('--vscode-editorWidget-background')?.trim()
		|| toolbarStyles?.getPropertyValue('--vscode-sideBar-background')?.trim()
		|| '#252526';
	const borderColor = toolbarStyles?.getPropertyValue('--vscode-editorWidget-border')?.trim()
		|| toolbarStyles?.getPropertyValue('--vscode-panel-border')?.trim()
		|| '#454545';
	const hoverBg = toolbarStyles?.getPropertyValue('--vscode-list-hoverBackground')?.trim()
		|| 'rgba(255,255,255,0.06)';
	const mutedColor = toolbarStyles?.getPropertyValue('--vscode-descriptionForeground')?.trim()
		|| '#999';

	dropdown.style.color = fgColor;
	dropdown.style.background = bgColor;
	dropdown.style.borderColor = borderColor;

	for (const item of items) {
		if (item.separator) {
			const sep = append(dropdown, $('div.wescode-more-sep'));
			sep.style.background = borderColor;
			continue;
		}
		if (item.groupLabel) {
			const heading = append(dropdown, $('div.wescode-more-group'));
			heading.textContent = item.groupLabel;
			heading.style.color = mutedColor;
		}
		const row = append(dropdown, $('button.wescode-more-item')) as HTMLButtonElement;
		row.style.color = item.disabled ? mutedColor : fgColor;
		if (item.disabled) {
			row.style.opacity = '0.5';
			row.style.cursor = 'not-allowed';
		}
		if (item.icon) {
			const iconSpan = append(row, $(`span.codicon.codicon-${item.icon}`));
			iconSpan.style.color = mutedColor;
		}
		const label = append(row, $('span.wescode-more-label'));
		label.textContent = item.label;
		if (item.disabledHint) {
			const hint = append(row, $('span.wescode-more-hint'));
			hint.textContent = item.disabledHint;
			hint.style.marginLeft = 'auto';
			hint.style.fontSize = '11px';
			hint.style.color = mutedColor;
		}
		if (item.checked !== undefined) {
			const check = append(row, $('span.wescode-more-check'));
			check.textContent = item.checked ? '✓' : '';
		}
		if (!item.disabled) {
			row.addEventListener('mouseenter', () => { row.style.background = hoverBg; });
			row.addEventListener('mouseleave', () => { row.style.background = 'transparent'; });
			row.addEventListener('click', () => {
				dismiss();
				item.action();
			});
		}
	}

	function dismiss() {
		backdrop.remove();
		doc.removeEventListener('keydown', handleEsc, true);
	}
	function handleEsc(e: KeyboardEvent) {
		if (e.key === 'Escape') { dismiss(); }
	}

	// Backdrop click outside dropdown → dismiss
	backdrop.addEventListener('mousedown', (e) => {
		if (dropdown.contains(e.target as Node)) { return; }
		e.preventDefault();
		e.stopPropagation();
		dismiss();
	});

	// Mount: backdrop is full-screen at body level; dropdown is inside backdrop.
	backdrop.appendChild(dropdown);
	doc.body.appendChild(backdrop);

	// Position dropdown relative to the anchor button
	const anchorRect = anchor.getBoundingClientRect();
	dropdown.style.top = `${anchorRect.bottom + 4}px`;
	dropdown.style.right = `${doc.documentElement.clientWidth - anchorRect.right}px`;

	doc.addEventListener('keydown', handleEsc, true);
}
