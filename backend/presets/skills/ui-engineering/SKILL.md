---
name: ui-engineering
version: "1.0.0"
min_engine: "1.0.0"
description: "Frontend UI: components, layout, design tokens, interaction states, a11y. Use when building or reviewing UI code."
operators:
  - read
  - write
  - exec
metadata:
  enabled: true
  tags: [React, TypeScript, component, responsive, accessibility]
---

# UI Engineering

Build consistent, accessible UI components. Guard against interaction state gaps, design token violations, and reactive state bugs.

## When to Use

- Building new React/TypeScript UI components
- Reviewing frontend code for UX completeness
- Fixing interaction state bugs (hover/focus/disabled)
- Ensuring design system compliance

## Procedure

1. **Six-state coverage** — every interactive element must handle:
   - **Default**: base visual state
   - **Hover**: `hover:bg-surface-3` (mouse over)
   - **Active**: `active:bg-surface-hover` (mouse down / touch)
   - **Focus**: visible focus indicator for keyboard navigation
   - **Disabled**: `opacity-50 pointer-events-none`
   - **Loading**: skeleton / spinner / disabled with indicator

2. **Design token compliance**:
   - Colors: `bg-surface`, `text-text`, `border-border` — never raw Tailwind (`bg-gray-800`)
   - Typography: `text-body`, `text-small`, `text-caption` — never `text-sm`, `text-xs`
   - Radii: `rounded` (input/avatar), `rounded-md` (card), `rounded-lg` (dialog)
   - No `rounded-full` except scrollbar thumb
   - No focus rings (`focus:ring-*`) — use `shadow-accent` or no visible ring

3. **Component architecture**:
   - Props-in, callbacks-out: data via props, interaction via `onXxx` callbacks
   - Accept `className` prop; merge with `cn()` (tailwind-merge)
   - Export Props type: `export type XxxProps = { ... }`
   - No `fetch` / `useEffect` network calls inside components — data comes from props
   - No `import` of external stores (Zustand/Redux) — state managed by parent

4. **Reactive state discipline** (Zustand):
   - Selectors return **data**, not functions: `s => s.messages[id]` not `s => s.getMessages`
   - `useEffect` deps include ALL variables read in the effect body (including `if` branches)
   - Avoid derived state that can be computed from existing state

5. **Accessibility baseline**:
   - Interactive elements are `<button>` or `<a>`, not `<div onClick>`
   - Images have `alt` text; decorative images use `alt=""`
   - Form inputs have associated `<label>` elements
   - Color contrast ratio ≥ 4.5:1 for text
   - Keyboard navigation works for all interactive flows

6. **Performance**:
   - Memoize expensive computations with `useMemo`
   - Memoize callbacks passed to child components with `useCallback`
   - Lazy load heavy components with `React.lazy` + `Suspense`
   - Avoid re-rendering entire lists — use stable keys

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Zustand selector selects a function | Select data directly — function refs won't trigger re-render |
| `useEffect` deps missing conditional variables | Include ALL variables read inside the effect body |
| `rounded-full` for avatars, `text-sm` for body | Design tokens: `rounded`, `text-body` |
| `bg-gray-800`, `text-gray-400` | Semantic tokens: `bg-surface`, `text-dim` |
| `<div onClick>` for interactive elements | Use `<button>` with proper keyboard handling |
| Deliver without `tsc --noEmit` check | Always verify type safety before delivering |
| Import Zustand store inside a component library | Pass state via props; let the consumer manage state |

## Verification

- [ ] All interactive elements cover 6 states (default/hover/active/focus/disabled/loading)
- [ ] `tsc --noEmit` passes with zero errors
- [ ] No raw Tailwind color classes — all using design tokens
- [ ] No `<div onClick>` — interactive elements use semantic HTML
- [ ] Zustand selectors return data, not functions
