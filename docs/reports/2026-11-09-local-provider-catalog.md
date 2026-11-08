# Local provider: каталог, жанры и incoming matching

Срез продолжает пункты 7/8 плана main-interface-design-parity. Recovery не
расширяется: незавершённая работа над unsuitable target сохранена отдельно.

Добавлены provider-only CRUD артиста/recording/релиза и экран «Собственный каталог».
Поиск не создаёт сущности, создание явно, внешние записи доступны как ссылки и
не редактируются. Local key стабилен; genres сохраняют MBID и имена отдельно от
tags. Пустые genres не требуют доступного каталога. Релиз содержит один носитель,
упорядоченные ссылки на существующие recordings и необязательные title/duration
overrides. Правка сама не создаёт library entities и не публикует файлы.

Incoming Local matching выбирает provider release и явные file/track assignments;
preview и confirm используют существующие revision/digest/receipt механизмы.
Library materialization сохраняет реальные provider namespaces ссылок, credits
и duration. Прямой standalone create library удалён из API/UI вместе с заменой;
общие внутренние persistence helpers сохранены.

Миграция не вводит новые таблицы: seed Local provider, metadata существующих
positions и разрешение cross-provider target references. Composite owner FK
релиза сохранены. Rollback отказывает при cross-provider references.

Независимые reviews исправили DTO/list defaults, необязательные HTTP-поля,
owner FK, zero-based coordinates, credit display names, recording credits/duration,
stale detail responses, сохранение millisecond durations и uncertain-confirm fence.
Добавлены минимальные CRUD/matching transaction и UI-регрессии. `task generate`
выполнен; полный hook, browser-проверка и CI этого среза ещё ожидаются.
Общая aggregate/repeat приёмка пунктов 3/6/7 остаётся отдельным незавершённым scope.
