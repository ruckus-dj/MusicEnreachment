# План 10 — план реализации staged source analysis

**Дата:** 2026-10-08. **Статус: одобрен владельцем 2026-10-08; реализация
разрешена.** Требования зафиксированы в
[решениях владельца](#appendix-a-owner-decisions);
технические варианты и детали — в [контракте D03–D05](#appendix-b-contract-d03-d05).
Фактическая карта текущего исполнения: [D01](../../reports/plan09-execution-map-2026-10-08.md).
Публикация вне области плана. План не создаёт продуктовые defaults сверх уже
одобренного processing concurrency default 4. Cache contract уже решён владельцем:
один последний успешный fingerprint на SHA, версия инструмента — provenance, не
часть cache identity. Технические решения в контракте одобрены; новые
непредусмотренные продуктовые решения требуют отдельного согласования.

## Перед началом

Условия начала выполнения:

- явное owner approval контракта и последовательного implementation plan получено
  2026-10-08 («В остальном ок, давай пробовать») с уточнением UI смены output;
- разрешения оставшихся технических вопросов в ходе review до соответствующих
  зависимых commits: имена/layout/API и физическая migration strategy. Upgrade
  compatibility для опубликованных данных не требуется: это unpublished
  prototype, squash допустим, но не обязателен;
- независимое архитектурное review выполнено 2026-10-08
  ([отчёт](../../reports/plan09-independent-review-2026-10-08.md)); этот review
  не заменяло owner approval.

D02 сценарии, cache policy, reset scope и crash/recovery предложения внесены в
одобренный контракт. Продуктовых блокеров в записанных решениях сейчас не осталось;
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

## Progress

- **I01 — complete:** added an additive migration with an
  `in_place` transitional DB default and a check-constrained per-root mode, a
  typed positive integer `source_file_concurrency` runtime setting (default 4),
  and an owned-artifact registry skeleton. The artifact row records work identity,
  source size/mtime, a relative managed-output path, the creating delivery fence,
  and acquisition/readiness/cleanup states. Relative paths avoid persisting an
  output-root snapshot; linking the work and operation fence prevents orphan
  ownership references. No filesystem behavior or worker integration is included
  here; those belong to I05. The mode DB default is transitional only, so I02 can
  make service/API creation require an explicit mode without disrupting existing
  callers in this commit. `task verify` passed before commit; migration/application
  behavior beyond schema constraints remains for later stages.

- **I02 — complete (commit `9d1471e`, 2026-10-08):** source
  root creation requires an explicit valid processing mode; mode edits apply only
  to future work. Repository reads now include the mode on both single-root and
  list paths, and a name/path edit that omits mode preserves a concurrent mode
  change. The existing row-lock serialization and conditional mode update are
  covered by regression and PostgreSQL integration tests. `task verify` passed:
  generated-contract check, Go/frontend lint, tools tests, Go integration tests,
  frontend tests, and frontend/backend builds all completed successfully. The
  frontend build emitted its large-chunk-size warning. Native platform matrix,
  deployment smoke, and other CI-only checks were not run locally.

- **I03 — implementation complete locally (2026-10-08):** added an
  observation-capable stat-only traversal, with root/subtree/file unreadable scopes,
  cancellation and visitor-error aborts, and pinned-root namespace confirmation.
  Transactional apply filters candidates from unreadable scopes before reconciling
  all unseen locations; a root-level unreadable observation marks the root
  unavailable and removes all root locations, while preserving
  `last_successful_scan_at`. Candidate clearing is fenced atomically by operation, delivery, root
  and configured path. Unchanged work is preserved; changed work retirement uses
  the existing active-hold/artifact restrictions and does not delete SHA cache
  rows, enqueue River jobs, or admit work. A repeated successful apply is
  idempotent. Integration coverage checks unavailable-root state, retained
  successful-scan time, root-scope pruning, subtree filtering/pruning, mismatched-root candidate
  fencing, idempotence and owned-artifact retirement restrictions. The existing
  `Run` and scan-worker wiring remain unchanged; this is preparation only, not the
  I04 cutover. `task verify` passed locally. Native platform matrix and deployment
  smoke remain CI-only and are not claimed locally.

- **I04 — implementation complete locally (2026-10-09):** production scan now
  enumerates/stat-checks only, reconciles inaccessible locations, and admits
  singleton file operations. Operation/step snapshots contain identifiers and
  explicit step intent rather than tool/settings snapshots. Fenced execution
  resolves current mode/tools and replaces admission tool holds; admissions and
  claims take the shared output gate first. PostgreSQL guards enforce per-work
  exclusivity and exact step membership. Independent step successes remain
  available when a file operation fails. Scan recovery preserves the durable
  unreadable-observation outcome. `task verify` passed, including PostgreSQL/River
  integration and builds. The analysis queue starts with the typed concurrency
  setting; live increases beyond its startup capacity require the later settings
  integration before UI exposure. Production staged execution remains fail-closed
  until I06; output reset/journal remains unavailable until I09. Native platform
  matrix and deployment smoke were not run locally.

- **I05 — acquisition capability complete locally (2026-10-09):** added the
  focused artifact repository and safe writable output capability. Acquisition
  creates an exclusively owned staged file, performs one size-bounded sequential
  copy, verifies source/output namespace and file identity, and marks readiness
  under the delivery fence. Partial failures retain registered artifacts;
  collisions preserve foreign files. Tests cover cancellation, sync/write and
  readiness failures, zero-length inputs, growth and namespace replacement.
  `task verify` passed. This capability remains unwired until I06; retained reuse,
  cleanup and reset are not implemented by this stage. Native platform CI remains
  outstanding.

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
      are documented in the owner-approved contract; this is not proof of
      implementation.
- [x] Independent review performed 2026-10-08
      ([report](../../reports/plan09-independent-review-2026-10-08.md)); independent
      review did not substitute for owner approval.
- [x] Contract and implementation plan explicitly approved by owner 2026-10-08
      («В остальном ок, давай пробовать»), with output-change UI clarification.
- [x] Implementation scope approved; plan moved to `todo/` and implementation
      authorized.
- [ ] Per commit: `task verify` and all applicable CI, with runtime/CI limits
      reported accurately.
- [ ] Final acceptance confirms no source mutation, no non-owned/live cleanup,
      no accidental full snapshots, no new environment variables, and no
      publication/matching scope creep.

---

# Приложение A. План 09 — решения владельца по staged-анализу источников (2026-10-08)

<a id="appendix-a-owner-decisions"></a>


**Дата фиксации: 2026-10-08. Статус: решения владельца и реализация staged
source analysis одобрены; выполнение реализации начато с I01.** Этот документ фиксирует явные
продуктовые решения для дальнейшей проработки. Он не является доказательством
выполнения этапов D01–D06 и не утверждает, что код, API или схема БД уже
соответствуют решениям. Контракт D03–D05 и implementation plan одобрены
владельцем 2026-10-08 («В остальном ок, давай пробовать») с уточнением UI смены
output; реализация начата, но staged functionality пока не поставлена.

## Контекст и границы

Проект — unpublished prototype. Совместимость со старыми данными не требуется;
squash миграций допустим, но не обязателен. Это снимает требование поддерживать
миграционный путь для уже опубликованных инсталляций, но не предписывает squash.

Решения ниже относятся к staged source analysis и управлению источниками.
Публикация остаётся отдельной областью реализации и не включается в план 09.
Временная staging-подготовка публикации описана только как утверждённый контракт
границы, а не как поставленная функциональность.

## Утверждённые решения

### Режим источника, интерфейс и параллельность

- Режим обработки выбирается для каждого source root. При добавлении root
  оператор обязан явно выбрать режим; значения по умолчанию нет. UI объясняет
  различия режимов. Настройка режима находится в форме Sources, там же показан
  список корней.
- Смена режима влияет только на будущую работу и не переписывает уже созданные
  операции или результаты.
- Число одновременно обрабатываемых файлов настраивается отдельно от RPS
  провайдеров; default — 4. Это ограничение concurrency обработки файлов, не
  настройка скорости запросов к провайдеру. AcoustID RPS=3 не утверждён: к нему
  можно вернуться только после отдельного исследования официальных требований.
- Отдельной настройки staged mode, каталога или container overlay не вводить.

### Durable jobs и анализ

- Аргументы job минимальны: тип операции, необходимые идентификаторы и явный
  intent. Остальные входы перечитываются из актуальной БД при выполнении и retry;
  исторический snapshot полной входной конфигурации не нужен.
- История анализов не ведётся. Сохраняется текущее состояние публикации, а не
  снимок её прошлого. Независимые анализы считаются актуальными по SHA; это
  кэш/текущее состояние, не журнал изменений.
- Обработка файла выполняется отдельно для каждого кандидата. Scan только
  перечисляет файловую систему и получает stat кандидатов, после чего ставит
  отдельные per-file processing jobs. В scan не копировать, не probe-ить и не
  хешировать файлы.
- Если size и mtime не изменились, автоматической повторной постановки нет;
  ошибочную обработку оператор запускает вручную. Дубликаты одной работы не
  создаются.
- Пропавшие или нечитаемые файлы теряют свои locations. Недоступность корня,
  коллекции или поддерева удаляет locations соответствующей области. Root,
  коллекции и SHA-анализы сохраняются; при повторном обнаружении допустимо
  переиспользовать кэш по SHA.

### Обязательный output path и каталог служебных данных

- Output path обязателен уже при Initial setup. После сохранения его нельзя
  очистить, но можно изменить.
- Приложение автоматически создаёт структуру служебных областей `analysis`,
  `publication`, `checks` и `media` в выбранном output. Это названия для
  продуктового разделения; точные физические имена и layout пока являются
  техническим предложением и требуют проработки.
- **Уточнение владельца от 2026-10-08 (UI смены output):** отдельного действия
  «Проверить новый путь» нет. Единственный элемент управления — кнопка «Сменить
  output»; по её вызову сначала валидируется новый путь, и при неуспешной
  валидации изменение отклоняется без мутации. Разрушительное подтверждение
  (alarm) сохраняется, но отдельной кнопки проверки не вводить.
- При смене output сначала валидируется новый путь. Изменение запрещено при
  любых активных задачах. После успешной проверки и в отсутствие активных задач
  очищаются очередь/задачи и все ссылки БД на старый output: публикации и
  staging. Старые физические файлы остаются на месте, становятся unmanaged и
  приложением не удаляются.
- При смене output сохраняются локальные artists/releases/tracks/metadata,
  source links, inventory и analyses. Автоматической повторной публикации нет.
  Source/publication entity не должен оставаться валидным, если теряет
  необходимую связь при этой очистке.

### Staged-копии анализа и очистка

- Временная копия AUDIO сохраняется до успешного завершения всех запрошенных
  шагов обработки файла. В БД отслеживаются её identity/path. Retry и restart
  используют пригодную существующую копию; если копия отсутствует, ссылка
  удаляется, и копия создаётся заново при необходимости.
- Свежесть исходника определяется size/mtime. Успешные соседние шаги сохраняются
  независимо от ошибки другого шага.
- Ошибка удаления временной копии не превращает успешный анализ в ошибку.
  Очистка ожидает отдельного действия «Очистить»; допускается много ожидающих
  файлов. Удалять разрешено только подходящие копии, которые не используются
  живой работой и не относятся к незавершённой обработке.
- Не вводить квоты, произвольный предел размера или fallback на in-place.
  ENOSPC на подготовке — ошибка подготовки; восстановление выполняется ручным
  retry после устранения причины.

### Корни, сканирование и удаление

- Для root нет продуктового действия «выключить»: доступны только добавление и
  удаление. Добавление запускает ручной scan. Автоматическое или периодическое
  сканирование — будущая область, не решение этого плана.
- Удаление root запрещено при активных операциях этого root. Оно удаляет
  ожидающие задачи и locations этого root, но не физические source bytes;
  анализы и коллекции сохраняются.
- Scan перечисляет файлы, получает stat, формирует отдельные задания. Он не
  выполняет file copy, probe или hashing. Неизменившиеся по size/mtime файлы
  повторно автоматически не ставятся; повтор ошибочной обработки — ручной.
- Исчезновение/нечитаемость файла удаляет его location; недоступность root,
  коллекции или поддерева удаляет locations затронутой области. Root и
  коллекции, а также SHA-анализы не удаляются. Повторное обнаружение может
  присоединиться к сохранённому SHA-кэшу.

### Граница с публикацией

- Для публикации утверждена отдельная подготовка: extraction audio,
  optional container и новые tags, затем атомарная замена файла.
- Реализация публикации находится вне плана 09. Её текущий статус не следует
  выводить из данного решения или считать выполненным.

## Статус дальнейшей проработки

Решения собраны, но D01–D06 плана 09 не объявлены выполненными: для этого
необходимы отдельные результаты и свидетельства. Следующие шаги — сопоставить
решения с текущим кодом и физической моделью, описать UI/lifecycle/API/deployment,
уточнить предложенные технические имена/layout и подготовить отдельную
декомпозицию реализации. До этого приложение не реализовано и код этим документом
не меняется.

**Уточнение владельца от 2026-10-08:** fingerprint cache хранит только один
последний успешно вычисленный результат на уникальный SHA-256, без сохранённой
истории, индексированной по версиям `fpcalc`. При необходимости результат
пересчитывается текущим выбранным `fpcalc`; старый успешный результат остаётся
доступен во время выполнения и при ошибке повторного вычисления и заменяется
только после успешного вычисления нового результата. Существующая физическая
version-keyed схема — текущая реализация, требующая refactor, а не поставленный
контракт.

---

# Приложение B. План 09 — контракт staged source analysis (D03–D05)

<a id="appendix-b-contract-d03-d05"></a>


**Дата:** 2026-10-08. **Статус: контракт одобрен владельцем 2026-10-08**
(«В остальном ок, давай пробовать») с уточнением UI смены output (одна кнопка
«Сменить output» без отдельного действия проверки пути). Независимое ревью
проведено 2026-10-08
([отчёт](../../reports/plan09-independent-review-2026-10-08.md)). Требования
владельца зафиксированы в
[решениях](#appendix-a-owner-decisions). Фактическое текущее
исполнение описано в [D01](../../reports/plan09-execution-map-2026-10-08.md).
Сохранённые ниже имена полей, endpoints, SQL layout и физические пути —
согласованные к реализации технические предложения, не новые продуктовые
решения. При расхождении с решениями владельца приоритет имеют решения владельца.

## 1. Предмет и известные границы

- Источник остаётся read-only. Scan только перечисляет root, получает stat
  кандидатов и планирует отдельные per-file processing jobs; scan не копирует,
  не probe-ит и не хеширует файлы.
- Режим (`in_place`/`staged`) выбирается явно для каждого нового root; default
  нет. Смена режима влияет только на будущую работу. Processing concurrency —
  отдельное runtime setting, начальное значение **4** по явному решению владельца;
  это лимит одновременно обрабатываемых файлов, не provider RPS.
- Durable job arguments минимальны: operation ID, необходимые root/location/work
  IDs и явный intent. Остальные рабочие settings/config читаются из текущей БД
  при старте/retry/execution. Полный `input_snapshot` конфигурации не
  сохраняется и не копируется в operation/job. Сохранять только минимальную
  delivery identity/fence, необходимую для надёжности и валидации актуальности.
- Изменившийся size/mtime делает работу устаревшей. Неизменённые файлы
  автоматически повторно не ставятся; повтор ошибки — вручную; дубликаты одной
  работы не создаются. Смена режима влияет только на будущую работу и не
  переписывает уже созданные результаты и завершённые операции: ещё не
  выполненная queued/retry работа читает текущий mode при execution, а не
  сохранённый при admission.
- Audio copy нужна лишь staged mode. Существующая пригодная copy сохраняется для
  retry/restart, пока все запрошенные шаги этой работы не завершились успешно.
  После успеха она становится eligible для явной bulk cleanup. Ошибка cleanup не
  превращает analysis success в failure. Никаких квот, произвольных размеров и
  fallback в in-place.
- Output обязателен в Initial Setup и остаётся non-empty setting: его можно
  изменить, нельзя очистить. Перед reset валидируется новый путь; reset запрещён
  при running/claimed исполнении (включая гонку claim), тогда как поставленная в
  очередь работа не блокирует reset; при разрешённом reset queue/tasks и DB
  references старого output атомарно инвалидируются, но старые файлы не удаляются
  и остаются unmanaged. Сохраняются локальные entities, source links, inventory и analyses;
  автоматического republish нет.
- Области output автоматически создаются; одобренные логические названия:
  `analysis`, `publication`, `checks`, `media`. Точные физические имена/layout —
  техническое предложение. Этот документ описывает staging-подготовку анализа;
  атомарная публикация остаётся вне плана 09.

## 2. D03 — UI-flow (предложение)

### Sources: root list и создание

В existing Sources screen списка корней добавить действия «Добавить источник» и
явный выбор режима в форме создания: `In-place` или `Staged`. Ни один radio не
выбран заранее; submit disabled до выбора. Краткое объяснение различает прямое
чтение источника и подготовку audio copy в writable output. Показывается именно
server/container path, существующий source path validator проверяет root.
Редактирование существующего root позволяет изменить mode для будущих jobs,
не вызывает reanalysis и не меняет active operations; до submit виден текст о
границе влияния. Добавление запускает ручной scan.

### Output и concurrency settings

Settings показывает обязательный output path (очистка запрещена), действие
одна кнопка «Сменить output»: при её вызове новый путь сначала валидируется
внутренне, а отдельного действия «Проверить новый путь» нет. При неуспешной
валидации изменение отклоняется без мутации; подтверждение destructive reset
(alarm) сохраняется. Новое значение не применяется до успешной проверки и атомарного reset; при running/claimed
исполнении кнопка disabled с перечнем причины/активности и повторным readback,
а поставленная в очередь работа не блокирует reset и очищается в транзакции. Draft
формы не подменяет сохранённое значение. Изменение concurrency показывается отдельно,
не связывается с RPS и остаётся DB-backed setting; значение 4 — одобренная
начальная настройка. Интерфейс не представляет число как пользовательскую квоту.

### Per-file operation и inspector

На operation list отображать scan и per-file processing как отдельные jobs.
Operation REST snapshot остаётся источником истины; SSE остаётся только wake-up,
после которого UI повторно читает snapshot. Inspector отображает статус файла,
каждого запрошенного шага, safe error, retry, подготовку/повторное использование
copy, cleanup pending/error, но не обещает progress percentage без backend total.
Retry конкретного шага доступен только для failed шага. Успешный соседний шаг и
его provenance остаются видимыми при ошибке/повторе другого шага.

### Явная очистка

Bulk action «Очистить завершённые staged-копии» показывает число eligible items,
не предлагает выбор недоступных/live artifacts и не подразумевает немедленного
удаления. Ошибки отдельных удалений остаются cleanup failures, копия остаётся
учтённой для следующей попытки. Подтверждение и список результатов доступны
оператору; неуспех cleanup не переписывает успешный analysis.

### Состояния и доступность

Для screens/controls предусмотреть loading, empty, API error, stale output/root,
dirty draft, validating, rejected, queued/running, step failure, partial success,
cleanup eligible/in progress/failure и completed. Ошибка привязана к полю или
операции, доступна текстом, не только цветом. После completion/reconnect перечитать
REST snapshot и сохранить несохранённый draft. Tab/Shift-Tab, visible focus,
keyboard activation, focus restoration после закрытия диалога и screen-reader
labels обязательны для proposal review. Проверить desktop screenshots light/dark;
375px — только проверка сохранности действий, не mobile-first требование.

## 3. D04 — lifecycle prepared audio input

### Предложенный lifecycle для каждой работы

1. **Scan/enumeration.** Scan обходит root, не следует symlinks и перечисляет
   files/stat only. В транзакции фиксирует завершённое наблюдение inventory,
   синхронизирует locations и создаёт не более одной ожидающей работы для нового,
   изменённого или явно ручного retry файла. Недоступная область удаляет только
   locations этой области; нечитаемая location не сохраняется как будто доступна.
   Root/collection/SHA cache остаются. Обход не создаёт audio copy и не запускает
   анализ.
2. **Admission.** Per-file operation получает root/location/work IDs и intent;
   enqueue вместе с DB operation/step fences атомарен через `River.InsertTx`.
   Валидация читает текущий root mode, output path, analysis settings и active
   tools, а не восстанавливает историческую полную конфигурацию. Для staged
   работы перед side effect проверяются актуальность location и доступность
   output. Tools/source/output read/write holds согласуются с root deletion,
   mode/output changes и tools-root operations.
3. **Owned-copy acquisition (staged only).** Worker открывает конкретный source
   через текущий безопасный opener, получает до копии size/mtime, создаёт
   эксклюзивный owned artifact в staging area, последовательно копирует один раз
   в файл и после чтения валидирует size/mtime против наблюдения. При mismatch
   copy/result отбрасываются, analysis не публикуется. In-place путь использует
   открытый source input без copy; режим выбирается per root.
4. **Step execution.** Один пригодный prepared input переиспользуется SHA,
   `ffprobe`, `fpcalc`; source bytes повторно не читаются для запрошенных steps.
   Steps независимы: запись success одного шага не откатывается из-за ошибки
   другого; ручной retry failed step использует retained copy. Retry/restart
   проверяет artifact identity/availability и source size/mtime; если пригодной
   copy нет — снимает stale DB reference и создаёт новую при необходимости.
   Никакого fallback in-place при copy error/ENOSPC.
5. **Apply results.** Перед DB publication worker повторно валидирует source
   identity/size/mtime и fenced delivery ownership. DB transaction применяет
   результат только при совпадающей live fence и актуальном work. Step success
   становится visible в установленной per-step модели. Copy остаётся retained,
   пока каждый запрошенный step работы не имеет requested success.
6. **Terminal/cleanup.** После всех requested steps success запись copy
   переходит в eligible-for-cleanup. Сама файловая очистка запускается только
   явным bulk action. Копия, используемая живой работой или незавершённой
   обработкой, не eligible. Ошибка удаления сохраняет ownership и не меняет
   analysis success. Возможна множественность eligible копий без quota/TTL.

### SHA/cache — решение владельца от 2026-10-08

SHA — независимый запрашиваемый step; hash backfill для неизменённых файлов не
создаётся. На один уникальный SHA существует ровно один последний успешный
fingerprint. Версия `fpcalc` сохраняется как provenance, но не является частью
cache identity и не образует retained history. Если текущий выбранный `fpcalc`
требует пересчёта, прежний результат остаётся текущим/доступным, пока повтор
работает или завершается ошибкой; только успешный новый результат атомарно
заменяет его. Никакой промежуточный failed/running status не затирает последнюю
successful result row. При первом вычислении успешного результата ещё нет. Этот
контракт supersede-ит version-keyed reuse в текущем коде/физической схеме: schema
refactor необходим, и историческая схема не считается уже доставленной. Прежние
успешные fingerprint и успешные sibling steps сохраняются при ошибке probe или
fingerprint rerun; ошибки шагов остаются независимыми. Текущий выбранный инструмент
проверяется при execution/retry, а не фиксируется старой cache identity.

### Artifact ownership, fencing и crash matrix

Предлагаемая запись artifact содержит случайный `artifact_id`, work/location ID,
канонический относительно managed output path, ожидаемые size/mtime/length,
создавшую operation ID + attempt/job ID, lifecycle state, и timestamps. Имена —
proposal. Реестр в БД доказывает ownership; случайное имя само по себе не
доказывает. Создание файла — эксклюзивное, без overwrite. Старый delivery может
удалять/менять только свой artifact при сравнении operation/attempt/job fence;
если есть live fence или неизвестный/foreign owner — отказ без удаления. Нельзя
удалять путь, найденный только сканированием каталога, либо «почти совпавшее»
имя. Проверки path containment и symlink-safe операции остаются обязательны.

| Crash/failure boundary | Durable evidence / restart response | Разрешённое удаление |
| --- | --- | --- |
| До artifact row/file | Operation/step fence; нет artifact | Ничего |
| Artifact row создан, file ещё нет | Owned row в acquiring | Только fenced owner/recovery, если delivery доказанно не live |
| File создан, copy частичная / ENOSPC | Owned row + exact path + delivery fence; пометить incomplete/retryable | Только если fence больше не live и ownership row совпадает; не удалять foreign file |
| Copy готова, DB ready state ещё не записан | Row acquiring + fenced delivery; recovery сверяет файл/identity | Не удалять пока delivery live; orphan удалять только после fenced recovery |
| Copy ready, до/во время tool execution | Row retained + running step delivery; recovery делает retryable, copy сохраняется | Не удалять live/unfinished copy |
| Один tool success, sibling error/crash | Успешный step опубликован отдельно; requested set остаётся незавершённым | Не удалять; retry использует copy если пригодна |
| DB step success committed, process ещё live | DB state/fence authoritative; state transition идемпотентна/fenced | Не удалять до terminal success и explicit cleanup |
| Все requested steps success, до cleanup action | Artifact eligible, DB results success | Только explicit bulk cleanup, после повторной проверки отсутствия live owner |
| Bulk cleanup начат, unlink завершён, DB row ещё есть | Owned row + file absent; retry cleanup снимает row | Повторно удалять только тот же fenced owned artifact; отсутствующий путь — reconcile |
| Unlink error / permission/I/O failure | Keep row, record safe cleanup failure, success unchanged | Не менять ownership и не снимать row |
| Root removed/output reset/config changed during work | Active-task gate/coordination должна запретить конфликт; foreign/stale delivery fails fence | Старые/чужие paths не трогать; reset не удаляет physical files |
| Process restart, delivery live in River | Reconciler не считает job orphan | Ничего; worker продолжает/retries |
| Process restart, delivery orphan | Reconciler fenced-terminalizes work; copy пригодность проверяется при ручном retry | Cleanup только если fence invalidated и artifact ownership подтверждён |

В этой фазе **не обещать same-filesystem atomic rename/publication** и не считать
rename доказательством crash safety. Final promotion/atomic replace не требуется
для analysis input; при необходимости отдельный post-review контракт. Recovery
не удаляет live или foreign artifact.

### Concurrency/resource policy

Runtime setting ограничивает одновременно обрабатываемые файлы значением default
4. Это owner-approved default, не квота на размер/суммарное место и не RPS.
Внутри одной staged file операции — один последовательный copy; несколько
independent files могут идти с заданной concurrency. На ENOSPC шаг подготовки
fail-safe; оператор устраняет причину и делает ручной retry. Не добавлять новый
таймер, auto retry, quota или fallback.

## 4. D05 — DB/API/deployment предложения

### Предлагаемая физическая модель (review, не migration)

- `source_root.processing_mode TEXT NOT NULL`, ограниченный `in_place|staged`.
  Для существующих roots потребуется backfill `in_place`, сохраняющий текущий
  путь. Создание root требует явного значения; UI не посылает default.
- Typed runtime setting `source_file_concurrency`, стартовое значение `4`.
  Обычная settings registry/table validation, без env var.
- Output path остаётся существующей обязательной настройкой. Предложение
  физического layout: `<output>/analysis/staging/<root-id>/<work-id>/<artifact-id>`;
  logical areas `analysis`, `publication`, `checks`, `media` создаются при
  проверке/создании output. Это имена предложены для обсуждения, не утверждённые
  физические имена. Не использовать второй общий work-directory setting.
- Owned artifacts — нормализованная таблица (candidate name
  `source_analysis_artifact`) со ссылками work/location, relative output path,
  expected source identity (size/mtime; SHA если уже запрошен/получен, без
  обязательного backfill), ownership delivery triple, состояния `acquiring`,
  `ready`, `cleanup_eligible`, cleanup error/time. DB reference и operation/step
  fences связаны FK; индексы нужны на active owner и eligible cleanup. Названия,
  columns и enum могут измениться на design review.
- Per-file durable work/step rows переиспользуют существующие normalized
  entities/step model там, где семантика совпадает; операции имеют только
  минимальный payload. Полная копия current settings, tool paths, selected tool
  snapshot, output config и source file bytes в `operation.input_snapshot` не
  добавляются. Для безопасности immutable identity (root/location/work ID,
  explicit retry intent) и attempt/job fence не являются copied config.
- Root mode updates блокируются/сериализуются с admission на root: выбор mode
  сериализован. Уже начатое исполнение сохраняет выбранный для него mode, а
  queued/retry работа читает текущий mode из БД при execution; admitted mode не
  персистится. Удаление root остаётся запрещённым при активных задачах и удаляет
  очередь/locations; physical source/audio bytes не удаляет.

Миграционный путь: это unpublished prototype; совместимость с историческими
опубликованными данными не требуется. Squash миграций разрешён, но не обязателен;
точную стратегию выбрать в implementation review. Не представлять legacy
backfill/upgrade path как продуктовое требование и не добавлять destructive down
без явного описания его поведения.

### Operation admission и output reset

Оставить внешний job args скудными и транзакционными: River args — operation ID,
текущая `Operations`/reconciler схема; operation rows содержат необходимый target
и explicit intent. Admission в одной DB транзакции: lock coordination gates в
детерминированном порядке → проверить root/output setting и отсутствие конфликтной
мутации → создать operation + per-file references/fences → `River.InsertTx` →
commit. Rollback оставляет ни operation, ни job, ни artifact claim. Worker
не восстанавливает исторический mode: queued и retry работа читает текущий mode
root из актуальной БД при выполнении и retry (решение владельца: «остальные
входы перечитываются из актуальной БД при выполнении и retry»). Admission не
сохраняет admitted mode; уже завершённые операции и записанные результаты при
смене mode не переписываются.

Output reset требует двухфазного пользовательского flow, но не двухфазного
применения: сначала validate-only кандидата path; затем exclusive global
output/admission gate. Все operation admissions и queued-to-running worker claims
берут тот же cross-process gate shared, в едином порядке до reset-token/root/
operation/domain rows. Reset под exclusive gate проверяет отсутствие незавершённого
reset token, перечитывает setting и running/claimed исполнение; running/claimed
исполнение, включая гонку queued-to-running claim, запрещает reset, а поставленная
в очередь работа не блокирует его и инвалидируется в той же транзакции. Gate
удерживается через filesystem steps и DB commit, поэтому
между проверкой и инвалидацией не может проскочить admission/claim. При crash lock
освобождается, но durable `preparing` token заставляет admission/claim отказывать
до startup recovery. Предложенный cross-process primitive — PostgreSQL advisory
lock; точный вариант подтвердить при implementation review. В одной DB transaction
обновить setting и инвалидировать все queue/tasks и все DB refs старого output;
не ограничиваться staging/publication refs. Старые физические файлы не удалять.

Filesystem mkdir не атомарен с DB commit. Предлагаемый recoverable journal:
создать reset token в `preparing` после захвата gate; до DB transaction создавать
только отсутствующие logical directories эксклюзивно и сохранять точный список
каталогов, созданных этим token (не присваивать существующие). Одна DB transaction
меняет setting, инвалидирует все queue/tasks и старые output refs и помечает token
`committed`. После commit token finalized. При обычном rollback удалять лишь
записанные token-owned каталоги, если они всё ещё пусты, затем закрыть token.
При startup recovery `preparing` без commit marker означает rollback: сохранить
старое setting/refs и удалить только доказанно owned пустые dirs; `committed`
означает finalize без удаления paths. Если ownership или пустота не доказаны,
оставить каталог и предъявить recovery error оператору. Старые физические файлы
не трогать. При reset сохранить
local artists/releases/tracks/metadata, source links, inventory/analyses; очистить
только output-owned publication/staging references и связанные queue/task state.
Scope «очистить очередь/задачи» уже одобрен владельцем как **все** queue/tasks и
все ссылки БД на старый output (публикации и staging); этот продуктовый scope не
переоткрывается. Технически остаётся определить invalidation rows и их конфликт
с job history/reconciler.

### HTTP/API proposal

Предпочесть расширение текущих DTO, а не новые endpoints:

- `SourceRootResponse` и create/update request включают `processing_mode`; server
  валидирует только поддерживаемые enum values и не default-ит create.
- Settings response/update включает output directory и `source_file_concurrency`;
  path validation endpoint переиспользует существующий `CheckSetupPaths` pattern
  или typed settings path-check. Output reset может требовать отдельный explicit
  `reset_output` command, если текущий update endpoint не выражает подтверждение.
- Per-file operations используются существующим `GET /operations`,
  `GET /operations/{id}`, retry endpoints. Operation DTO называет targets/status,
  не копирует full config. Новые URL/error codes — открытые технические варианты,
  определить при изучении текущих endpoints и review.
- Bulk cleanup: кандидат `POST /source-analysis/artifacts/cleanup` с eligible
  count/result operation; точная форма endpoint и sync/async граница — review
  issue. Не выдавать cleanup selection за user-facing file picker.
- Ошибки валидируют readable output, path overlap/case-normalization, disk
  write/ENOSPC и stale source; возвращают безопасное actionable сообщение без
  server-local secret/path leaks сверх уже существующего UI server paths.
- Huma contract → экспорт OpenAPI → `frontend/openapi.json` → Orval generated
  client. Не менять `frontend/src/api/generated/**` вручную.

### Deployment

Применить существующий Compose output bind mount как writable managed area:

```yaml
volumes:
  - tools-data:/var/lib/melotrove/tools
  - ./music:/var/lib/melotrove/output
  - /srv/music/sources:/srv/music/sources:ro
```

Явно оставить managed output writable/persistent; source read-only; не добавлять
отдельный scratch bind mount, container overlay, каталог binary/temp или новую env
var. Host path в примере — буквальный существующий default `./music`, а не новая
env var. Standalone deployment обеспечивает, чтобы настроенный абсолютный output
path существовал/был writable внутри процесса. Compose/readme описывают host path
только как mount source, runtime использует container path.

## 5. Оставшиеся технические review details

Точные physical directory/table/column/state/API names, migration strategy
(squash разрешён, не обязателен), cleanup operation response shape и реализация
reset journal выбираются при technical review до соответствующих implementation
commits. Это не новые продуктовые блокеры. Reset scope **все** queue/tasks и все
старые output DB refs фиксирован; cache policy решена владельцем (один последний
успешный результат на SHA, fpcalc version — provenance). Если review обнаружит
реальное противоречие, его нужно вернуть владельцу; не объявлять proposal полностью
approved заранее.

## 6. D02 — сценарная матрица и техническая сверка

Матрица завершает сценарную проработку D02; она не является финальным owner
acceptance технического предложения. Root «disabled» отсутствует. Work-path в отдельных
сценариях означает выбранный writable output и его `analysis` area: отдельная
work-directory/path setting не вводится. Режимы, root lifecycle, cache и output
reset scope следуют решениям владельца; locks, ownership records и recovery ниже —
технические предложения, а не описание текущего кода.

| Сценарий | Ожидаемый результат | Сохраняемое состояние / действие |
| --- | --- | --- |
| Новый SSD root / in-place | Mode обязательно выбран; scan enumerate/stat-only и создаёт per-file jobs; tools читают source | Read-only source, ручной scan; нет default и scan-time analysis |
| Новый HDD/NAS root / staged | То же явное создание; один последовательный audio copy на file; запрошенные шаги используют copy | Без второго path setting, квот и in-place fallback |
| Существующий root, смена mode | Будущая работа читает текущий mode при execution/retry; уже запущенная завершает выбранный путь | Не переписывать старые операции/результаты; mode не сериализуется в job |
| Root/source недоступен при scan | Нет analysis для невидимых файлов; locations недоступной области удаляются по owner rule, root/collections/SHA analyses остаются | Показать server path/actionable error; восстановить диск и вручную scan; disable недоступен |
| Source исчезает или unreadable между stat и open/copy | Delivery fail/stale, incomplete result не применяется; успешный sibling сохраняется | Rediscovery/ручной retry; source bytes не изменять |
| Source size/mtime меняется при copy или перед apply | Отбросить copy/result и fenced apply; не заменять прежний analysis | Retry вручную после исправления; текущесть — owner-approved size/mtime, не stronger threat model |
| Невалидный/недоступный/non-writable output | Validate отклоняет до setting/reset mutation; staged copy не начинается/падает без fallback | Сохранить старое setting/refs и dirty draft; actionable inline error |
| ENOSPC, short read/write, partial copy | Copy не становится ready и не передаётся tools; preparation failure отдельно от tool failure | Предыдущие step successes остаются; восстановить место и вручную retry; без quota/limit |
| ffprobe/fpcalc частичный успех, multi/no audio | Независимые step results сохраняются; fingerprint может быть успешен без matching eligibility | Retry только failed step; matching gate не затирает provenance/success |
| SHA setting on/off; digest hit/miss | Off пропускает SHA, не tools; on только новые/изменённые/explicit retry; известный digest участвует в lookup | Нет backfill неизменённых файлов; toggle сохраняет digest/analyses |
| Fingerprint SHA hit / сменился selected fpcalc | Одна cache запись на SHA; reuse при актуальном результате, иначе вычислить current selected tool | Running/failed rerun оставляет прежний success; replace только по success; нет version history/backfill |
| Manual retry одного шага; partial success | Duplicates suppressed; retry reuses valid retained copy либо пересоздаёт отсутствующую | Не запускать/перезаписывать успешных siblings; нет timer auto retry |
| Active tools/settings сменились при queued/running/retry | Worker читает актуальные БД settings/selected tool; operation args не содержат полный конфиг | Retry использует current selection; live process безопасно завершает текущую работу, provenance сохраняет факт инструмента |
| Restart: row/mkdir/partial copy/ready/tools/step commit | Startup recovery проверяет DB ownership row, exact path и delivery fence; живой River delivery не orphan-ится | Пригодная copy retained; отсутствующая/stale ссылка снимается; cleanup только после fence invalidation |
| Crash после шагов success, до terminal/cleanup | DB result и fence authoritative; copy сохраняется до success всех requested steps | Явная cleanup позже; cleanup error не превращает analysis в failure |
| Bulk cleanup: missing/permission/live/foreign artifact | Только зарегистрированные eligible rows, item-by-item fenced unlink; missing reconcile, I/O error сохраняет ownership | Не сканировать директорию для выбора; повторить «Очистить» после исправления |
| Output reset, running/claimed исполнение или гонка worker claim | Running/claimed исполнение (включая гонку claim) запрещает reset, тогда как поставленная в очередь работа — нет; admission и queued-to-running claim берут shared cross-process output gate, reset — exclusive gate в одном lock order; durable preparing token блокирует новую admission/claim после crash до recovery | Никакой частичной инвалидации при отказе; queued работа очищается транзакционно и не блокирует reset; admission не может проскочить между проверкой и commit |
| Output reset: path valid, running/claimed исполнение отсутствует | Validate-only → exclusive gate → reread setting/running-or-claimed execution → одна DB tx меняет setting и инвалидирует ВСЕ queue/tasks и ВСЕ DB refs старого output | Сохраняются local entities/source links/inventory/analyses; старые physical files остаются unmanaged; no republish |
| Reset mkdir/DB error/crash между filesystem и commit | Durable reset token (`preparing`, exact owned-dir manifest, `committed` marker). Создать эксклюзивно только отсутствующие dirs; DB tx меняет setting, invalidates all queue/tasks+old refs и помечает committed. Rollback/recovery удаляет только manifest dirs доказанно пустые; committed token только finalized | До commit старое setting/refs живы; никогда не удалять старые/foreign files. При сомнительном ownership оставить dirs и показать recovery error; нет distributed transaction |
| Root removal | Только без active root operations; удалить queued root work/locations | Source bytes/root analyses/collections остаются; disable не предлагать |

### Текстовое сопоставление D03 с доступными mock screenshots (не visual/browser review)

В каталоге реально присутствуют `sources-1440.png`, `settings-1280.png`,
`sources-375.png`, `settings-375.png` (также desktop Sources/Settings по 1280/1440
и промежуточные размеры). Имена и список файлов проверены; screenshot pixels здесь
не проходили самостоятельный visual review. Из макетов можно только сформулировать
текстовую точку сравнения: Sources содержит root cards с путём/status/actions,
Settings — группы инструментов; узкий layout сводит контент в одну колонку. Эти
экранные состояния не показывают staged mode/output reset/concurrency. В glob
каталога нет Sources/Settings dark screenshots; есть только unrelated
`v2-match-dark-draft.png` и `v2-release-dark-draft.png`. Это не доказывает
непроверенность иных внешних артефактов, но полный dark visual review не заявляется.
Будущий UI review должен проверить canvas/ink/status tokens и статус текстом, не
только цветом, по фактическим light/dark screenshots.

Текстовый mock flow: Sources → Add root → выбрать один незаполненный mode radio
(submit disabled без выбора) → server path → save и manual scan → per-file
operation/status → partial success, retry only failed step → all-success cleanup
eligible → Settings: validate new output → running/claimed исполнение блокирует reset →
подтвердить reset после settlement → readback, при ошибке сохранить draft → явно
запустить bulk cleanup. На 375px flow предполагает вертикальные кнопки, переносы
строк, inline errors и focus restoration/keyboard controls; доступность этих
элементов в UI не проверялась. Это textual mock flow, не визуальная оценка screenshot
pixels, не browser run, не новый screenshot и не claim implemented. Финальный UI
acceptance остаётся отдельным этапом.
