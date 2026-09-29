# Обновление внешних инструментов

## Зафиксированные требования

- Инструменты: `ffmpeg`, `ffprobe` и `fpcalc`.
- Бинарники не включаются в Docker-образ и не берутся из системного `PATH`.
- Инструменты должны работать и при локальном запуске без Docker.
- Обычный Settings screen загружает runtime settings и configuration health из
  `GET /settings` и показывает health problems. При platform mismatch UI
  отключает platform-dependent tool actions; обычные controls остаются
  отображены/редактируемы, хотя Settings mutation routes backend также отклоняют
  при platform diagnostic state.
- Settings позволяет менять output directory и publication format; MusicBrainz
  mode/base URL с отдельной проверкой; LRCLIB и log level. Изменение tools root
  выполняется только через move flow. Log level применяется backend без
  перезапуска.
- Для `ffmpeg` package и `fpcalc` отдельно показываются active, installed и
  доступные версии. Установка после Setup добавляет verified version, не
  активируя её; activation выполняется отдельным явным действием. Удаление
  доступно только для inactive installation без занятой операции, а backend
  повторно запрещает удаление active/occupied installation.
- Install dialog показывает серверные target paths и conflicts. Move dialog
  выполняет server preflight и показывает число managed files и conflict paths;
  полный список move targets API/UI не раскрывает. В обоих flow серверный token
  ограничивает подтверждение актуальным набором preflight conflicts. Запуск
  создаёт River operation; прогресс читается из REST snapshot, SSE используется
  как wake-up с повторным REST-read после connect/reconnect/error. Failed
  operations предлагают Retry и Dismiss.
- Каталог хранится только в памяти клиента; timestamp последней успешной
  проверки — в client storage. После загрузки Settings автоматическая проверка
  выполняется не чаще одного раза за 24 часа (один запрос для каждого package).
  Внутри cooldown reload показывает timestamp без скрытого запроса. Manual
  Refresh всегда запрашивает каталог, но ничего не устанавливает и не
  активирует.
- Settings changes re-read `GET /settings` before reporting success; installation
  and operation completion re-read server settings/installations or use the
  server-returned operation snapshot. UI checks mutation responses before
  reporting success; it does not report success optimistically.
- Целевые платформы: Linux и macOS в вариантах `amd64` и `arm64`, Windows
  `amd64`. Windows `arm64` явно не поддерживается. Для целевой платформы
  каталог предлагает только доступные совместимые готовые бинарники.
- Готовые `ffmpeg` и `ffprobe` для Windows/Linux берутся из
  https://github.com/BtbN/FFmpeg-Builds/releases; используется GPL static
  release variant.
- Готовые `ffmpeg` и `ffprobe` для macOS берутся из
  https://ffmpeg.martin-riedl.de/ — GPL release builds, никаких snapshots.
- Upstream-проект и source code FFmpeg: https://ffmpeg.org/ и
  https://github.com/FFmpeg/FFmpeg. MeloTrove инициирует прямую загрузку
  со стороннего источника и запускает отдельный executable, но не включает и не
  распространяет FFmpeg в собственных artifacts.
- Готовый `fpcalc` берётся из GitHub Releases проекта AcoustID Chromaprint:
  https://github.com/acoustid/chromaprint/releases.
- Setup Manager требует persistent tools-directory. После настройки Settings
  предоставляет preflight/operation flow для переноса managed files; root
  изменяется только по завершённой серверной операции переноса.
- Приложение самостоятельно получает готовый совместимый выпуск инструментов;
  offline bootstrap и ручная загрузка binaries оператором в текущий этап не
  входят.

## Утверждённый lifecycle

1. Setup Manager требует persistent tools-directory, определяет OS и
   архитектуру, затем предлагает доступные совместимые версии FFmpeg package
   и `fpcalc`. Каталог содержит только numbered releases (не snapshots или
   prereleases); macOS Intel (`darwin/amd64`) зависит от наличия релизов
   совместимой архитектуры у утверждённого источника.
2. В PostgreSQL хранятся только локальные installations и выбранные active IDs;
   compatible upstream catalog запрашивается backend по запросу UI и остаётся в
   памяти frontend.
3. Проверка обновлений получает описание готового release из заранее заданного
   доверенного HTTPS-источника; произвольный URL из UI не принимается.
4. Установка скачивает готовый совместимый release в staging. Если источник
   публикует SHA-256 checksum, backend сверяет её с идентичностью preflight и
   проверяет загруженный archive; подписи release не проверяются. Затем
   запускаются `ffmpeg --version`, `ffprobe --version` и `fpcalc --version`,
   сверяя результат с ожидаемой версией release.
5. В первоначальном Setup успешно проверенная выбранная версия атомарно
   становится активной. После Setup установка только добавляет версию, а
   переключение active version выполняется отдельным явным действием; прежняя
   версия остаётся доступной для ручного отката.
6. В Docker persistent volume хранит скачанные tools versions. Все успешно
   установленные версии сохраняются до явного удаления оператором.
7. В Setup оператор выбирает версии и явно запускает их установку. После Setup
   Settings предоставляет catalog, preflight/install, отдельные activation и
   delete, а также move flow. Установка не активирует новую версию. Каталог
   проверяется автоматически не чаще одного раза за 24 часа по persistent client
   timestamp; manual Refresh обходит интервал, запрашивает каталог и не запускает
   install/activation. Завершённый Setup Manager повторно не используется для
   управления версиями.
8. Ошибка скачивания, проверки целостности или `--version` не активирует новый
   бинарник. Сервис сохраняет предыдущую рабочую версию, а при её отсутствии
   оставляет Setup Manager незавершённым и показывает оператору критический
   alert.

## Ограничения

- Целевые платформы: Linux и macOS в вариантах `amd64` и `arm64`, Windows `amd64`.
  Windows `arm64` пока не поддерживается: в утверждённых releases Chromaprint
  отсутствует готовый `fpcalc` для неё. Другие OS и архитектуры не поддерживаются.
- Для целевой платформы необходимы совместимые готовые бинарники всех трёх
  инструментов из утверждённых доверенных источников.
- Проверка `--version` подтверждает запуск и соответствие ожидаемому release.
  Опубликованная SHA-256 checksum проверяется при её наличии; release signatures
  не проверяются.
- Приложение не выполняет постоянный integrity monitoring managed binaries и не
  требует backup tools-directory. Ручное изменение его файлов находится вне
  управляемого lifecycle и остаётся ответственностью администратора.
- Tools-directory может содержать посторонние файлы. Managed считаются только
  точные executable paths installations, созданных приложением и записанных в
  БД. Приложение не сканирует tools root в поисках неизвестных файлов и не
  удаляет их; неизвестный файл в целевом пути требует явного подтверждения
  overwrite.
- Платформа экземпляра (`GOOS`/`GOARCH`) фиксируется в БД при первом запуске и
  не меняется. Несовпадение после переноса БД оставляет UI и диагностику
  доступными, но readiness завершается ошибкой, а Setup/product operations
  блокируются. Автоматической миграции tools нет.
