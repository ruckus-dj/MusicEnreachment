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
