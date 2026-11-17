# Повторный preview: effective-output сравнение тегов

## Обнаружение

Полный browser Setup→publication прогон на `ac679830` создал корректный CURRENT.
Повторный aggregate preview без metadata-правок показал Update из-за отсутствующих
ARTISTS/ALBUMARTISTS/MBID tags против желаемых clearing directives `[]`.

Stage validator уже принимает такой физический результат как корректный.
Preview использовал иной критерий — key presence/DeepEqual — и ошибочно требовал
повторной записи. Это false-positive, не потеря данных первой публикации.

## Исправление и проверка

Desired non-nil empty slice считается выполненным при отсутствующем или пустом
CURRENT tag. Остальные сравнения точные; nil desired и `['']` не становятся
clearing intent. Projection intent и factual CURRENT не изменяются.
Исправлены comparison и existing pure regression; схема/API не менялись.
Независимое ревью блокеров не выявило; полный gate ещё не запускался.

`5d3336d1` прошёл обычный полный hook и отправлен; CI `43982699409` выполняется.
Fresh acceptance-процесс 18080 обновлён PID 28202 → 33720. Browser повторного
aggregate preview подтвердил `No tag differences`, но общий verdict всё ещё
Update. Значит tag false-positive устранён, а оставшийся unchanged predicate
требует отдельного разбора; полное устранение повторной публикации не заявляется.

Отдельный read-only разбор БД/ffprobe установил второй predicate: оба FLAC не
имеют stream bitrate (`nil`), хотя codec/container/lossless/channels/mono layout,
sample rate/bit depth и source authority совпадают. Обязательный non-nil bitrate
в `sourceRepublishCopyAudioMatches` даёт false Update. Исправляется optional exact
equality: nil/nil равно, асимметрия или разные известные значения не равны.
File-level bitrate не подставляется: он зависит от размера metadata/container.

Optional-bitrate правка и existing regression готовы, gofmt/diff whitespace
чисты. Независимое ревью подтвердило nil-safe exact comparison, сохранение
остальных guards и forced-mode eligibility. Полный gate ещё не запускался.

`5ed0b445` прошёл обычный полный hook, отправлен; CI `43983633399` выполняется.
Acceptance-only процесс обновлён PID 33720 → 36387. Browser реального backend
повторил aggregate preview на том же CURRENT без metadata-правок: Ordinary
показал Unchanged 1 / Update 0 / Blocked 0 и отсутствие tag differences. Forced
показал Update 1 и enabled confirmation; подтверждение не нажималось, режим
возвращён в Ordinary. SHA-256 нового CURRENT/его исходника и двух baseline
CURRENT/трёх прежних исходников сохранены; readback
`final-acceptance-20261117/hashes-after-unchanged-preview.txt`.

CI `43982699409` для первого tag-fix `5d3336d1` failed на Windows: persistence
package timeout 10m (600.157s), не прежний supervisor cleanup. Остальные jobs
прошли, container publication skipped. Лог `windows-5d3336d1-attempt1.log`
передан на разбор goroutine stacks; повтор без диагноза не выполняется.
Отдельная CI последующего bitrate-fix `5ed0b445` пока выполняется.

Независимый разбор timeout: восемь активных тестов возрастом 3–18s ожидали
fixture database creation через TEMPLATE либо четыре соединения admin pool;
application-lock wait не обнаружен. После этого evidence выполнен failed-only
rerun `43982699409`. Он не является fix или основанием считать CI зелёной до
завершения; повторяющийся provisioning stall потребует отдельного исследования.

Вся CI `43983633399` для `5ed0b445` завершилась успешно с первого attempt,
включая Windows и публикацию контейнера. Оба исправления повторного preview
подтверждены последней ревизией и реальным browser; результат старой
CI `43982699409` учитывается отдельно. Зелёный Windows job не устанавливает
причину прежнего supervisor/provisioning сбоя.

Failed-only attempt 2 старой CI `43982699409` (`5d3336d1`) также полностью
зелёный, включая Windows и контейнер. Оба исправляющих commit теперь имеют
собственный подтверждённый полный CI result. Таймауты и gates не менялись.
