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

Проверенная локально ревизия — `2c81f970f786eba74e0985159ec291cd84c19ae3`:
обычный commit-hook успешно выполнил полный `task verify`, ревизия отправлена
в `go-development`. [CI 42316624690](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42316624690)
завершился успешно: verify, Docker Compose smoke, Linux arm64, Windows x64,
macOS x64/arm64 и публикация контейнера.
Первый обычный hook прошёл lint и PostgreSQL persistence suite, но обнаружил
три новых API/service fixture, отправлявших `comment` в genre-only artist scope.
Запросы исправлены на допустимый `genre: explicit_empty`; диагностический
прогон трёх тестов прошёл. Production validation не ослаблялась.
Следующий hook обнаружил ошибочную assertion отсутствующего alert в UI-тесте;
она заменена на проверку отсутствия элемента, диагностический файл прошёл 17/17.
Последующий полный hook прошёл.

Browser smoke на контролируемых API подтвердил отдельные baseline/override,
PATCH только desired genre и сохранение baseline при PATCH-ответе без него.
Матрица 1440/390 × light/dark × RU/EN прошла последовательным прогоном, без
overflow и alert, но использовала одну вкладку. Повторный прогон populated
baseline прошёл во всех восьми отдельных свежих contexts с независимым API state:
без page errors, overflow и alert; baseline сохранился после PATCH без baseline,
Edit/Save rectangles совпали (120×30 desktop, 120×40 mobile). Проверки пустого
и отсутствующего baseline из первого прогона остаются same-tab evidence, не
изолированной browser-проверкой этих состояний. Это не upstream E2E.
Пункт 7 плана 13 целиком не закрывается этим readback-срезом.
