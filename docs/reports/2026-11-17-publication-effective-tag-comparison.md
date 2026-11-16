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
