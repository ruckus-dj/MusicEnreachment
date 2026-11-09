# Общий release preview поверх позиционных операций

База `cc3cedda` прошла полный hook и CI 42539934336. CI предыдущего Local CRUD
среза `fbd020dc` также полностью прошла после rerun 42537649217, включая контейнер.

Release-level UI переиспользует уже смонтированные позиционные preview/admission
controllers: нет новых API, таблиц, bulk-транзакции, cancel policy или recovery.
Выбор позиций, обычный/forced режим для CURRENT, количества результатов и явное
подтверждение показаны вместе. First publication остаётся ordinary. Подтверждаются
только готовые create/update; unchanged и явно исключённые позиции не отправляют
POST. Неизвестные причины не объявляются отсутствующим файлом. Exact CURRENT tag
diff доступен и в retained preview. Позиционные pending/uncertain fences сохранены.

Review исправило несовпадающие Map/Record types, устаревшую готовность после смены
режима и отсутствие контроллера при оставшемся snapshot. Минимальный UI-сценарий
проверяет ready + unchanged, исключение, смену режима и admission fence.

Browser component harness использовал production AggregatePublicationPreview и
FirstPublicationPreview с origin-anchored API fixtures: RU/EN, 1440/390, отсутствие
overflow, GENRE в деталях, инвалидирование готовности сменой режима и один explicit
POST с frozen digest/epoch. Это не real-backend E2E; mixed-row поведение подтверждает
UI regression, а не этот однопозиционный browser fixture. Первый browser harness
не смонтировался из-за разных Vite React module identities; повторный использовал
те же versioned imports, что production module.

Полный hook этого дополнения ожидается. Более широкие explicit source selection,
changed-path republish и полный визуальный паритет пунктов 3/6 не объявляются
выполненными этим ограниченным aggregate-срезом.
