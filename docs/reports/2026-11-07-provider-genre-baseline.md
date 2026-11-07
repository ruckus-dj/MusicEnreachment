# Provider genre baseline: readback

Базовая ревизия: `4f9e1f0f21c470988fda1dc64d582994dbb3b8cf`.
Предыдущий genre override срез прошёл полный локальный hook и
[всю CI-матрицу](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42313798554).

Этот срез добавляет отдельное read-only представление принятых MusicBrainz
genres в metadata GET artist/release/recording. Оно не использует файловый
fallback, не заполняет локальный draft и не участвует в PATCH. Envelope,
revision, текущий source, binding и field evidence читаются одним
repeatable-read snapshot. CURRENT и исходные файлы не изменяются.

В прототипе добавлен управляемый presentation input без выдуманного
provider provenance в canonical fixtures; граница описана в `PARITY.md`.

## Проверки

`task generate` прошёл. Независимое ревью выявило смешение отсутствующего
evidence и JSON `null`; reader исправлен. В digest добавлена configuration
identity, поскольку UUID source может сохраняться между её поколениями.
Frontend использует generated DTO вместо отдельного типа с неверным
типом authority.

Повторное UI-ревью обнаружило race старого GET после успешного PATCH и
зависимость reread от наличия callback родителя. Исправление инвалидирует
предшествующие чтения при commit ответа и запускает независимый guarded
baseline reread; регрессии используют управляемые отложенные ответы.

Полный gate, browser acceptance и CI этого среза пока не завершены.
Первый обычный hook прошёл lint и PostgreSQL persistence suite, но обнаружил
три новых API/service fixture, отправлявших `comment` в genre-only artist scope.
Запросы исправлены на допустимый `genre: explicit_empty`; диагностический
прогон трёх тестов прошёл. Production validation не ослаблялась.
Пункт 7 плана 13 целиком не закрывается этим readback-срезом.
