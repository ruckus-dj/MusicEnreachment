# Evidence inspector и tag overrides: RU/EN

Следующий ограниченный срез плана 13 после `3635f18e`.

## Scope

Собственные labels, headings, доступные имена, пояснения и missing/empty
placeholders `MatchingEvidenceInspector` и `TrackTagOverrides` переводятся
RU/EN. Evidence keys, raw JSON/values, музыкальные и пользовательские данные
не переводятся. Порядок и распознавание factors/profiles/edges сохраняются.

Override draft shape, callback timing, disabled state и row identity не меняются.
Locale не вызывает запросов или сброса drafts. Parent authority/preview/query
логика не перерабатывается. Grouping raw IDs — отдельный presentation-пробел.

## Статус

Реализация четырёх frontend файлов завершена; Biome formatting/import
organization и diff whitespace check прошли. Existing locale-toggle regression
расширен сохранением override draft, raw tags и confidence при смене языка без
API requests. Независимое ревью выполняется; runtime-проверки ещё не проводились.

Ревью подтвердило production boundaries и выявило неоднозначные override
selectors в fixture с двумя позициями. Они ограничены fieldset первой позиции
по полному accessible name; retention/raw evidence/request assertions сохранены.

Первый обычный hook прошёл backend, но остановился на неоднозначном raw-tag
assertion нового теста: «Исходник» одновременно есть в metadata и evidence JSON.
Обе locale-фазы теперь проверяют конкретный raw-tags PRE внутри evidence region
и semantic JSON с ALBUM/TITLE, вместо глобального поиска текста. Production
код не менялся; Biome проверил исправленный тест. Commit пока не создан.
Зелёная CI предыдущего commit не является его проверкой. План 13 открыт.
