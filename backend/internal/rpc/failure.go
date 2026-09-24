package rpc

import (
	platform "github.com/weisyn/weisyn/sdk/failure"
	"github.com/weisyn/wescode/internal/failure"
)

// errFailure builds an RPCError naming a product condition, so the renderer
// supplies the sentence in the reader's language.
//
// The JSON-RPC code stays a transport-level bucket (-32000 for application
// errors); the Reason is what a consumer branches on. Those two used to be
// conflated — `no_workspace` rode as -32000's *message* while login rode as
// -32003 — which meant the renderer read one condition off the code and
// another off a substring of the prose.
func errFailure(reason failure.Reason) *RPCError {
	return &RPCError{
		Code:    -32000,
		Reason:  string(reason),
		Message: reason.Diagnostic(),
	}
}

// errFailureDetail is errFailure with upstream diagnostic text appended, for
// conditions the product names but whose cause came from below (a provider
// call that failed, a file that would not parse).
//
// detail reaches the renderer as-is and is *not* displayed: the renderer keys
// off Reason and keeps detail for the log/details view. So detail must be
// English — it is a diagnostic, and the reader's language is not knowable here.
func errFailureDetail(reason failure.Reason, detail string) *RPCError {
	if detail == "" {
		return errFailure(reason)
	}
	return &RPCError{
		Code:    -32000,
		Reason:  string(reason),
		Message: reason.Diagnostic() + ": " + detail,
	}
}

// errFrom builds the RPCError for an error that may or may not name a
// condition, which is what a handler holding an `error` from a service has.
//
// This is the shape that keeps a Reason from being dropped by accident.
// `internalError(err)` compiles, reads correctly, and silently flattens a
// platform failure into prose — so the renderer falls back to showing English
// and the localised sentence never runs. Every auth handler in this package did
// exactly that, and nothing failed: `platform.ReasonOf` reports `false` for an
// error it does not recognise rather than erroring, so a wrong password arrived
// as the platform's English diagnostic with an empty Reason, and the renderer's
// table for every platform reason had no way to be reached.
//
// Only the platform's domain is consulted, because only the platform's domain
// travels inside an error here: internal/failure has no error carrier at all
// (no NewError, no ReasonOf), and this product's own conditions are asserted at
// the handler with errFailure, where the reason is already in hand. Adding a
// second branch for a domain with no producer is the wiring that reads live and
// is not (wesgine anti-pattern 323) — the day internal/failure grows a carrier
// is the day this function grows the branch, and its absence will be a compile
// error at the first call site rather than a silent flattening.
func errFrom(err error) *RPCError {
	if err == nil {
		// A handler that decided to fail must say why; an empty message renders
		// as a blank error box with nothing to grep for.
		return internalError(nil)
	}
	if reason, ok := platform.ReasonOf(err); ok {
		return platformFailure(reason, platform.DetailOf(err))
	}
	return internalError(err)
}

// errPlatform names a platform condition this handler decided itself, before
// any service call — a missing field, a malformed body.
//
// These were spelled `&RPCError{Code: -32602, Message: "email and password are
// required"}`: a correct English sentence with no Reason, so the renderer had
// nothing to key on and showed the English. The platform already names every
// one of them, and reusing its names means an empty field reads the same
// whether this handler caught it or the platform did.
func errPlatform(reason platform.Reason) *RPCError {
	return platformFailure(reason, "")
}

// platformFailure carries a platform reason out to the renderer.
//
// Separate from errFailure rather than generic over the two Reason types: they
// are distinct types on purpose (each is a closed domain with its own totality
// test), and a shared `~string` constraint would let a caller mint an RPCError
// from any string — which is how an unregistered reason reaches the renderer
// and renders as its own identifier.
//
// detail reaches the renderer as-is and is *not* displayed: the renderer keys
// off Reason and keeps detail for the details/log view. So detail stays English
// — it is a diagnostic, and the reader's language is not knowable here.
func platformFailure(reason platform.Reason, detail string) *RPCError {
	msg := reason.Diagnostic()
	if detail != "" {
		msg += ": " + detail
	}
	return &RPCError{
		Code:    rpcCodeFor(reason),
		Reason:  string(reason),
		Message: msg,
	}
}

// rpcCodeFor derives the JSON-RPC code from the reason rather than taking it
// from the caller.
//
// The Reason is what the renderer branches on, so the code is only read by a
// generic client deciding whether the request or the server was at fault. That
// makes it a property of the condition, and asking every call site to restate
// it invites the pairing that was already here: a missing e-mail address rode
// as -32602 (caller's fault, correct) while a wrong password rode as -32603
// (server's fault, wrong — the server worked exactly as designed).
//
// The platform assigns every reason an HTTP status; this reuses that judgement
// instead of making a second one that can disagree with it.
func rpcCodeFor(reason platform.Reason) int {
	switch status := reason.HTTPStatus(); {
	case status >= 500:
		return -32603 // internal error
	case status == 400:
		return -32602 // invalid params
	default:
		// 401/402/403/404/409/429: the request was well-formed and the server
		// worked; it refused. Neither JSON-RPC bucket fits, which is what
		// -32000 (application error) is for.
		return -32000
	}
}
