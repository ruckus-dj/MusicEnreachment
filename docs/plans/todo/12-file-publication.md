# План 12 — файловая публикация

**Текущее состояние на 2026-10-18: подготовительная реализация.** Продуктовый
контракт согласован в A.1–A.16. Независимое review
[технического дизайна](12-file-publication-technical-design.md) разрешило
срезы identity/read model, metadata/primary artist и unified reader/native
removal. Первоначальное описание DRAFT ниже сохранено как контекст подготовки;
оно не отменяет поздние решения. Exposure/tool mutation допускаются только
после соответствующих технических гейтов и проверок; завершение плана и
runtime acceptance ещё не достигнуты.

### Текущий прогресс реализации — 2026-10-18

Добавлен durable manual-lyrics receipt storage/read-model: applicability
ограничена active epoch, selected desired revision и published generation;
fulfilled требует строгих actual metadata/assets, warning остаётся diagnostic,
а не самостоятельным pending-флагом. Production receipt writer и canonical
commit integration ещё отсутствуют. PostgreSQL constraints, read projection и
populated rollback refusal проверены; полный локальный `task verify` прошёл,
native CI ещё не проверен. Native rebuild test теперь наблюдает успешные
unlink/exclusive-create вместо предположения о lifetime-уникальности file ID.

Реализованы isolated optional-lyrics helper и native candidate rebuild: исходный
live reservation удерживается через write/readback/rebuild; rebuild journal
предшествует identity-checked unlink и exclusive byte-copy из immutable input.
Opaque writer errors остаются fatal; mismatch допускает fallback лишь с
независимой required audio/asset validation и restoration authority. Тест FLAC
проверяет encoded-audio digest, mandatory tags до восстановления cover и exact
lyrics absence после fallback. Полный локальный `task verify` прошёл; native CI
ещё не проверен. V3 execution остаётся
запрещённым; durable warning/retry state и runtime integration ещё отсутствуют.

Реализован unregistered V3 manual-lyrics admission: locked desired revision,
release membership и confirmed current generation ограждают manual LRC/removal;
mandatory tags берутся только из current publication. Generic V3 admission
запрещён, существующие execution paths явно отвергают V3 до tools/filesystem
mutation. Это не executable Apply: optional-writer isolation, durable warning/
retry status, route/UI и runtime wiring ещё отсутствуют. Полный локальный
`task verify` прошёл; native CI этого среза ещё не проверен.

В текущем срезе связаны staging → sealed replacement → commit →
owned backup cleanup → terminal settlement в исходном live execution scope.
Rewrite settlement не требует original source hold; отсутствие источников не
снимает проверки backup/current/namespace. Native Unix runner и PostgreSQL
settlement проверены focused-тестами; полный локальный `task verify` прошёл,
native CI ещё не проверен. Windows
replacement по-прежнему fail-closed до подтверждения directory durability;
production worker не зарегистрирован, cross-boot recovery не реализован.

Реализован атомарный replacement commit: проверяется полный canonical prefix,
actual namespace, expected current projection, staged observation и отдельная
identity приватного backup. Commit сохраняет source-free current, потребляет
frozen inputs через SQL NULL и удерживает claims/holds; replay требует exact
current witness. Полный локальный `task verify` прошёл; native CI ещё не проверен.
Replacement cleanup,
settlement и связь exposure → commit в runtime пока не завершены.

Следующий срез — guarded same-path rewrite exposure: проверенный
private backup остаётся доступным до единственного native atomic replacement;
ordered replacement journal связывает actual namespace, current generation и
validated candidate. Unix использует file/directory sync barriers; Windows
exposure пока fail-closed из-за неподтверждённой directory durability capability.
Replacement commit, cleanup/settlement и workflow wiring ещё отсутствуют;
после exposure scope остаётся fenced. Полный локальный `task verify` среза прошёл;
native CI для него ещё не проверен.

Реализован следующий срез: завершение first-absent публикации
в исходном live execution scope — sealed synchronous reservation, anchored
удаление только собственного private input и пустого attempt directory,
cleanup journal и атомарный terminal settlement. Полный локальный `task verify`
прошёл; native CI ещё не проверен. Это не recovery takeover, optional source
cleanup или разрешение регистрации worker. Сквозной тест real preparer → real
persistence и полное покрытие commit/cleanup panic boundaries ещё отсутствуют.

Дополнение (startup barrier): `app.Run` проверяет read-only
repeatable-read snapshot публикаций до platform initialization, output-reset
recovery, consumers и HTTP. Retained execution claims независимо от их состояния
и несогласованные attempts блокируют запуск; never-started queued attempts
допускаются только с проверенными River delivery и namespace reservations.
Это не interprocess exclusion, recovery execution или settlement и не разрешение
на регистрацию publication worker. Полный локальный `task verify` текущего
среза прошёл. CI `edb5fc9` (run `39112796255`) прошёл на Linux amd64/arm64,
macOS x64 и Windows x64, но завершился ошибкой macOS arm64 в независимом
MusicBrainz cooldown concurrency test; container publish пропущен. Исправление
тестового измерения через `testing/synctest` ещё проверяется.

После исторической отметки ниже добавлен узкий filesystem publication slice:

- При первом отсутствии target проверенный staged file публикуется через
  no-replace; ownership journal устанавливается после публикации. Первый
  publication commit выполняется атомарно и сохраняет `NULL` source provenance
  по frozen snapshot.
- Execution scope закрывается с quiescence barrier; удерживаемые input
  descriptors освобождаются только после установленного завершения использующих
  их команд. Неизвестная quiescence не трактуется как успех.
- Эти изменения вошли в `c32e10b`, `cd5eec1` и `0ea97ea`. Для каждого из этих
  коммитов `task verify` и локальные hooks действительно прошли.
- V2 typed evidence подтверждает нормализацию первого cover и ограничения
  evidence: topology covers пока отвергаются как non-audio; сохранённый optional
  sample rate не является доказательством bit-perfect результата.

Это не завершает реализацию публикации. Recovery execution, private-artifact
cleanup и settlement, replacement/upgrade/rewrite exposure, runtime interprocess
startup exclusion, Linux/macOS containment и Windows native CI остаются
непроверенными (Windows CI для этой серии ещё не запушен). Acquisition assets,
UI и automatic triggers также не образуют завершённый end-to-end путь; план 12
остаётся незавершённым. Это progress evidence, а не изменение продуктовых
решений и не отметка завершения P05/T07 или иных checklist items.

### Историческая отметка прогресса — 2026-10-17 (после `1c3bb62`)

Реализованы подготовительные foundation-срезы: атомарный admission публикации в
namespace с durable claims и River jobs, содержащими только operation ID;
durable source holds и защита мутаций источника согласно A.17; typed execution
inputs с probe-verified selected facts; подготовка переписывает только
ownership-verified artifact, не оригинальный source; проекция согласованных
metadata в tags и staged writer/preparers. Добавлены exclusive stage journal,
supervised execution scopes с удержанием до доказанного quiescence,
descriptor-bound probing, разрешение инструментов из frozen managed tools и
ограничения demuxer/protocol для publication inputs. Это foundation, а не
завершённые exposure/recovery или end-to-end publication slices.

Windows job backend пока **UNREGISTERED**; проверка membership процесса в job —
не arbitrary broker containment и не полный sandbox. Native Windows поведение
не проверено, CI для этой серии ещё не запушен. Также ещё не валидированы
sample-rate и сохранение аудиобайтов как фактические характеристики результата.
После settle не сохраняется история исходного аудиоисточника; это не означает,
что сохранение аудиобайтов текущей публикации уже доказано.

Остаются runtime interprocess exclusion, доказательство поведения при потере БД
на старте, Linux/macOS containment, exposure journal/recovery/settlement,
wiring cover/lyrics assets, automatic reconciliation и cleanup, а также UI/API
end-to-end, Navidrome/tag mapping и distribution/licensing. Поэтому план 12 не
завершён и не переносится в `done/`; owner contract и отложенный scope этим
статусом не меняются.

Последняя указанная полная проверка `task verify` прошла; изменения внесены
отдельными коммитами с локальными hooks, но пока не pushed. Ключевой диапазон
реализации: `d649800`–`1c3bb62` (последний — demuxer/protocol policy).

**Статус: DRAFT.** Подготовлена предметная декомпозиция следующей основной
области. Это не разрешение на реализацию: перед implementation необходимо
получить явные решения владельца по открытым продуктовым пунктам D01–D05 и
зафиксировать их в этом плане. Технические варианты ниже — предложения для
review, а не утверждённый контракт. До этого план не готов к реализации.

## 1. Цель и границы

Сделать путь от существующей локальной медиатеки до проверяемого управляемого
аудиоартефакта сквозным: открыть релиз и его позиции, выбрать применимые
источники, увидеть предварительный результат, явно запустить публикацию и
прочитать фактический созданный файл. Состояние в БД само по себе не доказывает,
что публикация существует или что её теги/аудио содержат ожидаемое.

План покрывает ручную, в том числе частичную, публикацию и утверждённый
автоматический сценарий полного уверенного релиза. Автоматический выбор источника
зависит от согласования политики качества. Ни одна неподтверждённая политика
не должна быть закодирована как default. Выделение ручного-only первого релиза
поставки — предложение о последовательности, требующее отдельного согласования,
а не сокращение целевого scope.

### Не входит

- Пересмотр source inventory, matching, provider acquisition или Operations как
  самостоятельной следующей области. План 11 завершён; Operations не является
  следующим этапом.
- Новая модель локальной медиатеки, изменение matching/unlink semantics или
  автоматическое удаление и повторное связывание `track_source`.
- Расписания, периодический scan/retry либо массовая миграция старых сущностей.
  Конкретные triggers автоматической публикации требуют проработки D04;
  сам утверждённый автоматический сценарий этим планом не отменяется.
- Редактирование исходных файлов. Source roots по умолчанию read-only; отдельная
  настройка удаления исходника после успешной публикации уже предусмотрена
  действующими требованиями, но её поведение и охват требуют решения D05.
- Закрепление физического layout, naming convention, audio codec/preset, лимитов,
  digest policy, новых зависимостей или env-переменных без обоснованного решения.

## 2. Источники требований и подтверждённые ограничения

При расхождении применять актуальные `docs/design/decisions.md`,
`requirements.md`, `data-model.md` и правила архитектуры, а не более строгую
реализацию, прототип или исторический аудит. Источники для этой проработки:

- [Решения владельца: источники и публикации](../../design/decisions.md#источники-и-публикации)
  и [контракт инвентаря/output](../../design/decisions.md#текущий-контракт-инвентаря-анализа-и-output).
- [Требования](../../design/requirements.md#источники-analysis-и-output),
  [логическая модель](../../design/data-model.md) и
  [границы слоёв](../../design/repository-architecture.md).
- [План 10](../done/10-staged-source-analysis.md): в приложении A сохранены
  решения по source freshness, output reset, сохранению локальных сущностей и
  источников. Это свидетельство согласованного контракта, но не текущая
  реализация публикации.
- [План 11](../done/11-incoming-grouping-and-automatic-matching.md): завершённое
  incoming/matching поведение. Релиз может быть локальным и не иметь provider
  links; пустые ручные значения допустимы; matching не выбирает качество и не
  публикует файлы.

Уже подтверждено и не открывается этим планом повторно:

1. Сервис работает с путями файловой системы сервера. Публикация — управляемая
   сервисом копия выбранного аудиоисточника с метаданными релиза/позиции,
   artwork и lyrics. Исходники по умолчанию не изменяются.
2. Локальные release/track самостоятельны и допускают публикацию без provider
   match. Метаданные тега для конкретной позиции принадлежат позиции релиза,
   даже если один `media_variant` используется в нескольких позициях.
3. Для каждой позиции допускается одна активная опубликованная копия; один
   исходный файл может быть выбран для разных позиций/релизов. Дубли публикации
   одной позиции не создаются.
   Целевой результат — извлечённое аудио выбранного источника, при необходимости
   помещённое в выбранный контейнер и снабжённое согласованными новыми тегами,
   с заменой опубликованного артефакта как одного целого. Это утверждает
   наблюдаемый результат, но не способ staging, атомарности конкретной FS
   операции, имена временных файлов или crash-recovery protocol.
4. Автоматическая публикация не заменяет текущую копию источником того же или
   худшего качества. Оператор может явно выбрать любой подходящий источник.
   Формула качества требует согласования; scores разных policy versions напрямую
   не сравниваются — оба варианта оцениваются по текущей policy.
5. Свежесть источника на продуктовом уровне определяется совпадением
   наблюдаемых `size` и `mtime`. Более строгие filesystem/race guards могут быть
   техническими средствами, но не становятся новыми продуктовыми блокерами.
6. При смене output запрещено изменение во время running/claimed execution;
   queued work и все DB references на прежний output сбрасываются транзакционно.
   Старые файлы остаются на диске unmanaged; локальные entities, source links,
   inventory и analyses сохраняются; автоматической повторной публикации нет.
   Publication execution обязан быть включён в running/claimed fencing и reset.
7. История завершённых попыток как продуктовая функция не требуется. Модель
   описывает актуальную публикацию и незавершённые попытки, а не архив.
8. Настройка `publication_format` уже существует и валидирует значения `source`
   и `mka`. Это существующее состояние настройки, а не полное решение о
   контейнере, кодеке, пересэмплировании или преобразовании тегов.

## 3. Проверенное текущее состояние кода

Состояние сверено при подготовке плана по migrations, settings, persistence,
integrations и `backend/internal/publication/`:

- Пакет `backend/internal/publication/` сейчас содержит только `.gitkeep`; его
  рабочей логики публикации нет.
- В последовательности baseline migrations `20261015000001`–
  `20261015000005` заведены application core, source inventory/analysis,
  provider cache, local library и incoming matching. Publication tables и
  публикационных API/jobs/UI в текущей реализации нет.
- Действующие сущности включают `local_release`, `release_track`, `local_track`,
  `local_recording`, `track_source`, `media_variant` и provider links.
  Artwork candidates и lyrics selections описаны целевой моделью, но их runtime
  таблицы/API ещё не поставлены. Это существующая основа, а не утверждение, что
  её схема полностью покрывает D01–D05.
- В Settings уже есть `PublicationFormat` и методы чтения/сохранения
  `publication_format` со значениями `source`/`mka`. Настройка не доказывает,
  что любой выходной файл можно безопасно получить выбранным способом.
- В проекте имеется TagLib wrapper для **чтения** source tags/properties; в
  runtime он не является согласованным/готовым writer-контрактом. Добавление
  writer или новой библиотеки требует технической и лицензионной проверки;
  текущую зависимость нельзя молча считать решённой для записи/распространения.
- Output reset уже использует durable coordinator/journal. Планирование должно
  встроить publication admission/claims и ссылки артефактов в существующие
  механизмы, а не проектировать обходной reset.

Перед implementation повторно проверить текущие имена таблиц, APIs, composition
root и fences: migrations могут быть обновлены независимо от этого документа.

## 4. Реестр открытых решений владельца (блокирует зависящие этапы)

Ниже — конкретные продуктовые вопросы. Варианты приведены как предмет выбора,
не как рекомендованный или согласованный default. Если решение влияет на другой
пункт, фиксировать связь и итог совместно; не заполнять пропуск кодом.

### D01 — идентичность output, структура и коллизии

- Целевая модель уже разделяет каталог публикации релиза и файлы отдельных
  позиций. Уточнить их физическую раскладку и адресацию, не отменяя это разделение.
- Какие части пути формируются из локальных artist/release/track полей; как
  обрабатываются пустые значения, разделители, недопустимые имена, длина,
  регистр/Unicode semantics и переименование сущности?
- При существующем постороннем файле/каталоге по рассчитанному адресу: показать
  коллизию, разрешить явное подтверждение замены, выбрать альтернативу или иной
  вариант? Как различить управляемый artifact и чужой файл после restart?
- Какая гарантия уникальности нужна с учётом возможностей уже проверенного
  filesystem semantics probe? Не предполагать, что PostgreSQL строковое сравнение
  моделирует семантику FS.

### D02 — выбираемый source, audio stream и сегмент

- Как трактовать отсутствие `track_source`, недоступную/удалённую location,
  неуспешный/отсутствующий probe и несовпадение наблюдаемых size/mtime?
- Как оператор выбирает одну из нескольких связей `track_source`; как UI
  различает source path, location, shared `media_variant` и release position?
- Какой audio stream использовать при нуле, одном или нескольких audio streams?
  Нужен ли ручной stream selector; можно ли опубликовать source с неизвестной
  структурой или публикация блокируется?
- Как обрабатывать CUE/manual segment boundaries: точность/округление, границы,
  длительность, пропущенную/устаревшую разметку, несколько позиций из одного
  source? Не вводить авторазделение большого файла без отдельного решения.
- Как обрабатывать неподдерживаемые входные форматы/stream layouts? Целевой
  результат — извлечённое audio; отсутствие согласованной поддержки должно
  приводить к явной ошибке, а не молчаливому выбору другого stream. Конкретный
  форматный охват требует подтверждения tool capability, а не предположения.

### D03 — режим `source`/`mka`, теги и значения

- Для `source`: как именно извлечённый audio представляется в source-form output;
  какие container/codec свойства сохраняются и какие изменения контейнера
  разрешены? Как объяснить несовместимость выбранного metadata mapping с форматом?
- Для `mka` сохраняется утверждённый remux без перекодирования. Проверить
  ограничения Matroska Audio на выбранные streams/codecs и согласовать поведение
  при несовместимости; не вводить транскодирование как fallback.
- Состав полей и mapping для artist credits, album artist, release/track title,
  номера/носителя, даты, MBID, genre и других полей. Как различать отсутствие,
  пустую строку, явный пустой override, несколько значений и порядок значений?
  Не заполнять отсутствующие значения source tags или provider data молча.
- Приоритет локального поля, явного override, выбранного provider значения и
  source tag; правила для удалённых/пустых overrides; представление multi-value
  tags в target container. Согласовать отдельно readback/preview и фактическую
  запись, чтобы интерфейс не показывал выдуманное содержимое.
- Требуется ли сохранение нераспознанных/пользовательских source tags, artwork
  embedded tags и технических stream properties? Не обещать lossless metadata
  preservation без подтверждённой реализации.

### D04 — качество, выбор и автоматический режим

- Какая проверяемая политика определяет пригодность источника и сравнивает
  варианты: какие измерения, веса/порядок, missing values, пороги, равенство и
  несовместимость policy versions? Текущие поля quality в conceptual model не
  задают формулу.
- Частичная ручная публикация уже разрешена: согласовать точный preview/API и
  обозначение пропусков, не превращая полное покрытие релиза в обязательное условие.
- Полный уверенно сопоставленный релиз предусматривает автоматическую
  публикацию. Уточнить конкретный trigger, orchestration и реакцию на
  ties/unknown quality; не пересматривать разрешение automatic flow.
- Равное/худшее качество не запускает автоматическую замену. Уточнить поведение
  при недоступном source и способ переоценки обоих вариантов по текущей policy.
  Явный ручной выбор любого подходящего источника сохраняется; определить
  интерфейс предупреждения/подтверждения, не запрещая ручной downgrade.
- До ответа D04 автоматический selector заблокирован; никаких придуманных
  quality score/default thresholds. Возможность отдельной ручной поставки
  согласовать явно, не объявлять весь план завершённым без automatic flow.

### D05 — assets, lifecycle, ошибки и изменения

- Включение artwork и lyrics входит в обязательную публикацию или допускаются
  настройки/отсутствие отдельных assets? Где берётся artwork (выбранный
  release candidate, встроенная картинка, обе возможности), как обрабатывается
  неизвестный MIME/ошибка? Lyrics могут быть несколькими языками/типами; что
  публикуется и в каком представлении?
- Какие assets создаются в том же atomic intent, а какие могут публиковаться
  позже? Разрешена ли публикация audio, если artwork/lyrics отсутствуют или
  передача asset завершилась ошибкой? Нельзя молча трактовать обещание «включены»
  как «попытались, ошибка скрыта».
- При обновлении tags/source/format/path: нужна ли немедленная публикация новой
  версии, явно запускаемая замена или сохранение старого output? При ошибке
  остаётся старый корректный файл или допустимо состояние без опубликованной
  копии? Что означает delete/reorganize и кто инициирует его?
- Если разрешено удаление source после публикации: триггер только успешный
  физический commit или также успешная проверка/readback; поведение при shared
  variant/location и нескольких tracks; источник остаётся read-only без
  отдельной явной настройки.
- Удаление source root/location не должно стирать локальную сущность или
  source bytes. Нужно определить effect на активную публикацию и retry; не
  расширять утверждённый output reset до удаления физических старых файлов.
- Сбой asset/audio/DB commit, partial success по релизу и retry/restart:
  операторский статус и возможность повтора; нужны ли допустимые частичные
  результаты и как они отражаются без выдачи успеха за полный успех?

## 5. Технический дизайн после решений

Эти задачи уточняют физическую реализацию после закрытия соответствующего D-gate.
Они не требуют owner approval сами по себе, пока не меняют продуктовые правила;
схема, границы и детали должны пройти независимое техническое review.

### D06 — data lifecycle и публикационный read model

Описать актуальный persisted state, attempt state и идентификаторы артефактов:
release-level metadata/assets, position-level source/quality/output, отсутствие
или наличие управляемой копии, незавершённые операции и причины ошибок. Не
хранить завершённую историю как пользовательский feature. Сопоставить логическую
модель `release_publication`, `track_publication` и attempts с действующими
`local_release`, `release_track`, `local_track`, `track_source`, variant и будущими
artwork/lyrics сущностями. Уточнить уникальные ключи и поведение cascade так, чтобы DB cascade не
считался удалением файлов. Зафиксировать, какие данные являются фактическим
readback опубликованного файла, а какие являются только намерением/metadata
snapshot.

### D07 — operation, fences, recovery, replacement

Предложить конечный набор transient states и durable transitions; claim/lease и
delivery fencing; transaction boundary для записи intent и `River.InsertTx`;
идемпотентное восстановление после crash до/после подготовки и exposure; защита
от повторной публикации одной позиции; lock ordering с output reset, source
processing, настройками, claim и сущностями. Спроектировать механизм реализации
уже утверждённого результата «замена как одно целое» так, чтобы читатель не увидел
незавершённый output и ошибка новой попытки не уничтожала прежнюю опубликованную
копию до согласованного commit. Это технический вариант для review, а не
утверждение об уже выбранном filesystem protocol.

### D08 — output namespace и staging

Предложить разделение managed artifacts и временных файлов, containment,
ownership evidence, collision/readback strategy, cleanup и restart recovery.
Существующие логические области `analysis`, `publication`, `checks`, `media` не
задают закреплённые физические имена/иерархию. Выбранный physical layout требует
отдельного технического обоснования; он не должен случайно стать настройкой или
пользовательским контрактом. Не вводить произвольную quota/лимит размера.

### D09 — реальные tools и tag writer

Проверить актуальный FFmpeg/ffprobe lifecycle и поддержку extraction/remux/tag
writing на согласованных форматах; отдельно проверить target platform matrix,
лицензии, packaging/notices и замену/добавление writer library. TagLib в коде
используется для чтения, а не как одобренный writer. Нельзя реализовать preview,
который утверждает успешную запись полей, основываясь только на входной map.
Тесты должны открывать готовый файл независимым readback путём и сверять доступные
поля/stream properties. Выбор инструмента не меняет продуктовый контракт и не
должен расширять зависимости до завершения review.

### D10 — API, UI и operation feedback

Зафиксировать Huma request/response для небольшого release detail read model,
preview/validation, start/status/readback и явных действий замены/retry, только
после решений D01–D05. React использует Orval generated API; generated code не
править вручную. SSE остается wake-up channel: клиент перечитывает snapshot.
UI показывает выбранную позицию/source/формат и фактический путь/статус;
не скрывает пропуски, warning/error assets или то, что теги ещё не проверены.
Не превращать в обязательный prerequisite полный Library screen/всю навигацию.

## 6. Декомпозиция реализации после согласования

Срезы небольшие и допускают отдельный review. Все новые решения, меняющие
поведение, сначала возвращаются владельцу; технический дизайн не является
разрешением заполнить продуктовые пробелы.

| Slice | Результат | Зависимости / критерий приёмки |
| --- | --- | --- |
| P01 — контракт и минимальный read model | Закреплённые D01–D05; диаграмма данных/lifecycle; API shapes и ошибки; технические D06–D10; migration/reset plan. Минимальное чтение выбранного локального релиза и его позиций, без требования строить полный экран медиатеки. | Все нужные owner decisions записаны; независимое review дизайна завершено; подтверждены реальные schema names и отсутствие существующей publication реализации. |
| P02 — схема и repository | Additive migration для текущего publication state, незавершённой попытки/intent и owned artifact refs согласно P01; persistence чтение/запись в транзакциях; down migration безопасна для применённого прототипа. | P01. Constraints проверяют уникальность активной публикации, согласованность состояния и ссылок; cascade не удаляет filesystem artifact. Чистая migration up/down integration coverage. |
| P03 — безопасный ручной planner/preview | Чистое планирование preview из локальных release/track/source links/overrides и согласованного формата; до старта показывает конкретные позиции, source/segment, metadata outcome, target collision/validation. Planner versioned или имеет эквивалентную freshness guard по согласованному контракту. | P01–P02. Preview не меняет исходники, DB publication state или output files; stale preview не принимает молча новые inputs; empty/unknown/conflicting values и collision имеют проверенные outcomes. |
| P04 — интеграция writer и локальный staged output | Извлечь/подготовить согласованный audio stream/segment; скопировать stream или преобразовать только согласно D02/D03; записать agreed tags; сформировать temporary artifact под managed ownership и проверить его реальным readback. | P01, P03, D09. Source не меняется; существующая копия остаётся видимой при failure; source size/mtime проверяется продуктовым freshness rule; покрыть неподдерживаемый stream/format и ошибку инструмента. |
| P05 — atomic exposure/replacement и reset | Durable state machine, staging/recovery/idempotence, collision handling, replacement/cleanup по D01/D05. Publication admission и running/claimed fence участвуют в output reset; reset queued work и publication DB refs транзакционно, старые файлы оставляет unmanaged, entities/source links сохраняет, auto-republish не выполняет. | P02–P04 и D07–D08. Нельзя сбрасывать/переносить output во время publication claim; recovery не удаляет чужой/текущий файл; prior publication сохраняется при fail before commit. |
| P06 — ручной API и truthful UI | Показ release/track/source summary, preview, explicit start, operation snapshot, retry допустимой ошибки и реальный readback состояния. Частичные outcomes отражены согласно D05. | P03–P05 и D10. Huma/OpenAPI → `task generate`; UI без фиктивных значений, с доступными validation/error states; действие оператора явное; no hidden auto publish. |
| P07 — assets и их prerequisites | Спроектировать и реализовать отсутствующие persistence/acquisition/selection для artwork/lyrics в согласованном D05 объёме, затем resolve, staging, publication status и failure reporting; при надобности отдельные asset jobs/refs. | D05 и P05; отсутствие опционального asset не маскируется под успешное обязательное включение. Вынос assets в отдельную поставку требует явного согласования scope. |
| P08 — automatic quality policy и запуск | Чистый, версионированный selector для candidate sources; automatic flow полного уверенного релиза на согласованном trigger/scope. | Согласована формула D04, P01–P06. Тесты доказывают согласованное tie/unknown behavior, запрет автоматической замены равным/худшим качеством и дубликатов. Не выбирать winner произвольно. |
| P09 — regression/integration acceptance | PostgreSQL/River real integration, migration tests, managed filesystem, реальный ffmpeg/writer, restart/recovery/crash matrix, output reset interleavings, UI/API scenarios. | Реализационные slices завершены. Изолированные runtime tests подтверждают фактический файл, не только DB snapshots. |
| P10 — independent review и завершение | Review всего контракта/реализации, исправление замечаний, runtime evidence, актуализация статуса и перенос плана в `done/` после принятой проверки. | P01–P09 по принятому scope. Полный `task verify`; ручные evidence с revision/platform/limitations. Не утверждать реализацию по одному green unit suite. |

Порядок P04/P05 может уточниться после D07/D09, но P06 не должен обходить
транзакционные admission/fencing из P05. P07/P08 заблокированы соответствующими
открытыми решениями, а не исключены из целевого scope. Их нельзя реализовать
через выдуманный default или молча вычеркнуть. Уменьшение первой поставки и любое
изменение пользовательского scope согласовать отдельно.

P05 также включает River worker: args содержат только operation ID, intent и
актуальные execution inputs перечитываются по утверждённому контракту; admission
и enqueue выполняются в одной Bun-транзакции через `River.InsertTx`. Повторная
delivery и restart не создают вторую публикацию. Worker вызывает service;
пакет `publication` владеет файловыми операциями, persistence — БД.

## 7. Обязательная проверочная матрица

Implementation slices должны добавлять проверки рядом с каждым контрактом; P09
сводит их в end-to-end evidence. Fixtures покрывают локальный-only релиз,
provider-linked релиз, допустимые пустые ручные overrides, несколько позиций с
общим variant, разные выбранные sources и отсутствие `track_source`.

### Планирование, чтение и формат

- Release detail не смешивает position metadata с тегами общего `media_variant`;
  preview соответствует выбранному source/segment/format и показывает неизвестные
  или пустые значения различимо.
- Ноль/один/несколько audio streams; невалидная, пустая, граничная и устаревшая
  segment selection; source location отсутствует/недоступна; size или mtime
  изменились относительно preview.
- `source` и `mka` проверяются на согласованных реальных форматах. Проверяются
  stream codec/format, отсутствие случайного re-encode, где он запрещён, и
  readback фактически записанных metadata; неподдерживаемые файлы fail явно.
- Artwork и lyrics: отсутствие, multi-value/language selection, успешное чтение,
  ошибка получения/копирования/проверки и D05-определённая partiality.

### Filesystem, attempts и сбои

- Target отсутствует; target — managed prior output; target — чужой файл;
  collision case/Unicode normalization; неожиданная directory/file; недостаток
  прав; ENOSPC; отмена и недоступный/удалённый source; ffmpeg/writer missing,
  stale version или ненулевой exit; некорректный/неполный staged output.
- Ошибка до exposure сохраняет prior publication. Повтор не создаёт вторую
  управляемую копию и не удаляет чужие файлы. Cleanup failure видим и повторяем,
  не подменяет состояние успешного audio commit без явного D05 решения.
- Crash/restart до начала, после intent, при staging, после validation, вокруг
  exposure и при commit; recovery делает однозначный исход и не публикует
  частично записанный результат.
- Две параллельные попытки одной позиции и конфликт разных tracks на target;
  проигравший не перезаписывает победителя и не оставляет неконсистентные refs.

### Reset, связи и статус

- Output reset против queued, running, claimed publication; против claim/reset
  гонки; reset prepare/DB commit/recovery. Running/claimed операция запрещает
  смену output; queued work и references инвалидируются в одном согласованном
  reset, старые physical files остаются unmanaged.
- Reset сохраняет local entities, source links, inventory и analyses, не
  ставит автоматическую публикацию; root/location deletion и matching changes не
  удаляют source bytes и не симулируют publication success.
- DB failure/rollback не оставляет UI green state или broken active ref. API/UI
  operation snapshot и перечитанный файл согласованы по agreed semantics;
  SSE wakeup не является data source.
- Повторная публикация сохраняет ровно один активный экземпляр на track;
  asset error, partial release и manual replacement отображают раздельно то, что
  действительно committed.

## 8. Acceptance gates и правила реализации

1. **До начала P02:** D01–D05 отвечены владельцем; P01 reviewed; выбранный output
   layout и writer имеют техническое подтверждение; миграционный/reset lifecycle
   согласован с текущими fences. Открытые решения явно помечены deferred, а
   зависящий функционал исключён из scope, не реализован по умолчанию.
2. **На каждом backend slice:** unit tests + focused PostgreSQL integration для
   persistence/River; миграция up/down; commit/rollback; transactional enqueue;
   тесты на подлинных временных файлах; не только mock filesystem при проверке
   ownership/exposure.
3. **После API изменений:** только `task generate` создаёт generated frontend
   client; `task verify` проверяет согласованность и полный проектный gate.
4. **Финальный локальный gate:** для будущей реализации выполнять только
   `task verify` согласно репозиторным инструкциям. Не запускать ad-hoc suites как
   замену полного gate. Платформенную matrix и Compose smoke выполнять средствами
   GitHub CI, не локальными кросс-сборками.
5. **Документация и статус:** источником утверждённых новых продуктовых решений
   остаётся `docs/design/decisions.md` или owner-decision appendix этого плана с
   явной датой/подтверждением и ссылкой. DRAFT можно перевести в implementation
   ready только после решения блокирующих пунктов. В `done/` перемещать только
   после независимого review и принятой evidence.

Никакие новые env vars, произвольные file size limits/quotas, пересэмплирование,
кодек-профили, дефолтные правила записи тегов, физические пути, digest-обязательства
или поведение автоматического quality winner данным планом не утверждаются.

## Приложение A — owner decisions от 2026-10-17

**Статус после дополнения: DRAFT.** Приложение фиксирует только решения,
явно подтверждённые владельцем 2026-10-17 в текущем диалоге. D01 согласован
частично; D02, D03, D04 и D05 остаются открытыми. План не переходит в
implementation ready, implementation не разрешена, исторические разделы выше не
переписаны. Всё, что не перечислено ниже, остаётся открытым.

### A.1 Подтверждённые решения по D01

1. **Path template.** Путь публикации задаётся конфигурируемым относительным
   path template. Конкретное значение default template не утверждено.

2. **Artist credits.** `ALBUM_ARTIST` и `TRACK_ARTIST` — полный credit.
   `ALBUM_PRIMARY_ARTIST` и `TRACK_PRIMARY_ARTIST`: если артист один, он
   определяется автоматически; если несколько — требуется явный выбор в UI.
   При отсутствии выбора использование primary token блокирует публикацию.
   Теги сохраняют полный credit, а не только primary artist.

3. **Общие переменные.** Подтверждены `ALBUM_NAME`, `TRACK_NAME`, `ALBUM_YEAR`,
   `DISC_NUMBER`, `TRACK_NUMBER`; числовые переменные поддерживают форматирование
   `:02`. `EXT` — фактический контейнер выходного файла. Подтверждены полные и
   короткие MBID, а также стабильные локальные IDs артиста, релиза и позиции.
   Primary-artist ID tokens scoped к релизу/позиции (например,
   `ALBUM_PRIMARY_ARTIST_MBID_PART`, `ALBUM_PRIMARY_ARTIST_ID` и аналогичные).
   Для релиза используется release MBID, не release-group MBID.

4. **Fallback.** Синтаксис `{A | B | "literal"}` выбирает первое непустое
   доступное значение. Неизвестный token или ошибка вычисления не являются
   основанием для fallback.

5. **Скрытые defaults отсутствуют.** Отсутствующая или пустая переменная не
   подменяется неоговорённым значением.

6. **Release type.** Подтверждены `ALBUM_PRIMARY_TYPE`, `ALBUM_SECONDARY_TYPES`
   и `ALBUM_TYPE`. Пример комбинированного представления — `Album - Remix`;
   при первичном типе `EP` или `Single` — соответственно `EP` или `Single`.
   Локальные типы без MusicBrainz
   допустимы.

7. **Various Artists.** Various Artists определяется специальным MBID, а не
   именем. Для сборника используется общий каталог; артист трека не выбирается
   автоматически. Локальный аналог Various Artists — явная отметка.

8. **Коллизии.** При коллизии добавляется стабильный position suffix; выбранное
   имя закрепляется; существующее имя не переименовывается; при новом конфликте
   suffix удлиняется. Учитывается filesystem semantics для case/Unicode.
   Коллизии каталогов разных релизов не разрешается смешивать молча.

9. **Идентичность.** Идентичность сущности/владельца в БД не выводится из MBID
   или имени.

10. **Владение файлом и замена.** Чужой файл можно явно заменить через UI.
    Свою публикацию другой позиции перезаписывать нельзя. Внешнее изменение
    своего опубликованного файла — отдельный конфликт.

11. **Изменение metadata/template.** Само по себе не перемещает и не
    переименовывает опубликованный файл. Reorg и replacement — только явные
    действия.

12. **Replacement и ошибки.** При явной замене, включая mp3 → flac, старый файл
    удаляется. Ошибка подготовки сохраняет старый файл. Незавершённый cleanup
    даёт видимый статус незавершённой замены, а не полный успех; замена
    восстанавливается после перезапуска и не создаёт две активные публикации.

13. **Пустые metadata.** Пустые metadata хранить можно, но публикация
    блокируется без release artist, release title и track title — независимо от
    выбранного шаблона.

### A.2 Что осталось открытым

- Значение default template.
- Правила отсутствующего года и иных optional components (сверх
  подтверждённого синтаксиса fallback).
- Точные правила sanitization и длины; полная case/Unicode-нормализация сверх
  подтверждённой обработки коллизий.
- Плоский layout против release directories и размещение assets явно не
  закрыты.
- D02 — выбираемый source, audio stream и сегмент.
- D03 — режим `source`/`mka`, теги и значения.
- D04 — качество, выбор и автоматический режим.
- D05 — assets, lifecycle, ошибки и изменения.

### A.3 Границы приложения

Приложение не утверждает длины IDs, конкретные правила sanitization, форматный
scope, качество, поведение assets, обязательные defaults или иные
неперечисленные продуктовые решения. До закрытия D01 и D02–D05 действуют
прежние acceptance gates раздела 8.

### A.4 Уточнения владельца по D02/D04 от 2026-10-17

Следующие ответы получены после A.1–A.3; они уточняют соответствующие открытые
пункты, но не закрывают D02/D04 целиком и не разрешают implementation:

- Первичная публикация выбирает источник автоматически по качеству, а не
  требует ручной публикации каждой позиции. Используется лучший из доступных
  проверенных источников. В automatic mode появление источника более высокого
  качества приводит к перепубликации; замена равным/худшим по-прежнему запрещена.
- Явный ручной выбор источника с публикацией закрепляет manual mode: дальнейшие
  автоматические замены запрещены. Возможность явного возврата в automatic mode
  предложена владельцем, но точное действие и его последствия ещё требуют
  согласования. Формула качества и поведение ties/unknown остаются открытыми.
- Несовпадение size/mtime не должно означать публикацию непроверенного файла.
  Владелец допускает автоматический переанализ либо обращение к оператору;
  конкретный вариант ещё не выбран. Другие доступные проверенные источники
  остаются кандидатами для автоматического выбора.
- Ноль audio streams — ошибка; один выбирается автоматически; несколько требуют
  явного выбора в UI с отображением языка, названия, кодека и каналов.
- Источник «альбом целиком + CUE» не публикуется целиком вместо отдельных
  позиций. Точные правила использования CUE, границ/точности extraction,
  отсутствующей или устаревшей разметки остаются открытыми. Предложение
  публиковать весь stream при отсутствии границ не утверждено.
- Для неподдерживаемого формата — явная ошибка, без транскодирования или выбора
  другой аудиодорожки как fallback (подтверждено владельцем «пока да»).

Повторная проверка документации: `docs/design/decisions.md` явно откладывает
формулу качества на отдельное согласование (раздел о `quality_policy_version`).
Приоритеты lossless/lossy, bitrate, sample rate, bit depth и channels этим
приложением не устанавливаются; демонстрационная MP3 → FLAC эвристика прототипа
не является утверждённой quality policy.

### A.5 Согласование качества и переноса CUE от 2026-10-17

Владелец подтвердил следующие предложения после повторной проверки отсутствия
ранее утверждённой формулы качества в документации:

- Подтверждённый lossless предпочтительнее lossy.
- Для lossless при одинаковом числе и назначении каналов сравниваются сначала
  bit depth, затем sample rate (большее значение предпочтительнее).
- Для lossy одного кодека больший bitrate предпочтительнее. Разные lossy-кодеки
  и разные channel layouts автоматически не ранжируются.
- Неизвестные характеристики не считаются доказательством улучшения.
- При равенстве текущая публикация сохраняется; для первой публикации из равных
  кандидатов применяется стабильный технический порядок. Конкретный порядок
  ещё должен быть описан техническим дизайном. Эта политика предпочтения
  характеристик не обнаруживает апсемпл и не доказывает происхождение записи.
- При несовпадении size/mtime автоматически запускается переанализ. Пока он
  выполняется, доступны другие проверенные кандидаты. Если переанализ не удался,
  оператор видит ошибку. Существующая публикация не понижается, manual mode
  не снимается.
- Действие «Вернуть автоматический выбор» показывает preview выбранного
  источника и необходимости замены. Подтверждение включает automatic mode;
  равное/худшее качество текущую публикацию не заменяет.
- **Изменение scope по явному решению владельца:** публикация источников
  «альбом + CUE» и позиций с CUE/manual segment boundaries откладывается на
  отдельный шаг/план. Если обнаружены такие разбивки, UI явно сообщает, что
  публикация сегментов пока не поддерживается. Нельзя игнорировать границы и
  публиковать целый исходник вместо позиции. Это решение заменяет предложение
  о реализации segment extraction в текущем этапе, но не удаляет существующие
  source links или разметку и не меняет matching semantics.

Требования P03/P04/P09 и матрицы к extraction сегментов в исходном тексте
относятся к отложенному scope; в текущем этапе проверяется явное сообщение
о неподдерживаемом сценарии без публикации целого образа. До реализации
необходимо отдельно оформить план отложенной поддержки. Другие открытые
решения D01–D05 этим дополнением не закрываются; статус остаётся DRAFT.

### A.6 Метаданные и предложение M4A от 2026-10-17

Владелец подтвердил следующие принципы D03:

- MKA — remux без перекодирования, несовместимый кодек даёт явную ошибку.
- Writer использует текущие локальные метаданные релиза/позиции с явными
  overrides. Подготовка и согласование provider/source/manual значений относятся
  к предыдущим этапам, а не к публикационному writer. Это не утверждает, что
  все необходимые поля и правила подготовки уже реализованы.
- Явно пустое необязательное поле удаляет соответствующий тег; отсутствующее
  значение не восстанавливается молча из исходника.
- Согласованный набор тегов записывается заново; сохранение неизвестных
  пользовательских source tags не обещается. Artwork/lyrics требуют D05.
- Preview показывает намерение; после публикации отдельно отображаются
  фактический readback и ошибки проверки.

Владелец предложил расширить варианты до исходного контейнера без
перекодирования, MKA без перекодирования и M4A с возможным перекодированием.
Запрос требует технической оценки и дальнейшего согласования: политика
ALAC/AAC, параметры AAC, допустимость lossy → lossy и изменение запрета
автоматического понижения аудиокачества пока не утверждены. M4A не является
универсальным контейнером для всех исходных кодеков; нельзя считать расширение
гарантией совместимости с любым Apple-плеером или готовности tag mapping.

### A.7 Уточнение разрешения M4A от 2026-10-17

Владелец разрешил необходимое перекодирование при выборе M4A без отдельного
переключателя-разрешения. UI при выборе контейнера явно сообщает о возможности
перекодирования. Это заменяет предложение об отдельном разрешении lossy → AAC;
конкретные параметры кодирования и окончательная codec policy ещё не выбраны.
Запрос владельца на проверку произвольных M4A-тегов, имеющегося TagLib writer
и совместимости MBID/других полей с Navidrome/OpenSubsonic является технической
задачей исследования, а не разрешением молча терять необходимые теги или
использовать FFmpeg как единственный tag writer.

### A.8 Writer, целевой reader и M4A codec policy от 2026-10-17

Владелец явно подтвердил:

- Для M4A AAC/ALAC копируются без повторного кодирования; другой lossless
  преобразуется в ALAC, другой lossy — в AAC. Отдельного разрешения
  перекодирования нет; действует предупреждение A.7. Параметры AAC и поведение
  при неподдерживаемых ALAC характеристиках ещё требуют согласования.
- **Единственный tag writer для всех поддерживаемых форматов — TagLib.**
  FFmpeg только подготавливает чистую аудиодорожку, выполняет remux и необходимые
  согласованные перекодировки; запись публикационных тегов через FFmpeg
  не допускается. Это правило относится также к MKA; отсутствие writer support
  нельзя обходить записью тегов через другой инструмент без нового решения.
- Текущий целевой потребитель опубликованных файлов — Navidrome. Mapping тегов
  для каждого поддерживаемого формата должен быть совместим с чтением Navidrome,
  включая MusicBrainz IDs и согласованный состав полей. Хранение тега в файле не
  означает гарантии его выдачи OpenSubsonic API или отображения любым клиентом.

Технический gate D09: проверить возможности именно используемой версии
TagLib/wrapper для каждого формата, особенно MKA, включая реальные write/readback
и Navidrome mapping. Если нужный формат или поле не поддерживается, вернуть
вопрос владельцу; не подменять writer и не объявлять тег записанным. Состав
полей, assets и оставшиеся продуктовые пункты по-прежнему требуют закрытия.

### A.9 Исключение для MKA от 2026-10-17

После A.8 владелец уточнил: для MKA разрешено исключение из TagLib-only writer,
предложив использовать существующую библиотеку чтения Matroska. В проекте это
`github.com/remko/go-mkvparse`. Возможность записи этой библиотекой ещё должна
быть подтверждена; само наличие reader не доказывает writer capability.
Если записи нет, выбор другой зависимости возвращается владельцу. Для прочих
форматов сохраняется TagLib-only правило; FFmpeg по-прежнему только готовит
аудио. Поддержка MKA не исключается из scope этим уточнением.

### A.10 Единый TagLib reader/writer и удаление native capture от 2026-10-17

Владелец согласовал переход на `deluan/go-taglib` для чтения и записи всех
целевых форматов, включая MKA, с удалением `go-mkvparse`. Для MKA достаточно
нормализованной плоской карты; сохранение вложенности, targets, языкового
контекста и бинарных тегов в отдельном native capture больше не требуется.
Обложки обрабатываются отдельно и этим упрощением не исключаются.

Владелец явно отверг предложение сохранять старые native snapshots как legacy:
проект находится в разработке, ненужный native capture нужно удалить целиком
из модели, persistence, API, UI и тестов, а не оставлять compatibility layer.
Это заменяет прежние требования native Matroska capture и предложение сохранять
существующие snapshots. Исторические свидетельства в завершённых планах не
переписываются. Конкретная очистка схемы является технической задачей.

Запись согласованных тегов по-прежнему должна проверяться на готовых файлах
и на совместимость с Navidrome. Использование того же wrapper в Navidrome
обосновывает выбор интеграции, но не заменяет проверки нашего writer/mapping.

Уточнение текущего состояния: technical analysis уже сохраняет все audio
streams; matching eligibility требует ровно один. Publication ещё не
реализована. Вопрос владельца о текущей поддержке нескольких потоков не является
новым решением об их поддержке/запрете и сам по себе не отменяет A.4.

### A.11 Assets, lifecycle и границы удаления от 2026-10-17

**Статус: DRAFT.** Дополнение фиксирует решения владельца по части D05 и
уточняет автоматический/manual lifecycle. Это не полное закрытие D05 и не
разрешение implementation. Разделы A.1–A.10 и остальной текст выше сохранены
как исторические свидетельства и не переписаны.

1. **Audio без cover.** Публикация audio разрешена при отсутствии artwork.
   Ошибка загрузки cover не блокирует audio: остаётся прежняя опубликованная
   обложка либо отсутствие обложки, а UI показывает warning с действием retry.

2. **Одна обложка на релиз.** Для релиза допускается одна cover. Источник по
   умолчанию — provider, затем fallback на embedded source artwork. Ручная
   загрузка/выбор обложки переопределяет автоматический выбор и закрепляет
   manual mode. Возврат к автоматическому выбору — только явное действие
   оператора.

3. **Embedded vs sidecar.** С учётом flat layout владелец **склоняется** к
   embedding обложек в аудиофайл вместо sidecar. Это зафиксировано именно как
   предпочтение, а не как окончательное решение: варианты sidecar этим пунктом
   не закрыты и требуют отдельного подтверждения.

4. **Lyrics.** Lyrics опциональны. Публикуется только одна synced-версия:
   предпочтительно уже существующий synced source, иначе LRCLIB. Unsynced
   lyrics не публикуются.

5. **Форма lyrics.** Synced lyrics встраиваются, если Navidrome это
   поддерживает; иначе используется sidecar. Проверка фактической поддержки
   Navidrome остаётся технической задачей; это условное решение владельца,
   а не произвольный выбор реализации.

6. **Ошибка lyrics.** Неудача получения/записи lyrics не блокирует публикацию
   audio: файл публикуется без lyrics, UI показывает warning с retry/выбором
   источника.

7. **Автоматические изменения.** Изменения metadata/assets в automatic mode
   публикуются автоматически через staging/replacement по действующим правилам
   замены. Ручные изменения помечают публикацию как dirty; оператор применяет
   изменения по отдельности или все pending — скрытого auto-apply ручных правок
   нет.

8. **Удаление source.** Удаление источника после публикации — opt-in настройка,
   по умолчанию **OFF**. Удаление допускается только после успешной публикации и
   validation. Включение настройки позже не запускает retroactive удаление:
   доступны точечное действие и bulk-операция в UI. Ошибка удаления — warning,
   а не провал публикации; UI даёт точечный и bulk retry.

9. **Неоднозначная формулировка владельца.** Фраза «Публикуем самую
   качественную, ... удаляем все связанные» **не** утверждает семантику удаления
   для shared sources/positions. Требуется явный ответ владельца, что именно
   считается связанным: все альтернативные входы одной опубликованной позиции
   или также source/location, нужные другим позициям/релизам, дубликаты путей и
   т.п. До ответа зависящая от этого семантика удаления не реализуется.

10. **Удаление root/location.** Удаление source root/location всегда оставляет
    publication bytes, опубликованные metadata и quality. Оно не
    является удалением публикации и не ухудшает опубликованное качество.

11. **Удаление публикации.** Добавляется явное действие удаления публикации.
    Внешнее изменение опубликованного файла требует отдельного подтверждения
    перед удалением; замена рассматривается как конфликт по A.1.

12. **Граница job.** Один job обрабатывает одну позицию (track/position) со
    всеми её sidecars и metadata. Успешно опубликованные позиции остаются
    валидным частичным результатом релиза; частичный успех не выдаётся за полный.

13. **Техническая оговорка.** Кросс-путевая «атомарность» (несколько файлов,
    assets, DB и FS) не гарантируется этим планом. Технический дизайн обязан
    описать реальные границы и recovery, а не постулировать атомарность, которой
    нет.

Legacy native capture не возвращается: действует A.10. Незакрытые части D05
(точный формат/приоритеты assets сверх перечисленного, окончательное решение
embedding обложки, объём удаления связанных источников) остаются открытыми;
статус плана — DRAFT.

### A.12 Удаление альтернативных исходников и provenance от 2026-10-17

Владелец уточнил A.11:

- При включённом удалении исходников после успешной публикации и валидации
  позиции удаляются все её альтернативные исходники, включая менее качественные.
  Цель — единая переорганизованная медиатека с приоритетом качества; это не
  ограничивается удалением только выбранного для аудио файла.
- Общий исходник не удаляется, пока он нужен другим связанным позициям/релизам:
  должны завершиться все связанные публикации и проверки. Удаление не должно
  лишать ещё не опубликованную позицию нужного источника. Правила OFF по умолчанию,
  отсутствия retroactive auto-delete и warning/retry из A.11 сохраняются.
- Источник происхождения опубликованного файла как сохраняемая функция не нужен.
  Хранится текущее состояние публикации, metadata и достаточные характеристики
  качества для обнаружения улучшений и необходимости обновления. Не создавать
  архив происхождения или сохраняемые historical source snapshots ради legacy.
  Это не отменяет временные execution inputs/claims для безопасного выполнения
  незавершённой операции и не требует удаления действующего source inventory.
- Для обложки владелец подтвердил направление «только embedding, без
  cover.jpg/png рядом», запросив возможные технические возражения. Обнаруженная
  несовместимость формата должна возвращаться на согласование, а не молча
  добавлять sidecar.

Вопрос synced lyrics из других исходников остаётся открытым: владелец указал
на возможное смещение таймингов и запросил рекомендацию о переходе к LRCLIB.
Нельзя считать lyrics другой версии аудио автоматически совместимыми или
самостоятельно вводить retiming/пороги совпадения длительности.

### A.13 Согласованный порядок synced lyrics от 2026-10-17

Владелец подтвердил предложенный порядок и уточнил пропущенное «не» в A.12:

1. Сначала synced lyrics из выбранного аудиоисточника.
2. При их отсутствии — LRCLIB с названием, исполнителем, альбомом и длительностью
   выбранной аудиодорожки.
3. Автоматического переноса lyrics из других связанных исходников нет.
4. При смене аудиоисточника lyrics разрешаются заново; прежние таймкоды не
   переносятся вслепую.
5. Автоматический сдвиг таймингов не входит в текущий этап. UI предоставляет
   просмотр, ручной выбор и возможность убрать неверный текст.

LRCLIB не гарантирует совпадение таймингов конкретной версии аудио. Данный
порядок не утверждает произвольных duration thresholds или скрытого retiming.

### A.14 Оставшиеся решения D01–D05 от 2026-10-17

Владелец ответил на итоговый список вопросов:

- Default path должен группировать релизы по типу: добавить отдельный компонент
  `ALBUM_TYPE` либо `ALBUM_PRIMARY_TYPE` перед компонентом
  `{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]`.
  Базовый предложенный шаблон использует primary album artist с MBID/local ID,
  затем каталог релиза и `{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`.
  Конкретный выбор между двумя type tokens, fallback отсутствующего типа и
  вопрос разрешения общего каталога разных релизов ещё требуют уточнения.
- AAC: фиксированный bitrate с ориентацией на исходник, например MP3 256 → AAC
  256 kbit/s, источник 320 → AAC 320 kbit/s. Цель — максимально сохранить
  качество исходника. Это не означает эквивалентности качества разных кодеков.
  Правила unknown/VBR bitrate, других кодеков и числа каналов ещё не определены;
  параметры ALAC вне поддерживаемого диапазона этим ответом не согласованы.
- Утверждён базовый состав тегов при наличии локального значения: release/position
  titles; полные credits и отдельные списки артистов; номера и общее количество
  треков/дисков; release/original release dates; release types, genre; artist,
  album-artist, release, release-group, recording и release-track MBIDs; ISRC,
  label, catalog number, barcode, release country/status; выбранные cover и
  synced lyrics. Обязательного заполнения всех этих полей нет. Дополнительные
  поля Navidrome нужно исследовать и предложить, а не выдумывать значения или
  обязательные пользовательские теги.
- Единственный проверенный пригодный источник может публиковаться автоматически
  при неполных quality characteristics. Несколько несравнимых кандидатов требуют
  ручного выбора. Для автоматической замены нужно доказанное улучшение.
- Manual authority независима: ручная правка конкретного тега защищает только
  этот тег от автоматического изменения; ручной выбор cover/lyrics защищает
  соответствующий asset; ручной выбор source защищает только смену source/audio
  stream. Остальная автоматика продолжает работать. Ручные изменения применяются
  явно по правилам pending из A.11.
- Автоматический старт готовых позиций разрешён после уверенного automatic
  matching, ручного подтверждения matching или подтверждения local-only релиза.
  Не требуется ждать готовности всего релиза. Улучшение опубликованной позиции
  не зависит от готовности остальных позиций.
- При отсутствии provider cover автоматический выбор embedded cover стабилен
  по порядку позиций релиза; оператор может изменить выбор вручную.

Эти ответы не выбирают неоговорённые fallback/encoder profiles. Неотвеченные
подпункты остаются открытыми до явного уточнения владельцем.

### A.15 Закрытие последних уточнений от 2026-10-17

Владелец подтвердил:

- Default template:
  `{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`.
  Пользовательский шаблон может явно складывать разные релизы в общий каталог.
  Конфликтующие файлы различаются position suffix согласно A.1; технический
  дизайн должен отличать явно общий layout от непреднамеренной коллизии.
- AAC: для MP3 сохраняется измеренный bitrate аудиопотока; для VBR используется
  средний bitrate; для других lossy-кодеков и при неизвестном bitrate —
  AAC 320 kbit/s для stereo. Multichannel в AAC пока не перекодируется: UI
  показывает ограничение. Если lossless нельзя представить в ALAC без потерь,
  возвращается ошибка без скрытого уменьшения разрядности или сведения каналов.
- Дополнительные поддерживаемые metadata: sort names названий/артистов;
  composer, lyricist и другие contributors с ролями; work/work MBID и movement
  name/number/count; disc title, release version; compilation, explicit/clean;
  mood, BPM, grouping, comment; copyright, license, website, language, media.
  При наличии локальных значений они входят в mapping; отсутствующие значения
  не выдумываются и заполнение не становится обязательным.
- Для дополнительных полей требуется сохранять источники их значений
  (metadata field provenance) и предоставить локальное заполнение. Это
  происхождение metadata, а не отменённая A.12 история аудиоисточника публикации.
- ReplayGain/R128 выносятся в отдельный этап; вычисление и публикационный
  mapping этих значений не реализуются в текущем scope без нового согласования.

Ответы A.1–A.15 являются актуальным owner contract вместо незакрытых вопросов
исходного DRAFT. Перевод плана в implementation ready требует свести контракт,
оформить отложенные CUE/ReplayGain этапы и завершить технические D06–D10 с
независимым review. Это технические гейты, а не необходимость повторного
подтверждения уже перечисленных решений владельца. Доказанная несовместимость
или новый продуктовый выбор возвращаются владельцу отдельно.

### A.16 Lyrics при смене аудио и mono AAC от 2026-10-17

Владелец подтвердил следующие уточнения:

- При автоматической замене аудиоисточника вручную выбранные lyrics с
  неподтверждённой совместимостью таймингов не переносятся в новый файл.
  Аудио обновляется без спорного текста; ручной выбор сохраняется pending
  для проверки оператором, manual authority не снимается.
- Для mono других lossy-кодеков либо неизвестного bitrate выбран fallback
  AAC 160 kbit/s; для MP3 сохраняется правило измеренного bitrate.
  Это профиль кодирования, а не уменьшение числа каналов и не утверждение
  эквивалентного качества. Multichannel transcode по-прежнему не поддерживается.
- Если точный LRCLIB lookup не дал synced lyrics, аудио публикуется без текста.
  Поиск других вариантов доступен для ручного выбора в UI; автоматического
  выбора другой найденной версии нет.

### A.17 Изменение занятого источника

Владелец выбрал запрет удаления каталога-источника и изменения его пути,
пока незавершённая публикация удерживает его файлы. Это относится и к очереди:
автоматически отменять queued-публикации ради удаления не требуется.
Неразрешённое исполнение после сбоя сохраняет защиту до безопасного завершения;
одного terminal operation state недостаточно для снятия удержания.
Уже опубликованные файлы, их metadata и quality при последующем удалении
источника сохраняются согласно ранее согласованным правилам.
