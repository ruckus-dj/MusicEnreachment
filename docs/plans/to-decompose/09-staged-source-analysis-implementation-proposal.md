# План 09 — предлагаемая декомпозиция реализации staged source analysis

**Дата:** 2026-10-08. **Статус: техническое предложение подготовлено; ожидает
явного одобрения владельца.** Это не разрешение начинать implementation. Требования
зафиксированы в [решениях владельца](../todo/09-staged-source-analysis-owner-decisions.md);
технические варианты и open issues — в [контракте D03–D05](../todo/09-staged-source-analysis-contract.md).
Фактическая карта текущего исполнения: [D01](../../reports/plan09-execution-map-2026-10-08.md).
Публикация вне области плана. План не создаёт продуктовые defaults сверх уже
одобренного processing concurrency default 4. Cache contract уже решён владельцем:
один последний успешный fingerprint на SHA, версия инструмента — provenance, не
часть cache identity. Технические предложения ниже всё ещё требуют approval.

## Перед началом

Реализация может начаться только после:

- явного owner approval контракта и последовательного implementation plan;
- разрешения оставшихся технических вопросов в ходе review до соответствующих
  зависимых commits: имена/layout/API и физическая migration strategy. Upgrade
  compatibility для опубликованных данных не требуется: это unpublished
  prototype, squash допустим, но не обязателен;
- независимого архитектурного review: выполнено 2026-10-08
  ([отчёт](../../reports/plan09-independent-review-2026-10-08.md)); этот review
  не является owner approval.

D02 сценарии, cache policy, reset scope и crash/recovery предложения внесены в
контракт. Сценарии — текстовая проработка для review, не отдельная фиксация их
owner acceptance. Продуктовых блокеров в записанных решениях сейчас не осталось;
новый обнаруженный конфликт нужно вынести владельцу, а не разрешать молча.

Если решение блокирует только отдельный этап, его нельзя «решать» реализацией или
миграцией; независимые этапы допустимы лишь при доказанном отсутствии зависимости.

## Правила выполнения

- Маленькие последовательные commits, каждый оставляет компилируемую/проверяемую
  систему и имеет свой acceptance. Не объединять весь feature в один PR.
- Единственный локальный gate — **`task verify`**. Не запускать локально узкие
  suites/platform builds. При сбое `task verify` разрешён только узкий запуск для
  диагностики конкретного сбоя.
- Каждый implementation PR указывает ревизию и результат `task verify`; изменившие
  миграции/API/runtime части проходят требуемый GitHub CI. Native platform matrix,
  Compose/deployment smoke и ручные browser/runtime доказательства — CI/отдельная
  acceptance, не дополнительные локальные тестовые команды.
- `test_stand/` — пользовательские данные, не использовать/не менять в рамках
  разработки и проверки.
- Любой кодовый этап, не указанный в плане или выводящий scope в matching,
  publication, periodic scan, auto retry, quota или deletion source bytes, требует
  отдельного решения.

## Последовательные implementation commits

| Commit | Содержимое и основные точки изменения | Зависимость / безопасный результат | Проверяемый результат |
| --- | --- | --- | --- |
| I01 — контракт/миграционный skeleton | После одобрения физического дизайна: schema для per-root mode (явное значение; migration может быть squashed либо новой, по решению implementation review), typed concurrency setting default 4; output artifact schema без worker side effects. `backend/internal/migrations/`, `backend/internal/persistence/source_repository.go`, `backend/internal/settings/`. | Owner approval и выбранная migration/down policy. Не требуется legacy compatibility для опубликованных инсталляций; prototype squash разрешён, не обязателен. | Constraints, clean setup и выбранный migration path; отсутствие неявных orphan artifact refs; `task verify`. |
| I02 — root/settings service model | `backend/internal/service/source_roots.go`, `backend/internal/service/settings.go` (точные имена перепроверить), persistence repositories; требовать явный mode create, future-only update, concurrency setting. | I01. Без UI/API/generated client изменений; старое поведение явно отображается `in_place`. | Repo/service validation и concurrent root mutation acceptance через полный gate. |
| I03 — scan enumeration-only (подготовка) | `backend/internal/service/source_scan.go`, `backend/internal/jobs/scan_worker.go`, `backend/internal/persistence/source_candidates.go`, `source_repository.go`: подготовить enumeration-only traversal и reconciliation candidates; недоступность root/subtree reconciles удалением locations только затронутой области; одна работа на новый/изменённый кандидат. Удалить analysis/probe/hash из candidate path без удаления общей preparer logic, но пока не переключать scan: прежний pipeline остаётся живым. | I02; только подготовка enumeration/reconciliation code. Cutover scan на enumeration-only + per-file admission в I03 не входит и выполняется атомарно в I04 после проверенной capability. | Preparation компилируется и проходит gate; behavior не переключён (прежний pipeline живой); inaccessible subtree reconciled; `task verify`. |
| I04 — per-file durable admission / output gate | `backend/internal/service/source_analysis_start.go`, `source_analysis_pending.go`, `backend/internal/persistence/source_analysis_admission.go`, `source_analysis_enqueue.go`, `source_analysis_snapshot.go`, coordination helpers. Минимальный operation/job IDs+intent; текущий root mode и settings читаются при admission/execution (admitted mode не персистится); операция+River `InsertTx` atomic; fences и lock ordering. Реализует per-file admission и атомарно переключает scan на enumeration-only после проверенной capability. | I01–I03 (I03 — только подготовка enumeration/reconciliation, без cutover). Safe intermediate: operations admitted atomically, retry does not copy serialized full settings. | Commit/rollback, duplicate suppression, source/root/settings mutation races, stale delivery; `task verify`. |
| I05 — staged artifact ownership & serial copy | Новый focused persistence artifact repository и service preparer рядом с `backend/internal/service/source_analysis_preparer.go`; `backend/internal/jobs/source_analysis_worker.go`. Exclusive owned path under proposed output layout; source pre/post stat, one sequential copy, current delivery fence. In-place path unchanged. | I04 plus resolved output layout/path validation. Safe intermediate: staged prep can create/verify/remove only its owned test artifacts; no result publication yet. | Partial copy, mismatch, ENOSPC, symlink/path containment, collisions, no overwrite, fencing foreign/live owners; `task verify`. |
| I06 — shared prepared input and independent steps | `backend/internal/jobs/source_analysis_worker.go` (`runWorkGroup`, `prepareWorkSteps`), `backend/internal/persistence/source_analysis_steps.go` (`Claim...`, `Apply...`), service preparer. Pass one prepared input through SHA/probe/fingerprint; each step commits independently. Fingerprint lookup identity is SHA only; successful result replacement is atomic and preserves the prior success until new success. | I05 and this owner-approved cache contract. Safe intermediate: existing in-place behavior remains; staged tools receive copy, not source. | Probe once, one sequential source read/copy, SHA toggles/no-backfill, independent failures/success, cache replacement-on-success, tool provenance; `task verify`. |
| I07 — retained copy retry/restart/recovery | `backend/internal/jobs/reconcile.go`, `backend/internal/persistence/source_analysis_recovery.go`, new artifact recovery methods, operations retry path. Reuse valid retained copy while requested set incomplete; invalidate missing/stale refs and create anew; never clear live/foreign artifact. | I05–I06; lifecycle/crash table approved. Safe intermediate: failed operation is retryable and successes remain; no unowned cleanup. | Recovery at every table boundary; old attempt cannot delete/rewrite new artifact; live River job untouched; `task verify`. |
| I08 — explicit bulk cleanup | API/service/persistence cleanup coordinator; selection only eligible artifacts; result per artifact, cleanup errors retain row and do not fail analysis. | I07; API behavior reviewed. Safe intermediate: no automatic unlink and no artifact is deleted by discovery. | eligible/ineligible selection, live/foreign fence, missing file, permission failure, idempotence; `task verify`. |
| I09 — output setting/reset and logical areas | Existing Setup/settings validation (`backend/internal/api/setup.go`, settings API/registry, `backend/internal/app/` composition), output coordinator, `deploy/compose/docker-compose.yml`. Mandatory output path; validate-only first. Under one global output/admission gate, re-read setting and running/claimed execution, reject running/claimed execution including the queued-to-running claim race, then transactionally update setting and invalidate all queued/task rows and old-output DB refs. Preserve local entities/source/inventory/analysis; leave old files untouched. | I01–I08 per data references. Reset cannot ship until the gate covers admissions and worker claim-to-running, and the filesystem journal/recovery sequence is implemented. Safe intermediate: reset unavailable until both are complete. | Admission/claim/reset races; failed validation no mutation; DB rollback preserves old setting/refs; crash at each mkdir/transaction boundary recovered from journal; all reset scope invalidated, old physical files untouched; `task verify`; CI deployment smoke. |
| I10 — API/OpenAPI/generated client | Huma DTO/routes under `backend/internal/api/sources.go`, source analysis/settings APIs; run repository `task generate`; update React call sites only from generated contract. Endpoints/DTO resolved per contract. | I02–I09 API shapes stable; no direct generated-file edits. Safe intermediate: backend contract + generated artifacts synchronized. | `task generate` included in `task verify`; generated drift check; API error and transaction cases; GitHub CI. |
| I11 — Sources/Settings UI | `frontend/src/features/sources/` (root create/edit, scan list, inspector, cleanup) and `frontend/src/features/settings/SettingsScreen.tsx`; reuse existing operation REST reread/SSE wake-up. Accessible loading/error/dirty/retry/reset flow. | I10. Safe intermediate: no fake mode default, no unsupported actions; current features remain usable. | RTL/accessibility coverage in full gate; review light/dark desktop + 375px action retention; browser CI/manual evidence as explicitly reported. |
| I12 — integration acceptance | Integration fixtures for source tree, DB/River, real managed tools; deployment fixture; docs/status update and independent reviewer. No application behavior additions beyond approved contract. | I01–I11 complete. | `task verify`; GitHub native platform matrix, PostgreSQL/River integration and Compose deployment smoke; real runtime staged vs in-place; source bytes unchanged; artifact ownership/recovery evidence; independent acceptance report. |

## Cross-commit dependency notes

- I03 may develop an enumeration-only traversal only after candidate reconciliation
  behavior is specified; do not leave interim code that reports scan success while
  silently omitting required work. The previous pipeline must stay live until the
  enumeration-only + per-file admission switch in I04 is atomic — never ship a stage where
  processing is disabled.
- I04/I05 need a minimal current-input contract: preserve only IDs/explicit intent
  and delivery fence. No serialized full runtime settings, paths, or tool config in
  `input_snapshot` or River args. Do not persist an admitted mode: queued and
  retried work reads the current per-root mode from the DB at execution.
- I06–I07 must use existing shared step engine and preserve successes independently;
  do not build a second analysis pipeline.
- I08 is explicit operator action; no timer/automatic cleanup. A cleanup failure
  does not change the analysis operation's success.
- I09 uses a single deterministic lock order: global output/admission gate before
  root/operation locks. Every operation admission and queued-to-running claim takes
  the shared gate; reset takes it exclusively, rechecks running/claimed execution,
  and holds it through DB commit. Thus no task can slip between the active-task
  check and invalidation. Reset invalidates all queue/task state and every DB
  reference to old output, not only staging/publication subsets; preserve local
  entities, source links, inventory and analysis. Never unlink old output files.
- I09 filesystem/DB coordination is a recoverable journal, not a distributed
  transaction: validate candidate without mutation; acquire the cross-process
  exclusive output gate (proposed PostgreSQL advisory lock); check there is no
  unfinished reset token; create a durable reset token in `preparing`; create only
  missing logical directories with exclusive creation and record exactly which
  directories this token owns. Every admission/worker claim checks for a preparing
  token while holding the shared gate and refuses admission/claim until startup
  recovery clears it. The lock order is output gate, then reset-token/root/operation
  rows, then domain rows; all callers use the same order. Hold exclusive gate across
  filesystem steps and commit. One DB transaction updates the output setting,
  invalidates all queue/tasks
  and old-output refs, and marks token `committed`. After commit, the token is
  finalized. On ordinary DB failure, remove only token-owned directories that are
  still empty, then mark/clear the token; never remove pre-existing directories or
  files. On crash, startup recovery examines token and DB commit marker: for an
  uncommitted token, remove only its recorded empty dirs and retain old setting and
  refs; for committed token, finalize without deleting paths. If ownership/emptiness
  cannot be proven, retain the directory and report recovery failure for operator
  action. No hidden timer or automatic retry is introduced.
- I08 cleanup is an explicit operation with its own operation ID and per-artifact
  result; select only DB-registered `cleanup_eligible` artifacts. For each item,
  transactionally claim it with a cleanup delivery fence only if no live work
  owner exists, unlink the exact registered path outside the DB transaction using
  containment/symlink-safe operations, then transactionally remove the row on
  success/missing or retain ownership and safe error on I/O failure. A crash after
  unlink is reconciled as registered-but-missing on the next explicit cleanup;
  never discover candidates by directory scan, delete foreign paths, or alter
  analysis success. No timer/TTL/background cleanup.
- I10 generated code comes only from `task generate`; I11 may not invent response
  fields outside API contract.

## Acceptance checklist for the implementation plan

- [x] Owner resolved fingerprint cache semantics: single latest success per SHA,
      tool version is provenance only; version-keyed schema requires refactor.
- [x] D02 scenarios, reset coordination/journal, crash/recovery and cleanup
      proposals are documented in the contract. These are proposals, not proof of
      owner acceptance or implementation.
- [x] Independent review performed 2026-10-08
      ([report](../../reports/plan09-independent-review-2026-10-08.md)); this is not
      final owner approval.
- [ ] Technical proposal and implementation plan receive explicit owner approval.
- [ ] Owner explicitly approves implementation scope; only then move this plan to
      `todo/` and authorize implementation.
- [ ] Per commit: `task verify` and all applicable CI, with runtime/CI limits
      reported accurately.
- [ ] Final acceptance confirms no source mutation, no non-owned/live cleanup,
      no accidental full snapshots, no new environment variables, and no
      publication/matching scope creep.
