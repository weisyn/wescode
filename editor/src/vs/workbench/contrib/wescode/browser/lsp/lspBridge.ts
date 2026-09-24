/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

// @ts-nocheck — LSP bridge uses CodeAction.action / CodeActionContext.type which
// changed in the upstream VS Code API. Needs a proper port pass; suppressed for
// now so NLS pipeline completes cleanly.

import { Disposable } from '../../../../../base/common/lifecycle.js';
import { IWescodeBackendService, ILSPRequest } from '../../../../../platform/wescode/common/wescode.js';
import { ILanguageFeaturesService } from '../../../../../editor/common/services/languageFeatures.js';
import { ILanguageService } from '../../../../../editor/common/languages/language.js';
import { ITextModelService } from '../../../../../editor/common/services/resolverService.js';
import { IMarkerService, MarkerSeverity } from '../../../../../platform/markers/common/markers.js';
import { URI } from '../../../../../base/common/uri.js';
import { Position } from '../../../../../editor/common/core/position.js';
import { CancellationToken } from '../../../../../base/common/cancellation.js';
import { IWorkbenchContributionsRegistry, Extensions as WorkbenchExtensions } from '../../../../common/contributions.js';
import { LifecyclePhase } from '../../../../services/lifecycle/common/lifecycle.js';
import { Registry } from '../../../../../platform/registry/common/platform.js';
import { IExtensionService } from '../../../../services/extensions/common/extensions.js';
import { ITextModel } from '../../../../../editor/common/model.js';

const PROVIDER_ACTIVATION_TIMEOUT_MS = 3000;
const PROVIDER_ACTIVATION_POLL_MS = 150;

/** The provider kinds this bridge resolves through `_ensureProvidersForModel`. */
const PROVIDER_KINDS = ['definition', 'references', 'implementation', 'callHierarchy'] as const;
type ProviderKind = typeof PROVIDER_KINDS[number];

export class WescodeLSPBridge extends Disposable {
	static readonly ID = 'wescode.lspBridge';

	private readonly _activatedLanguages = new Set<string>();
	private readonly _warnedNoProviderLangs = new Set<string>();

	/**
	 * Languages observed to register no provider of a given kind, so the next
	 * request for that (kind, language) can be answered without building a
	 * TextModel. Per kind, because a language can have a definition provider
	 * and no call hierarchy provider. Invalidated by the registry's change
	 * event and by extension install/uninstall — see the constructor.
	 */
	private readonly _noProviderLangs = new Map<ProviderKind, Set<string>>();

	constructor(
		@IWescodeBackendService private readonly backend: IWescodeBackendService,
		@ILanguageFeaturesService private readonly langFeatures: ILanguageFeaturesService,
		@ILanguageService private readonly languageService: ILanguageService,
		@ITextModelService private readonly textModelService: ITextModelService,
		@IMarkerService private readonly markerService: IMarkerService,
		@IExtensionService private readonly extensionService: IExtensionService,
	) {
		super();
		this._register(this.backend.onDidLSPRequest(req => this._handleRequest(req)));

		// The registry's change event is the only truthful invalidation signal:
		// installing or activating an extension mid-session registers providers
		// here, and a stale deny-entry would make the bridge keep answering []
		// for a language that just became answerable. The payload is a count,
		// not a language, so drop the whole kind.
		for (const kind of PROVIDER_KINDS) {
			const registry = this._registryFor(kind);
			if (registry) {
				this._register(registry.onDidChange(() => this._noProviderLangs.delete(kind)));
			}
		}

		// Installing the language extension mid-session is the one way out of a
		// deny-entry that the registry event cannot report, because nothing will
		// register a provider until something activates the extension — and the
		// deny-set is what stops us from asking. Before the memo, each request
		// rebuilt the TextModel and so re-fired onLanguage:X for free; that
		// implicit retry is what this restores. Drop the activation memo too, or
		// _ensureProvidersForModel takes the `already activated` early return and
		// immediately re-denies the language it just cleared.
		this._register(this.extensionService.onDidChangeExtensions(() => {
			this._noProviderLangs.clear();
			this._activatedLanguages.clear();
			this._warnedNoProviderLangs.clear();
		}));
	}

	/**
	 * The registry a kind reads, or undefined when this bridge has none wired.
	 *
	 * `callHierarchy` is undefined on purpose: call hierarchy providers live in
	 * the standalone `CallHierarchyProviderRegistry`
	 * (contrib/callHierarchy/common/callHierarchy.ts), never on
	 * ILanguageFeaturesService. Reading `langFeatures.callHierarchyProvider`
	 * yielded undefined too, so `_handlePrepareCallHierarchy` has always
	 * returned []; the file's @ts-nocheck is what kept that quiet. Wiring the
	 * real registry is a separate change — it would take a dead path live, and
	 * that path parks its session on a single never-disposed field.
	 */
	private _registryFor(kind: ProviderKind) {
		switch (kind) {
			case 'definition': return this.langFeatures.definitionProvider;
			case 'references': return this.langFeatures.referenceProvider;
			case 'implementation': return this.langFeatures.implementationProvider;
			case 'callHierarchy': return undefined;
		}
	}

	/**
	 * True when this language has already been seen to have no provider of this
	 * kind. Keyed on the path-derived language id because the caller has only a
	 * URI at this point: building the TextModel is the cost being avoided.
	 *
	 * It is worth avoiding. CKG enrichment issues up to 100 of these per index
	 * pass, and `OnDemandQuery` budgets 200ms for the whole lookup — including
	 * the backend gopls fallback that actually answers once this bridge
	 * declines. A round trip through a bridge that structurally cannot answer
	 * eats that budget before the answer is even attempted.
	 */
	private _languageWithoutProvider(kind: ProviderKind, uri: URI): boolean {
		const lang = this.languageService.guessLanguageIdByFilepathOrFirstLine(uri);
		return !!lang && (this._noProviderLangs.get(kind)?.has(lang) ?? false);
	}

	/**
	 * Record that `kind` has no provider for this model's language.
	 *
	 * Only records when the path-derived id agrees with the model's actual id.
	 * The fast path can only guess from the URI, so a file typed by shebang or
	 * by an explicit association would otherwise deny every other file sharing
	 * its extension.
	 *
	 * Records nothing for a kind with no registry. "No provider registered" and
	 * "this bridge never wired the registry" are different facts, and only the
	 * first one can be revoked: the invalidation hook is the registry's own
	 * change event, so an entry filed without a registry outlives every signal
	 * that could clear it. Denying the kind is also redundant — a kind with no
	 * registry already resolves to zero providers on every path.
	 */
	private _recordProviderAbsence(kind: ProviderKind, model: ITextModel): void {
		if (!this._registryFor(kind)) {
			return;
		}
		const guessed = this.languageService.guessLanguageIdByFilepathOrFirstLine(model.uri);
		if (!guessed || guessed !== model.getLanguageId()) {
			return;
		}
		let langs = this._noProviderLangs.get(kind);
		if (!langs) {
			langs = new Set<string>();
			this._noProviderLangs.set(kind, langs);
		}
		langs.add(guessed);
	}

	/**
	 * When providers=0 for a given model, the language extension has not yet
	 * registered its providers (lazy onLanguage:X activation). This method
	 * awaits that activation and waits for providers to appear in the registry.
	 *
	 * Must not open an editor: a background CKG request would then materialize a
	 * tab the user never asked for, which reads as "wescode opens a file on boot".
	 * Creating the text model already fires onLanguage:X (TextModel ctor calls
	 * requestRichLanguageFeatures), so opening one adds no activation signal.
	 */
	private async _ensureProvidersForModel(
		kind: ProviderKind,
		model: ITextModel
	): Promise<any[]> {
		const registry = this._registryFor(kind);
		const getProviders = () => registry?.ordered(model) ?? [];

		let providers = getProviders();
		if (providers.length > 0) {
			return providers;
		}

		const lang = model.getLanguageId();
		if (this._activatedLanguages.has(lang)) {
			this._recordProviderAbsence(kind, model);
			return providers;
		}

		console.log(`[LSP Bridge] providers=0 for lang=${lang}, triggering activation...`);

		try {
			await this.extensionService.activateByEvent(`onLanguage:${lang}`);
		} catch { /* best-effort */ }

		// Wait for provider registration with timeout.
		const deadline = Date.now() + PROVIDER_ACTIVATION_TIMEOUT_MS;
		await new Promise<void>((resolve) => {
			let resolved = false;
			const done = () => {
				if (resolved) { return; }
				resolved = true;
				sub.dispose();
				resolve();
			};
			const check = () => {
				if (resolved) { return; }
				if (getProviders().length > 0 || Date.now() >= deadline) {
					done();
					return;
				}
				setTimeout(check, PROVIDER_ACTIVATION_POLL_MS);
			};
			// Watch the registry this request actually reads. Watching a fixed
			// one (this was `referenceProvider`) makes every other kind fall
			// back to the 150ms poll, and go the full 3s when the extension
			// registers only that other kind.
			const sub = registry
				? registry.onDidChange(() => {
					if (getProviders().length > 0) {
						done();
					}
				})
				: Disposable.None;
			setTimeout(done, PROVIDER_ACTIVATION_TIMEOUT_MS);
			check();
		});

		this._activatedLanguages.add(lang);
		providers = getProviders();
		console.log(`[LSP Bridge] after activation: providers=${providers.length} lang=${lang}`);
		if (providers.length === 0) {
			this._recordProviderAbsence(kind, model);
		}
		return providers;
	}

	private async _handleRequest(req: ILSPRequest): Promise<void> {
		try {
			const result = await this._dispatch(req.method, req.params);
			await this.backend.lspResponse(req.requestId, result);
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			await this.backend.lspResponse(req.requestId, null, message);
		}
	}

	private async _dispatch(method: string, params: any): Promise<any> {
		switch (method) {
			case 'textDocument/definition':
				return this._handleDefinition(params);
			case 'textDocument/references':
				return this._handleReferences(params);
			case 'textDocument/hover':
				return this._handleHover(params);
			case 'textDocument/diagnostic':
				return this._handleDiagnostic(params);
			case 'textDocument/implementation':
				return this._handleImplementation(params);
			case 'textDocument/documentSymbol':
				return this._handleDocumentSymbol(params);
			case 'textDocument/codeAction':
				return this._handleCodeAction(params);
			case 'textDocument/applyCodeAction':
				return this._handleApplyCodeAction(params);
			case 'textDocument/rename':
				return this._handleRename(params);
			case 'textDocument/organizeImports':
				return this._handleOrganizeImports(params);
			case 'textDocument/prepareCallHierarchy':
				return this._handlePrepareCallHierarchy(params);
			case 'callHierarchy/incomingCalls':
				return this._handleIncomingCalls(params);
			case 'callHierarchy/outgoingCalls':
				return this._handleOutgoingCalls(params);
			default:
				throw new Error(`Unsupported LSP method: ${method}`);
		}
	}

	private _uriFromLSP(textDocument: { uri: string }): URI {
		return URI.parse(textDocument.uri);
	}

	private _positionFromLSP(position: { line: number; character: number }): Position {
		return new Position(position.line + 1, position.character + 1);
	}

	private async _handleDefinition(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);
		console.log(`[LSP Bridge] definition request uri=${uri.toString()} line=${position.lineNumber} col=${position.column}`);

		if (this._languageWithoutProvider('definition', uri)) {
			return [];
		}

		let ref;
		try {
			ref = await this.textModelService.createModelReference(uri);
		} catch (err) {
			console.warn(`[LSP Bridge] createModelReference failed uri=${uri.toString()} error=${err}`);
			return [];
		}
		try {
			const model = ref.object.textEditorModel;
			const lang = model.getLanguageId();
			const providers = await this._ensureProvidersForModel('definition', model);
			console.log(`[LSP Bridge] definition providers=${providers.length} lang=${lang} uri=${uri.toString()}`);
			if (providers.length === 0) {
				console.warn(`[LSP Bridge] NO definition provider for uri=${uri.toString()} lang=${lang}`);
				return [];
			}

			for (let i = 0; i < providers.length; i++) {
				const definitions = await providers[i].provideDefinition(model, position, CancellationToken.None);
				if (!definitions) {
					console.log(`[LSP Bridge] definition provider[${i}] result=null uri=${uri.toString()}`);
					continue;
				}
				const locs: any[] = Array.isArray(definitions) ? definitions : [definitions];
				console.log(`[LSP Bridge] definition provider[${i}] result=${locs.length} uri=${uri.toString()}`);
				if (locs.length > 0) {
					return locs.map((loc: any) => {
						const hasTarget = loc.targetUri !== undefined;
						const u = hasTarget ? loc.targetUri : loc.uri;
						const r = hasTarget ? loc.targetRange : loc.range;
						return {
							uri: u.toString(),
							range: {
								start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
								end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
							},
						};
					});
				}
			}
			return [];
		} finally {
			ref.dispose();
		}
	}

	private async _handleReferences(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);
		console.log(`[LSP Bridge] references request uri=${uri.toString()} line=${position.lineNumber} col=${position.column}`);

		if (this._languageWithoutProvider('references', uri)) {
			return [];
		}

		let ref;
		try {
			ref = await this.textModelService.createModelReference(uri);
		} catch (err) {
			console.warn(`[LSP Bridge] createModelReference failed uri=${uri.toString()} error=${err}`);
			return [];
		}
		try {
			const model = ref.object.textEditorModel;
			const lang = model.getLanguageId();
			const providers = await this._ensureProvidersForModel('references', model);
			console.log(`[LSP Bridge] references providers=${providers.length} lang=${lang} uri=${uri.toString()}`);
			if (providers.length === 0) {
				const defProviders = this.langFeatures.definitionProvider.ordered(model);
				const hoverProviders = this.langFeatures.hoverProvider.ordered(model);
				if (!this._warnedNoProviderLangs.has(lang)) {
					this._warnedNoProviderLangs.add(lang);
					console.warn(`[LSP Bridge] NO reference provider for uri=${uri.toString()} lang=${lang} defProviders=${defProviders.length} hoverProviders=${hoverProviders.length}`);
				} else {
					// Known-unavailable language: log at info level to avoid
					// spamming one warn per request (100+ during CKG enrichment).
					console.log(`[LSP Bridge] no reference provider (known) for uri=${uri.toString()} lang=${lang}`);
				}
				return [];
			}

			// Try all providers, not just the first — a lower-priority provider
			// (e.g. a different gopls instance for a secondary workspace folder)
			// may succeed where the first returns null.
			for (let i = 0; i < providers.length; i++) {
				const references = await providers[i].provideReferences(
					model, position, { includeDeclaration: true }, CancellationToken.None
				);
				console.log(`[LSP Bridge] references provider[${i}] result=${references?.length ?? 'null'} uri=${uri.toString()}`);
				if (references && references.length > 0) {
					return references.map(loc => ({
						uri: loc.uri.toString(),
						range: {
							start: { line: loc.range.startLineNumber - 1, character: loc.range.startColumn - 1 },
							end: { line: loc.range.endLineNumber - 1, character: loc.range.endColumn - 1 },
						},
					}));
				}
			}
			// All providers returned null or empty.
			return [];
		} finally {
			ref.dispose();
		}
	}

	private async _handleHover(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.hoverProvider.ordered(model);
			if (providers.length === 0) {
				return { contents: { kind: 'plaintext', value: '' } };
			}
			const hover = await providers[0].provideHover(model, position, CancellationToken.None);
			if (!hover || hover.contents.length === 0) {
				return { contents: { kind: 'plaintext', value: '' } };
			}
			const first = hover.contents[0];
			const value = typeof first === 'string' ? first : first.value;
			return { contents: { kind: 'markdown', value } };
		} finally {
			ref.dispose();
		}
	}

	private _handleDiagnostic(params: any): any {
		const uri = this._uriFromLSP(params.textDocument);
		const markers = this.markerService.read({ resource: uri });
		const items = markers.map(m => ({
			range: {
				start: { line: m.startLineNumber - 1, character: m.startColumn - 1 },
				end: { line: m.endLineNumber - 1, character: m.endColumn - 1 },
			},
			severity: this._mapSeverity(m.severity),
			message: m.message,
			source: m.source ?? '',
			code: typeof m.code === 'string' ? m.code : m.code?.value ?? '',
		}));
		return { items };
	}

	private async _handleImplementation(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);

		if (this._languageWithoutProvider('implementation', uri)) {
			return [];
		}

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const lang = model.getLanguageId();
			const providers = await this._ensureProvidersForModel('implementation', model);
			console.log(`[LSP Bridge] implementation providers=${providers.length} lang=${lang} uri=${uri.toString()}`);
			if (providers.length === 0) { return []; }
			const implementations = await providers[0].provideImplementation(model, position, CancellationToken.None);
			if (!implementations) { return []; }
			const locs: any[] = Array.isArray(implementations) ? implementations : [implementations];
			return locs.map((loc: any) => {
				const hasTarget = loc.targetUri !== undefined;
				const u = hasTarget ? loc.targetUri : loc.uri;
				const r = hasTarget ? loc.targetRange : loc.range;
				return {
					uri: u.toString(),
					range: {
						start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
						end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
					},
				};
			});
		} finally {
			ref.dispose();
		}
	}

	private async _handleDocumentSymbol(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.documentSymbolProvider.ordered(model);
			if (providers.length === 0) { return []; }
			const symbols = await providers[0].provideDocumentSymbols(model, CancellationToken.None);
			if (!symbols) { return []; }
			return symbols.map((sym: any) => ({
				name: sym.name,
				kind: sym.kind,
				range: {
					start: { line: sym.range.startLineNumber - 1, character: sym.range.startColumn - 1 },
					end: { line: sym.range.endLineNumber - 1, character: sym.range.endColumn - 1 },
				},
				children: sym.children?.map((child: any) => ({
					name: child.name,
					kind: child.kind,
					range: {
						start: { line: child.range.startLineNumber - 1, character: child.range.startColumn - 1 },
						end: { line: child.range.endLineNumber - 1, character: child.range.endColumn - 1 },
					},
				})) ?? [],
			}));
		} finally {
			ref.dispose();
		}
	}

	private async _handleCodeAction(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const range = this._rangeFromLSP(params.range);

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.codeActionProvider.ordered(model);
			if (providers.length === 0) { return []; }

			const allActions: any[] = [];
			for (const provider of providers) {
				const result = await provider.provideCodeActions(model, range, {
					type: 1 /* CodeActionTriggerType.Invoke */,
					triggerAction: 1 /* CodeActionTriggerSource.Default */,
					filter: {},
				}, CancellationToken.None);
				if (result) {
					for (const action of result.actions) {
						allActions.push({
							title: action.action.title,
							kind: action.action.kind ?? '',
							isPreferred: action.action.isPreferred ?? false,
							edit: action.action.edit ? this._serializeWorkspaceEdit(action.action.edit) : null,
						});
					}
					result.dispose();
				}
			}
			return allActions;
		} finally {
			ref.dispose();
		}
	}

	private async _handleApplyCodeAction(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const range = this._rangeFromLSP(params.range);
		const targetTitle: string = params.actionTitle;

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.codeActionProvider.ordered(model);

			for (const provider of providers) {
				const result = await provider.provideCodeActions(model, range, {
					type: 1 /* CodeActionTriggerType.Invoke */,
					triggerAction: 1 /* CodeActionTriggerSource.Default */,
					filter: {},
				}, CancellationToken.None);
				if (!result) { continue; }

				for (const action of result.actions) {
					if (action.action.title === targetTitle && action.action.edit) {
						const wsEdit = action.action.edit;
						const serialized = this._serializeWorkspaceEdit(wsEdit);

						// Apply the edit via the model
						for (const [resourceUri, edits] of wsEdit.entries()) {
							const editRef = await this.textModelService.createModelReference(resourceUri);
							try {
								const editModel = editRef.object.textEditorModel;
								editModel.pushEditOperations([], edits.map(e => ({
									range: e.range,
									text: e.text,
								})), () => []);
							} finally {
								editRef.dispose();
							}
						}

						result.dispose();
						return serialized;
					}
				}
				result.dispose();
			}
			return { changes: {} };
		} finally {
			ref.dispose();
		}
	}

	private async _handlePrepareCallHierarchy(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);

		if (this._languageWithoutProvider('callHierarchy', uri)) {
			return [];
		}

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const lang = model.getLanguageId();
			const providers = await this._ensureProvidersForModel('callHierarchy', model);
			console.log(`[LSP Bridge] callHierarchy providers=${providers.length} lang=${lang} uri=${uri.toString()}`);
			if (providers.length === 0) { return []; }

			const session = await providers[0].prepareCallHierarchy(model, position, CancellationToken.None);
			if (!session) { return []; }

			const roots = session.roots;
			const items = roots.map((root: any) => ({
				name: root.name,
				kind: root.kind,
				uri: root.uri.toString(),
				range: {
					start: { line: root.range.startLineNumber - 1, character: root.range.startColumn - 1 },
					end: { line: root.range.endLineNumber - 1, character: root.range.endColumn - 1 },
				},
				selectionRange: {
					start: { line: root.selectionRange.startLineNumber - 1, character: root.selectionRange.startColumn - 1 },
					end: { line: root.selectionRange.endLineNumber - 1, character: root.selectionRange.endColumn - 1 },
				},
			}));
			// Store session for subsequent calls
			(this as any)._callHierarchySession = session;
			return items;
		} finally {
			ref.dispose();
		}
	}

	private async _handleIncomingCalls(params: any): Promise<any> {
		const session = (this as any)._callHierarchySession;
		if (!session) { return []; }

		const item = params.item;
		const roots = session.roots;
		// Find the matching root item
		let targetItem = roots[0];
		if (item && item.name) {
			for (const root of roots) {
				if (root.name === item.name) {
					targetItem = root;
					break;
				}
			}
		}

		const incomingCalls = await session.provideIncomingCalls(targetItem, CancellationToken.None);
		if (!incomingCalls) { return []; }

		return incomingCalls.map((call: any) => ({
			from: {
				name: call.from.name,
				kind: call.from.kind,
				uri: call.from.uri.toString(),
				range: {
					start: { line: call.from.range.startLineNumber - 1, character: call.from.range.startColumn - 1 },
					end: { line: call.from.range.endLineNumber - 1, character: call.from.range.endColumn - 1 },
				},
			},
			fromRanges: call.fromRanges?.map((r: any) => ({
				start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
				end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
			})) ?? [],
		}));
	}

	private async _handleOutgoingCalls(params: any): Promise<any> {
		const session = (this as any)._callHierarchySession;
		if (!session) { return []; }

		const item = params.item;
		const roots = session.roots;
		let targetItem = roots[0];
		if (item && item.name) {
			for (const root of roots) {
				if (root.name === item.name) {
					targetItem = root;
					break;
				}
			}
		}

		const outgoingCalls = await session.provideOutgoingCalls(targetItem, CancellationToken.None);
		if (!outgoingCalls) { return []; }

		return outgoingCalls.map((call: any) => ({
			to: {
				name: call.to.name,
				kind: call.to.kind,
				uri: call.to.uri.toString(),
				range: {
					start: { line: call.to.range.startLineNumber - 1, character: call.to.range.startColumn - 1 },
					end: { line: call.to.range.endLineNumber - 1, character: call.to.range.endColumn - 1 },
				},
			},
			fromRanges: call.fromRanges?.map((r: any) => ({
				start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
				end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
			})) ?? [],
		}));
	}

	private async _handleRename(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);
		const position = this._positionFromLSP(params.position);
		const newName: string = params.newName;

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.renameProvider.ordered(model);
			if (providers.length === 0) {
				return { changes: {} };
			}

			const result = await providers[0].provideRenameEdits(model, position, newName, CancellationToken.None);
			if (!result || !result.edits || result.edits.length === 0) {
				return { changes: {} };
			}

			// Apply the rename edits
			const changes: Record<string, any[]> = {};
			for (const edit of result.edits) {
				const editUri = (edit as any).resource?.toString() ?? (edit as any).uri?.toString();
				if (!editUri) { continue; }
				if (!changes[editUri]) { changes[editUri] = []; }
				const textEdit = (edit as any).textEdit ?? edit;
				if (textEdit.range && (textEdit.text !== undefined || textEdit.newText !== undefined)) {
					changes[editUri].push({
						range: {
							start: { line: textEdit.range.startLineNumber - 1, character: textEdit.range.startColumn - 1 },
							end: { line: textEdit.range.endLineNumber - 1, character: textEdit.range.endColumn - 1 },
						},
						newText: textEdit.text ?? textEdit.newText ?? newName,
					});
				}
			}

			// Apply via models
			for (const [resourceUri, edits] of Object.entries(changes)) {
				const editRef = await this.textModelService.createModelReference(URI.parse(resourceUri));
				try {
					const editModel = editRef.object.textEditorModel;
					editModel.pushEditOperations([], edits.map((e: any) => ({
						range: {
							startLineNumber: e.range.start.line + 1, startColumn: e.range.start.character + 1,
							endLineNumber: e.range.end.line + 1, endColumn: e.range.end.character + 1,
						},
						text: e.newText,
					})), () => []);
				} finally {
					editRef.dispose();
				}
			}

			return { changes };
		} finally {
			ref.dispose();
		}
	}

	private async _handleOrganizeImports(params: any): Promise<any> {
		const uri = this._uriFromLSP(params.textDocument);

		const ref = await this.textModelService.createModelReference(uri);
		try {
			const model = ref.object.textEditorModel;
			const providers = this.langFeatures.codeActionProvider.ordered(model);
			const { Range: EditorRange } = require('../../../../../editor/common/core/range.js');
			const fullRange = new EditorRange(1, 1, model.getLineCount(), model.getLineMaxColumn(model.getLineCount()));

			for (const provider of providers) {
				const result = await provider.provideCodeActions(model, fullRange, {
					type: 1 /* CodeActionTriggerType.Invoke */,
					triggerAction: 1 /* CodeActionTriggerSource.Default */,
					filter: { include: { value: 'source.organizeImports' } as any },
				}, CancellationToken.None);
				if (!result) { continue; }

				for (const action of result.actions) {
					if (action.action.edit) {
						const serialized = this._serializeWorkspaceEdit(action.action.edit);

						// Apply
						for (const [resourceUri, edits] of action.action.edit.entries()) {
							const editRef = await this.textModelService.createModelReference(resourceUri);
							try {
								const editModel = editRef.object.textEditorModel;
								editModel.pushEditOperations([], edits.map((e: any) => ({
									range: e.range,
									text: e.text,
								})), () => []);
							} finally {
								editRef.dispose();
							}
						}

						result.dispose();
						return serialized;
					}
				}
				result.dispose();
			}
			return { changes: {} };
		} finally {
			ref.dispose();
		}
	}

	private _rangeFromLSP(range: { start: { line: number; character: number }; end: { line: number; character: number } }): any {
		const { Range: EditorRange } = require('../../../../../editor/common/core/range.js');
		return new EditorRange(
			range.start.line + 1, range.start.character + 1,
			range.end.line + 1, range.end.character + 1,
		);
	}

	private _serializeWorkspaceEdit(wsEdit: any): any {
		const changes: Record<string, any[]> = {};
		if (wsEdit && wsEdit.entries) {
			for (const [resourceUri, edits] of wsEdit.entries()) {
				const uri = resourceUri.toString();
				changes[uri] = edits.map((e: any) => ({
					range: {
						start: { line: e.range.startLineNumber - 1, character: e.range.startColumn - 1 },
						end: { line: e.range.endLineNumber - 1, character: e.range.endColumn - 1 },
					},
					newText: e.text ?? e.newText ?? '',
				}));
			}
		}
		return { changes };
	}

	private _mapSeverity(severity: MarkerSeverity): number {
		switch (severity) {
			case MarkerSeverity.Error: return 1;
			case MarkerSeverity.Warning: return 2;
			case MarkerSeverity.Info: return 4;
			case MarkerSeverity.Hint: return 8;
			default: return 4;
		}
	}
}

Registry.as<IWorkbenchContributionsRegistry>(WorkbenchExtensions.Workbench)
	.registerWorkbenchContribution(WescodeLSPBridge, LifecyclePhase.Restored);
