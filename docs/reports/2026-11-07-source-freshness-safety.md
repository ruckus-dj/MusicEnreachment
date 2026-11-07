# Свежесть source: копирование и полный обход

Базовая ревизия — `91d8e28148f70cd3de5e3cc1f799440096c93c4c`,
прошедшая полный hook и всю CI-матрицу.

Владелец уточнил правило в текущем разделе
[источников](../design/decisions.md#источники-инвентарь-и-анализ): изменившийся
файл не является прежним source и не должен наследовать его identity.

## Первый защитный срез

- Publication source копируется в приватный файл после разрешения tools.
  Открытый no-follow source handle проверяется по размеру и microsecond mtime
  до и после копирования; дальнейшая подготовка получает приватную копию.
  CURRENT rewrite copying остаётся отдельным путём. Приватное имя ограничено,
  не зависит от длины исходного basename.
- Неполный или нечитаемый обход сохраняет опубликованные inventory locations,
  generation и associations. Полный успешный обход очищает связи действительно
  исчезнувших variants после последней location; shared variants сохраняются.
- Независимое ревью исправило неверное положение mutation assertion в тесте
  tool-version barrier и переполнение basename приватной копии.

Добавлены unit и PostgreSQL-регрессии. Первый полный hook подтвердил новые
publication-copy проверки (успешный baseline, независимые size/mtime mutations,
ограниченный basename), но остановился на трёх старых scan-сценариях с ожиданием
частичного pruning при неполном обходе. Fixtures исправлены на полный/неполный
сценарий согласно контракту; диагностический integration-прогон jobs/persistence
прошёл. Итоговый обычный commit-hook с полным `task verify` прошёл для
`9b5725dec44ead2f9617a2243a19735a1dfeb740`. Полностью завершён
[CI 42357464505](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42357464505):
verify, Docker Compose smoke, Linux arm64, macOS x64/arm64, Windows x64 и
публикация контейнера — success. Это подтверждает первый защитный срез, но не
устраняет перечисленные ниже ограничения.

## Незавершённая защита

Отзыв identity при обнаружении изменения во время активного анализа, при
publication/cleanup holds и до завершения обхода требует отдельного durable
invalidation fence. Этот срез не выдаётся за устранение этих разрывов.
Удержания, immutable результаты и recovery evidence нельзя удалить ради
разрыва live location→variant связи. Реализация этого fence продолжается отдельно.
