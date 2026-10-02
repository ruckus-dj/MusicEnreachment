# Черновик области: инвентарь и анализ источников

## Статус документа

Не готовый план и не следующая задача. Документ сохраняет границы будущей
области и уже утверждённые решения по файловой модели, кроссплатформенности и
I/O. После реализации Setup Manager область нужно повторно оценить и
декомпозировать на короткие самостоятельные продуктовые этапы; только затем для
ближайшего этапа отдельно проектируются API, UI, физическая SQL-схема, критерии
готовности и план проверки.

## Поставлено (обновлено 2026-10-02)

Первый короткий продуктовый этап этой области декомпозирован и поставлен:
`docs/plans/done/05-source-technical-analysis-and-inspector.md` (шаги 1–10).
Ниже перечислено только то, что реально поставлено; черновик **не** считается
выполненным целиком, а его остальные решения — будущая работа.

- Ручной `in_place` анализ одного source location со статусом `audio` по явному
  действию оператора: сохранённый минимальный технический результат
  (`media_variant`) с контейнером, всеми аудиодорожками, наблюдёнными тегами
  (имена в верхнем регистре, значения — массивами строк, без деления по `;`/`/`)
  и неизменённым `ffprobe` JSON, плюс версия политики, фактическая версия
  `ffprobe` и время анализа.
- Durable operation `analyze_source` в River с выделенной очередью и одним
  worker, stages `queued/probing/applying`, immutable snapshot, retry, startup
  recovery, SSE wake-up, REST detail и инспектор
  `#/sources/{root}/locations/{location}`.
- Source read-only: файл не открывается на запись; неудача сохраняет прежний
  результат; успешный scan после изменения size/mtime отвязывает результат;
  удаление корня удаляет только инвентарь этого корня, файлы остаются.
- Обработка только `in_place`: режим `staged`, work-directory, глобальная
  настройка SHA-256 и авто-планирование анализа не добавлялись. README и
  `docs/design/deployment.md` приведены в соответствие поставленному срезу.

Остаются будущими этапами и в этот срез не входят: fingerprint
`fpcalc`/Chromaprint, SHA-256 и точная дедупликация, `staged`-режим с общим
work-directory, автоматический анализ новых и изменённых файлов, группировка
входящих, MusicBrainz/AcoustID matching и confidence, quality/lossless policy,
CUE-сегменты, извлечение artwork, создание медиатеки, публикация, удаление
source после публикации и автоматический выбор processing mode.

> Дальше по документу описана **полная** будущая область. К поставленному срезу
> относится только перечисленное выше; всё прочее (SHA-256, `staged`, авто-анализ,
> группировка и т. д.) читать как план, а не как уже реализованное.

## Цель

Позволить оператору подключать read-only каталоги NAS, локальных и сетевых
дисков, надёжно обнаруживать новые и изменённые аудиофайлы и получать их
технический анализ без изменения source bytes. Обработка должна учитывать
разницу между медленным source storage и быстрым локальным scratch-диском.

## Зафиксированные решения

- `source_root` — настроенный входящий каталог, независимый от managed output.
- У root выбирается processing mode: `in_place` или `staged`.
  (Поставлено только `in_place`; выбора режима и `staged` в UI/БД ещё нет.)
- Staged mode использует общий настраиваемый work-directory. Source копируется
  туда одним последовательным чтением, инструменты работают с временной копией,
  после чего операция очищает staging.
- Глобальная настройка управляет вычислением SHA-256 только для новых и
  изменённых sources. Она не запускает ретроактивную обработку при переключении.
  (Будущий этап; настройки SHA-256 нет.)
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
  SHA-256. — Поставлен только минимальный срез: UUID, размер, версия политики,
  версия `ffprobe`, сырой `ffprobe` JSON, наблюдённые теги, время анализа и
  применившая результат operation. Chromaprint/quality/SHA-256 — будущая работа.
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
