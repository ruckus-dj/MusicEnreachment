# CURRENT: сравнение исходного аудио

Реализовано согласованное правило AUDIO: тот же identity variant совпадает
независимо от расположения; для разных variants совпадающий исходный SHA-256
означает то же аудио, отличающийся — замену. Недостаточные сведения не доказывают
равенства. Хеш опубликованного payload и probe variant для этого не используются.

Repeatable-read context сохраняет canonical source SHA выбранного identity variant
и CURRENT authority с отдельными missing/stale/present состояниями. Новое evidence
входит только в CURRENT digest; initial admission digest не меняется. UI RU/EN
различает совпадение, замену и недоступное сравнение. CURRENT остаётся read-only;
typed v4 admission/result/executor ещё не активированы. Пункт 3 плана не закрыт.

Независимое review блокеров не выявило. Минимальная SQL-регрессия расширяет
существующую authority fixture без новой БД; pure comparison проверяет identity,
хеши и stale evidence. Полный hook и CI нового среза ещё ожидаются.

Базовый `fcbab459` прошёл полный локальный hook. CI 42469761075 сначала
остановилась на скачивании Task с HTTP 504, а повторный запуск — на загрузке
PostgreSQL supervisor/checksum с HTTP 504. Compose smoke успешен; полной зелёной
матрицы и публикации контейнера пока нет. Gates не изменялись.
