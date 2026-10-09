# План 11, M01 — provider и River research appendix

**Дата проверки внешних источников:** 2026-10-09.  
**Назначение:** документальная фиксация baseline и upstream facts для M01 плана 11.  
**Статус:** исследование; это не продуктовый контракт и не подтверждение выполнения следующих этапов.

**Позднейшее решение владельца, 2026-10-09:** Python служит reference логики,
не целью переноса 1:1. RapidFuzz и Unidecode, их внутренние алгоритмы и таблицы
не переносятся в приложение. M02 использует готовые Go-библиотеки; см.
[B11–B12 плана](todo/11-incoming-grouping-and-automatic-matching.md#appendix-b-owner-decisions-late-2026-10-09).
Исторические сведения об upstream requirements и лицензиях ниже остаются
результатом исследования, а не списком зависимостей MeloTrove.

## Baseline репозитория

- `git rev-parse HEAD`: `8c205db526d23e05279e17e185d11a2cb7a6813c`.
- Уточнение baseline при дополнении 2026-10-09: commit `671759030f32893161bc87c4ab0e7ca36be9c1ec` содержит только изменение документации плана 11 и не меняет исследованную кодовую ревизию `8c205db526d23e05279e17e185d11a2cb7a6813c`. Последующий HEAD `c26bf689c37179a6dd90be8f69ab9bca40936a03` также меняет только документацию плана 11 (coverage wording и ссылку M01); исследование кода по-прежнему привязано к 8c205db. Внешняя PostgreSQL документация и upstream clone проверены отдельно.
- До этой работы `git status --short` показывал уже изменённый `docs/plans/todo/11-incoming-grouping-and-automatic-matching.md` и неотслеживаемый `test_stand/`. Эти предварительные изменения не принадлежат M01 appendix; не трактовать их как результат M01 и не перезаписывать.
- Backend объявляет Go `1.27` (`backend/go.mod`); локально проверенная версия — `go1.27.1 darwin/arm64`. На дату доступа текущая документация `pkg.go.dev/net/http` опубликована для Go `1.27.2` (8 октября 2026); для версионной HTTP-семантики ниже используется именно эта версия, а не предположение о версии локального toolchain.
- `backend/go.mod` и `backend/go.sum` фиксируют River `v0.48.0` (включая `riverdatabasesql` и `rivertype`). Официальные River pages ниже — текущие docs на дату доступа, не архивная документация, привязанная к `v0.48.0`.
- В коде на baseline MusicBrainz ограничен connectivity check в `backend/internal/integrations/musicbrainz/client.go`: официальный WS/2 endpoint, `http.Client.Timeout = 10s`, контекст запроса, JSON и ограничение тела 1 MiB. Настраивается `User-Agent`; production release/recording search, cache, общий rate limiter и AcoustID adapter в этой границе не обнаружены. Конфигурация public/self-hosted находится в settings/service; текущий base URL — существующая настройка, не новая abstraction для matching.
- Существующие операции сохраняют состояние и ставят River jobs через `InsertTx` в Bun/PostgreSQL transaction, например `backend/internal/persistence/setup_manager.go`, `source_scan_enqueue.go`, `source_scan_retry.go`. В persistence применяются транзакционные row locks (`FOR UPDATE`) и точечные PostgreSQL advisory locks; пример для анализа — `source_analysis_steps.go:122-145,631-665`, где порядок lock-ов начинается с `source_root`, затем `source_location`, `source_analysis_work`, `operation` и step. Это наблюдаемые примеры текущего кода, а не утверждённый порядок lock-ов для будущего matching.
- На baseline верхняя версия встроенной SQL-миграции — `20261027000000_source_analysis_artifact_requested_steps.tx.{up,down}.sql`. DBML в `docs/design/music_ingest_redesign.dbml` концептуален: он не доказывает наличия matching schema.
- Текущие boundaries подтверждаются `docs/design/repository-architecture.md` и `docs/design/decisions.md`: integrations владеет внешним HTTP/parsing; service оркестрирует; только persistence обращается к Bun/PostgreSQL; `InsertTx` позволяет одной транзакцией записать application state и поставить job. Это исследование не меняет их.

## Официальные provider facts

Все ссылки ниже проверены 2026-10-09. Ревизии MusicBrainz взяты из footer самих страниц, а не выведены из даты доступа.

### MusicBrainz WS/2

Источники: [MusicBrainz API](https://musicbrainz.org/doc/MusicBrainz_API), ревизия wiki `79405`; [Rate Limiting](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting), ревизия `78895`; [Search](https://musicbrainz.org/doc/MusicBrainz_API/Search). Основной JSON endpoint документирован как `https://musicbrainz.org/ws/2/`; JSON выбирается через `fmt=json` или `Accept: application/json`.

Проверенные facts:

- Для публичного сервиса клиентское приложение не должно превышать **один запрос в секунду**; rate limiting отдельно рассматривает User-Agent, IP и общую нагрузку. Превышение/перегрузка может привести к `503 Service Unavailable`. IP rule применяет блокировку к запросам с IP, пока частота не опустится до 1/s или ниже; это не обещание, что отдельные лишние запросы просто будут обслужены с меньшей частотой.
- Каждому запросу необходим содержательный `User-Agent` с названием приложения и достаточной контактной информацией; версия рекомендована. Документированные примеры имеют форму `Application/version (contact-url-or-email)`. Нынешний connectivity client уже задаёт `MeloTrove/0.1.0 (https://github.com/ruckus/MusicEnreachment)`.
- Lookup по MBID может включать связанные сущности, но число возвращаемых linked entities ограничено 25; за остальными данными требуется browse. Browse поддерживает `offset`, а `limit` — максимум 100.
- Browse `/release` дополнительно ограничивает совокупный результат страницы 500 треками: страница может содержать меньше запрошенного количества релизов, но релиз не делится. Для paging `offset` следует увеличивать на фактическое число полученных релизов, а не на заданный `limit`.
- Search принимает `limit` от 1 до 100, по умолчанию 25, и `offset` для paging. Search не становится lookup/browse; ответ содержит результаты Lucene query.
- Публичный MusicBrainz WS — бесплатен для некоммерческого использования согласно API FAQ. Эта информация не разрешает коммерческое использование.

**Граница переноса:** публичный лимит относится к публичному MusicBrainz; не распространять его автоматически на self-hosted инстанс. Owner decisions по отдельному self-hosted toggle/default/delay находятся в [Приложении A плана](todo/11-incoming-grouping-and-automatic-matching.md#appendix-a-owner-decisions-2026-10-09), а не выводятся из этой policy. Официальная API documentation не задаёт продукту cache freshness, batch size, внутренний RPS default либо стратегию повторов.

### AcoustID lookup

Источник: [AcoustID Web Service](https://acoustid.org/webservice), проверен 2026-10-09. Lookup endpoint: `https://api.acoustid.org/v2/lookup`.

Проверенные facts:

- Для fingerprint lookup необходимы application API key (`client`), длительность всего аудиофайла в секундах (`duration`) и fingerprint (`fingerprint`). Можно запросить recording IDs/metadata через `meta`; найденные MusicBrainz recordings — связанное evidence.
- Сервис разрешает GET и POST, причём документация предпочитает сжатый POST для длинных fingerprints. Параметры lookup описаны как параметры запроса. Для данного приложения передача application key и fingerprint в POST body согласуется с требованием `docs/design/decisions.md` не помещать credentials в URL; это обоснование для реализации, а не отдельное правило AcoustID.
- Указан предел **не более 3 запросов в секунду**. Указано **non-commercial use only**; для коммерческого применения сайт направляет к отдельному коммерческому сервису.
- Выполнение lookup — необязательное evidence: owner contract плана 11 устанавливает optionality и обработку отсутствующего AcoustID factor. API docs не устанавливают для приложения default включения, cache freshness или расписание запросов.

В scope M01 не входят fingerprint submission, пользовательский API key для submission и изменение данных AcoustID; документируемый для matching use case — lookup evidence для recording.

### Go `net/http`

Источник: [pkg.go.dev/net/http для Go 1.27.2](https://pkg.go.dev/net/http@go1.27.2), текущая опубликованная версия на 2026-10-09.

- `http.NewRequestWithContext` связывает контекст с lifecycle request; отмена/timeout контекста применяются, пока отправляется запрос, получен ответ и читается response body.
- `http.Client.Timeout` охватывает connect, redirects и чтение response body; timer продолжает действовать после возврата `Do` и может прервать чтение body. В репозитории connectivity client задаёт 10s timeout, дополнительно принимая caller context.
- `Client.Do` сам по себе не трактует HTTP status вне 2xx как Go error; вызывающая сторона должна прочитать/закрыть body и интерпретировать status. Это релевантно будущей обработке `503`/rate limit, но не утверждает конкретную политику retry.
- `net/http.Transport` может в некоторых случаях автоматически повторить идемпотентный запрос при ранее использованном соединении; условия зависят от replayable body/идемпотентности и ошибки. Поэтому официальная документация не даёт основания утверждать, что встроенных повторов «нет вообще». При внедрении rate limiter нужно учитывать возможную повторную HTTP отправку и не объявлять число вызовов `Do` равным точному числу wire requests без проверки конкретного поведения.

Контекст/timeout — механизм ограничения и отмены HTTP работы, не политика freshness, retries, backoff или частоты провайдера.

## River facts и наблюдаемая практика репозитория

Официальные источники, проверенные 2026-10-09: [Transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing), [Job retries](https://riverqueue.com/docs/job-retries), [Writing reliable workers](https://riverqueue.com/docs/reliable-workers), [Unique jobs](https://riverqueue.com/docs/unique-jobs).

- Транзакционный enqueue связывает вставку job с прикладными изменениями в одной транзакции: job становится доступной после commit вместе с состоянием, от которого зависит. Репозиторий использует `InsertTx` для этого паттерна.
- Ошибки/сбои могут приводить к retry; workers должны наследовать и уважать `context` и быть безопасны к повторному исполнению. Документированный default River retries — максимум 25 попыток с экспоненциальной задержкой/jitter; не следует принимать default этой библиотеки за утверждённую retry-политику matching.
- Unique jobs ограничивают повторную **вставку** по заданным атрибутам/состояниям. Они не обеспечивают exactly-once execution: River описывает выполнение как at-least-once, поэтому приложение должно идемпотентно применять side effects и иметь собственные DB fences/constraints.
- `InsertTx`/unique job semantics не выбирают автоматически idempotency key, допустимость повторного применения устаревшего результата, recovery lifecycle или модель matching cache. Это остаётся design/implementation scope соответствующих этапов.

## Upstream scoring и fixture provenance

Проверена read-only shallow clone, указанный в плане: `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/upstream_repo`, `HEAD=17448df1ee7ab5c9ad4769b4e09a474b27547728`. Clone не содержит найденного файла `LICENSE`/`COPYING`; отсутствие файла в shallow clone не является юридическим выводом об upstream лицензии.

- Ниже перечислены provider fixture files, читаемые matching tests через `tests/support/providers.py` (`FIXTURES_DIRECTORY / provider / <FixtureCase>.json`), и файлы inline/scoring regression cases. Для всех перечисленных путей source commit в исследованном clone — `17448df1ee7ab5c9ad4769b4e09a474b27547728`; в clone на этом commit отсутствует найденный LICENSE/COPYING. Provenance помечен по наблюдаемой форме/комментариям тестов; где первичный источник не записан, он так и отмечен как неизвестный.

| Путь в upstream | Наблюдаемый origin | License/provenance disposition |
| --- | --- | --- |
| `tests/fixtures/musicbrainz/ambiguous.json` | Synthetic fixture-control JSON (`outcome`, `candidate_count`), не MusicBrainz wire response | Exact upstream file owner-approved для переноса по A7; отдельная лицензия/атрибуция файла в clone не указана. |
| `tests/fixtures/musicbrainz/disabled.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/malformed.json` | Synthetic malformed fixture-control JSON (незакрытая JSON value) | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/no_match.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/rate_limited.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/success.json` | Synthetic fixture DTO (`Fixture Release`/`Fixture Artist`, тестовые MBID); не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/timeout.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/unavailable.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/ambiguous.json` | Synthetic fixture-control JSON (`outcome`, `candidate_count`), не AcoustID wire response | Exact upstream file owner-approved для переноса по A7; отдельная лицензия/атрибуция файла в clone не указана. |
| `tests/fixtures/acoustid/disabled.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/malformed.json` | Synthetic malformed fixture-control JSON (незакрытая JSON value) | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/no_match.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/noize-pesnya-dlya-radio.json` | Test comment называет содержимое «verbatim current AcoustID response captured for the Noize MC source»; response hash закреплён regression test. Но запрос использует placeholder `'noize-fingerprint'`, не сохранённый реальный fingerprint; provenance реального lookup/input не установлена. | A7 даёт owner approval для переноса upstream fixture. Точная лицензия/provider attribution и происхождение захваченного response отдельно в clone не записаны — считать эти детали неизвестными, сохранять известное attribution и проверять применимые требования при переносе. |
| `tests/fixtures/acoustid/rate_limited.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/success.json` | Synthetic fixture DTO с `Fixture` recording MBID и тестовым score; не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/timeout.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/unavailable.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/matching/test_matching.py` | Inline constructed score inputs/candidates; содержащиеся реальные на вид названия/имена не снабжены отдельной source provenance в test file | Владелец одобрил использование upstream fixture material по A7; отдельная лицензия test file не обнаружена. Для каждого фактически переносимого real-world literal точная исходная provenance неизвестна. |
| `tests/matching/test_matching_providers.py` | Inline synthetic transport JSON/test objects, в основном явно названные `Fixture`; test file также содержит реалистичные названия/IDs без per-literal provenance | A7 owner approval для upstream test material; отдельная лицензия test file не обнаружена. Не объявлять real-looking literals синтетическими provider captures; их внешняя provenance не зафиксирована. |
| `tests/matching/test_noize_matching_regression.py` | Regression consumer/inline test, который читает указанный Noize JSON и передаёт `'noize-fingerprint'`; реального fingerprint input в test нет | A7 owner approval для файла; provenance/лицензия response ограничены сведениями из строки таблицы выше и не становятся установленными только из test name. |

`test_matching.py` — scoring-focused family (406 строк); `test_matching_providers.py` (1319 строк) покрывает также inline provider adapter/evidence cases; `test_noize_matching_regression.py` — 92 строки. Noize JSON не является fingerprint corpus и не доказывает live-network lookup. SHA-256 содержимого `tests/fixtures/acoustid/noize-pesnya-dlya-radio.json`, зафиксированный test assertion: `048562094731e7993eeb78a4cae77adb552c8a54f01b2af08d43e2902efee3a0`.

- В `pyproject.toml` upstream указаны `rapidfuzz>=3.14.6` и `Unidecode==1.4.0`. Зафиксированная owner-approved лицензируемость upstream fixtures/семантики и обязательство реализовать логику самостоятельно на Go отражены в A7 плана 11. Это не делает Python-код runtime dependency и не меняет выбор разрешённых в проекте зависимостей.
- Справочные license sources для этих upstream requirements: [RapidFuzz license](https://github.com/rapidfuzz/RapidFuzz/blob/main/LICENSE) — MIT; [PyPI Unidecode 1.4.0](https://pypi.org/project/Unidecode/1.4.0/) — GNU GPL v2 or later (GPLv2+). Допустимость, подтверждённая владельцем, не отменяет выполнения применимых требований лицензии/атрибуции, если fixture или производный материал действительно переносится; это требует compliance-проверки при таком переносе.
- Для переноса fixture corpus точные источники каждого значения/записи и происхождение Noize fixture следует сохранять. Личная собственность upstream и допустимость лицензий подтверждены владельцем; это снимает owner approval gate, но не отменяет соблюдение применимых уведомлений/атрибуции при фактическом переносе файлов.
- Инвентаризация выше ограничена тремя matching tests и shallow clone. Она не является полным SBOM/лицензионным аудитом всего upstream-репозитория или всех его зависимостей.

## PostgreSQL locking semantics и локальная практика

Источники проверены 2026-10-09 в версионной документации [PostgreSQL 18, Explicit Locking](https://www.postgresql.org/docs/18/explicit-locking.html) и [PostgreSQL 18, Advisory Lock Functions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS). На дату проверки документация указывает PostgreSQL 18 как current supported version; это версия документации, не заявление о конкретной runtime-версии установленной БД.

**Официальные факты:**

- Row-level locks удерживаются до завершения транзакции (либо соответствующего savepoint rollback). `SELECT ... FOR UPDATE` блокирует конкурирующие записи и несовместимые row-lock requests на затронутых строках; обычный SELECT без row lock не блокируется row lock-ом.
- Взаимные ожидания блокировок могут образовать deadlock; PostgreSQL обнаруживает deadlock и abort-ит одну транзакцию. Docs рекомендуют брать locks на несколько объектов в согласованном порядке. Ожидание конфликтующей блокировки без deadlock может продолжаться без ограничения времени.
- Advisory locks — application-defined; PostgreSQL не навязывает их протокол и не гарантирует, что все конкурирующие участники берут тот же lock. Session-level lock держится до явного unlock либо конца session и не откатывается вместе с transaction rollback. Transaction-level (`*_xact_lock`) автоматически освобождается в конце transaction и не требует unlock.
- PostgreSQL предоставляет key формы двух `int4` или одного `int8`; shared/exclusive варианты существуют как для session-, так и для transaction-level locks. Это механизм сериализации только между участниками, соблюдающими одинаковые lock keys/protocol.

**Наблюдаемое соответствие кода (не контракт для будущего matching):**

- `backend/internal/persistence/source_analysis_coordination.go`: `advisoryXactLock` формирует вызов `pg_advisory_xact_lock` или `_shared` из пары `int32`; namespace `0x4d545256` (`MTRV`) и domain keys заданы явно. `lockPackageKinds` сортирует package kinds перед взятием нескольких advisory locks; комментарий явно запрещает `hashtext` для этих ключей.
- `backend/internal/persistence/source_coordination.go`: package activation использует те же sorted advisory keys; `lockToolsRoots` сортирует roots, а SQL запрашивает их `ORDER BY id` до row-lock. Это две наблюдаемые меры порядка в соответствующих call paths, а не глобальная гарантия для всех операций.
- `backend/internal/persistence/output_admission_coordination.go`: output admission gate берёт shared transaction-level advisory lock, reset gate — exclusive transaction-level lock. Отдельный `WithExclusiveOutputAdmissionSession` закрепляет `*sql.Conn`, берёт `pg_advisory_lock` session-level и явно делает `pg_advisory_unlock` на том же соединении; это связано с callback/journal/filesystem flow и отличается lifecycle-ом от обычного xact helper.
- `backend/internal/persistence/source_analysis_steps.go` также демонстрирует row-lock порядок `source_root → source_location → source_analysis_work → operation → source_analysis_step`. Никакой из этих примеров не утверждает, что будущая matching-транзакция должна копировать этот порядок без собственного design/review.

PostgreSQL facts не выбирают продуктовую модель или lock protocol matching; точный стабильный порядок и transaction scope для будущих group apply/cache operations остаются техническим design/review вопросом.

## Facts и решения, которые остаются открытыми

Подтверждённые выше лимиты, HTTP semantics и River guarantees — **внешние факты**. Одобренные продуктовые решения остаются в owner appendix плана. Это исследование не принимает дополнительных продуктовых решений и не выбирает технический механизм.

Перед соответствующими increments ещё требуется отдельно определить и проверить: provider cache freshness/invalidation; конкретный compliant limiter/semaphore и поведение при `503`; self-hosted endpoint compatibility assumptions; правила AcoustID key transport/storage в соответствии с проектным secret policy; применение query/browse/lookup и dedup/paging к конкретным сущностям; соответствие River integration/docs версии `v0.48.0`; idempotency, lock order, fences и recovery для matching transactions. Не переносить текущие timeout, file concurrency=4 или чужие library defaults как новые RPS, cache TTL, retry или persistence decisions.

## Проверенные локальные материалы

- `docs/design/decisions.md`, `docs/design/repository-architecture.md`, `docs/design/data-model.md`, `docs/design/music_ingest_redesign.dbml`.
- `backend/internal/integrations/musicbrainz/client.go`; `backend/internal/settings/settings.go`; `backend/internal/service/setup.go`.
- `backend/internal/persistence/setup_manager.go`, `source_scan_enqueue.go`, `source_scan_retry.go`, `source_analysis_steps.go`; `backend/internal/jobs/`.
- `backend/go.mod`, `backend/go.sum`, `backend/internal/migrations/`; `frontend/src/routes/AppShell.tsx`, `frontend/src/features/sources/`.

Файлы и directory boundaries перечислены как места baseline inspection, а не свидетельство наличия в них matching implementation. Codegraph/исходный код и миграции проверялись на code baseline `8c205db526d23e05279e17e185d11a2cb7a6813c`; HEAD на момент этого уточнения `c26bf689c37179a6dd90be8f69ab9bca40936a03` содержит только более поздние documentation-only изменения. Локальные тесты не запускались, поскольку это документационное уточнение M01.
