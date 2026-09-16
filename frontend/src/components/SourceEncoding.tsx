import { useEffect, useRef, useState } from "react";
import "./sourceEncoding.css";
import {
  ApiError,
  applySourceEncoding,
  getSourceEncoding,
  previewSourceEncoding,
} from "../api/client";
import {
  ENCODING_CODECS,
  type EncodingChoice,
  type EncodingCodec,
  type EncodingDetail,
  type EncodingPreview,
  isEncodingPreview,
} from "../domain/sourceEncoding";

function errorText(error: unknown): string {
  if (error instanceof ApiError && error.status === 409)
    return error.message === "source_processing_busy"
      ? "Источник обрабатывается. Дождитесь завершения и обновите поля."
      : "Ревизия источника изменилась. Обновите поля и повторите выбор.";
  return error instanceof Error ? error.message : "Не удалось проверить кодировки.";
}

export function SourceEncoding({
  sourceId,
  onApplied,
  disabled = false,
}: {
  readonly sourceId: string;
  readonly onApplied: (queued: boolean) => Promise<void>;
  readonly disabled?: boolean;
}) {
  // A keyed session prevents choices and outstanding requests crossing sources.
  return (
    <EncodingSession key={sourceId} sourceId={sourceId} onApplied={onApplied} disabled={disabled} />
  );
}

function EncodingSession({
  sourceId,
  onApplied,
  disabled,
}: {
  readonly sourceId: string;
  readonly onApplied: (queued: boolean) => Promise<void>;
  readonly disabled: boolean;
}) {
  const [detail, setDetail] = useState<EncodingDetail | null>(null);
  const [choices, setChoices] = useState<EncodingChoice[]>([]);
  const [preview, setPreview] = useState<EncodingPreview | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(true);
  const [applying, setApplying] = useState(false);
  const [reload, setReload] = useState(0);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setDetail(null);
    setChoices([]);
    setPreview(null);
    setError("");
    getSourceEncoding(sourceId, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) setDetail(value);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(errorText(reason));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [sourceId, reload]);

  useEffect(() => {
    if (!detail || choices.length === 0) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      previewSourceEncoding(
        sourceId,
        { expected_revision: detail.source_revision, choices },
        controller.signal,
      )
        .then((value) => {
          if (!controller.signal.aborted) {
            if (value.source_id !== sourceId || value.source_revision !== detail.source_revision)
              setError("Ревизия источника изменилась. Обновите поля.");
            else setPreview(value);
          }
        })
        .catch((reason: unknown) => {
          if (!controller.signal.aborted) setError(errorText(reason));
        });
    }, 180);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [sourceId, detail, choices]);

  function change(choice: EncodingChoice) {
    setPreview(null);
    setError("");
    setMessage("");
    setChoices((current) => [
      ...current.filter((item) => item.field_id !== choice.field_id),
      choice,
    ]);
  }
  async function apply() {
    if (!detail || !preview?.valid || error || applying || disabled) return;
    setApplying(true);
    setError("");
    let saved = false;
    try {
      const result = await applySourceEncoding(sourceId, {
        expected_revision: detail.source_revision,
        choices,
      });
      saved = true;
      if (!alive.current) return;
      setPreview(null);
      setChoices([]);
      const readback = await getSourceEncoding(sourceId);
      if (!alive.current) return;
      setDetail(readback);
      await onApplied(result.queued);
      if (!alive.current) return;
      setMessage(
        result.queued
          ? "Сохранено. MusicBrainz и последующая обработка поставлены в очередь; AcousticID не запускается."
          : "Сохранено. Текст не изменился, повторная обработка не нужна.",
      );
    } catch (reason) {
      if (!alive.current) return;
      if (reason instanceof ApiError && isEncodingPreview(reason.detail)) setPreview(reason.detail);
      else setPreview(null);
      setError(
        saved
          ? "Изменения сохранены, но обновление данных не удалось. Обновите поля и страницу; не применяйте повторно."
          : errorText(reason),
      );
    } finally {
      if (alive.current) setApplying(false);
    }
  }

  function table(fields: EncodingDetail["fields"], label: string) {
    return (
      <div className="encoding-table-scroll">
        <table className="encoding-table">
          <caption>{label}</caption>
          <thead>
            <tr>
              <th scope="col">Поле</th>
              <th scope="col">Сейчас</th>
              <th scope="col">Предпросмотр</th>
              <th scope="col">Кодировка</th>
            </tr>
          </thead>
          <tbody>
            {fields.map((field) => {
              const identity = `${field.tag_name} · ${field.container} · ${field.physical_id ?? "legacy"} · #${field.field_id}`;
              const chosen = choices.find((item) => item.field_id === field.field_id);
              const choice = chosen ??
                field.applied_choice ?? { field_id: field.field_id, mode: "original" as const };
              const result = preview?.fields.find((item) => item.field_id === field.field_id);
              return (
                <tr key={field.field_id}>
                  <th scope="row" title={identity}>
                    {field.tag_name}
                  </th>
                  <td>{field.current_value}</td>
                  <td aria-live="polite">
                    <p>{result ? (result.value ?? "—") : chosen ? "Проверяем…" : "—"}</p>
                    {result?.error && <p role="alert">Ошибка поля: {result.error}</p>}
                    {chosen && <small>Предпросмотр · не сохранено</small>}
                  </td>
                  <td className="encoding-controls">
                    {field.decision_origin === "auto" && !chosen && <small>Авто</small>}
                    <label>
                      <span className="encoding-sr-label">Кодировка {identity}</span>
                      <select
                        disabled={applying || disabled}
                        value={choice.decode_codec ?? "original"}
                        onChange={(event) =>
                          change(
                            event.target.value === "original"
                              ? { field_id: field.field_id, mode: "original" }
                              : {
                                  field_id: field.field_id,
                                  mode: "codec",
                                  decode_codec: event.target.value as EncodingCodec,
                                },
                          )
                        }
                      >
                        <option value="original">
                          {field.declared_codec?.toUpperCase() ?? "Неизвестна"} (Исходная)
                        </option>
                        {ENCODING_CODECS.map((codec) => (
                          <option key={codec} value={codec}>
                            {codec.toUpperCase()}
                          </option>
                        ))}
                      </select>
                    </label>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }

  return (
    <section className="evidence-card source-encoding" aria-labelledby="source-encoding-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Source · интерпретация тегов</p>
          <h2 id="source-encoding-title">Кодировки исходных полей</h2>
        </div>
        {detail && <span className="badge">Source rev {detail.source_revision}</span>}
      </div>
      <p>
        Выбор меняет только прочтение исходных тегов в базе, не файл и не редактор Final. Все
        выбранные поля применяются вместе после проверки.
      </p>
      {loading && <p role="status">Загружаем исходные поля…</p>}
      {detail?.fields.length === 0 && <p>Исходные текстовые поля не сохранены.</p>}
      {detail &&
        table(
          detail.fields.filter((field) => field.selected),
          "Исходные поля",
        )}
      {detail?.fields.some((field) => !field.selected) && (
        <details className="encoding-shadows">
          <summary>
            Теневые физические поля ({detail.fields.filter((field) => !field.selected).length})
          </summary>
          {table(
            detail.fields.filter((field) => !field.selected),
            "Теневые поля",
          )}
        </details>
      )}
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      <div className="provider-actions">
        <button
          type="button"
          className="primary"
          disabled={
            loading || applying || disabled || choices.length === 0 || !preview?.valid || !!error
          }
          onClick={() => void apply()}
        >
          {applying ? "Применяем и обновляем…" : "Применить кодировки"}
        </button>
        <button
          type="button"
          className="secondary"
          disabled={applying || loading}
          onClick={() => {
            setMessage("");
            setReload((value) => value + 1);
          }}
        >
          Отменить выбор / обновить поля
        </button>
      </div>
    </section>
  );
}
