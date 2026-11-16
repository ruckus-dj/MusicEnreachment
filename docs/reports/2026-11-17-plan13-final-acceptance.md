# План 13: итоговая приёмка

Проверяемая application-ревизия: `ac679830`, полный hook и CI `43712658879`
успешны. Этот отчёт фиксирует проверенные действия и ограничения, не новые
продуктовые требования. Общий план пока открыт.

## Защита данных

Контрольный стенд 8787 не используется. Приёмка выполняется на 18080;
существующие два CURRENT и три исходника сохраняются. Для полного Setup-маршрута
готовится отдельная временная БД и отдельные каталоги, без reset исходного стенда.

## Выполнено

- Реальный browser: из общей навигации открыта Library, затем карточка
  `Plan13 Acceptance Artist`. В English сводка, availability/publication counts,
  отсутствие даты и Show positions отображаются по-английски. Старое замечание
  о русских подписях этой карточки больше не воспроизводится на данной ревизии.

## Ещё не проверено в этом проходе

- Полный Setup → source → analysis → incoming → matching → publication через UI.
- Итоговая сверка с React-прототипом, RU/EN, обеими темами и viewport/keyboard.
- Populated evidence/overrides и агрегированный preview с реальным backend.

## Текущий проход Setup

В контейнере создана отдельная БД `plan13acceptance_final_20261117`; исходная
БД и bootstrap сохранены без изменений. Только acceptance-процесс 18080
переключён PID 2557 → 21997. Отдельные каталоги и копия собственного третьего
FLAC подготовлены в `plan13acceptance/final-acceptance-20261117`, source SHA-256
зафиксирован до запуска. Это provisioning входных файлов, не API-обход UI.

Свежий browser context штатно открыл Setup. Через directory picker выбраны
новые tools/output; через UI скачаны и проверены FFmpeg 9.0.2 и fpcalc 1.6.2,
обе версии активны. Выбран Source format; удаление исходников оставлено выключенным.
Запущена проверка публичного MusicBrainz. Остальные шаги ещё не завершены.

## Найденный blocker RU/EN

Активный `LibraryReleaseScreen` и `newReleaseCoverEditor` не подключены к
локализации и содержат собственные русские labels/status/fallbacks при English.
Соседние дочерние редакторы уже локализованы. Исправление выполняется отдельно;
raw server diagnostics, музыкальные данные и authority/state guards сохраняются.

## Сквозной browser маршрут `ac679830`

Setup завершён кнопкой Finish setup; без root показано Add source. Через это
действие открыт Sources и зарегистрирован новый in-place root. После регистрации
UI показал queued scan, затем успешный inventory с одним audio-файлом. Отдельная
кнопка Scan не нажималась: регистрацию и последующий scan/analysis нельзя описывать
как два явных пользовательских запуска. Incoming показал 1/1 ready и saved data.

Через Own catalog выполнены поиски и созданы Local artist, recording и release
`Final Acceptance … 20261117`, с одной позицией. Через Incoming → Local matching
выбраны релиз/позиция, нажаты Preview assignments и Confirm matching. UI показал
manual revision 1 и сохранённую связь; Library показала 0 published.

Через Library → artist → Show positions → позиция открыт экран релиза. Нажаты
Preview publication и Publish. Без ручного refresh UI показал CURRENT и завершение
operation; generation `e29d60f3-07a5-4043-a2a5-8394501451fa`. Все application mutations
выполнены browser-контролами, без API mutation или ручных обходных URL.

Физический FLAC найден только в новом output; ffprobe подтвердил FLAC и теги
ALBUM/ARTIST/TITLE новых Local записей. Readback: `current-probe.json`, `current.sha256`
в `final-acceptance-20261117`. Копия исходника и оба baseline CURRENT/три прежних
исходника сохранили SHA-256. Смешанный RU/EN на экране релиза воспроизведён браузером.
Полный маршрут функционально пройден, но визуальная/RU/EN приёмка не завершена.

## Исправление release RU/EN

Локализованы собственные подписи release screen/cover editor; сохранённые
feedback/errors переведены на semantic descriptors с форматированием при render.
Даты используют текущую locale. Server detail, safe_error, неизвестные Error.message,
пути, музыкальные значения и raw evidence сохранены. Existing tests переведены на
явную locale, добавлен locale-flip lyrics draft/terminal-notice regression без
дополнительных reads. Независимое ревью выявило один устаревший RU selector в EN
тесте; он исправлен. Biome formatting/import assists и Impeccable detector прошли.
Полный gate и browser-проверка нового кода ещё не выполнялись.

### Результат `a053c93d`

Обычный полный hook прошёл, commit отправлен; CI `43977514007` выполняется.
Свежий acceptance-процесс обновлён PID 21997 → 28202 с той же отдельной БД.
Browser после reload показал English release/cover/lyrics/publication labels
и английскую дату. Lyrics draft сохранился EN→RU→EN в том же DOM node без API
requests; затем очищен без сохранения. RU/EN × light/dark × 1440×900/390×844:
cover heading доступен, page overflow отсутствует. Это не полная визуальная
сверка с прототипом и не проверка всех cover/lyrics failure states.

Aggregate preview на `ac679830` после первой публикации без metadata-правок
показал Update и различия `absent → []` для четырёх omitted artist/MBID tags.
Причина исследуется отдельно; повторное подтверждение не выполнялось.

CI `43977514007`, attempt 1: verify/Linux/macOS/Docker smoke прошли; Windows
завершился ошибкой API-package cleanup: `supervisor did not respond to shutdown;
force-terminated without confirming PostgreSQL cleanup` (116.805s). Публикация
контейнера skipped. Failed-step log сохранён в
`final-acceptance-20261117/windows-a053c93d-attempt1.log`, передан на независимый
разбор; повтор без диагноза не запускался, gates/таймауты не менялись.

Независимый разбор обнаружил `PASS` перед package cleanup failure, без assertion
failure/panic/timeout; PostgreSQL сообщил shutdown, но supervisor не подтвердил
cleanup. Это не успешный job и не установленная точная причина. После разбора
запущен только failed-job rerun, без изменения gates/таймаутов.

Settings на реальном backend `a053c93d`: configuration healthy, активные tools
отображены. Несохранённый concurrency draft `7` сохранился при blur и EN→RU→EN
без API requests, затем восстановлен в `4` без Save. Keyboard Tab из поля
перевёл фокус на Save concurrency с `:focus-visible` и solid 2px outline.
Обнаружен отдельный сохранённый network failure обновления genre catalog;
его диагностика не подменяется успешной MusicBrainz connectivity проверкой.

Attempt 2 CI `43977514007` также failed: тот же supervisor shutdown/cleanup,
теперь migrations package (275.848s). Лог `windows-a053c93d-attempt2.log`.
Третий автоматический rerun не выполняется: начат разбор supervisor/IPC lifecycle.
Полная CI остаётся красной; зелёные prerequisite jobs не заменяют её результата.

Независимый архитектурный разбор подтвердил false Update: валидатор результата
считает desired clearing `[]` выполненным при физически отсутствующем теге,
а preview считает такое отсутствие изменением. Исправляется effective-output
comparison; clearing intent и фактический CURRENT сохраняются. Непустое текущее
значение против `[]` остаётся изменением, `['']` не приравнивается к отсутствию.
Отдельный hardcoded diagnostic `Comparison status: blocked` не является admission
решением и не доказывает фактическую блокировку публикации.

Разбор Windows уточнил границу: owner ждёт process exit отдельного supervisor
из embedded-postgres/v2 alpha.3 после EOF lease, затем watchdog принудительно
завершает его. Directory cleanup начинается позже и не объясняет watchdog.
В attempt 1 PostgreSQL штатно завершился примерно за 6s; это не подтверждает
завершение supervisor. Причина зависания не установлена, проверяется upstream.
Не увеличиваются таймауты, ошибка cleanup не игнорируется, повтор CI без
дополнительных оснований не запускается.

Upstream research: shutdown-related source в pinned alpha.3 и актуальном v2.0.1
совпадает; известное исправление именно этого watchdog failure не найдено.
Обновление версии на предположении о таком fix не выполняется. Для точной причины
нужны phase-specific данные внутри отдельно скачиваемого supervisor, а не снятие
ошибки на стороне приложения. Источники:
https://github.com/fergusstrange/embedded-postgres/blob/v2.0.0-alpha.3/internal/supervisor/supervisor.go
и https://github.com/fergusstrange/embedded-postgres/blob/v2.0.1/internal/supervisor/supervisor.go.
