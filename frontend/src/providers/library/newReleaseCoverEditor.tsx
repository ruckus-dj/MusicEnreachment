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
import { useI18n } from "../../app/i18n/host";
import {
  type LibraryReleaseText,
  LocalizedLibraryReleaseError,
  libraryReleaseError,
  libraryReleaseText,
  renderLibraryReleaseText,
} from "../../ui/features/library/libraryReleaseMessages";
import { ReleaseCoverEditorView } from "../../ui/features/library/ReleaseCoverEditorView";

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
    <ReleaseCoverEditorView
      locale={locale}
      releaseId={releaseId}
      selected={
        cover?.selected_generation
          ? {
              imageSrc: imageURL(cover.selected_generation),
              checksum: cover.selected_sha256,
            }
          : undefined
      }
      pending={
        cover?.pending_generation
          ? {
              imageSrc: imageURL(cover.pending_generation),
              checksum: cover.pending_sha256,
            }
          : undefined
      }
      fileInput={fileInput}
      busy={busy}
      savePendingDisabled={!file || busy}
      applyDisabled={!cover?.pending_generation || !epoch || busy}
      retryDisabled={!cover?.selected_generation || !epoch || busy}
      onFileChange={setFile}
      onSavePending={() => void savePending()}
      onApply={() => void apply("apply")}
      onRetry={() => void apply("retry")}
      warning={
        cover?.warning
          ? displayText(
              libraryReleaseText("coverWarning", { warning: cover.warning }),
            )
          : undefined
      }
      notice={notice ? displayText(notice) : undefined}
      error={error ? displayText(error) : undefined}
    />
  );
}
