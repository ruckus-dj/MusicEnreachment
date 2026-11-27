import { type ReactNode, useEffect, useRef, useState } from "react";
import {
  applyLibraryReleaseCover,
  getGetLibraryReleaseCoverImageUrl,
  getLibraryReleaseCover,
  getLibraryReleaseCoverCandidates,
  retryLibraryReleaseCover,
  saveLibraryReleaseCover,
  selectLibraryReleaseCoverCandidate,
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
import {
  ReleaseCoverEditorDataBinder,
  type ReleaseCoverEditorModel,
} from "../../ui/features/library/ReleaseCoverEditorOutlet";
import type { ReleaseCoverCandidateChoice } from "../../ui/features/library/ReleaseCoverEditorView";

type Props = {
  releaseId: string;
  epoch?: string;
  onOperationsAdmitted: (operations: ReleaseCoverPositionOperation[]) => void;
  onCoverSnapshot?: (releaseId: string, selectedGeneration?: string) => void;
  children: ReactNode;
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

type CandidateLoad =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "pending" }
  | { status: "loaded"; items: ReleaseCoverCandidateChoice[] }
  | { status: "error"; text: LibraryReleaseText };

type RequestScope = { releaseId: string; generation: number };

function candidateSelectionNotice(
  summary: ReleaseCoverSummary,
): LibraryReleaseText {
  if (summary.pending_generation)
    return libraryReleaseText("coverCandidatePendingSaved");
  if (summary.selected_generation)
    return libraryReleaseText("coverCandidateSelected");
  return libraryReleaseText("coverCandidateSaved");
}

export function ReleaseCoverEditor({
  releaseId,
  epoch,
  onOperationsAdmitted,
  onCoverSnapshot,
  children,
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
  const [candidateLoad, setCandidateLoad] = useState<CandidateLoad>({
    status: "idle",
  });
  const [selectingCandidateID, setSelectingCandidateID] = useState<string>();
  const [error, setError] = useState<LibraryReleaseText | "">("");
  const [notice, setNotice] = useState<LibraryReleaseText | "">("");
  const fileInput = useRef<HTMLInputElement>(null);
  const candidateRequest = useRef(0);
  const onCoverSnapshotRef = useRef(onCoverSnapshot);
  useEffect(() => {
    onCoverSnapshotRef.current = onCoverSnapshot;
  }, [onCoverSnapshot]);

  useEffect(() => {
    const scope = requestScope.current;
    let active = true;
    candidateRequest.current += 1;
    setCover(undefined);
    setFile(undefined);
    setBusy(false);
    setCandidateLoad({ status: "idle" });
    setSelectingCandidateID(undefined);
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

  async function loadCandidates() {
    const request = ++candidateRequest.current;
    const scope = requestScope.current;
    setCandidateLoad({ status: "loading" });
    try {
      const response = await getLibraryReleaseCoverCandidates(releaseId, {
        cache: "no-store",
      });
      if (
        candidateRequest.current !== request ||
        requestScope.current !== scope
      )
        return;
      if (response.status !== 200) {
        if (response.status === 503) {
          setCandidateLoad({ status: "pending" });
          return;
        }
        throw errorMessage(response.data, response.status);
      }
      const items = Array.isArray(response.data.items)
        ? response.data.items
        : [];
      setCandidateLoad({
        status: "loaded",
        items: items.map((item) => ({
          id: item.id,
          front: item.front,
          back: item.back,
          types: item.types ?? [],
          comment: item.comment,
          thumbnailSrc: item.thumbnail_url,
        })),
      });
    } catch (reason) {
      if (
        candidateRequest.current === request &&
        requestScope.current === scope
      )
        setCandidateLoad({
          status: "error",
          text: libraryReleaseError(reason, "loadCoverCandidatesFailed"),
        });
    }
  }

  function revealCandidates() {
    if (candidateLoad.status === "idle") void loadCandidates();
  }

  function retryCandidates() {
    if (candidateLoad.status !== "loading") void loadCandidates();
  }

  async function refreshCoverAuthority(scope: RequestScope): Promise<boolean> {
    const refreshed = await getLibraryReleaseCover(releaseId, {
      cache: "no-store",
    });
    if (requestScope.current !== scope) return false;
    if (refreshed.status !== 200)
      throw errorMessage(refreshed.data, refreshed.status);
    setCover(refreshed.data);
    onCoverSnapshot?.(releaseId, refreshed.data.selected_generation);
    return true;
  }

  async function selectCandidate(candidateID: string) {
    if (!cover || busy) return;
    const scope = requestScope.current;
    setBusy(true);
    setSelectingCandidateID(candidateID);
    setError("");
    setNotice("");
    try {
      const response = await selectLibraryReleaseCoverCandidate(
        releaseId,
        candidateID,
        { expected_revision: cover.revision },
      );
      if (requestScope.current !== scope) return;
      if (response.status === 409) {
        try {
          if (!(await refreshCoverAuthority(scope))) return;
        } catch (reason) {
          if (requestScope.current === scope)
            setError(
              libraryReleaseError(reason, "coverCandidateRefreshFailed"),
            );
          return;
        }
        if (requestScope.current !== scope) return;
        setError(libraryReleaseText("coverCandidateConflict"));
        return;
      }
      if (response.status !== 200)
        throw errorMessage(response.data, response.status);
      setCover(response.data);
      onCoverSnapshot?.(releaseId, response.data.selected_generation);
      setNotice(candidateSelectionNotice(response.data));
    } catch (reason) {
      if (requestScope.current === scope)
        setError(libraryReleaseError(reason, "selectCoverCandidateFailed"));
    } finally {
      if (requestScope.current === scope) {
        setBusy(false);
        setSelectingCandidateID(undefined);
      }
    }
  }

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

  const model: ReleaseCoverEditorModel = {
    releaseId,
    selected: cover?.selected_generation
      ? {
          imageSrc: imageURL(cover.selected_generation),
          checksum: cover.selected_sha256,
        }
      : undefined,
    pending: cover?.pending_generation
      ? {
          imageSrc: imageURL(cover.pending_generation),
          checksum: cover.pending_sha256,
        }
      : undefined,
    fileInput,
    busy,
    savePendingDisabled: !file || busy,
    applyDisabled: !cover?.pending_generation || !epoch || busy,
    retryDisabled: !cover?.selected_generation || !epoch || busy,
    onFileChange: setFile,
    onSavePending: () => void savePending(),
    onApply: () => void apply("apply"),
    onRetry: () => void apply("retry"),
    candidates: {
      status: candidateLoad.status,
      items: candidateLoad.status === "loaded" ? candidateLoad.items : [],
      error:
        candidateLoad.status === "error"
          ? displayText(candidateLoad.text)
          : undefined,
      selectingId: selectingCandidateID,
      disabled: busy || !cover,
      onReveal: revealCandidates,
      onRetry: retryCandidates,
      onSelect: (candidateID) => void selectCandidate(candidateID),
    },
    warning: cover?.warning
      ? displayText(
          libraryReleaseText("coverWarning", { warning: cover.warning }),
        )
      : undefined,
    notice: notice ? displayText(notice) : undefined,
    error: error ? displayText(error) : undefined,
  };

  return (
    <ReleaseCoverEditorDataBinder model={model}>
      {children}
    </ReleaseCoverEditorDataBinder>
  );
}
