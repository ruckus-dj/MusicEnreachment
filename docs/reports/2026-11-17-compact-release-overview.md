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
