# RFC: возможность интеграции beets

Статус: **рекомендация принята для проектирования, реализация не начата**.

## 1. Контекст

beets часто воспринимают как CLI, однако фактически это Python-приложение с БД библиотеки, моделями, query language, importer pipeline и plugin API. Поэтому построить вокруг него UI возможно. Вопрос состоит не в технической возможности, а в правильной границе ответственности.

MusicEnreachment уже реализует:

- read-only intake через reconciliation scan;
- стабильный `LibraryRecord`, не зависящий от пути;
- версии источников и SHA-256 provenance;
- AcousticID/MusicBrainz evidence и review decisions;
- слои `original`, `analyzed`, `final`;
- безопасный staging и атомарную публикацию;
- durable jobs/retries/quarantine;
- React UI с каталогом, инспектором и candidate review.

См. `DESIGN.md:7`, `src/music_ingest/processing/worker.py:232`, `src/music_ingest/publication/service.py:51`.

## 2. Что beets предоставляет как платформа

| Область | Реальная поверхность | Оценка стабильности |
|---|---|---|
| Library DB | `Library`, `Item`, `Album`, queries, `Transaction` | Документирована, но названа внутренним API; использовать только через адаптер и pinning |
| Importer | `ImportSession`, `ImportTask`, candidate lookup, plugin stages | Повторно используем, но не является стабильным асинхронным протоколом для GUI |
| Plugins | events, commands, fields, queries, metadata sources | Самая подходящая extension boundary, но registries process-global |
| CLI | import/list/update/write/move/config | Ориентирован на человека; нет единого JSON-контракта |
| Web | Flask CRUD/query/media UI | Базовый локальный UI; не application platform |
| Metadata logic | MusicBrainz, scoring, matching, mediafile integration | Частично отделима; многие части зависят от глобальной конфигурации и плагинов |

Основные источники: [library API](https://beets.readthedocs.io/en/latest/dev/library.html), [plugin API](https://beets.readthedocs.io/en/latest/dev/plugins/index.html), [importer](https://beets.readthedocs.io/en/latest/dev/importer.html), [CLI](https://beets.readthedocs.io/en/latest/reference/cli.html).

## 3. Capability fit

| Возможность beets | Что уже есть | Решение |
|---|---|---|
| Autotag scoring | Typed scoring и provider evidence в `matching/scoring.py` | Не внедрять без benchmark, доказывающего лучшую точность |
| Discovery/grouping | Сканирование путей есть, но полноценной группировки альбомов для drop-folder нет | Единственный сильный кандидат для будущего subprocess |
| Query language | UI-фильтрация простая, данные уже в PostgreSQL | Заимствовать синтаксис или реализовать subset над PostgreSQL |
| Plugins | В проекте уже модульные capabilities | Подключать только конкретный plugin с измеримой ценностью |
| Path templates | Есть управляемая и атомарная publication layout | Не передавать beets владение путями |
| Tag/media I/O | Есть инспекторы и allowlisted writer | Рассматривать `mediafile` напрямую только при format gap |
| Duplicate handling | Есть source hashes, stable identity, history | beets может только предложить candidates; merge/delete остаются у приложения |
| Library SQLite | PostgreSQL уже является authority | Не использовать как вторую постоянную медиатеку |
| File operations | Есть read-only source и atomic publication | Запретить beets move/copy/write/delete в первом контуре |

Алгоритм scoring в beets развит ([distance](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beets/autotag/distance.py#L384-L510)), но полнота алгоритма не означает автоматическую пользу для нашей уже существующей evidence-led модели.

## 4. Варианты интеграции

### A. Полная замена текущей медиабиблиотеки

**Вердикт: reject.**

Теряются или усложняются стабильные IDs, append-only revisions/events, PostgreSQL jobs, provider snapshots и текущий safety contract. `Item`/`Album` и SQLite не являются эквивалентом нашей модели.

### B. In-process embedding в FastAPI runtime

**Вердикт: reject.**

Плюсы: низкая задержка, прямой Python API.

Минусы:

- process-global config/plugins;
- blocking importer pipeline;
- fault/config coupling с API и worker;
- локальная, а не межпроцессная сериализация транзакций;
- невозможность независимо обновлять и перезапускать integration runtime.

### C. Отдельный долгоживущий управляющий сервис beets

**Вердикт: условно приемлемый вариант, но не первый шаг.**

Сервис нужен только если selected capability действительно требует долгоживущей `Library` или startup cost измеримо мешает. SQLite является одноразовой производной проекцией, и writer должен быть один. API принадлежит MusicEnreachment, все команды идемпотентны.

### D. Изолированный subprocess

**Вердикт: предпочтительный proof-of-contract для bounded operations.**

Подходит для read-only discovery, импорта as-is в одноразовую БД, list/query и dry-run. Не является полноценным machine API: общего JSON output нет ([issue #376](https://github.com/beetbox/beets/issues/376)), `-c` накладывает config поверх пользовательского, а exit 0 не означает item-level success ([CLI source](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beets/ui/__init__.py#L910-L999)).

### E. Выборочное переиспользование

**Вердикт: preferred baseline.**

Не тянуть весь beets ради малых функций. Рассматривать отдельные зависимости или идеи:

- `mediafile` напрямую;
- query syntax как UX/reference;
- собственный benchmark scoring;
- basic template concepts;
- отдельный metadata plugin только после contract tests.

## 5. Рекомендуемая роль beets

На текущем этапе: **никакая роль beets в runtime не обязательна**.

Возможная будущая роль:

```text
read-only input snapshot
  -> isolated beets subprocess
  -> discovered groups / candidate manifest
  -> parse into MusicEnreachment DTO
  -> persist as evidence
  -> review and publication remain in MusicEnreachment
```

beets не получает права:

- изменять incoming files;
- публиковать в Navidrome-visible root;
- назначать domain IDs;
- создавать финальную revision без application command;
- решать duplicate merge/delete;
- определять UI status.

## 6. Почему официальный web plugin не подходит

Он полезен как пример структуры API: items, albums, queries, artwork и media. Но:

- routes объявлены в module-global Flask app;
- endpoint extension hook отсутствует;
- встроенной authentication/authorization нет;
- writable mode вызывает изменение тегов и удаление;
- API не версионирован как наш application contract.

Источники: [web docs](https://beets.readthedocs.io/en/latest/plugins/web.html), [implementation](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beetsplug/web/__init__.py#L109-L204), [open extension issue](https://github.com/beetbox/beets/issues/739).

## 7. Проверенные ограничения

### 7.1 Transaction behavior

В disposable probe на CPython 3.14.3 / beets 2.13.1 / SQLite 3.51.2:

- create/query/reopen прошли;
- mutation перед исключением внутри root transaction сохранилась.

Это согласуется с текущим `Transaction.__exit__`, который на root exit вызывает commit ([source](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beets/dbcore/db.py#L925-L952)). Следовательно, adapter не должен обещать rollback на основании context manager.

### 7.2 Concurrency

beets держит connection per thread и process-local lock ([source](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beets/dbcore/db.py#L1107-L1195)). SQLite защищает страницы между процессами, но не кэши Python, выбор путей и операции с файловой системой. Требуется один writer.

### 7.3 Importer/UI

`choose_match()` вызывается синхронно внутри pipeline; cancellation является internal pipeline mechanism, но не `ImportSession` contract ([stages](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/beets/importer/stages.py#L143-L166)). Серьёзные GUI-проекты поэтому добавляют отдельное job/event storage: [beets-flask](https://github.com/pSpitzner/beets-flask), [Beetkeeper](https://github.com/zach-overflow/beetkeeper), [Beets Web Manager](https://github.com/Iranman/beets-web-manager).

## 8. License, runtime и upgrades

- beets 2.13.1: MIT, уведомление должно попасть в third-party notices ([LICENSE](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/LICENSE)).
- Поддерживается Python `>=3.10,<3.15`, включая наш Python 3.14 ([pyproject](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/pyproject.toml#L1-L41)).
- Security policy поддерживает только последнюю версию ([SECURITY.md](https://github.com/beetbox/beets/blob/dc1709e14849adfec7208c53196dc2b01b5f3fd9/SECURITY.md)).
- Совместимость с legacy metadata plugins будет удалена в v3, дата v3 не объявлена ([migration guidance](https://beets.readthedocs.io/en/latest/dev/plugins/autotagger.html#migration-guidance)).

Нужен точный pin версии, allowlisted plugins, dependency/license inventory и candidate-version compatibility suite.

## 9. Go / No-Go

### Go для ограниченного эксперимента

- есть конкретный capability gap;
- подготовлены representative fixtures и baseline;
- subprocess полностью read-only;
- результат сериализуется без parsing arbitrary prose;
- value metric заметно лучше текущего решения;
- удаление disposable SQLite не приводит к потере canonical state.

### No-Go

- beets должен стать authority для IDs, tags, publish eligibility или history;
- требуется bidirectional sync двух изменяемых БД;
- операция требует интерактивного importer без durable decision protocol;
- нужны move/write/delete по incoming root;
- восстановление требует beets numeric IDs, importer history или plugin-local cache;
- adapter и compatibility suite дороже доказанной пользы.

## 10. Итог

**Встраивание возможно, но продукту сейчас выгоднее продолжить собственную архитектуру.** beets следует держать как optional experimental engine, а не как foundation. Это сохраняет возможность воспользоваться его сильными алгоритмами и экосистемой без отказа от уже реализованных преимуществ MusicEnreachment.
