# Компактный обзор релиза: presentation-срез

Основание — план 13 и графический эталон `docs/app-design-react/02-screens.md`
90–104; продуктовый контракт остаётся в `docs/design/decisions.md`.

## Реализация после `eba04403`

Общий маршрут релиза показывает компактные medium/position links вместо
повторения полных position workspaces. Рабочий экран позиции сохраняется на
существующем position route. Headless publication controllers остаются
смонтированы для всех подходящих позиций, вместе с parent-owned reads/monitors.
Для tracks без medium добавлены переходы без выдумывания новой принадлежности.
Ошибки чтения позиции доступны из обзора с повторным чтением.

Компактная обложка шапки получает selected generation через callback существующего
cover editor; pending generation не подменяет selected. Дублирующее чтение cover
не добавлено; editor сохраняет владение mutations и остаётся смонтированным.

## Проверка и ограничения

Реализация и адаптация existing tests переданы на независимое ревью. Требуется
проверить сохранение aggregate-overview invalidation coverage после переноса
workspace assertions на position route. Форматирование/import assists выполнены,
`git diff --check` чист. Полный gate, CI и browser-проверки ещё не выполнялись.
Предыдущий Settings-срез имеет успешную полную CI `43992174938`.

Независимое ревью production-блокеров не выявило. Дорабатываются два existing
теста: invalidation на общем обзоре и сохранение DOM identity обоих preview
controllers на overview→position 1→position 2→overview с корректным переключением
aggregate-managed. Это проверка сохранения admission fences, не новая функция.

Оба замечания ревью учтены: overview invalidation восстановлен, existing
controller-bank regression расширен переходами с сохранением DOM identity и
одного QueryClient/provider tree. Форматирование чистое. Следующий этап —
обычный полный commit hook; runtime-результат ещё не подтверждён.

Первый hook остановился на двух Biome exhaustive-dependencies errors:
лишний positionId в test mock effect и отсутствующий onCoverSnapshot в cover
read effect. Коммит не создан; зависимости исправляются с проверкой стабильности
callback, без отключения lint и без добавления повторных cover reads.

Лишняя mock dependency удалена. Cover read использует latest-callback ref:
inline parent callback не становится причиной нового fetch, load effect остаётся
release-scoped с прежними active/request-scope guards. Форматирование выполнено;
повторный полный hook ещё не подтверждён.

Повторный hook прошёл lint, затем остановился на двух frontend assertions:
manual-position-refresh тест остался на overview route, а read-error regression
ожидал fallback вместо raw `Failed to fetch`. Адресно исправляются route fixture
и ожидаемое raw сообщение; recovery assertions и production behavior сохраняются.
151 из 153 frontend tests прошли; commit пока не создан.

## Результат `f8b42863`

Адресные исправления двух тестовых ожиданий приняты; обычный полный hook прошёл,
commit отправлен. CI `44001037696` выполняется. Acceptance-only сервер обновлён
PID 45339 → 55576 на прежней отдельной БД, 8787 не затронут.

Browser реального backend: overview содержит medium position link, без article
workspaces и lyrics textarea. Aggregate preview показал Unchanged. Переход по
позиции открыл ровно один article, sources и lyrics; Full release вернул overview
без workspaces. После возврата aggregate требует Prepare preview заново; сохранение
готового preview при смене маршрута не заявляется. RU/EN × light/dark ×
1440×900/390×844: page overflow отсутствует, overview не монтирует articles/lyrics.
На этом релизе selected cover отсутствует: populated header artwork и его размер
в браузере пока не проверены. Скриншоты не добавлялись. Общий план ещё открыт.

CI `44001037696`, attempt 1: Windows API package завершился cleanup error
`supervisor did not respond to shutdown; force-terminated without confirming
PostgreSQL cleanup` (131.987s). Остальные jobs прошли, контейнер skipped.
Лог `final-acceptance-20261117/windows-f8b42863-attempt1.log` передан на разбор;
полная CI красная. Диагностическая сборка supervisor не запускалась без согласия
владельца, gates/таймауты не менялись.

Разбор текущего лога подтвердил PostgreSQL shutdown/checkpoint completion при
отсутствии подтверждённого supervisor exit; assertion failure в failed-step log
не обнаружен. Package failure не считается PASS. С учётом уже повторявшейся
необъяснённой ошибки новый неизменённый rerun не выполняется. Нужны данные внутри
supervisor; ранее предложенный bounded диагностический прогон ожидает согласия.

Browser cover check на `f8b42863`: через Choose PNG or JPEG загружен существующий
локальный JPEG fixture и нажата только Save pending cover. Показаны pending image
и notice об отсутствии изменения publications; header image остался отсутствующим
(selected не задан). Apply/Retry не нажимались. Новый CURRENT и его исходник
сохранили SHA-256. Повторная baseline-проверка остановилась: прежний checksum-файл
`first-current-file-after-second.sha256` не найден; baseline hashes в этом проходе
не подтверждены. Это не доказательство изменения самих контрольных файлов.

После согласия владельца продолжить приёмку (`Делай`) на disposable релизе
отдельной fresh БД через UI нажата Apply to all published tracks. Единственная
позиция успешно перепубликована: generation `e3034018-18ee-4f87-ba08-bf3783fbf5cd`,
browser показывает Operation completed и Selected cover embedding: verified.
Это намеренное изменение нового acceptance CURRENT, не двух baseline CURRENT;
его прежний checksum больше не является ожидаемым текущим значением.

Selected image `17e7a03c-c315-4494-86af-dd80b71766f5` загрузился в шапке
(natural width 500, rendered 62×62 внутри compact cover). Затем через UI сохранён
другой JPEG только как pending `eb02a957-b703-465e-8646-65adc6d959df`:
header URL остался selected, pending отображается отдельно. Повторные Apply/Retry
не запускались. Проверка populated selected-versus-pending теперь выполнена.

Поиск baseline evidence: пять исходных checksum-артефактов и их digest values
не найдены в проверенных repository/temp evidence paths. Все два контрольных
CURRENT и три исходника физически присутствуют. Сохранившиеся `...hashes.txt`
содержат только прежние `OK` и пути: они подтверждают факт исторической проверки,
но не позволяют повторить сравнение с исходными digest. Новые hashes не выдаются
за восстановленный baseline; исторические успешные проверки не переписываются.

Дополнительный поиск в retained shell outputs также не нашёл исходные digest/file
associations пяти baseline-файлов. Повторное сравнение с историческим baseline
остаётся неподтверждённым; два сохранившихся fresh checksum-файла относятся к
другим объектам и не используются как замена.

## Populated evidence / override: отдельный fixture-assisted browser

На `f8b42863` создан отдельный context `plan13-populated-evidence-fixture`.
GET incoming snapshot/review перехвачены данными existing fixture из
`IncomingGroupReviewScreen.test.tsx` (два файла, повтор recording на двух discs,
saved release confidence 0.87 и edge medium/position). Все non-GET API requests
блокируются route handler, preview/confirm не нажимались; реальная БД не менялась.

Через UI выбран релиз, раскрыты raw tags и включён title override. Inspector
показал saved 0.87, отсутствие position confidence как not provided, raw
ALBUM/TITLE без перевода и сохранённые edge coordinates без вычисленных factors.
Draft сохранился EN→RU→EN в том же DOM node без запросов. При 1440×900 inspector
доступен, page overflow отсутствует, кнопка Preview all assignments проходит
hit-test после прокрутки. Это populated UI-проверка на fixtures, не real-backend
E2E и не доказательство живой provider evaluation; fresh manual group по-прежнему
проверяет реальное пустое состояние candidate edges.
