# План 07 — независимое ревью, 2026-10-07

## Итоговый вердикт: COMPLETE — 2026-10-07

Независимая приёмка плана 07 завершена для code revision
`5448d069ea91ca0e9bc234ed41e7aa031a79c9b3`: реализация, полный primary gate и
свежий runtime evidence приняты. Открытых actionable blockers не найдено.
Основание: матрица всех критериев ниже и финальный раздел проверки runtime.
Исторические PENDING_RUNTIME формулировки сохранены как история промежуточного
ревью и **замещены этим итоговым COMPLETE**, а не являются текущим вердиктом.

## Историческое состояние перед runtime evidence: PENDING_RUNTIME

**Проверенная ревизия:** `5448d069ea91ca0e9bc234ed41e7aa031a79c9b3`.
Первоначальная полная матрица проверена на `a7177220987d97653c79c8ace1cb5928fb84ab81`;
завершающий commit `5448d06` отдельно проверен как узкое исправление aggregate UI copy.
Статическая приёмка реализации и архитектуры шагов 1–7 выполнена: открытых
блокирующих замечаний в проверенном scope нет. Итоговый COMPLETE пока **не выдан**:
для шага 8 требуется свежий runtime evidence на этой ревизии/бинарнике.
Ожидаемый отчёт: `docs/reports/plan07-runtime-acceptance-5448d06.md`.

### Метод и границы доказательств

Независимо прочитаны утверждённые решения плана и применимые AGENTS, текущий
код через Codegraph, SQL up/down, изменения четырёх завершающих коммитов и последующего UI-copy fix и
содержательные assertions связанных tests. Матрица ниже охватывает все строки
итоговых критериев плана (605–617), а не только новые frontend tests.
Reviewer не запускал tests/build/gates и не изменял исходники. HTTP integration
проверяет реальные PostgreSQL/admission/apply/readback boundaries, но синтетически
задаёт результаты tool execution; RTL использует HTTP/SSE mocks. Эти проверки
не выдаются за реальный запуск managed ffprobe/fpcalc.

Полный `task verify` выполнен primary session после коммита: exit 0. Reviewer
прочитал сохранённый лог
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan07-verified-5448d06.log`:
15 RTL files / 168 tests, Go integration packages, 2 tools tests, generation drift
check, golangci-lint/Biome, TypeScript/Vite и Go build. Наличие exit 0 сообщено
primary, а не получено повторным запуском reviewer. Перед созданием этого отчёта
HEAD подтверждён; tracked source changes отсутствовали (`test_stand/` untracked).
Бинарник прежней ревизии a717722 не выдаётся за финальный: его ранее сообщённый
SHA-256 относится только к той ревизии. Финальные binary checksum, vcs.modified
и соответствие runtime ревизии 5448d06 будут проверены по свежему runtime evidence.

## Матрица итоговых критериев

В путях ниже `persistence/`, `service/`, `jobs/`, `settings/`, `api/`,
`migrations/` означают `backend/internal/`; frontend пути — `frontend/src/features/`.
Статус «принято статически» описывает слой code/test evidence; требуемые реальные
runtime сценарии теперь приняты отдельно в финальном разделе ниже.

| Строка плана / критерий | Код и UI | Конкретные tests / свидетельство | Итог |
| --- | --- | --- | --- |
| 605. Typed PostgreSQL SHA setting, Settings UI, без env | `settings/settings.go`: `SHA256EnabledKey`, `GetSHA256Enabled`, `SetSHA256Enabled`; `service/setup_sha256.go`; `api/settings.go`; `settings/SettingsScreen.tsx` передаёт explicit boolean и перечитывает сервер | `TestSHA256EnabledDefaultsToTrueWithoutRow`, `TestSHA256EnabledReadbackPersistsAcrossRegistryInstances`, `TestSettingsSHA256DefaultAndRoundTrip`, `TestSHA256ProjectionAcrossReadRoutes`; RTL “saves an explicit false SHA-256 value, waits for the save, then refreshes server state”, loading/rejected-save tests | Принято статически; новая env не вводится |
| 606. Enabled new/changed only; disabled neutral; NULL/no bulk backfill; transitions сохраняют данные | `service/source_scan_start.go` сэмплирует policy в snapshot; `service/source_scan.go` выбирает targets по текущему stat; `persistence/source_scan_publish_analysis.go` сохраняет независимые шаги; UI отображает skipped/disabled без фиктивного digest | `TestSourceScanStartPinsSHA256PolicyAndRefusesPolicyReadErrors`, `TestSourceScanUnchangedStatusDoesNotBackfillWhenSHAIsEnabledLater`, `TestPublishPreparedSourceScanAnalysisDoesNotBackfillUnchangedWork`, disabled-hash часть `TestPublishPreparedSourceScanAnalysisTransactionally`, `TestSourceAnalysisPreparerHashFailureDoesNotUseSuppliedDigestOrBlockRunners` | Принято статически |
| 607. No-current-SHA independent locations; stale digest не identity; fingerprint не identity | `persistence/source_analysis_steps.go`: SHA-only canonical association; `source_scan_publish_analysis.go` меняет work при changed identity; `service/source_analysis_preparer.go`: `preparationDigest` не использует supplied digest при failed targeted SHA; inspector не фабрикует SHA | `TestSourceAnalysisStepAppliesAndPromotionWithPostgreSQL`, disabled-hash публикация без нового cache key, `TestSourceScanProbeErrorWithSelectedFingerprintAndNoDigestRetriesProbeOnly`; RTL “shows a shared digest and partial ffprobe result without fabricating a digest” | Принято статически |
| 608. Manual scan → automatic durable analysis только после полного успеха | `persistence/source_repository.go`: `ApplySourceScan`; private candidates + atomic publish/pending intent; `jobs/scan_worker.go` вызывает pending admission; startup reconciliation восстанавливает dispatch | `TestPublishPreparedSourceScanAnalysisTransactionally` проверяет rollback без результатов; `TestSourceScanWorkerRiverDispatchPostgreSQL`, `TestSourceScanStartupRecoveryPostgreSQL`, `TestSourceScanClosesPinnedHandlesAndAbandonsFailedTraversal`; `sources/SourceAutomaticAnalysis.test.tsx`: “queues and completes analysis for a newly inventoried location without a user analysis action” проходит actual SourcesScreen → scan wake → inventory navigation → queued/running/success, POST только scan | Принято статически; реальный crash/scan evidence ожидается |
| 609. Freshness size/mtime; changed stat rehash; same-stat bytes-proof не требуется | `service/source_scan.go`, `jobs/source_analysis_worker.go`: stat/namespace checks до/после; `persistence/source_analysis_steps.go`: `lockSourceAnalysisStep` проверяет current work/location/root и delivery fence | `TestSourceScanProbesNewFilesAndFilesWhoseSizeOrMtimeChanged`, `TestSourceScanFailsWhenAFileChangesUnderItsProbe`, `TestSourceScanFailsWhenATraversedFileChangedBeforeTheSnapshotCompleted`, `TestSourceScanRejectsOpenedReplacementRestoredBeforePostCheck`, `TestVerifyAbsoluteSourceStillCurrentDetectsReplacedInventoryRoot`; RTL stale admission refresh | Принято статически; лишнее требование bytes-proof не вводится |
| 610. Независимые успехи, fingerprint cache при probe failure, точечный retry/rerun сохраняет prior success | `service/source_analysis_preparer.go`: SHA-first independent lookup и parallel missing runners; `jobs/source_analysis_worker.go`: каждый sibling apply независимо; `persistence/source_analysis_steps.go` не затирает success failure; `service/source_analysis_start.go`: exact target/pins; `SourceAnalysisSteps.tsx`, `SourceInspectorScreen.tsx`, `useSourceInspector.ts` сохраняют result и перечитывают REST на SSE | `TestSourceAnalysisWorkerRunsSHAThenCombinedGroupPostgreSQL` / “probe failure does not prevent fingerprint apply”; `TestPublishPreparedSourceScanAnalysisTransactionally`; `TestSourceAnalysisPreparerSiblingFailureIsIndependentAndVersionIsPinned`, cache-hit tests; `TestSourceAnalysisHTTPAgainstPostgreSQL`: failure/success fingerprint и probe apply/settle, sibling preservation, version pin, holds=0; RTL retry queued/running/success, rerun retained failure и success replacement, terminal subscription close, disconnect/open | Принято статически |
| 611. Single-audio matching only; zero/multi retain successful fingerprint; no_audio enabled hash | `persistence/source_location_detail.go:84–89`: eligibility требует selected probe + fingerprint и count=1; хранение fingerprint не зависит от gate; `SourceAnalysisSteps.tsx` отображает отдельную нейтральную matching eligibility, а не ошибку fpcalc | `TestSourceAnalysisPreparerAcceptsNoAudioAndFailsMalformedProbeLocally`, `TestSourceScanPreparedAnalysisAllowsFingerprintOnProbeError`, `TestSourceAnalysisStepAppliesAndPromotionWithPostgreSQL`; RTL “keeps successful execution data when matching is unsupported”; scan/publication tests отдельно проверяют SHA/probe/fingerprint, no_audio inventory | Принято статически; no retry относится к matching eligibility, не к реальной ошибке fpcalc |
| 612. Current SHA+version reuse, concurrency/provenance; no SHA independent | `ApplySourceSHA256` выполняет INSERT ON CONFLICT, затем отдельный SELECT при READ COMMITTED и проверяет size; immutable fingerprint/cache keys SHA+fpcalc version; first committed cache winner не подменяет selected executed result | `TestConcurrentSourceSHA256ApplyUsesOneCanonicalVariantWithPostgreSQL`, `TestPublishPreparedSourceScanAnalysisLocksSharedDigestsInOrder`, `TestPublishPreparedSourceScanAnalysisOrdersRetainedAndNewDigests`, `TestApplySourceScanUsesSHA256FingerprintCacheWithoutMutation`, `TestSourceAnalysisPreparerIndependentCacheHitsPreserveProvenanceAndSuppressExecutors` | Принято статически; real duplicate reuse ожидается |
| 613. Active fpcalc swap lazy; actual/current versions в UI | `service/source_location_detail.go`: `activeFPCalcVersion`; selected result immutable; `service/source_analysis_start.go`: явный fingerprint-only rerun; `SourceInspectorScreen.tsx` предлагает rerun только при known different version и succeeded step, `SourceAnalysisSteps.tsx` отображает provenance/current | `TestSourceLocationDetailKeepsCanonicalAndSelectedProbeIdentityAndActiveVersionSeparate`, `TestSourceLocationDetailDoesNotInventActiveFPCalcVersion`; RTL older-version rerun, same-version/unknown-version no rerun, “retains the previous fingerprint version after a failed rerun”; `TestSourceAnalysisHTTPAgainstPostgreSQL` active version 1.6→1.7 и pinned rerun | Принято статически; real lazy update ожидается |
| 614. Restart/retry; migration/rollback не фабрикуют results | Durable per-step input survives operation deletion; recovery возвращает только owned unfinished delivery, не persisted failed siblings; strict snapshot decode; новая `20261015000000_source_analysis_guard_closure` preflight + deferred guards + down | `TestSourceAnalysisStartupRecoveryPostgreSQL`, `TestRecoverInterruptedNormalizedSourceAnalysisReturnsOnlyOwnedUnfinishedStepsToPendingWithPostgreSQL`, `TestRecoveredFingerprintRerunAdmissionUsesOriginalIntentWithPostgreSQL`, `TestRetainedSourceAnalysisReadmissionAfterDismissalWithPostgreSQL`, `TestRetainedSourceAnalysisStepIntentRejectsMalformedInputWithoutFallback`, `TestMigrationsApplyAndRollbackWithPostgreSQL`, `TestSourceAnalysisGuardClosureMigrationWithPostgreSQL` | Принято статически; реальный restart ожидается |
| 615. Read-only sources; exact tool holds; independent roots; references сохранены | sourcefs read-only descriptors; filesystem execution вне DB transaction; root locks локальные; shared tool gates совместимы; digest-less cleanup проверяет references; tool acquire/release для scan меняет flag атомарно и допускает subset snapshot tools | `TestSourceAnalysisToolFreeRetryIsCompatibleWithToolsMoveWithPostgreSQL`, `TestSourceAnalysisEnqueueLocksPinnedInstallationWithPostgreSQL`, оба concurrency order move/admission tests, `TestNormalizedSourceAnalysisTerminalReleasesToolAndWorkHoldsWithPostgreSQL`, `TestSourceAnalysisDeletionCleanupWithPostgreSQL`, `TestSourceScanToolHoldsUseVerifiedSelectionsWithPostgreSQL`, concurrent two-root ordered-digest tests | Принято статически; source byte evidence ожидается. Matching/publication ещё не исполняются, их сохранность не объявляется испытанной live-сценарием |
| 616. Без scheduled scan/auto retry/matching/groups/publication | Единственный explicit scan; recovery pending delivery не auto retry persisted failure; REST/UI содержит только step retry/rerun; README точно перечисляет вне-scope зоны | Архитектурное ревью dispatch/admission/recovery и README; `TestSourceAnalysisStartupRecoveryPostgreSQL` сохраняет failed sibling; RTL no all-in-one Analyze и automatic flow без analysis POST | Принято статически; новых product decisions не требуется |
| 617. task verify + independent acceptance | Gate log на проверенной revision, этот независимый отчёт и ожидаемый свежий runtime report | Primary gate exit 0, reviewer прочитал log; COMPLETE возможен только после проверки runtime evidence | **COMPLETE**: gate и свежий runtime evidence независимо сопоставлены; см. финальный раздел |

## Закрытие замечаний SQL и regression review

Коммит `4df4f46` вводит новую миграцию, не переписывая старые:

1. `execution_operation_attempt IS NOT NULL` закрывает SQL CHECK NULL loophole.
2. Membership/hold guards проверяют root/location work относительно operation;
   integration test проверяет также mutation target после admission.
3. Tool flag/holds проверяются до NULL-mode return; terminal scan holds запрещены.

Guard остаётся deferred: admission, cache hit и промежуток между файлами законно
имеют zero holds; parallel tools могут держать subset snapshot selections; release
последнего hold атомарно снимает flag. Snapshot tool pins не превращены в требование
постоянно удерживать все инструменты. Existing terminal/recovery flow очищает holds
и execution triples атомарно.

Первоначальное замечание к preflight test fixture также закрыто: теперь fixture
создаёт настоящую normalized operation + work hold и execution с non-NULL ID/job,
но NULL attempt — именно комбинацию, которую принимал прежний CHECK. Upgrade
отказывает до DDL; down возвращает прежнюю shape без удаления результатов.
Новых actionable blockers в повторном review не найдено.

## Шаг 8: документация и оставшаяся runtime приёмка

`README.md` описывает ручной scan, automatic independent steps, default-enabled
SHA policy/no unchanged backfill, private publication после successful traversal,
120-second fpcalc default без собственного length limit, fingerprint reuse при
probe failure, single-audio matching gate, step retry и lazy version rerun.
`docs/design/data-model.md` согласует физические constraints/holds со схемой;
концептуальная модель не выдаётся за downstream implementation. Исторические done
планы 05/06 не переписаны завершающими коммитами. План остаётся в todo до COMPLETE.

Свежий runtime report должен подтвердить actual PostgreSQL/River + managed tools:
new/changed/unchanged, explicit failed-step retry без sibling rerun, restart recovery,
duplicate SHA reuse, lazy active fpcalc update, read-only bytes и соответствие
бинарника указанной revision. Пока отчёт отсутствует, reviewer не заявляет
выполнение этих сценариев и не переносит план в done.

Ограничения: локальный gate и статическое ревью на macOS; Windows/Linux platform
matrix и реальные filesystem interleavings на всех платформах этим отчётом не
проверены. Windows arm64 остаётся утверждённо unsupported. Для managed runtime
платформы/версии/ограничения будут взяты только из свежего runtime evidence.

## Узкий финальный regression review: 5448d06

`SourceInspectorScreen.tsx:169–175` теперь различает queued по operation.state,
а running описывает нейтрально («Выполняется этап анализа файла»); applying
остаётся сохранением результата. Это закрывает реальную low-severity неточность:
worker использует stage probing даже для fingerprint-only/SHA-only delivery,
поэтому прежнее сообщение ошибочно обещало запуск ffprobe. Новая формулировка
не меняет product decisions или исполнение; per-step states остаются источником
точных сведений.

RTL `SourceInspectorScreen.test.tsx`, параметризованный test «describes a
fingerprint-only running operation neutrally at stage %s», проверяет probing и
unknown future-running-stage, сохранность old fingerprint и отсутствие обеих
ложных надписей (ffprobe/queued). Обновлённые operation/navigation assertions
согласованы с этим контрактом. Reviewer прочитал код и diff; tests самостоятельно
не запускал. Final primary gate log подтверждает 168 RTL tests / 15 files.

Сообщённый primary диагностический SIGSTOP/SIGKILL сценарий a717722 (реальный
running step с exact delivery fence, затем recovered successor с сохранёнными
siblings; отдельный committed failure без successor) принят только как
предварительное диагностическое свидетельство, не заменяет свежий report 5448d06.
Вердикт остаётся **PENDING_RUNTIME**.

## Финальная независимая проверка runtime и COMPLETE — 2026-10-07

Прочитан актуальный `docs/reports/plan07-runtime-acceptance-5448d06.md` и реальные
JSON/SQL/HTTP/server-log artifacts, а не только summary исполнителя. Первые шесть
сценариев и exact probe retry уже независимо приняты из каталога
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan07-final-5448d069ea91ca0e9bc234ed41e7aa031a79c9b3/`:

- initial: новые файлы автоматически получают независимые результаты; malformed
  file сохраняет успешный SHA при ошибках tools; no_audio также имеет успешный SHA;
- unchanged: успешные selected results/provenance сохранены;
- duplicate: новый путь выбирает реальный SQL fingerprint cache winner с
  reuse_origin=sha256, не подменяя original selected result;
- changed: новый stat и digest дают новый анализ;
- toggle: disabled SHA skipped без digest при успешных tool siblings; включение
  при неизменном stat не делает backfill; последующее изменение снова даёт SHA;
- lazy: activation не меняет selected results; explicit fingerprint rerun на
  1.6.1 меняет только fingerprint, не SHA/probe;
- exact probe retry: attempt 1→2 только у probe, прежние SHA/fingerprint одинаковы;
  retained input/version/policy совпадают. Повтор malformed fixture ожидаемо failed.

Семь actual per-scan before/after source manifests и retry manifest независимо
сравнены: paths, sizes и SHA-256 одинаковы. Fixture preparation между сценариями
не трактовалась как запись приложения. Lazy recorded operation — fingerprint rerun,
не scan; runtime report теперь правильно различает эти события.

### Свежий классифицированный crash/restart

Независимо прочитаны `crash-prekill-sql.json`, `crash-recovery-evidence.json`,
`crash-post-sql.json`, manifest и server log в каталоге
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan07-runtime-restart-e7c3b6e0-d0bc-42cf-a139-cf96925ca3f8/`.
Фактический isolated run/database ID в manifest/post-SQL —
`ed6aa884-102f-40ac-b1cf-f0ac878a8880`; имя artifact directory — отдельный идентификатор,
не имя database. Root: `2a40715f-7192-4ae9-a5ac-3d1d4c41010c`.

Замороженный после SIGSTOP owned PID 62436 SQL capture подтверждает:

- operation `976ff2b3-2e3d-49b9-a9e9-ab985207534a`, queued, operation attempt 1;
- **River job 8 running**, то есть delivery уже claimed, несмотря на попытку
  harness приостановить isolated queue;
- work `236c954d-6ec5-4156-a6ac-2ee483a752ef`, probe queued, step attempt 1;
- execution triple точно совпадает с operation ID/attempt/job 8.

После SIGKILL и restart того же бинарника old operation стала failed/recovered;
создана **новая** operation `1bd00025-f091-4c93-b16b-7c5f07aab290`, отсутствующая в
ops_before (старые b419…/0da… не приняты за новый successor). Successor имеет River
job 9, тот же work/root/location/probe target, полностью одинаковый operation
snapshot и retained step input с policy version 1. Post-SQL и terminal REST
подтверждают settled failed/applying с ожидаемой ошибкой malformed ffprobe fixture;
River job 9 completed. Probe attempt вырос ровно 1→2, execution triple очищен.
SHA sibling целиком совпадает с before (succeeded, attempt 0), fingerprint sibling
целиком совпадает (failed, attempt 0). Source before/after JSON manifests одинаковы.

В intermediate JSON successor ещё running; это не выдаётся за terminal evidence:
для settlement использованы `after_terminal`, `successors_terminal_after_capture`
и более поздний post-SQL. Старый orphan River job 8 ещё running в коротком capture
не означает потерю работы: durable old operation terminal и delivery fenced;
River rescue/late delivery не может повторно применить terminal operation.

Fresh stand manifest имеет `acceptance_complete=false`, поскольку этот stand не
прогоняет полный шестисценарный harness; данный общий флаг не использован как
доказательство pass. Приёмка основана на конкретном frozen/terminal SQL и REST,
которые подтверждают именно restart scenario. Предыдущий unclassified crash без
River phase и диагностическая ревизия a717722 остаются историческими, не подменяют
этот финальный classified proof.

### Provenance, gate и границы итогового решения

Reviewer самостоятельно вычислил SHA-256 обоих retained runtime binaries:
`6f417876010b256f78564d241811591ad4e56f84398778be8f00b0efcb754ab2`.
Чтение embedded build metadata подтверждает `vcs.revision=5448d069ea91ca0e9bc234ed41e7aa031a79c9b3`,
`vcs.modified=false`, darwin/arm64. При итоговой проверке HEAD —
`93fe36f379e74a48089ac20230dd56a96621585f`; read-only diff относительно 5448d06
для backend/frontend/tools/Taskfile.yml пустой: последующие docs commits не меняют
проверенный code/build interface. Финальный primary `task verify` exit 0 и
15 RTL files/168 tests подтверждены ранее прочитанным gate log; reviewer gate
не повторял, не запускал tests/build, не менял исходники/plan location и не коммитил.

Реальный runtime ограничен macOS arm64, PostgreSQL 17, managed ffmpeg/ffprobe 9.0.2
и fpcalc 1.6.0/1.6.1. Cross-platform runtime и весь OS/filesystem matrix не заявляются
проверенными; Windows arm64 остаётся unsupported. Успех fpcalc при probe failure и
zero/multi-audio сохранение подтверждены code/test слоями матрицы; malformed runtime
fixture не заявляется доказательством успешного fingerprint. Ни matching/groups,
ни publication, ни scheduled scan/auto retry в приёмку не добавлены.

**Итог: COMPLETE.** Все критерии плана сопоставлены с реализацией и доказательствами;
обязательный restart proof теперь есть на финальном бинарнике. Дополнительный
still-available queued-job/same-operation runtime capture не требуется планом:
наблюдаемый claimed-job/successor путь достаточен. Перенос плана в done оставлен
primary session после этого независимого вердикта.
