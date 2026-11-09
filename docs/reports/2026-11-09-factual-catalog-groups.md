# Каталог: отсутствующие группы и фактические счётчики

Срез поверх `5786cfa9`; описание относится к изменениям этого коммита.

- Страница релизов объединяет конкретные записи медиатеки и отсутствующие
  MusicBrainz release groups. Фильтры, сортировка и total применяются до paging.
- Группа представлена, если в медиатеке есть хотя бы один её релиз, независимо
  от доступности аудио. Все конкретные записи считаются отдельно. Artist total
  добавляет по одной известной отсутствующей группе, не все её editions.
- Provider-проекция ограничена accepted evidence текущего настроенного source.
  Выбор edition и просмотр известных треков — read-only; автоматического выбора,
  provider fetch и создания library entities нет. Неизвестные totals остаются `—`.
- Расширены существующий PostgreSQL catalog-сценарий и один UI-сценарий;
  существующие API/service fixtures адаптированы к tagged release items.
  Независимое ревью исправило local-only edition fallback и поиск/artist membership
  группы по всем её известным editions, а не только display representative.

Браузерная проверка production UI использовала API fixtures в восьми свежих
contexts: RU/EN, light/dark, 1440/390 px. До явного выбора запросов track-list нет,
picker не выбирает единственное издание автоматически, после выбора выполняется
один GET, mutations отсутствуют; horizontal overflow страницы и JS errors нет.
В прототипе проверены неизменность artist total при смене availability filter и
layout 1440/390 px. Screenshot-артефакты не создавались.

Полный локальный gate — `task verify` через обычный commit hook. Эта приёмка не
заменяет общий real-backend/upstream E2E и полный визуальный паритет пункта 11.
