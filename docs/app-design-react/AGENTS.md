# UI prototype

See [README.md](README.md) for the layout and commands, and the
[product contract](../design/decisions.md#интерфейс) for the transfer boundary.

- `src/ui/` is the portable UI. It may own presentation, host-neutral copy,
  models and binder contracts. It must not own or import demo fixtures, IDs,
  sample content, demo-only labels/badges or descriptions, simulated behavior,
  host providers, or entry points. Test-only data local to portable tests is
  fine; it must not enter the application bundle.
- The prototype's `src/demo/` providers supply demo data, actions, and any
  demo-only copy (including translated badges and descriptions). The
  application's `frontend/src/providers/` supplies real data and actions.
  Expose host-dependent content as model inputs, not hidden host checks in UI.
- Make portable changes in this prototype first. When the owner asks to
  transfer the UI, copy the whole `src/ui/` tree verbatim to `frontend/src/ui/`,
  including additions, renames, and deletions. Do not cherry-pick or adapt the
  copied files in place. Verify with `diff -qr`.
- If the application already contains independent UI changes, merge them into
  the prototype without overwriting newer work before taking the copy. Make
  application-specific adaptations outside `frontend/src/ui/`.
