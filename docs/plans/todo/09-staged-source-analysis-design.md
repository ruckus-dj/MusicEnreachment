# План 09: проработать staged-анализ источников и рабочий каталог

## Статус

Подготовлен 2026-10-08 по запросу владельца выбрать следующий шаг.
**Статус на 2026-10-08: проработка выполняется; решения собраны, приложение
пока не реализовано.** Утверждённые решения зафиксированы в отдельном
[документе решений владельца](09-staged-source-analysis-owner-decisions.md).
Этот план сохраняет последовательность и критерии проработки, но не является
разрешением считать D01–D06 выполненными или объявлять реализацию поставленной.
Результат этапа — согласованный продуктово-технический контракт и короткий
исполняемый план. Требования к staged mode, output и scan из более ранних
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

Текущий анализ работает только `in_place`. Для NAS/HDD уже предусмотрен второй
режим: одно последовательное копирование в явно выбранный общий work-directory,
анализ временной копии и её очистка. Это ограниченное развитие существующего
вертикального среза без выбора matching/confidence и политики качества.

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
  `in_place`/`staged`, явный work-directory, hash в потоке копирования,
  size/mtime как достаточный критерий текущести, независимые результаты шагов.
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

- [ ] Описать локальный SSD/in-place и HDD/NAS/staged, новое и существующее root.
- [ ] Разобрать неверный/недоступный work path, нехватку места, изменение source
      при copy, исчезновение диска и disabled root.
- [ ] Разобрать partial tool success, retry одного шага, изменение active tools,
      смену режима/пути и restart после каждого filesystem/DB перехода.
- [ ] Представить владельцу таблицу вариантов Q01–Q08; явно записать ответы,
      нерешённые вопросы и границу одобрения.
- [ ] Только после ответа обновить авторитетные документы, с датой уточнения.

**Результат:** согласованный scope; если согласование не завершено, статус
WAITING FOR OWNER, а не «готово к реализации».

### D03. Подготовить UI-flow

- [ ] Нарисовать дополнение формы root: режим, объяснение I/O и server path.
- [ ] Подготовить секцию work-directory в согласованном месте Settings; черновик,
      проверка, сохранение и readback имеют разные состояния.
- [ ] Показать queued/copying/tool/cleanup/error/retry только в согласованном
      контракте; не обещать проценты, если backend не предоставляет total.
- [ ] Описать loading/empty/error/stale, inline errors, keyboard navigation,
      focus restoration и сохранность drafts при completion/reconnect.
- [ ] Сопоставить с `screenshots/v2/sources-{light,dark}-1440.png` и
      `settings-{light,dark}-1440.png`: плотная desktop-first геометрия,
      canvas/ink tokens, различение состояний текстом, не только цветом.
- [ ] Отдельно проверить узкий viewport 375 px как отсутствие потери действий,
      не как новое обязательство mobile-first.

**Результат:** reviewed UI-flow со всеми отказами; демоданные не заменяют API.

### D04. Спроектировать один lifecycle prepared input

- [ ] Сформировать таблицу переходов: admission → owned copy → validated copy
      → cache/tool preparation → DB publication → cleanup; точные состояния
      выбрать по согласованному контракту, не добавлять новый analyzer pipeline.
- [ ] Объяснить, как один copy используется нужными независимыми steps и как
      выполняются retry/recovery без повтора успешных siblings.
- [ ] Определить границу «одного копирования»: обычная обработка, отдельный
      retry/rerun и crash recovery. Не выводить из этого бессрочное хранение
      scratch или запрет повторного copy после сбоя.
- [ ] Отдельно описать SHA enabled/disabled, cache hit/miss, неизменённый source,
      probe_error retry и fingerprint rerun. Запретить неявный hash backfill.
- [ ] Указать stat до/после copy и перед применением, связь source identity с
      snapshot и сохранённым результатом, реакцию на изменение size/mtime.
- [ ] Описать ownership temporary paths, manifest/DB references, fencing старой
      доставки и очистку только доказанно принадлежащих приложению artifacts.
- [ ] Разобрать crash после создания каталога, partial copy, готовой copy,
      tool success, DB commit и до/во время cleanup. Не удалять живую работу.
- [ ] Подготовить concurrency/resource модель без новых продуктовых квот,
      timers или более строгой filesystem threat model.

**Результат:** таблица happy path/отказов/recovery и аргументация отсутствия
второго полного probe для обычного текущего файла.

### D05. Определить DB/API и deployment-контракт

- [ ] Предложить физическое хранение режима root, typed work-directory setting
      и минимальных owned-staging references; объяснить связь с целевой моделью.
- [ ] Указать upgrade существующих roots и безопасный rollback; не переписывать
      исторические миграции и не заявлять destructive down допустимым молча.
- [ ] Определить snapshot режим/путь/policy/tool pins, их валидацию и holds;
      атомарный enqueue через `River.InsertTx`, commit/rollback и lock order.
- [ ] Спроектировать DTO существующих root/settings/operation API и ошибки;
      URL новых endpoints, если нужны, остаются предложением до review.
- [ ] Описать OpenAPI → Orval pipeline без ручных правок generated files.
- [ ] Подготовить явный Compose bind mount/volume для work-directory и сценарий
      standalone; новых bootstrap env vars не вводить.

**Результат:** согласованная физическая и HTTP-модель, а не SQL/API реализация.

### D06. Декомпозировать последующую реализацию

- [ ] Разбить на небольшие последовательные задачи: settings/roots/schema;
      admission/snapshots; prepared-input copy; scan integration; retry/recovery;
      API/generated; UI; regression/runtime/independent acceptance.
- [ ] Для каждой задачи указать зависимости, конкретные файлы/символы,
      проверяемый результат и безопасное промежуточное состояние.
- [ ] Не делать весь черновик source-inventory-and-analysis одним этапом.
- [ ] Приложить матрицу проверок из раздела 6 и критерии пользовательской готовности.
- [ ] Получить независимое архитектурное/продуктовое ревью и явное одобрение
      владельца перед переводом implementation-плана в ready.

**Результат:** отдельный детальный исполняемый план staged-среза. Этот план 09
не объявляется выполненным вследствие одной только записи следующего плана.

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

**Статус на 2026-10-08:** решения владельца собраны, однако перечисленные ниже
критерии проработки не подтверждены как завершённые. В частности, наличие
решений не свидетельствует о выполнении D01–D06, UI-flow, lifecycle, DB/API-плана,
декомпозиции или независимого ревью. Приложение пока не реализовано.

- [x] Актуальная карта кода и завершённых этапов составлена; новый дефект, если
      обнаружен, имеет воспроизведение, а не вывод из ограничения старого отчёта.
- [ ] По Q01–Q08 есть явные ответы владельца либо зависимый scope исключён;
      нигде не выдано молчаливое одобрение default или новой политики.
- [ ] UI-flow и lifecycle покрывают happy path, partial success, retry, crash,
      cleanup и конфигурационные изменения.
- [ ] DB/API/deployment предложения прошли независимое ревью и согласование;
      слои, immutable snapshots и transactional enqueue сохранены.
- [ ] Готов короткий исполняемый план с файлами, зависимостями, критериями и
      проверками; grouping/matching/publication не включены побочно.
- [ ] `docs/plans/README.md` указывает текущий статус; исторические done/audit
      и датированные свидетельства не переписаны.

**Условие начала разработки:** явное одобрение владельцем контракта и отдельного
implementation-плана. Создание этого документа не означает поставку staged.
