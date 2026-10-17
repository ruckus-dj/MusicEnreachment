# План 12 — технический дизайн файловой публикации

**Статус: после независимого review разрешены подготовительные срезы
identity/read model, metadata/primary artist и unified reader/native removal.**
Exposure/tool mutation остаётся за отдельными техническими гейтами D09 и
execution exclusion. Review от 2026-10-17 не является runtime evidence.
Документ развивает D06–D10 плана [12](12-file-publication.md) с учётом решений
A1–A16. Он не меняет owner contract и не закрывает оставшиеся технические
гейты. При противоречии продуктовых формулировок приоритет у A1–A16 и
`docs/design/`. Всё обозначенное «предложением» должно пройти техническое review;
неизвестные требования не заполняются предположениями.

## 1. Границы, предпосылки и проверенная текущая реализация

Цель — одна актуальная управляемая публикация на позицию релиза: извлечённое аудио,
локальные теги, встроенная обложка и допустимые synced lyrics, с подтверждённым
readback, восстановлением после сбоя и без ложного статуса успеха. Одна River job
обрабатывает одну позицию со всеми её согласованными assets. Частичный успех релиза
остаётся частичным.

План 12, включая **все A1–A16**, является источником продуктовых решений.
В частности, current plan требует автоматического выбора при уверенном качестве,
manual authority по каждому полю/source/asset отдельно, независимую подготовку audio/
cover/lyrics, явные pending-изменения, removal OFF по умолчанию и отсутствие
retroactive удаления. CUE/manual segments и ReplayGain/R128 отложены. D09 ниже —
техническая проверка, не разрешение заявлять поддержку форматов либо лицензий.

На момент подготовки плана в `backend/internal/publication/` нет рабочей публикации;
таблиц/API/jobs publication нет. Есть локальные сущности и matching,
`publication_format` (`source`/`mka`), source analysis, а output reset уже имеет
durable coordinator. Эти факты требуют перепроверки перед реализацией.
В `backend/internal/jobs/move_publication.go` и
`backend/internal/jobs/install_publication.go` уже применены журналы владения и
проверки файлов; они являются локальными примерами, не готовым протоколом
публикации.

Архитектурные примеры, подтверждённые кодом:

- `backend/internal/service/source_technical_analysis.go` сохраняет распознанные
  технические данные и tags анализа; это не metadata writer/readback контракт.
- `backend/internal/jobs/move_publication.go` фиксирует operation identity,
  target и ownership witness в journal и не считает совпадение пути доказательством
  владения.
- `backend/internal/jobs/install_publication.go` пишет durable publication
  journal, проверяет conflicts и делает rollback по ownership evidence.
- Перед кодом сверить текущие `release_track`, `track_source`, operation claims,
  reset и lock flows; этот документ не утверждает, что существующие коды API,
  имена колонок или lock order уже совпадают с предложениями ниже.

## 2. Архитектурные границы и сохраняемая идентичность

Слои: API (Huma) → service → persistence; только persistence обращается к Bun/DB.
`backend/internal/publication/` предлагается как владелец filesystem planner,
staging, journal, extraction/writer orchestration и readback. Jobs владеют River
delivery/recovery и вызывают service. UI — только через API; клиент генерируется
Orval. Не добавлять env vars, auth, файловые записи в API/persistence или ручные
правки generated client.

### Предложение: стабильный ID позиции

Текущая логическая идентичность `release_track` — составной ключ
`release_id`/medium/position, без UUID; текущий `selected track_source` связан с
`local_track`, а не отдельной release position. Публикация же адресуется
позиции. Добавить UUID `release_track.id` как additive migration и использовать
его как стабильный технический identity в paths/suffix, publication refs,
jobs, locks и claims. Backfill существующих рядов один раз; новые позиции
получают новый UUID, перемещение/переименование не меняет его. Сохраняются
уникальность/семантика существующего составного ключа и все FK; план миграции
должен проверить текущие зависимости, индексы и nullable поведение.

Не использовать MBID, имена, track ID либо filesystem path как owner identity.
UUID — техническая идентичность; шаблонные переменные `ALBUM_ID`/ID позиции
разрешаются из стабильных локальных identity, а не из пути/имени. Это не новый
продуктовый выбор. `track_publication` ссылается на position UUID и гарантирует
не более одной активной записи на позицию.

### Предложенные таблицы и ответственность

Это минимальный логический контракт, не готовый SQL; точные типы/имена
колонок — на migration slice.

| Сущность | Назначение и ограничения |
| --- | --- |
| `release_publication` | Одна запись на local release: текущий шаблон/формат и активная конфигурация, cover resolution/authority, dirty/pending summary. Не дублировать весь release metadata. |
| `track_publication` | Одна запись на position UUID: source/stream selection и authority; текущий audio target/artifact identity; фактический readback, quality characteristics/policy version, состояние; отдельная lyrics selection/authority/status; metadata snapshot только в объёме, нужном для актуальной публикации и pending. **Не** хранит завершённую source provenance/history. |
| `track_publication_attempt` | Только незавершённая попытка: operation ID, attempt/fence identity, stage, frozen inputs, journal/artifact refs, warnings/errors, recovery intent. Удаляется после settle; не становится историческим журналом. |
| `publication_namespace_claim` | Уникальное занятие вычисленного target по реальной FS equivalence semantics, владельцу position UUID и epoch. Отдельно представляет конфликт с чужим файлом/каталогом и подтверждённую замену. |
| `publication_asset_cache` (предложение) | Управляемый кеш provider/local cover или synced lyrics для повторного использования/retry; отделён от актуального embedded bytes и очищается только ownership-verified путём. Не вводить без доказанной необходимости конкретной схемы. |
| `source_cleanup_item` | Незавершённая opt-in cleanup цель после валидации: candidate identity/size/mtime snapshot, список требуемых позиций/публикаций, состояние и retry warning. Нет FK на источник, который уже удалён; lifetime допускает cleanup retry после удаления link/root. |

Фактический audio provenance после settle не сохраняется (A12). Временные
execution inputs/claim допустимы для незавершённой работы; source inventory и
metadata-field provenance продолжают жить отдельно. Ошибки FK/cascade не должны
удалять файлы. Для каждого output ref требуется отдельная filesystem cleanup
операция с подтверждением ownership.

### Primary artist selection

Сохранить выбранного primary artist как persisted reference, отдельно scoped к
release и position, со ссылкой на локальную artist identity и связанную MBID
identity, если она есть. Candidate membership строится по актуальному ordered
credit list; не хранить только строку/индекс credit и не выбирать первого
артиста автоматически, когда их несколько. При одном артисте выбор разрешается
автоматически; при нескольких требуется явный выбор. Локальный ID и MBID должны
разрешаться к одной и той же выбранной artist identity, а не независимо брать
разные позиции. Если выбранный артист удалён из membership/credit list, считать
выбор invalid: шаблон с `ALBUM_PRIMARY_ARTIST*`/`TRACK_PRIMARY_ARTIST*` блокирует
публикацию и запрашивает выбор оператора. Не выбирать нового артиста молча.

## 3. Metadata, authority, preview и критерий readback

Для каждого поддерживаемого metadata field нужна typed value с тремя
состояниями: `absent` (не менять/не восстанавливать молча из source), `value`,
`explicit_empty` (для согласованных optional fields удалить target tag).
Preview строится из текущих локальных release/position значений и явных
overrides; provider/source metadata не подмешиваются writer-ом. Обязательные
release artist/title и track title проверяются согласно A1. Согласованный набор
тегов пишется заново; произвольные source tags не обещаются.

Предлагается typed registry полей и mapping contract: normalized application
field → целевые ключи → writer/readback capability → Navidrome interpretation.
Сохранить field-level provenance для расширенных A15 полей, но не спутывать его
с provenance аудиоисточника. Preview показывает намерение; active publication
показывает отдельно фактический readback, неподтверждённые поля и warnings.
Успешный вызов `SaveTags` сам по себе не является доказательством записи.
Поля без реального readback подтверждения нельзя показывать как успешно
опубликованные; capability gap блокирует заявленную поддержку и возвращается на
review, а не маскируется.

Manual authority независима для конкретного тега, выбранного audio source/stream,
cover и lyrics. Автоматическое обновление одного поля не меняет другие
authority. Ручные изменения формируют отдельные pending значения и не
переписывают auto-current; применение — явное (по полю или все pending).
При автоматической смене аудио manual lyrics с неподтверждённой совместимостью
не встраиваются: audio публикуется без них, ручной выбор сохраняется pending и
authority остаётся manual (A16).

Quality policy — чистый versioned comparator над проверенными characteristics
аудиоисточников, а не gate для metadata/asset updates:
lossless предпочтительнее lossy; lossless при одинаковых числе/назначении
каналов сравнивать bit depth, затем sample rate; lossy того же codec — bitrate;
разные lossy codecs/layouts несравнимы. Unknown не доказывает улучшение.
Current equal сохраняется; при первой публикации equal candidates разрешаются
стабильным техническим порядком (UUID позиции/источника только как tie-break,
не как качество). Один проверенный кандидат можно публиковать с неполными
характеристиками; несколько несравнимых требуют ручного выбора. Автоматическая
замена требует доказанного улучшения. Comparator должен иметь результаты
better/equal/worse/incomparable/unknown и тест-векторы; никакого score/default
порога не добавлять. Если audio source/stream не меняется, metadata/cover/lyrics
update не сравнивается по quality и не блокируется равенством audio quality.

## 4. Source, audio, assets и инструменты

Публикационный source выбирается на position, хотя существующий link может
принадлежать общему `local_track`/variant. Claim фиксирует source, location,
audio stream, наблюдаемые size/mtime, technical-analysis identity и settings
snapshot для незавершённой попытки; это execution input, не historical source
provenance. При изменении size/mtime запускается переанализ; пока он идёт,
допускаются другие проверенные candidates, прежняя publication не понижается.
Успех переанализа должен повторно валидировать выбранный input.

Ноль audio streams — ошибка; ровно один выбирается автоматически; несколько
показываются для явного выбора с языком, title, codec и channels. Unsupported
format/layout — явная ошибка, без fallback на другой stream/транскодирование.
CUE/manual segment boundary и album+CUE не публикуются в текущем scope: сообщить
явно, не выдавать весь исходный stream за позицию. Никакого source modification.

FFmpeg подготавливает чистую аудиодорожку, remux или разрешённое A8/A15
transcode. Не passthrough metadata attachments/chapters/неаудио streams. MKA —
без re-encode, несовместимый codec — ошибка. M4A: AAC/ALAC копировать без
re-encode; иной lossless → ALAC, иной lossy → AAC. AAC: MP3 использует measured
stream bitrate (VBR — average); другие lossy/unknown bitrate: stereo 320,
mono 160 kbit/s. Multichannel AAC transcode не поддержан. Если ALAC требует
lossy reduction/уменьшения разрядности/сведения каналов — ошибка. Это не
утверждает больше форматов, чем проверено tool capability.

Единый `deluan/go-taglib` reader/writer предлагается pin-ить на commit
`32d7614f30ecf47d451cd801e0cfb93bf9739db4`; WASM 2.3.2. Это техническое
предложение, не owner-approved pin; подтвердить совместимость в narrow tool
slice. Удалить
`go-mkvparse` и native capture trees целиком — без legacy blobs, API, UI или
compatibility layer (A10). Удаление `go-mkvparse` и native capture model — явно
согласованное полное удаление A10, не legacy compatibility task. Оно не
разрешает destructive rollback живых publication refs/artifacts: для них
действуют правила безопасного down migration в разделе 11. Сначала проверить действительный supported-format
scope, реальные read/write/readback, Navidrome field mapping и build/test matrix.
Не обещать распространение/лицензионную чистоту: D09 external research пока не
runtime-verified, лицензии не являются legal approval. Обновить notices только
после проверки действительных транзитивных лицензий и packaging.

### Artwork и lyrics

Одна cover на релиз: provider → embedded source cover со стабильным порядком
position; manual selection блокирует автоматическую смену до явного возврата.
Только embedding, без sidecar. Cover failure не блокирует audio: прежняя cover
или отсутствие cover остаётся, UI показывает warning и retry. Несовместимость
writer/container embedding — вернуть на согласование, не вводить sidecar.

Только одна synced lyrics версия: сначала synced lyrics выбранного audio source,
иначе точный LRCLIB lookup по title/artist/album/duration; нет результата —
публиковать без lyrics. Никакого авто-поиска альтернативных LRCLIB matches,
переноса lyrics с других источников или auto-retiming. Unsynced не публиковать.
Ручные поиск/просмотр/выбор/удаление доступны UI; ручная authority — отдельна.
Проверить фактическую поддержку synced embedded lyrics в Navidrome; sidecar
допустим только по условному решению A11 и не становится fallback без проверки.
Provider/LRCLIB failures — warning/retry, не audio failure.

## 5. Планирование путей и namespace ownership

Использовать подтверждённый A15 default template буквально:

```text
{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}
```

Template configurable relative path. `EXT` означает фактический output
container; metadata/template edit сама не перемещает существующий output.
Неопределённые template/security details должны сверяться с последним plan12
решением; никакого path traversal/absolute path. Явно общий каталог допускается
только явно выбранным template; непреднамеренное столкновение каталогов разных
релизов — конфликт. При collision добавляется стабильный position suffix,
который закрепляется, существующее имя не меняется, при новом конфликте suffix
удлиняется. Filesystem case/Unicode equivalence определяется существующим probe,
а не PostgreSQL collation. Чужой файл можно заменить только после явного UI
подтверждения; файл другой публикации не перезаписывается. Внешне изменённый
owned file — конфликт. Sanitize/length limits и unresolved cross-release shared
directory behavior — implementation gate, не здесь выдуманный default.

Namespace claim резервирует вычисленные destinations и parent namespace в
соответствии с пробой FS semantics; уникальность проверять под transaction/claim
и на реальной FS. Файловая проверка на этапе exposure повторяется. Нет удаления
неизвестных файлов и «исправления» коллизии через overwrite.

## 6. Durable operation, fencing, exposure и recovery

### Admission и execution identity

Одна транзакция Bun: проверить settings/output epoch, захватить position и
namespace claims, заморозить допустимые execution inputs/attempt, создать
operation и enqueue River через `River.InsertTx`; River args содержат только
operation ID. Не создавать отдельный transactional gap между DB intent и job.
Delivery обязана перепроверить все claim/fence components до DB mutation и
каждого необратимого filesystem step:

`operation ID + operation attempt + River job ID + output epoch + claim/borrower
identity`.

Stale delivery не может продолжить после смены attempt/epoch/claim. Exclusion
состоит из единого process-local resource coordinator и durable execution claims.
PostgreSQL session advisory locks дают дополнительную координацию, но теряются
при разрыве соединения и сами не ограждают filesystem mutation.

Coordinator разделяется workers, recovery, source cleanup и reset; блокировки
позиции, FS-equivalent namespace и общих source resources удерживаются от
claim/revalidation до завершения tool/FS callbacks, всех mutating goroutines,
остановки дочерних процессов и settlement либо durable recovery handoff.
Разные operation IDs не обходят блокировку общего ресурса. Локальные locks
берутся до DB row locks; порядок ресурсов детерминирован и сверяется с текущими
flows. Recovery использует те же locks.

Claim хранит boot UUID, delivery token, attempt/job/output epoch/generation и
состояние executing/recovery_required/quiescent. Lease timeout, River rescue,
terminal operation state и исчезновение advisory lock не разрешают takeover.
При DB uncertainty прекратить новые шаги, отменить/убить и дождаться tools и
mutating callbacks; не освобождать local resources раньше их завершения.
Неподтверждённый settlement оставляет durable claim, блокирующий competitors
и reset. Tools пишут только в attempt-private staging; shared-target exposure
и unlink делает приложение после остановки writer и validation.

Startup recovery предшествует consumers и mutating endpoints. Для существующей
single-process архитектуры замена процесса должна установить завершение прежнего
server и mutating children; отсутствие DB lock или проверка PID этого не
доказывают. Если quiescence не установлена, takeover запрещён. Перекрывающиеся
запуски требуют отдельно проверенных interprocess exclusion и child containment;
обычный parent-only OS lock проблему orphan child не решает. Это технический
gate перед exposure, а не заявленная готовность текущего launcher. Неизвестное
private staging остаётся quarantined, не переиспользуется и не выставляется.

Обязательный regression interleaving: старая delivery теряет DB connection,
новая уже получила advisory lock, но отмена старого mutator ещё не завершилась.
Новая мутация, claim takeover, reset и shared-source unlink должны оставаться
запрещёнными. Проверять барьерами, не sleeps, включая recovery/uncertain commit.
Внешние filesystem writers не участвуют в coordinator; ownership/readback
обнаруживает конфликт, но не обещает fence от враждебного процесса.

### Предложенный lifecycle

```text
queued → preparing → staged → validated → exposing → committed → cleaning → settled
                      ↘ failed/retryable (old publication remains)
committed/cleaning → cleanup_pending → settled
```

Attempt state не является пользовательской историей. `validated` означает, что
audio/metadata/required checks прошли и readback получен. Cover/lyrics error,
source cleanup error — отдельные warnings согласно A11–A16, не выдуманный
audio success failure. Partial release выводится из состояний позиций.

Старый валидный output остаётся активным до validated replacement. Разделить
intent на audio-source replacement и metadata/assets rewrite. Новый audio source
проходит audio planner, comparator и extraction/transcode policy. При изменении
только tags/cover/lyrics использовать текущий owned artifact после повторной
проверки ownership, generation и фактического audio readback; не требовать
наличия исходников (включая уже удалённые cleanup) и не прогонять audio через
lossy re-encode. Сохранять audio stream без потерь при rewrite; если writer/container
не позволяет это доказать, остановить rewrite с видимой ошибкой, не ухудшать
audio молча. Quality comparator применяется только при смене audio source/stream:
равный audio не запрещает metadata/assets update.

Для same-path replacement сначала создать durable journal и verified recovery
backup старой версии, пока старая публикация остаётся доступной по target; только
после проверки backup выполнить exposure кандидата поверх target. Не перемещать
старый target заранее и не оставлять промежуток без файла. При DB failure
восстановить backup только если DB всё ещё указывает на ожидаемую старую
generation, а target доказуемо принадлежит новой generation этой попытки; иначе
conflict без blind overwrite/unlink. При crash recovery сопоставить journal, DB
generation, target и backup и детерминированно завершить commit либо
ownership-checked rollback. Для другого output path новую версию поставить
отдельно и переключить DB до старого cleanup; пока старый не удалён UI показывает
`cleanup_pending`/неполную замену. Нет гарантии кросс-путевой FS+DB атомарности.

Для каждого rename/unlink journal содержит expected source/target, old/new
publication generation, ownership evidence (identity/digest если он реально
выбран как technical witness), operation/fence и шаг; fsync/atomicity обещать
только после платформенной проверки. Recovery сверяет actual FS state и journal.
При неизвестном содержимом,
несовпадении ownership или stale fence — конфликт для оператора; не удалять и не
«усыновлять» файл молча. Не объявлять универсальную atomic replace для разных
путей/volume. Digest policy не превращать в дополнительное требование продукта.

## 7. Output reset и удаление источников

Publication claims/in-flight attempts и source cleanup должны быть включены в
существующие `AcquireOutputAdmissionGate`/`WithOutputAdmissionSession` и
`checkResetAllowed`/`resetDomainState`. Claimed/running публикация или cleanup
блокируют reset. Reset проверяет все unresolved execution claims независимо от terminal operation
или River state и не удаляет их ownership/recovery refs для обхода блокировки.
Reset обязан в одной DB-транзакции с обновлением output
epoch/snapshot отменить queued publication/cleanup jobs, удалить их queued refs
и пометить cleanup items cancelled/stale. Любая
подготовленная journal operation должна либо быть запрещена reset gate, либо
иметь явно доказанный recovery contract — не оставлять файловый mutation без
DB owner. Перепроверить реальные signatures/reset coordinator и tool/source
fences перед проектированием порядка. При reset старые publication files
остаются unmanaged, active DB refs очищаются, local entities/source links,
inventory/analysis сохраняются, автоматической republish нет.

Source removal opt-in OFF. Cleanup item и его operation/job должны быть связаны с
output epoch, publication generation и source/borrower revisions. Включение
настройки не запускает retroactive cleanup; точечный и bulk action — явные.
После успешной публикации+validation запланировать
все альтернативные sources позиции. Общий источник удалять только когда все
позиции/релизы, которые его используют, имеют успешно committed + validated
publication и ни один активный job/claim не требует его. Нужна атомарная
проверка borrowers и fencing против новых links/jobs перед unlink; непосредственно
перед unlink повторно сверить path identity и size/mtime, все borrower/source-link
revisions, validated publication generations и отсутствие queued/running consumer.
Нельзя удалять путь, если identity/revision изменился или путь уже чужой. На
ошибку удаления — warning с retry. Удаление root/location не удаляет publication
bytes/metadata/quality. Отдельное удаление publication — только явное действие;
внешне изменённый файл требует отдельного подтверждения. Удаление source root не
должно оставлять попытке FK на исчезнувший источник; snapshot cleanup item
позволяет безопасную ошибку/retry без source provenance.

## 8. Изменения данных и trigger coalescing

Automatic-ready trigger после уверенного matching, явного matching confirm,
local-only release confirm и изменения локального metadata/provider value/asset
должен coalesce-ить dirty fields/positions, не публиковать все сущности при
startup. Ручная authority оставляет изменившееся значение pending. Для ready
automatic position запускается planner/comparator; improvement replacement не
ждёт остальных позиций. Защита от повторных/одновременных triggers — idempotent
unique active intent per position/config version; старый intent переоценивает
актуальные входы до исполнения либо завершается stale, не пишет устаревшее.

Source freshness mismatch запускает согласованный re-analysis pathway, не
публикацию непроверенного пути. Анализ source, изменения source links, metadata
field, cover, lyrics и cleanup должны инвалидировать только зависимую область.
Output reset ставит durable suppression/epoch boundary; startup scan не должен
превращать reset в массовую auto-republish.

## 9. API/UI scope и наблюдаемость

Минимальная release detail/read model возвращает position UUID, локальные
значения/field provenance, pending authority, source candidates и quality
comparison, текущую active publication с output path, фактическим tag readback,
audio quality/format и отдельные asset/cleanup warnings. Preview показывает
intended template/path, collision, source/stream, planner inputs, mapping
outcome и validation. Mutations: source/stream choice, template/config, preview,
explicit publish/replace/retry, return-to-auto с preview/confirm, выбор cover/
lyrics, pending apply/discard, delete publication, source-cleanup retry. Все
экшены используют operation snapshot; SSE — только wake-up, после него UI
перечитывает состояние.

UI не выдаёт intention за readback, partial release за полный, warning за success,
hidden automatic decision за user choice либо формат capability за гарантию
любого файла. Huma → OpenAPI → Orval pipeline; generated code не редактируется.

## 10. Технические срезы и зависимости

Срезы уточняют P01–P10 плана, не сужая scope и не отменяя acceptance gates.

| Slice | Результат / блокеры |
| --- | --- |
| T01 — позиционная идентичность и read model | UUID release position additive migration/backfill, FK audit, release/position detail; проверка shared track/variant и uniqueness. |
| T02 — typed metadata, manual fields, provenance | Typed absent/value/explicit-empty, field registry/mapping contract, pending/authority; UI/API shape после capability map. |
| T03 — reader/writer и native removal | Pin unified deluan reader/writer; убрать go-mkvparse/native trees end-to-end; проверить format scope, actual readback и Navidrome. Не включать поле/формат без подтверждения. |
| T04 — publication schema, operation, reset | Additive tables/constraints, operation claims/epoch, River InsertTx, reset integration, safe down behavior. |
| T05 — чистый planner/template/quality | Versioned pure planner, template parser, FS namespace probe, stable suffix/claims, quality comparator vectors. |
| T06 — worker/extraction/tools | Claims/fencing/recovery inputs, source freshness, FFmpeg clean audio only, writer invocation, real readback. CUE scope rejected with clear validation. |
| T07 — exposure/recovery | Per-mutation journal, ownership-aware replacement, extension cleanup state, restart/conflict matrix, no cross-path atomicity claim. |
| T08 — cover | Provider → stable embedded fallback, manual lock/pending, optional warning/retry, embedding capability gate. |
| T09 — lyrics | Source synced → exact LRCLIB lookup; manual alternatives, no wrong audio transfer/retiming, Navidrome behavior gate. |
| T10 — automatic triggers + cleanup | Per-field dirty coalescing, no startup sweep; borrower-aware source cleanup with fencing, UI bulk/targeted retry. |
| T11 — API/UI, end-to-end, independent review | Generated API, truthful state, real DB/River/files/tool scenario, reset/recovery/partial cases; full task verify and independent acceptance. |

## 11. Верификация, миграции и последовательность реализации

Технический дизайн задаёт достаточно конкретный контракт, чтобы после его
независимого review начинать независимые узкие срезы: UUID позиции и модель
чтения; типизированные метаданные и выбор основного артиста; исследование
библиотеки чтения/записи тегов. Срезы, меняющие выходные файлы, обязаны сначала
реализовать и проверить взаимоисключение исполнений, журнал/recovery и fencing
сброса output; UI не обходит эти механизмы. Неизвестные возможности инструмента —
граница поддерживаемых возможностей: сначала исследовать и проверить, затем
включать только доказанные форматы/поля. Это не требует откладывать независимые
срезы.

Проверка реальных `deluan/go-taglib`, FFmpeg, соответствия Navidrome для тегов и
lyrics, а также упаковки должна зафиксировать версии, поддержанные форматы/поля,
readback и ограничения. Внешнее исследование D09 пока не проверено на runtime;
лицензионная проверка не является юридическим одобрением. WASM 2.3.2 и
предложенный commit — входные данные для проверки, а не доказательство
возможностей. Нельзя заявлять поддержку на основании констант/списков форматов;
при несовместимости функция остаётся явно неподдерживаемой, вопрос возвращается
владельцу, writer не подменяется.

Единственная локальная проверка проекта — `task verify` согласно инструкциям;
отдельные выборочные наборы тестов и сборки не заменяют её. Приёмка должна
включать реальные сценарии PostgreSQL-миграций, River, файловой системы и
инструментов с проверкой готового файла через readback. Матрица сбоев/recovery:
до/после intent, staging, validation, backup, exposure, фиксации транзакции БД и
cleanup; потеря закреплённого соединения при живом дочернем процессе; гонки с
reset; устаревшие attempt/epoch/generation; конкуренция за target и borrowers
source; сбой БД/rollback; внешняя мутация; ошибки provider/writer/tool. Для
Navidrome проверять реальное чтение согласованных тегов/assets, не выводить
поведение OpenSubsonic API из факта записи тега.

Откат additive migration не удаляет ссылки/артефакты действующих публикаций,
активные claims или незавершённые попытки: откат отклоняется, пока зависимые
публикации не удалены штатным безопасным действием. Это ограничение не относится
к отменённым A10 native capture/schema: их полное удаление одобрено и выполняется
без сохранения legacy-совместимости. Backfill UUID/схемы не запускает публикацию
старых локальных сущностей. Проверку существующих PK/FK и транзакции reset
выполнить в T01/T04; это задача с конкретным сроком/срезом, а не бессрочный
архитектурный блокер.

### Related deferred work

- CUE/manual segment support: explicitly deferred by plan12 A5; do not publish
  whole source as a track. See [deferred segmented publication](../to-decompose/segmented-file-publication.md).
- ReplayGain/R128: separate stage per A15; see [deferred ReplayGain/R128](../to-decompose/replaygain-r128.md).
- Source inventory/provider/matching remain plan 11 domain; publication consumes
  their verified inputs and does not redesign them.

**Условие перехода:** после независимого review дизайна можно начинать узкие
срезы в указанной последовательности. Подтверждение D09 на runtime требуется
перед включением/заявлением конкретных возможностей. Этот файл не меняет решений
владельца и не переписывает статус основного `12-file-publication.md`.
