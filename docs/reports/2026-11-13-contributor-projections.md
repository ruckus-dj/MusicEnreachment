# Contributor projections и продолжение приёмки плана 13

## Предыдущие срезы

- `159468ce`: тональность recording + position override; обычный полный hook
  и [вся CI](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43404395660)
  успешны.
- `7e5eacb3`: удаление после settled reconciliation; обычный полный hook
  и [вся CI](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43315446319)
  успешны. Успешная API-assisted приёмка удаления отдельно описана в плане;
  она не является browser E2E.

## Browser-проверка отдельного acceptance backend

Проверена работающая ревизия `7e5eacb3`, `127.0.0.1:18080`, контекст
`manual-acceptance-7e5eacb3`. Стенд 8787 не изменялся.

На dark 390×844 открыто preview удаления Plan13 Second Artist. Показаны
читаемое имя артиста, Plan13 Second Album, одна позиция и один безопасный
CURRENT. Диалог помещается по ширине; обе кнопки внутри viewport, горизонтального
переполнения страницы и диалога нет. Cancel закрывает диалог и возвращает фокус
на Delete artist; alerts отсутствуют. Confirm deletion не нажимался.

После проверки SHA-256 обоих оставшихся CURRENT и всех трёх исходных FLAC
совпал с сохранённым baseline из `$TMPDIR/opencode/plan13acceptance`.

Ограничения: browser-confirm/delete не выполнен, два CURRENT сохраняются.
Это дополнительная проверка preview/отмены, не весь сквозной путь и не полная
визуальная приёмка. При English карточка артиста всё ещё содержит русские
подписи («Медиатека», «позиций», «С аудио», «опубликовано», «Дата не указана»,
«Показать позиции»); RU/EN-паритет не принят.

Через общую навигацию Library → артист → релиз открыта первая сохранённая
публикация (без обходного URL). На desktop 1440×900 раскрыта группа Contributors
в Recording metadata: все шесть disclosure-групп имеют одинаковые x/width
(242/972 px), Contributors раскрывается в той же вертикальной колонке;
при закрытии Edit сохраняет bbox 120×30 px и ту же позицию. Горизонтального
переполнения страницы нет. На 390×844 те же шесть групп имеют одинаковые x/width
(34/322 px), раскрытая Contributors остаётся в колонке без page overflow.
Проверка ограничена пустым сохранённым
contributors и DOM-геометрией; это не приёмка заполненного contributor editor.

## Фактические пробелы contributor projection

До этого среза production editor/storage сохраняют director и contributor MBID,
но `projectContributors` не проецирует DIRECTOR и ни один из 11 role MBID.
Общий флаг неполноты не различает роли. Поля composer/lyricist sort и
MusicBrainz Disc ID исключены и в срез не входят.

Одной проверки JSON или симметричного writer/readback недостаточно: нужны
файловые aliases, распознаваемые потребителем, и позиционное соответствие
конкретной role/subrole-группы. Имена с запятыми нельзя разбивать, отсутствующий
ID нельзя выкидывать из середины массива с последующим сдвигом.

### Проверенные файловые aliases

Сверены pinned Navidrome
[`mappings.yaml`](https://github.com/navidrome/navidrome/blob/101145742f4762164202cb4858c9126258bdc463/resources/mappings.yaml),
[`map_participants.go`](https://github.com/navidrome/navidrome/blob/101145742f4762164202cb4858c9126258bdc463/model/metadata/map_participants.go)
и [`gotaglib.go`](https://github.com/navidrome/navidrome/blob/101145742f4762164202cb4858c9126258bdc463/adapters/gotaglib/gotaglib.go).
Потребитель сначала читает normalized PropertyMap и приводит ключи к lowercase:
обычные `MUSICBRAINZ_<ROLE>ID` распознаются без дополнительной записи raw aliases.
Generic TXXX fallback TagLib 2.3.2 сохраняет underscore-описания
([source](https://github.com/taglib/taglib/blob/v2.3.2/taglib/mpeg/id3v2/frames/textidentificationframe.cpp)).
Для MP4 uppercase также необходим для generic freeform write path
([source](https://github.com/taglib/taglib/blob/v2.3.2/taglib/mp4/mp4itemfactory.cpp)).
Spaced raw TXXX/freeform aliases не добавляются: они не эквивалентны underscore
ключам в PropertyMap и не нужны для проверенного normalized пути.
Все 11 role IDs — поддерживаемые Navidrome-представления, не утверждение, что
все они являются стандартными полями Picard.

Performer ID имеет тип pair: плоский массив UUID не связывает ID с инструментами.
Нужны `PERFORMER:<INSTRUMENT>` и `MUSICBRAINZ_PERFORMERID:<INSTRUMENT>` с одинаковым
порядком внутри подроли. Для пустой подроли normalized sentinel зависит от base
key: `PERFORMER:PERFORMER` и
`MUSICBRAINZ_PERFORMERID:MUSICBRAINZ_PERFORMERID`.
Ограничение MP3/M4A на structured performer сохраняется вместе с его ID.

### Границы проверки

Расширен существующий `TestMetadataWriterRoundTripsKnownStandardFields`, без
новой DB-матрицы: DIRECTOR с comma/Unicode, десять обычных role MBID, paired
performer IDs с несколькими инструментами и пустой подролью. Проверяется
native writer/readback FLAC/MP3/M4A/MKA; performer применяется только к FLAC/MKA.
UUID в fixture валидны и позиционно согласованы с именами.
Это проверка транспортного слоя. Совместимость с consumer обоснована также
исходниками выше; живой Navidrome scan в этом срезе не выполнялся.
Contributor-ID UI не расширяется этим projection-only срезом: существующие
идентификаторы сохраняются; это не завершение полного metadata editor port.

Ревью дополнительно проверило consumer-side splitting/deduplication: успешный
native roundtrip не доказывает alignment, если Navidrome разбивает role name по
`/` или `;` либо удаляет повторные имена/IDs. Отдельно проверяется коллизия пустой
performer-подроли с буквальным инструментом `performer`.

В реализации DIRECTOR и десять обычных role-ID tags используют единый mapping;
performer даёт одиннадцатую MBID-проекцию через paired keys. Неполная или
непредставимая группа опускает только соответствующие IDs, не имена и не другие
роли. Сравнения для alignment используют trimmed names/canonical UUID; исходные
строки сохраняются. Имена с role-разделителями и за пределом известного
consumer length limit не получают предположительно согласованный ID-массив.
Performer IDs при неоднозначной подроли опускаются целиком, включая оба literal
sentinel-инструмента даже в одиночку. Минимальные assertions добавлены в
существующие projection/footprint tests; новых DB-сценариев нет.

Блокеры независимого ревью исправлены; финальная ревизия проверена повторно.
В последнем проходе найдено и исправлено отсутствовавшее объявление локального
флага alignment. Полный локальный gate выполняется обычным commit hook.
Первый hook выявил невалидный successful-baseline fixture: повтор одного имени
performer имел два разных MBID. Срабатывание suppression было корректным;
fixture и ожидаемый ID для повторённого имени согласованы без ослабления
production guard и без удаления rejection assertions.

`392f8983` прошёл повторный обычный полный hook (`task verify`) и отправлен в
go-development.

## Browser-проверка тональности на новом backend

Только acceptance PID 18749 штатно остановлен SIGTERM и заменён PID 96650 с
hook-built бинарником source snapshot `392f8983`. Build metadata показывает
parent `8272844d` + modified, поскольку обычный hook собирает бинарник перед
созданием commit; это не отдельная сборка другой ревизии. PostgreSQL-контейнер
`plan13acceptance-pg`, данные и стенд 8787 не перезапускались.

В production browser context `plan13acceptance` на 390×844:

- через Recording metadata сохранено `D minor` (PATCH 200, revision 2,
  `publication_applied:false`);
- RU→EN сохранил несохранённый key draft; language-only API-запросов не было;
- Position metadata отобразил `D minor` / Inherited from recording;
- через тот же UI сохранён position override `F♯ minor` (PATCH 200, revision 2);
- штатный Prepare preview показал Update 1 / Blocked 0; mobile inspector показал
  точный diff KEY: absent → `["F♯ minor"]`, INITIALKEY: absent → `[]` для FLAC.
  Внутренний comparison status был `blocked`; это не утверждение о successful
  publication admission. Inspector не вызвал page overflow;
- position explicit_empty (revision 3) показал Explicitly cleared; возврат к
  absent (revision 4) после перечитывания снова показал recording fallback;
- recording также возвращён в absent через UI (revision 3). Сохранены manual
  absent entries, а не побайтово первоначальные пустые metadata envelopes.

Все mutations выполнены браузерными контролами, без API-assisted записи.
Последующие GET metadata служат readback-свидетельством, а не заменой browser
сценария. Confirm ready publications не нажимался. Оба CURRENT и все три исходных
FLAC повторно прошли проверку SHA-256 по прежнему baseline. Новая запись key
в файл этим browser-прогоном не проверена; native writer/readback покрыт gate.

## Browser-проверка director и найденный notice gap

На том же `392f8983` через Recording metadata → Contributors добавлен director
`Director, 監督` без MBID (PATCH 200, revision 4). Mobile preview inspector показал
DIRECTOR: absent → `["Director, 監督"]` без page overflow; сохранение не применяло
publication. После проверки contributors возвращены через UI в manual absent
(revision 5). Подтверждение публикации не выполнялось.

Проверка выявила отдельный presentation gap: причина пропуска contributor-ID
группы сохранена в `TagProjection.Fields`, но не передана через `Notices`,
которые видит обычный preview. Поэтому успешная проекция имени не принимается
за успешное предупреждение о пропущенных IDs; исправление notice выполняется
отдельным минимальным follow-up без требования обязательных MBID.

Follow-up передаёт весь уже собранный список MBID omission reasons в Notices,
не только missing-ID случай: обычные collision/split/dedup и performer
incomplete/ambiguous причины также должны быть видимы. Независимое ревью
подтвердило actual service path; существующие omission assertions проверяют
теперь и Notices. Семантика допуска публикации и optionality IDs не изменена.

В отдельном dark-контексте на том же новом backend проверен несохранённый key
draft `B♭ major` второй записи: 390×844, English → Русский, затем 1440×900.
Draft сохранился, language-only API-запросов нет, `document.lang=ru`, page
overflow отсутствует. На mobile Save/Cancel по 120×40 px внутри viewport;
на desktop disclosure-группы остаются одинаковой ширины. Escape из самого key
input отменил draft и вернул readonly inherited state; Save не нажимался.

План 13 остаётся открытым.
