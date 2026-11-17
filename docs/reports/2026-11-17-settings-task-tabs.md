# Settings: вкладки по задачам

## Основание и границы

План 13, graphical reference `docs/app-design-react/02-screens.md` 177–202.
Вместо одновременного длинного grid вводятся General/Folders/Tools/Providers/Genres
с RU/EN labels. General получает отдельную существующую настройку удаления
исходников; Folders — output/template/work, Tools — tools root и managed tools,
Providers — AcoustID/MusicBrainz/LRCLIB, Genres — каталог жанров.

Health, feedback и текущие операции остаются общими; dialogs не переносятся
в панели. Parent-owned drafts/controllers, save policy и API не меняются.
Скрытая панель не должна оставаться доступной через keyboard/accessibility.

Это описание проверяемого presentation-среза, не новые продуктовые требования:
save policy и остальные правила задаёт `docs/design/decisions.md`.

## Статус

Реализация выполняется после `5ed0b445` с зелёной полной CI `43983633399`.
Runtime-проверки нового среза ещё не выполнялись. Остальные presentation gaps
и общая приёмка плана не закрываются этим срезом.

Реализация четырёх Settings-файлов завершена и передана на независимое ревью.
Inactive forced-mounted панели скрываются через `data-inert`; восстановление
фокуса сначала активирует соответствующую вкладку. Existing tests выбирают
вкладки явно; locale/draft regression дополнен сохранением черновика при
переключении вкладок и проверкой отсутствия дополнительных settings reads.
Форматирование/import assists выполнены, `git diff --check` чист.
Полный gate, CI и browser-проверки нового среза пока не выполнены.

Независимое ревью четырёх файлов блокеров не выявило: handlers/controllers и
screen-level feedback/dialogs сохранены; focus restoration использует layout
effect после выбора нужной вкладки. Следующий этап — обычный полный commit hook.

Первый полный hook остановился на frontend: 3 из 13 Settings-тестов не нашли
General tab при намеренно задержанной initial loading/queued autosave; остальные
147 frontend tests прошли. Коммит не создан. Проверяется порядок tab selection
и разрешения deferred requests в этих concurrency-сценариях; assertions и gates
сохраняются. Source review само по себе не подтвердило runtime-корректность.

Разбор установил два преждевременных выбора General после remount: предыдущая
запись ещё удерживала сериализованную очередь, поэтому tabs пока не существовали.
Первое падение оставляло deferred write незавершённым и блокировало следующий
dirty-group тест. Убраны только эти два ожидания; исходные loading/queue-ordering
и deferred-write assertions сохранены, production code не менялся.

## Результат `eba04403`

Повторный обычный полный hook прошёл; commit отправлен. CI `43992174938`
выполняется, полный результат пока не подтверждён. Acceptance-only сервер 18080
обновлён PID 36387 → 45339 на той же отдельной БД; 8787 не затронут.

Реальный browser: пять вкладок доступны, выбранная панель одна. Concurrency
draft `7` сохранился General→Folders→RU→General→EN в том же DOM node, без API
requests; восстановлен в исходное `4` без Save. Inactive panel имеет `inert`
и `display:none`. ArrowRight выбирает Folders с видимым keyboard focus;
directory picker закрывается Escape с возвратом фокуса на Choose folder.
Пять вкладок × RU/EN × light/dark × 1440×900/390×844: 40 состояний, page overflow
не обнаружен, доступна только одна tabpanel. Скриншоты не добавлялись.
Это не повторная проверка каждого изменяющего настройки действия и не полная
визуальная приёмка остальных экранов.

Вся CI `43992174938` завершилась успешно с первого attempt, включая Windows
и публикацию контейнера. Срез `eba04403` подтверждён обычным полным hook,
полной CI и описанными browser-проверками.
