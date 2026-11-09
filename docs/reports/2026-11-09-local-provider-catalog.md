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

Срез `fbd020dc` прошёл полный обычный hook. Browser component checks с fixtures
подтвердили RU/EN, отсутствие overflow на 1440/390 и создание с `genres: []` при
недоступном каталоге; это не real-backend E2E. CI 42537649217 остановилась только
на Windows testpostgres supervisor shutdown; failed jobs перезапущены без
ослабления gate. Нельзя считать эту матрицу завершённой до результата rerun.

Итоговая сверка жанров выявила недостающий Local read projection для publication:
добавлен reader через существующие provider bindings без копирования в overrides,
изменения MusicBrainz-only display baseline или обращения к каталогу жанров.
Файловый fallback и preview freshness используют существующий механизм.
Сохранённые имена выбранных жанров не заменяются при добавлении жанра после
обновления каталога. Incoming Local editor переведён на RU/EN. Проверка реального
DB reader добавлена в тот же matching fixture; независимое review блокеров не нашло.
Полный hook дополнения ещё ожидается.

Дополнение `cc3cedda` прошло hook и полную CI 42539934336. Финальные Local UI
исправления `7da9776a` также прошли hook и всю CI 42547720179, включая контейнер.
После поиска с результатами доступно явное создание при отсутствии подходящего
кандидата; позиции сохраняют названия и duration существующих recordings независимо
от текущего поиска; сохранение блокирует смену контекста, ошибка сохраняет draft.
Первое открытие genre picker разрешено до чтения его статуса. Один UI regression
проверяет настоящий picker: initial status read → search → MBID selection → save.
Вместе с PostgreSQL provider matching и CRUD это завершает критерии пункта 8;
browser component fixtures не объявляются real-backend E2E.
