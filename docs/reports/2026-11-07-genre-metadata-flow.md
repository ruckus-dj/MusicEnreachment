# Жанры: metadata и файловая проекция

Базовая ревизия: `1030b7e635ece9ded2f428860a60314255434b77`.
Работа относится к пункту 7 программы
[`13-main-interface-design-parity.md`](../plans/todo/13-main-interface-design-parity.md).
Продуктовые правила находятся в текущем разделе
[`Метаданные и управление`](../design/decisions.md#метаданные-и-управление),
а не в этом отчёте.

## Реализованный срез

- Metadata API хранит genre references с MBID, исходным и читаемым именами.
  Artist scope получил тот же optimistic revision workflow; колонка добавлена
  в существующий fresh-instance baseline, без новой цепочки миграций.
- Новые выбранные MBID проверяются по каталогу; сохранённые ссылки этого объекта
  сохраняют серверные имена независимо от обновления каталога. Удаление части
  сохранённого списка не требует повторного выбора оставшихся жанров.
- Production picker перенесён из presentation прототипа и подключён к API.
  Локальные overrides редактируются для artist, release и recording; очистка
  и отсутствие override различаются. Каталог не становится источником
  автоматической перезаписи metadata.
- Файловая проекция использует утверждённый fallback; источниками не служат
  generic tags или теги исходного файла. MusicBrainz lookup запрашивает `genres`.
- Добавлены browser preference RU/EN/system и scoped-переводы genre editor;
  остальные production-экраны ещё не локализованы полностью.

## Проверенные уровни

- Standalone-прототип: `task design:check` после исправления scope-validation
  и приоритета release override прошёл: 42 файла / 224 Vitest-теста,
  проверки fixtures, TypeScript и Vite build. Предыдущий успешный прогон
  не обнаружил ошибку применения release override поверх recording genres;
  независимое ревью потребовало проверки фактического `applyMetadataPreview`.
- Первичная генерация API обнаружила ошибки интеграции: каталог принимал
  `*bun.DB`, тогда как metadata repository работает с `bun.IDB`, и в provider
  projection отсутствовали imports. После выделения общего reader по `bun.IDB`
  `task generate` прошёл; generated client не редактировался вручную.
- Прототип открыт в свежем browser context на desktop 1440×1000; главная
  медиатеки и карточка Mira Vale доступны без ошибок JavaScript и без
  горизонтального overflow. Это не проверка production API или native writer.
- Production genre editor проверен на контролируемых API-ответах во всех восьми
  комбинациях ширины 1440/390, light/dark и RU/EN. Проверены поиск без свободного
  создания жанра, сохранение MBID-bearing references, удаление одного выбранного
  жанра при недоступном каталоге, `explicit_empty` и `absent`, возврат фокуса и
  Escape вложенного picker без отмены родительского draft. JavaScript errors и
  горизонтальный overflow отсутствовали. Edit/Save имеют одинаковые bounding
  boxes в обеих ширинах. Запросов запуска publication не было. Это UI-проверка
  с mocks, не реальный upstream/ingest-to-publication E2E.
- Механический UI detector на новых компонентах не обнаружил замечаний.
- Первый обычный commit-hook прошёл generation и Go lint, затем остановился на
  Biome (React effect dependencies, imports и ARIA). Application tests этим
  запуском ещё не проверены; gate не ослаблялся и не пропускался.
- Повторный hook прошёл lint и обнаружил в Go-тестах неверное имя FK-колонки
  нового provider genre reader, старые string/empty-value fixtures и JSON-order
  assertion. Это исправления среза, не инфраструктурный сбой; новая запись HEAD
  до успешного обычного hook не создаётся.

## Ограничения

Четвёртый hook прошёл все тесты (frontend 108, tools 34, prototype 224),
но финальная TypeScript-сборка обнаружила два неподдерживаемых React Aria props.
После замены на `excludeFromTabOrder` и `disabledKeys` диагностические `tsc -b`
и Biome прошли. Полный hook повторяется без ослабления gate.

Третий полный hook прошёл Go suite и остановился на двух UI-тестах,
обращавшихся к родительской форме при открытом modal и ожидавших поисковые
options без повторного поиска. Исправлен обычный пользовательский сценарий
тестов, без изменения production поведения; диагностический прогон файла
прошёл 12/12. Итоговый полный gate ещё требуется.

Редактор этого среза работает с локальными overrides artist/release/recording.
Состояние «без переопределения» не выдаётся за отображение полного provider
baseline в карточке. Его отдельное readback-представление, Local CRUD и общий
explicit repeat/aggregate publication workflow остаются в программе плана 13.

Application gate и GitHub CI этого среза пока
не завершены. Отчёт будет дополнен их фактическими результатами; перечисленные
выше проверки не означают завершения пункта 7 или полного графического паритета.
Screenshot-артефакты не создавались.
