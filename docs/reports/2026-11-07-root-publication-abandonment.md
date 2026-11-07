# Root deletion: подготовка safe abandonment publication recovery

Решение владельца внесено в раздел удаления `decisions.md`: удаление root само
безопасно прекращает recovery зависимых failed publications и убирает retry,
сохраняя CURRENT, library и output-файлы. В `4c7e2633` этот путь ещё не реализован;
publication/recovery holds продолжают блокировать root deletion.

## Найденная техническая граница

Удалить failed attempt и лишь сохранить JSON-архив недостаточно:

- `planPublicationNamespace` переиспользует filename по same-position claim,
  пропуская обычную проверку filesystem collision;
- `reservePublicationNamespace` допускает same-position claim reuse;
- `executeSimplePublication` при существующем target вызывает native atomic
  replacement без отдельного требования зарегистрированного CURRENT;
- unfinished attempt сейчас является дополнительным position-level барьером.

После abandonment прежний claim сам по себе не должен давать новой operation
право перезаписать незарегистрированный файл старой попытки. Постоянный запрет
всей позиции при этом не вводится: другой доступный source и свободный путь
остаются обычным preview/confirm workflow. Нужна точная связь archival evidence
с namespace planning, confirmation freshness и install ownership; same-attempt
retry до удаления root сохраняет действующее поведение.

## Границы следующего среза

Сохранить неизменяемые frozen attempt/operation/claim/hold/cover/namespace
свидетельства как неисполняемый архив до удаления их executable ownership.
Namespace claims/reservations и CURRENT нельзя удалять blanket-reset SQL.
Source-cleanup retry resolutions имеют собственные RESTRICT/immutability guards:
их abandonment требует отдельной exact-authority проверки, а не ослабления
глобальных triggers. Ни один archival reader не разрешает unlink, retry или
CURRENT adoption.

## Начатая реализация

База следующего среза — `4c7e2633`, прошедшая обычный полный hook и всю
[CI 42365879090](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42365879090).
Работа разделена на immutable archive/schema и read witnesses, exact-owner
retirement обычной failed attempt, и service planning/freshness/install guards.
До совместной проверки этих частей новый путь удаления не считается готовым.

Первый ограниченный срез не ослабляет source-cleanup retry-resolution и
obsolete-output-cleanup guards: зависимости этого вида остаются защищёнными
блокерами для отдельного продолжения. Это не завершение root lifecycle.

Пока это архитектурные находки и выполняемая реализация, не приёмка.

Schema/read-witness и service-компоненты написаны, но ещё не проверены общим
gate. Archive добавлен в существующий baseline 05; он неизменяем и запрещает
rollback с сохранёнными свидетельствами. Namespace claims не фильтруются:
добавляется признак abandoned лишь для archived claims вне exact CURRENT
reservation. Planning не использует его как own-claim exemption, digest включает
abandonment witness, install проверяет право replacement до atomic replace.
Exact-owner retirement реализован; production receipt переводит только exact
вернувшийся delivery в существующее состояние `quiescent`, не затрагивая successor.
Общая DB-регрессия проверяет stale receipt, executing blocker, rollback при ошибке
cleanup и сохранение frozen attempt/namespace evidence на одной fixture.

Первый `task verify` рабочего среза дошёл до новой archive-проверки и выявил
неверное чтение массива UUID в тесте; запрос исправлен на чтение строк UUID.
Срез `8431f4bf` прошёл обычный полный commit hook и всю
[CI 42369569762](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42369569762),
включая платформенную матрицу, Compose smoke и публикацию контейнера.

## Продолжение: защищённый cleanup recovery

Следующий рабочий срез добавляет отдельный transaction-bound архив для failed
source-cleanup original/retry/frontier, с canonical SQL evidence и deferred
проверкой полного retirement вместе с root. Исключения из защиты incomplete
resolution и frontier ограничены exact receipt текущей транзакции. Операции не
выдаются за успешные; CURRENT/library/namespace сохраняются. Running River job
или не вернувшийся execution owner продолжают блокировать удаление.

Unrelated obsolete-output cleanup той же позиции не удаляется; admission и
execution отдельно защищают namespace claims архивированных publications от unlink.
Срез `c475140b` прошёл обычный полный commit hook и всю
[CI 42408036183](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42408036183),
включая матрицу платформ, Compose smoke и публикацию контейнера.

Следующее уточнение текста production-подтверждения явно сообщает об удалении
копий анализа и failed-задач без retry, сохраняя точный путь и число entries.
Проверено в браузере на текущем Vite-модуле при 1440×900 и 390×844:
диалог показывает `/srv/inbox` и 12 файлов без горизонтального overflow,
Escape закрывает его и возвращает focus на кнопку удаления. API подменён;
inventory locations вернул 500, удаления не выполнялись. Это проверка текста
и диалога, не сквозная приёмка lifecycle. Попытка запустить дополнительный Vite
на 5175 отклонена занятой портовой привязкой; проверка использовала уже работающий
сервер, актуальность модуля подтверждена отдельным HTTP-чтением.
