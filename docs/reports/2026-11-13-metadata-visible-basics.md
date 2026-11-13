# Постоянно видимые основные metadata

Следующий ограниченный срез плана 13 после `1a630839`.
Карточки сохраняют read/edit геометрию и общую draft/revision логику.

## Scope

Основные поля выводятся один раз в нераскрываемом разделе, как в самостоятельном
эталоне `metadata/editorLayout.ts` / `MetadataEditor.tsx`:

- release: title, release date, country, label, catalog number и оба типа релиза;
- recording: title, content advisory, ISRC;
- position: track number и disc number.

Владелец подтвердил совместное постоянно видимое отображение основного и
дополнительных типов релиза; решение внесено в тематический раздел
`docs/design/decisions.md`. Поля сохраняют отдельные значения и семантику.
Остальные поля остаются в прежних тематических disclosures, без дублирования
и автоматического раскрытия всех групп при Edit. Artist-credit controls,
genres, ownership, API и сохранение не перерабатываются.

## Статус

Реализация завершена; прежний field renderer переиспользуется для обоих видов
групп. Добавлена минимальная frontend проверка постоянно видимых release basics
и сохранения закрытого additional group при Edit; прежние MBID/reorder assertions
сохранены. Biome и механический design detector прошли (findings `[]`).
Независимое ревью не нашло блокеров. Runtime-проверки текущего дерева ещё не проводились;
CI предыдущего `1a630839` не является проверкой этого среза.
План 13 остаётся открытым.
