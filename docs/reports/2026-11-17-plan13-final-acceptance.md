# План 13: итоговая приёмка

Проверяемая application-ревизия: `ac679830`, полный hook и CI `43712658879`
успешны. Этот отчёт фиксирует проверенные действия и ограничения, не новые
продуктовые требования. Общий план пока открыт.

## Защита данных

Контрольный стенд 8787 не используется. Приёмка выполняется на 18080;
существующие два CURRENT и три исходника сохраняются. Для полного Setup-маршрута
готовится отдельная временная БД и отдельные каталоги, без reset исходного стенда.

## Выполнено

- Реальный browser: из общей навигации открыта Library, затем карточка
  `Plan13 Acceptance Artist`. В English сводка, availability/publication counts,
  отсутствие даты и Show positions отображаются по-английски. Старое замечание
  о русских подписях этой карточки больше не воспроизводится на данной ревизии.

## Ещё не проверено в этом проходе

- Полный Setup → source → analysis → incoming → matching → publication через UI.
- Итоговая сверка с React-прототипом, RU/EN, обеими темами и viewport/keyboard.
- Populated evidence/overrides и агрегированный preview с реальным backend.

## Текущий проход Setup

В контейнере создана отдельная БД `plan13acceptance_final_20261117`; исходная
БД и bootstrap сохранены без изменений. Только acceptance-процесс 18080
переключён PID 2557 → 21997. Отдельные каталоги и копия собственного третьего
FLAC подготовлены в `plan13acceptance/final-acceptance-20261117`, source SHA-256
зафиксирован до запуска. Это provisioning входных файлов, не API-обход UI.

Свежий browser context штатно открыл Setup. Через directory picker выбраны
новые tools/output; через UI скачаны и проверены FFmpeg 9.0.2 и fpcalc 1.6.2,
обе версии активны. Выбран Source format; удаление исходников оставлено выключенным.
Запущена проверка публичного MusicBrainz. Остальные шаги ещё не завершены.

## Найденный blocker RU/EN

Активный `LibraryReleaseScreen` и `newReleaseCoverEditor` не подключены к
локализации и содержат собственные русские labels/status/fallbacks при English.
Соседние дочерние редакторы уже локализованы. Исправление выполняется отдельно;
raw server diagnostics, музыкальные данные и authority/state guards сохраняются.

## Сквозной browser маршрут `ac679830`

Setup завершён кнопкой Finish setup; без root показано Add source. Через это
действие открыт Sources и зарегистрирован новый in-place root. После регистрации
UI показал queued scan, затем успешный inventory с одним audio-файлом. Отдельная
кнопка Scan не нажималась: регистрацию и последующий scan/analysis нельзя описывать
как два явных пользовательских запуска. Incoming показал 1/1 ready и saved data.

Через Own catalog выполнены поиски и созданы Local artist, recording и release
`Final Acceptance … 20261117`, с одной позицией. Через Incoming → Local matching
выбраны релиз/позиция, нажаты Preview assignments и Confirm matching. UI показал
manual revision 1 и сохранённую связь; Library показала 0 published.

Через Library → artist → Show positions → позиция открыт экран релиза. Нажаты
Preview publication и Publish. Без ручного refresh UI показал CURRENT и завершение
operation; generation `e29d60f3-07a5-4043-a2a5-8394501451fa`. Все application mutations
выполнены browser-контролами, без API mutation или ручных обходных URL.

Физический FLAC найден только в новом output; ffprobe подтвердил FLAC и теги
ALBUM/ARTIST/TITLE новых Local записей. Readback: `current-probe.json`, `current.sha256`
в `final-acceptance-20261117`. Копия исходника и оба baseline CURRENT/три прежних
исходника сохранили SHA-256. Смешанный RU/EN на экране релиза воспроизведён браузером.
Полный маршрут функционально пройден, но визуальная/RU/EN приёмка не завершена.

## Исправление release RU/EN

Локализованы собственные подписи release screen/cover editor; сохранённые
feedback/errors переведены на semantic descriptors с форматированием при render.
Даты используют текущую locale. Server detail, safe_error, неизвестные Error.message,
пути, музыкальные значения и raw evidence сохранены. Existing tests переведены на
явную locale, добавлен locale-flip lyrics draft/terminal-notice regression без
дополнительных reads. Независимое ревью выявило один устаревший RU selector в EN
тесте; он исправлен. Biome formatting/import assists и Impeccable detector прошли.
Полный gate и browser-проверка нового кода ещё не выполнялись.
