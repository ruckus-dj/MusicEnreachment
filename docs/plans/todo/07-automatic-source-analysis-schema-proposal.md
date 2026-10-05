# Проект физической схемы автоматического анализа источников

**Дата:** 2026-10-05. **Статус:** техническая proposal перед независимым review,
не реализация и не applied DBML. Основание — план 07 и уточнения владельца
в `decisions.md`. Исторические планы 05/06 не изменяются.

## 1. Границы и последовательность

Для нового/изменённого файла существующего inventory filter:

```text
stat → SHA при enabled → lookup успешного probe по digest
     → reuse либо один полноценный ffprobe → stat/confirm → private candidate
     → атомарный успешный scan apply → независимый fingerprint
```

SHA default enabled, включая no_audio. Hash-first — порядок оптимизации,
не prerequisite: disabled/failed hash не блокирует probe и допустимый fpcalc.
Hash-only variant и failed/in-flight probe не считаются успешным cache hit.
Успешный zero-audio probe является результатом и допускает reuse.
Один полноценный probe сразу получает все нужные данные; второго probe после
scan нет. Multi-stream/zero-audio не запускают fpcalc. Source read-only/in_place,
13 расширений, без staged/work-directory и новых policies.

Unchanged audio/no_audio сохраняет current results без повторного чтения.
Unchanged probe_error перепроверяется при scan; текущий сохранённый digest
допускает lookup, но отсутствующий digest не вычисляется и failed SHA сам не
повторяется. Переход probe_error → audio ставит первый fingerprint, не SHA
backfill. Toggle не создаёт jobs, не удаляет результаты и не меняет уже начатую
operation. Concurrent cache misses могут выполнить probe независимо; это не
создаёт две SHA identities и не требует глобального I/O lock.

## 2. Текущее → target → миграция

Физический baseline — migrations до `20261005000000_source_media_variant`.
Существующий DBML концептуален: SQL использует `mtime timestamptz`, размер
`>=0`, location → variant RESTRICT и provenance UUID без FK на очищаемую
operation. Quality/matching/publication таблицы из DBML не применены.

| Текущее | Target | Новая forward migration |
| --- | --- | --- |
| media_variant требует полный успешный ffprobe | Partial variant с nullable probe group и SHA-only identity | Добавить SHA/provenance/audio count, снять NOT NULL/defaults только с probe group, all-or-none CHECK |
| SHA отсутствует | NULL либо ровно 32 bytes; unique non-null canonical identity | CHECK octet_length, UNIQUE source_sha256; без legacy backfill |
| Fingerprint отсутствует | Успешный result отдельно от latest attempt | media_fingerprint_result с actual version/algorithm/banner/time/provenance |
| Один общий analysis outcome | Независимые current steps | source_analysis_work и source_analysis_step |
| Candidates содержат только metadata/status | Private SHA/probe outcomes с provenance/reuse origin | Nullable candidate payload fields; не публиковать до успешного apply |
| Reconciliation удерживает variant только для unchanged audio | Удерживать любой допустимый current result при unchanged identity | Исправить predicate, не наследовать stale digest |
| Обязательный legacy FFmpeg hold | Только фактически используемые tools | operation_tool_read_hold; legacy columns и dispatch сохраняются |
| Один active scan/analysis на root | Одна active batch/single-step operation с durable backlog | V2 operation shape; сохранить per-root admission, убрать global table lock |
| Любой orphan удаляется | SHA orphan cache сохраняется; no-SHA только после всех refs/holds | Targeted cleanup затронутых IDs |
| Decoder только v1 | Explicit v1/v2 paths | Новые snapshots; старые bytes/state не переписывать |

Existing variant UUIDs, location links, legacy successful probe и v1 snapshots
сохраняются. Не мигрировать fingerprint/digest из тегов или других эвристик.

### media_variant

Probe group присутствует целиком либо отсутствует:
`ffprobe_json`, `ffprobe_version`, `analysis_policy_version`, `observed_tags`,
`inspected_at`, `applied_operation_id`. SHA-only row не получает `{}`, пустой
banner или fabricated success. `applied_operation_id` остаётся provenance
probe, не последнего sibling apply. Добавляются nullable digest, hash time /
algorithm / provenance UUID и audio count (0 — успешный zero-audio, NULL —
отсутствующий результат/неизвестное legacy значение).

### media_fingerprint_result

Immutable compressed fingerprint, actual parsed fpcalc version, raw version
banner, algorithm ID с явным namespace, reported duration, calculated time,
operation UUID и parser contract version. Отдельная SHA/version cache association
задаёт reuse key; selected result принадлежит step, не UNIQUE variant/version.
У SHA variants сохраняются successful versions для cache. У no-SHA variant
прежний success удерживается во время rerun и при failure; после успешной замены
старый неиспользуемый fingerprint очищается после освобождения refs/holds.
Неудача не перезаписывает successful row.

### source_analysis_work / source_analysis_step

Work — current location/root, immutable configured/inventory/relative paths,
size/mtime, origin scan, sampled SHA policy и operation linkage. Step key —
`(work_id, step)`. Отдельно хранятся latest attempt/delivery/safe error и ссылка
на successful result с provenance/reuse origin. Это current state, не новая
пользовательская история. Changed identity заменяет current work; старые terminal
snapshots остаются самостоятельной диагностикой. Отказ retry ничего не меняет.

## 3. Scan publication и durable batch

До полного успешного traversal SHA/probe принадлежат исключительно candidates:
не менять visible location, variant association или inspector; не создавать
downstream jobs. Abandoned/failed scan очищает private candidates. Внутри одного
scan допустим ephemeral cache уже подготовленных successful probe по digest.

В transaction scan v2: проверить operation/attempt/root → reconciliation всех
candidates → отвязать stale identity → canonical SHA lookup/insert → association
successful outcomes → independent step state и immutable work backlog →
generation/path/availability → terminal scan/release holds → durable pending
intent → удалить candidates. Отдельная admission transaction создаёт batch и
River job из зафиксированного intent. Ошибка откатывает всю публикацию.

Выбор: **одна automatic batch operation на root**, а не N active операций,
конфликтующих с нынешним root unique index. Work items durable, каждый fingerprint
apply отдельный. Failure одного item не прекращает остальные. Explicit failed
step retry / fingerprint rerun — single-target v2 operation. Root lock только в
short transaction, не на hashing/probe/fpcalc; другие roots независимы.
Queue concurrency 1 допустима как существующий технический предел, не оправдание
глобальной exclusive DB lock.

SHA policy фиксируется в **scan v2 snapshot**: именно scan выполняет hash
preparation. Subsequent analysis work наследует этот immutable факт, не читает
toggle заново. Новая настройка не отменяет enabled failed-hash retry и не создаёт
hash intent для unchanged legacy digest-less location. Fpcalc selection
фиксируется при старте требующей его analysis operation, не меняется activation.

## 4. Version dispatch, terminal states и recovery

| Snapshot | Путь |
| --- | --- |
| analyze_source v1 | Исходный manual ffprobe-only executor, previous-variant fence, holds/retry/recovery |
| scan_source v1 | Legacy scan/apply/recovery, без ретроактивного automatic pipeline |
| scan_source v2 | Sampled SHA, pinned FFmpeg, private full probe, atomic publication/backlog |
| analyze_source v2 batch | Durable file work items, только нужные downstream steps/tools |
| analyze_source v2 single-step | Explicit failed-step retry либо successful fingerprint rerun |
| Unknown | Safe refusal/failure/release, без guesses из current settings |

Decode envelope, затем конкретный snapshot. Args содержат только operation ID.
V2 fence — current immutable work identity + target step + attempt/job ID.
Не переносить blanket previous_variant equality из v1: SHA может законно
присоединить current location к canonical shared variant.

Step states: legacy `not_requested`; durable `pending`; `queued/running`;
`succeeded`; `failed` с safe error; neutral `skipped` с disabled/unsupported
reason. `reused` — provenance success, не новый запуск executable. Internal
dependency wait и superseded не выдумывают success; их внешний projection
уточняется при API реализации, без дополнительных продуктовых действий.

Recovery восстанавливает недоставленный pending/uncommitted dispatch из durable
state, не перепоставляет persisted failed steps и не выполняет committed success.
Deleted/stale target не получает late apply; old-attempt delivery fencing-ится.
Duplicate terminal delivery — no-op. Successful probe retry атомарно создаёт
первый необходимый fingerprint intent, не перепоставляя успешные siblings.
Terminal batch освобождает holds, не стирая successful item results.

## 5. SHA identity, provenance и lifecycle

`INSERT ... ON CONFLICT DO NOTHING`, затем **отдельный SELECT** canonical row
при READ COMMITTED: один CTE snapshot может не увидеть победивший concurrent
insert. Проверить size canonical row; mismatch — corruption/conflict. Mtime
принадлежит location/work, не shared identity. NULL не объединяет locations.
Ошибки per-location attempts не загрязняют shared successes. Reuse сохраняет
исходный version/time/provenance, stale digest не участвует в lookup.

FK: root → locations CASCADE только inventory; location → variant RESTRICT;
active source targets/variant/tool holds нельзя потерять cascade-ом. Provenance
UUID не FK на очищаемую successful operation. Snapshot UUID сам по себе не hold.
Legacy hold columns сохраняются для v1. V2 normalized read holds защищают только
реально запускаемые tools: scan — FFmpeg, fingerprint — fpcalc, hash — ни одного.
Reuse не требует executable/version query/hold. Retry восстанавливает нужные
holds вместе с durable enqueue; terminal transition/releases atomic.

Cleanup no-SHA variant проверяет **все существующие** inbound references:
нет locations, current successful-result consumers, live read holds или иных
domain refs. SHA orphan остаётся cache. Сейчас track_source/matching/artwork
из DBML не существуют; при их появлении cleanup должен учитывать их, не
применять CASCADE как неявное решение lifecycle. Предпочтителен targeted cleanup,
не глобальный sweep на каждом apply. Не сохранять старый result у changed
location как current или как новую историю.

## 6. Общий lock ordering

Менять все participating boundaries согласованно, включая legacy paths.
Порядок: shared tools-move coordination gate для tool-dependent admission
(exclusive для move admission/switch) → package active-selection locks в
стабильном порядке → roots → locations/work → installations → operations/steps
→ digest/variants/results → writes/InsertTx. UUID sets сортируются.
Compatible shared/key-share holds для readers, exclusive mutation для delete.
SHA-only path не берёт tools gate. Под gate проверять durable active move.

Не брать operation row до root, если competing mutation берёт root → operation.
Terminal release не должен затем захватывать более раннюю lock category.
Filesystem execution вне DB tx: durable holds защищают tool lifetime, short locks
— admission/state. Shared gate не сериализует unrelated readers.

Blast radius: scan start/apply/retry/recovery; v1/v2 analysis start/apply/fail/
retry/recovery; root update/delete; tool activation/delete/move; tools-root
settings mutation; common transitions. Убрать global operation table lock только
после приведения всех путей к этому порядку. Query barriers должны доказать
progress unrelated roots, не только отсутствие constraint failure.

## 7. fpcalc transport

Managed argv: `-json -- ABSOLUTE_SERVER_SOURCE_PATH`, без shell, PATH, client
paths/version или `-length`. Server-resolved pathname + before/after stat
соответствует owner trusted-filesystem contract. Descriptor-only fpcalc не
требуется и не является blocker. CLI не предоставляет fd/AVOption whitelist;
absolute path **не блокирует secondary local/network references**, sandbox или
single-primary-file guarantee не заявляются. Новый sandbox/demuxer policy не
вводится. SHA идентифицирует primary source bytes, не внешние зависимости.

Nonzero exit/empty/malformed output не success. Bounded capture, cancellation,
reap; compressed algorithm сохраняется с namespace, header validation не
выдаётся за full payload validation. Reported duration — stream/container
duration, не processed fingerprint length. Actual Chromaprint version и build
library identifiers сохраняются из `-version`. Проверенные upstream сведения:
[техническая записка](../../reports/plan07-fpcalc-upstream-notes.md).

## 8. Migration chain / rollback / legacy compatibility

Архитектор статически изучил все existing up/down пары: bootstrap (20260927),
setup_manager (28), persistence_contract (29), transition_invariants (30),
operation_targets (20261001), installation_contract (02), source_inventory (03),
configured_path_platform (04), source_media_variant (05). Это repository chain,
не migration ledger пользовательской БД. Existing media_variant down удаляет
analysis domain и не подходит как down расширения. Старые migrations не менять.

Новая forward migration сохраняет populated legacy succeeded/failed/queued/
running rows, UUIDs/FKs/snapshots, без source I/O/jobs/backfill. Legacy absent
steps не становятся сегодняшним disabled/success. Новый down должен сохранять
представимые legacy rows и ссылки. Выбран строгий preflight refusal при любых
v2 artifacts по разделу 9, до частичных изменений: не fabricated probe и не
молчаливый destructive downgrade. Проверки populated legacy upgrade/down
реализуются в шаге 2.

## 9. Конкретизация физических контрактов после первого review

Ниже конкретизированы первоначальные proposals. Эти контракты имеют приоритет
над кратким описанием таблиц выше; alternatives не передаются исполнителю.

### Operation shape и exclusion

Добавить `source_contract_version smallint NULL` (source kinds: 1/2, остальные
NULL), `source_analysis_mode text NULL` (v2 analysis: batch/single_step),
`target_work_id uuid NULL REFERENCES source_analysis_work(id) ON DELETE SET NULL`,
`target_step text NULL` (sha256/probe/fingerprint), `tools_read_required boolean
NOT NULL DEFAULT false`. Source contract version совпадает с snapshot version;
CHECK NULL-safe, отсутствующий JSON key не проходит через UNKNOWN.

V1 analysis сохраняет legacy shape/holds, новые mode/work/step NULL. Scan
содержит active root, location/work/step/mode NULL. V2 batch содержит active
root, location/work/target_step всегда NULL. V2 single-step active требует
root/location/work/step; live targets после terminal nullable. V2 analysis не
использует legacy installation/variant hold columns или mutation installation.
Принадлежность work/location/root проверяется admission и constraint trigger.

`operation_tool_read_hold`: PK `(operation_id,installation_id)`, оба NOT NULL
FK RESTRICT. Deferred constraint trigger проверяет для committed active v2
`tools_read_required = EXISTS(tool holds)`. Terminal удаляет holds атомарно;
terminal flag может сохранять факт исполнения. Legacy source flag backfill true.
Source branch tools-move exclusion учитывает **только tools_read_required**,
без fallback, втягивающего hash-only operation. Existing install/move branches
сохраняются. Per-root active-operation unique index действует для всех source
operations. Hash-only retry не зависит от tools move.

### Work/step и delivery

Work: `id uuid PK`, `location_id uuid NOT NULL UNIQUE`, `source_root_id uuid NOT
NULL`, immutable paths/stat/sha_enabled, `origin_scan_operation_id uuid NOT NULL`
как provenance **без FK**, created_at. Добавить source_location UNIQUE
`(id,source_root_id)`; work composite FK `(location_id,source_root_id)` CASCADE.
Changed identity получает новый work UUID; unchanged сохраняет work. Legacy
locations не получают migration backfill.

Step: PK `(work_id,step)`, work FK CASCADE; state из перечисленных выше,
`step_attempt integer >=0`, safe_error/skip_reason, updated_at. Execution triple:
`execution_operation_id uuid`, `execution_operation_attempt integer`,
`execution_job_id bigint` — все NULL либо все заполнены, attempts >0;
queued/running требуют triple. Failed требует непустой safe_error, другие
states NULL error; skipped требует reason. Отдельные nullable RESTRICT FKs:
`success_sha_variant_id`, `success_probe_variant_id`,
`success_fingerprint_result_id` разрешены только соответствующему step.
Succeeded требует соответствующий result; queued/running/failed могут удерживать
previous success. Last-operation provenance UUID без FK. Pending может ожидать
successful probe и не означает fingerprint success/failure.

`operation_source_work_hold`: PK `(operation_id,work_id)`, оба FK RESTRICT.
Step execution pair `(execution_operation_id,work_id)` ссылается на этот hold.
Admission проверяет root membership; hold не позволяет потерять active batch
target через location/work CASCADE. Terminal сначала очищает execution triples,
затем holds, оставляя successful consumers. Operation cleanup не удаляет work.

Выбран **один orchestration job на batch**, без child jobs. Authoritative
delivery — operation.river_job_id. Claim/apply проверяет current work/stat,
operation ID/attempt/job ID и captured step_attempt; claim увеличивает последний.
Each item apply отдельно. Recovery атомарно меняет потерянную delivery/job,
operation attempt и fences только незавершённых owned steps; persisted failures
и committed successes не перепоставляются. Old delivery не проходит fence.

### Immutable fingerprint selection и SHA promotion

Fingerprint result имеет UUID PK, actual version/banner/algorithm namespace/ID
(0..255)/nonempty fingerprint, finite nonnegative reported duration, time,
provenance UUID без FK, parser contract version >0, UNIQUE `(id,fpcalc_version)`.
**Variant FK отсутствует:** consumers — selected fingerprint step и SHA cache.
`media_fingerprint_cache`: PK `(source_sha256,fpcalc_version)`, SHA FK RESTRICT
на unique media_variant digest; composite `(result_id,fpcalc_version)` FK
RESTRICT на result. Immutable cache: first committed success wins.

Explicit successful rerun создаёт новый immutable result и переключает только
selection данного work. Failure сохраняет прежний success. Same-version rerun
не конфликтует с UNIQUE result: existing SHA/version cache не перезаписывается.
Это не новая кнопка и не запрет existing action. No-SHA old result очищается
после потери consumers, SHA cache сохраняет result без locations.

При успешном hash retry digest-less work с probe/fingerprint successes:
revalidate fence → insert/select canonical SHA и проверить size → обновить
location media identity и SHA selection → **сохранить прежние probe/fingerprint
selection** → заполнить отсутствующие canonical probe/cache successes с исходной
provenance → cleanup только реально unreferenced rows. Существующие canonical
successes не заменять. Конкурентный canonical probe заполняется под row lock
только если отсутствует; собственный executed result проигравшего остаётся
отдельным probe-bearing row, когда нужен его selected consumer. Inspector v2
читает selected step results, legacy fallback остаётся прежним. Promotion не
перенаправляет чужие consumers и не запускает siblings.

### Handoff и liveness

Scan apply создаёт только durable pending intent, без fpcalc selection под
root lock. Отдельное admission: shared tools gate → shared fpcalc active-selection
lock (activation exclusive) → root/work → installation → operation/steps →
cache/results → holds + batch + InsertTx. UUID sets sorted. Только cache misses
нуждаются в actual executable hold. Отсутствующий/непригодный tool даёт failed
fingerprint, не откат probe/SHA. Active move оставляет pending, не failure.

Dispatch запускается после successful scan commit, successful prerequisite
retry, terminal source operation и terminal tools move; startup reconciliation
также независимо от origin operation ищет pending intents. Все вызовы
идемпотентны. Crash между publication/admission восстанавливается из work/step;
failed steps не входят в automatic admission. Event wiring и recovery входят
в шаги 3/5, без periodic scan/failed-step retry или новой настройки.

### Rollback preflight

Добавить variant `result_contract_version smallint NOT NULL DEFAULT 1 CHECK
IN (1,2)`: новые v2 rows пишутся с 2, legacy не перелабеливать. Down **до любых
изменений** отказывает при source operation v2 (включая terminal), work/step,
normalized tool/work hold, fingerprint result/cache, variant v2 или SHA/provenance
или partial/zero-audio probe, v2 candidate payload, unfinished River kinds
`scan_source_v2`/`analyze_source_v2` (state не completed/cancelled/discarded).
Ничего не удалять автоматически для разрешения down. При отсутствии artifacts
удалить только добавленную схему и вернуть constraints; legacy UUIDs/links/
snapshots/states сохраняются. Проверить каждый refusal и неизменность ledger.

## 10. Проверки и кодовые точки

Source scan/probe/parser; candidate apply/reconciliation; media_variant partial
model/cleanup; analysis enqueue/retry/recovery; jobs dispatch/reconcile;
setup_manager tool locks; typed settings registry; inspector REPEATABLE READ;
Huma/OpenAPI/Orval штатным pipeline.

Обязательные сценарии: legacy populated upgrade/down и queued v1 execution;
known SHA audio/no-audio reuse с original provenance; hash failure + probe success;
probe failure + SHA success; enabled/disabled/transitions/no backfill;
unchanged probe_error → audio; failed traversal/no partial publication;
crash around scan commit/dispatch/step apply; duplicate/old attempt delivery;
step-only retry и rerun preservation; concurrent canonical insert; оба порядка
move/delete/root/retry; unrelated-root progress; cache/orphan/refs cleanup;
real managed tools/argv/provenance; consistent inspector при apply/unlink.

Изучены root/local AGENTS, весь design/DBML, план 07, done 03–06 и relevant done
02, future source-inventory plan, audits 2026-10-01/02, plan06 reports, README,
app-design DESIGN/product/flows/screens/evidence. Codegraph использован первым
для code exploration. SQL — источник физической схемы. Ни статический review,
ни historical reports не являются свежим runtime evidence. Baseline `task verify`
включая cache-free PostgreSQL gate прошёл; **новая схема ещё не реализована и
не проверена**. Локальный gate — только task verify, platform matrix — CI.
