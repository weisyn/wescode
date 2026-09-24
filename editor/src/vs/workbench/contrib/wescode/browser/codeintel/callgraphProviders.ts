/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *
 *  P3 渗透点: CKG-backed HoverProvider + CodeLensProvider.
 *  Appends caller/callee info to hover; shows "⬆ N callers · ⬇ M callees"
 *  CodeLens above every top-level function/method.
 *
 *  Data source: codeintel/callgraph RPC (same as Project Overview panel).
 *--------------------------------------------------------------------------------------------*/

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWorkbenchContribution } from '../../../../common/contributions.js';
import { ILanguageFeaturesService } from '../../../../../editor/common/services/languageFeatures.js';
import { IWescodeBackendService } from '../../../../../platform/wescode/common/wescode.js';
import { ITextFileService } from '../../../../services/textfile/common/textfiles.js';
import type { HoverProvider, Hover, CodeLensProvider, CodeLens, CodeLensList } from '../../../../../editor/common/languages.js';
import type { ITextModel } from '../../../../../editor/common/model.js';
import { Position } from '../../../../../editor/common/core/position.js';
import { Range } from '../../../../../editor/common/core/range.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { MarkdownString } from '../../../../../base/common/htmlContent.js';
import { isUnderGitDir } from '../../common/wescodePath.js';

interface CallgraphNode {
	id: string;
	name: string;
	kind: string;
	file: string;
	line: number;
	signature?: string;
	is_focus: boolean;
	is_test?: boolean;
}

interface CallgraphEdge {
	source: string;
	target: string;
	kind: string;
	certainty: number;
}

interface CallgraphResult {
	nodes: CallgraphNode[];
	edges: CallgraphEdge[];
	impact_summary?: { direct_callers: number; indirect_dependents: number; affected_files: number };
}

class SymbolCache {
	private _cache = new Map<string, { data: CallgraphResult }>();

	get(key: string): CallgraphResult | undefined {
		return this._cache.get(key)?.data;
	}

	set(key: string, data: CallgraphResult): void {
		if (this._cache.size > 200) {
			const oldest = this._cache.keys().next().value;
			if (oldest) { this._cache.delete(oldest); }
		}
		this._cache.set(key, { data });
	}

	clearForFile(filePath: string): void {
		for (const [key] of this._cache) {
			if (key === `file:${filePath}` || key.startsWith(`${filePath}:`)) {
				this._cache.delete(key);
			}
		}
	}

	clear(): void {
		this._cache.clear();
	}
}

// ─── Hover Provider ────────────────────────────────────────────────────

export class WescodeCallgraphHoverProvider extends Disposable implements IWorkbenchContribution {
	static readonly ID = 'workbench.contrib.wescodeCallgraphHover';

	constructor(
		@ILanguageFeaturesService languageFeaturesService: ILanguageFeaturesService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ITextFileService textFileService: ITextFileService,
	) {
		super();

		const cache = new SymbolCache();

		this._register(textFileService.files.onDidSave(e => {
			cache.clearForFile(e.model.resource.fsPath);
		}));
		this._register(this.backend.onDidIndexComplete(() => {
			cache.clear();
		}));

		const provider: HoverProvider = {
			provideHover: async (model: ITextModel, position: Position, _token: CancellationToken): Promise<Hover | undefined> => {
				const word = model.getWordAtPosition(position);
				if (!word || word.word.length < 2) { return undefined; }

				const symbol = word.word;
				const filePath = model.uri.fsPath;

				const cacheKey = `${filePath}:${symbol}`;
				let result = cache.get(cacheKey);
				if (!result) {
					try {
						result = await this.backend.callgraphQuery({ symbol, file: filePath, depth: 1 });
						cache.set(cacheKey, result);
					} catch {
						return undefined;
					}
				}

				if (!result || result.nodes.length === 0) { return undefined; }

				const focusNode = result.nodes.find(n => n.is_focus);
				if (!focusNode) { return undefined; }

				const callerEdges = result.edges.filter(e => e.target === focusNode.id && e.kind === 'call');
				const calleeEdges = result.edges.filter(e => e.source === focusNode.id && e.kind === 'call');

				if (callerEdges.length === 0 && calleeEdges.length === 0) { return undefined; }

				const md = new MarkdownString('', true);
				md.isTrusted = true;
				md.supportHtml = true;

				md.appendMarkdown('---\n');
				md.appendMarkdown(`**CKG** \u2003`);

				if (callerEdges.length > 0) {
					md.appendMarkdown(`⬆ ${callerEdges.length} caller${callerEdges.length > 1 ? 's' : ''}`);
					const callerNodes = callerEdges
						.map(e => result!.nodes.find(n => n.id === e.source))
						.filter((n): n is CallgraphNode => !!n)
						.slice(0, 3);
					for (const c of callerNodes) {
						const shortFile = c.file ? c.file.split('/').slice(-2).join('/') : '';
						md.appendMarkdown(`\n- \`${c.name}\` *(${shortFile}:${c.line + 1})*`);
					}
					if (callerEdges.length > 3) {
						md.appendMarkdown(`\n- *...and ${callerEdges.length - 3} more*`);
					}
				}

				if (calleeEdges.length > 0) {
					if (callerEdges.length > 0) { md.appendMarkdown('\n\n'); }
					md.appendMarkdown(`⬇ ${calleeEdges.length} callee${calleeEdges.length > 1 ? 's' : ''}`);
					const calleeNodes = calleeEdges
						.map(e => result!.nodes.find(n => n.id === e.target))
						.filter((n): n is CallgraphNode => !!n)
						.slice(0, 3);
					for (const c of calleeNodes) {
						md.appendMarkdown(`\n- \`${c.name}\``);
					}
					if (calleeEdges.length > 3) {
						md.appendMarkdown(`\n- *...and ${calleeEdges.length - 3} more*`);
					}
				}

				if (result.impact_summary) {
					const s = result.impact_summary;
					md.appendMarkdown(`\n\n*Impact: ${s.direct_callers} direct · ${s.indirect_dependents} indirect · ${s.affected_files} files*`);
				}

				return {
					contents: [md],
					range: new Range(position.lineNumber, word.startColumn, position.lineNumber, word.endColumn),
				};
			}
		};

		this._register(languageFeaturesService.hoverProvider.register({ pattern: '**/*' }, provider));
	}
}

// ─── CodeLens Provider ─────────────────────────────────────────────────

export class WescodeCallgraphCodeLensProvider extends Disposable implements IWorkbenchContribution {
	static readonly ID = 'workbench.contrib.wescodeCallgraphCodeLens';

	constructor(
		@ILanguageFeaturesService languageFeaturesService: ILanguageFeaturesService,
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ITextFileService textFileService: ITextFileService,
	) {
		super();

		const cache = new SymbolCache();

		this._register(textFileService.files.onDidSave(e => {
			cache.clearForFile(e.model.resource.fsPath);
		}));
		this._register(this.backend.onDidIndexComplete(() => {
			cache.clear();
		}));

		const provider: CodeLensProvider = {
			onDidChange: undefined,

			provideCodeLenses: async (model: ITextModel, _token: CancellationToken): Promise<CodeLensList> => {
				const empty: CodeLensList = { lenses: [], dispose() { } };
				const filePath = model.uri.fsPath;
				if (!filePath || filePath.includes('node_modules') || isUnderGitDir(filePath)) {
					return empty;
				}

				const cacheKey = `file:${filePath}`;
				let result = cache.get(cacheKey);
				if (!result) {
					try {
						result = await this.backend.callgraphQuery({ file: filePath, depth: 1 });
						cache.set(cacheKey, result);
					} catch {
						return empty;
					}
				}

				if (!result || result.nodes.length === 0) { return empty; }

				const lenses: CodeLens[] = [];
				const focusNodes = result.nodes.filter(n => n.is_focus);

				for (const node of focusNodes) {
					if (node.line < 0) { continue; }

					const callerCount = result.edges.filter(e => e.target === node.id).length;
					const calleeCount = result.edges.filter(e => e.source === node.id).length;

					if (callerCount === 0 && calleeCount === 0) { continue; }

					const parts: string[] = [];
					if (callerCount > 0) { parts.push(`⬆ ${callerCount} caller${callerCount > 1 ? 's' : ''}`); }
					if (calleeCount > 0) { parts.push(`⬇ ${calleeCount} callee${calleeCount > 1 ? 's' : ''}`); }

					const range = new Range(node.line + 1, 1, node.line + 1, 1);
					lenses.push({
						range,
						command: {
							id: 'wescode.showCallgraph',
							title: parts.join(' · '),
							arguments: [node.name, filePath],
						},
					});
				}

				return { lenses, dispose() { } };
			},

			resolveCodeLens: async (_model: ITextModel, codeLens: CodeLens, _token: CancellationToken): Promise<CodeLens> => {
				return codeLens;
			},
		};

		this._register(languageFeaturesService.codeLensProvider.register({ pattern: '**/*.{go,ts,tsx,js,jsx,py,rs,java,kt,rb,swift,cpp,c}' }, provider));
	}
}
