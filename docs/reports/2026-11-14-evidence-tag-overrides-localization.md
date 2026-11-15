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

## Проверки `48a844f6`

Повторный обычный полный hook прошёл, commit отправлен; полная CI
`43523218873` выполняется. Acceptance-only процесс 18080 штатно обновлён
PID 64931 → 71128; PostgreSQL и 8787 не менялись.

Browser реального backend подтвердил RU/EN evidence headings, пояснения и
empty-candidate/factors состояние третьей группы без locale-only API requests
и page overflow. Candidate confidence/raw JSON и override draft retention
проверены frontend fixtures, не real-backend browser E2E. Полная визуальная
theme/size приёмка этого среза ещё не завершена; подтверждений не выполнялось.
Зелёная CI предыдущего commit не является его проверкой. План 13 открыт.

Вся [CI 43523218873](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43523218873)
для `48a844f6` завершилась успешно с первого attempt, включая Windows и
публикацию контейнера. Это завершает проверку own-copy среза, но не подменяет
оставшуюся browser-приёмку populated evidence/overrides, theme/size и сквозного
сценария. Общий план 13 остаётся открытым.

Дополнительный browser проход `48a844f6` после reload обеих acceptance tabs:
empty-candidate inspector доступен в RU/EN × light/dark × 1440×900/390×844,
page overflow отсутствует. Это проверка видимости и layout пустого inspector,
не contrast/screenshot audit и не populated evidence/override browser coverage.
SHA-256 обоих CURRENT и трёх исходников вновь совпали с baseline; readback:
`$TMPDIR/opencode/plan13acceptance/evidence-grouping-hashes.txt`.
