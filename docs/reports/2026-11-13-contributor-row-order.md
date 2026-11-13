# Порядок contributor rows

Следующий ограниченный срез открытого плана 13 после `9e82d8fd`.

Действующий контракт сохраняет порядок descriptive credits
(`docs/design/navidrome-metadata.md`). Production editor получает уже имеющиеся
в графическом эталоне move up/down controls. Перемещается целая draft row со
стабильным `uiKey`, именем, ролью, инструментом, MBID и `artist_id`, а не отдельное
имя или ID. Запись проходит через прежний dirty/revision PATCH путь; wire
contract, entity links и provider identity не меняются.

На границах списка и во время сохранения соответствующие кнопки недоступны.
Labels локализованы; визуальный мир сохраняется. Always-visible basics остаются
отдельным срезом. Полный план 13 не закрывается.

## Статус

Реализация завершена; независимое ревью production diff не нашло блокеров.
В существующем frontend regression проверяются disabled boundary controls,
сохранение DOM input при перемещении и полный reordered PATCH payload.
Дополнительное ревью заметило потерю прежнего add-MBID сценария при изменении
fixture; сценарий восстановлен вместе с change/clear, без нового теста.
Biome проверил оба файла. Полный hook и browser-приёмка ещё не завершены;
зелёная CI `43478345845` относится только к предыдущему commit `9e82d8fd`.

## Проверки `1a630839`

Обычный полный hook прошёл, commit отправлен; вся
[CI 43483367417](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43483367417)
зелёная, включая Windows и публикацию контейнера. Acceptance-only процесс штатно обновлён PID 19956 → 24125;
PostgreSQL и 8787 не менялись.

Browser создал временный matching-only release
`0122463c-bbf8-4e7a-9bd5-904c5d38c9b6`. В position editor добавлены две строки
с разными именами, ролями, инструментами и MBID; move down сохранил тот же DOM
input и переставил целые строки. Save вернул 200 и reordered PATCH B→A;
reload подтвердил порядок. RU move up сохранил парные instrument/MBID; draft
отменён без API requests. Граничные кнопки disabled; mobile 390×844 без page
overflow. Связанных `artist_id` в browser fixture нет: их сохранение покрывается
frontend regression, не объявляется browser-проверкой.

Cleanup временного scope пока не завершён: exact deletion preview показал
ноль CURRENT outputs, но blocker `publication cannot be safely deleted`.
Confirmation не нажимался; причина исследуется read-only, safety checks
не обходятся. Публикация не запускалась. Это незавершённый cleanup, не успешная
приёмка удаления и не закрытие плана.

Read-only диагностика установила: release preview уже возвращал пустой список
blockers после завершившегося reconciliation, но ранее загруженный artist preview
оставался в диалоге при закрытии/повторном открытии. После browser reload новый
artist preview показал ноль outputs/blockers; штатное confirmation создало
operation `a87d06ba-3dc3-5237-9e5f-f4fb754c5a60`, завершившуюся succeeded.
GET подтвердил 404 временных release/artist. Cleanup завершён без обхода guards.
Сохранение старого preview без очевидного refresh остаётся отдельным UX-пробелом.
SHA-256 обоих baseline CURRENT и трёх исходников сохранены; readbacks —
`$TMPDIR/opencode/plan13acceptance/reorder-*`.
