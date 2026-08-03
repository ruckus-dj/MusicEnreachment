# Review Desk Design Contract

## 1. Direction

An evidence-led operator desk: warm archival paper, ink-blue text, terracotta decision accent, and restrained depth. The review surface should feel deliberate and reversible rather than like a generic admin grid.

## 2. Tokens

- Surface: `#f4f1eb` page, `#fffdf8` panels, `#f0ebe2` evidence blocks.
- Ink: `#17202a` primary, `#66727d` muted, `#d9d3c9` divider.
- Semantic: `#bd4b2d` action and diff accent, `#2f6b55` ready/review state.
- Space: `8px` control gap, `12px` panel radius, `16px` base padding, `24px` section gap, `48px` desktop page inset.
- Type: Georgia for display/headings; system sans for body; UI monospace for state labels.
- Depth: low warm shadow on panels; no decorative motion beyond button state feedback.

## 3. Layout

The queue and evidence detail use a two-column desktop shell and collapse to one column below `760px`. Evidence is always shown as original and proposed columns before actions and audit history.

## 4. Accessibility

Use semantic headings, buttons for actions, readable contrast, responsive reflow, and no information conveyed by color alone.

## 5. Primitives

The review page uses `panel`, `state`, `evidence`, `actions`, and `audit` primitives. Their required states are default, ready/review semantic state, hoverable action, and narrow-screen stacked layout.

## 6. Accepted Debt

The first UI is server-rendered HTML with API action wiring supplied by the FastAPI surface; a richer client-side editor and component showcase belong to a later UI task.
