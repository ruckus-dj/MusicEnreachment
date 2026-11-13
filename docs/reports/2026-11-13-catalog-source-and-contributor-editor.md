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

## Проверки `9e82d8fd`

После исправления assertions обычный полный hook прошёл; commit отправлен в
go-development. [CI 43478345845](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43478345845)
полностью зелёная, включая Windows x64 и публикацию контейнера, с первого CI
attempt. Acceptance-процесс 18080 штатно
обновлён PID 11686 → 19956 gate-built бинарником. PostgreSQL и 8787 не менялись.

Browser-приёмка реального backend:

- Все три catalog views проверены в light/dark × RU/EN × 1440×900/390×844
  (24 состояния), с ожиданием специфичных view headers и загруженных строк,
  а не оставшейся DOM предыдущего view. Везде две baseline записи, page overflow
  отсутствует; mobile tables сохраняют собственный горизонтальный scroll.
- Смена языка сохранила route, publication filter, тот же search DOM node и
  несохранённый search draft; language-only API requests отсутствуют.
- Source table показывает row header `1` вместо UUID; labels RU/EN меняются,
  пути, selection, policy и raw server facts сохраняются. Dark source section
  проверен в обеих локалях и размерах; light также проверен на реальном CURRENT.
- MBID draft сохранился при смене языка без API requests; отменён без записи
  в baseline CURRENT. На временном третьем library-релизе UI Save добавил,
  изменил и очистил MBID (HTTP 200, expected revisions 1/2/3). Последний PATCH
  опустил `mbid`; reload подтвердил сохранённые имя, роль composer и пустой MBID.
  `artist_id` в этом browser fixture отсутствовал: его сохранение покрывает
  frontend regression, не объявляется browser-проверкой связанного артиста.
- Временный release `67f9cfce-64c7-4cdd-a4d5-db9196087fa5` удалён через browser
  Delete artist → exact preview → confirm. Scope содержал одну позицию и ноль
  CURRENT, blockers отсутствовали; operation
  `be490b2f-1bd8-51a5-a6b3-91aa43ad2f51` succeeded.

Все записи выполнены browser-контролами; дополнительных API mutations нет.
Read-only GET подтвердил удаление временных artist/release и сохранение Local
provider release. SHA-256 обоих baseline CURRENT и трёх исходников не изменились.
Readbacks — `$TMPDIR/opencode/plan13acceptance/catalog-editor-*`.
Публикация не запускалась. Это не полный setup/scan/analysis browser E2E.
