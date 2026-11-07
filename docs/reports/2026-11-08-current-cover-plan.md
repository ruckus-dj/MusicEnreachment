# CURRENT preview: точный план обложки

База — `525a1c50`; срез остаётся предпосылкой republish, не его приёмкой.

Repeatable-read context сохраняет selected original в той же транзакции,
что и summary; pending-изображение не выбирается. После завершения чтения
используется тот же production PNG converter, что и при публикации.
Оригинал и производный файл имеют отдельные SHA-256/MIME/size. В CURRENT
сравниваются факты embedded cover с planned derivative, не original hash.
Authority, исходные/производные факты и состояние derivation входят в digest.
Raw bytes не выходят в API; first-admission digest не меняется.

Absent остаётся отдельным состоянием. Optional conversion failure сохраняет
выбранный original и делает сравнение unavailable, а не «обложки нет».
Cancellation не выдаётся за optional failure. Нет acquisition, смены authority,
новых таблиц или write producer. CURRENT confirmation по-прежнему запрещено:
lyrics и typed replacement execution ещё не готовы.

Независимое review выявило неполную native fixture (missing size) и неверное
presence-presuming сообщение для absent/absent; оба исправлены до gate.
Регрессия расширяет существующую native fixture без новой БД.

Браузерная проверка — непосредственно production preview component с API fixture,
не полный release shell/backend E2E: RU/EN, 1440×900 и 390×900, ready и optional
failure, отдельные original/derivative facts, сохранённый original при ошибке,
отсутствие confirm controls и горизонтального overflow.

`7135c8ed` прошёл обычный полный commit hook и всю
[CI 42420678807](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42420678807),
включая матрицу платформ, Compose smoke и публикацию контейнера.
