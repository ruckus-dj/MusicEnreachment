# План 08: согласовать Settings и Setup с утверждённым дизайном

## Статус и назначение

Подготовлен 2026-10-07 по запросу владельца выбрать следующий этап и составить
подробный план. **Статус: TODO; реализация и приёмка не начаты.** Этот документ
планирует исправление существующего среза, а не утверждает новые продуктовые
решения. Запрос на план не означает, что исправления уже выполнены или приняты.

Основание — [аудит кода и дизайна от 2026-10-07](../to-decompose/code-design-audit-2026-10-07.md),
пункты 1–8. Аудит не является приёмкой; его предложения ниже превращены в
ограниченные задачи с проверяемым результатом. Исторический аудит не переписывать.

## 1. Почему выбран этот этап

Планы 01–07 находятся в `done/`; `todo/` до подготовки этого документа был пуст.
Поставлены фундамент приложения, одноразовый Setup, managed tools, инвентарь
источников, технический инспектор и автоматический независимый анализ
SHA-256 / ffprobe / fpcalc. План 06 закрыл прежние замечания A01–A09; они не
открываются заново без нового свидетельства. План 07 принят как COMPLETE:
[независимая приёмка](../../reports/plan07-independent-review-2026-10-07.md)
разделяет статические тесты, реальный runtime и ограничения платформ.

Актуальный аудит выявил другие проблемы, прежде всего опасное подтверждение
переноса с уже изменёнными параметрами формы. Также остаются потеря черновиков,
недоступные ошибки диалогов, неполная тёмная тема и две backend-дыры в существующих
инвариантах. Добавлять новые продуктовые экраны поверх этих проблем преждевременно.

Следующий этап — **корректирующий вертикальный срез Settings + границы Setup**.
Он даёт наблюдаемый результат без выбора алгоритма matching, правил confidence,
провайдерских контрактов или новой политики публикации. Matching, медиатека и
публикация остаются последующими отдельными этапами, а не дефектами планов 01–07.

### Источники требований

- `docs/design/decisions.md`, раздел об инструментах: автоматическая активация
  после успешной проверки только в Setup; после Setup — отдельное явное действие;
  initial flow выбирает ровно одну версию каждого обязательного package.
- `docs/design/external-tools.md`: scoped preflight/confirmation, управление
  точными managed files, перенос persistent tools-directory.
- `docs/plans/done/02-setup-manager-and-managed-tools.md`: непересечение
  tools/output, повторные серверные проверки, Settings и доступность диалогов.
- `docs/app-design/02-screens.md`, S14–S16 и критерии приёмки: заметные
  несохранённые изменения, inline errors, retry, конкретные пути до действия.
- `docs/app-design/DESIGN.md`: плотная дизайн-система v2, canvas/ink tokens,
  одинаковая геометрия светлой и тёмной тем, доступность не только через цвет.
- `docs/app-design/screenshots/v2/settings-{light,dark}-{1440,375}.png` —
  визуальные ориентиры, а не обязательное буквальное копирование старого макета.

При расхождениях приоритет у последних решений владельца в `design/decisions.md`.
Формулировки старых планов об «установке» не расширяют контракт: managed binaries
скачиваются и проверяются, без package manager или OS installer.

## 2. Пользовательский результат и границы

После этапа оператор:

1. Подтверждает перенос ровно в показанный проверенный каталог и с показанной
   политикой удаления; изменение любого входа требует новой проверки.
2. Видит ошибку внутри открытого диалога и может исправить её с клавиатуры.
3. Не теряет несохранённые изменения из-за сохранения другой секции или
   завершения фоновой операции.
4. Может повторить неудачную первую загрузку Settings без reload страницы.
5. Получает читаемый Settings и окружающую оболочку в обеих существующих темах.
6. Не может конкурентными действиями создать пересечение tools/output или
   превратить initial Setup в управление несколькими успешными версиями.

Вне этапа: matching/AcoustID, группы входящих, медиатека, публикация, naming,
artwork/lyrics, staged/work-directory, scheduled scan, автоматический retry,
история завершённых операций, новые пользовательские настройки, глобальный
переключатель темы и выбор хранилища UI-preferences. Не добавлять env vars,
auth/CORS, новые зависимости, лимиты анализа или более строгий критерий
текущести source вместо утверждённых size/mtime.

Не переделывать весь Settings ради исправлений. Допустимо выделить локальные
компоненты/hooks для draft или dialog state, если это сокращает связность;
самостоятельный архитектурный рефакторинг не является целью этапа.

## 3. Карта изменений

Пути `service/`, `settings/`, `persistence/`, `api/`, `jobs/` ниже относятся к
`backend/internal/`; frontend-пути приведены полностью. Имена новых методов и
тестов определяет исполнитель; описанные здесь инварианты обязательны.

| ID | Аудит | Область | Результат |
| --- | --- | --- | --- |
| C01 | 1, P1 | Settings move dialog | Нельзя подтвердить старый план после изменения формы |
| C02 | 2, P2 | Move/download dialogs | Ошибка и фокус внутри активного диалога |
| C03 | 3, P2 | Settings drafts | Refresh не стирает пользовательское редактирование |
| C04 | 5, P2 | Settings initial load | Явный retry без snapshot |
| C05 | 4, P2 | Settings + AppShell CSS | Читаемые canvas/ink в обеих темах |
| C06 | 6, P2 | Runtime settings + tools move | Транзакционное непересечение tools/output |
| C07 | 7, P2 | Setup/install/activation | Ровно одна успешная версия package в initial flow |
| C08 | 8, P3 | README | Остановка через действующий `task stop` |

## 4. Порядок исполнения

Каждый шаг — отдельный ограниченный change set. Сначала C01, затем C02–C05;
C06 и C07 можно прорабатывать независимо от UI, но согласовать порядок общих
locks до реализации. C08 независим. Приёмка выполняется после интеграции всех
изменений. Не начинать продуктовый downstream-этап внутри этого плана.

### Шаг 1. Привязать move confirmation к неизменным проверенным входам — C01

**Код:** `frontend/src/features/settings/SettingsScreen.tsx`: `MovePlan`,
`preflightMove`, `confirmMove`, поля move dialog; `service/move_tools.go` —
существующая серверная повторная проверка snapshot.

- При изменении пути или `removeOld` немедленно инвалидировать текущий план и
  недоступным делать подтверждение. Возврат к прежнему значению сам по себе
  не восстанавливает инвалидированный token.
- Ответ preflight устанавливает план только для той же редакции входов и того
  же открытия диалога. Поздний ответ после редактирования, закрытия/повторного
  открытия или более новой проверки игнорируется; один AbortController сам по
  себе не заменяет проверку актуальности ответа.
- Показывать immutable summary проверенных destination, количества managed
  files, конфликтов и политики удаления прежних файлов. Не смешивать summary
  старого token с текущим input. Исходный tools root показывать из доступного
  server snapshot, не выдавая его за серверно закреплённый новым полем token.
- Перед submit дополнительно сверять актуальность плана. После серверного отказа
  из-за stale/expired token нужна новая проверка, а не повтор старого подтверждения.
- Сохранить backend preflight, точное подтверждение conflicts и повторные проверки;
  исправление UI не заменяет серверные guards.

**Регрессии RTL:** смена пути; снятие и установка checkbox после preflight;
изменение входов при pending response; поздний ответ после reopen; два ответа
в обратном порядке; старый token не отправляется; свежий token отправляет ровно
проверенные параметры. Отдельно проверить опасный сценарий `removeOld=true → false`.

### Шаг 2. Сделать ошибки диалогов доступными — C02

**Код:** тот же Settings screen, `confirmInstall`, `preflightMove`, `confirmMove`,
dialog refs и close handlers; соседние dialog tests.

- Разделить screen error, install-dialog error и move-dialog error. Ошибка
  операции внутри modal отображается внутри него текстом с alert semantics.
- Фокусировать актуальный error внутри открытого диалога, а не inert-страницу.
  Сохранить подписи полей, pending/disabled states и возврат фокуса на инициатор.
- Закрытие/повторное открытие не переносит ошибку или поздний ответ прежней сессии.
  Успешный start закрывает dialog и показывает operation, failure оставляет
  редактируемые входы; stale token инвалидируется по шагу 1.

**Регрессии:** HTTP refusal preflight; expired/stale token; ошибка start download
и start move; повтор после исправления; close/reopen; focus находится в modal.
RTL дополнить реальным браузерным keyboard-прогоном: jsdom не доказывает native inert.

### Шаг 3. Разделить server snapshot и drafts — C03

**Код:** `SettingsScreen.tsx`: `syncForm`, `refreshState`, `mutate`, `onOperation`,
все существующие редактируемые секции, включая SHA-256 setting.

- Server snapshot хранить отдельно от редактируемых значений. Initial load
  инициализирует draft; последующие refresh обновляют pristine-поля, но не dirty.
- Сохранение захватывает значения и revision своей секции. После успешного
  server re-read синхронизировать сохранённую секцию лишь если пользователь не
  изменил её после начала запроса. Другие dirty-секции остаются без изменений.
- Показывать несохранённое состояние текстом, не только цветом. Не вводить
  autosave, discard-on-navigation или обязательный диалог ухода как новые решения.
- При отказе сохранить drafts и показать ошибку. Успех mutation и ошибка
  последующего readback различаются: не объявлять подтверждёнными сервером
  значения, которых UI ещё не перечитал.
- Фоновая операция обновляет installations/active paths/operations и актуальный
  snapshot, не сбрасывая пользовательскую форму. Refresh не отменяет dirty-draft.
- Изменение входов move через любую синхронизацию также соблюдает C01.

**Регрессии:** dirty output + save LRCLIB; dirty MusicBrainz + завершение tool
operation; редактирование своей секции во время save; failed mutation; successful
mutation + failed readback; pristine updates; explicit false для boolean-настроек.
Не ослаблять имеющиеся тесты server re-read и независимых шагов анализа.

### Шаг 4. Восстановление первой загрузки — C04

**Код:** `SettingsScreen.tsx`: `loadScreen`, initial loading/error branch.

- Retry доступен при ошибке initial load независимо от наличия `state`.
  Повтор выполняет существующий набор запросов экрана, не требует navigation/reload.
- Показать loading и исключить duplicate retry. Частично успешно загруженные
  данные не выдавать за полностью готовый screen. Поздние ответы не меняют
  размонтированный экран; retry с уже имеющимся snapshot соблюдает C03.

**Регрессии:** первый GET settings fails → retry → success; отказ installations
или operations; повторный отказ; disabled retry при pending; keyboard focus.

### Шаг 5. Согласовать тему Settings с оболочкой — C05

**Код:** `frontend/src/features/settings/settings.css`, применимые tokens из
setup/shared CSS, `frontend/src/routes/AppShell.tsx` и его стили.

- Устранить сочетание светлого `main` со светлым текстом Settings. Canvas, header,
  navigation, panels, поля, dialogs, alerts, disabled и focus states используют
  согласованные tokens существующей темы.
- Сохранить плотность v2, геометрию, routes и действующий способ определения темы.
  Не добавлять selector, localStorage/API preference или новый default.
- Проверить соседние Setup/Sources: общая CSS-правка не должна ухудшить их темы,
  навигацию или размер layout.

**Проверка:** RTL для применимого структурного поведения; реальные browser
screenshots Settings в light/dark при 1440 и 375 px, keyboard traversal и dialogs.
Проверить читаемость текста вне panels, перенос длинных путей и отсутствие
горизонтального переполнения. Скриншоты не объявлять полной WCAG-сертификацией.

### Шаг 6. Закрыть конкурентное пересечение output/tools — C06

**Код:** `service/setup.go:SaveRuntime`, `settings/settings.go:UpdateRuntime`,
`persistence/settings.go:SetMany/UpdateRuntime`,
`persistence/setup_manager.go:CreateToolsMoveOperationAndEnqueue/SwitchToolsRoot`,
`service/move_tools.go` и worker finalization.

- Перед правкой составить краткую карту порядка locks для output update, tools
  update, move admission и root switch; использовать действующий tools-move gate,
  а не несогласованную вторую систему блокировок.
- Production output update не должен обходить транзакционный путь только потому,
  что `ToolsDirectory` отсутствует в update. Вместе с output атомарно сохранять
  его filesystem semantics и остальные поля этого runtime update.
- В транзакции перечитывать актуальные managed roots и проверять непересечение.
  Для admitted move учитывать закреплённый target либо отклонять конфликтующую
  запись; перед root switch повторить проверку относительно текущего output.
  Допустимая реализация должна сохранять инвариант при обоих порядках операций.
- Общая gate/короткие DB transactions не должны удерживаться на весь download/
  copy. Filesystem probes не переносить в длительную DB transaction.
- Отклонённая операция не меняет roots/semantics, не создаёт лишнюю River job
  и не теряет прежнюю active installation. Сохранить ошибки, retry/recovery и
  cleanup move; не делать compensating overwrite пользовательского output.

**Детерминированные PostgreSQL integration tests:** barriers после move preflight/
admission до switch и между service reads и settings commit. Проверить равенство,
родителя и потомка target; оба порядка commits; неконфликтующий output; смену tools
без installations; failed move и retry; отсутствие partial write и deadlock.
Проверять фактические roots и filesystem semantics после settlement, не только
HTTP status. Никаких sleep-based probabilistic tests вместо synchronization.

### Шаг 7. Защитить одноразовый Setup на backend — C07

**Код:** `api/tools.go`, `service/install_operations.go:StartFromPreflight`,
`persistence/setup_manager.go:CreateInstallationOperationAndEnqueue`,
`ActivateInstallation/ActivateInstallationDuringSetup`, `jobs/install_worker.go`,
Setup completion/retry admission; `frontend/src/features/setup/SetupTools.tsx`.

- До завершения Setup не допускать вторую успешную выбранную версию того же
  package. Проверку выполнять атомарно на admission под согласованными completion/
  package locks; endpoint-only read недостаточен при конкурентных запросах.
- Сохранить скачивание FFmpeg package и fpcalc, автоматическую активацию каждой
  первой полностью проверенной версии и существующий retry неуспешной операции.
  Не превращать failed/preparing row в бессрочный запрет восстановления.
- Explicit activation/rollback нескольких installations доступны только после
  Complete Setup. HTTP gate дополнить транзакционной проверкой; worker-only
  activation во время Setup сохранить как отдельный разрешённый путь.
- При завершённом Setup новые скачанные версии по-прежнему не активируются сами.
  Учесть гонку completion и worker finalization, не возвращая completed instance
  в initial flow и не меняя active ID поздним результатом старой попытки.
- Не удалять автоматически версии/строки существующих БД ради нового ограничения.
  Если legacy-состояние с несколькими версиями не позволяет применить утверждённый
  контракт без выбора активной версии или очистки, остановить зависимый сценарий
  и запросить решение владельца; не придумывать repair policy.

**API + PostgreSQL regressions:** первая успешная версия, отказ второй;
конкурентные starts одного package; независимые starts разных packages;
failed download → допустимый retry; explicit activation до/после completion;
completion vs admission/finalization; post-Setup download сохраняет active ID.
При отказе не остаются orphan operation, installation или River job. Проверить
Setup UI: он не предлагает запрещённое действие и не требует ручной activation
вместо автоматической успешной первой проверки.

### Шаг 8. Исправить локальную инструкцию остановки — C08

**Файлы:** `README.md`, сверка с `Taskfile.yml:run/stop`.

Заменить утверждение об остановке через Ctrl+C на `task stop`: `task run` запускает
Compose detached. Не менять действующий lifecycle/build interface ради текста.
Проверить соседние инструкции запуска. Новых shell scripts не добавлять.

## 5. Реализация, проверки и передача между агентами

- До каждого change set прочитать локальные `AGENTS.md`, текущие call paths и
  соседние tests. Ссылки аудита относятся к его ревизии; не полагаться на старые
  номера строк как на точные места после правок.
- `implementer` — C01–C03 и backend C06–C07; для общего lock order предварительно
  `architect`. `quick` — C08 и уже определённые локальные CSS/retry-правки.
  `tester` — регрессии; `reviewer` — независимая проверка инвариантов и blast radius.
- Единственный локальный test/build gate — **`task verify`**. Новые tests входят
  в существующие suites; узкий запуск допустим только для диагностики его отказа.
  Платформенные builds/cross-compilation относятся к GitHub CI, не к дополнительным
  локальным прогонам. Browser acceptance — отдельное ручное UX-свидетельство.
- API shape по возможности сохранить. Если изменение действительно необходимо,
  OpenAPI/Orval обновлять существующей генерацией; generated client не редактировать.
  Forward SQL не переписывать; новая миграция нужна только при обоснованном
  изменении persistence contract, с up/down и проверкой сохранности данных.
- Текущая подготовка плана — статическое исследование, не новый test/runtime pass.
  PASS из аудита или приёмки 07 не выдавать за проверку будущей реализации 08.

## 6. Итоговая приёмка

План становится COMPLETE только после выполнения всей матрицы C01–C08:

1. Ни изменение формы, ни запоздалый preflight не позволяют подтвердить старые
   destination/removeOld; summary совпадает с отправленным проверенным планом.
2. Ошибки move/download доступны внутри modal; keyboard focus/return работают
   в реальном браузере, не только в jsdom.
3. Dirty drafts переживают refresh, другие saves и operation completion;
   редактирование после submit не стирается, серверный readback сохранён.
4. Initial error Settings восстанавливается явным retry.
5. Light/dark Settings и shell читаемы на desktop/mobile; Setup/Sources не регрессируют.
6. Все проверенные конкурентные output/move paths сохраняют непересечение;
   отклонённые записи атомарны, recovery/retry не обходят guard.
7. Initial Setup не допускает вторую успешную версию package/explicit switching,
   но первая успешная активация и failed retry работают; post-Setup semantics прежние.
8. README соответствует `task run`/`task stop`.
9. Полный `task verify` успешен, независимое ревью не оставляет blockers;
   выполнена ручная проверка критических пользовательских сценариев.

### Свидетельства

Создать `docs/reports/plan08-acceptance-<date>.md` с проверенной code revision,
результатом gate, test names и матрицей C01–C08. Для runtime фиксировать платформу,
версии tools и соответствие бинарника ревизии. Минимальные ручные сценарии:
реальный move с изменением checkbox после preflight и новой проверкой; успешный
перенос без удаления прежних files; ошибка внутри modal; сохранность dirty output
при tool operation completion; initial load retry; Settings обеих тем/размеров.
Реальный download/Setup smoke допустим в изолированном stand с approved tools;
синтетические concurrency fixtures не выдавать за реальную загрузку binaries.

Отдельный независимый отчёт `docs/reports/plan08-independent-review-<date>.md`
сопоставляет критерии, code/tests и manual evidence. Ограничения native platforms,
browser и legacy cases указать явно; macOS pass не доказывает всю CI/runtime-матрицу.
Если остаётся неоднозначное продуктовое поведение, зафиксировать blocker и
вопрос владельцу, а не молчаливое допущение. Перенос в `done/` — после приёмки,
с обновлением `docs/plans/README.md`; старые отчёты и аудиты не переписывать.
