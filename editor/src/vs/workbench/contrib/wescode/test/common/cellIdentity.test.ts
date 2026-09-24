/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import assert from 'assert';
import { URI } from '../../../../../base/common/uri.js';
import { IWorkspace } from '../../../../../platform/workspace/common/workspace.js';
import { cellWorkDirForWorkspace } from '../../common/cellIdentity.js';

suite('cellIdentity (INV-WS-09)', () => {

	function ws(...folderPaths: string[]): IWorkspace {
		return {
			id: 'test-workspace',
			folders: folderPaths.map((p, index) => ({
				uri: URI.file(p),
				name: p.split('/').pop() ?? p,
				index,
			})),
		} as IWorkspace;
	}

	test('multi-root: returns folders[0] directory path', () => {
		// The 2026-08-16 regression scenario: wesgine.git / wescode.git / wesui.git
		// multi-root window must resolve to wesgine.git (folders[0]) regardless of
		// which file the user is currently editing.
		const workspace = ws('/home/dev/projects/engine',
			'/home/dev/projects/wescode',
			'/home/dev/projects/wesui');
		assert.strictEqual(cellWorkDirForWorkspace(workspace),
			'/home/dev/projects/engine');
	});

	test('single-root: returns the only folder', () => {
		assert.strictEqual(cellWorkDirForWorkspace(ws('/home/user/project')),
			'/home/user/project');
	});

	test('folder order is authoritative: reordering changes identity', () => {
		// Cell identity follows the workspace definition, so reordering the
		// folders list intentionally yields a different (new) identity. This
		// locks the folders[0] contract — a caller may never substitute the
		// active editor folder.
		assert.strictEqual(cellWorkDirForWorkspace(ws('/a', '/b')), '/a');
		assert.strictEqual(cellWorkDirForWorkspace(ws('/b', '/a')), '/b');
	});

	test('empty workspace: config mode → empty string', () => {
		// Preheater passes '' for windows with no folders; the identity
		// function must mirror that instead of crashing or inventing a path.
		assert.strictEqual(cellWorkDirForWorkspace(ws()), '');
	});
});
