# Interrupted first-audio recovery: ограниченный срез

База — `0e2f2203`, прошедшая обычный полный hook и всю
[CI 42411140017](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42411140017),
включая контейнер. Пункт 3 плана 13 остаётся открытым.

Новый путь отличает takeover executing claim другого boot от вернувшегося
quiescent delivery. Обычный retry известной ошибки полностью готовит candidate,
а прерванный ordinary first-audio delivery сначала проверяет frozen target.

Target открывается no-follow; private inspection snapshot читается из этого
handle, не из source. Snapshot не преобразуется и не устанавливается в output.
Сравниваются tags и технические параметры; hash фиксирует фактический CURRENT,
а не доказывает происхождение из source или равенство прежнему candidate.
Перед registration проверяются исходный handle, именованный target, ancestors
и актуальная delivery authority. Фактические lyrics сохраняются без выдуманной
истории получения (`InitialLyrics` отсутствует).

Граница среза: только version-1 ordinary first audio без CURRENT и специальных
rewrite/upgrade intents, с явным absent cover receipt. Requested/unknown cover
пока использует полную подготовку. Missing target и доказанное несовпадение
ведут к полной подготовке; operational/native/cancellation errors не маскируются.
Metadata adapter не различает malformed content и operational read failure:
такие ошибки пока безопасно завершают delivery, а не разрешают adoption.
Forced republish и специализированные recovery flows ещё не реализованы.

Добавлена одна DB-регрессия с prior-boot executing claim, отсутствующим source
и фактическими lyrics target; существующая регрессия known-error retry остаётся.
Новый срез ожидает полный gate и CI; это ещё не приёмка.
