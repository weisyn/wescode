/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { RawContextKey } from '../../../../platform/contextkey/common/contextkey.js';

export const WESCODE_VIEWLET_ID = 'workbench.view.wescode';
export const WESCODE_VIEW_PANE_ID = 'workbench.panel.wescode.chat';

export const WESCODE_HAS_PENDING_DIFFS = new RawContextKey<boolean>('wescode.hasPendingDiffs', false);

export interface IDiffHunk {
	startLine: number;
	removedLines: string[];
	addedLines: string[];
}

export interface ICompletionRequest {
	prefix: string;
	suffix: string;
	language: string;
	filePath: string;
}

export interface ICompletionResult {
	insertText: string;
	range?: { startLine: number; startColumn: number };
}
