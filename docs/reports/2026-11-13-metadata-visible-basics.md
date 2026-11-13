# Постоянно видимые основные metadata

Следующий ограниченный срез плана 13 после `1a630839`.
Карточки сохраняют read/edit геометрию и общую draft/revision логику.

## Scope

Основные поля выводятся один раз в нераскрываемом разделе, как в самостоятельном
эталоне `metadata/editorLayout.ts` / `MetadataEditor.tsx`:

- release: title, release date, country, label, catalog number и оба типа релиза;
- recording: title, content advisory, ISRC;
- position: track number и disc number.

Владелец подтвердил совместное постоянно видимое отображение основного и
дополнительных типов релиза; решение внесено в тематический раздел
`docs/design/decisions.md`. Поля сохраняют отдельные значения и семантику.
Остальные поля остаются в прежних тематических disclosures, без дублирования
и автоматического раскрытия всех групп при Edit. Artist-credit controls,
genres, ownership, API и сохранение не перерабатываются.

## Статус

Реализация завершена; прежний field renderer переиспользуется для обоих видов
групп. Добавлена минимальная frontend проверка постоянно видимых release basics
и сохранения закрытого additional group при Edit; прежние MBID/reorder assertions
сохранены. Biome и механический design detector прошли (findings `[]`).
Независимое ревью не нашло блокеров. Runtime-проверки текущего дерева ещё не проводились;
CI предыдущего `1a630839` не является проверкой этого среза.
План 13 остаётся открытым.

## Проверки `f8ca7b63`

Обычный полный hook прошёл, commit отправлен. Полная
[CI 43487504866](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43487504866)
выполняется. Acceptance-only процесс 18080 штатно обновлён PID 24125 → 28646
gate-built бинарником; PostgreSQL и 8787 не менялись.

Browser проверил release/recording/position basics в обеих локалях, темах и
размерах 1440×900/390×844 (24 edit состояния), плюс read→edit placement:
основные поля не имеют details ancestor и остаются в том же разделе; оба
release types видны вместе. Additional groups и artist credits не раскрываются
от входа в Edit. Page overflow отсутствует, mobile dark release section также
просмотрен по реальному screenshot.

Release title и additional comment изменены только в draft: закрытие/открытие
группы и RU/EN сохранили оба значения, закрытая группа сохранила состояние,
language-only API requests отсутствуют. Cancel выполнен; PATCH не отправлялся.
Это read/edit/cancel приёмка presentation, не новый save/publication сценарий.
Оба baseline CURRENT и три исходника сохранили SHA-256;
`$TMPDIR/opencode/plan13acceptance/basics-hashes.txt`. Временные library-записи
для этого среза не создавались.

Первый CI attempt остановился на Windows jobs suite: два River dispatch/recovery
теста не дождались завершения job (`context deadline exceeded`), после чего
notifier многократно сообщал ту же ошибку. Это не прежний PostgreSQL cleanup
сбой и не green CI. Остальные платформы, verify и Compose прошли; container
publication skipped. Причина исследуется по failed-step log и helper timings;
код, gates и таймауты не менялись. Следующий implementation slice не начат.

Независимый tester подтвердил provisioning delays: clone 19–24 секунды,
ожидание clone connection до 14 секунд, fixture open до 33.56 секунды.
Это согласуется с нагрузкой runner/БД, но не устанавливает точный bottleneck
и не исключает отдельную application-проблему. Повторены только failed jobs;
attempt 2 выполняется. При повторном падении требуется новый разбор timings,
а не автоматическое приписывание той же причины или повышение таймаутов.

Attempt 2 завершился успешно: вся CI `43487504866` зелёная, включая Windows
и публикацию контейнера. Код и параметры проверок не менялись. Повтор устранил
неподтверждённость результата, но сам по себе не доказывает точную причину
первого инфраструктурного/нагрузочного сбоя.
