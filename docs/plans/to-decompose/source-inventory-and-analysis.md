# Черновик области: инвентарь и анализ источников

## Статус документа

Не готовый план и не следующая задача. Документ сохраняет границы будущей
области и уже утверждённые решения по файловой модели, кроссплатформенности и
I/O. После реализации Setup Manager область нужно повторно оценить и
декомпозировать на короткие самостоятельные продуктовые этапы; только затем для
ближайшего этапа отдельно проектируются API, UI, физическая SQL-схема, критерии
готовности и план проверки.

## Цель

Позволить оператору подключать read-only каталоги NAS, локальных и сетевых
дисков, надёжно обнаруживать новые и изменённые аудиофайлы и получать их
технический анализ без изменения source bytes. Обработка должна учитывать
разницу между медленным source storage и быстрым локальным scratch-диском.

## Зафиксированные решения

- `source_root` — настроенный входящий каталог, независимый от managed output.
- У root выбирается processing mode: `in_place` или `staged`.
- Staged mode использует общий настраиваемый work-directory. Source копируется
  туда одним последовательным чтением, инструменты работают с временной копией,
  после чего операция очищает staging.
- Глобальная настройка управляет вычислением SHA-256 только для новых и
  изменённых sources. Она не запускает ретроактивную обработку при переключении.
- При включённой настройке одинаковый SHA-256 переиспользует один
  `media_variant`, но каждый путь остаётся отдельным `source_location`.
- Variant с SHA-256 хранится без locations как кэш; orphan variant без digest
  удаляется после завершения удерживающих его операций.
- `device`/`inode` не сохраняются и не используются. Source paths хранятся точно
  как возвращены файловой системой.
- Scan фиксирует reconciliation только после полностью успешного обхода.
  Неувиденные locations удаляются; ошибка или недоступность root не меняет
  предыдущий inventory.
- Directory symlinks не обходятся, выход за root запрещён. Hardlinks специально
  не распознаются.
- `fpcalc` явно запускается с ограничением 120 секунд. `ffprobe` возвращает
  технические данные и format/stream tags; embedded artwork извлекается отдельно
  только при необходимости.
- Один `media_variant` может использоваться несколькими tracks и recordings;
  source tags не определяют metadata конкретной managed-публикации.

## Модель данных

Этап добавляет физические таблицы `source_root`, `source_location` и
`media_variant` на основе утверждённой концептуальной модели.

- Root хранит server path, display name, processing mode, enabled state, scan
  generation, текущее состояние доступности и время успешного scan.
- Location хранит root, точный relative path, size, mtime, last seen generation
  и nullable variant во время незавершённого анализа. Уникальность задаётся парой
  `(source_root_id, relative_path)` без case folding в PostgreSQL.
- Variant хранит normalized tags, `ffprobe` snapshot и version, Chromaprint и его
  параметры/version, quality policy version, технические проблемы и nullable
  SHA-256.
- Ненулевой SHA-256 уникален. Ограничения гарантируют корректную пару digest и
  состояние завершённого анализа.
- Work-directory и глобальный SHA-256 mode хранятся typed settings; EAV storage
  не раскрывается через API.

## Поток сканирования

1. Worker проверяет доступность root и начинает новое scan generation.
2. Обход сохраняет точные relative paths и metadata snapshot `(size, mtime)`.
3. Новые и изменённые locations получают analysis operations; неизменившиеся не
   перечитываются.
4. Только успешный полный обход фиксирует generation и удаляет неувиденные
   locations. Любая traversal/permission/cancellation ошибка оставляет предыдущий
   inventory без удаления.
5. Удаление последнего location запускает cleanup orphan variant согласно
   наличию SHA-256 и активных ссылок операций.

## Поток анализа

### In-place

Инструменты читают source напрямую. При включённом SHA-256 сначала выполняется
полное последовательное hashing-чтение; точное совпадение с актуальным variant
позволяет пропустить повторный анализ.

### Staged

1. До чтения сохраняется stat source.
2. Source копируется в `<work-directory>/<operation-id>/` с ограниченными
   правами. При включённом SHA-256 digest считается в том же copy-потоке.
3. После copy выполняются fsync и повторный stat. Изменившийся source отменяет
   результат и планируется повторно.
4. При найденном digest существующий актуальный variant переиспользуется. Если
   версии tools или analysis policy устарели, reanalysis выполняется сразу на
   уже созданной временной копии.
5. `ffprobe` и `fpcalc` работают с staged-файлом; успешный результат фиксируется
   транзакционно, staging удаляется.
6. Startup cleanup удаляет безопасно распознанные брошенные operation directories
   после сверки с текущими operation snapshots.

## API и UI

- CRUD source roots и ручной запуск scan.
- Выбор `in_place`/`staged` с пояснением I/O trade-offs.
- Настройка и проверка writable work-directory, свободного места и server path.
- Глобальный toggle вычисления SHA-256 для последующих анализов с явным текстом,
  что существующие variants ретроактивно не обрабатываются.
- Состояние root, текущий scan/analysis progress и безопасные ошибки через REST и
  operation-specific SSE по уже утверждённой модели operations.
- Просмотр locations, технического анализа, точных duplicate locations и
  повторный анализ выбранного файла.

## Безопасность и эксплуатация

- Source roots остаются read-only; никакая scan/analysis operation не изменяет
  source bytes.
- Work-directory не может неявно использовать container overlay или каталог
  бинарника; Docker deployment предоставляет явный bind mount/volume.
- Перед staging проверяются размер source, доступное место и configured limits.
- Concurrency ограничивается, чтобы параллельные jobs не превращали HDD workload
  в random seeks и не заполняли scratch storage.
- Реальный путь staged/source-файла не принимается из HTTP как произвольная
  команда; service разрешает только зарегистрированные root/location IDs.

## Критерии готовности

1. Новый и изменённый файл анализируется, неизменившийся не перечитывается.
2. Недоступный root и оборванный scan не удаляют прежние locations.
3. Успешный scan удаляет locations отсутствующих paths.
4. Staged mode читает source одним copy-проходом и всегда очищает временные файлы
   после успеха; recovery безопасно очищает остатки после сбоя.
5. SHA-256 toggle влияет только на последующие анализы. Совпадение digest
   переиспользует variant из любого root и не повторяет актуальный анализ.
6. Variant с digest переживает удаление всех locations; variant без digest
   очищается после исчезновения последней location.
7. Изменение source во время чтения не создаёт готовый variant.
8. Пути с разным регистром на case-sensitive source остаются разными; приложение
   не применяет безусловный lowercase/Unicode normalization.
9. Symlink не позволяет выйти за root, directory symlink не вызывает цикл.
10. Один variant можно подтвердить и выбрать для tracks разных recordings и
    релизов, сохраняя разные публикационные metadata.

## План проверки

- Fixture roots для Linux/macOS/Windows path forms и case-sensitive test volume.
- Успешный, прерванный, permission-failed и unavailable-root scans.
- Добавление, изменение, удаление и case-only rename файла.
- In-place и staged analysis с включённым и выключенным SHA-256.
- Два одинаковых файла в одном и разных roots; одинаковое аудио с разными tags.
- Изменение source во время copy, нехватка места, crash и startup cleanup.
- Актуальный и устаревший cached variant без locations.
- Symlink traversal и hardlink fixtures на поддерживающих их платформах.
- Ограничение concurrency и заполнения work-directory.
- Migration rollback, REST/SSE tests и общий `task verify`.

## Вне области этапа

- MusicBrainz/AcoustID matching и формула confidence.
- Создание collection releases/tracks из подтверждённого matching.
- Remux, запись итоговых tags и managed publication.
- Удаление source после публикации.
- Автоматическое определение оптимального processing mode по топологии дисков.
