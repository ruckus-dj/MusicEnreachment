# Сверка реализации с дизайном на 2026-10-01

Ревизия: `6a846af`. Сопоставлены текущие исходники с `docs/design/`,
`docs/app-design/` и планами `docs/plans/`. Ниже отдельно отмечены нарушения
уже завершённых этапов, расхождение формулировок и функции, которые в
завершённые этапы не входили. Ссылки на строки относятся к указанной ревизии.

## Нарушения поставленного среза и общих правил дизайна

1. **Недоступность корня не отражается в его состоянии.** План требует хранить
   доступность и безопасную ошибку корня
   (`docs/plans/done/03-source-inventory-first-slice.md:85-86`), а при
   недоступности показывать прошлый инвентарь вместе с ошибкой
   (`docs/design/deployment.md:70-73`). Схема содержит `status`, `safe_error`
   и вариант `unavailable`
   (`backend/internal/migrations/20261003000000_source_inventory_first_slice.tx.up.sql:10-18`),
   но успешное применение сканирования только устанавливает `available` и
   очищает ошибку (`backend/internal/persistence/source_repository.go:319-320`).
   В остальных production-путях записи статуса `unavailable` нет:
   `backend/internal/persistence/source_repository.go:18` объявляет константу,
   которая используется лишь тестовой фикстурой
   (`backend/internal/api/sources_test.go:352`). При ошибке чтения корня
   операция сообщает собственную ошибку, но `GET /sources` возвращает старый
   `available` или `unknown`. UI умеет показывать `unavailable`
   (`frontend/src/features/sources/sourcesApi.ts:38-42`,
   `frontend/src/features/sources/SourcesScreen.tsx:269-274`), но backend
   никогда не поставляет это состояние в production.
   Кроме того, детальная страница перечитывает корень после **успеха**
   операции, но не после failure
   (`frontend/src/features/sources/SourceScanControl.tsx:92-101`,
   `frontend/src/features/sources/SourceDetailScreen.tsx:59-77`); одного
   исправления записи в БД будет недостаточно для обновления статуса на уже
   открытом экране.
   Ошибка отдельного файла (`probe_error`) не равнозначна недоступности корня.

2. **Регистрация корня не работает на заявленной Windows amd64.**
   `docs/design/decisions.md:276-279` включает
   Windows amd64; `docs/design/deployment.md:26-32` требует абсолютный путь
   сервера. `settings.NormalizePath` принимает платформенный абсолютный путь
   (`backend/internal/settings/filesystem.go:12-14`), но ограничение
   `configured_path LIKE '/%'` отвергает Windows-путь вида `C:\Music`
   (`backend/internal/migrations/20261003000000_source_inventory_first_slice.tx.up.sql:14`).
   `POST /sources` возвращает общую ошибку регистрации
   (`backend/internal/api/sources.go:151-154`). Ограничение находится в уже
   применённой миграции: исправление требует *новой* пары миграций, а не
   изменения старого файла. Локальные тесты на Unix не доказывают
   работоспособность Windows.

3. **Диагностический режим блокирует scan, но не изменения корней.**
   Дизайн требует блокировать продуктовые операции при несовпадении платформ
   (`docs/design/decisions.md:108-111`).
   Создание, изменение и удаление корня проверяют только завершённость Setup
   (`backend/internal/api/sources.go:141-155,178-215`), тогда как запуск scan
   дополнительно проверяет платформу (`backend/internal/api/sources.go:266-274`).
   Следовательно, HTTP-мутации корней доступны в диагностическом режиме,
   хотя изменение runtime settings и инструментов в нём закрыто.
   Это нарушение **общего** правила дизайна, а не буквально указанного
   объёма плана 03: тот явно упоминает platform gate только для scan
   (`docs/plans/done/03-source-inventory-first-slice.md:256`).
   Чтение состояния для диагностики не следует смешивать с мутациями.

## Расхождение документа и фактической реализации

- Документы требуют запрос версии через `--version`
  (`docs/design/decisions.md:127`;
  `docs/design/external-tools.md:75-76,102`). Реализация запускает `-version`
  (`backend/internal/integrations/tools/tools.go:78-82`,
  `backend/internal/integrations/tools/lifecycle.go:438,545`): пояснение в
  исходнике указывает, что `fpcalc` отклоняет двойной дефис, а macOS FFmpeg
  возвращает ненулевой код. Это обоснование кода, а не результат запуска
  реальных бинарников в этом аудите. Исправлять нужно текст требования,
  сохраняя используемую форму аргумента.

## Граница завершённого этапа

План `docs/plans/done/03-source-inventory-first-slice.md:11`,
`docs/plans/done/03-source-inventory-first-slice.md:20-28` и
`docs/plans/done/03-source-inventory-first-slice.md:44-55` явно
ограничивает инвентарь проверкой аудиопотока через `ffprobe` и исключает
`media_variant`, SHA-256, fingerprint, staged mode и полный технический анализ.
Долгосрочный дизайн предусматривает эти возможности
(`docs/design/decisions.md:298-333,395-408`), но их отсутствие сейчас **не** является
невыполнением закрытого плана. Черновик
`docs/plans/to-decompose/source-inventory-and-analysis.md:3-11` прямо запрещает
брать всю область в реализацию без повторной проработки.

## Сопоставление графического дизайна с текущим React

Графический прототип — отдельная предлагаемая UX-спецификация, а не
production-приложение (`docs/app-design/02-screens.md:1-3`,
`docs/app-design/AGENTS.md`). Текущий shell маршрутизирует только Setup,
Sources и Settings (`frontend/src/routes/AppShell.tsx:15-25,138-151`).
Поэтому отсутствие «Входящих», сопоставления, медиатеки, релиза, позиции,
публикаций и отдельного экрана операций (S02–S08, S11–S13) — **ожидаемый
непоставленный объём**, а не дефект завершённого плана 03.

- **S09, уже частично реализован.** Список и страница корня, форма добавления,
  read-only пояснение и инвентарь существуют
  (`frontend/src/features/sources/SourcesScreen.tsx:49-59,119-165`,
  `frontend/src/features/sources/SourceLocations.tsx:160-230`).
  Эталон `docs/app-design/screenshots/v2/sources-light-1440.png` использует
  плотную широкую таблицу и постоянную боковую навигацию, а приложение
  ограничивает источники `max-w-5xl` и использует верхнюю навигацию
  (`frontend/src/features/sources/SourcesScreen.tsx:154-158`,
  `frontend/src/routes/AppShell.tsx:91-117`). Это наблюдаемое отличие
  композиции; план 03 требует сравнить снимки как ориентир, а не обещает
  пиксельное совпадение. Processing mode, количество уникальных variants и
  инспектор технического анализа из S09/S10 ещё не входят в поставленный
  срез (`docs/plans/done/03-source-inventory-first-slice.md:44-55`).
  Отдельная команда «Проверить доступность» из S09
  (`docs/app-design/02-screens.md:104-114`) пока отсутствует: текущий
  `SourceScanControl` предлагает только «Сканировать»/«Повторить»
  (`frontend/src/features/sources/SourceScanControl.tsx:230-262`);
  отдельный API-контракт проверки доступности ещё не утверждён.
- **S14/S15, частично реализованы; темы и единая геометрия не реализованы.**
  Настройки output/format, MusicBrainz и LRCLIB уже есть
  (`frontend/src/features/settings/SettingsScreen.tsx:94-101,521-620`);
  именование, расписания и будущие интеграции остаются непоставленным
  объёмом S14/S15 (`docs/app-design/02-screens.md:158-175`).
  `docs/app-design/DESIGN.md:18-65`
  фиксирует system/light/dark и постоянный desktop shell. Production shell
  задаёт светлый фон и текст жёстко
  (`frontend/src/routes/AppShell.tsx:82-91`), а CSS источников содержит
  только светлые токены (`frontend/src/features/sources/sources.css:1-16`);
  настройки не содержат theme state
  (`frontend/src/features/settings/SettingsScreen.tsx:81-121`).
  Это разница между утверждённой целевой UX-концепцией и текущим UI,
  **не требование завершённого этапа инвентаря**. Отдельный этап темизации
  потребует решить место хранения личной UI-настройки: дизайн прямо оставляет
  этот вопрос открытым (`docs/app-design/DESIGN.md:39`).
- **S01/S16.** Одноразовый Setup и управление проверенными инструментами
  представлены React-компонентами
  (`frontend/src/routes/AppShell.tsx:138-151`,
  `frontend/src/features/setup/SetupManager.tsx:53-78`,
  `frontend/src/features/settings/SettingsScreen.tsx:81-121`).
  При этом S01 предполагает показать по инструменту факт upstream checksum
  и результат проверки версии (`docs/app-design/02-screens.md:11-14`), а
  доступная строка выбора показывает только identity и source
  (`frontend/src/features/setup/SetupTools.tsx:258-266`); S16 просит active
  path, checksum, previous version и критическое предупреждение при утрате
  рабочего инструмента (`docs/app-design/02-screens.md:177-187`), а карточка
  показывает только версии и строку «Нет активной версии»
  (`frontend/src/features/settings/SettingsScreen.tsx:709-753`).
  Это различия текущего UI с полной UX-спецификацией, а не недоказанное
  нарушение объёма пунктов 10–11 плана 02
  (`docs/plans/done/02-setup-manager-and-managed-tools.md:730-797`).
  Точный пиксельный вердикт без снимка запущенного production UI здесь
  не заявляется.
- **Общие элементы.** Эталон использует единую зелёную палитру навигации
  (`docs/app-design/DESIGN.md:18-37`), тогда как общая кнопка применяет
  Tailwind `stone` (`frontend/src/components/AppButton.tsx:3-9`), а shell
  использует постоянную светлую верхнюю шапку вместо фиксированных sidebar
  и topbar (`frontend/src/routes/AppShell.tsx:81-117`,
  `docs/app-design/DESIGN.md:45-53`). Это композиционное расхождение с v2,
  не изменение бизнес-операций завершённых планов.
