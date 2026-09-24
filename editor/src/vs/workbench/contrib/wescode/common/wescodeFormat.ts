/*---------------------------------------------------------------------------------------------
 *  Copyright (c) wescode contributors. All rights reserved.
 *  Licensed under the MIT License.
 *--------------------------------------------------------------------------------------------*/

import { getWescodeLocale } from './wescodeLocale.js';

/**
 * Extension-side time formatting. Mirror of `@wesui`'s `formatChatTimestamp` /
 * `formatClock` (`wesui.git/src/lib/format.ts`) — keep the two in step.
 *
 * The duplication is bounded by the same wall that produced `wescodeLocale.ts`:
 * `editor/src/vs/**` is a VSCode fork built by VSCode's own layered build and
 * cannot import `@wesui` (React, Tailwind, webview-only locale context). The
 * mirror is therefore three lines of `Intl` options, not a second policy —
 * both sides delegate the actual month/day order, separator, and 12h/24h
 * choice to `Intl`, so they agree by construction rather than by convention.
 *
 * What must NOT reappear here: hand-built `${d.getMonth() + 1}/${d.getDate()}`
 * or `padStart` clocks. Those write CJK month/day order into the code and look
 * correct in both shipped languages, so the error only surfaces on the third.
 */

/** A moment in time, as it arrives on the wire: ISO string or epoch millis. */
export type WescodeInstant = string | number;

function parse(raw: WescodeInstant | undefined): Date | undefined {
	if (raw === undefined || raw === '') { return undefined; }
	const d = new Date(raw);
	return isNaN(d.getTime()) ? undefined : d;
}

/** "14:09" */
export function wescodeClock(raw: WescodeInstant | undefined): string {
	const d = parse(raw);
	if (!d) { return ''; }
	return d.toLocaleTimeString(getWescodeLocale(), { hour: '2-digit', minute: '2-digit' });
}

/** "08/30" */
export function wescodeDate(raw: WescodeInstant | undefined): string {
	const d = parse(raw);
	if (!d) { return ''; }
	return d.toLocaleDateString(getWescodeLocale(), { month: '2-digit', day: '2-digit' });
}

/**
 * Conversation-list timestamp: clock for today, date otherwise. Same rung as
 * `@wesui`'s `formatChatTimestamp` — deliberately no relative time, because
 * relative and absolute cannot share a column (a reader cannot order "just now"
 * against "08/29").
 *
 * Returns `''` for a missing or unparseable value: these strings land in
 * QuickPick `description` and tab tooltips, where echoing a raw wire value or
 * "Invalid Date" is worse than showing nothing.
 */
export function wescodeChatTimestamp(raw: WescodeInstant | undefined): string {
	const d = parse(raw);
	if (!d) { return ''; }
	const now = new Date();
	const sameDay =
		d.getFullYear() === now.getFullYear() &&
		d.getMonth() === now.getMonth() &&
		d.getDate() === now.getDate();
	return sameDay ? wescodeClock(raw) : wescodeDate(raw);
}
