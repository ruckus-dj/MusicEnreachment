# План 09 — предлагаемая декомпозиция реализации staged source analysis

**Дата:** 2026-10-08. **Статус: NOT READY — предложение для review и явного
одобрения владельца.** Это не разрешение начинать implementation. Требования
зафиксированы в [решениях владельца](../todo/09-staged-source-analysis-owner-decisions.md);
технические варианты и open issues — в [контракте D03–D05](../todo/09-staged-source-analysis-contract.md).
Фактическая карта текущего исполнения: [D01](../../reports/plan09-execution-map-2026-10-08.md).
Публикация вне области плана. План не создаёт продуктовые defaults сверх уже
одобренного processing concurrency default 4 и не выбирает unresolved cache
semantics.

## Перед началом

Implementation может перейти в ready только после:

- явного owner decision о fingerprint results для одного SHA при нескольких
  `fpcalc` versions (version-keyed cache versus single current result);
- технического review recovery/compensation порядка DB setting ↔ output directory
  creation; продуктовый scope output-reset уже одобрен владельцем как **все**
  queue/tasks и не переоткрывается;
- согласования технических directory/table/API имен и migration/down policy;
- описанных D02 user scenarios (new/existing root, недоступный/невалидный path,
  ENOSPC, изменение source при copy, исчезновение диска); сценарий «disabled
  root» исключён — владелец допускает только add/remove;
- независимого архитектурного review контракта и этого плана: ревью выполнено
  2026-10-08 ([отчёт](../../reports/plan09-independent-review-2026-10-08.md)),
  итоговое одобрение ожидается;
- owner approval, что согласованные предложения разрешено реализовывать.

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
| I01 — контракт/миграционный skeleton | После согласованного физического дизайна: migration для per-root mode с legacy backfill `in_place`; typed concurrency setting default 4; output artifact schema без worker side effects. `backend/internal/migrations/`, `backend/internal/persistence/source_repository.go`, `backend/internal/settings/`. | Owner/design decisions выше; до commit зафиксировать down behavior. Существующий scan/analysis продолжает работать, schema обратимо читается. | Constraints, clean setup и upgrade fixture; rollback не теряет/не orphan-ит artifact data; `task verify`. |
| I02 — root/settings service model | `backend/internal/service/source_roots.go`, `backend/internal/service/settings.go` (точные имена перепроверить), persistence repositories; требовать явный mode create, future-only update, concurrency setting. | I01. Без UI/API/generated client изменений; старое поведение явно отображается `in_place`. | Repo/service validation и concurrent root mutation acceptance через полный gate. |
| I03 — scan enumeration-only | `backend/internal/service/source_scan.go`, `backend/internal/jobs/scan_worker.go`, `backend/internal/persistence/source_candidates.go`, `source_repository.go`: finish scan traversal/stat only, inaccessible region reconciliation per approved rules, одна работа на новый/изменённый кандидат. Удалить analysis/probe/hash из candidate path без удаления общей preparer logic. | I02; сначала согласовать новые generation/reconciliation semantics и тесты. Safe intermediate: scan inventory корректен, а прежний pipeline сохраняется до атомарного переключения на enumeration-only + per-file admission; промежутка с отключённой обработкой нет. | Source unchanged; no scan tool invocation/copy/hash; unchanged size/mtime не enqueue; unreadable subtrees reconciled; failure handling; `task verify`. |
| I04 — per-file durable admission / output gate | `backend/internal/service/source_analysis_start.go`, `source_analysis_pending.go`, `backend/internal/persistence/source_analysis_admission.go`, `source_analysis_enqueue.go`, `source_analysis_snapshot.go`, coordination helpers. Минимальный operation/job IDs+intent; текущий root mode и settings читаются при admission/execution (admitted mode не персистится); операция+River `InsertTx` atomic; fences и lock ordering. | I01–I03; не включать cache variant semantics unresolved. Safe intermediate: operations admitted atomically, retry does not copy serialized full settings. | Commit/rollback, duplicate suppression, source/root/settings mutation races, stale delivery; `task verify`. |
| I05 — staged artifact ownership & serial copy | Новый focused persistence artifact repository и service preparer рядом с `backend/internal/service/source_analysis_preparer.go`; `backend/internal/jobs/source_analysis_worker.go`. Exclusive owned path under proposed output layout; source pre/post stat, one sequential copy, current delivery fence. In-place path unchanged. | I04 plus resolved output layout/path validation; cache question may remain only if interface independent. Safe intermediate: staged prep can create/verify/remove only its owned test artifacts; no result publication yet. | Partial copy, mismatch, ENOSPC, symlink/path containment, collisions, no overwrite, fencing foreign/live owners; `task verify`. |
| I06 — shared prepared input and independent steps | `backend/internal/jobs/source_analysis_worker.go` (`runWorkGroup`, `prepareWorkSteps`), `backend/internal/persistence/source_analysis_steps.go` (`Claim...`, `Apply...`), service preparer. Pass one prepared input through SHA/probe/fingerprint; each step commits independently. | I05 and explicit approved SHA/cache semantics before touching cache persistence. Safe intermediate: existing in-place tests remain unchanged; staged tool receives copy, not source. | Probe once, one sequential source read/copy, SHA toggles/no-backfill, independent failures/success, changed source stale fence, tool provenance; `task verify`. |
| I07 — retained copy retry/restart/recovery | `backend/internal/jobs/reconcile.go`, `backend/internal/persistence/source_analysis_recovery.go`, new artifact recovery methods, operations retry path. Reuse valid retained copy while requested set incomplete; invalidate missing/stale refs and create anew; never clear live/foreign artifact. | I05–I06; lifecycle/crash table approved. Safe intermediate: failed operation is retryable and successes remain; no unowned cleanup. | Recovery at every table boundary; old attempt cannot delete/rewrite new artifact; live River job untouched; `task verify`. |
| I08 — explicit bulk cleanup | API/service/persistence cleanup coordinator; selection only eligible artifacts; result per artifact, cleanup errors retain row and do not fail analysis. | I07; API behavior reviewed. Safe intermediate: no automatic unlink and no artifact is deleted by discovery. | eligible/ineligible selection, live/foreign fence, missing file, permission failure, idempotence; `task verify`. |
| I09 — output setting/reset and logical areas | Existing Setup/settings validation (`backend/internal/api/setup.go`, settings API/registry, `backend/internal/app/` composition), proposed output coordinator, `deploy/compose/docker-compose.yml`. Mandatory output path, area creation, validate-first reset, global active-task gate, queue/ref invalidation atomic in DB; filesystem compensation after design. Preserve local entities/source/inventory/analysis; leave old files untouched. | I01–I08 as dictated by references; explicit resolution of open reset/recovery issues. Safe intermediate: reset unavailable until transaction/compensation complete. | Active-task race, failed validation no mutation, DB rollback preserves setting/refs, crash recovery, output refs invalidated, old physical files untouched; `task verify`; CI deployment smoke. |
| I10 — API/OpenAPI/generated client | Huma DTO/routes under `backend/internal/api/sources.go`, source analysis/settings APIs; run repository `task generate`; update React call sites only from generated contract. Endpoints/DTO resolved per contract. | I02–I09 API shapes stable; no direct generated-file edits. Safe intermediate: backend contract + generated artifacts synchronized. | `task generate` included in `task verify`; generated drift check; API error and transaction cases; GitHub CI. |
| I11 — Sources/Settings UI | `frontend/src/features/sources/` (root create/edit, scan list, inspector, cleanup) and `frontend/src/features/settings/SettingsScreen.tsx`; reuse existing operation REST reread/SSE wake-up. Accessible loading/error/dirty/retry/reset flow. | I10. Safe intermediate: no fake mode default, no unsupported actions; current features remain usable. | RTL/accessibility coverage in full gate; review light/dark desktop + 375px action retention; browser CI/manual evidence as explicitly reported. |
| I12 — integration acceptance | Integration fixtures for source tree, DB/River, real managed tools; deployment fixture; docs/status update and independent reviewer. No application behavior additions beyond approved contract. | I01–I11 complete. | `task verify`; GitHub native platform matrix, PostgreSQL/River integration and Compose deployment smoke; real runtime staged vs in-place; source bytes unchanged; artifact ownership/recovery evidence; independent acceptance report. |

## Cross-commit dependency notes

- I03 may develop an enumeration-only traversal only after candidate reconciliation
  behavior is specified; do not leave interim code that reports scan success while
  silently omitting required work. The previous pipeline must stay live until the
  enumeration-only + per-file admission switch is atomic — never ship a stage where
  processing is disabled.
- I04/I05 need a minimal current-input contract: preserve only IDs/explicit intent
  and delivery fence. No serialized full runtime settings, paths, or tool config in
  `input_snapshot` or River args. Do not persist an admitted mode: queued and
  retried work reads the current per-root mode from the DB at execution.
- I06–I07 must use existing shared step engine and preserve successes independently;
  do not build a second analysis pipeline.
- I08 is explicit operator action; no timer/automatic cleanup. A cleanup failure
  does not change the analysis operation's success.
- I09 cannot ship until atomic DB invalidation is guarded against concurrent
  admission and external filesystem mkdir crash behavior is designed. Never delete
  old output files.
- I10 generated code comes only from `task generate`; I11 may not invent response
  fields outside API contract.

## Acceptance checklist for the implementation plan

- [ ] Owner resolves fingerprint cache/version semantics.
- [ ] Technical review resolves output-reset invalidation/filesystem compensation;
      owner scope «все queue/tasks» уже одобрен и не переоткрывается.
- [ ] D02 remaining user scenarios are documented and owner-accepted.
- [ ] Independent review performed 2026-10-08
      ([report](../../reports/plan09-independent-review-2026-10-08.md)); approval
      still pending — the plan remains NOT READY.
- [ ] Owner explicitly approves implementation scope; move this plan to `todo/`
      only after the repository's readiness criterion is met.
- [ ] Per commit: `task verify` and all applicable CI, with runtime/CI limits
      reported accurately.
- [ ] Final acceptance confirms no source mutation, no non-owned/live cleanup,
      no accidental full snapshots, no new environment variables, and no
      publication/matching scope creep.
