# План: Setup Manager и управляемые внешние инструменты

## Статус документа

Текущий исполняемый план. Продуктовые и технические границы этапа согласованы.
План не определяет модель медиатеки, сканирования, matching или публикации
файлов.

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

## Порядок реализации

Тесты добавляются вместе с каждым этапом. Финальный quality-gate этап не должен
впервые вводить PostgreSQL, transaction или worker tests.

### 1. Bootstrap runtime и Compose prerequisites

- Добавить validation `HTTP_BIND_ADDRESS`/`HTTP_PORT`, listener address и
  healthcheck, использующий фактический port.
- Ввести `slog.LevelVar`, пока с default `info`.
- Добавить output bind mount interpolation
  `${MELOTROVE_OUTPUT_DIR:-./music}:/var/lib/melotrove/output`; host path не
  передаётся Go-приложению как runtime setting.
- Сохранить существующий persistent tools volume и отсутствие tools в image.

**Результат:** bootstrap env и container filesystem готовы до появления Setup.

### 2. Миграция и PostgreSQL test harness

- Добавить `app_setting`, `tool_installation`, `operation`, constraints и
  indexes одной согласованной up/down migration.
- Сразу создать integration-test harness с реальным PostgreSQL для migration
  up/down, repositories, partial uniqueness и транзакционного River enqueue.
- Не использовать SQLite или mocks для SQL/River семантики.

**Результат:** persistence contract и критические DB-инварианты проверяются до
service/API реализации.

### 3. Typed settings, immutable platform и filesystem primitives

- Реализовать registry/repository типизированных settings и dynamic log level.
- Атомарно зафиксировать platform при первом старте и добавить diagnostic mode
  для mismatch/unsupported platform.
- Разделить `setup_completed_at` и вычисляемый configuration health.
- Реализовать безопасную нормализацию/сравнение paths, writable/empty probes,
  filesystem semantics probe и tools conflict preflight.
- Покрыть Linux/macOS/Windows path cases unit tests, а реальные filesystem
  semantics — platform tests там, где они доступны runner.

**Результат:** базовые настройки и файловые правила не зависят от UI.

### 4. Source adapters и transient catalog

- Определить единый package adapter API: list compatible releases, resolve
  package artifacts, download/verify и materialize expected executables.
- Реализовать Chromaprint, BtbN и отдельный macOS adapter. Детали двух macOS
  artifacts не выходят за FFmpeg package adapter.
- Добавить HTTP limits/timeouts и fixtures для normal, malformed, rate-limited,
  missing asset, prerelease/snapshot/master и unavailable responses.
- Реализовать catalog service/endpoint без backend persistence/cache.

**Результат:** backend возвращает текущий валидированный список совместимых
release versions для immutable platform.

### 5. Installation lifecycle

- Реализовать operation staging, безопасную распаковку, checksum/signature при
  наличии, version checks и managed layout.
- Реализовать conflict preflight/explicit overwrite, идемпотентную фиксацию
  ready installation, отдельные activation/delete use cases и cleanup staging.
- Проверить package-level failure: FFmpeg installation не существует как ready,
  если любой artifact, `ffmpeg` или `ffprobe` не прошёл проверку.

**Результат:** несколько версий сосуществуют, а любой сбой оставляет active
installation неизменной.

### 6. River operations, move, retry и SSE

- Зарегистрировать install/move workers, transactionally enqueue jobs и
  реализовать operation state machine.
- Реализовать move только известных DB paths, полную повторную проверку,
  атомарное переключение root и опциональную точечную очистку старых files.
- Добавить REST snapshots, retry/dismiss, SSE notifier и cleanup successful
  snapshots старше 24 часов.
- Проверить process interruption на каждой filesystem stage и отсутствие
  duplicate installations/jobs после retry.

**Результат:** долгие операции переживают HTTP disconnect/reload и безопасно
продолжаются или повторяются.

### 7. Setup/settings services и Huma contract

- Реализовать typed Setup/runtime settings use cases и обязательный
  MusicBrainz connectivity check.
- Зарегистрировать API, экспортировать OpenAPI и обновить Orval client.
- Проверить route gate policy и финальную server-side Setup validation.

**Результат:** весь vertical backend flow доступен через стабильный
типизированный контракт, не раскрывающий storage details.

### 8. Production Setup Manager

- Реализовать согласованный S01 в React Aria/Tailwind: environment/platform,
  tools, publication, metadata providers и summary.
- Добавить route gate, восстановление сохранённых шагов, operation progress,
  keyboard/focus/loading/error states и отсутствие фиктивных статусов.
- В Setup выбранные installations активируются только после полной проверки.

**Результат:** fresh database можно полностью подготовить без ручного изменения
БД, env или filesystem.

### 9. Обычные Settings

- Реализовать редактирование output/publication/MusicBrainz/LRCLIB/log level.
- Реализовать installed/active/available tool states, client-side 24-hour
  catalog cooldown, manual Refresh, install, explicit activate, delete и move.
- Показывать configuration health после Setup без возврата в Setup Manager.

**Результат:** весь lifecycle настроек этого этапа доступен после первичной
настройки.

### 10. Документация и общие quality gates

- Обновить README и deployment docs: host/container paths, empty output rule,
  mixed tools root ownership, platform mismatch и macOS Intel limitation.
- Выполнить `task generate`, `task verify`, migration rollback, production build
  и Linux Compose smoke. Сохранить Linux/macOS/Windows `amd64`/`arm64` build/test
  matrix для поддерживаемых сочетаний.
- Реальные upstream requests в CI не выполнять.

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
