# 13. Основной интерфейс по утверждённому дизайну

Основание: запрос владельца привести приложение к техническому контракту и
графическому эталону, а не маскировать пустую главную страницу. Оболочка и API
основного каталога уже существуют; программа ниже описывает оставшиеся расхождения
и проверенные части реализации. Существующие анализ, сопоставление и публикация
переиспользуются с сохранением concurrency и recovery authority.

Единственный текущий графический эталон — standalone React-макет
`docs/app-design-react/`. Его `DESIGN.md`, `02-screens.md`, `03-data-and-evidence.md`
и `PARITY.md` описывают текущий standalone React-прототип и границы переноса;
runtime прежнего `docs/app-design/` удалён решением владельца от 2026-11-03.
Единственный продуктовый контракт — `docs/design/decisions.md`; связанные
технические документы уточняют реализацию, а макет задаёт визуальную форму.
Снимки удалены по решению владельца от 2026-11-01; новые screenshot-артефакты не
добавляются. Браузерная приёмка сохраняется. Демо-данные прототипа не переносятся.

## Исторические свидетельства прежнего этапа

Промежуточный статус 2026-10-29: каталог, ручное создание, маршруты и список
текущих публикаций реализованы; дополнительное отображение сохранённых
сведений проверено локальным `task verify` (445 frontend-тестов). План не
закрыт: повторная полная матрица CI, визуальная и сквозная приёмка остаются.
Дополнение на `1140cef`: стенд восстановлен, локальный релиз из 12 файлов
создан; первая позиция опубликована в прежнюю физическую output-папку,
проверена в CURRENT и каталоге. Исследуется зависшее отображение статуса
операции в открытой карточке; полная приёмка пока не завершена.
Свидетельства и ограничения — `../../reports/2026-10-29-library-interface-follow-up.md`.

Пункты выше фиксируют состояние и согласования на 2026-10-29, а не актуальный
бэклог. Согласование самостоятельного создания collection-релиза от 2026-10-28
заменено решением владельца 2026-11-04: библиотечная запись появляется только
через provider record → incoming matching → library.

## Оставшаяся программа исправлений и паритета

Пункты — направления программы, а не неизменный порядок коммитов. Следующий
ограниченный вертикальный срез выбирается по фактическому коду, текущему контракту
и зависимостям; перед реализацией критерии сверяются заново. Отметка подпункта
не закрывает всю область. RU/EN и перенос presentation выполняются вместе с
затронутыми экранами, а не только после остальных направлений.
Product contract — `docs/design/decisions.md`; связанные design-документы
уточняют предметную область. Prototype port переиспользует пригодные формы и
presentation modules; утверждённые UI-изменения синхронизируются в production и standalone
React-прототипе. Не добавлять backwards compatibility: старые данные/потоки не
нужно сохранять ради совместимости. Это не разрешение удалять существующие
данные владельца или потерять текущее recovery authority.

1. [x] **Удалить автоматическую перезапись metadata.** Убрать автоматическое
   файловое обновление metadata существующего CURRENT без явного выбора,
   preview и подтверждения. Provider cache refresh и автоматическое улучшение
   AUDIO сохраняются; последнее сохраняет
   metadata/cover/lyrics CURRENT. Правила распространения provider-данных в
   library metadata не меняются. **Приёмка:** после реального refresh и обработки
   reconciliation CURRENT generation и metadata не изменились, rewrite не
   поставлен; metadata-only событие завершается, не зацикливается и не ждёт
   provider graph. Лучшее AUDIO по-прежнему допускается независимо от metadata.
   Реализовано в `58eec59a`, локальный полный gate пройден 2026-11-04.
   Последующая ревизия `68c3d56a` прошла обычный hook и полную
   [CI-матрицу](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42273104143).
   Свидетельства и ограничения первоначальной проверки —
   [отчёт](../../reports/2026-11-04-publication-metadata-policy.md).
2. [x] **Staged/reset/source deletion lifecycle.** Сверить staged execution,
   reset и удаление roots с актуальным контрактом. Добавить отдельный общий
   `work_directory` в Settings/UI/БД без default и env; Setup и регистрация root
   не требуют его, staged processing требует. При копиях или использующей каталог
   работе смена запрещена, автоматического переноса нет. **Приёмка:** смена output
   запрещена при running/claimed execution; queued jobs и ссылки БД на прежний
   output сбрасываются транзакционно. Физические файлы не удаляются, автоматической
   перепубликации нет; медиатека, source links, inventory и analyses сохраняются.
   Staged-копии и их учёт независимы от output reset; jobs не сохраняются,
   обработка сама не возобновляется. Явный новый запуск может переиспользовать
   подходящую копию; отсутствующая ссылка очищается, копия при необходимости
   создаётся заново. Успешные результаты независимых шагов анализа сохраняются;
   сохранение jobs и автоматическое продолжение operation не обещаются.
   Удаление root сохраняет host sources, library и CURRENT; удаляет его locations
   и app-owned staged copies. SHA-256 variants остаются cache, variants без digest
   удаляются после последней location. Все `track_source`-связи variant удаляются
   после его последней location; выбор очищается без fallback. Подтверждаются точный
   server path и число inventory entries; queued/running/claimed задачи root
   блокируют удаление, failed-задачи и их временные копии удаляются без возможности
   retry. Покрыть shared variants, missing copy, ошибку cleanup, конкуренцию и
   rollback. По решению от 2026-11-04
   проверять отсутствие пересечения work_directory с sources/tools/output
   при изменении любой стороны; не требовать пустоты или одного volume.
   - [x] Текущее исполнение `60188468`: общий `work_directory` в Settings/UI/БД
     без default и env; staged processing требует его, Setup и регистрация root —
     нет. Пересечение проверяется со всеми roots, включая tools; смена при
     копиях или использующей каталог работе запрещена (lock/immutability),
     переноса нет. Reset отменяет jobs, но сохраняет копии и записи. Полный hook
     и завершённый CI:
     [42253784784](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42253784784).
    - [x] Итоговая сверка удаления root и reset lifecycle завершена на `331eea29`:
      существующие DB/native-сценарии покрывают критерии пункта; новых механизмов
      и тестовых матриц для закрытия не потребовалось.
   - [x] В `91d8e281` удаление последней location при удалении root очищает
     все `track_source`-связи без fallback и изменения CURRENT. В `9b5725de`
     добавлены приватная publication source copy с size/mtime-проверками и
     сохранение inventory при неполном scan; оба среза прошли полный hook и CI.
     Ограничения — [отчёт source freshness](../../reports/2026-11-07-source-freshness-safety.md).
   - [x] В `686028c1` durable отзыв identity при обнаруженном size/mtime mismatch
     реализован для analysis/scan; полный hook и вся CI прошли:
     [42360466600](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42360466600).
     Tombstones, execution history, artifacts и publication/recovery ownership
     сохраняются; это не завершение staged/root failed-task lifecycle.
     Свидетельства — [отчёт](../../reports/2026-11-07-source-identity-invalidation.md).
   - [x] В `4c7e2633` root-specific cleanup staged-копий и failed analysis tasks
     прошёл полный hook и всю [CI 42365879090](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42365879090).
     Publication/recovery holds пока блокируют
     удаление: согласованный safe abandonment остаётся отдельным следующим срезом.
      Свидетельства — [отчёт](../../reports/2026-11-07-root-staged-task-cleanup.md).
    - [x] В `8431f4bf` и `c475140b` добавлен safe abandonment ordinary failed
      publication и защищённого source-cleanup recovery внутри удаления root.
      Frozen evidence остаётся неисполняемым архивом; CURRENT/library/output и
      namespace сохраняются, active/unreturned owners блокируют удаление.
      Оба среза прошли обычный полный hook и всю CI, включая контейнер:
      [42369569762](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42369569762),
      [42408036183](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42408036183).
       Ограничения приёмки — [отчёт](../../reports/2026-11-07-root-publication-abandonment.md).
    - [x] Итоговые доказательства: `runDeletingRootCleansArtifactsAndAnalysisState`
      покрывает canonical staged path, отсутствующую копию, сохранение output и
      удаление failed task/job; root abandonment tests — ошибку callback и rollback;
      output reset tests — transactional cancellation, сохранение независимых
      analysis results и старых файлов. Полный gate и вся CI `331eea29`, включая
      публикацию контейнера, прошли. Это service/DB/native-приёмка; общий browser
      E2E и визуальная приёмка остаются в пункте 11.
3. [ ] **Recovery и явная republish.** Устранить расхождения восстановления и
   повторной публикации с утверждёнными правилами. **Приёмка:** interrupted,
   frozen target при актуальной authority регистрируется по соответствующим
   тегам/техническим параметрам без транскодирования и доказательства происхождения
   из source; дата файла и побайтовая идентичность не требуются. Неподходящий или
   отсутствующий target ведёт к полному retry. Retry ошибки после replacement без
   DB commit полностью повторяет подготовку и замену. Явное forced republish
   всегда полностью готовит candidate из выбранного source и не превращается в
   unchanged; обычный preview unchanged ничего не пишет. Retry ошибки и forced
   republish различаются; initial/repeat/forced publication используют общий
   workflow точного preview и подтверждения.
   Покрыть прерывание после установки до DB commit, отсутствующий source,
   stale authority и completed redelivery; не возвращать step journal/receipts.
   По решению от 2026-11-04 republish применяет текущие format/path settings и
   эффективные metadata/cover/lyrics через preview; recovery сохраняет фактический
   прочитанный текст, но не выдумывает неизвестные источник/предупреждения его
    получения и не пересоздаёт подходящий файл ради этой справки.
    - [x] В `98d21f1b` ordinary first-audio interrupted delivery может принять
      подходящий frozen target без доступа к source; known-error retry продолжает
      полную подготовку. Полный hook и вся
      [CI 42415924806](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42415924806)
      прошли. Requested cover и специализированные intents пока не включены;
      [границы среза](../../reports/2026-11-07-interrupted-publication-recovery.md).
    - [x] Для interrupted same-path republish неподходящий target, включая
      изменённый извне, запускает полную подготовку из source. Разрешение замены
      привязано к наблюдённой identity; inspection mode сохраняется при ошибке
      подготовки до проверенной замены. Расширен существующий retry-сценарий,
      без нового журнала или схемы. Остальные критерии пункта остаются открытыми.
4. [x] **Metadata ownership и provider edges.** Привести ownership, provider
   relationships и границы влияния metadata к документации; Local create/update
   не создаёт library entries. **Приёмка:** самостоятельный recording и позиция
   трека со своими утверждёнными overrides по `data-model.md`; отдельная сущность
   position не вводится. Для title/duration действует только override позиции →
   recording, release в эту цепочку не входит. Metadata и credits сохраняют свои
   scopes; явные пустые tag overrides не смешиваются с отсутствием override;
   необязательный artist `sort_name` и производные sort credits: fallback к
   display name допустим только в проекции итоговых sort-тегов, не в сохранённых
   данных; коллизии и неполные MBID-группы обрабатываются по текущему контракту;
   cross-provider references сохраняют собственный namespace без зеркал.
   Удалить прямое создание library из API/UI, не запрещая внутреннее создание
   сущностей matching-процессом. Не выбирать незаданные правила распространения
   provider metadata или наследования tags/explicit/ISRC. Файловый `GENRE`
    разрешать по текущему контракту без изменения genres сущностей.
    - [x] Итоговая сверка на `b4500bef`: Local CRUD/matching сохраняет namespace
      ссылок и самостоятельные recording/position overrides; прямой create library
      удалён. Artist-only `sort_name`, projection-only fallback/disambiguation,
      collision clearing и explicit-empty overrides проверены существующими
      projector/service/DB/native/UI сценариями. Полный hook и вся
      [CI 42558342610](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42558342610)
      прошли, включая контейнер. Независимая сверка не нашла остатка критериев
      этого пункта. Полный metadata editor/визуальный паритет остаётся в пункте 6,
      общий real-backend E2E — в пункте 11.
5. [ ] **Фактический каталог.** Довести отображение/агрегацию до factual
   данных, разделив source/audio availability, provider entries, library и
   publications. **Приёмка:** unknown не показывается как ноль, edition и
   release-group не смешиваются, completeness основана на реальном состоянии;
   решение об агрегатах берётся только из утверждённых документов. «В наличии»
   означает доступный source ИЛИ существующий published audio; частичный объект
   входит и в «В наличии», и в «Не хватает»; изначально выбрано «Все».
   Missing rows объединяются только по MusicBrainz release-group identity;
   фактические editions плоские, просмотр missing edition требует явного выбора
   и не создаёт collection entities. До выбора нет track-list, число missing
   позиций неизвестно («—»); canonical/default edition не выбирается автоматически.
   Полный известный track-list не скрывается фильтром. Provider-числа отражают лишь
   известный partial cache scope; единица provider-агрегатов, смешанный числовой
   агрегат артиста и влияние доступности output на группировку остаются открытыми.
   - [x] Текущее исполнение `85cd19d6`: «В наличии» — доступный source ИЛИ
      существующий published audio; для source проверяются size/mtime inventory.
      Фильтр по умолчанию — «Все», частичный объект входит в обе выборки.
      Фильтрация и totals считаются до paging; раскрытый альбом запрашивает
      полный track-list независимо от фильтра; счётчики раздельные. Полный hook и
     завершённый CI:
     [42225902383](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42225902383).
   - [ ] Остаются provider-only entries и агрегаты, поэтому пункт целиком не
     закрыт.
6. [ ] **Prototype port: оболочка, Settings, matching и publication/metadata flows.** Портировать
   необходимые утверждённые сценарии из standalone React-макета, включая
   агрегированный preview и полный metadata ownership/редактирование. Presentation
   modules можно копировать/адаптировать с production-owned API/controllers;
   production не импортирует prototype code и не использует demo fixtures или
   execution models. Сохранять production concurrency/SSE и directory controllers;
   долгие серверные действия возвращают operation ID, SSE только будит повторное
   чтение snapshot. Settings сохраняет селекторы/переключатели автоматически,
   ручной ввод — явно; browser-only drafts, предупреждения об уходе и конфликты
   с изменёнными настройками проверяются по текущему разделу «Интерфейс» контракта.
   Переносить подходящие prototype ui, styles, tokens, i18n, metadata
   editor, MatchingDnD и LocalProviderScreen без обязательного shared package.
   **Приёмка:** production и prototype синхронны по согласованным presentation-
   сценариям; зелёный gate сам по себе не доказывает визуальный паритет.
   Metadata редактируются на месте без потери draft и изменения геометрии действий;
   disclosure-группы остаются одной вертикальной полноширинной колонкой по макету.
   На desktop от 1400 px matching inspector постоянно доступен снизу без перекрытия
   последних строк/действий; это проверяется браузером, не только CSS-assertions.
   Aggregate preview раскрывает результат до позиции, не создаёт library entities и требует
   явного подтверждения согласно контракту. Не создавать отдельный раздел
   «Публикации»: управление в контексте артиста/релиза/позиции. Состояния preview
   следуют контракту и presentation макета: невалидные metadata — unprocessable,
   отсутствующий файл/явно исключённая позиция — skipped с причиной; обычный
   unchanged ничего не пишет, forced republish проверяется отдельно в пункте 3.
   Точный metadata diff
   относительно последней успешной CURRENT отделён от completeness, AUDIO/path и
   filesystem drift; порядок multi-values значим, поисковая нормализация не
   применяется. Новую bulk atomicity/cancel policy не выбирать. Полноту Navidrome
   проверять по всей цепочке editor/storage/preview/writer/readback, а не JSON.
    - [x] `8bdb1207` и `2235177e`: in-place редактор девяти release fields и
      независимые title/sort_title overrides recording/позиции через существующие
      metadata API. Сохранены revision fences, draft при конфликте, retry чтения
      и немедленное инвалидирование preview. Полные hooks и CI 42561160523 /
      42563854883 прошли, включая контейнеры. Browser component fixtures проверили
      RU/EN, 390/1440, типизированные PATCH, exact strings, ordered lists и
      absent/explicit_empty; это не real-backend E2E и не полный metadata port.
    - [x] Текущее исполнение `68c3d56a`: отдельного раздела и API «Публикации»
     нет — CURRENT показывается в контексте позиции, независимо от source, через
     обновлённый controller; preview, SSE, cleanup и retry сохранены. Полный hook
     и завершённый CI:
     [42273104143](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42273104143).
   - [ ] Остаётся aggregate preview metadata, поэтому пункт целиком не закрыт.
7. [x] **Genres.** Завершить каталог и genre flows до Local UI. **Приёмка:**
   жанры сохраняют MBID и утверждённые правила каталога; состояния каталога и
   picker корректны согласно `docs/design/genres.md`. Полная загрузка идёт через
   действующую конфигурацию/governor MusicBrainz, полная версия заменяется атомарно;
   ошибка сохраняет последнюю успешную. Setup не блокируется, недельное обновление
   и явное обновление/статус доступны по контракту. Picker не разрешает свободный
   ввод; MBID и каноническое имя сохраняются, читаемое английское имя используется
   одинаково в UI/preview/GENRE. Наличие каталога не является условием публикации
   MusicBrainz genres или работы с пустыми Local genres; файловый fallback
   следует текущему контракту и не меняет genres сущностей.
   - [x] Реализован срез каталога: полный JSON download через общий MusicBrainz
     governor, MBID/каноническое/читаемое имя, атомарное сохранение последнего
     успешного набора; durable initial/weekly dispatch и ручное обновление через
     Operation/SSE; Settings показывает состояние/ошибку, API предоставляет поиск.
     Baseline дополнен без новой цепочки миграций; metadata и CURRENT не меняются.
   - [x] Добавлены MBID-bearing overrides artist/release/recording, production
     catalog-only picker и файловый fallback по текущему контракту. Сохранённые
     ссылки не переименовываются после обновления каталога; clear и отсутствие
     override различаются. Artist metadata revisions и порядок recording credits
     участвуют в authority/freshness. Добавлены API/DB/projection/native readback
     и UI-регрессии; native executor fixture использует синтетические команды,
     не подтверждая совместимость реальных ffmpeg/ffprobe.
     UI проверен на контролируемых API в 1440/390, обеих темах и RU/EN;
     это не полный upstream E2E. Подробности и границы —
     [отчёт](../../reports/2026-11-07-genre-metadata-flow.md).
   - [x] В `2c81f970` добавлен отдельный read-only provider genre baseline
     для artist/release/recording: same-scope accepted evidence текущего source,
     без файлового fallback, копирования в draft или PATCH. Полный hook и вся CI
     прошли; populated baseline проверен в восьми свежих browser contexts.
     Границы — [отчёт](../../reports/2026-11-07-provider-genre-baseline.md).
    - [x] В `fbd020dc` picker подключён к отдельному Local provider CRUD;
      provider-mediated incoming matching заменяет прямое создание library из API/UI.
      Полный hook прошёл. Сохранение пустых genres при недоступном каталоге проверено
      в браузере; CRUD и matching проверены PostgreSQL-сценариями.
    - [x] В `cc3cedda` Local genres читаются в publication через provider bindings,
      сохраняя scope/fallback и серверные имена; catalog lookup при публикации не нужен.
      В `265ce9f2` общий release preview переиспользует позиционные initial/retained
      repeat controllers с явным подтверждением и без записи unchanged/исключённых.
      PostgreSQL readback, native GENRE writer/readback и mixed aggregate UI покрывают
      жанровую цепочку; browser checks — production components с API fixtures,
      не real-backend E2E. Независимое review не нашло оставшегося genre-блокера.
      Полные hooks и CI обоих срезов прошли, включая контейнер:
      [42539934336](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42539934336),
      [42545356498](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42545356498).
      Более широкий republish и полный визуальный паритет остаются в пунктах 3/6;
      общий real-backend/визуальный E2E — в пункте 11.
8. [x] **Local create/update.** Использовать переиспользуемые формы для provider Local по
   `docs/design/local-provider.md`, с каталогом жанров из пункта 7.
   **Приёмка:** явное создание после поиска и редактирование только provider records,
   без auto-create/merge по имени, drafts или unusable statuses. Обязательны только
   business-поля provider-модели; `sort_name` необязателен. Local-релиз содержит один
   носитель; позиции ссылаются на уже существующие recordings, нумеруются из порядка
   и могут переопределить только title/duration по соответствующему контракту.
   Пустой track-list допустим; duration вводится секундами/М:СС, хранится в ms.
   Жанры и tags раздельны; недоступный genre catalog отключает только picker, не
   создание/редактирование/публикацию с пустыми genres. UI-удаление provider-записей
   сюда не входит, Local edits сами не публикуются. В медиатеку ведёт provider-
   mediated incoming matching; прямой create library путь удаляется в составе
    законченного заменяющего workflow, внутреннее создание matching сохраняется.
    - [x] `fbd020dc`, `cc3cedda` и `7da9776a`: отдельный «Собственный каталог»,
      provider-only artist/recording/release CRUD, существующие cross-provider
      references, жанровый picker и provider-mediated incoming matching.
      Прямой create library удалён из API/UI вместе с заменой. Последнее review
      исправило создание после поиска с неподходящими результатами, сохранение
      recording defaults в позициях, save-time context fence и первое открытие
      picker. PostgreSQL CRUD/matching и UI picker regression прошли; полный hook
      и вся [CI 42547720179](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42547720179)
      успешны, включая контейнер. Общий real-backend E2E остаётся в пункте 11.
9. [ ] **Deletion.** Реализовать отдельный утверждённый workflow удаления.
   **Приёмка:** точные dependency scope, confirmations и сохранение независимых
   provider/library данных соответствуют разделу «Удаление и очистка»
   `docs/design/decisions.md`, а не выводятся из FK или макета.
10. [ ] **RU/EN integration.** Интегрировать локализацию во все затронутые
   production/prototype экраны; направление не зависит от завершения удаления
   или Local CRUD. **Приёмка:** все состояния и доступные имена доступны на обоих
   языках без потери draft/операций; browser preference, fallback English,
   «Как в системе» и локальное сохранение выбора — по разделу «Интерфейс» контракта.

11. [ ] **Сквозная и визуальная приёмка.** Проверить актуальный provider-mediated
   путь на реальных данных и UI против sole prototype reference на desktop/mobile,
   обеих темах и языках. **Приёмка:** путь setup → source → analysis → incoming →
   matching → library/publication работает без обходных URL. После Setup без root
   показано действие добавления source; при наличии root открываются «Входящие»
   напрямую, без промежуточной страницы. Desktop-приоритет — от 1400 px;
   mobile-first требование не вводится. Keyboard и
   responsive состояния проверены; acceptance фиксируется без screenshot-
   артефактов. Выполнить требуемые для application changes gates/CI и независимое
   ревью по правилам проекта.

**Целевой результат программы:** после настройки открывается настоящий рабочий интерфейс;
утверждённые разделы доступны из общей навигации, каталог читает реальные
данные, пользователь проходит путь без угадывания URL и обходных инструкций.
