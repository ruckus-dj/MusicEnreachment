import { useEffect, useRef } from "react";
import { Link } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { SourceTechnicalResult } from "./SourceTechnicalResult";
import { statusLabel } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";
import "./sources.css";

export function SourceInspectorScreen({
  sourceId,
  locationId,
}: {
  readonly sourceId: string;
  readonly locationId: string;
}) {
  const inspector = useSourceInspector(sourceId, locationId);
  const { detail, operation, loading, busy, pending, error, streamError } =
    inspector;
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  const allowed =
    detail?.root.enabled &&
    !detail.root.stale &&
    detail.root.status !== "unavailable" &&
    detail.probe_status === "audio";
  const failed = operation?.state === "failed";
  return (
    <section
      className="sources-screen sources-inspector mx-auto max-w-5xl py-3"
      aria-labelledby="inspector-title"
    >
      <nav aria-label="Путь к файлу" className="sources-breadcrumb">
        <Link href="#/sources">Источники</Link>
        <span aria-hidden="true">/</span>
        <Link href={`#/sources/${encodeURIComponent(sourceId)}`}>
          Каталог источника
        </Link>
        <span aria-hidden="true">/</span>
        <span>Файл</span>
      </nav>
      <h1 id="inspector-title" ref={heading} tabIndex={-1}>
        Инспектор файла
      </h1>
      {loading && (
        <p role="status">Загрузка файла и проверка активного анализа…</p>
      )}
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
      {error && (
        <AppButton
          isDisabled={loading || busy}
          onPress={() => void inspector.load()}
        >
          Повторить загрузку
        </AppButton>
      )}
      {detail && (
        <>
          <section className="sources-panel" aria-labelledby="identity-title">
            <h2 id="identity-title">Исходный файл</h2>
            <dl className="sources-summary">
              <div>
                <dt>Относительный путь</dt>
                <dd>
                  <code>{detail.relative_path}</code>
                </dd>
              </div>
              <div>
                <dt>Путь инвентаря</dt>
                <dd>
                  <code>{detail.root.inventory_path ?? "Неизвестно"}</code>
                </dd>
              </div>
              <div>
                <dt>Размер</dt>
                <dd>
                  {new Intl.NumberFormat("ru-RU").format(detail.size_bytes)} Б
                </dd>
              </div>
              <div>
                <dt>Изменён</dt>
                <dd>{new Date(detail.mtime).toLocaleString()}</dd>
              </div>
              <div>
                <dt>Режим источника</dt>
                <dd>Только чтение · in-place</dd>
              </div>
              <div>
                <dt>Доступность каталога</dt>
                <dd>{statusLabel(detail.root.status)}</dd>
              </div>
            </dl>
            {!detail.root.enabled && (
              <p className="sources-note">
                Каталог выключен: анализ недоступен.
              </p>
            )}
            {detail.root.stale && (
              <p className="sources-note">
                Инвентарь устарел: сначала сканируйте текущий путь каталога.
              </p>
            )}
            {detail.root.status === "unavailable" && (
              <p className="sources-note">
                Каталог недоступен. Сохранённый результат остаётся доступен.{" "}
                {detail.root.safe_error}
              </p>
            )}
            {detail.probe_status !== "audio" && (
              <p className="sources-note">
                Анализ недоступен:{" "}
                {detail.probe_status === "no_audio"
                  ? "нет аудиодорожки"
                  : "ошибка проверки"}
                . {detail.safe_error}
              </p>
            )}
          </section>
          <section className="sources-panel" aria-labelledby="analysis-title">
            <h2 id="analysis-title">Технический анализ</h2>
            <p className="sources-help">
              Запускается вручную. Повторный анализ заново читает исходный файл,
              не изменяя его.
            </p>
            <AppButton
              isDisabled={!allowed || loading || busy || pending || !!error}
              onPress={() => void inspector.action("start")}
            >
              {detail.result ? "Повторить анализ" : "Анализировать"}
            </AppButton>
            {busy && <p role="status">Выполняется запрос…</p>}
            {pending && (
              <p role="status">
                {operation?.stage === "applying"
                  ? "Сохранение результата анализа."
                  : operation?.stage === "probing"
                    ? "Чтение технических данных ffprobe."
                    : "Анализ поставлен в очередь."}
              </p>
            )}
            {operation?.state === "succeeded" && (
              <p role="status">Анализ завершён.</p>
            )}
            {failed && (
              <p role="alert">
                Ошибка анализа:{" "}
                {operation.safe_error || "Анализ не завершился."}{" "}
                {detail.result && "Предыдущий результат сохранён."}
              </p>
            )}
            {failed && (
              <AppButton
                isDisabled={loading || busy || !allowed}
                onPress={() => void inspector.action("retry")}
              >
                Повторить попытку операции
              </AppButton>
            )}
            {failed && (
              <AppButton
                isDisabled={loading || busy}
                onPress={() => void inspector.action("dismiss")}
              >
                Скрыть операцию
              </AppButton>
            )}
            {streamError && pending && (
              <p role="status">
                Поток событий прерван. Соединение восстановится автоматически;
                показан последний ответ сервера.
              </p>
            )}
            {!detail.result && (
              <p className="sources-note">Файл ещё не анализировался.</p>
            )}
          </section>
          {detail.result && <SourceTechnicalResult result={detail.result} />}
        </>
      )}
    </section>
  );
}
