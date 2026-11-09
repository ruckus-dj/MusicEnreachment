# Artist-owned sort и явные tag overrides

Ограниченный срез пункта 4 исправляет существующие требования metadata-контракта,
не меняя provider propagation, identity, recovery или обязательные business-поля.

Artist `sort_name` сохраняется как самостоятельный необязательный desired field
через существующий metadata envelope; редактор расположен в карточке артиста.
Отсутствие sort_name не заполняется display name в БД. Publication projection
вычисляет ordered sort groups из credits; fallback и disambiguation относятся
только к файловым тегам. Повтор одного artist identity не считается коллизией.
Неустранимая коллизия подавляет только соответствующую MBID/sort группу с notice
и явным clearing, не оставляя устаревшие значения в output.

Явные title/artist_credit/artists overrides разрешены в существующем API без
изменения identity entities. Projector по-прежнему проверяет business baseline
до overrides, но не отвергает согласованный пустой итоговый тег. Release title
для baseline отделён от его tag override. Artist revisions и provider evidence
участвуют в прежнем preview/frozen-input freshness механизме.

Независимое review исправило ложные same-identity коллизии, недостающую
disambiguation в first/retained context и ошибочное ослабление baseline guard.
Добавлены минимальные проверки к существующим projector/service/DB fixtures и
artist-screen test; новая тестовая матрица не вводилась. `task generate` выполнен.
Полный hook и CI среза ещё ожидаются; пункт 4 целиком пока не закрывается.
