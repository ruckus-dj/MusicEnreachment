# Incoming overview/review: RU/EN

Продолжение открытого плана 13 после `ac58c532`.

## Ограниченный scope

Локализация собственных текстов `IncomingGroupsOverview` и
`IncomingGroupReviewScreen`: headings, labels, доступные имена, layout/position
labels, metadata fallback, own notices/errors. Сохранённые собственные сообщения
становятся semantic descriptors и переводятся при render, а не при записи
state; refresh controls не зависят от поиска русской подстроки в error.

Queries, drafts, preview, confirm/authority intents, scope generations и wire
values сохраняются. Музыкальные данные, пути, IDs, revisions/digests и raw server
diagnostics не переводятся. Local matching copy уже локализован и не переделывается.

Вложенные grouping editor, manual pickers, evidence inspector, track overrides
и operation follower не входят в этот срез: их оставшиеся RU-строки не объявляются
исправленными. Это не завершение всего Incoming RU/EN или полного browser E2E.

## Статус

Реализация четырёх frontend файлов завершена, Biome formatting/import
organization и diff whitespace check прошли. Existing RU assertions используют
явную locale fixture; добавлен один locale-flip сценарий с assignment/preview,
semantic conflict/refresh notice и request counters. Независимое ревью выявило
неоднозначные assignment selectors в новом тесте (fixture содержит два файла);
assertions приведены к тому же первому control, который изменяет сценарий.
Других блокеров review не нашло.
Runtime-проверки текущего дерева ещё не проводились;
зелёная CI предыдущего commit не является его проверкой.

## Проверки `a5fb17e1`

Обычный полный hook прошёл, commit отправлен; CI `43504329510` выполняется.
Acceptance-only процесс штатно обновлён PID 40931 → 47496; PostgreSQL и 8787
не менялись.

Browser реального backend проверил overview и открытый review третьей группы
в light/dark × RU/EN × 1440×900/390×844 (8 состояний) без page overflow.
Headings/ARIA, group options, counts, source/position labels следуют локали;
raw authority mode и musical/path facts сохраняются. Вложенные pickers и
evidence inspector остаются русскими в English по указанной границе среза.

Открыт Local release, создан только read-only Local matching preview:
RU/EN сохранил выбранную группу, тот же assignment DOM node и его ID, готовый
preview, без language-only API requests. Matching confirmation и publication
не запускались, временных library-записей нет. Это **не** real-backend проверка
основного manual matching preview/conflict: сохранение его semantic feedback
проверено frontend regression с API fixtures.

Приёмка обнаружила две небольшие copy-поправки этого среза: English `1 of 1
files` и отсутствие пробела между предложениями summary. Они исправляются
отдельной минимальной поправкой; CI предыдущего snapshot не считается её gate.

`35d57845`: copy-поправка прошла обычный полный hook и отправлена; CI
`43504970853` выполняется отдельно от `a5fb17e1` / `43504329510`.
Acceptance-only процесс обновлён PID 47496 → 49549; browser нового бинарника
подтвердил `1 of 1 file with saved data` и разделённые пробелом предложения,
без page overflow. SHA-256 обоих CURRENT и трёх исходников совпал с baseline:
`$TMPDIR/opencode/plan13acceptance/incoming-review-hashes.txt`.

Первый CI attempt `a5fb17e1` остановился на Windows migrations package после
`PASS`: PostgreSQL supervisor не подтвердил shutdown. Cleanup stop занял
1m0.784s (shutdown 12.214s, remove/release 48.571s); named test failures/panic
в failed-step log нет. Остальные платформы прошли, container publication skipped.
Разобран log `$TMPDIR/opencode/plan13-incoming-windows-failed.log`, повторены
только failed jobs; attempt 2 выполняется без изменения кода/gates/таймаутов.
Результат `35d57845` / `43504970853` учитывается отдельно.

Вся CI `43504970853` для `35d57845` завершилась успешно, включая Windows
и публикацию контейнера. Attempt 2 исходного `a5fb17e1` / `43504329510`
ещё выполняется; зелёный результат последующей поправки не подменяет его.

Attempt 2 `43504329510` завершился ошибкой Windows: теперь persistence package
получил `panic: test timed out after 10m0s` (600.640s), а не migrations cleanup
сбой первого attempt. Container publication skipped; исходная CI не зелёная.
Новый failed-step log сохранён в
`$TMPDIR/opencode/plan13-incoming-windows-attempt2.log`; goroutine stacks и
fixture timings переданы на независимый разбор. Автоматический повтор без
диагноза не выполняется, gates/таймауты не меняются; следующий срез без правок.

Независимый разбор attempt 2 выявил ожидание PostgreSQL fixture creation:
`testpostgres.createDatabaseForFixture` выполняет `CREATE DATABASE … TEMPLATE`
через ordinary COPY; другие parallel tests также ожидают provisioning.
Активным тестам на момент dump было 3–29 секунд. Это свидетельство медленного
provisioning/нагрузки, не доказательство application deadlock или точной причины
задержки. После разбора повторены только failed jobs, без изменения gates и
таймаутов. При новом persistence timeout требуется исследование Windows
parallel fixture-clone path, а не очередное автоматическое повторение.

Attempt 3 завершился успешно: вся
[CI 43504329510](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43504329510)
зелёная, включая Windows и публикацию контейнера. Вместе с отдельной зелёной
CI `43504970853` это завершает проверку own-copy overview/review среза.
Gates и таймауты не менялись; успешный повтор не устанавливает точную причину
прошлых provisioning/cleanup задержек. Вложенные manual pickers, grouping,
evidence/tag overrides и общая сквозная приёмка остаются открытыми.
