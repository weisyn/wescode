/**
 * Auth state: the renderer half of `IWescodeAuthState`.
 *
 * The closed domain itself is not defined here. It lives in `@wesui`
 * (`src/auth/status.ts`), which the webview resolves through the same Vite
 * alias it uses for components, and which `weisyn/sdk/auth`'s mirror test locks
 * against the Go `Status` values. This file only adapts that domain to the one
 * thing the webview sees differently from wesclaw and wescraft: it reads a
 * camelCase state object off the `authMe` bridge call, not the platform's
 * snake_case wire.
 *
 * What the hand-written copy that used to be here cost, both live bugs:
 *
 *  1. Two components hand-rolled the payload and one spelled the nickname
 *     `display_name`. The extension host's `normalizeAuth` converts snake_case
 *     into camelCase before it reaches a webview, so `display_name` read
 *     `undefined` on every load and the save path wrote its optimistic update
 *     under a key nobody read back. An optional field spelled wrong is
 *     indistinguishable from an account with no nickname.
 *  2. Six call sites each wrote out `'authenticated' || 'degraded' ||
 *     'billing_overdue'`. That triple is INV-GATE-03 / INV-BILLING-02 — an
 *     overdue account still holds an identity JWT and must stay logged in — and
 *     a rule restated six times holds only until someone adds a fourth status.
 */

import { asAuthStatus, authHasIdentity, type AuthStatus } from '@wesui'

/**
 * The five wire values, re-exported so consumers name the domain once.
 *
 * Deliberately the wire union, not `AuthViewStatus`: the local copy this
 * replaces carried a `'loading'` member that nothing in the webview ever set —
 * `GateGuard` falls back to `'anonymous'` on timeout and `settings/model.tsx`
 * holds `AuthStatus | null`. A member no producer writes is a value every
 * `switch` and comparison has to carry anyway.
 */
export type { AuthStatus }

/** Mirror of `IWescodeAuthState`, as delivered by the `authMe` bridge call. */
export interface AuthState {
  status: AuthStatus
  userId?: string
  email?: string
  displayName?: string
}

/**
 * INV-GATE-03 / INV-BILLING-02: an identity JWT is present.
 *
 * Overdue is still logged in — billing gates the model call, not the session.
 * Treating it as logged out sends a paying customer to the login screen.
 *
 * Takes a bare `string | undefined` because callers read it off a bridge
 * response that may predate this build; an out-of-domain value narrows to
 * `undefined` and answers `false`, which is the fail-closed reading.
 */
export function isIdentityAuthenticated(status: string | undefined): boolean {
  const s = asAuthStatus(status)
  return s !== undefined && authHasIdentity(s)
}
