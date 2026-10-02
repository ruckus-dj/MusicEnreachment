# План: технический анализ источника и инспектор файла

## Выполнение

Все десять шагов выполнены 2026-10-02 на ветке `go-development`.
Независимая итоговая приёмка: COMPLETE; полный `task verify` завершился с exit 0.
Реальный сценарий с PostgreSQL, River и скачанным и проверенным managed ffprobe,
REST/SSE, повтором, отказом, изменением источника и восстановлением после restart
подтверждён. Инспектор проверен в браузере на 1440 и 375 px в обеих темах;
байты источника сохранены, ресурсы QA закрыты.

## Исходный статус и выбор этапа

Подготовлен 2026-10-01 по состоянию `f9b5c53` ветки `go-development`.
Это новый план реализации, а не отчёт о выполнении. Пользователь поручил
выбрать и декомпозировать следующий этап; конкретные контракты ниже являются
предложением этого плана, а не ранее утверждёнными решениями технического
дизайна. Запись и коммит плана не означают реализацию функциональности.

Завершены фундамент, Setup с управляемыми инструментами, первый инвентарь и
его приведение к дизайну: `docs/plans/done/01-development-foundation.md`,
`02-setup-manager-and-managed-tools.md`, `03-source-inventory-first-slice.md`,
`04-source-inventory-design-alignment.md`. В коде есть ручной scan,
транзакционное применение поколения, `audio/no_audio/probe_error`, recovery,
REST/SSE и экраны roots/locations. Нет сохранённого технического анализа и
`media_variant`. `ffprobe` сейчас подтверждает только наличие аудиопотока.

Следующий полезный срез — получить фактические параметры и теги одного
найденного файла и показать их оператору. Это основа будущих fingerprint,
группировки и matching, но не требует придумывать confidence или quality score.
Полный черновик `docs/plans/to-decompose/source-inventory-and-analysis.md`
слишком широк для дешёвого исполнителя: он одновременно включает scratch,
дедупликацию, fingerprint и автоматическое планирование анализа. Здесь
отделён меньший пользовательский сценарий с десятью проверяемыми шагами.

Основания: `docs/design/requirements.md`, разделы «Инвентарь и обработка
источников» и «Технические диапазоны и качество» в
`docs/design/decisions.md`, `docs/design/data-model.md`,
`docs/design/music_ingest_redesign.dbml`, `docs/design/repository-architecture.md`.
Интерфейс следует S09/S10 из `docs/app-design/02-screens.md` и плотной
композиции `docs/app-design/DESIGN.md`. В HTML-прототипе отдельного полного
инспектора S10 нет: не выдавать придуманный скриншот за утверждённый макет.

## Пользовательский результат и границы

После успешного scan оператор открывает файл со статусом `audio`, запускает
«Анализировать» и получает контейнер, все audio streams, доступные технические
параметры, исходные теги и раскрываемый JSON `ffprobe`, время и версию анализа.
Операция переживает закрытие страницы; повторное открытие подключается к ней.
«Повторить анализ» явно перечитывает файл. Ошибка не стирает предыдущий
валидный результат и не меняет inventory или доступность root.

Этот срез работает только `in_place`, читает source напрямую и запускает
анализ только отдельной кнопкой одного файла. Регистрация root и scan не
запускают его автоматически. После изменения файла и успешного scan старый
результат больше не относится к location; новый анализ запускается явно.
Это намеренная промежуточная поставка, не завершение всей области анализа.

Вне этапа: `fpcalc`/Chromaprint, SHA-256 и exact deduplication, staged mode,
work-directory, автоматический анализ новых файлов, группировка входящих,
MusicBrainz/AcoustID matching, quality/lossless policy, CUE-сегменты,
извлечение artwork, создание медиатеки, публикация и удаление source.
Не добавлять фиктивные scores, fingerprint, выбор staged или новую настройку
SHA-256 в UI. Существующий scan и его 13 расширений сохранить.

## Контракты для исполнителя

### Данные и актуальность

- `media_variant` в этом срезе — минимальный сохранённый результат успешного
  технического анализа, а не вся концептуальная таблица DBML. UUID,
  `size_bytes >= 0`, `analysis_policy_version=1`, `ffprobe_version`,
  `ffprobe_json jsonb`, `observed_tags jsonb`, `inspected_at`, ID применившей
  результат operation. Структуру контейнера и потоков хранить в JSON snapshot
  и выводить через типизированный service read-model; не дублировать все
  параметры отдельными SQL-колонками до появления потребителя запросов.
- `source_location.media_variant_id` nullable; один результат без SHA-256
  не связывать с другим path по похожим тегам, size, inode или fingerprint.
  Старые locations после миграции не анализировать автоматически.
- Не создавать сейчас обязательные `quality_score`, `lossless`,
  `chromaprint_hash` или digest с выдуманными значениями. DBML концептуален;
  неполный физический срез допустим. Неизвестные технические значения — null,
  а не ноль или пустая строка. Длительности в read-model — `int64` миллисекунд,
  без произвольного верхнего лимита.
- Идентичность версии файла: root ID, configured/inventory path, location ID,
  exact relative path, size и сохранённый mtime. GET/body/snapshot и сверка
  с inventory используют существующую микросекундную точность PostgreSQL и
  `sourceScanMtime`, без миграции timestamps. Дополнительная сверка stat до
  и после чтения использует полную доступную точность файловой системы;
  snapshot повторно сверяется с БД при применении. Это stat-модель;
  не обещать обнаружение подмены
  bytes с искусственно сохранёнными size/mtime без digest.
- Успешный scan сохраняет связь для неизменённого файла; при изменении
  size/mtime, смене inventory path или переходе из `audio` отвязывает её.
  Удаление location/root убирает связи, но не source bytes.
- Orphan variant без digest удаляется, когда на него больше не ссылаются
  locations и активные analysis operations. Ошибочный повторный анализ
  неизменного файла сохраняет прежний variant. Новый успешный анализ
  атомарно заменяет связь и убирает больше не удерживаемый старый variant.

### Нормализация результата

`ffprobe` возвращает format и все streams, без извлечения бинарного artwork.
Read-model содержит container names, format duration/bitrate и список audio
streams в порядке stream index: index, codec name/profile, duration,
bitrate, sample rate Hz, sample format, bits per sample, channels/layout.
Не выбирать один поток как единственный «правильный» и не классифицировать
неизвестный codec как lossy. Streams других типов сохраняются в raw snapshot.

Числовые строки и JSON numbers разбирать с проверкой диапазона; отсутствующее,
`N/A` или неприменимое значение — null. Отрицательное/неразбираемое значение
не становится готовым техническим значением; некорректная обязательная
структура ответа проваливает анализ. Duration округлять до ближайшей
миллисекунды; не терять многочасовые значения в `int32`.

Имена observed tags переводить в uppercase, каждое значение представлять
массивом строк, не делить строки по `;` или `/`. Добавлять format tags первыми,
затем tags audio streams по index, удаляя только точные повторы внутри одного
ключа с сохранением порядка. Raw JSON сохраняет namespace и provenance
конфликтующих значений; не подменять source tags локальными/provider tags.

### Операции и конкуренция

Новый `kind=analyze_source`, stages `queued/probing/applying`;
River args несут только operation ID. Versioned snapshot включает identity
файла, ID прежнего variant для удержания, analysis policy version и
выбранную ready FFmpeg installation ID. Executable path разрешается сервером
по managed installation, не принимается от HTTP и не ищется в `PATH`.

Для простоты этого первого среза активные scan и analysis взаимно исключены
на одном root; активный анализ запрещает удаление root, path change и disable,
но не редактирование имени. Правило обеспечивается общей DB boundary/index,
а не только кнопкой или mutex. Другие roots остаются независимыми.
Выделенная River analysis queue имеет один worker: это технический предел
первого среза, без новой runtime-настройки и автоматического расписания.

Analysis удерживает выбранную installation: её удаление и перенос tools root
не допускаются до terminal state. Activation другой версии допустима, потому
что уже поставленная analysis использует snapshot installation. Обратная
проверка также обязательна: enqueue анализа конфликтует с активным move tools.
Удержание выразить отдельным nullable FK
`operation.analysis_installation_id`, очищаемым при terminal transition;
`target_installation_id` нового kind остаётся null. Общий инструмент может
иметь несколько read holds от разных roots: существующий unique index
на mutation target не должен применяться к этим удержаниям. Tool delete/move
проверяет их под общей блокировкой с analysis start/retry.
Не расширять существующий общий диапазон install/move exclusivity на analysis
без проверки: он не должен случайно запретить независимые source roots.

Retry использует ту же logical operation и прежний input snapshot; если
файл или root изменились, вернуть conflict и предложить новый анализ текущего
файла, не анализировать старый snapshot по новому пути. Retry повторяет
порядок DB locks и exclusions start: выбранная snapshot installation ещё
существует и ready, move отсутствует, прежний snapshot variant ещё существует
и остаётся текущим variant location (включая совпадение null). Иначе 409 и
предложение нового анализа; immutable snapshot не переписывать.
Удержания variant/installation восстанавливаются атомарно вместе с queued
transition и новым River job. Отказ оставляет operation failed, без job и
новых удержаний. Результат и succeeded
фиксируются одной DB transaction с уведомлением после commit. Повторная
доставка уже применённой operation не запускает `ffprobe` снова.

### HTTP и UI

Пути ниже начинаются с `/api`:

| Команда | Контракт |
| --- | --- |
| `GET /sources/{root_id}/locations/{location_id}` | Identity location, probe status, optional variant ID, технический результат либо `not_analyzed`, active analysis operation ID. |
| `POST /sources/{root_id}/locations/{location_id}/analyze` | Типизированный JSON body с ожидаемыми size/mtime из GET; ответ 202 с operation snapshot. Повторный анализ тем же endpoint, без отдельного force flag. |
| Существующие `/operations/{id}` и SSE | Чтение, wake-up, retry/dismiss по существующему контракту с dispatch нового kind. |

Проверять принадлежность location root. 404 — нет root/location; 409 —
stale inventory, изменившаяся identity, disabled root, не-`audio` или
конфликтующая операция; 503 — действующий отказ Setup/platform или отсутствует
рабочий managed tool. В diagnostic mode GET разрешён, мутации запрещены.
Ошибки чтения source после enqueue — безопасная ошибка operation, не
выдуманная недоступность всего root. Путь файла из клиента не принимается.

Инспектор находится под `#/sources/{root_id}/locations/{location_id}`.
Показать breadcrumb, точный relative path, size/mtime, источник read-only,
stale/availability отдельно от результата, статус операции и кнопки.
Список существующих locations дополнить ссылкой; не переносить туда весь JSON.
Сначала компактные технические факты, затем потоки, теги и raw disclosure.
Нет большого hero, фальшивой обложки, score или «используется в 0 треках»:
соответствующие сущности ещё не реализованы.

## Порядок выполнения и доказательства

Все десять шагов последовательны. Каждый передаётся одному дешёвому агенту
отдельно вместе с этим документом, результатом предыдущего шага и указанными
путями. Агент не выбирает заново продуктовую область и не начинает следующий
шаг. Если обнаружен конфликт контракта с кодом, вернуть точное противоречие
ведущему агенту вместо молчаливого изменения модели.

Перед правкой читать соседние реализации и tests. Каждый шаг заканчивается
относящимися к нему тестами, `task verify` с exit 0 и кратким отчётом:
изменённые пути, выполненные проверки, ограничения среды. Generated-клиент
обновлять только pipeline внутри gate; для проверки generated drift новые
генерируемые изменения должны попасть в индекс согласно текущему механизму
`tools/check-generated.mjs`. Не обходить hook и не ослаблять проверки.
Коммит каждого зелёного инкремента — только при явном разрешении в сессии
исполнения; разрешение закоммитить этот документ не является разрешением
реализовать и коммитить все шаги.

**Единственный локальный test/build gate — `task verify`.** Узкая диагностика
допустима только после его failure. Platform matrix и Compose smoke остаются
в CI. PostgreSQL tests используют `backend/internal/testpostgres`,
River enqueue проверяется реальным commit/rollback. Без sleeps и timing luck:
подписка на точный event или управляемый барьер до действия. Реальные внешние
провайдеры не нужны. Новые tests проверяют поведение, не текст этого плана.

### 1. Добавить минимальную схему результата и target операции

**Зависимости:** нет. **Область:** `backend/internal/migrations/`,
`backend/internal/persistence/` (модели).

Новой парой миграций после `20261004000000` добавить `media_variant` из
контракта, nullable FK location → variant с запретом удаления связанного
variant, `operation.target_source_location_id` и новый operation kind.
Для terminal snapshots FK location допускает `ON DELETE SET NULL`;
активную operation нельзя потерять каскадом. Удержание старого variant
выразить nullable FK operation → variant, очищаемым при terminal transition.
Для read hold инструмента добавить отдельный `analysis_installation_id`;
он не входит в unique/exclusion constraints installation mutation target.
Ограничить shape targets нового kind; объединить root exclusivity
`scan_source/analyze_source`, сохранив install/move constraints.
Использовать существующие UUID/JSON/operation conventions, не переписывать
применённые миграции.

**Готово, когда:** integration tests проверяют up/down на чистой БД,
nullable locations, FK/target shape, отрицательный size, active scan против
analysis и analysis против analysis на одном root; разные roots допустимы.
Существующие install/move tests зелёные. Никакие старые locations не изменили
данные и не получили искусственный variant.

### 2. Реализовать чтение, применение и очистку variants

**Зависимости:** 1. **Область:** `backend/internal/persistence/`,
особенно `source_candidates.go`, `source_repository.go`.

Добавить typed get location с проверкой root, read variant и transactional
apply результата: lock root/location/operation, сверка snapshot, insert
нового variant, замена location FK, terminal succeeded, очистка удержания
и orphan variant. Для уведомления вернуть committed operation вызывающему
service. Повторное применение той же operation — no-op.
Дополнить scan apply: неизменённая identity сохраняет FK, изменённая
отвязывает, reconciliation и root delete очищают не удерживаемые orphans.
Все удаления variant делать внутри persistence transaction, не через
filesystem и не через nullable FK с молчаливым обнулением.

**Готово, когда:** PostgreSQL tests доказывают apply/rollback/idempotence,
сохранение связи на unchanged scan, reset на modified/path change/no_audio,
удаление location/root, удержание старого variant активной operation и его
очистку после terminal state. Failed scan сохраняет и inventory, и связи.

### 3. Добавить bounded технический запуск ffprobe

**Зависимости:** 2. **Область:** `backend/internal/integrations/tools/`,
тесты рядом с `ffprobe.go`.

По образцу существующего `FFProbe` добавить отдельный технический запрос
`-v error -show_format -show_streams -of json` с одним серверным filename.
Сохранить старый короткий probe scan без изменения его результата.
Production запускать абсолютный executable без shell; stdout и stderr
ограничивать во время чтения, не после unbounded `CombinedOutput`.
Deadline 120 секунд, stdout не более 1 MiB, stderr не более 64 KiB.
Превышение/timeout/nonzero/malformed JSON — ошибка анализа, без сохранения
сырого stderr в user-facing operation. JSON должен быть object и содержать
как минимум один audio stream. Наличие streams само по себе ещё не заменяет
структурную проверку.

**Готово, когда:** детерминированные tests runner/process boundary покрывают
валидный ответ, video-only, пустой/malformed/oversized stdout, oversized
stderr, nonzero и cancellation. Передаются точные argv и absolute managed
path; filename с пробелами не превращается в shell command.

### 4. Сделать типизированный разбор технических данных и тегов

**Зависимости:** 3. **Область:** новый parser/read-model в
`backend/internal/service/` и его unit tests.

Реализовать контракт нормализации выше отдельно от HTTP DTO.
Сохранить оригинальный JSON как snapshot; построить типизированные container
и audio stream facts, source observed tags. Сохранить все audio streams,
отсутствующие значения и конфликтующие tags. Не добавлять quality policy,
codec allowlist или fingerprint.

**Готово, когда:** table tests на fixtures FLAC/MP3/MKA с несколькими streams,
attached picture, format+stream tags, mixed-case keys, `N/A`, отсутствующими
числами, дробной и многочасовой duration, некорректными числами проверяют
exact machine values и отсутствие потери тегов. Raw snapshot не подменяется
нормализованным объектом.

### 5. Реализовать безопасный анализ одного source location

**Зависимости:** 2–4. **Область:** новый use case в
`backend/internal/service/`; существующие path helpers.

По snapshot разрешить зарегистрированный root/location и managed tool,
проверить identity/`audio`/enabled/stale, разрешить путь без directory/file
symlink и выхода за root. Повторить stat до/после запуска инструмента.
Вернуть подготовленный результат для transactional apply; при изменении
файла не применять ничего. Проверить реальную версия-query `-version` через
существующий tool lifecycle и записать фактическую версию.
Source никогда не открывать на запись; не менять scan generation,
probe status или root availability. Временный scratch не создавать.

**Готово, когда:** service tests через управляемый runner/barrier доказывают
успех, отсутствие записи в source, missing/changed file, stale inventory,
root/location mismatch, symlink replacement/escape, unavailable executable
и cancellation. Изменение файла между stat и ответом не публикует variant;
прежний успешный variant остаётся видимым.
Fixture с mtime не на границе микросекунды подтверждает, что сравнение
с inventory использует `sourceScanMtime`, а pre/post stat — полную точность.

### 6. Добавить атомарный enqueue и блокировки ресурсов

**Зависимости:** 1–2, 5. **Область:** `backend/internal/persistence/`,
analysis start service, существующие root/tool mutations.

Start под DB locks читает current identity и выбранную ready installation,
сверяет ожидаемые size/mtime, создаёт versioned snapshot и operation вместе
с River job через существующий `InsertTx`. Snapshot удерживает прежний
variant и installation. Применить root exclusivity не только к scan start,
но и к root delete/path change/disable, включая уже существующие use cases.
Расширить tool delete/move boundary для active analysis; start анализа
повторно проверяет отсутствие активного move под тем же порядком locks.
Activation не меняет snapshot поставленного job.

**Готово, когда:** PostgreSQL/River integration tests доказывают commit и
rollback enqueue, конкурирующие start/scan/edit/delete, start/move в обе
стороны, удержание installation и неизменность snapshot после activation.
Нет job без operation или orphan operation без job. Два разных roots не
блокируются новым root constraint, даже с одной FFmpeg installation:
оба read holds допустимы, activation другой версии разрешена, delete
удерживаемой installation и tools move запрещены.

### 7. Подключить worker, retry и startup recovery

**Зависимости:** 5–6. **Область:** `backend/internal/jobs/`,
`backend/internal/service/operations.go`, `backend/internal/app/app.go`.

По образцу `SourceScanWorker` зарегистрировать analysis worker и очередь с
concurrency 1. Перечитывать immutable snapshot и текущие условия, stages
вести через существующую Operations. После probing применить результат
методом шага 2; SSE notification отправлять после commit.
Добавить dispatch retry нового kind в существующий endpoint: failed →
queued, восстановление обоих read holds и новый job той же operation
атомарно, только если все input/resource проверки retry выше успешны.
Для startup reconciliation использовать существующую проверку живого River
job: orphan неприменённой операции → failed и освобождение удержаний,
уже committed результат → succeeded без повторного чтения source.
Не трогать живой job и не вводить пользовательский cancel.

**Готово, когда:** реальные River tests покрывают delivery, duplicate delivery,
probe/apply failure, interruption до/после commit, recovery, retry и stale
retry conflict. Обязательны последовательность «A failed → B успешно
заменил variant неизменного файла → retry A даёт 409», удаление snapshot
installation после failure и retry против move/delete в обоих порядках.
Отказ retry не создаёт job/holds и не меняет failed state.
Результат не бывает виден с неприменённой связь-версией,
terminal transition освобождает ресурсы. Install/move/scan recovery сохраняют
поведение. Progress не использует bytes как количество файлов.

### 8. Добавить API анализа и сгенерировать клиент

**Зависимости:** 4, 6–7. **Область:** `backend/internal/api/`,
общая registration и экспорт OpenAPI.

Добавить два endpoint из таблицы контрактов; GET строит DTO из typed
read-model и показывает актуальную active operation ID, не только последний
успех. В operation response обеспечить root/location target IDs для
повторного подключения UI. Использовать существующие Setup/platform gates,
без обхода проверки service. Dismiss/Retry dispatch поддерживают новый kind.
Raw JSON возвращается только по детальному GET; списки locations получают
лишь variant ID/наличие результата, без огромного snapshot.

**Готово, когда:** API tests подтверждают 202/404/409/503, чужой location,
невалидные size/mtime, отсутствие arbitrary path input, diagnostic read-only,
read результата и discover активной операции. `task verify` обновляет
OpenAPI/Orval без ручных изменений generated-файлов.

### 9. Сделать инспектор и наблюдение операции в реальном UI

**Зависимости:** 8. **Область:** `frontend/src/features/sources/`,
`frontend/src/routes/AppShell.tsx`, существующие source styles.

Расширить текущий hash route matcher и разбор SourcesScreen для nested
location route; не оставлять глубокий адрес попадающим на главную страницу.
Добавить ссылки в SourceLocations и компактный inspector по контракту.
GET при открытии восстанавливает ongoing analysis; кнопка запуска недоступна
до завершения discovery. SSE только будит REST reread; после terminal
state обновить detail даже при failure. Показывать предыдущий результат с
его временем отдельно от ошибки повторной операции. Переход на другой файл
отменяет устаревшие requests и подписки. Использовать существующие
React Aria controls, тему и способы форматирования.

**Готово, когда:** RTL tests покрывают прямой nested адрес, загрузку/404,
not_analyzed/success/failed-with-previous-result, start/conflict/retry,
active adoption и reconnect, stale/disabled root, несколько streams/tags,
raw disclosure и переход между файлами без чужих ответов. Реальный браузер:
1440 и 375 px, light/dark, keyboard focus, длинный path и tags; нет blank
screen, document overflow или перекрытой кнопки. Снимки ориентировать на
`docs/app-design/screenshots/v2/sources-*.png`, не требовать совпадения с
несуществующим макетом инспектора.

### 10. Пройти пользовательский сценарий и независимую приёмку

**Зависимости:** 1–9. **Область:** документация и evidence этапа.

Обновить `README.md` и `docs/design/deployment.md`: scan всё ещё подтверждает
аудиопоток, отдельный ручной анализ сохраняет технические данные; source
read-only, режим только in-place, повторный анализ явный. В черновике
to-decompose отметить поставленную часть и оставить fingerprint/SHA/staged/
автопланирование будущими этапами, не объявляя черновик целиком выполненным.

На реальных PostgreSQL, подготовленном Setup и managed `ffprobe` пройти:
scan небольшого fixture root → открыть audio location → анализ → REST/SSE
и инспектор → reload → явный повтор → контролируемый failure с сохранением
старого результата → изменить source → успешный scan отвязывает variant →
новый анализ → удалить root без удаления файлов. Сравнить известные теги и
параметры fixture с API, не только увидеть зелёный статус. Проверить прямой
URL, restart с queued/running job, desktop/узкий viewport и обе темы.
Source bytes до/после сравнить проверочным digest в QA; это не runtime
SHA-256 feature.

Передать независимому проверяющему revision, этот план, diff, отчёт gate,
ручные результаты и screenshots. Он повторяет `task verify`, проверяет
каждый шаг и критерий ниже и выдаёт COMPLETE либо точные blockers.
Markdown не требует prose tests; отсутствие LSP для него отмечается отдельно.
В `done/` переносить только после COMPLETE и выполнения всех критериев,
а не после одного успешного unit test.

**Готово, когда:** независимый отчёт сопоставляет все 10 шагов с конкретными
файлами/tests и живым сценарием; gate exit 0, непроверенные условия среды
перечислены без объявления их успешными.

## Критерии готовности и карта проверки

| Критерий | Доказательство |
| --- | --- |
| Фактические format, все audio streams, source tags и raw JSON доступны после ручного анализа. | Шаги 3–5, 8–10: fixtures parser + реальный ffprobe/API/inspector. |
| Analysis operation и River job атомарны, переживают disconnect/restart и безопасно повторяются. | Шаги 6–8: PostgreSQL/River rollback, delivery, recovery, stale retry. |
| Устаревший source/snapshot не получает готовый результат; unchanged scan сохраняет variant. | Шаги 2, 5–7: barriers, DB fencing, reconciliation tests. |
| Scan, root mutations и tool move/delete не разрушают active analysis. | Шаги 1, 6–7: constraints и конкурентные integration tests. |
| Ошибка сохраняет предыдущий результат; orphan без digest очищается после освобождения ссылок. | Шаги 2, 7, 9–10: rollback, retention и failed UI. |
| GET/REST/SSE/generated client согласованы; nested URL и reconnect работают. | Шаги 8–10: API, RTL и браузер на реальном сервере. |
| Source bytes не изменены; нет фальшивых quality/fingerprint/SHA/staged возможностей. | Шаги 5, 9–10: filesystem tests, digest QA, review области. |
| Все шаги завершены, `task verify` exit 0, независимый вердикт COMPLETE. | Шаг 10: evidence на точной ревизии. |
