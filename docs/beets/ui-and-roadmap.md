# RFC: UI вокруг медиатеки и необязательной интеграции с beets

Статус: **product/UI blueprint**. UI строится вокруг MusicEnreachment, а не вокруг сущностей beets или CLI.

## 1. UX-принцип

Пользователь должен видеть три разные истины:

1. **Что обнаружено и проверено**: source/provenance/intake.
2. **Что решено человеком или автоматикой**: identity и metadata revision.
3. **Что материализовано**: publication и, если когда-либо будет включён адаптер, optional beets projection.

Нельзя показывать единый статус «Импортировано», который скрывает partial success, provisional metadata или unknown external outcome.

## 2. Что сохранить из текущего UI

Текущая структура соответствует продукту и не должна заменяться beets-подобным CRUD:

- **Медиатека / Исполнители**: что есть в управляемой медиатеке;
- **Альбомы**: релизы исполнителя;
- **Треки**: строки с независимыми измерениями состояния;
- **Инспектор трека**: source evidence, provider attempts, candidates, revisions, events;
- **Настройки**: provider/runtime policy.

Основа: `DESIGN.md:54`, `frontend/src/components/LibraryCatalog.tsx:36`, `frontend/src/screens/TrackDetail.tsx:35`.

Нужно дополнить существующий UI, а не создавать параллельный интерфейс управления beets.

## 3. Информационная архитектура

```text
Медиатека
  Исполнители
    Альбомы
      Треки
        Инспектор

Операции
  Пакеты импорта
    Пакет
      Задания / исключения
        Решение по элементу

Настройки
  Providers
  Intake policy
  Optional integration status
```

Новые экраны верхнего уровня нужны только для операционного процесса. Catalog остаётся library projection.

## 4. Новые экраны

### 4.1 Пакеты импорта

Показывает:

- источник: Lidarr, reconciliation scan, manual directory snapshot;
- policy/config revision;
- started/updated timestamps;
- точные раздельные счётчики;
- batch state;
- operator;
- быстрые фильтры по exceptions.

Counts должны разделять:

```text
not_started
active
awaiting_decision
succeeded_final
succeeded_provisional
skipped
cancelled
failed_retryable
failed_terminal
blocked
outcome_unknown
```

### 4.2 Детали пакета

Содержит:

- immutable input snapshot;
- per-item status table;
- active/awaiting decision/failed/unknown filters;
- попытки и last checkpoint;
- batch-level actions только над безопасным subset;
- receipt для partial bulk action.

### 4.3 Decision surface

Переиспользуемая страница или drawer, открываемый из batch и Track Inspector:

- source tags/hash/path;
- normalized candidate diff;
- provider provenance и candidate-set revision;
- оценка и объяснимые компоненты;
- allowed actions;
- impact preview;
- версию задания и состояние устаревания.

Он моделирует MusicEnreachment decision, а не beets prompt.

### 4.4 Integration status

Появляется только после появления реального адаптера:

- selected capability;
- beets/runtime/plugin versions;
- last success;
- queue depth;
- retry/reconciliation counts;
- projection rebuild readiness;
- кнопка contract test для оператора, если безопасна.

## 5. Полный lifecycle

| Phase | States | Owner | User actions |
|---|---|---|---|
| Discovery | `discovering`, `discovered`, `empty`, `discovery_failed` | scanner/job | retry, inspect rejected, remove unstarted |
| Admission | `draft`, `queued`, `starting`, `cancel_requested` | batch scheduler | start, cancel before work, clone snapshot |
| Intake | `running`, `source_missing`, `source_changed`, `unsupported`, `quarantined` | intake job | retry after fix, archive terminal failure |
| Identification | `identifying`, `provider_retry_wait`, `identified`, `no_match`, `ambiguous`, `provider_unavailable` | matching job | retry, review, defer |
| Candidate decision | `awaiting_candidate_decision`, `submitting`, `stale`, `rejected`, `applied` | decision service | select, reject all, defer |
| Duplicate decision | `duplicate_checking`, `awaiting_duplicate_decision`, `duplicate_stale`, `resolved`, `conflict` | duplicate service | keep, skip, reviewed replacement/merge |
| Metadata review | `awaiting_metadata_review`, `editing`, `edit_conflict`, `approved`, `provisional` | revision service | save draft, approve, defer |
| Publication | `queued`, `publishing`, `published_provisional`, `published_final`, `publication_failed` | ME worker | retry/recover according to ownership |
| Optional projection | `projection_queued`, `projecting`, `pending_confirmation`, `projected`, `failed`, `unknown`, `reconciling` | adapter/reconciler | retry safe failure, reconcile unknown |
| Completion | `completed`, `completed_with_exceptions`, `cancelled`, `failed`, `recovery_required` | batch aggregate | inspect exceptions, clone failures |

## 6. Provisional-first policy

Текущий worker может публиковать безопасную резервную копию до завершения provider review, а затем создавать final publication (`src/music_ingest/processing/worker.py:273`, `src/music_ingest/processing/worker.py:538`).

UI должен честно показывать:

- `visible_provisional`: файл доступен Navidrome, но identity/metadata review не финализирован;
- `visible_final`: publication соответствует approved final revision;
- `withheld`: publication запрещена safety policy;
- `superseded`: историческая версия.

Вариант «hold until approval» является другой product policy и требует отдельного ADR. Он не должен незаметно подразумеваться дизайном UI.

## 7. Versioned decisions

Каждая consequential command включает:

```text
batch_id
job_id
job_version
decision_task_id
decision_task_version
candidate_or_duplicate_set_revision
base_metadata_revision
idempotency_key
```

Сервер атомарно проверяет версии. Stale command:

- не создаёт revision/job;
- возвращает typed conflict с актуальной task/revision;
- UI показывает причину и refreshed diff;
- пользователь подтверждает заново.

Это обязательно для candidate selection, duplicate action, metadata save, retry и cancellation.

## 8. Candidate review

Показывать:

- provider и snapshot timestamp;
- recording/release MBID;
- title, artist, album, album artist;
- track/disc mapping;
- duration difference;
- missing/unmatched tracks;
- score components, а не только один процент;
- current source tags и proposed final tags;
- recommendation: strong/medium/low/no match;
- actions: select, reject all, manual search/ID, defer.

Если future beets scoring участвует, его output нормализуется в те же поля. UI не знает, был score получен текущим matcher или optional adapter.

## 9. Duplicate taxonomy

Не использовать один generic duplicate dialog.

| Тип | Evidence | Безопасные действия по умолчанию |
|---|---|---|
| Exact source/content duplicate | Same hash/source generation | skip/link existing record |
| Existing managed identity | Same approved MBID/record mapping | attach source, keep current publication, reviewed replace |
| Destination path collision | Calculated path already exists | inspect owner; replace только для service-owned target |
| Candidate identity overlap | Candidate points to occupied MBID | block and resolve identity conflict |
| Unverified possible duplicate | Similar tags/duration only | keep separate or defer; no destructive action |

Перед заменой или объединением UI показывает:

- target record/publication/path;
- managed/unmanaged ownership;
- affected files, metadata, artwork и history;
- reversibility;
- new revision/publication that will be created.

beets duplicate actions `remove`, `merge`, `upgrade` напрямую не выставляются, потому что могут удалять файлы и опираются на его Library model ([duplicate config](https://beets.readthedocs.io/en/latest/reference/config.html#duplicate-action)).

## 10. Manual metadata editing

Сохраняется текущая модель Original / Analyzed / Final.

Правила:

- edit использует base revision;
- save создаёт новую immutable final revision;
- queued projection старой revision становится `superseded`;
- running external mutation после commit boundary переходит в reconciliation;
- изменение, инициированное beets, если оно когда-либо разрешено, поступает как review proposal, а не canonical mutation.

## 11. Cancellation

Отмена является запросом, а не обещанием немедленного undo.

### Checkpoints

1. До admission: batch/item становится `cancelled`.
2. До начала локальной mutation: будущая работа не стартует.
3. Во время безопасного cancellable analysis: `cancel_requested`, затем `cancelled`.
4. После начала staging/publication: завершить или безопасно cleanup согласно текущему service contract.
5. После отправки optional external command до acknowledgement: `projection_unknown`, затем reconciliation.

Нельзя показывать `cancelled`, если beets мог применить изменение.

## 12. Error model

Generic notice из текущего `api/client.ts` недостаточен для operational UI.

API error DTO должен содержать:

```text
code
category: input | provider | conflict | policy | filesystem | integration | unknown_outcome
retryability
human_message
operator_detail
job_id / attempt_id
current_version
next_allowed_actions[]
```

UI различает malformed source, provider outage, source drift, auth/config fault, write failure и external acknowledgement uncertainty.

## 13. Progress и delivery

Текущий интервал опроса до 30 секунд (`frontend/src/app/useAppController.ts:364`) не может быть correctness contract.

Правильная последовательность:

1. Snapshot endpoints являются authoritative.
2. При reconnect/navigation UI заново загружает batch/job snapshot.
3. Polling допустим как transport optimization, но не источник state inference.
4. SSE/WebSocket добавляется только при измеримой необходимости; event stream всегда восстанавливается snapshot.

## 14. API sketch

```text
POST /api/import-batches
GET  /api/import-batches
GET  /api/import-batches/{batch_id}
POST /api/import-batches/{batch_id}/cancel

GET  /api/import-jobs/{job_id}
POST /api/import-jobs/{job_id}/retry
POST /api/import-jobs/{job_id}/cancel

GET  /api/decision-tasks/{task_id}
POST /api/decision-tasks/{task_id}/candidate
POST /api/decision-tasks/{task_id}/duplicate
POST /api/decision-tasks/{task_id}/defer

GET  /api/integrations/beets/status
POST /api/integrations/beets/reconcile/{command_id}
```

Routes для beets не создаются, пока adapter отсутствует. Existing library routes продолжают обслуживать catalog/inspector.

## 15. Acceptance scenarios

### Stale candidate

Reviewer открывает candidate set revision 7. Provider retry создаёт revision 8. Submit revision 7 получает conflict; metadata и jobs не меняются; UI показывает revision 8.

### Duplicate race

Два item указывают на один managed target. Решение первого invalidates/recomputes task второго. Второе действие по старому preview отклоняется.

### Manual edit supersedes work

Revision 12 queued. Пользователь сохраняет revision 13. Job 12 получает `superseded`; только 13 может стать final. Если внешняя mutation 12 уже могла завершиться, запускается reconciliation.

### Source drift

Файл изменился после discovery. Старый source/job/candidate становится stale, новая source generation проходит intake отдельно.

### Partial batch

Шесть final, один provisional, один deferred duplicate, один quarantined, один provider retry. Batch не становится plain `completed`; categories видимы и фильтруемы.

### Adapter offline

Optional projection не отвечает, worker перезапускается. Сохраняются command key, attempt и checkpoint. UI показывает retryable или unknown; blind reimport запрещён.

### Provisional publication

Пользователь открывает track до provider resolution. Catalog/inspector явно показывают provisional и ссылку на active review job.

## 16. Roadmap

### Phase 0. Не внедрять beets, закрыть продуктовые пробелы

- Вынести authoritative job snapshots в API.
- Добавить attempts/retry history в inspector.
- Показать current publication path/hash/revision.
- Подключить уже существующие recovery/conflict backend actions.
- Добавить state filters: review, quarantine, stale/missing publication.

Exit: текущая модель полностью наблюдаема без beets.

### Phase 1. Durable batch/decision UX

- `ImportBatch` и per-item job aggregate.
- Versioned decision tasks.
- Typed conflict/error DTOs.
- Batch list/detail и exception filters.
- Provisional/final labels.
- Exact partial-success aggregation.

Exit: stale submissions, перезапуск и частичные пакеты покрыты сценариями приёмки.

### Phase 2. Product benchmark beets

Выбрать ровно один эксперимент:

1. directory-drop discovery/grouping;
2. matching benchmark.

Подготовить fixtures и baseline. Эксперимент read-only, plugins disabled, no publication. Результат: отчёт с метриками и решение Go/No-Go, а не production adapter.

Exit: измеримое улучшение и документированный дефицит возможностей либо отказ от зависимости.

### Phase 3. Изолированный adapter, только при Go

- Version-pinned subprocess wrapper.
- Per-job `BEETSDIR`.
- Typed result artifact.
- Outbox/idempotency/checkpoints.
- Timeout/process cleanup.
- Crash/duplicate-delivery tests.
- Operator status screen.

Exit: no source mutation; unknown outcome reconciles; deleting temp SQLite loses no canonical state.

### Phase 4. Необязательная одноразовая производная проекция

Только если read-only adapter недостаточен и long-lived Library даёт доказанную пользу:

- single writer;
- projection links;
- deterministic rebuild;
- plugin audit;
- drift UI;
- backup/restore exercise.

Exit: delete-SQLite rebuild дважды даёт эквивалентный нормализованный каталог.

### Phase 5. Service promotion

Долгоживущий управляющий сервис допускается только после измерений:

- subprocess startup dominates latency/throughput;
- required capability действительно stateful;
- service operations/recovery проще, чем per-job mode;
- compatibility and security ownership принято командой.

## 17. Метрики

### Product

- time-to-review;
- доля auto-approved против manual review;
- stale/conflict rate;
- количество помещённых в карантин элементов и возраст нерешённых случаев;
- provisional-to-final latency;
- successful recovery without data loss.

### Benchmark beets

- album grouping precision/recall;
- candidate top-1/top-3 accuracy;
- false confident match rate;
- operator corrections per album;
- runtime and dependency cost;
- deterministic repeatability.

### Reliability

- duplicate command deliveries без duplicate external mutation;
- unknown outcomes reconciled;
- projection rebuild success;
- zero writes to incoming root;
- zero canonical state reconstructed from beets SQLite.

## 18. Идеи дальнейшего развития

После завершения core workflow, независимо от beets:

- PostgreSQL-backed advanced query language, вдохновлённый beets syntax;
- saved smart views: «нужна проверка», «publication stale», «source disappeared»;
- album-level review с track mapping;
- explainable scoring comparison;
- artwork endpoint и controlled thumbnails;
- event timeline с attempts/checkpoints;
- dry-run import manifest diff;
- plugin-like capability registry на application-owned typed interfaces;
- export/import policy bundles для воспроизводимой обработки;
- Navidrome sync observability без переноса playback в MusicEnreachment.

Playback и streaming не следует включать в beets integration: существующая граница с Navidrome дешевле и безопаснее, а опыт beets ecosystem показывает, что streaming/Range/authentication является отдельным продуктом.

## 19. Итог для UI

UI вокруг логики beets построить можно, но лучший UI для этого проекта не должен выглядеть как графическая версия `beet import`. Он должен быть интерфейсом к доказательному, версионированному и восстанавливаемому workflow MusicEnreachment. Тогда beets можно подключить или убрать без переписывания экранов, domain state и пользовательских решений.
