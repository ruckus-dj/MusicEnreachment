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
import { useI18n } from "../../i18n/I18nProvider";
import {
  releaseControlAlertClass,
  releaseControlButtonClass,
} from "./libraryReleaseControlStyles";
import {
  type LibraryReleaseText,
  LocalizedLibraryReleaseError,
  libraryReleaseError,
  libraryReleaseMessage,
  libraryReleaseText,
  renderLibraryReleaseText,
} from "./libraryReleaseMessages";

type Props = {
  releaseId: string;
  epoch?: string;
  onOperationsAdmitted: (operations: ReleaseCoverPositionOperation[]) => void;
  onCoverSnapshot?: (releaseId: string, selectedGeneration?: string) => void;
};

function errorMessage(data: unknown, status: number): Error {
  const detail =
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string"
      ? data.detail
      : "";
  if (detail) return new Error(detail);
  return new LocalizedLibraryReleaseError(
    libraryReleaseText("requestFailed", { status }),
  );
}

export function ReleaseCoverEditor({
  releaseId,
  epoch,
  onOperationsAdmitted,
  onCoverSnapshot,
}: Props) {
  const { locale } = useI18n();
  const text = (
    key: Parameters<typeof libraryReleaseMessage>[1],
    values?: Record<string, string | number>,
  ) => libraryReleaseMessage(locale, key, values);
  const displayText = (value: LibraryReleaseText) =>
    renderLibraryReleaseText(locale, value);
  const requestScope = useRef({ releaseId, generation: 0 });
  if (requestScope.current.releaseId !== releaseId)
    requestScope.current = {
      releaseId,
      generation: requestScope.current.generation + 1,
    };
  const [cover, setCover] = useState<ReleaseCoverSummary>();
  const [file, setFile] = useState<File>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<LibraryReleaseText | "">("");
  const [notice, setNotice] = useState<LibraryReleaseText | "">("");
  const fileInput = useRef<HTMLInputElement>(null);
  const onCoverSnapshotRef = useRef(onCoverSnapshot);
  useEffect(() => {
    onCoverSnapshotRef.current = onCoverSnapshot;
  }, [onCoverSnapshot]);

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
          throw errorMessage(response.data, response.status);
        setCover(response.data);
        onCoverSnapshotRef.current?.(
          releaseId,
          response.data.selected_generation,
        );
      })
      .catch((reason: unknown) => {
        if (active && requestScope.current === scope)
          setError(libraryReleaseError(reason, "loadCoverFailed"));
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
      if (data.status !== 200) throw errorMessage(data.data, data.status);
      setCover(data.data);
      onCoverSnapshot?.(releaseId, data.data.selected_generation);
      setFile(undefined);
      if (fileInput.current) fileInput.current.value = "";
      setNotice(libraryReleaseText("coverSaved"));
    } catch (reason) {
      if (requestScope.current === scope)
        setError(libraryReleaseError(reason, "saveCoverFailed"));
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
        throw errorMessage(response.data, response.status);
      const operations = response.data.operations ?? [];
      onOperationsAdmitted(operations);
      setNotice(
        libraryReleaseText("coverApplyStarted", { count: operations.length }),
      );
      const refreshed = await getLibraryReleaseCover(releaseId, {
        cache: "no-store",
      });
      if (requestScope.current !== scope) return;
      if (refreshed.status !== 200)
        throw errorMessage(refreshed.data, refreshed.status);
      setCover(refreshed.data);
      onCoverSnapshot?.(releaseId, refreshed.data.selected_generation);
    } catch (reason) {
      if (requestScope.current === scope)
        setError(libraryReleaseError(reason, "applyCoverFailed"));
    } finally {
      if (requestScope.current === scope) setBusy(false);
    }
  }

  return (
    <section
      aria-labelledby="release-cover-title"
      className="space-y-3 rounded border border-[var(--line)] bg-surface p-4"
    >
      <h2
        id="release-cover-title"
        className="text-base leading-[22px] font-semibold"
      >
        {text("releaseCover")}
      </h2>
      {cover?.selected_generation ? (
        <div>
          <p>{text("selectedCover")}</p>
          <img
            className="max-h-48"
            alt={text("selectedReleaseCover")}
            src={imageURL(cover.selected_generation)}
          />
          <p className="[overflow-wrap:anywhere]">
            SHA-256: {cover.selected_sha256 ?? text("unspecified")}
          </p>
        </div>
      ) : (
        <p>{text("noSelectedCover")}</p>
      )}
      {cover?.pending_generation && (
        <div>
          <p>{text("pendingCover")}</p>
          <img
            className="max-h-48"
            alt={text("pendingReleaseCover")}
            src={imageURL(cover.pending_generation)}
          />
          <p className="[overflow-wrap:anywhere]">
            SHA-256: {cover.pending_sha256 ?? text("unspecified")}
          </p>
        </div>
      )}
      <label className="block">
        {text("choosePngJpeg")}
        <input
          key={releaseId}
          ref={fileInput}
          type="file"
          className="max-w-full rounded bg-raised text-ink max-[599px]:min-h-10"
          accept="image/png,image/jpeg"
          disabled={busy}
          onChange={(event) => setFile(event.target.files?.[0])}
        />
      </label>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={!file || busy}
        onClick={() => void savePending()}
      >
        {text("savePendingCover")}
      </button>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={!cover?.pending_generation || !epoch || busy}
        onClick={() => void apply("apply")}
      >
        {text("applyCoverToTracks")}
      </button>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={!cover?.selected_generation || !epoch || busy}
        onClick={() => void apply("retry")}
      >
        {text("retrySelectedCover")}
      </button>
      {cover?.warning && (
        <p role="alert" className={releaseControlAlertClass}>
          {text("coverWarning", { warning: cover.warning })}
        </p>
      )}
      {notice && <p role="status">{displayText(notice)}</p>}
      {error && (
        <p role="alert" className={releaseControlAlertClass}>
          {displayText(error)}
        </p>
      )}
    </section>
  );
}
