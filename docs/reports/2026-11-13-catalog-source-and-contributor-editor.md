# Каталог, исходники и descriptive contributor MBID

Продолжение открытого плана 13 после `1483ef85`. Текущий контракт не меняется.

## Ограниченный scope

- RU/EN presentation главного каталога во всех трёх views и сведений об
  исходниках позиции; query/filter values, routes и бизнес-данные сохраняются.
- Видимый UUID варианта заменяется display-only индексом строки. Это не новая
  стабильная идентичность: внутренние IDs и React keys сохраняются.
- Устаревший empty-state direct-create текст заменяется действующим путём
  source → analysis → provider-mediated matching.
- Optional descriptive contributor MBID становится редактируемым и видимым
  в read mode через существующие draft/revision PATCH механизмы. Очистка
  опускает `mbid`, сохраняя `artist_id`, имя, роль и инструмент строки.

Descriptive MBID разрешён `decisions.md:283–290` и
`navidrome-metadata.md:151–155,165–175`: он не создаёт artist entities/links
и не меняет provider identity. Backend/API уже поддерживают это поле;
генерация нового wire contract не требуется. Prototype уже содержит MBID input.

Reorder целой contributor row и always-visible basics остаются отдельными
незавершёнными срезами. MusicBrainz Disc ID, composersort и lyricistsort
не возвращаются.

## Статус проверки

Реализация завершена; независимое ревью catalog/source и MBID не нашло блокеров.
Biome проверил семь frontend-файлов без дополнительных исправлений.
Новые runtime-проверки ещё не запускались;
результаты предыдущего `1483ef85` не являются проверкой текущего дерева.
Полный обычный hook запускается только после завершения всех правок.

MBID-срез реализован в `ReleaseMetadataEditor.tsx` и существующем regression
test: actual PATCH payload покрывает изменение, добавление и очистку MBID с
сохранением `artist_id` и остальных полей. Независимое ревью двух файлов не
нашло блокеров; dirty/revision/error-retention механизмы не менялись.

Первый обычный hook остановился на четырёх frontend assertions: два старых
RU expectations «Не хватает» вместо «Отсутствует», `aria-checked` вместо native
input `checked` и неоднозначный single-element поиск retained artist notice
после расширения fixture до трёх contributors. Backend suite прошёл. Эти ошибки
исправляются в assertions, без изменения production или ослабления gates;
commit и новый runtime acceptance пока не выполнены.
