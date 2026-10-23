import { useEffect, useRef, useState } from "react";
import {
  applyLibraryReleaseCover,
  getGetLibraryReleaseCoverImageUrl,
  getLibraryReleaseCover,
  retryLibraryReleaseCover,
  saveLibraryReleaseCover,
} from "../../api/generated/client";
import type {
  ReleaseCoverPositionOperation,
  ReleaseCoverSummary,
} from "../../api/generated/client.schemas";

type Props = {
  releaseId: string;
  epoch?: string;
  onOperationsAdmitted: (operations: ReleaseCoverPositionOperation[]) => void;
};

function errorMessage(data: unknown, status: number) {
  if (
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string" &&
    data.detail
  )
    return data.detail;
  return `Запрос не выполнен (HTTP ${status}).`;
}

export function ReleaseCoverEditor({
  releaseId,
  epoch,
  onOperationsAdmitted,
}: Props) {
  const requestScope = useRef({ releaseId, generation: 0 });
  if (requestScope.current.releaseId !== releaseId)
    requestScope.current = {
      releaseId,
      generation: requestScope.current.generation + 1,
    };
  const [cover, setCover] = useState<ReleaseCoverSummary>();
  const [file, setFile] = useState<File>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const fileInput = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const scope = requestScope.current;
    let active = true;
    setCover(undefined);
    setFile(undefined);
    setBusy(false);
    setError("");
    setNotice("");
    void getLibraryReleaseCover(releaseId, { cache: "no-store" })
      .then((response) => {
        if (!active || requestScope.current !== scope) return;
        if (response.status !== 200)
          throw new Error(errorMessage(response.data, response.status));
        setCover(response.data);
      })
      .catch((reason: unknown) => {
        if (active && requestScope.current === scope)
          setError(
            reason instanceof Error
              ? reason.message
              : "Не удалось загрузить обложку.",
          );
      });
    return () => {
      active = false;
    };
  }, [releaseId]);

  const imageURL = (generation?: string) =>
    generation
      ? getGetLibraryReleaseCoverImageUrl(releaseId, generation)
      : undefined;

  async function savePending() {
    if (!cover || !file || !["image/png", "image/jpeg"].includes(file.type))
      return;
    const scope = requestScope.current;
    const selectedFile = file;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const bytes = new Uint8Array(await selectedFile.arrayBuffer());
      if (requestScope.current !== scope) return;
      let binary = "";
      for (const byte of bytes) binary += String.fromCharCode(byte);
      const data = await saveLibraryReleaseCover(releaseId, {
        expected_revision: cover.revision,
        image: btoa(binary),
        mime: selectedFile.type,
      });
      if (requestScope.current !== scope) return;
      if (data.status !== 200)
        throw new Error(errorMessage(data.data, data.status));
      setCover(data.data);
      setFile(undefined);
      if (fileInput.current) fileInput.current.value = "";
      setNotice("Обложка сохранена как ожидающая. Публикации не изменены.");
    } catch (reason) {
      if (requestScope.current === scope)
        setError(
          reason instanceof Error
            ? reason.message
            : "Не удалось сохранить обложку.",
        );
    } finally {
      if (requestScope.current === scope) setBusy(false);
    }
  }

  async function apply(action: "apply" | "retry") {
    if (!cover || !epoch || busy) return;
    const generation =
      action === "apply" ? cover.pending_generation : cover.selected_generation;
    if (!generation) return;
    const scope = requestScope.current;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const response =
        action === "apply"
          ? await applyLibraryReleaseCover(releaseId, {
              expected_epoch: epoch,
              expected_revision: cover.revision,
              expected_pending_generation: generation,
            })
          : await retryLibraryReleaseCover(releaseId, {
              expected_epoch: epoch,
              expected_revision: cover.revision,
              expected_selected_generation: generation,
            });
      if (requestScope.current !== scope) return;
      if (response.status !== 202)
        throw new Error(errorMessage(response.data, response.status));
      const operations = response.data.operations ?? [];
      onOperationsAdmitted(operations);
      setNotice(
        `Применение запущено для ${operations.length} опубликованных треков. Текущие обложки обновятся только после проверки публикаций.`,
      );
      const refreshed = await getLibraryReleaseCover(releaseId, {
        cache: "no-store",
      });
      if (requestScope.current !== scope) return;
      if (refreshed.status !== 200)
        throw new Error(errorMessage(refreshed.data, refreshed.status));
      setCover(refreshed.data);
    } catch (reason) {
      if (requestScope.current === scope)
        setError(
          reason instanceof Error
            ? reason.message
            : "Не удалось запустить применение обложки.",
        );
    } finally {
      if (requestScope.current === scope) setBusy(false);
    }
  }

  return (
    <section
      aria-labelledby="release-cover-title"
      className="space-y-3 rounded border border-stone-300 p-4"
    >
      <h2 id="release-cover-title" className="font-semibold">
        Обложка релиза
      </h2>
      {cover?.selected_generation ? (
        <div>
          <p>Выбранная обложка</p>
          <img
            className="max-h-48"
            alt="Выбранная обложка релиза"
            src={imageURL(cover.selected_generation)}
          />
          <p>SHA-256: {cover.selected_sha256 ?? "не указан"}</p>
        </div>
      ) : (
        <p>Выбранная обложка не задана.</p>
      )}
      {cover?.pending_generation && (
        <div>
          <p>Ожидающая обложка — ещё не опубликована</p>
          <img
            className="max-h-48"
            alt="Ожидающая обложка релиза"
            src={imageURL(cover.pending_generation)}
          />
          <p>SHA-256: {cover.pending_sha256 ?? "не указан"}</p>
        </div>
      )}
      <label className="block">
        Выбрать PNG или JPEG
        <input
          key={releaseId}
          ref={fileInput}
          type="file"
          accept="image/png,image/jpeg"
          disabled={busy}
          onChange={(event) => setFile(event.target.files?.[0])}
        />
      </label>
      <button
        type="button"
        disabled={!file || busy}
        onClick={() => void savePending()}
      >
        Сохранить ожидающую обложку
      </button>
      <button
        type="button"
        disabled={!cover?.pending_generation || !epoch || busy}
        onClick={() => void apply("apply")}
      >
        Применить ко всем опубликованным трекам
      </button>
      <button
        type="button"
        disabled={!cover?.selected_generation || !epoch || busy}
        onClick={() => void apply("retry")}
      >
        Повторить применение выбранной обложки
      </button>
      {cover?.warning && (
        <p role="alert">
          Предупреждение обложки: {cover.warning}. Это не подтверждение
          успешного встраивания.
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
