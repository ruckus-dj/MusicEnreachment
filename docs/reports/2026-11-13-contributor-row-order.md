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
