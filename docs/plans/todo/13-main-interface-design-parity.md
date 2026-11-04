# 13. Основной интерфейс по утверждённому дизайну

Основание: запрос владельца привести приложение к графическому эталону (тогда
`docs/app-design`), а не
маскировать пустую главную страницу. Это не быстрый косметический фикс:
отсутствуют оболочка и API основного каталога. Существующие анализ,
сопоставление и публикация переиспользуются, не переписываются.

Единственный текущий графический эталон — standalone React-макет
`docs/app-design-react/`. Сохранённые `DESIGN.md` и `02-screens.md` содержат
письменное описание прежнего макета; runtime `docs/app-design/` удалён решением
владельца от 2026-11-03. Продуктовые ограничения — `docs/design`, они имеют приоритет
над графическим эталоном. Снимки удалены по решению владельца от 2026-11-01 и не
хранятся/перегенерируются на каждую правку. Демо-данные прототипа не переносятся.

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

Каждый пункт — отдельная bounded stage; выполнять последовательно и проверять
критерии до перехода дальше. Product contract — `docs/design/decisions.md` и
связанные design-документы. Prototype port переиспользует пригодные формы и
presentation modules; утверждённые UI-изменения синхронизируются в production и standalone
React-прототипе. Не добавлять backwards compatibility: старые данные/потоки не
нужно сохранять ради совместимости. Это не разрешение удалять существующие
данные владельца или потерять текущее recovery authority.

1. [ ] **Удалить автоматическую перезапись metadata.** Убрать автоматическое
   файловое обновление metadata существующего CURRENT без явного выбора,
   preview и подтверждения. Provider cache refresh, начальная автоматическая
   публикация и автоматическое улучшение AUDIO сохраняются; последнее сохраняет
   metadata/cover/lyrics CURRENT. Правила распространения provider-данных в
   library metadata не меняются. **Приёмка:** после реального refresh и обработки
   reconciliation CURRENT generation и metadata не изменились, rewrite не
   поставлен; metadata-only событие завершается, не зацикливается и не ждёт
   provider graph. Лучшее AUDIO по-прежнему допускается независимо от metadata.
   Реализовано и проверено локальным полным gate 2026-11-04; hook/CI текущего
   коммита ещё ожидаются. Свидетельства и ограничения —
   [отчёт](../../reports/2026-11-04-publication-metadata-policy.md).
2. [ ] **Staged/reset/source deletion lifecycle.** Сверить staged execution,
   reset и удаление roots с актуальным контрактом. Добавить отдельный общий
   `work_directory` в Settings/UI/БД без default и env; Setup и регистрация root
   не требуют его, staged processing требует. При копиях или использующей каталог
   работе смена запрещена, автоматического переноса нет. **Приёмка:** reset
   отменяет jobs, но сохраняет копии, ownership и незавершённые requested steps;
   restart не возобновляет их автоматически, явный retry переиспользует копию.
   Удаление root очищает его owned copies и все `track_source` variant без
   оставшихся locations, снимает выбор без fallback; SHA cache, медиатека,
   CURRENT и host source files сохраняются. Покрыть shared variants, missing
   copy, ошибку cleanup, конкуренцию и rollback. По решению от 2026-11-04
   проверять отсутствие пересечения work_directory с sources/tools/output
   при изменении любой стороны; не требовать пустоты или одного volume.
3. [ ] **Recovery и явная republish.** Устранить расхождения восстановления и
   повторной публикации с утверждёнными правилами. **Приёмка:** interrupted,
   frozen target при актуальной authority регистрируется по соответствующим
   тегам/техническим параметрам без транскодирования и доказательства происхождения
   из source; неподходящий target ведёт к полному retry. Обычный retry и явный
   republish полностью готовят candidate, republish не превращается в unchanged.
   Покрыть прерывание после установки до DB commit, отсутствующий source,
   stale authority и completed redelivery; не возвращать step journal/receipts.
   По решению от 2026-11-04 republish применяет текущие format/path settings и
   эффективные metadata/cover/lyrics через preview; recovery сохраняет фактический
   прочитанный текст, но не выдумывает неизвестные источник/предупреждения его
   получения и не пересоздаёт подходящий файл ради этой справки.
4. [ ] **Metadata ownership и provider edges.** Привести ownership, provider
   relationships и границы влияния metadata к документации; CRUD Local не
   создаёт library entries. **Приёмка:** один owner позиции вместо параллельных
   track/position metadata, самостоятельный recording и утверждённые overrides;
   artist `sort_name`, производные sort credits без display-name fallback;
   cross-provider references сохраняют собственный namespace без зеркал.
   Удалить прямое создание library из API/UI, не запрещая внутреннее создание
   сущностей matching-процессом. Не выбирать незаданные правила распространения
   provider metadata или наследования genres/tags/explicit/ISRC.
5. [ ] **Фактический каталог.** Довести отображение/агрегацию до factual
   данных, разделив source/audio availability, provider entries, library и
   publications. **Приёмка:** unknown не показывается как ноль, edition и
   release-group не смешиваются, completeness основана на реальном состоянии;
   решение об агрегатах берётся только из утверждённых документов. «В наличии»
   означает доступный source ИЛИ существующий published audio; частичный объект
   входит и в «В наличии», и в «Не хватает», default фильтра не выбирается.
   Missing rows объединяются только по MusicBrainz release-group identity;
   фактические editions плоские, просмотр missing edition требует явного выбора
   и не создаёт collection entities. Полный track-list не скрывается фильтром.
6. [ ] **Prototype port и агрегированные publication/metadata flows.** Портировать
   необходимые утверждённые сценарии из standalone React-макета, включая
   агрегированный preview и полный metadata ownership/редактирование. Presentation
   modules можно копировать/адаптировать с production-owned API/controllers;
   production не импортирует prototype code и не использует demo fixtures или
   execution models. Сохранять production concurrency/SSE и directory controllers;
   переносить подходящие prototype ui, styles, tokens, i18n, metadata
   editor, MatchingDnD и LocalProviderScreen без обязательного shared package.
   **Приёмка:** production и prototype синхронны по действиям/состояниям; aggregate
   preview раскрывает результат до позиции, не создаёт library entities и требует
   явного подтверждения согласно контракту. Не создавать отдельный раздел
   «Публикации»: управление в контексте артиста/релиза/позиции. Исходы preview —
   create/update/unchanged/unprocessable/skipped с причинами. Точный metadata diff
   относительно последней успешной CURRENT отделён от completeness, AUDIO/path и
   filesystem drift; порядок multi-values значим, поисковая нормализация не
   применяется. Новую bulk atomicity/cancel policy не выбирать. Полноту Navidrome
   проверять по всей цепочке editor/storage/preview/writer/readback, а не JSON.
7. [ ] **Genres.** Завершить каталог и genre flows до Local UI. **Приёмка:**
   жанры сохраняют MBID и утверждённые правила каталога; состояния каталога и
   picker корректны согласно `docs/design/genres.md`.
8. [ ] **Local CRUD.** Использовать переиспользуемые формы для provider Local.
   **Приёмка:** Local CRUD создаёт/изменяет только provider records; в медиатеку
   ведёт provider-mediated incoming matching, без прямого create library пути.
9. [ ] **Deletion.** Реализовать отдельный утверждённый workflow удаления.
   **Приёмка:** точные dependency scope, confirmations и сохранение независимых
   provider/library данных соответствуют `docs/design/requirements.md`.
10. [ ] **RU/EN integration.** Интегрировать локализацию во все затронутые
   production/prototype экраны. **Приёмка:** все состояния доступны на обоих
   языках без потери draft/операций; соблюдены текущие locale decisions.

11. [ ] **Сквозная и визуальная приёмка.** Проверить актуальный provider-mediated
   путь на реальных данных и UI против sole prototype reference на desktop/mobile,
   обеих темах и языках. **Приёмка:** путь setup → source → analysis → incoming →
   matching → library/publication работает без обходных URL; keyboard и
   responsive состояния проверены; acceptance фиксируется без screenshot-
   артефактов. Выполнить требуемые для application changes gates/CI и независимое
   ревью по правилам проекта.

**Целевой результат программы:** после настройки открывается настоящий рабочий интерфейс;
утверждённые разделы доступны из общей навигации, каталог читает реальные
данные, пользователь проходит путь без угадывания URL и обходных инструкций.
