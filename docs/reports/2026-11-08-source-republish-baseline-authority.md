# Source republish: baseline authority

Frozen v4 source authority описывает источник прежнего CURRENT, а association
и Audio — выбранный источник нового candidate. Эти identity могут различаться.
Убрано ошибочное требование их равенства; полнота baseline и его generation
по-прежнему обязательны.

Регрессия расширяет существующую pure fixture; новых БД нет. Независимое review
структурного исправления блокеров не выявило. Первый hook отклонил преждевременно
добавленный неиспользуемый raw-authority predicate. Он удалён вместе с его
проверками до появления production consumer; исключения lint не добавлялись.
Повторный полный gate/CI ожидаются.

Это groundwork, не активация: полный CURRENT witness predicate, typed admission,
result settlement и executor dispatch ещё не подключены. SupportsExecution v4
остаётся false; generic admission и public confirmation по-прежнему закрыты.
