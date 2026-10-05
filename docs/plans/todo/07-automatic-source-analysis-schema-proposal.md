# Проект физической схемы автоматического анализа источников

**Дата:** 2026-10-05. **Статус:** revised техническая proposal перед новым независимым review; не реализация и не applied DBML. **Область (уточнение владельца 2026-10-05):** dev-ветка, ноль установок/релизов, legacy отсутствует; прежний backward-compatibility дизайн отменён. Предыдущий review PASS относился к прежней, более широкой области и не покрывает эту редакцию — требуется новый review. Продуктовые правила из `decisions.md` не меняются; исторические планы 05/06 не редактируются, основной план 07 актуализирован.

Схема — единая актуальная форма: нет v1/v2-исполнителей, нет version-колонок «только для совместимости», нет dispatch по версии схемы и нет фикстур старых операций. Существующая migration chain упоминается фактически (раздел 8). Реализация разбита на два staging-этапа без legacy-рамки (раздел 9).

## 1. Границы и последовательность

Для нового/изменённого файла source inventory:

```text
stat → SHA при enabled → lookup успешного probe по digest
     → reuse либо один полноценный ffprobe → stat/confirm → private candidate
     → атомарный успешный scan apply → независимый fingerprint
```

SHA default enabled, включая `no_audio`. Hash-first — порядок оптимизации, не prerequisite: disabled/failed hash не блокирует probe. Hash-only и failed/in-flight probe не считаются cache hit. Успешный zero-audio probe — результат и допускает reuse. Один полноценный probe получает все данные сразу, второго probe после scan нет. Multi-stream/zero-audio не запускают fpcalc. Source read-only/`in_place`, 13 расширений, без staged/work-directory и новых policies.

Unchanged audio/`no_audio` сохраняет current results без повторного чтения. Unchanged `probe_error` перепроверяется при scan; сохранённый digest допускает lookup, отсутствующий не вычисляется, failed SHA сам не повторяется. Переход `probe_error` → `audio` ставит первый fingerprint, не SHA backfill. Toggle не создаёт jobs, не удаляет результаты и не меняет начатую operation. Concurrent cache misses могут выполнить probe независимо: это не создаёт две SHA identities и не требует глобального I/O lock.

## 2. Физическая целевая схема

Baseline — репозиторная migration chain до `source_media_variant` (раздел 8). Существующий DBML концептуален: SQL использует `mtime timestamptz`, размер `>= 0`, location → variant RESTRICT, provenance UUID без FK на очищаемую operation; quality/matching/publication таблицы не применяются.

### media_variant

Probe group присутствует целиком либо отсутствует: `ffprobe_json`, `ffprobe_version`, `analysis_policy_version`, `observed_tags`, `inspected_at`, `applied_operation_id`. SHA-only row не получает `{}`, пустой banner или fabricated success; `applied_operation_id` — provenance probe, не последнего sibling apply. Добавляются nullable `source_sha256` (**NULL либо ровно 32 bytes**; UNIQUE canonical identity), hash time/algorithm/provenance UUID и audio count (0 — успешный zero-audio, NULL — нет результата).

### media_fingerprint_result / media_fingerprint_cache

Immutable compressed fingerprint, actual parsed fpcalc version, raw version banner, algorithm ID с namespace, reported duration, calculated time, operation UUID и parser contract version. PK UUID; UNIQUE `(id, fpcalc_version)`. Отдельная SHA/version cache association задаёт reuse key; selected result принадлежит step, не UNIQUE variant/version. `media_fingerprint_cache`: PK `(source_sha256, fpcalc_version)`, SHA FK RESTRICT на unique variant digest, composite `(result_id, fpcalc_version)` FK RESTRICT на result; first committed success wins, same-version rerun не перезаписывает cache. У no-SHA variant прежний success удерживается во время rerun и при failure; после успешной замены старый неиспользуемый fingerprint очищается после освобождения refs/holds. Неудача не перезаписывает successful row.

### source_analysis_work / source_analysis_step

Work: `id uuid PK`, `location_id uuid NOT NULL UNIQUE`, `source_root_id uuid NOT NULL`, immutable configured/inventory/relative paths/size/mtime, `sha_enabled`, `origin_scan_operation_id uuid NOT NULL` (provenance **без FK**), `created_at`. Добавить source_location UNIQUE `(id, source_root_id)`; work composite FK `(location_id, source_root_id)` CASCADE. Changed identity получает новый work UUID; unchanged сохраняет work; backfill нет.

Step: PK `(work_id, step)`, work FK CASCADE. Каждый шаг независим: latest attempt/delivery/safe error и ссылка на successful result с provenance/reuse origin. Execution triple: `execution_operation_id uuid`, `execution_operation_attempt integer`, `execution_job_id bigint` — все NULL либо все заполнены, attempts > 0; queued/running требуют triple. Failed требует непустой safe_error, skipped требует reason. Отдельные nullable RESTRICT FK: `success_sha_variant_id`, `success_probe_variant_id`, `success_fingerprint_result_id` — допустимы только соответствующему step; succeeded требует соответствующий result, а queued/running/failed могут удерживать previous success. Last-operation provenance UUID без FK. Это current state, не история; changed identity заменяет current work, старые terminal snapshots — диагностика. Отказ retry ничего не меняет.

### Operation shape и holds

Operation получает `source_analysis_mode text NULL` (`batch`/`single_step`), `target_work_id uuid NULL REFERENCES source_analysis_work(id) ON DELETE SET NULL`, `target_step text NULL` (`sha256`/`probe`/`fingerprint`), `tools_read_required boolean NOT NULL DEFAULT false`. Scan содержит active root, work/step/mode NULL. Batch содержит active root, location/work/step NULL. Single-step требует active root/location/work/step; после terminal targets nullable. Принадлежность work/location/root проверяется admission и constraint trigger. Прежний ручной ffprobe-only trigger заменён этим pipeline, dual executor не сохраняется.

`operation_tool_read_hold`: PK `(operation_id, installation_id)`, оба NOT NULL FK RESTRICT. Deferred constraint trigger проверяет для committed active source operation `tools_read_required = EXISTS(tool holds)`. Terminal удаляет holds атомарно. Source tools-move exclusion учитывает **только `tools_read_required`**, без fallback; hash-only retry не зависит от tools move. Per-root active-operation unique index действует для всех source operations.

`operation_source_work_hold`: PK `(operation_id, work_id)`, оба FK RESTRICT. Step execution pair `(execution_operation_id, work_id)` ссылается на этот hold; admission проверяет root membership. Hold не позволяет потерять active batch target через location/work CASCADE. Terminal сначала очищает execution triples, затем holds; operation cleanup не удаляет work. Authoritative delivery — `operation.river_job_id`.

## 3. Scan publication и durable batch

До полного успешного traversal SHA/probe принадлежат только candidates: не менять visible location, variant association или inspector; не создавать downstream jobs. Abandoned/failed scan очищает private candidates. Внутри одного scan допустим ephemeral cache подготовленных successful probe по digest.

В transaction: проверить operation/attempt/root → reconciliation candidates → отвязать stale identity → canonical SHA lookup/insert → association successful outcomes → independent step state и immutable work backlog → generation/path/availability → terminal scan/release holds → durable pending intent → удалить candidates. Отдельная admission transaction создаёт batch и River job из зафиксированного intent. Ошибка откатывает всю публикацию.

Выбор: **одна automatic batch operation на root**, work items durable, каждый fingerprint apply отдельный. Failure одного item не прекращает остальные. Explicit failed-step retry / fingerprint rerun — single-target operation. Root lock только в короткой transaction, не на hashing/probe/fpcalc; другие roots независимы. Queue concurrency 1 — существующий технический предел, не оправдание глобального DB lock.

SHA policy фиксируется в scan snapshot: scan выполняет hash preparation, analysis work наследует immutable факт, не читает toggle заново. Новая настройка не отменяет enabled failed-hash retry и не создаёт hash intent для unchanged digest-less location. Fpcalc selection фиксируется при старте требующей его analysis operation.

## 4. Terminal states и recovery

Один orchestration River job на batch, без child jobs. Step хранит
`step_attempt integer NOT NULL >=0`; claim увеличивает его и возвращает captured
attempt. Claim/apply проверяют текущий work/stat/root/location, operation
ID/attempt/job и captured step attempt. Execution triple FK ссылается на UNIQUE
`operation(id,attempt,river_job_id)`; pair operation/work — на work hold.
Recovery атомарно очищает/перевязывает только unfinished owned steps перед
сменой delivery identity. Terminal очищает triples до удаления holds;
persisted failures и committed successes не перепоставляются.

Step states: durable `pending`; `queued`/`running`; `succeeded`; `failed` с safe error; neutral `skipped` с disabled/unsupported reason. `reused` — provenance success, не новый запуск executable. Args содержат только operation ID; snapshot имеет единую актуальную форму с внутренним `schema_version` (обозначение формы, не dispatch-ключ), без dual-path. Unknown/malformed snapshot → safe refusal/failure/release, без guesses из current settings. Fence — current immutable work identity + target step + attempt/job ID. SHA может законно присоединить current location к canonical shared variant.

Recovery восстанавливает недоставленный pending/uncommitted dispatch из durable state, не перепоставляет persisted failed steps и не выполняет committed success. Deleted/stale target не получает late apply; old-attempt delivery fencing-ится. Duplicate terminal delivery — no-op. Successful probe retry атомарно создаёт первый необходимый fingerprint intent, не перепоставляя успешные siblings. Terminal batch освобождает holds, не стирая successful item results.

## 5. SHA identity, provenance и lifecycle

`INSERT ... ON CONFLICT DO NOTHING`, затем **отдельный SELECT** canonical row при READ COMMITTED: один CTE snapshot может не увидеть победивший concurrent insert. Проверить size canonical row; mismatch — corruption/conflict. Mtime принадлежит location/work, не shared identity. NULL не объединяет locations. Ошибки per-location attempts не загрязняют shared successes. Reuse сохраняет исходный version/time/provenance; stale digest не участвует в lookup.

Успешный hash retry при уже успешных probe/fingerprint: revalidate fence →
canonical insert/select/size check → переключить location identity и SHA
selection, **не изменяя selected sibling results** → заполнить только
отсутствующие canonical probe/cache successes с исходной provenance → cleanup
после обновления consumers. Existing canonical successes и чужие consumers
не заменяются. Concurrent probe writer, проигравший заполнение canonical row,
сохраняет собственный probe-bearing result, пока тот selected своим work.

FK: root → locations CASCADE только inventory; location → variant RESTRICT; active source targets/variant/tool holds нельзя потерять cascade-ом. Provenance UUID не FK на очищаемую operation; snapshot UUID сам по себе не hold. Normalized read holds защищают только реально запускаемые tools: scan — pinned managed FFmpeg, fingerprint — fpcalc, hash — ни одного. Reuse не требует executable/version query/hold. Retry восстанавливает нужные holds вместе с durable enqueue; terminal transition/release atomic.

Cleanup no-SHA variant проверяет **все существующие** inbound references: нет locations, current successful-result consumers, live read holds или иных domain refs. SHA orphan остаётся cache. Предпочтителен targeted cleanup, не глобальный sweep на каждом apply. Старый result у changed location не сохраняется как current.

## 6. Общий lock ordering

Порядок: shared tools-move coordination gate для tool-dependent admission (exclusive для move admission/switch) → package active-selection locks в стабильном порядке → roots → locations/work → installations → operations/steps → digest/variants/results → writes/InsertTx. UUID sets сортируются. Compatible shared/key-share holds для readers, exclusive mutation для delete. SHA-only path не берёт tools gate; под gate проверяется durable active move.

Не брать operation row до root, если competing mutation берёт root → operation. Terminal release не должен затем захватывать более раннюю lock category. Filesystem execution вне DB tx: durable holds защищают tool lifetime, короткие locks — admission/state.

Blast radius: scan start/apply/retry/recovery; analysis start/apply/fail/retry/recovery; root update/delete; tool activation/delete/move; tools-root settings mutation; common transitions. Убрать global operation table lock только после приведения всех путей к этому порядку; query barriers должны доказать progress unrelated roots, не только отсутствие constraint failure.

Отдельное pending admission получает tools gate/package selection до root lock;
выбирает fpcalc и holds только для cache misses. Active tools move оставляет
intent pending; отсутствующий/непригодный fpcalc переводит только fingerprint
в failure, без отката SHA/probe. Dispatch вызывается после scan commit,
successful prerequisite retry, terminal source operation и terminal tools move;
startup reconciliation также ищет pending независимо от origin operation.
Вызовы идемпотентны: progress не требует рестарта, crash handoff не теряет intent.

## 7. fpcalc transport

Managed argv: `-json -- ABSOLUTE_SERVER_SOURCE_PATH`, без shell, PATH, client paths/version или `-length`. Server-resolved pathname + before/after stat соответствует owner trusted-filesystem contract. Descriptor-only fpcalc не требуется и не является blocker. Absolute path **не блокирует secondary local/network references**; sandbox или single-primary-file guarantee не заявляются; новый sandbox/demuxer policy не вводится.

Nonzero exit/empty/malformed output не success. Bounded capture, cancellation, reap; compressed algorithm сохраняется с namespace, header validation не выдаётся за full payload validation. Reported duration — stream/container duration, не processed fingerprint length. Actual Chromaprint version и build library identifiers сохраняются из `-version`. Проверенные upstream сведения: [техническая записка](../../reports/plan07-fpcalc-upstream-notes.md).

## 8. Migration и rollback

Существующая репозиторная chain (факт): bootstrap (20260927), setup_manager (28), persistence_contract (29), transition_invariants (30), operation_targets (20261001), installation_contract (02), source_inventory (03), configured_path_platform (04), source_media_variant (05). Это repository chain, а не утверждение о том, в каких БД она применена. Старые migrations не изменяются; existing `media_variant` down удаляет analysis domain и не подходит как down расширения.

Forward migration добавляет актуальную схему. **Down до любых изменений**
проверяет несовместимые строки и отказывает при небезопасном состоянии — без
fabricated probe, silent downgrade или destructive guesses. Данные не удаляются
автоматически для разрешения down; без несовместимых artifacts удаляется только
добавленная структура и возвращается предыдущая shape. Нет дискриминации версий
и default/backfill legacy-триггеров. Rollback проверяется на clean/populated
current model; каждый refusal сохраняет строки, schema и migration ledger.

## 9. Staging (без legacy-рамки)

Реализация разбита архитектором на два этапа, чтобы этап 2 не зависел от будущего admission-кода.

**Этап 2 (migration + apply APIs):** только result/candidate SHA/fingerprint cache, work/step, normalized hold tables, delivery fences и полные apply APIs. Существующие operation-колонки и active-constraints временно не меняются — это не legacy-рамка и не второй executor. `operation (id, attempt, river_job_id)` UNIQUE сохраняется как step-fence до этапа 3.

**Этап 3 (одна coherent migration + code commit):** unified operation `mode`/`target`/`tool flag`, CHECK/exclusion, deferred membership guards, все constructors, admission/retry/recovery/terminal, общий root/tools lock order. Normalized hold tables уже доступны из этапа 2.

Без `source_contract_version`/`result_contract_version`, без default/backfill legacy-триггеров, без `job kind_v2`. Down каждого этапа удаляет только свои фактические artifacts и отказывает при небезопасном состоянии: down этапа 3 восстанавливает shape этапа 2; down этапа 2 возвращает предыдущую shape.

## 10. Проверки и кодовые точки

Кодовые точки: source scan/probe/parser; candidate apply/reconciliation; media_variant partial model/cleanup; analysis enqueue/retry/recovery; jobs dispatch/reconcile; setup_manager tool locks; typed settings registry; inspector REPEATABLE READ; Huma/OpenAPI/Orval штатным pipeline.

Обязательные сценарии: clean и populated current-model upgrade/down; known SHA audio/no-audio reuse с original provenance; hash failure + probe success; probe failure + SHA success; enabled/disabled/transitions/no backfill; unchanged probe_error → audio; failed traversal/no partial publication; crash around scan commit/dispatch/step apply; duplicate/old attempt delivery; step-only retry и rerun preservation; concurrent canonical insert; move/delete/root/retry в обоих порядках; unrelated-root progress; cache/orphan/refs cleanup; real managed tools/argv/provenance; consistent inspector при apply/unlink.

Изучены root/local AGENTS, design/DBML, план 07, done 02–06, audits 2026-10-01/02, plan06 reports, app-design. Codegraph использован первым. SQL — источник физической схемы; статический review и historical reports не являются свежим runtime evidence. Baseline `task verify` прошёл; **новая схема не реализована и не проверена**. Локальный gate — только `task verify`; platform matrix — CI.
