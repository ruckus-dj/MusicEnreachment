# Удаление root: staged-копии и failed analysis tasks

База — `686028c125f055382c48412c2218430908c2271f`: полный обычный hook
и [вся CI 42360466600](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42360466600)
прошли, включая контейнер.

Продолжается пункт 2 [плана](../plans/todo/13-main-interface-design-parity.md).
Текущий ограниченный срез реализует связанные app-owned staged-копии и failed analysis
tasks, включая detached work. Успех удаления root не должен оставлять эти копии;
cleanup host sources, CURRENT и файлов публикаций сюда не входит.

Владелец отдельно подтвердил: удаление root само безопасно прекращает recovery
зависимой failed publication, а не требует предварительного отдельного действия.
Это уточнение внесено на месте в `decisions.md`. Реализация такого abandonment
остаётся следующим срезом; до неё существующие publication/recovery holds
не снимаются и продолжают защищать authority.

Отдельные новые гарантии inode/device/hash identity временных staged-копий,
новый пользовательский retry workflow или speculative cleanup matrix не
вводятся. Используется существующий безопасный cleaner зарегистрированных
app-owned путей. Ревизия `4c7e263340170a71f20584d032c6e4ee3cd492f3`
прошла итоговый обычный commit-hook с полным `task verify` с первой попытки и
отправлена в `go-development`.

[CI 42365879090](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42365879090)
полностью прошёл: verify, Docker Compose smoke, Linux arm64, macOS x64/arm64,
Windows x64 и публикация контейнера — success. Новая native cleanup fixture
проверена платформенной матрицей.

## Реализация и review

- Root-specific deletion перечисляет все works по immutable `source_root_id`,
  включая detached tombstones, и их зарегистрированные artifacts. Generic
  retirement scan по-прежнему сохраняет invalidated history.
- Configuration/output-admission/source gates удерживаются до удаления строк;
  queued/running consumers, live analysis deliveries, queued/running artifact
  cleanup и outstanding cleanup claims блокируют файловый callback.
- Callback использует только `work_directory`, прочитанный тем же transaction
  под configuration gate. В первоначальной реализации ошибочно читался output;
  integration review также выявил риск connection-pool deadlock при чтении
  settings через отдельный pooled connection внутри callback. Оба места исправлены.
- Failed analysis tasks ищутся также через execution history и immutable snapshot:
  terminal settlement уже очищает root selectors, поэтому одного selector мало.
  Их exact River deliveries блокируются под row locks; running блокирует удаление,
  оставшиеся queued/retryable deliveries failed-задачи отменяются транзакционно.
- Все registered paths сверяются с root/work/artifact UUID до первого unlink,
  чтобы канонический путь чужого artifact не стал полномочием удаления.
- Существующая DB fixture расширена реальным canonical staged path, отсутствующей
  второй копией, сохранением файла в отдельном output directory и реальным
  admission → failed settlement → удаление failed task/отмена job. Отдельная БД
  или новая матрица не добавлялись. Финальное независимое review не выявило
  дополнительных ошибок; последующий полный hook прошёл в `4c7e2633`.

Файлы удаляются до commit БД; rollback сохраняет root/учёт, но не может вернуть
уже удалённую копию. При следующем явном удалении её отсутствие допустимо.
Синхронный callback удерживает coordination locks на время cleanup: это
ограниченный технический компромисс данного среза, не новый async workflow.
