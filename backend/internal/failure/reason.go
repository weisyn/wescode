// Package failure names the failure conditions wescode itself asserts, as
// machine codes rather than sentences.
//
// Why this package exists: the backend does not know the reader's interface
// language, so any sentence it writes is a sentence in one language shown to
// every reader. Six Chinese literals had accumulated in `internal/rpc`, and
// the `no_workspace` arm three lines below one of them shows what the absence
// of this package cost: with no field to carry a code, the code was smuggled
// as the message text, and `web/src/lib/errors.ts` (deleted with this package's
// arrival) grew a six-way substring match to recover it ("no_workspace" || "no
// workspace" || "not initialized" || "backend not" || ...). That was not a
// frontend bug; it was the frontend reconstructing a field the wire refused to
// carry. Its replacement is `web/src/lib/failure.ts`, which mirrors this domain
// and matches on the field instead of the sentence.
//
// The split this package draws:
//
//   - A condition the product *names* — no folder open, engine still warming,
//     not signed in — gets a Reason. The renderer owns the sentence
//     (`t('failure.<reason>', '<中文>')`), so it is a function of the
//     interface language, which is where that decision belongs.
//   - A failure *propagated* from a lower layer (`err.Error()` off the engine,
//     SQLite, an LLM provider) has no Reason and passes through as diagnostic
//     text. Enumerating those would mean a code per upstream error string, and
//     hiding them would make bugs unreportable (ENG-5).
//
// The enforceable half of that split is a single rule: **no CJK in a Go error
// string** (`srcgate/nocjk_test.go`). A Chinese literal means somebody wrote
// product copy where diagnostics belong, and that is mechanically checkable —
// unlike "is this user-facing?", which is a judgement call every reviewer
// re-litigates.
//
// Deliberately per-product, not shared with wesclaw's identically-shaped
// package: a shared domain is total over the union of every product's
// conditions, so each product ships locale entries for reasons it can never
// emit. Dead keys are indistinguishable from live ones at the call site, and
// the next person maintaining wescode's locale file cannot tell which half is
// reachable. Two small closed domains beat one union with unreachable arms.
package failure

// Reason is a closed domain. Wire values are stable identifiers: the renderer
// keys locale lookups off them, so renaming one is a breaking change to the UI
// even though nothing in Go stops it.
type Reason string

const (
	// ReasonNoWorkspace: wescode is running without an open folder (Config
	// Mode). One workspace is one Cell, so with no folder there is no Cell and
	// therefore no CKG, no memory, no skills — every data surface is empty by
	// construction rather than by failure.
	//
	// This is the one Reason whose wire value predates the package: it was
	// already travelling as the *message* of a -32000 error, and the renderer
	// already routed on it (GateGuard shows "open a folder"). Keeping the
	// identifier byte-identical meant the migration moved it from Message to
	// Reason without a flag day — the renderer's guard changed from a substring
	// test to `isFailure(err, 'no_workspace')` and nothing else had to move.
	ReasonNoWorkspace Reason = "no_workspace"

	// ReasonEngineNotReady: the workspace Cell exists but is not Active —
	// still booting, or cooled by the idle temperature scheduler and warming
	// back up (Cool→Warm is 100-500ms, Cold 1-3s, so "retry shortly" is honest
	// advice rather than a shrug).
	//
	// This collapses two literals that stood three lines apart in
	// `ensureCellActive`: "AI 引擎启动中，请稍候" (cell == nil) and "AI 引擎未就绪，
	// 请稍后重试" (EnsureActive failed). The distinction is real to the engine
	// and invisible to the user — both mean "not yet, try again" and neither
	// offers a different action — so shipping two sentences only asked the
	// reader to tell apart two phrasings of the same wait.
	ReasonEngineNotReady Reason = "engine_not_ready"

	// ReasonLoginRequired: the method needs a weisyn session and there is
	// none. wescode's auth gate, not the engine's.
	ReasonLoginRequired Reason = "login_required"

	// ReasonSkillRepairFailed: `skill/repair` asked the model to rewrite a
	// broken SKILL.md and got nothing usable back — either the provider call
	// failed or it returned empty content.
	//
	// One Reason for both because the user's next action is the same (retry,
	// or fix the manifest by hand); the two literals it replaces differed only
	// in whether an upstream `err.Error()` was appended, and that detail
	// belongs in the diagnostic half, not in a second code.
	ReasonSkillRepairFailed Reason = "skill_repair_failed"

	// ReasonProviderInUseByCron: 要删的模型正被定时任务引用，删掉会让那些任务
	// 在下次触发时静默失败（它们此刻不在跑，所以没有任何东西会当场报错）。
	//
	// 它替换的中文字面量带着任务名列表（`「a、b」正在使用此模型`），而 detail 不显示
	// 只进日志（见 rpc/errFailureDetail）——所以句子不能依赖那个列表，只能指路：
	// "请先在定时任务里改掉或删掉引用它的任务"。这是个取舍，不是遗漏：把名字塞进
	// Reason 的句子里就等于把产品文案搬回后端，而后端不知道读者的语言。
	//
	// 任务名仍然经 detail 传给诊断面——排查时需要它，而用户不需要在这句话里看到它
	// （他要去的那个页面会列出全部任务）。
	ReasonProviderInUseByCron Reason = "provider_in_use_by_cron"
)

// AllReasons returns every Reason in the domain (INV-CLOSED-02). The renderer
// mapping must be total over this list; a Reason with no locale entry renders
// as its own identifier, which is the one failure mode this whole package
// exists to prevent.
func AllReasons() []Reason {
	return []Reason{
		ReasonNoWorkspace,
		ReasonEngineNotReady,
		ReasonLoginRequired,
		ReasonSkillRepairFailed,
		ReasonProviderInUseByCron,
	}
}

// Diagnostic returns the English developer-facing text that accompanies a
// Reason on the wire. It is what lands in logs and in `curl` output, and what
// the renderer shows only if it does not recognise the Reason — never the
// normal display path. Deliberately not a sentence a product would ship.
func (r Reason) Diagnostic() string {
	switch r {
	case ReasonNoWorkspace:
		return "no workspace folder open"
	case ReasonEngineNotReady:
		return "workspace cell not active"
	case ReasonLoginRequired:
		return "authentication required"
	case ReasonSkillRepairFailed:
		return "model returned no usable skill content"
	case ReasonProviderInUseByCron:
		return "provider still referenced by scheduled tasks"
	}
	return string(r)
}
