import { routePath } from "../routing";
import type { WorkerQueue } from "../types";

type WorkerQueueScreenProps = {
  readonly queue: WorkerQueue | null;
  readonly loading: boolean;
  readonly error: string;
  readonly onRefresh: () => void;
};

function jobLabel(kind: string): string {
  const labels: Record<string, string> = {
    acoustid_analysis: "Анализ AcousticID",
    filesystem_scan: "Проверка источника",
    final_publish: "Публикация Final",
    musicbrainz_analysis: "Анализ MusicBrainz",
    musicbrainz_refresh: "Обновление подтверждённой пары MusicBrainz",
    reconciliation_scan: "Сверка источников",
    selection_refresh: "Обновление выбора",
    artwork_enrichment: "Обогащение обложкой",
  };
  return labels[kind] ?? kind.replaceAll("_", " ");
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat("ru-RU", {
    dateStyle: "short",
    timeStyle: "medium",
  }).format(new Date(value));
}

function slotLabel(state: string): string {
  const labels: Record<string, string> = {
    disabled: "Отключён",
    error: "Ошибка цикла",
    idle: "Ожидает задачу",
    processing: "Обрабатывает",
  };
  return labels[state] ?? state;
}

export function WorkerQueueScreen({ queue, loading, error, onRefresh }: WorkerQueueScreenProps) {
  const visibleSlots = queue?.worker.slots.filter((slot) => slot.state !== "disabled") ?? [];

  return (
    <section className="worker-queue-screen" aria-labelledby="worker-queue-heading">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Фоновая обработка</p>
          <h2 id="worker-queue-heading">Очередь worker’ов</h2>
        </div>
        <button type="button" className="secondary" disabled={loading} onClick={onRefresh}>
          {loading ? "Обновляем…" : "Обновить"}
        </button>
      </div>
      {error ? (
        <div className="worker-queue-error" role="alert">
          {error}
        </div>
      ) : null}
      {queue ? (
        <>
          <section className="worker-summary" aria-label="Состояние обработки">
            <div>
              <span>Состояние worker’ов</span>
              <strong className={`worker-status ${queue.worker.liveness}`}>
                {queue.worker.liveness === "available" ? "Наблюдается" : "Недоступно"}
              </strong>
              <small>
                Активных слотов: {visibleSlots.filter((slot) => slot.state === "processing").length}{" "}
                из {queue.worker.configured_concurrency}.
              </small>
            </div>
            <div>
              <span>Выполняется</span>
              <strong>{queue.summary.running}</strong>
            </div>
            <div>
              <span>Готово к запуску</span>
              <strong>{queue.summary.ready}</strong>
            </div>
            <div>
              <span>Ожидают повтор</span>
              <strong>{queue.summary.retry_wait}</strong>
            </div>
          </section>
          {queue.worker.liveness === "available" ? (
            <section className="worker-slots" aria-labelledby="worker-slots-heading">
              <h3 id="worker-slots-heading">Слоты worker’ов</h3>
              <ul>
                {visibleSlots.map((slot) => (
                  <li key={slot.slot}>
                    <strong>#{slot.slot + 1}</strong>
                    <span className={`worker-slot-state ${slot.state}`}>
                      {slot.job_kind
                        ? `${slotLabel(slot.state)}: ${jobLabel(slot.job_kind)}`
                        : slotLabel(slot.state)}
                    </span>
                    <small>{formatDate(slot.observed_at)}</small>
                    {slot.error ? <small role="alert">{slot.error}</small> : null}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}
          {queue.jobs.length === 0 ? (
            <div className="empty-state">Активных задач нет. Worker ожидает следующую задачу.</div>
          ) : (
            <ul className="worker-job-list" aria-label="Активные задания">
              {queue.jobs.map((job) => (
                <li className="worker-job" key={job.job_id}>
                  <div className="worker-job-state">
                    <span
                      className={job.state === "running" ? "job-state running" : "job-state queued"}
                    >
                      {job.state === "running"
                        ? "Выполняется"
                        : job.queue_state === "retry_wait"
                          ? "Повтор позже"
                          : "В очереди"}
                    </span>
                    <span>{job.attempt_count} попыт.</span>
                  </div>
                  <div className="worker-job-main">
                    <strong>{jobLabel(job.kind)}</strong>
                    {job.target ? (
                      <a
                        className="worker-track-link"
                        aria-label={`Открыть инспектор трека: ${job.target.title}`}
                        href={routePath({
                          screen: "track",
                          recordId: job.target.record_id,
                          sourceId: job.target.source_id,
                        })}
                      >
                        <span>{job.target.title}</span>
                        <small>
                          {[job.target.artist, job.target.album].filter(Boolean).join(" · ") ||
                            job.target.path}
                        </small>
                      </a>
                    ) : (
                      <small>Общая задача библиотеки</small>
                    )}
                  </div>
                  <dl className="worker-job-times">
                    <div>
                      <dt>Создана</dt>
                      <dd>{formatDate(job.created_at)}</dd>
                    </div>
                    {job.next_attempt_at ? (
                      <div>
                        <dt>Не ранее</dt>
                        <dd>{formatDate(job.next_attempt_at)}</dd>
                      </div>
                    ) : null}
                  </dl>
                </li>
              ))}
            </ul>
          )}
          {queue.total_jobs > queue.jobs.length ? (
            <p className="worker-observed">
              Показаны первые {queue.jobs.length} из {queue.total_jobs} активных задач.
            </p>
          ) : null}
          <p className="worker-observed">
            Обновляется каждые 2 секунды, пока открыта страница. Снимок:{" "}
            {formatDate(queue.observed_at)}
          </p>
        </>
      ) : !loading ? (
        <div className="empty-state">Данные очереди пока не загружены.</div>
      ) : null}
    </section>
  );
}
