# План 09: проработать staged source analysis источников

## Статус

Подготовлен 2026-10-08 по запросу владельца выбрать следующий шаг.
**Статус на 2026-10-08: техническая проработка D01–D06 подготовлена; приложение
не реализовано, явное approval владельца контракта и implementation proposal
ожидается.** Утверждённые решения зафиксированы в отдельном
[документе решений владельца](09-staged-source-analysis-owner-decisions.md).
Этот план сохраняет последовательность и критерии проработки, но не является
разрешением начинать реализацию или объявлять её поставленной. Документационные
результаты D01–D06 подготовлены; явное owner approval контракта и плана ещё нужно.
Требования к staged mode, output и scan из более ранних
разделов этого документа, противоречащие датированным решениям владельца,
считаются superseded. D01–D06 остаются unchecked до появления свидетельств.
Код приложения, SQL-миграции и generated API этим обновлением не изменяются.

## 1. Почему именно этот шаг

Поставлены фундамент и Setup/managed tools (01–02), инвентарь и технический
инспектор (03–05), исправления прежнего аудита (06), автоматический независимый
анализ (07) и согласование Settings/Setup с дизайном (08).

Последний [аудит](../to-decompose/code-design-audit-2026-10-07.md) содержит восемь
замечаний: все вошли в C01–C08 плана 08 и приняты
[независимым ревью](../../reports/plan08-independent-review-2026-10-08.md).
Ограничения свидетельств не превращаются в новые требования и не переоткрывают
завершённые планы без нового обнаруженного дефекта.

Текущий анализ работает только `in_place`. Утверждённое владельцем направление для
NAS/HDD — per-root staged mode: одна последовательная временная AUDIO-копия в
служебной области управляемого output (`analysis`), анализ копии и её явная
очистка. Отдельный общий work-directory setting/mount не вводится. Это
ограниченное развитие существующего вертикального среза без выбора
matching/confidence и политики качества.

Другие варианты отложены осознанно:

- Входящие группы — следующий мост к разбору музыки, но правила группировки,
  read-model и сохранение drafts ещё требуют отдельного согласования.
- MusicBrainz/AcoustID matching — не определены формула confidence, пороги и
  неоднозначность; демонстрационные verdict нельзя переносить в backend.
- Медиатека и публикация — потребуют самостоятельной проработки назначения
  источников, именования, коллизий и quality policy; не включать их побочно.
- Theme selector и хранение UI-preferences — отдельный открытый вопрос, не
  основание задерживать работу с источниками.

### Основания

- [Требования](../../design/requirements.md): выбор обработки на месте или через
  временную копию для каждого входящего каталога.
- [Решения](../../design/decisions.md), «Инвентарь и обработка источников»:
  `in_place`/`staged`, size/mtime как достаточный критерий текущести,
  независимые результаты шагов. Исторические положения того же раздела о явном
  общем work-directory и SHA только в copy-потоке помечены позднейшим уточнением
  владельца 2026-10-08 как superseded: staged использует output-managed копии,
  SHA — независимый запрашиваемый step.
- [Модель данных](../../design/data-model.md): root/location/variant, SHA identity,
  nullable digest, отсутствие истории и освобождение удерживаемых результатов.
- [Архитектура](../../design/repository-architecture.md): технические слои,
  snapshot операции, транзакционная постановка River.
- [Deployment](../../design/deployment.md): серверные read-only источники,
  текущая поставка только `in_place`, отсутствие неявного container scratch.
- [Черновик области](../to-decompose/source-inventory-and-analysis.md): сохранённая
  staged-область, а не готовый контракт. Исторические формулировки о SHA,
  fingerprint и legacy уступают решениям владельца и плану 07.
- [Графический дизайн](../../app-design/02-screens.md) и
  [дизайн-система](../../app-design/DESIGN.md): Sources/Settings, доступные ошибки,
  конкретные server paths и обе темы. Готового staged-flow прототип не поставляет.

## 2. Цель и границы проработки

Спроектировать понятный оператору выбор режима одного root и безопасный lifecycle
временной копии, сохранив весь контракт анализа плана 07. Установить, где staging
встраивается в подготовку scan и последующий точечный retry, не создавая второго
независимого анализатора.

Включено: сценарии, открытые решения, UI-состояния, физическая модель и snapshots,
filesystem lifecycle, транзакционные границы, recovery, план проверки и короткая
декомпозиция реализации.

Исключено: реализация функций, автоматический выбор processing mode, scheduled
scan, новые автоматические повторы, grouping/matching, quality/lossless scoring,
CUE, artwork/lyrics, публикации и удаление исходников. Не добавлять env vars,
auth/CORS, новые зависимости или произвольные пределы размера/длительности.

## 3. Утверждённые инварианты — не обсуждать заново

**Уточнение 2026-10-08:** этот исходный список сохраняет прежние границы
проработки, но в части противоречий superseded решением владельца из
`09-staged-source-analysis-owner-decisions.md`. В частности, общий work-directory,
hash в copy-потоке и полный scan как атомарный источник анализа не являются
актуальными решениями. Не переносить эти положения в требования реализации.

1. Режим выбирается для root; источник не записывается и не удаляется.
2. Work-directory — общий явно настроенный серверный путь; не implicit temp
   рядом с бинарником и не container overlay.
3. Staged-файл копируется одним последовательным чтением; при включённом SHA
   digest вычисляется в copy-потоке. При выключенном SHA инструментальные шаги
   продолжаются, а digest остаётся необязательным.
4. Включение SHA не делает backfill неизменённых locations и не удаляет прежние
   результаты. Fingerprint не становится identity.
5. Актуальность source определяется size/mtime; не требовать доказательства
   неизменности байтов при совпадающих метаданных.
6. Успех одного шага не пропадает из-за ошибки другого. Fingerprint сохраняется
   независимо от probe; single-audio gate ограничивает matching, не хранение.
7. Полный успешный scan атомарно заменяет inventory; незавершённый обход не
   публикует частичные результаты и не удаляет прежние locations.
8. Snapshot фиксирует входы durable операции; River args содержат operation ID.
   DB-состояние и enqueue атомарны; SSE только будит повторный REST-read.
9. Не вводить историю результатов, отдельный legacy executor и массовый reanalysis.
10. Временная копия очищается после использования; cleanup не получает права
    удалять чужие файлы из общего каталога.

## 4. Вопросы для явного согласования

**Уточнение 2026-10-08:** Q01–Q08 получили ответы владельца; актуальные решения
перечислены в [дополнении решений](09-staged-source-analysis-owner-decisions.md).
Таблица ниже сохраняется как запись исходных вопросов, а не как список
неразрешённых продуктовых решений. Технические детали, явно оставленные
предложением (например точные имена директорий), требуют дальнейшей проработки.

Исходное указание подготовить варианты/рекомендацию и дождаться ответа владельца
сохранено как исторический процесс. Ответы уже внесены в dated owner decisions;
не считать таблицу незакрытым согласованием и не возвращать superseded варианты.

| ID | Вопрос | Что требуется решить |
| --- | --- | --- |
| Q01 | Начальное состояние и UI режима | Как отображать существующие roots; значение для нового root; где выбирать режим. Сохранить существующее in-place поведение до одобрения миграционного правила. |
| Q02 | Work-directory | Где редактировать и проверять путь; обязателен ли он только для staged roots; влияет ли настройка на уже завершённый Setup; допустим ли смешанный каталог; какие пересечения с sources/tools/output исключаются. |
| Q03 | Изменение конфигурации | Поведение смены режима/рабочего пути при queued/running/failed работе; что сохраняет retry, когда доступна повторная настройка. |
| Q04 | Ресурсы | Реакция на недостаток места и несколько staged roots; нужен ли отдельный пользовательский limit либо достаточно ограниченной технической concurrency. Не выбирать размеры/quota молча. |
| Q05 | Ошибка копирования | Как оператор видит ошибку подготовки, какие результаты сохраняются, какое действие повтора доступно; разрешён ли fallback в in-place. Не вводить fallback молча и не выдавать copy error за ошибку каждого инструмента. |
| Q06 | Retry и recovery | Копировать ли заново после частичного успеха/restart; срок жизни копии, поведение при недоступности старого work path; когда операция считается завершённой при cleanup error. |
| Q07 | Повтор текущего файла | Какие чтения допускает explicit fingerprint rerun/step retry, если hash для неизменённого файла не запрошен; не превращать copy-time SHA в запрещённый backfill. |
| Q08 | Наблюдаемость | Какие copy/cleanup stages, пути и ресурсные ошибки показываются в существующей операции/инспекторе; не вводить пользовательский aggregate analysis result. |

Независимый reviewer может уточнить список неоднозначностей. Разрешение на
проработку не является согласием с ответами; молчание владельца не является
одобрением. Нерешённый вопрос блокирует только зависимую часть контракта.

## 5. Последовательность работ

### D01. Снять актуальную карту исполнения

- [x] Сопоставить планы 01–08 с текущей реализацией и отметить ограничения, не
      создавая новый список дефектов из исторических отчётов.
- [x] Проследить scan admission → preparation → candidate publication → pending
      analysis → single-step retry → terminal settlement/startup recovery.
- [x] Отметить каждое прямое source-read: stat, SHA, probe и fingerprint; место,
      где возможно подставить prepared input без дублирования ffprobe.
- [x] Зафиксировать snapshot fields, source/tool holds, lock order и filesystem
      действия вне транзакций. Для карты указать точные символы и файлы.

Ориентиры текущего кода:

| Область | Файлы |
| --- | --- |
| Admission и snapshots | `backend/internal/service/source_scan_start.go`, `backend/internal/persistence/source_scan_retry.go`, `source_analysis_snapshot.go` |
| Подготовка и scan | `backend/internal/service/source_scan.go`, `source_analysis_preparer.go`, `backend/internal/jobs/scan_worker.go` |
| Pending/steps | `backend/internal/service/source_analysis_pending.go`, `backend/internal/persistence/source_analysis_steps.go`, `source_analysis_enqueue.go` |
| Settlement/recovery | `backend/internal/persistence/source_analysis_operation.go`, `source_scan_recovery.go`, `backend/internal/jobs/` |
| Настройки и roots | `backend/internal/settings/`, `backend/internal/service/source_roots.go`, `backend/internal/persistence/source_repository.go`, `backend/internal/api/sources.go` |
| UI | `frontend/src/features/sources/`, `frontend/src/features/settings/SettingsScreen.tsx`; пример Setup/tools operation-flow — `frontend/src/features/setup/OperationProgress.tsx`, не готовый компонент Sources |

Имена новых файлов/методов не утверждаются этой картой; отсутствующий ориентир
уточнить по актуальному codegraph, не создавать файл только ради плана.

**Результат:** [фактическая карта исполнения на 2026-10-08](../../reports/plan09-execution-map-2026-10-08.md), снятая с `959c680`.

### D02. Описать пользовательские сценарии и согласовать Q01–Q08

- [x] Описать локальный SSD/in-place и HDD/NAS/staged, новое и существующее root.
- [x] Разобрать неверный/недоступный output path, нехватку места, изменение source
      при copy, исчезновение диска (отдельного режима «выключить» у root нет —
      владелец допускает только add/remove).
- [x] Разобрать partial tool success, retry одного шага, изменение active tools,
      смену режима/пути и restart после каждого filesystem/DB перехода.
- [x] Зафиксировать сценарную матрицу и решения Q01–Q08 в
      [контракте](09-staged-source-analysis-contract.md); сценарии не означают
      отдельного owner acceptance технического предложения.
- [x] Обновить документацию с датой уточнений, сохранив явную границу approval.

**Результат:** D02-сценарии и ответы владельца зафиксированы в контракте;
описание не является owner acceptance технической реализации.

### D03. Подготовить UI-flow

- [x] Описать дополнение формы root: режим, объяснение I/O и server path.
- [x] Подготовить секцию output/concurrency в Settings; черновик,
      проверка, сохранение и readback имеют разные состояния.
- [x] Показать queued/copying/tool/cleanup/error/retry только в согласованном
      контракте; не обещать проценты, если backend не предоставляет total.
- [x] Описать loading/empty/error/stale, inline errors, keyboard navigation,
      focus restoration и сохранность drafts при completion/reconnect.
- [x] Сверить фактический glob `screenshots/`: light Sources/Settings макеты есть;
      отдельных Sources/Settings dark screenshots нет (имеются только unrelated
      dark drafts). Документировать ограничения; полноценный visual review не заявлять.
- [x] Описать проверку узкого viewport 375 px как отсутствие потери действий,
      не как новое обязательство mobile-first.

**Результат:** UI-flow, error/stale states, draft/focus и проверка viewport
предложены в [контракте D03–D05](09-staged-source-analysis-contract.md).
Это текстовая проработка и текстовое сопоставление доступных screenshot names, не
visual/browser review flow и не API реализация.

### D04. Спроектировать один lifecycle prepared input

- [x] Сформировать таблицу переходов: admission → owned copy → validated copy
      → cache/tool preparation → DB publication → cleanup; точные состояния
      выбрать по согласованному контракту, не добавлять новый analyzer pipeline.
- [x] Объяснить, как один copy используется нужными независимыми steps и как
      выполняются retry/recovery без повтора успешных siblings.
- [x] Определить границу «одного копирования»: обычная обработка, отдельный
      retry/rerun и crash recovery. Не выводить из этого бессрочное хранение
      scratch или запрет повторного copy после сбоя.
- [x] Отдельно описать SHA enabled/disabled, cache hit/miss, неизменённый source,
      probe_error retry и fingerprint rerun. Запретить неявный hash backfill.
- [x] Указать stat до/после copy и перед применением, связь source identity с
      snapshot и сохранённым результатом, реакцию на изменение size/mtime.
- [x] Описать ownership temporary paths, manifest/DB references, fencing старой
      доставки и очистку только доказанно принадлежащих приложению artifacts.
- [x] Разобрать crash после создания каталога, partial copy, готовой copy,
      tool success, DB commit и до/во время cleanup. Не удалять живую работу.
- [x] Подготовить concurrency/resource модель без новых продуктовых квот,
      timers или более строгой filesystem threat model.

**Результат:** lifecycle/crash matrix и integration point shared preparer в
[контракте D03–D05](09-staged-source-analysis-contract.md). Fingerprint cache
решён владельцем: ровно один последний успешный результат на SHA, версия fpcalc —
provenance; version-keyed схема требует refactor. Решение не является свидетельством
реализации.

### D05. Определить DB/API и deployment-контракт

- [x] Предложить физическое хранение root mode, output-owned staging references
      и минимальных owned-staging references; объяснить связь с целевой моделью.
- [x] Зафиксировать отсутствие требования исторической compatibility для
      unpublished prototype: migration squash допустим, но не обязателен; точная
      стратегия остаётся техническим review, без предположения legacy backfill.
- [x] Предложить минимальный operation IDs/intent, актуальные settings, validation
      и holds; полный input snapshot конфигурации не сохранять.
- [x] Описать минимальную admission identity/intent, чтение текущих settings,
      validation и holds; enqueue через `River.InsertTx`, commit/rollback и lock order.
- [x] Спроектировать DTO существующих root/settings/operation API и ошибки;
      URL новых endpoints, если нужны, остаются предложением до review.
- [x] Описать OpenAPI → Orval pipeline без ручных правок generated files.
- [x] Подготовить явный Compose output bind mount и standalone сценарий
      standalone; новых bootstrap env vars не вводить.

**Результат:** предложенная физическая, HTTP и deployment-модель в
[контракте D03–D05](09-staged-source-analysis-contract.md). Требуется review;
это не согласованная SQL/API реализация.

### D06. Декомпозировать последующую реализацию

- [x] Разбить на небольшие последовательные commits: settings/roots/schema;
      admission/snapshots; prepared-input copy; scan integration; retry/recovery;
      API/generated; UI; regression/runtime/independent acceptance.
- [x] Для каждой задачи указать зависимости, точки изменения/символы,
      проверяемый результат и безопасное промежуточное состояние.
- [x] Не делать весь черновик source-inventory-and-analysis одним этапом.
- [x] Приложить матрицу проверок из раздела 6 и критерии пользовательской готовности.
- [x] Независимое архитектурное/продуктовое ревью проведено 2026-10-08
      ([отчёт](../../reports/plan09-independent-review-2026-10-08.md)).
- [ ] Получить явное одобрение владельца перед переводом implementation-плана
      в ready; approval не получен.

**Результат:** отдельный [implementation proposal](../to-decompose/09-staged-source-analysis-implementation-proposal.md)
с commit-by-commit sequencing, dependencies, verification и CI. Техническая
проработка закончена; proposal ожидает явного owner approval и не разрешает
implementation.

## 6. Матрица проверки будущей реализации

Это требования к плану проверки после согласования контракта, не свидетельство
уже выполненных тестов.

| Область | Обязательные сценарии |
| --- | --- |
| Регрессия in-place | Не меняются scan generations, SHA toggle/no-backfill, tool provenance, cache identity, step retry и read-only source |
| Copy | Одно последовательное source-read; tools получают copy; SHA включён/выключен; changed size/mtime отбрасывает результат; large source без произвольного лимита |
| Cache | Probe/fingerprint hits и misses; SHA-less source; разные версии fpcalc; fingerprint success при probe error |
| DB/River | Чистая БД и upgrade; enqueue commit/rollback; admission races; root/settings changes; stale delivery и holds |
| Ошибки | Недоступные source/work path, partial copy, ENOSPC, tool error, cleanup error; успешные siblings сохраняются |
| Recovery | Реальный restart на согласованных границах; нет утечки owned staging и удаления чужих/живых файлов |
| UI | Режим и path, dirty drafts, loading/error/retry, keyboard/focus, REST reread после SSE/reconnect, светлая/тёмная темы |
| Runtime | Настоящий PostgreSQL/River и managed tools; staged и in-place на одинаковом fixture; source bytes неизменны, temporary artifacts очищены |
| Deployment | Явный writable work mount и read-only source mount; standalone path; platform limitations раскрыты |

Единственный локальный test/build gate — `task verify`. Дополнительный узкий
запуск допустим только для диагностики его сбоя. Native platform matrix и
Compose smoke — GitHub CI, не локальные cross-builds. Ручной runtime/browser
прогон не подменяет gate; отчёт разделяет реальные и mocked свидетельства,
ревизию кода, binary provenance и непроверенные платформы.

## 7. Приёмка именно этой проработки

**Статус на 2026-10-08:** документационная техническая проработка D01–D06
подготовлена; приложение не реализовано. Финальное owner approval контракта и
implementation proposal остаётся необходимым и не подразумевается этой отметкой.

- [x] Актуальная карта кода и завершённых этапов составлена; новый дефект, если
      обнаружен, имеет воспроизведение, а не вывод из ограничения старого отчёта.
- [x] По Q01–Q08 есть явные ответы владельца либо зависимый scope исключён;
      нигде не выдано молчаливое одобрение default или новой политики.
- [x] D02 пользовательские сценарии описаны (new/existing root, недоступный/
       невалидный output, ENOSPC, изменение source при copy, исчезновение диска);
       «disabled root» исключён решением владельца. Описание не выдаётся за
       отдельное owner acceptance технического предложения.
- [x] UI-flow и lifecycle предложения покрывают happy path, partial success, retry, crash,
      cleanup и конфигурационные изменения.
- [x] Независимое ревью D03–D06 проведено 2026-10-08
      ([отчёт](../../reports/plan09-independent-review-2026-10-08.md)); findings
      внесены, но explicit owner approval ещё не получен.
- [x] DB/API/deployment предложения подготовлены с сохранением слоёв, минимальной
       identity вместо полного snapshot и transactional enqueue; это proposal,
       не согласованная реализация.
- [x] Подготовлен отдельный implementation proposal с файлами/точками изменения,
      зависимостями, критериями и
      проверками; grouping/matching/publication не включены побочно.
- [x] `docs/plans/README.md` указывает текущий статус; исторические done/audit
      и датированные свидетельства не переписаны.

**Условие начала разработки:** явное одобрение владельцем контракта и отдельного
implementation-плана. Это документационное readiness summary не является
финальным owner acceptance и не означает поставку staged.
