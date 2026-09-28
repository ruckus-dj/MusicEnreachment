# План: Setup Manager и управляемые внешние инструменты

## Статус документа

Текущий исполняемый план, частично реализованный. Ревизия 2026-09-28 отделила
фактически завершённую работу от оставшихся задач. Раздел «Осталось выполнить»
является последовательной инструкцией для реализации: задачи выполняются по
порядку, если в самой задаче явно не сказано обратное.

Продуктовые и технические границы этапа согласованы. План не определяет модель
медиатеки, сканирования, matching или публикации файлов.

## Цель

Реализовать первый полный пользовательский сценарий после технического
фундамента. При первом запуске MeloTrove фиксирует платформу экземпляра,
оператор задаёт обязательные runtime-настройки, выбирает версии FFmpeg package и
`fpcalc`, а приложение загружает и проверяет их. Обычный интерфейс становится
доступен только после успешного завершения одноразового Setup Manager.

После Setup оператор управляет всеми созданными настройками из обычного UI:

- меняет output-directory, publication format и настройки MusicBrainz/LRCLIB;
- проверяет каталог доступных версий инструментов;
- устанавливает, явно активирует и удаляет версии;
- переносит managed tools-directory;
- меняет log level без перезапуска;
- видит ход фоновых операций и повторяет неуспешные операции.

## Зафиксированные границы

### Одноразовый Setup и текущее состояние

- `setup_completed_at` фиксирует необратимое успешное завершение Setup. Поломка
  инструмента или изменение настройки позднее не возвращает пользователя в
  Setup Manager.
- До завершения Setup product routes закрыты; доступны Setup, диагностика и
  необходимые ему API. После завершения Setup его route больше не открывается.
- Текущее `configuration_health` вычисляется отдельно от факта завершения
  Setup. Проблема показывается в обычном UI и блокирует только зависящие от неё
  операции.
- Финальная команда завершения повторно и на сервере проверяет все обязательные
  условия. Сохранение отдельного шага не означает завершения Setup.
- Валидные значения сохраняются после каждого шага и восстанавливаются после
  reload или рестарта backend.

Для завершения Setup обязательны:

- поддерживаемая и неизменившаяся платформа экземпляра;
- абсолютный доступный для записи tools-directory;
- абсолютный доступный для записи пустой output-directory;
- явно выбранный publication format: исходный формат или MKA remux;
- успешная проверка соединения с текущей конфигурацией MusicBrainz;
- активные и успешно проверенные FFmpeg package и `fpcalc` installations.

LRCLIB включён по умолчанию, но сетевой запрос к нему не блокирует Setup.

### Платформа экземпляра

- При первом успешном старте с БД backend атомарно сохраняет текущие `GOOS` и
  `GOARCH` как immutable instance settings. Поддерживаются Linux/macOS
  `amd64`/`arm64` и Windows `amd64`; Windows `arm64` не поддерживается.
- Все source mappings, каталоги и installations относятся только к сохранённой
  платформе.
- Если сохранённая платформа не совпадает с текущим процессом, backend остаётся
  доступен в диагностическом режиме: UI и diagnostic API запускаются,
  readiness сообщает ошибку, Setup/product operations заблокированы. Изменить
  платформу через UI или автоматически нельзя.
- Для macOS `amd64` используется последний доступный совместимый release build
  утверждённого источника. После прекращения новых Intel builds в январе 2027
  UI честно сообщает, что новых обновлений может не быть; выбор нового источника
  является отдельным будущим решением.

### Runtime settings

- До подключения к БД процесс читает только `DATABASE_URL`,
  `HTTP_BIND_ADDRESS` (default `0.0.0.0`) и `HTTP_PORT` (default `8080`).
- Остальные настройки хранятся в PostgreSQL в EAV-таблице с `text` value.
  Централизованный Go registry задаёт тип, parser, serializer, default,
  validation и sensitive metadata каждого ключа.
- API использует предметные типизированные DTO и не предоставляет generic
  key/value endpoint.
- Log level принимает `debug|info|warn|error`, по умолчанию равен `info` и
  меняется через `slog.LevelVar` без рестарта. Startup до чтения БД использует
  `info`, а startup errors не скрываются уровнем из БД.
- Sensitive values хранятся в нужной интеграции форме, но read DTO сообщает
  только configured/masked state. Replace и clear явные; исходное значение не
  попадает в response, URL, logs, operation errors или диагностику.
- После Setup через Settings редактируются как минимум tools-directory,
  output-directory, publication format, MusicBrainz mode/base URL, LRCLIB
  enabled, log level и active installations.

### Output-directory

- Setup принимает новый каталог либо существующий **пустой** каталог. Непустой
  каталог не импортируется и не связывается с новой БД.
- Backend проверяет абсолютность, создаваемость, доступность записи и отсутствие
  пересечения с tools-directory. Символические ссылки и platform-specific path
  forms нормализуются до сравнения согласно правилам текущей ОС.
- Временные probe-файлы определяют фактическую case sensitivity и Unicode
  normalization semantics. Они удаляются и при успехе, и при ошибке; результат
  сохраняется и пересчитывается после смены output-directory.
- В рамках этого этапа новый output-directory также должен быть пустым. Перенос
  или присоединение уже опубликованной библиотеки появится только вместе с
  publication lifecycle.

### MusicBrainz

- Public mode использует фиксированный официальный endpoint; self-hosted mode
  требует корректный HTTP(S) base URL.
- Завершение Setup выполняет реальный route-specific connectivity check именно
  текущей сохранённой конфигурации. Timeout, недоступность или невалидный ответ
  блокируют завершение с безопасной диагностикой и возможностью повторить.
- Отдельная кнопка проверки доступна до завершения и позднее в Settings.
- Проверка соединения не создаёт MusicBrainz cache и не вводит остальные
  provider use cases в этот этап.

### Package model и источники

- FFmpeg является одним логическим package, содержащим `ffmpeg` и `ffprobe`.
  Installation, activation, deletion, move и failure относятся ко всему
  package атомарно.
- Linux/Windows adapter получает package из GPL static numbered releases BtbN
  FFmpeg-Builds. macOS adapter инкапсулирует получение двух upstream artifacts,
  но предоставляет service layer тот же package API. Ошибка любого artifact или
  executable проваливает весь FFmpeg package.
- `fpcalc` adapter использует AcoustID Chromaprint GitHub Releases.
- macOS использует только GPL release builds `ffmpeg.martin-riedl.de`, без
  snapshots. BtbN `master` builds и prereleases не предлагаются.
- Произвольный download URL не принимается от UI. Backend разрешает artifact
  locations заново через allowlisted HTTPS source adapter.
- Upstream catalog не хранится в PostgreSQL. Backend получает его синхронно по
  запросу, а frontend держит response только в памяти текущей сессии.

### Установка и активация

- Setup устанавливает ровно одну выбранную FFmpeg package version и одну
  `fpcalc` version и после полной проверки делает их active.
- После Setup install только добавляет проверенную installation. Activation —
  отдельное явное действие; прежняя active version остаётся active до его
  успешного завершения.
- Установка скачивается во временный operation staging. Распаковка запрещает
  absolute paths, `..`, symlink/hardlink escape и запись вне staging.
- Опубликованная upstream checksum/signature проверяется, если она доступна.
  Её отсутствие не блокирует allowlisted HTTPS download и не создаёт отдельный
  verification level.
- До фиксации installation запускаются все входящие executables с `--version`.
  FFmpeg package готов только после успешной проверки и `ffmpeg`, и `ffprobe` с
  ожидаемой release identity.
- Установленные версии не удаляются автоматически. Удалить можно только
  неактивную installation, не занятую operation.
- Системный `PATH`, bundled fallback, offline bootstrap и ручная загрузка
  binaries в этот этап не входят.

### Владение tools-directory

- Tools-directory может содержать посторонние данные; MeloTrove не требует
  пустого или специально помеченного root и не считает весь каталог своим.
- Управляемый layout имеет вид
  `ffmpeg/<version>/{ffmpeg,ffprobe}` и `fpcalc/<version>/fpcalc` с расширениями,
  допустимыми на целевой платформе. Version/path components формируются
  backend, а не принимаются как filesystem paths от клиента или upstream.
- Managed считаются только точные paths installations, которые MeloTrove сам
  успешно разместил и записал в БД. Приложение не сканирует каталог в поисках
  «своих» файлов и не удаляет неизвестные paths.
- Перед записью выполняется preflight exact target paths. Если неизвестный БД
  файл уже существует, операция возвращает список конфликтов и требует
  отдельного явного подтверждения overwrite. Подтверждение действует только на
  перечисленные target files данного package, а не на каталог целиком.
- Проверка `--version` подтверждает installation при создании, активации и
  переносе. Постоянный integrity monitoring и обнаружение ручной подмены
  managed files не выполняются.
- Staging именуется operation ID, не считается installation и безопасно
  очищается после успеха, окончательной ошибки и при восстановлении оборванной
  попытки.

### Перенос tools-directory

- Новый root должен быть абсолютным, writable, не совпадать и не пересекаться
  со старым root или output-directory.
- Worker переносит только exact managed files, известные из БД, сохраняя
  versioned layout. Неизвестные файлы старого root игнорируются.
- Конфликты target paths обрабатываются тем же preflight и отдельным явным
  overwrite confirmation.
- Все скопированные installations повторно проходят `--version`; setting root
  переключается только после полной проверки всей копии. При ошибке действующий
  root и active IDs не меняются.
- После переключения оператор выбирает, удалить ли старые managed files.
  Удаляются только прежние exact paths из snapshot операции; родительские
  директории удаляются лишь если после этого пусты.

### Catalog checks

- В Setup каталог запрашивается явно как необходимая часть выбора версий.
- После Setup автоматический запрос допускается не чаще одного раза за 24 часа.
  Timestamp последней успешной проверки хранится persistent на клиенте;
  catalog response остаётся только в памяти.
- Ручной Refresh всегда игнорирует cooldown. Если после reload response в памяти
  отсутствует, а cooldown ещё действует, UI показывает время последней проверки
  и предлагает ручной Refresh вместо скрытого запроса.
- Ни автоматическая, ни ручная проверка не устанавливает и не активирует версию.

### Фоновые операции

- Download/install и move выполняют River workers. Catalog и MusicBrainz
  connectivity checks являются синхронными HTTP use cases с route-specific
  timeout и не создают jobs.
- Создание operation row и River job выполняется атомарно в одном `bun.Tx` через
  `River.InsertTx`. Rollback не оставляет ни job без operation, ни operation без
  job.
- Operation snapshot — техническое текущее состояние, а не пользовательская
  история: `queued|running|failed|succeeded`, stage, измеримый byte progress,
  safe error, target identity, River job ID и timestamps.
- REST snapshot является источником истины. SSE конкретной operation лишь
  уведомляет об изменении; после reconnect клиент перечитывает REST. Event log
  не хранится, фиктивные проценты не вычисляются.
- Retry безопасно переиспользует logical operation/installation target и не
  создаёт дубликат installation. Одновременно допускается не более одной
  изменяющей операции на один package target. Move tools root взаимно исключён
  со всеми install/activate/delete и другими move operations.
- Пользовательской отмены нет. Успешные snapshots скрываются и удаляются через
  24 часа. Failed snapshot остаётся до Retry или явного Dismiss; active snapshot
  никогда не удаляется cleanup-процессом.

## Модель данных и инварианты

Этап добавляет первую предметную SQL-миграцию поверх bootstrap migration.

### `app_setting`

- `setting_name text primary key`;
- `setting_value text not null`;
- `updated_at timestamptz not null`.

Registry включает immutable instance `GOOS`/`GOARCH`, tools/output directories,
publication format, MusicBrainz mode/base URL, LRCLIB enabled, log level, active
FFmpeg/`fpcalc` installation IDs, output filesystem semantics и
`setup_completed_at`. Отсутствие строки отличается от пустого значения. Прямой
доступ к таблице вне settings/persistence boundary запрещён.

### `tool_installation`

Одна строка представляет логическую installation целого package:

- ID, package kind, platform, allowlisted source и upstream release identity;
- безопасный относительный versioned path;
- состояния подготовки/готовности и timestamps;
- фактические `--version` results всех executables package.

Database constraints и service transaction обеспечивают уникальную identity
package/source/release/platform в текущем managed root. Active ID должен
указывать на ready installation правильного package и immutable platform.
FFmpeg не разделяется на две installations или два active IDs.

### `operation`

Строка содержит kind/state/stage, immutable input snapshot, progress, safe
error, River job ID и timestamps. Snapshot install включает package, source,
release и выбранные artifact identities, но не произвольный URL; move snapshot
включает старый/new root, exact managed source/target paths и подтверждённые
conflicts.

Partial unique indexes и service locks запрещают конфликтующие активные
операции. Installation state change, active setting update и operation state
фиксируются транзакционно там, где filesystem boundary это допускает; файловые
шаги остаются идемпотентными и проверяемыми при retry.

## API

Huma регистрирует типизированные operations для:

- чтения Setup state, сохранения шагов и финального завершения;
- чтения platform и configuration health;
- проверки output/tools paths и MusicBrainz connection;
- чтения и изменения runtime settings;
- получения compatible package catalog;
- preflight/start/retry install и move operations;
- чтения installations, явной activation и удаления неактивной installation;
- чтения/Dismiss operation snapshot;
- operation-specific SSE.

OpenAPI остаётся источником REST DTO и Orval client. SSE transport реализуется
вручную в `frontend/src/api/client/`, но использует тот же публичный operation
state. API не раскрывает EAV rows, local absolute download URLs, filesystem
paths из upstream metadata или River internals.

Service layer повторяет validation, compatibility и transition checks перед
каждым изменением. UI не является границей корректности. Conflict overwrite
использует server-issued short-lived preflight token или эквивалентную
сверяемую identity, чтобы подтверждение нельзя было применить к изменившемуся
списку paths.

## Выполнено

### 1. Bootstrap runtime и Compose prerequisites

Завершено и проверено:

- `HTTP_BIND_ADDRESS` и `HTTP_PORT` валидируются, listener использует
  вычисленный адрес, container healthcheck обращается к фактическому port;
- logging использует `slog.LevelVar` с bootstrap level `info`;
- Compose монтирует
  `${MELOTROVE_OUTPUT_DIR:-./music}:/var/lib/melotrove/output`, не передавая host
  path приложению;
- tools-directory остаётся persistent volume, инструменты не входят в image.

Связанные файлы: `backend/cmd/server/main.go`, `backend/internal/app/app.go`,
`deploy/compose/docker-compose.yml`, `deploy/docker/Dockerfile`.

## Уже существующая частичная основа

Следующие компоненты можно дорабатывать, но нельзя считать завершёнными
этапами или подключать в UI как готовую функциональность:

- migration `20260928000000_setup_manager` создаёт базовые таблицы и часть
  operation constraints; есть PostgreSQL test harness и rollback test;
- `settings.Registry` фиксирует отдельные platform keys и умеет менять
  `slog.LevelVar`;
- filesystem package содержит path normalization и writable-empty probe;
- tool integrations содержат начальные catalog adapters, безопасную распаковку
  ZIP/TAR.GZ, SHA-256 helper, preflight и materialization;
- service package содержит in-memory notification и часть operation state
  transitions;
- frontend содержит только каркасы Setup/Settings, а не рабочие flows.

Нельзя считать комментарии или тестовые helper-вызовы production wiring.
Особенно важно: текущий `bootstrapWorker`, прямой `fetch`, disabled buttons и
локальное изменение catalog timestamp являются заглушками.

## Осталось выполнить

### Правила выполнения оставшихся задач

- Выполнять задачи ниже по порядку. Один change set должен закрывать одну задачу
  и её тесты.
- Перед изменением существующей migration определить, могла ли она уже быть
  применена вне disposable development DB. Если могла — добавить новую up/down
  migration, а не переписывать историю.
- Не ослаблять server-side validation ради UI. Каждый mutation use case повторно
  проверяет platform, Setup state, package identity и допустимость transition.
- Не использовать реальные upstream requests в tests/CI: только `httptest`,
  fixtures и fake command runner.
- После каждой задачи запускать релевантные Go/frontend tests; после изменения
  API обязательно выполнять `task generate` и проверять generated diff.
- Не переходить к frontend flow, пока соответствующий backend contract и его
  integration tests не готовы.

### 2. Завершить persistence contract и repositories

**Цель:** БД должна выражать все долговременные сущности и критические
инварианты, а service layer не должен собирать SQL вручную.

**Существующая основа:**
`backend/internal/migrations/20260928000000_setup_manager.tx.{up,down}.sql`,
`backend/internal/persistence/settings.go`,
`backend/internal/persistence/setup_manager.go`,
`backend/internal/testpostgres/postgres.go`.

**Сделать:**

1. Сверить `tool_installation` и `operation` с разделом «Модель данных и
   инварианты». Добавить недостающие constraints/indexes и поля, необходимые для
   artifact identities, безопасного retry и move snapshot.
2. Реализовать repository methods для list/get/update installations, проверки
   identity, фиксации ready/failed, чтения active operation conflicts и
   блокировки строк в service transactions.
3. Не пытаться сделать foreign key из text EAV value. Инвариант active ID
   реализовать транзакционным service method: installation существует, ready,
   имеет правильные package/platform; setting обновляется в той же транзакции.
4. Сделать общий transaction boundary, внутри которого production code вызывает
   `River.InsertTx` и создаёт operation row. Перенести доказательство атомарности
   из isolated test helper в реально вызываемый repository/service method.
5. Убедиться, что move взаимно исключается со всеми install/activate/delete/move,
   а install конфликтует только с тем же logical target. Не полагаться только на
   process-local mutex.
6. Добавить repositories для REST list/get operation snapshots и installations;
   storage types не должны напрямую становиться HTTP DTO.

**Тесты:**

- реальная PostgreSQL: migration up/down;
- duplicate installation identity;
- active operation uniqueness и move exclusivity;
- commit/rollback operation + River job;
- concurrent activation и неверный package/platform/ready state;
- retry переиспользует logical target и не создаёт вторую installation.

**Готово, когда:** production enqueue использует проверенный transaction method,
все repository paths покрыты PostgreSQL integration tests, а DB/service
инварианты не зависят от UI или in-memory state.

### 3. Завершить typed settings, platform policy и filesystem validation

**Цель:** создать единый backend boundary для всех runtime settings и path
правил до реализации Setup API.

**Существующая основа:** `backend/internal/settings/settings.go`,
`backend/internal/settings/filesystem.go`,
`backend/internal/persistence/settings.go`.

**Сделать:**

1. Заменить набор ad-hoc constants полноценным registry: для каждого ключа
   определить type, parser, serializer, default, validation, mutability и
   sensitive metadata. Включить все ключи из раздела `app_setting`.
2. Добавить typed methods/DTO-level values для tools/output directories,
   publication format, MusicBrainz mode/base URL/secret при наличии, LRCLIB,
   log level, active installation IDs, filesystem semantics и
   `setup_completed_at`. Generic key/value API не создавать.
3. Фиксировать GOOS и GOARCH атомарно одной DB transaction. Не допускать
   состояния, где сохранён только один ключ. Unsupported/mismatch возвращать как
   typed platform state, не изменяя сохранённую platform.
4. Хранить нормализованный server path, а не исходную строку. Перед сохранением
   проверять absolute path, writable/createable, empty output и overlap
   tools/output с учётом symlink ancestors и правил текущей ОС.
5. Добавить probe case sensitivity и Unicode normalization. Probe files должны
   удаляться при success и при любой ошибке; результат сохраняется только после
   успешного probe и пересчитывается при смене output.
6. Разделить необратимый `setup_completed_at` и вычисляемый configuration health.
   Повторный Complete не должен менять первоначальный timestamp.
7. Сделать сохранение взаимосвязанных runtime settings атомарным: invalid format
   или path не должны оставлять частично обновлённые values.
8. Sensitive reads возвращают только configured/masked state. Реализовать явные
   replace/clear и redaction для errors/logging/diagnostics.

**Тесты:** table tests для Linux/macOS/Windows path forms; symlink overlap;
empty/non-empty/unwritable paths; cleanup probe files; filesystem semantics;
platform initialization race/rollback/mismatch; log level; sensitive redaction;
atomic settings update.

**Готово, когда:** любой следующий service получает только typed validated
settings, а platform mismatch и configuration health можно вычислить без UI.

### 4. Реализовать Setup/settings domain services и MusicBrainz check

**Цель:** финальное завершение Setup должно быть невозможно без каждого
обязательного условия.

**Существующая основа:** `backend/internal/service/setup.go` проверяет только три
непустых значения и должен быть существенно расширен.

**Сделать:**

1. Разделить use cases чтения Setup state, сохранения отдельных шагов, path
   validation, MusicBrainz check, runtime Settings mutations и Complete.
2. Setup state должен возвращать сохранённые значения каждого шага в безопасном
   typed DTO, platform state, completion fact, health problems и ссылки на
   активные installations.
3. MusicBrainz public mode всегда использует официальный endpoint; self-hosted
   принимает только валидный HTTP(S) base URL. Добавить route-specific timeout,
   ограничение response body и проверку минимально ожидаемой структуры ответа.
4. Сохранять результат успешной проверки вместе с identity текущей конфигурации
   MusicBrainz. Изменение mode/base URL/secret инвалидирует прежний success.
5. LRCLIB включать по умолчанию, но не выполнять блокирующий Setup network call.
6. `Complete` в одной server-side проверке валидирует platform, paths,
   filesystem semantics, explicit publication format, актуальный MusicBrainz
   success и ready active FFmpeg/fpcalc нужной platform.
7. После completion изменения settings пересчитывают health, но никогда не
   очищают `setup_completed_at` и не возвращают пользователя в Setup.
8. Запретить прямую смену tools-directory после появления installations:
   использовать только move use case. Output по-прежнему должен быть пустым на
   этом этапе.

**Тесты:** public/self-hosted success; timeout, malformed и oversized response;
инвалидация check после изменения config; полный набор причин отказа Complete;
неизменность completion timestamp; health degradation после Setup.

**Готово, когда:** unit/service tests доказывают все критерии 1–5 без HTTP и
frontend.

### 5. Завершить source adapters и catalog service

**Цель:** по immutable platform вернуть только совместимые stable releases и
безопасно повторно разрешить artifacts перед download.

**Существующая основа:** `backend/internal/integrations/tools/catalog.go` умеет
читать часть GitHub/macOS metadata, но не имеет полного package adapter API и не
подключён к приложению.

**Сделать:**

1. Определить adapter interface для list releases, resolve selected release,
   получения artifact/checksum metadata, download/verify и materialization.
   Service принимает package/source/release/artifact identities, но никогда URL
   от клиента.
2. BtbN: только numbered GPL release assets, без master/prerelease; выбрать один
   package archive, содержащий `ffmpeg` и `ffprobe`.
3. Chromaprint: выбрать совместимый `fpcalc` asset для сохранённой platform.
4. macOS: сгруппировать два upstream artifacts (`ffmpeg` и `ffprobe`) в один
   логический Release/package; исключить snapshots и non-GPL. Для `amd64`
   корректно сообщать ограничение последнего доступного compatible release.
5. Перед каждым download заново resolve release через adapter и проверить HTTPS
   scheme и allowlisted hostname каждого artifact/checksum URL. Не доверять URL,
   ранее полученному frontend или сохранённому из catalog response.
6. Добавить HTTP timeout, redirect policy, status handling, body/download limits
   и безопасные ошибки без URL secrets.
7. Catalog service остаётся stateless: никаких DB/backend cache writes.

**Тесты:** fixtures normal/malformed/rate-limit/missing assets/unavailable;
master/prerelease/snapshot/non-GPL filters; все supported platform combinations;
redirect на запрещённый host; подмена artifact URL; macOS package с одной
отсутствующей частью.

**Готово, когда:** catalog service возвращает два logical packages с releases,
а resolve никогда не принимает произвольный download URL.

### 6. Реализовать installation lifecycle как production use cases

**Цель:** безопасно устанавливать, активировать и удалять версии, не затрагивая
неизвестные файлы.

**Существующая основа:** `backend/internal/integrations/tools/lifecycle.go`
содержит extraction/materialization helpers. Текущие `overwrite bool` и
`os.RemoveAll(versionDirectory)` использовать как финальную модель нельзя.

**Сделать:**

1. Staging root формировать только backend из operation UUID. На старте/retry
   очищать только staging этой operation; cleanup выполнять при success,
   terminal failure и восстановлении оборванной попытки.
2. Ограничить число entries и распакованный размер. Запретить absolute/traversal,
   symlink/hardlink и escape через существующие filesystem objects.
3. Проверять опубликованную checksum/signature, если adapter её предоставляет;
   отсутствие checksum у allowlisted source не считать ошибкой.
4. Проверять все executables через injected runner и immutable target platform,
   а не `runtime.GOOS` внутри materializer. Проверять ожидаемую release identity.
5. Preflight возвращает только exact target executable paths. Выдать
   short-lived signed/server-stored confirmation token, связанный с operation,
   root, package, release и точным conflict list.
6. Overwrite заменяет только подтверждённые exact files. Никогда не делать
   `RemoveAll` version directory: там могут находиться чужие файлы.
7. Ready installation фиксировать идемпотентно только после полной package
   verification. Частичный FFmpeg не становится ready.
8. Activation повторно проверяет executables и транзакционно меняет active ID;
   предыдущий active остаётся при ошибке.
9. Delete разрешён только для ready/failed неактивной installation без активной
   operation и удаляет exact DB-managed files; пустые parent directories можно
   удалить отдельно.

**Тесты:** traversal и links; corrupt archive/checksum; missing/mismatched
executable; package-level rollback; conflict token expiry/mismatch; unknown file
survives overwrite/delete; repeated commit is idempotent; active/busy delete
rejected.

**Готово, когда:** lifecycle tests проверяют filesystem до уровня exact paths и
ни один failure path не меняет active setting.

### 7. Подключить River install operations и безопасный retry

**Цель:** install выполняется реальным worker и переживает HTTP disconnect и
process interruption.

**Существующая основа:** `backend/internal/jobs/jobs.go` запускает River только с
`bootstrapWorker`; `backend/internal/service/operations.go` содержит часть state
machine, но не enqueue.

**Сделать:**

1. Определить versioned River args только с operation ID; immutable input
   snapshot читать из operation row.
2. Start install в одной transaction создаёт operation/installation target и
   вызывает `River.InsertTx`; записывает River job ID.
3. Worker реализует явные idempotent stages: resolve, download, verify archive,
   extract, verify executables, commit installation, cleanup. До и после каждой
   filesystem boundary сохранять stage.
4. Progress обновлять только измеримыми bytes downloaded/copied. Не вычислять
   фиктивные проценты.
5. Ошибки преобразовывать в заранее определённые safe messages; raw URL,
   response body, secrets и command environment не сохранять.
6. Retry разрешать только failed operation, очищать её terminal fields и
   транзакционно создавать новый River job для того же operation/target. Не
   создавать duplicate installation.
7. При startup/retry распознавать уже выполненный stage и безопасно продолжать
   либо повторять его.

**Тесты:** PostgreSQL + River test client; rollback enqueue; interruption после
каждого stage; duplicate delivery; retry; concurrent same/different target;
active ID остаётся прежним при failure.

**Готово, когда:** ни service, ни test напрямую не симулируют успешную установку
в обход production worker path.

### 8. Реализовать move tools-directory

**Цель:** перенести только DB-managed files и переключить root лишь после полной
проверки копии.

**Сделать:**

1. Preflight проверяет absolute/writable new root, отсутствие overlap со старым
   root и output, а также exact target conflicts.
2. Snapshot фиксирует old/new normalized roots, installation IDs, exact
   source/target files и подтверждённые conflicts. Изменение набора после
   preflight инвалидирует token.
3. Transactionally enqueue move worker с глобальной tools-operation
   exclusivity.
4. Копировать только snapshot files во временные target paths; unknown files в
   обоих roots не читать как managed и не удалять.
5. Повторно запустить `--version` для каждого copied executable. Только после
   проверки всех packages атомарно изменить tools-directory; active IDs не
   менять.
6. При ошибке удалить только созданные этой operation temp/target files,
   сохранив старый root действующим.
7. После switch выполнить выбранную оператором политику cleanup: удалить exact
   old snapshot files либо оставить их. Parent directories удалять только если
   пусты.
8. Сделать stages идемпотентными для River retry и process interruption.

**Тесты:** mixed source/target roots; overlap; conflicts и stale confirmation;
copy/verify/switch failures; оба cleanup решения; interruption на каждом stage;
unknown files сохраняются.

**Готово, когда:** failure никогда не меняет root, success не зависит от
неизвестного содержимого roots, повторная доставка job безопасна.

### 9. Реализовать полный Huma API, route gates, REST operations и SSE

**Цель:** предоставить frontend стабильный типизированный contract и сделать
REST snapshot единственным источником истины.

**Существующая основа:** `backend/internal/api/setup.go` регистрирует только три
неполных endpoint; `backend/cmd/openapi/main.go` сейчас генерирует пустой
contract, потому что создаёт API без registrations.

**Сделать:**

1. Создать единый registration function, используемый и production handler, и
   OpenAPI generator. Dependency interfaces для generation не должны требовать
   живую БД.
2. Добавить typed endpoints из раздела «API»: platform/health, Setup steps,
   runtime Settings, path/MusicBrainz checks, catalog, install/move preflight и
   start, installations, activation/delete, operation get/retry/dismiss.
3. DTO не раскрывают EAV rows, arbitrary URLs, upstream local paths, River args
   или raw errors. Sensitive values возвращаются только masked/configured.
4. Gate policy: до completion доступны diagnostics и необходимые Setup API;
   product/settings mutations закрыты. После completion Setup mutation/routes
   закрыты. Platform diagnostic блокирует Setup/product mutations, но оставляет
   UI, liveness и diagnostics доступными.
5. Передать platform state в readiness: mismatch/unsupported даёт 503 даже при
   доступной БД.
6. SSE endpoint конкретной operation отправляет только change notification/
   operation ID, heartbeat при необходимости и корректные no-cache headers.
   После connect/reconnect frontend обязан читать REST snapshot; event history
   не создавать.
7. Запланировать cleanup succeeded snapshots старше 24 часов. Failed удаляется
   только Dismiss/Retry, active — никогда cleanup job.
8. Исправить OpenAPI generation, выполнить Orval generation и перевести обычные
   REST вызовы frontend на generated client. Ручным остаётся только SSE transport
   в `frontend/src/api/client/`.

**Тесты:** API status/body tests для каждого transition и gate; diagnostic
readiness; no secret leakage; SSE reconnect contract; cleanup policy; generated
OpenAPI содержит все operation IDs и generated files не имеют drift.

**Готово, когда:** `frontend/openapi.json` не пуст, production и generator
используют один набор registrations, а обойти gates прямым HTTP нельзя.

### 10. Реализовать production Setup Manager

**Цель:** fresh database полностью настраивается из UI и сохраняет прогресс после
reload/restart.

**Существующая основа:**
`frontend/src/features/setup/SetupManager.tsx` — визуальный каркас. Текущий
tools step является текстом, publication format заранее выбран, state не
загружается, а route `#/` ошибочно снова открывает Setup.

**Сделать:**

1. App bootstrap сначала загружает Setup/platform state и показывает явный
   loading/error/retry state. Не выбирать route до ответа backend.
2. Исправить router: до completion product routes перенаправляются в Setup;
   после completion Setup перенаправляется в обычное приложение. Platform
   diagnostic показывает отдельный экран с diagnostics.
3. Реализовать шаги S01: environment/platform, directories, tools, publication,
   metadata providers, summary. Использовать React Aria components и generated
   client.
4. Восстанавливать сохранённые server values и текущий шаг после reload. Не
   считать frontend state источником истины.
5. Publication format должен требовать явного выбора; не отправлять default до
   выбора пользователя.
6. Tools step загружает catalog, позволяет выбрать версии, выполнить preflight,
   подтвердить точные conflicts, запустить обе installs и показывает REST
   operation progress с SSE wake-ups/reconnect.
7. Активировать выбранные installations только после ready verification.
   Summary показывает server validation и не имитирует готовность локально.
8. MusicBrainz step поддерживает public/self-hosted config и отдельную кнопку
   проверки; LRCLIB toggle не выполняет блокирующий request.
9. Для каждого async action реализовать disabled/loading/error/retry, перенос
   focus к heading/error после navigation, keyboard navigation и текстовые
   статусы без зависимости только от цвета.

**Тесты:** MSW для полного happy path и каждой server error; reload каждого
шага; SSE disconnect + REST recovery; conflict confirmation; route gates до/после
completion и diagnostic mode; keyboard/focus assertions.

**Готово, когда:** критерии 1–8 можно пройти в браузере на fresh database без
ручного SQL/filesystem вмешательства.

### 11. Реализовать обычные Settings и managed-tools UI

**Цель:** после Setup оператор управляет всеми созданными настройками без
возврата в Setup Manager.

**Существующая основа:** `frontend/src/features/settings/SettingsScreen.tsx`
сохраняет только три поля, не загружает server state и показывает disabled
кнопки. Текущий Refresh меняет только localStorage и не запрашивает catalog.

**Сделать:**

1. Загружать typed Settings и configuration health; показать проблемы и
   блокировать только зависящие от них actions.
2. Реализовать edit/save для output, publication, MusicBrainz, LRCLIB и log
   level. Tools root изменяется только через move flow.
3. Показать installed/active/available версии раздельно для FFmpeg package и
   fpcalc. Install после Setup не активирует версию автоматически.
4. Реализовать manual activation с server re-verification и delete только для
   допустимой неактивной/незанятой installation.
5. Реализовать install и move dialogs с preflight conflict list, scoped
   confirmation, operation progress, failure Retry/Dismiss и REST recovery после
   SSE reconnect.
6. Catalog response держать только в памяти. Timestamp последней успешной
   проверки хранить persistent на клиенте. Автоматический request — максимум раз
   в 24 часа; при reload во время cooldown показать timestamp и manual Refresh,
   не выполнять скрытый request. Manual Refresh всегда вызывает backend.
7. После settings mutation перечитывать server state/health; не оптимистично
   объявлять операцию успешной до server response.

**Тесты:** initial load; save и validation errors; dynamic log level; 24-hour
cooldown/reload/manual refresh; install без activation; activate rollback;
active/busy delete; successful/failed move; health degradation без Setup redirect;
accessibility states.

**Готово, когда:** критерии 9–15 и 17 выполняются через обычный UI.

### 12. Документация и финальные quality gates

**Цель:** удалить расхождения между документацией, contract и реально
проверенным deployment.

**Сделать:**

1. После реализации убрать из README утверждения `future Setup Manager` и
   `Automatic download is not implemented yet`; документировать фактические
   sources, host/container paths, empty output, mixed tools ownership, platform
   mismatch recovery и macOS Intel limitation.
2. Обновить deployment/design docs и примеры API только после стабилизации
   generated contract.
3. Расширить CI fixtures/tests без реальных upstream requests. Сохранить matrix
   для Linux/macOS `amd64`/`arm64` и Windows `amd64`; Windows `arm64` может
   проверять только явный unsupported behavior, но не считаться supported build.
4. Выполнить `task generate`, затем убедиться в содержательном generated diff;
   выполнить `task verify`, migration rollback на реальной PostgreSQL,
   production build и Linux Compose smoke с persistence после пересоздания app
   container.
5. Вручную пройти весь раздел «План проверки» и сохранить результаты в PR/issue
   checklist. Не заменять эти проверки фактом прохождения коротких unit tests.

**Готово, когда:** все 18 критериев готовности подтверждены тестом или явно
зафиксированной smoke/manual проверкой, документация не описывает функцию как
будущую, а этот файл можно целиком перенести из `todo/` в `done/`.

## Критерии готовности

1. Fresh database фиксирует текущую platform и открывает Setup, но не product
   routes.
2. Platform mismatch оставляет diagnostics/UI доступными, делает readiness
   неуспешной и блокирует Setup/product operations без автоматической миграции.
3. Каждый валидный Setup step восстанавливается после reload/restart, но
   `setup_completed_at` появляется только после общей server validation.
4. Setup нельзя завершить без пустого writable output, filesystem semantics,
   явного format, успешного MusicBrainz check и обеих active installations.
5. После завершения проблема configuration health не открывает Setup повторно.
6. Каталог содержит только allowlisted stable releases для immutable platform;
   BtbN master и macOS snapshots отсутствуют.
7. FFmpeg остаётся одной installation и готов только после успешной проверки
   обоих `ffmpeg`/`ffprobe` и всех внутренних macOS artifacts.
8. Checksum/signature проверяется при наличии; её отсутствие не блокирует
   allowlisted HTTPS package.
9. Неуспешная install/move operation не меняет active IDs или tools root и
   допускает идемпотентный retry.
10. После Setup install не активирует новую version автоматически; activation
    всегда отдельна.
11. MeloTrove изменяет и удаляет только exact DB-managed tool paths. Неизвестный
    target file требует подтверждения конкретного списка конфликтов.
12. Active или занятую operation installation удалить нельзя; остальные версии
    сохраняются до ручного удаления.
13. REST восстанавливает operation snapshot после потери SSE. Success удаляется
    через 24 часа, failed — только после Retry/Dismiss.
14. Автоматический catalog check не выполняется чаще раза в 24 часа на клиенте;
    manual Refresh всегда доступен и не запускает install/activation.
15. Все созданные Setup settings редактируются в обычном Settings UI.
16. Compose использует host output bind mount, а backend знает только
    server/container path.
17. Dynamic log level работает без рестарта; sensitive values не возвращаются и
    не попадают в logs/errors/diagnostics.
18. OpenAPI generation, PostgreSQL/River integration tests, backend/frontend
    tests, lint, production build и Compose smoke проходят.

## План проверки

1. Пройти Setup на чистой БД с latest и выбранными более старыми compatible
   versions; перезапускать backend после каждого шага.
2. Подменить сохранённую platform/запустить БД на другой platform и проверить
   diagnostic mode и readiness.
3. Проверить empty output rule, unwritable path, tools/output overlap,
   case/Unicode probes и отсутствие оставшихся probe files.
4. Проверить обязательный MusicBrainz public/self-hosted connection success,
   timeout, invalid response и безопасное отображение ошибки.
5. Прогнать fixtures каждого adapter, включая две части macOS FFmpeg package,
   missing asset, rate limit, malformed metadata и unsupported platform.
6. Проверить archive traversal/symlink escape, повреждённый archive, неверную
   checksum и несовпадающий `--version` каждого executable.
7. Установить package в смешанный tools root; проверить сохранение чужих файлов,
   conflict list, отказ без confirmation и точечный overwrite с confirmation.
8. Прервать backend на download/extract/verify/commit и подтвердить безопасный
   River retry без duplicate installation или смены active ID.
9. Разорвать SSE, перечитать REST snapshot и проверить cleanup success/failed
   snapshots по утверждённой политике.
10. Установить несколько versions, отдельно активировать, переключить обратно и
    удалить только допустимую неактивную installation.
11. Выполнить успешный и неуспешный move в mixed target root; проверить conflict
    confirmation, rollback root и оба решения об очистке старых managed files.
12. Проверить client cooldown после reload и ручной Refresh до истечения 24
    часов без автоматической установки.
13. После Setup сломать configuration и подтвердить alert/operation block без
    возврата в Setup Manager.
14. Запустить Compose с заданным host output path и проверить persistence после
    пересоздания app container.
15. Проверить keyboard navigation, focus restoration, loading/error states и
    отсутствие передачи статуса только цветом.
16. Выполнить общие quality gates и migration rollback на реальном PostgreSQL.

## Вне области этапа

- Source roots, scan и анализ пользовательских аудиофайлов.
- Вызов `ffprobe`/`fpcalc` для медиатеки после установки.
- MusicBrainz catalog/cache, LRCLIB requests, AcoustID и matching.
- Physical media model, confidence/evidence, grouping и drafts.
- Создание, remux, перенос, импорт или удаление managed publications.
- Присоединение непустой существующей output-library к новой БД.
- Автоматическая установка или активация tool updates.
- System `PATH`, bundled baseline tools и user-uploaded binaries.
- Постоянный integrity monitoring, backup tools-directory и обнаружение ручной
  подмены managed binaries.
- Пользовательская отмена jobs и история завершённых операций.
- Flow смены immutable platform, Windows `arm64`, неподтверждённые providers и
  произвольные download URLs.
