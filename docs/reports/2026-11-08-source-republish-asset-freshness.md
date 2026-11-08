# Source republish: asset authority before installation

База `a3b935f3` прошла полный hook и всю CI 42485144868, включая контейнер.

Guarded preparation и result-current reader теперь проверяют raw lyrics authority
и selected cover authority, а не только CURRENT/source authority. Result использует
тот же comparator. Freshness не материализует cover rows: SHARE table guard
сериализует implicit absent против вставки; result сохраняет materialization/row
lock protocol. Pending/acquisition conservatively отвергается без promotion.

Grouped DB fixture восстановлена между изменениями lyrics, cover и source
authority: каждый отказ сначала имеет успешный baseline обоих readers, чтобы
другая оставшаяся mutation не маскировала проверяемый guard.

Независимое review проверило lock order существующих cover writers. Отдельный
эксперимент typed admission удалён до commit: он содержал неиспользуемый private
путь и поздние blanket locks с риском inversion. Он не является частью среза.

Нового enqueue/executor/public confirmation нет; v4 SupportsExecution false и
generic admission закрыт. Полный hook/CI этого среза ожидаются.
