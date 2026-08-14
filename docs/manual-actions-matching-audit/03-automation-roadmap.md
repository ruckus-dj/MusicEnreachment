# Переписанный roadmap: конкретно для recording, album edition и нескольких source

## Решение, которое будем реализовывать

Будущая цепочка должна быть такой:

```text
immutable SourceRecord
  -> recording resolver (AcoustID OR text/ISRC fallback)
  -> verified recording association
  -> ReleaseResolutionGroup (все tracks наблюдаемого album)
  -> common release edition decision
  -> effective source decision (один лучший файл)
  -> existing staged atomic publication replacement
```

`LibraryRecord` остаётся identity recording/composition. `SourceRecord` остаётся неизменяемой наблюдаемой версией файла. `ReleaseResolutionGroup` — новая, отдельная aggregate для ответа на вопрос «какая edition объясняет этот набор дорожек?». Путь может быть evidence для grouping/debug, но никогда не является стабильным identity.

## P0-A. Устранить неверную зависимость recording от release

### Проблема

`record-3cad…` показывает единственный AcoustID 0.997, но `associate_automatic()` отказывает, потому что ищет уже успешный `musicbrainzmatch` candidate с тем же track ID. Ambiguous CD/Digital release не должен аннулировать recording evidence.

### Выполнено: Noize MC album-compatible AcoustID selection

Исправлена потеря уже существующего album-compatible winner в worker: когда ровно один AcoustID recording разрешается в `AUTO_SELECTED` MusicBrainz match для source album, именно его recording score теперь передаётся в automatic association. Раньше worker находил этот winner, но повторно считал все равные AcoustID scores и снова оставлял запись в review.

Добавлен regression test с исходными Noize tags и verbatim AcoustID response из текущего snapshot `048562094731e7993eeb78a4cae77adb552c8a54f01b2af08d43e2902efee3a0`: Vol.1 (`47d13484…`) выбирается, Vol.2 остаётся `NEEDS_REVIEW`. Это первый небольшой срез P0-A; direct recording verification и CD/Digital canonicalization остаются следующими задачами.

Также добавлен automatic-association guard: source с тем же SHA-256 не будет автоматически перемещён в aggregate с другим recording MBID. Вместо молчаливого split он остаётся reviewable с причиной conflict; ручной override не затронут.

### Изменение

Разделить результат на append-only `RecordingDecision` и `ReleaseDecision`.

- `RecordingDecision.auto_confirmed` допустим, когда один AcoustID recording выше порога и margin, или есть trusted MBID/ISRC, а прямой MusicBrainz lookup **этого recording MBID** подтверждает существование и не обнаруживает hard conflict artist/title/duration.
- `ReleaseDecision` не меняет recording decision. Если edition ambiguous, record имеет подтверждённый recording и `release_resolution=pending`, а не ложный `unmatched`.
- Исключение: если direct MBID verification unavailable/stale или text/duration конфликтуют, recording остаётся reviewable.

### Тесты до реализации

- один AcoustID 0.997 + два эквивалентных release → recording confirmed, release pending;
- два AcoustID выше threshold → no recording auto-confirm;
- один AcoustID + direct MBID mismatch title/duration → review;
- release selection никогда не стирает ранее confirmed recording и наоборот.

## P0-B. Сделать судьбу каждого source наблюдаемой

### Проблема

Для ожидаемого FLAC из `downloads` у Noize live record показывает только incoming FLAC. Текущая UI скрывает путь в selector и не показывает association/publication timeline; поэтому невозможно отличить «не ingested» от «на другом record».

### Изменение

1. Добавить read-only source audit view/API query по source path/SHA-256, возвращающий current record, assignment history, consolidation alias, job/intake state и eligibility reason. Никакой мутации source.
2. В `Файл-источник` заменить basename-only native select на cards/радиогруппу в стиле `CandidateReview`:
   - полный путь, codec/size/hash, origin/state;
   - quality/eligibility reason и «текущий effective source»;
   - к какой current/superseded publication этот source привёл;
   - явное confirmation before existing effective-source POST.
3. Показать append-only timeline: source reassigned → source selected → staged → exposed → old publication superseded/new current.

### Существующая безопасная база

Backend уже принимает effective `source_id`, reevaluates policy и queues selection refresh. Worker уже публикует единственный selected source; `replace_published_audio()` делает atomic replacement, а previous publication сохраняется `superseded`. Не добавлять filesystem move/delete в incoming roots.

### Тесты до реализации

- два source разных форматов одного recording → обе provenance entries видны; один current publication;
- manual switch FLAC/MP3 → old publication superseded, new one current, source files untouched;
- source на другом record → source audit показывает association boundary;
- missing source → audit различает absent intake, reassignment и superseded version.

## P0-C. Сделать worker lease и provider call наблюдаемыми

### Проблема

Live queue показал один `musicbrainz_analysis` для Noize MC, остающийся `running` более 43 минут при текущих `timeout_seconds=10`, `retry_delay_seconds=30` и `max_attempts=3`. UI показывает только aggregate `attempt_count=8`; без latest attempt timestamps/state и heartbeat нельзя отличить работающий долгий provider call от abandoned lease. Ручное изменение job row в этой ситуации небезопасно.

### Изменение

1. Persist runtime-owned worker heartbeat: slot ID, `last_poll_started_at`, `last_poll_completed_at`, `active_job_id` и last error summary. Это наблюдение, не источник истины для job transition.
2. В read-only queue/detail API вернуть latest `JobAttemptRecord`: attempt number/state, `started_at`, `finished_at`, `failure_reason`; для `running` явно показать lease age и eligibility for reclaim.
3. Записывать provider call timeout/outcome в attempt/event evidence. Не сводить timeout, rate-limit и malformed response к generic `needs_review`.
4. При stale reclaim сохранять interrupted attempt и показывать это в UI; не requeue/delete incoming source.

### Тесты до реализации

- running job с attempt старше lease возвращает `reclaimable=true`, но UI не выполняет мутацию;
- running job без attempt помечается как inconsistent и не объявляется здоровым;
- worker heartbeat пропадает после остановки slot, а durable job/attempt history сохраняется;
- provider timeout создаёт retry/blocked evidence, различимую от matching ambiguity.

## P1. Album-level `ReleaseResolutionGroup`

### Почему это обязательно

`Хаос` и `H! VLTG3` подтверждают, что track-local release decision создаёт incoherent album: release выбран без recording или tracks выбирают разные editions. Нужна единица решения выше `LibraryRecord`.

### Модель данных

Добавить миграцией:

- `release_resolution_groups`: ID, observed album artist/title evidence, state (`observed`, `resolving`, `selected`, `needs_review`, `conflicted`), current selected release MBID as projection;
- append-only `release_resolution_members`: source/record references, observed track/disc numbers, duration, raw normalized evidence, membership version;
- append-only `release_resolution_candidates`: MB release MBID, provider snapshot/run reference, candidate track mapping and component scores;
- append-only `release_resolution_decisions`: selected MBID/equivalent set, actor, policy version, rationale, full evidence snapshot, timestamp.

Membership grouping использует album metadata + track layout + recording evidence, а не directory path. Путь сохраняется как provenance and display evidence only.

### Group resolver

1. Собрать candidate releases из confirmed recordings и text fallback candidates.
2. Отбросить release, который не содержит required recording/compatible track position.
3. Оценить edition по всему набору: artist/album, track/disc mapping, number of observed tracks explained, duration, version labels, country/date/type, barcode/catalog number, ISRC.
4. Выбрать release, только если он единственный compatible либо top-margin проходит calibration.
5. Если CD/Digital candidates одинаковы по всем полям, записать `equivalent_release_set` и применить canonical policy с deterministic MBID tie-break. Пользователь видит, что edition была нормализована; история хранит альтернативы.
6. Если хотя бы один member противоречит выбранному release, state=`conflicted`; не делать partial auto-selection.
7. Выбранная group edition порождает final metadata revisions и record-target selection refresh для всех members. Каждая managed publication остаётся отдельной и заменяется existing atomic flow.

### Обязательные acceptance tests

- 18 tracks с общим единственным compatible release → все получают одну release MBID;
- часть tracks предлагает CD, часть Digital, но track layout эквивалентен → одна canonical edition и equivalent set;
- одна дорожка `live/remix/instrumental` не входит в edition → group conflict/review, остальные не переносятся молча;
- H! VLTG3: recording из выбранного release побеждает глобальный AcoustID candidate только при full compatibility;
- повторный resolver run идемпотентен; смена edition сохраняет решения и публикации как history.

## P1-B. Release scorer на реальных edition features

Заменить текущую формулу `artist 0.4 + album 0.4 + duration 0.2` как единственный критерий. Ввести explainable components:

| Component | Поведение |
|---|---|
| recording membership | hard filter |
| observed complete/partial tracklist vs medium | основной group score |
| track/disc position and totals | strong score/contradiction |
| album artist/title | retrieval + strong score |
| duration | corroboration |
| edition country/date/type | tie-break |
| barcode/catalog number | strong edition evidence |
| version labels | conflict detection |
| top-second margin | mandatory auto-select gate |

Для Busta это позволяет использовать `1/3`, album `We Made It [Maxi Single]`, Japanese `WPCR-12973`, country and catalog information, а не ставить 100% всем promo/compilation releases.

## P2. Fallback, когда AcoustID не нашёл запись

### Проблема

Для `здоровью.нет` AcoustID `nomatch`, но source tags и внешняя MB запись достаточно информативны. MusicBrainz `malformed` сейчас визуально и семантически неотличим от «ничего не найдено».

### Изменение

1. Retryable/observable provider failure: persist HTTP status, request descriptor, response hash and parse error class; в UI отделить `malformed` от `no match`.
2. Text candidate generator: exact artist+recording title; artist+album+track; normalized/transliterated retry. Затем details lookup каждого candidate.
3. Rank recording/release with the same group resolver; only one candidate with threshold and margin auto-confirms.
4. Resolve incoming ISRC only from verified MusicBrainz candidate; сохранять в analyzed/final revision with provenance, never alter original source tags.
5. Возможный future provider: fingerprint submission/AcoustID contribution — только отдельная opt-in capability, никогда не предполагать, что `nomatch` означает неверное аудио.

## P2-B. UX, который объясняет решение

В TrackDetail показывать не просто 97%, а:

- `Recording: confirmed/pending/conflict` и evidence;
- `Album edition: group selected/pending/conflict`, selected release и equivalent candidates;
- score components, top-second margin, rejected candidates и точную reason;
- полные source paths и publication history;
- ссылку на album-resolution screen, где оператор выбирает edition один раз для всей группы.

## Порядок исполнения

1. **Сначала расследование Noize source audit**: найти ожидаемый download FLAC по SHA/path in durable state; это определит, intake или association надо чинить.
2. Implement P0-A с red tests на Anacondaz clear case.
3. Implement P0-B UI/audit and source switch history with browser E2E.
4. Implement group schema/migration and resolver in shadow mode on the five cohorts.
5. Сравнить shadow output с ручными решениями; включить auto-selection только для cohorts, где precision подтверждён.
6. Implement text fallback and provider-error diagnostics.

## Реалистичная цель

Сначала целиться не в «99% любой ценой», а в измеримые outcomes: убрать workflow leakage, автоматически закрепить verified recording, принять одну edition на complete compatible album group, и оставлять только genuine conflicts/no-evidence в review. 99% coverage допустим как последняя метрика только после shadow-mode precision evaluation по отдельным cohorts.
