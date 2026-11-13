# Deletion modal: тема и доступность действий

Следующий ограниченный срез открытого плана 13 после `1c0a7f6c`.
Реальная browser-проверка выявила белый фон Modal при светлом foreground в dark
mode. Свидетельство — [refresh report](./2026-11-13-deletion-preview-refresh.md).

## Scope

Presentation-only port существующих theme tokens и modal/scroll/footer паттерна
самостоятельного прототипа. Fixed light colors заменяются surface/ink/muted/
line/control/error; высота содержимого ограничивается viewport, действия
сохраняют доступность при прокрутке на mobile. Refresh, frozen digest,
uncertain replay, scope guards и подтверждение не перерабатываются.

## Статус

Реализация завершена в `LibraryDeletionControl.tsx`: theme tokens/AppButton,
bounded flex modal, отдельный scroll content и shrink-free footer. Поведение
удаления/refresh не менялось. Biome и diff whitespace check прошли; независимое
ревью выявило потерянные overlay positioning/stacking/backdrop classes.
Они восстановлены; повторное независимое ревью не нашло оставшихся блокеров.
Runtime-проверки текущего дерева ещё не проводились;
зелёная CI предыдущего commit не является проверкой нового presentation.
План 13 остаётся открытым.

## Проверки `93073a42`

Обычный полный hook прошёл, commit отправлен; CI `43498110078` выполняется.
Acceptance-only процесс штатно обновлён PID 35204 → 38996; PostgreSQL и 8787
не менялись. Реальный dark modal теперь имеет surface `rgb(25,30,26)` при
foreground `rgb(227,234,227)`, а не белый фон. На mobile 390×844 его размеры
366×590.8, top 126.6; footer и все три действия внутри viewport, content
scrollHeight 742 при clientHeight 432. Проверены также RU/EN и 1440×900:
page overflow отсутствует, actions доступны после прокрутки.

Screenshot просмотрен. Обнаружена дополнительная same-slice поправка:
AppButton base перекрывает error text token destructive кнопок, поэтому
приоритет danger classes уточняется по существующему prototype pattern.
До повторной проверки computed danger colors визуальный срез не объявляется
полностью принятым. Confirm удаления baseline не нажимался.

## Проверки `ac58c532`

Двухстрочная token-priority поправка прошла обычный полный hook и отправлена;
CI `43498726954` в очереди. Acceptance-only процесс обновлён PID 38996 → 40931;
PostgreSQL и 8787 не менялись. Новый computed danger foreground/border в dark
равен `rgb(255,160,155)` (`--error`), normal foreground остаётся `rgb(227,234,227)`.

Повторный batched browser проход real-backend dialog в light/dark × RU/EN ×
1440×900/390×844 (8 состояний) подтвердил:

- modal целиком помещается в viewport, все три footer actions доступны до и
  после content scroll; page overflow отсутствует;
- контраст основного текста: light 13.20:1, dark 13.80:1;
- контраст destructive action: light 6.83:1, dark 8.66:1;
- screenshot dark/mobile просмотрен: белого фона нет, destructive action
  выделен error token, footer не обрезан.

Удаление baseline не подтверждалось, временные записи не создавались.
SHA-256 обоих CURRENT и трёх исходников сохранены:
`$TMPDIR/opencode/plan13acceptance/deletion-theme-hashes.txt`.
Это визуальная приёмка ограниченного dialog, не закрытие общего плана.

Вся CI `43498110078` исходного theme/scroll commit `93073a42` зелёная,
включая Windows и публикацию контейнера. Результат CI цветовой поправки
`ac58c532` / `43498726954` пока не подтверждён; эти результаты не смешиваются.

Финальная CI `43498726954` теперь также полностью зелёная, включая Windows
и публикацию контейнера. Визуальный theme/scroll/danger срез принят в указанных
границах; общий план и сквозная приёмка остаются открытыми.
