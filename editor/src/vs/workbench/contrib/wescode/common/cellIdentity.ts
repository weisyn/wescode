/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { IWorkspace } from '../../../../platform/workspace/common/workspace.js';

/**
 * INV-WS-09: Cell identity for a window is folders[0] from the workspace
 * definition — never the active editor's folder.
 *
 * Every `backend.initialize(workspacePath, ...)` caller MUST go through this
 * function so a multi-root workspace maps to exactly one Cell. The Go backend
 * hashes this directory path (workspaceCellID → ws-<hash>); a .code-workspace
 * FILE path is invalid here (spawn ENOTDIR) and a per-session / active-editor
 * directory would silently spawn a second Go process and split the Cell
 * (2026-08-16 regression: ws-f6486199 → ws-7913b4bf on first send after
 * restart with the active editor outside folders[0]).
 *
 * Active-editor paths are exec CWD hints (chat/send workDir), never Cell
 * identity.
 */
export function cellWorkDirForWorkspace(workspace: IWorkspace): string {
	return workspace.folders[0]?.uri.fsPath ?? '';
}
