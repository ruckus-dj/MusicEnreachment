# Редакторы metadata релиза, recording и позиции

Проверенные ревизии: `8bdb1207` (release editor), `2235177e` (scoped title editors).
Обе прошли полный обычный hook и всю CI 42561160523 / 42563854883 соответственно,
включая платформенную матрицу, Docker smoke и публикацию контейнера.

Release editor поддерживает title, sort_title, release_date, original_release_date,
release_country, label, catalog_number, release_primary_type и release_secondary_types.
Recording и позиция независимо редактируют title/sort_title. Используются прежние
GET/PATCH, metadata envelopes и ревизии; API/схема не расширялись. Жанры остаются
в отдельном picker, artist sort — в карточке артиста. Поля duration этим срезом
не добавлялись.

Общая ограниченная editor session сохраняет exact strings и порядок списков,
различает absent/value/explicit_empty, сохраняет draft при конфликте, позволяет
повторить неудачное чтение. Сохранение сразу инвалидирует publication previews,
не ожидая detail reread и не перемонтируя admission controllers. Review исправило
position route и обработку позднего PATCH после смены scope.

Browser-проверки использовали production components с API fixtures: RU/EN на
390/1440 без horizontal overflow; release PATCH сохранил пробелы title, порядок
label и clear/inherit. Recording PATCH ушёл в recording endpoint с ревизией 3;
position PATCH — во вложенный release/position endpoint с ревизией 5, sort_title
value и title explicit_empty. Valid fixture response завершил редактирование.
Fixtures возвращали исходные display values: проверена сериализация и UI transition,
не фактическое backend persistence или полный E2E. Общая приёмка пункта 6 остаётся
открытой; эти редакторы не доказывают полный Navidrome editor/writer/readback parity.
