# План: Setup Manager и управляемые внешние инструменты

## Статус документа

Текущий план реализации. Продуктовые и технические решения для этого этапа
согласованы владельцем проекта. План не утверждает модель медиатеки,
сканирования, matching или публикации файлов.

## Цель

Реализовать первый полноценный пользовательский сценарий после технического
фундамента: при первом запуске оператор задаёт обязательные runtime-настройки,
выбирает совместимые версии `ffmpeg`/`ffprobe` и `fpcalc`, приложение загружает и
проверяет их, а обычный интерфейс становится доступен только после успешного
завершения Setup Manager.

После первоначальной настройки оператор может:

- проверить наличие новых версий;
- установить любую доступную совместимую release-версию;
- явно переключить активную версию;
- удалить ненужную неактивную версию;
- перенести managed tools-directory с проверкой результата;
- наблюдать ход фоновых операций и повторять неуспешные операции.

## Согласованные решения

### Setup Manager

- Этап включает backend, миграции, River jobs, REST/SSE API и production UI.
- Setup Manager является одноразовым initial flow. После успешного завершения он
  не используется для update/rollback: весь дальнейший lifecycle инструментов
  находится в обычных настройках.
- Для завершения Setup обязательны:
  - абсолютный доступный для записи tools-directory;
  - абсолютный доступный для записи output-directory;
  - выбранный режим публикации: исходный формат или MKA remux;
  - конфигурация MusicBrainz;
  - активные и успешно проверенные `ffmpeg`, `ffprobe` и `fpcalc`.
- Проверка writable output-directory временными probe-файлами определяет
  фактическую case sensitivity и Unicode normalization semantics файловой
  системы. Результат сохраняется для будущего publication planner и повторно
  определяется после изменения output-directory.
- MusicBrainz public выбран по умолчанию. Для self-hosted режима требуется URL.
- LRCLIB включён по умолчанию.
- Режим публикации оператор выбирает явно; скрытого default нет.
- Валидные значения сохраняются после каждого шага. Завершённость Setup
  фиксируется только после итоговой серверной проверки всех обязательных
  условий.

### Настройки

- До подключения к БД процесс читает только bootstrap env: `DATABASE_URL`,
  `HTTP_BIND_ADDRESS` с default `0.0.0.0` и `HTTP_PORT` с default `8080`.
  Реализация bind/port overrides входит в этот план как небольшое продолжение
  технического фундамента.
- Runtime-настройки хранятся в PostgreSQL в EAV-таблице: одна строка на пару
  `setting_name` / `setting_value`.
- `setting_value` имеет SQL-тип `text`.
- Реестр настроек в Go задаёт для каждого известного ключа его Go-тип, parser,
  serializer, допустимость отсутствия, validation и признак sensitive.
  Некорректное значение не записывается.
- REST API использует типизированные DTO и не открывает generic key/value
  endpoint.
- Все runtime-настройки, включая log level и credentials внешних APIs,
  изменяются только через типизированный UI/API; env overrides отсутствуют.
- Log level поддерживает `debug|info|warn|error`, default `info`, и применяется
  динамически. До чтения settings startup использует `info`.
- Sensitive value сохраняется в PostgreSQL в форме, необходимой интеграции, но
  read DTO возвращает только configured/masked state. Secret не включается в
  URL, logs, operation errors или копируемую диагностику; replace и clear
  являются явными operations. PostgreSQL и backups считаются чувствительными.
- Возможный собственный API key приложения не входит в этап. В будущем он
  генерируется/перевыпускается в UI, показывается полностью один раз и хранится
  только как hash.
- Выбранные установки FFmpeg package и `fpcalc` хранятся как settings со
  значениями-ID. Существование, совместимость и готовность соответствующих
  установок проверяет service layer.

### Версии инструментов

- В PostgreSQL хранятся локальные установки, но не upstream release catalog.
  Backend получает совместимые версии по запросу, а frontend хранит ответ только
  в памяти текущей сессии.
- UI показывает все обнаруженные совместимые stable/release builds для текущих
  OS и архитектуры.
- При первом Setup последняя совместимая версия предвыбрана, но оператор может
  выбрать другую до установки.
- Setup устанавливает ровно одну выбранную FFmpeg package version и одну
  выбранную `fpcalc` version. Списки installed/active/available и управление
  несколькими версиями относятся только к settings UI после Setup.
- Любую доступную совместимую версию можно установить и затем сделать активной.
- Все успешно установленные версии сохраняются без автоматического удаления.
- UI позволяет удалить только неактивную версию, которая не используется
  текущей операцией.
- Наличие версии новее активной показывается как доступное обновление, но не
  запускает автоматическую установку или переключение.
- Уже установленная версия остаётся пригодной к переключению, даже если upstream
  позднее перестал предлагать её для скачивания.
- Fallback из Docker image или системного `PATH` не используется.
- Offline bootstrap и ручная загрузка собственного binary в этап не входят.

### Источники и варианты пакетов

- `fpcalc` загружается из AcoustID Chromaprint GitHub Releases.
- FFmpeg package для Linux и Windows загружается из BtbN FFmpeg-Builds GitHub
  Releases; используется GPL static variant из нумерованной release-линии, не
  `master` build.
- FFmpeg package для macOS загружается с `ffmpeg.martin-riedl.de`; используются
  только GPL release builds, snapshots запрещены.
- FFmpeg package логически содержит `ffmpeg` и `ffprobe`. Они показываются и
  проверяются отдельно, но устанавливаются, активируются, переносятся и
  переключаются атомарно.
- Произвольные provider URL не принимаются из UI или API.

### Проверка и активация

- Архив сначала загружается в staging и безопасно распаковывается без возможности
  выхода за staging-directory.
- Если upstream публикует digest или проверяемую подпись, они проверяются до
  активации. Несовпадение блокирует установку.
- Если upstream не публикует digest или подпись, загрузка с allowlisted
  HTTPS-источника разрешена без отдельного verification level в БД или UI.
- До активации запускаются `ffmpeg --version`, `ffprobe --version` и
  `fpcalc --version`; результат должен соответствовать выбранному release.
- Установки лежат в versioned directories. Активная установка выбирается через
  settings, а не заменой бинарника на месте и не системным symlink.
- Неуспешная загрузка, распаковка, проверка или активация не изменяет текущую
  активную установку.

### Фоновые операции и прогресс

- Catalog endpoint синхронно получает текущие compatible releases через source
  adapters и возвращает их UI без сохранения в БД. Установка и перенос
  tools-directory выполняются River workers.
- REST создаёт операцию и остаётся источником истины для её текущего снимка.
- SSE endpoint конкретной операции передаёт быстрые уведомления о смене стадии и
  доступном измеримом прогрессе. После reconnect клиент перечитывает REST snapshot;
  отдельный журнал SSE-событий не хранится.
- Фиктивные проценты не рассчитываются. При известном `Content-Length` можно
  показывать полученные и ожидаемые bytes; иначе показывается только стадия.
- Пользовательской отмены в этом этапе нет. Ошибочная операция сохраняет причину
  и допускает безопасный retry.
- Frontend получает каталог при открытии Setup/settings, может периодически
  обновлять его в активной сессии и предоставляет ручной refresh. Установка
  всегда остаётся явным действием оператора.

### Перенос tools-directory

- Перенос копирует все managed-версии в новый абсолютный каталог, повторно
  проверяет файлы и версии и только затем переключает настройку каталога.
- Ошибка до переключения оставляет действующий каталог и активные версии без
  изменений.
- После успешного переключения UI спрашивает, удалить ли старые managed-файлы или
  оставить их. Удаляются только файлы и директории, которыми владеет приложение.

### Docker Compose

- Управляемая медиатека подключается как bind mount пользователя через Compose
  interpolation, например
  `${MELOTROVE_OUTPUT_DIR:-./music}:/var/lib/melotrove/output`.
- Внутренний container path передаётся и подтверждается в Setup как
  output-directory. Host path не является runtime env-настройкой Go-приложения.
- Существующий persistent volume tools-directory сохраняется; fallback-бинарники
  в image не добавляются.

## Пользовательские сценарии

### 1. Первый запуск

1. Frontend получает серверный Setup state.
2. Пока обязательная конфигурация не завершена, обычные маршруты перенаправляют в
   Setup Manager; диагностические данные остаются доступны.
3. Оператор задаёт tools-directory. Backend проверяет абсолютность пути,
   создаваемость/существование и возможность записи.
4. Backend определяет поддерживаемые OS/architecture и получает каталог
   совместимых release-версий из allowlisted sources.
5. UI предвыбирает последние совместимые FFmpeg package и `fpcalc`, но позволяет
   выбрать другие доступные версии.
6. Подтверждение запускает фоновые установки. UI получает operation IDs, читает
   REST snapshots и подписывается на operation-specific SSE streams.
7. Setup продолжается только после успешной установки и активации обеих
   выбранных package versions.
8. Оператор задаёт output-directory, явно выбирает исходный формат или MKA,
   подтверждает MusicBrainz public либо задаёт self-hosted URL и видит состояние
   LRCLIB.
9. Итоговая серверная проверка фиксирует завершение Setup и открывает обычный
   shell приложения; дальнейшее управление инструментами выполняется только в
   settings UI.

### 2. Неуспешная установка

1. Операция останавливается на конкретной стадии и сохраняет безопасное для UI
   описание ошибки.
2. Staging не становится активной установкой.
3. Ранее активная версия продолжает использоваться; при первом Setup состояние
   остаётся незавершённым.
4. Оператор повторяет операцию после исправления причины, не теряя сохранённые
   настройки и выбранные версии.

### 3. Выбор и установка другой версии

1. UI показывает upstream-каталог и локально установленные версии раздельно.
2. Оператор выбирает совместимую версию, запускает установку и после успешной
   проверки явно активирует её.
3. Предыдущая версия остаётся установленной и доступной для обратного
   переключения.
4. Неактивную версию можно удалить отдельным подтверждённым действием.

### 4. Проверка обновлений

1. Периодическая или ручная проверка запрашивает свежий provider catalog и
   сохраняет его только в памяти frontend.
2. Если upstream содержит версию новее активной, UI показывает обновление.
3. Никакой download или activation не происходит без явного действия оператора.

### 5. Перенос tools-directory

1. Оператор вводит новый абсолютный серверный путь и запускает перенос.
2. River worker копирует managed-версии и повторяет version checks в новом месте.
3. Только полностью проверенная директория становится текущей.
4. После переключения оператор выбирает, удалить старые managed-файлы или
   оставить копию.

## Изменения модели данных

Этап добавляет первую предметную SQL-миграцию поверх bootstrap migration.

### `app_setting`

- `setting_name text primary key`;
- `setting_value text not null`;
- `updated_at timestamptz not null`.

Минимальный реестр известных ключей включает:

- tools-directory;
- output-directory;
- publication format;
- MusicBrainz mode и self-hosted URL;
- LRCLIB enabled;
- ID выбранной FFmpeg installation;
- ID выбранной `fpcalc` installation;
- определённые для output-directory case и Unicode normalization semantics;
- момент завершения Setup.

Точные строковые имена ключей объявляются централизованными Go-константами.
Прямой доступ к таблице вне `internal/settings` запрещён. Отсутствие строки
отличается от пустого значения и обрабатывается registry конкретного ключа.

### Установленные версии

Отдельная таблица хранит каждую успешно установленную package version:

- ID, package kind, allowlisted source и upstream release/asset identity;
- относительный versioned path внутри текущего tools-directory;
- фактические результаты `--version` для входящих в пакет executables;
- состояние установки и времена установки/последней проверки.

FFmpeg installation считается готовой только при успешной проверке обоих
исполняемых файлов. Дублирующая установка одного release в тот же managed root не
создаётся.

### Операции

Приложение хранит текущий snapshot фоновой операции, необходимый REST и SSE:

- ID, kind, state и stage;
- связанный package/release identity либо перенос каталога;
- измеримые bytes, если размер известен;
- безопасное описание ошибки;
- River job ID;
- timestamps создания и обновления.

Завершённая история действий пользователя не проектируется. Записи операций
нужны для текущего результата, retry и восстановления UI после reconnect;
политика их технической очистки не должна удалять активную/ошибочную операцию до
того, как её состояние стало доступно оператору.

## Операции и API

Huma регистрирует типизированные операции следующих групп:

- чтение текущего Setup state и сохранение каждого шага;
- итоговая серверная проверка и завершение Setup;
- чтение platform/tool status;
- получение compatible release catalog без сохранения на backend;
- создание установки выбранного release;
- активация уже проверенной установки;
- удаление неактивной установки;
- запуск переноса tools-directory и сохранение решения об очистке старого;
- чтение operation snapshot и retry failed operation;
- SSE stream конкретной операции.

REST DTO не раскрывают EAV-пары. OpenAPI остаётся источником TypeScript DTO и
обычных REST-клиентов. Подключение к `text/event-stream` реализуется в
`frontend/src/api/client/` как явная transport-обёртка; payload SSE использует
тот же публичный operation state, что REST.

Service layer повторно проверяет права, совместимость release, текущий managed
root и допустимость перехода непосредственно перед изменением состояния. UI не
является границей безопасности. Installation request передаёт allowlisted source
и release/asset identity, но не download URL; source adapter заново разрешает
актуальный HTTPS URL перед загрузкой.

## Затронутые компоненты

- `backend/internal/settings`: EAV registry, typed access, validation и repository.
- `backend/internal/persistence`: модели и repositories настроек, installations
  и operation snapshots.
- `backend/internal/integrations/tools`: source adapters, downloader, безопасная
  распаковка, integrity/version verification и versioned layout.
- `backend/internal/service`: Setup, installation, activation, deletion и directory
  move use cases.
- `backend/internal/jobs`: check/install/move River args и workers.
- `backend/internal/api`: Huma REST operations и operation-specific SSE.
- `backend/internal/migrations`: предметная up/down migration.
- `frontend/src/api`: generated REST contract и SSE wrapper.
- `frontend/src/features/setup`: пошаговый Setup Manager.
- `frontend/src/features/settings`: tools status, version catalog, installation,
  activation, deletion и directory move.
- `frontend/src/routes`: Setup gate и settings routes.
- `deploy/compose`: output bind mount через Compose interpolation.

## Этапы выполнения

### 1. Bootstrap и typed settings boundary

Добавить `HTTP_BIND_ADDRESS`/`HTTP_PORT`, их validation и совместимый с выбранным
port healthcheck. Добавить миграцию, Bun models/repositories и registry известных
settings, включая sensitive metadata. Реализовать типизированное чтение/запись,
маскирование secrets, validation, динамический log level и вычисление Setup
readiness.

**Результат:** настройки сохраняются по шагам, но произвольные ключи и
невалидные строковые значения не проходят service/API boundary.

### 2. Каталог совместимых релизов

Реализовать source adapters для Chromaprint, BtbN и Martin Riedl, fixture-based
HTTP tests и фильтрацию по текущей OS/architecture и утверждённому варианту
пакета. Catalog endpoint возвращает свежий список без сохранения на backend;
frontend держит его только в памяти. Исчезновение upstream release не удаляет
installation.

**Результат:** backend возвращает воспроизводимый список совместимых версий и
может определить наличие версии новее активной.

### 3. Безопасная установка и переключение

Реализовать staging, download, archive extraction, проверку опубликованной
upstream checksum/signature при наличии, version checks, versioned directory
layout и идемпотентную фиксацию установки. Активация и удаление выполняются
отдельными use cases с проверкой инвариантов.

**Результат:** сбой на любой стадии не повреждает активный пакет; несколько
версий могут безопасно сосуществовать.

### 4. River operations и SSE

Добавить workers установки и переноса. Сохранять operation snapshot,
реализовать retry и SSE-уведомления без журнала событий. Запросы каталога при
открытии Setup/settings, периодическое обновление активной сессии и ручной
refresh выполняет frontend.

**Результат:** длительные операции не удерживают HTTP request, а UI
восстанавливает состояние после reload или разрыва SSE.

### 5. Setup и tools API

Зарегистрировать Huma operations, экспортировать OpenAPI и обновить Orval client.
Проверить, что завершение Setup невозможно при отсутствии обязательных настроек
или рабочих active installations.

**Результат:** frontend получает только типизированный контракт, а EAV storage
не протекает через REST.

### 6. Production Setup Manager

Перенести согласованный flow S01 из прототипа в React Aria/Tailwind: среда,
инструменты, публикация, metadata providers и итог. Добавить route gate,
доступные keyboard/focus/error/loading states и восстановление сохранённого шага.

**Результат:** новый пользователь проходит реальную настройку без ручного
редактирования конфигов и без фиктивных статусов.

### 7. Tools и runtime settings UI

Реализовать согласованную часть S16: каталог версий, installed/active states,
update indicator, ручные install/activate/delete, update check и перенос каталога
с выбором судьбы старых managed-файлов. Добавить operational settings, включая
log level; provider-specific credentials появляются только вместе с контрактом
соответствующей интеграции и используют общий sensitive-setting boundary.

**Результат:** lifecycle инструментов доступен после Setup и не требует доступа
к файловой системе или БД вне приложения.

### 8. Compose и эксплуатационная документация

Добавить output bind mount interpolation, задокументировать различие host path и
server/container path, обновить README и примеры запуска.

**Результат:** публикационный каталог переживает пересоздание контейнера и виден
в выбранной директории host.

### 9. Quality gates

Добавить unit/component tests, HTTP fixture tests, migration discovery/rollback
на реальном PostgreSQL, transactional River enqueue/commit/rollback, archive
traversal tests и job retry tests. Compose smoke уже выполняется отдельным
Linux-only GitHub CI job и должен продолжить проходить после изменений этапа.
Обновить generated contract и сохранить прохождение общего `task verify` и
platform build/test matrix.

Source adapters используют fixtures для успешного ответа, malformed metadata,
rate limit, отсутствующего release/asset и повреждённой загрузки; реальные
upstream requests в CI не выполняются.

## Критерии готовности

1. Fresh database открывает Setup Manager и не открывает обычные product routes.
2. Каждый завершённый шаг восстанавливается после reload и рестарта процесса.
3. Setup нельзя завершить без обеих активных package installations, валидных
   путей, явного publication format и валидной MusicBrainz configuration.
   Проверка output-directory также должна успешно определить filesystem
   semantics и удалить все probe-файлы.
4. Каталог содержит только allowlisted и совместимые с текущей платформой
   releases; snapshots macOS и BtbN master builds не предлагаются.
5. Выбранная не-latest compatible version устанавливается и активируется так же,
   как latest.
6. FFmpeg package не становится готовым, если не проверен хотя бы один из
   `ffmpeg`/`ffprobe`.
7. Upstream checksum/signature проверяется, когда доступна; её отсутствие не
   блокирует загрузку с allowlisted HTTPS source и не создаёт отдельный статус.
8. Неуспешная установка не меняет active installation и допускает retry.
9. Все установленные версии сохраняются до явного удаления; активную или занятую
   операцией версию удалить нельзя.
10. Update check сообщает о более новой версии, но ничего не устанавливает.
11. REST snapshot восстанавливает актуальное состояние после потери SSE; UI не
    зависит от сохранённого event log.
12. Перенос переключает tools-directory только после полной проверки копии и
    спрашивает о судьбе старых managed-файлов.
13. Compose использует пользовательский output bind mount, а приложение не
    получает host path как runtime environment variable.
14. Linux/macOS `amd64`/`arm64` и Windows `amd64` используют корректные source
    mappings; Windows `arm64` остаётся неподдерживаемым.
15. OpenAPI/Orval generation, backend/frontend tests, lint и production build
    проходят общие quality gates.
16. `HTTP_BIND_ADDRESS`/`HTTP_PORT` управляют listener и healthcheck; все
    остальные runtime settings не имеют env overrides.
17. Изменение log level применяется без рестарта; sensitive settings никогда не
    возвращают сохранённое значение и не появляются в logs/diagnostics.

## План проверки

1. На чистой БД пройти Setup с latest versions и с явно выбранными более старыми
   compatible versions.
2. Перезапустить backend после каждого шага и подтвердить восстановление
   сохранённых значений и незавершённого состояния.
3. Проверить filesystem probe на case-sensitive и case-insensitive test volumes,
   отсутствие оставшихся probe-файлов и повторную проверку после смены output.
4. Для каждого source adapter проверить fixtures: несколько версий, несовместимая
   архитектура, prerelease/snapshot/master, исчезнувший asset и network error.
5. Проверить archive traversal, повреждённый архив, неверную доступную upstream
   checksum/signature и несовпадающий `--version`.
6. Прервать backend на download/staging/verification и подтвердить безопасный
   River retry без дублирования active installation.
7. Разорвать SSE, перечитать REST snapshot и продолжить отображение операции без
   потери корректности.
8. Установить несколько версий, переключаться между ними, удалить неактивную и
   подтвердить запрет удаления активной.
9. Выполнить успешный и неуспешный перенос tools-directory; проверить оба решения
   о сохранении/удалении старых managed-файлов.
10. Проверить ежедневный и ручной update check без автоматической установки.
11. Запустить Compose с заданным host output path и проверить persistence после
    пересоздания app container.
12. Проверить keyboard navigation, focus restoration, loading/error states и
    отсутствие зависимости статуса только от цвета.
13. Выполнить `task generate`, `task verify`, migration rollback и Compose smoke
    run.

## Вне области этапа

- Сканирование source roots и анализ пользовательских аудиофайлов.
- Вызов `ffprobe`/`fpcalc` для медиатеки после установки инструментов.
- AcousticID, MusicBrainz catalog cache, LRCLIB requests и matching.
- Формула confidence, evidence, группировка входящих и drafts.
- Физическая модель артистов, релизов, recordings и tracks.
- Создание, remux, замена или удаление managed publications.
- Реорганизация существующей медиатеки при смене output-directory.
- Автоматическая установка обновлений инструментов.
- Использование системного `PATH` или bundled baseline tools.
- Пользовательская отмена фоновых операций и журнал завершённых операций.
- Windows `arm64`, неподтверждённые providers и произвольные download URLs.
