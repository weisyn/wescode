/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { URI } from '../../../../../base/common/uri.js';
import { EditorInputCapabilities, IUntypedEditorInput } from '../../../../common/editor.js';
import { EditorInput } from '../../../../common/editor/editorInput.js';

export class PlanEditorInput extends EditorInput {

	static readonly ID = 'wescode.planEditorInput';
	static readonly EDITOR_ID = 'wescode.planEditorPane';

	constructor(readonly resource: URI) {
		super();
	}

	override get typeId(): string {
		return PlanEditorInput.ID;
	}

	override get editorId(): string {
		return PlanEditorInput.EDITOR_ID;
	}

	override get capabilities(): EditorInputCapabilities {
		return EditorInputCapabilities.Readonly | EditorInputCapabilities.Singleton;
	}

	override getName(): string {
		return this.resource.path.split('/').pop() ?? 'Plan';
	}

	override matches(otherInput: EditorInput | IUntypedEditorInput): boolean {
		if (otherInput instanceof PlanEditorInput) {
			return otherInput.resource.toString() === this.resource.toString();
		}
		return false;
	}
}
