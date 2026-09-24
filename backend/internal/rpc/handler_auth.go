package rpc

import (
	"context"
	"encoding/json"

	"github.com/weisyn/weisyn/sdk/auth"
	platform "github.com/weisyn/weisyn/sdk/failure"
)

// The seven request-taking handlers below decode straight into the platform's
// shared request types (weisyn/sdk/auth) and let the Service validate.
//
// They used to declare nine anonymous structs with camelCase tags and their
// own presence checks — a copy that also existed in wesclaw and wescraft,
// while the platform's own HTTP handler read snake_case and checked two of the
// nine fields. Four copies of one contract, disagreeing on both the field
// names and the checks: the same request body was accepted over HTTP and
// rejected as incomplete over RPC.
//
// This product's copy was the weakest of the four in a way worth naming,
// because both defects read as correct code. The checks were combined
// (`if p.Email == "" || p.Password == ""` → "email and password are
// required"), so a user who filled in one field of two was told to fill in
// both — the message names the pair, not the gap. And the service's error came
// back as `&RPCError{Code: -32603, Message: err.Error()}`: a wrong password
// reported as an internal server error, in English, with no Reason for the
// renderer to key on. Nothing failed; the renderer's table of platform reasons
// simply had no reachable entry, so every localised auth sentence was dead.
//
// Nothing is validated here now, and that is the point — a check at the call
// site is a check the next call site omits.

func (h *Handler) handleAuthMe(ctx context.Context, _ Request) (any, *RPCError) {
	status := h.auth.Me(ctx)
	if status.Status.HasIdentity() {
		go h.engine.BootstrapWesProvider(context.WithoutCancel(ctx))
	}
	return status, nil
}

func (h *Handler) handleAuthLogin(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.LoginRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	status, err := h.auth.Login(ctx, p)
	if err != nil {
		return nil, errFrom(err)
	}
	if status.Status.HasIdentity() {
		go h.engine.BootstrapWesProvider(context.WithoutCancel(ctx))
	}
	return status, nil
}

func (h *Handler) handleAuthRegister(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.RegisterRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	status, err := h.auth.Register(ctx, p)
	if err != nil {
		return nil, errFrom(err)
	}
	if status.Status.HasIdentity() {
		go h.engine.BootstrapWesProvider(context.WithoutCancel(ctx))
	}
	return status, nil
}

func (h *Handler) handleAuthSendCode(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.SendCodeRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	// An empty purpose used to be rewritten to "login" here. The shared modal
	// only ever emits register or reset, so that default could not be reached
	// by the product — it existed to turn a caller bug into a code mailed for
	// the wrong flow. auth.Purpose is a closed domain now and rejects it.
	if err := h.auth.SendCode(ctx, p); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAuthResetPassword(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.ResetPasswordRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	if err := h.auth.ResetPassword(ctx, p); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAuthLogout(ctx context.Context, _ Request) (any, *RPCError) {
	if err := h.auth.Logout(ctx); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAuthUpdateProfile(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.UpdateProfileRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	if err := h.auth.UpdateProfile(ctx, p); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAuthChangePassword(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.ChangePasswordRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	if err := h.auth.ChangePassword(ctx, p); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (h *Handler) handleAuthDeleteAccount(ctx context.Context, req Request) (any, *RPCError) {
	var p auth.DeleteAccountRequest
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return nil, errPlatform(platform.ReasonMalformedRequest)
	}
	// Account deletion is irreversible, so DeleteAccountRequest.Validate
	// requires the current password — proof of possession is verified by the
	// platform, not asserted here.
	if err := h.auth.DeleteAccount(ctx, p); err != nil {
		return nil, errFrom(err)
	}
	return map[string]bool{"ok": true}, nil
}
