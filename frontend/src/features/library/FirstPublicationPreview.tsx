import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getLibraryFirstPublicationPreview,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type {
  FirstPublicationPreviewSource,
  LibraryFirstPublicationAdmissionInputBody,
  LibraryFirstPublicationPreviewResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { firstPublicationPreviewMessages } from "./firstPublicationPreviewMessages";
import {
  LibraryPositionConfirmButton,
  LibraryPositionPreviewButton,
} from "./LibraryPositionWorkspace";
import { releaseControlFieldsetClass } from "./libraryReleaseControlStyles";
import { PublicationPreviewDetails } from "./PublicationPreviewDetails";

export type SourceChoice = Pick<
  FirstPublicationPreviewSource,
  "location_id" | "media_variant_id" | "stream_index"
>;

type PreviewWorkflow =
  | "first"
  | "comparison"
  | "retained_same_path"
  | "matching_receipt";
type PublicationMode = "ordinary" | "forced";

/**
 * Exact confirmation receipt and association that scope a publication.
 * `mediaVariantId` is never sent to the preview; it is the local proof that the
 * returned source belongs to the confirmed association.
 */
export type PublicationMatchingReceipt = {
  kind: "manual" | "local";
  requestId: string;
  trackSourceId: string;
  mediaVariantId: string;
};

export type SubmitMatchingReceiptPublication = (
  receipt: PublicationMatchingReceipt,
  body: LibraryFirstPublicationAdmissionInputBody,
) => Promise<
  Awaited<ReturnType<typeof startLibraryFirstPublication>> | undefined
>;

/**
 * A receipt-scoped preview is ordinary-only: a saved matching confirmation
 * never authorizes a forced first publication or a retained same-path rewrite.
 */
export type PublicationPreviewPrepare =
  | PublicationMode
  | {
      workflow: "matching_receipt";
      mode: "ordinary";
      receipt: PublicationMatchingReceipt;
    };

export type PublicationPreviewIdentity = {
  digest: string;
  epoch: string;
  generation?: string;
  workflow: "first" | "retained_same_path" | "matching_receipt";
  mode: PublicationMode;
  sourceChoice: string;
  receipt?: PublicationMatchingReceipt;
  refreshToken?: number | string;
};

export type PublicationPreviewState = {
  identity?: PublicationPreviewIdentity;
  canConfirm: boolean;
  loading: boolean;
  confirming: boolean;
  error: string;
  admittedOperationID: string;
  admissionUncertain: boolean;
  blockers: string[];
  confirmationCapabilityStatus?: string;
  confirmationCapabilityReasonCodes: string[];
  receipt?: PublicationMatchingReceipt;
  receiptAction?: "create" | "update" | "unchanged";
  inspection: {
    preview?: LibraryFirstPublicationPreviewResponse;
    workflow: PreviewWorkflow;
    sourceCandidates: FirstPublicationPreviewSource[];
    selectedChoice: SourceChoice | null;
    needsExplicitChoice: boolean;
    sourceChoiceDisabled: boolean;
  };
};

export type PublicationPreviewController = {
  prepare: (input: PublicationPreviewPrepare) => void;
  confirm: (expected: PublicationPreviewIdentity) => void;
  selectSource: (choice: SourceChoice | null) => void;
  refresh: () => void;
};

const zeroUUID = "00000000-0000-0000-0000-000000000000";

function choiceKey(choice: SourceChoice | null) {
  return choice
    ? `${choice.location_id}:${choice.media_variant_id}:${choice.stream_index}`
    : "unresolved";
}

function validMatchingReceipt(receipt: PublicationMatchingReceipt) {
  return (
    (receipt.kind === "manual" || receipt.kind === "local") &&
    receipt.requestId !== "" &&
    receipt.requestId !== zeroUUID &&
    receipt.trackSourceId !== "" &&
    receipt.trackSourceId !== zeroUUID &&
    receipt.mediaVariantId !== "" &&
    receipt.mediaVariantId !== zeroUUID
  );
}

function matchingReceiptKey(
  receipt: PublicationMatchingReceipt | null | undefined,
) {
  return receipt
    ? `${receipt.kind}:${receipt.requestId}:${receipt.trackSourceId}:${receipt.mediaVariantId}`
    : "";
}

function matchingReceiptPreviewParams(
  receipt: PublicationMatchingReceipt,
  choice: SourceChoice | null,
) {
  return {
    workflow: "matching_receipt",
    receipt_kind: receipt.kind,
    receipt_request_id: receipt.requestId,
    receipt_track_source_id: receipt.trackSourceId,
    mode: "ordinary",
    ...(choice
      ? {
          location_id: choice.location_id,
          media_variant_id: choice.media_variant_id,
          stream_index: String(choice.stream_index),
        }
      : {}),
  };
}

/**
 * The server capability is the sole authority for the confirmation action.
 * The FIRST branch has no capability and is a create; only the CURRENT
 * receipt branch can produce update or unchanged.
 */
function receiptConfirmationAction(
  preview: LibraryFirstPublicationPreviewResponse,
): "create" | "update" | "unchanged" | undefined {
  const capability = preview.confirmation_capability;
  if (!capability) return preview.current_comparison ? undefined : "create";
  if (capability.kind !== "matching_receipt_audio_upgrade") return undefined;
  if (capability.status === "unchanged") return "unchanged";
  if (capability.status !== "ready") return undefined;
  return "update";
}

function receiptExpectedGeneration(
  preview: LibraryFirstPublicationPreviewResponse,
) {
  const capability = preview.confirmation_capability;
  if (capability?.kind !== "matching_receipt_audio_upgrade") return undefined;
  if (capability.status !== "ready") return undefined;
  if (
    !capability.current_generation ||
    capability.current_generation === zeroUUID
  )
    return undefined;
  if (preview.current_comparison?.generation !== capability.current_generation)
    return undefined;
  return capability.current_generation;
}

function receiptProofMatches(
  preview: LibraryFirstPublicationPreviewResponse,
  receipt: PublicationMatchingReceipt | null | undefined,
) {
  return Boolean(
    receipt &&
      validMatchingReceipt(receipt) &&
      preview.selected_source &&
      preview.selected_source.media_variant_id === receipt.mediaVariantId &&
      preview.context_digest &&
      preview.epoch,
  );
}

function sameChoice(
  source: FirstPublicationPreviewSource,
  choice: SourceChoice,
) {
  return (
    source.location_id === choice.location_id &&
    source.media_variant_id === choice.media_variant_id &&
    source.stream_index === choice.stream_index
  );
}

function responseError(
  data: { detail?: string },
  status: number,
  fallback: (status: number) => string,
) {
  return data.detail || fallback(status);
}

export function FirstPublicationPreview({
  releaseId,
  positionId,
  refreshToken,
  comparisonOnly = false,
  aggregateManaged = false,
  onAdmitted,
  onRefresh,
  onController,
  onStateChange,
  onSubmitReceipt,
}: {
  releaseId: string;
  positionId: string;
  refreshToken?: number | string;
  comparisonOnly?: boolean;
  aggregateManaged?: boolean;
  onAdmitted: (operationID: string) => void;
  onRefresh: () => void;
  onController?: (controller: PublicationPreviewController | null) => void;
  onStateChange?: (state: PublicationPreviewState) => void;
  onSubmitReceipt?: SubmitMatchingReceiptPublication;
}) {
  const { locale } = useI18n();
  const copy = firstPublicationPreviewMessages[locale];
  const [preview, setPreview] =
    useState<LibraryFirstPublicationPreviewResponse>();
  const [selectedChoice, setSelectedChoice] = useState<SourceChoice | null>(
    null,
  );
  const [matchingReceipt, setMatchingReceipt] =
    useState<PublicationMatchingReceipt | null>(null);
  const [mode, setMode] = useState<PublicationMode>("ordinary");
  const [workflow, setWorkflow] = useState<PreviewWorkflow>(
    comparisonOnly ? "comparison" : "first",
  );
  const [sourceCandidates, setSourceCandidates] = useState<
    FirstPublicationPreviewSource[]
  >([]);
  const [previewChoiceKey, setPreviewChoiceKey] = useState("");
  const [previewWorkflow, setPreviewWorkflow] = useState<PreviewWorkflow>();
  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState("");
  const [admittedOperationID, setAdmittedOperationID] = useState("");
  const [admissionUncertain, setAdmissionUncertain] = useState(false);
  const aggregateStateRef = useRef<PublicationPreviewState>({
    canConfirm: false,
    loading: false,
    confirming: false,
    error: "",
    admittedOperationID: "",
    admissionUncertain: false,
    blockers: [],
    confirmationCapabilityReasonCodes: [],
    inspection: {
      workflow: comparisonOnly ? "comparison" : "first",
      sourceCandidates: [],
      selectedChoice: null,
      needsExplicitChoice: false,
      sourceChoiceDisabled: false,
    },
  });
  const aggregateConfirmRef = useRef<
    (expected: PublicationPreviewIdentity) => void
  >(() => {});
  const changeChoiceRef = useRef<(choice: SourceChoice | null) => void>(
    () => {},
  );
  const onRefreshRef = useRef(onRefresh);
  onRefreshRef.current = onRefresh;
  const confirmationPending = useRef(false);
  const admissionLocked = useRef(false);
  const previousRefreshToken = useRef(refreshToken);
  const mounted = useRef(false);
  const requestSequence = useRef(0);
  const currentIdentity = useRef({
    releaseId,
    positionId,
    choiceKey: "unresolved",
    mode: "ordinary",
    workflow: comparisonOnly ? "comparison" : "first",
    receiptKey: "",
    refreshToken,
  });
  currentIdentity.current = {
    releaseId,
    positionId,
    choiceKey: choiceKey(selectedChoice),
    mode,
    workflow,
    receiptKey: matchingReceiptKey(matchingReceipt),
    refreshToken,
  };

  useEffect(() => {
    currentIdentity.current = {
      ...currentIdentity.current,
      releaseId,
      positionId,
      choiceKey: "unresolved",
      workflow: comparisonOnly ? "comparison" : "first",
      receiptKey: "",
    };
    mounted.current = true;
    setSelectedChoice(null);
    setMatchingReceipt(null);
    setWorkflow(comparisonOnly ? "comparison" : "first");
    setSourceCandidates([]);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setLoading(false);
    setConfirming(false);
    setError("");
    setAdmittedOperationID("");
    setAdmissionUncertain(false);
    confirmationPending.current = false;
    admissionLocked.current = false;
    return () => {
      mounted.current = false;
      requestSequence.current += 1;
    };
  }, [releaseId, positionId, comparisonOnly]);

  const isCurrentRequest = useCallback(
    (
      request: number,
      requestIdentity: {
        releaseId: string;
        positionId: string;
        choiceKey: string;
        mode: string;
        workflow: PreviewWorkflow;
        receiptKey: string;
        refreshToken?: number | string;
      },
      requireToken = false,
    ) => {
      return (
        mounted.current &&
        requestSequence.current === request &&
        currentIdentity.current.releaseId === requestIdentity.releaseId &&
        currentIdentity.current.positionId === requestIdentity.positionId &&
        currentIdentity.current.choiceKey === requestIdentity.choiceKey &&
        currentIdentity.current.mode === requestIdentity.mode &&
        currentIdentity.current.workflow === requestIdentity.workflow &&
        currentIdentity.current.receiptKey === requestIdentity.receiptKey &&
        (!requireToken ||
          Object.is(
            currentIdentity.current.refreshToken,
            requestIdentity.refreshToken,
          ))
      );
    },
    [],
  );

  const loadPreview = useCallback(
    async (
      choice: SourceChoice | null,
      requestMode = mode,
      requestWorkflow = workflow,
      requestReceipt: PublicationMatchingReceipt | null = matchingReceipt,
    ) => {
      const receipt =
        requestWorkflow === "matching_receipt" ? requestReceipt : null;
      if (
        requestWorkflow === "matching_receipt" &&
        (!receipt || !validMatchingReceipt(receipt))
      ) {
        ++requestSequence.current;
        currentIdentity.current = {
          releaseId,
          positionId,
          choiceKey: choiceKey(choice),
          mode: "ordinary",
          workflow: "matching_receipt",
          receiptKey: "",
          refreshToken,
        };
        setLoading(false);
        setPreview(undefined);
        setSourceCandidates([]);
        setPreviewChoiceKey("");
        setPreviewWorkflow(undefined);
        setError(copy.sourceUnavailable);
        return;
      }
      const request = ++requestSequence.current;
      const requestIdentity = {
        releaseId,
        positionId,
        choiceKey: choiceKey(choice),
        mode: requestWorkflow === "matching_receipt" ? "ordinary" : requestMode,
        workflow: requestWorkflow,
        receiptKey: matchingReceiptKey(receipt),
        refreshToken,
      };
      currentIdentity.current = requestIdentity;
      setLoading(true);
      setError("");
      setPreview(undefined);
      setSourceCandidates([]);
      setPreviewChoiceKey("");
      try {
        const response = await getLibraryFirstPublicationPreview(
          releaseId,
          positionId,
          requestWorkflow === "matching_receipt" && receipt
            ? matchingReceiptPreviewParams(receipt, choice)
            : requestWorkflow === "retained_same_path"
              ? { workflow: "retained_same_path", mode: requestMode }
              : choice
                ? {
                    location_id: choice.location_id,
                    media_variant_id: choice.media_variant_id,
                    stream_index: String(choice.stream_index),
                    mode: requestMode,
                  }
                : { mode: requestMode },
          { cache: "no-store" },
        );
        if (!isCurrentRequest(request, requestIdentity, true)) return;
        if (response.status !== 200) {
          setError(
            response.status === 409 && choice
              ? copy.sourceUnavailable
              : response.status === 409
                ? copy.stalePreview
                : responseError(
                    response.data,
                    response.status,
                    copy.requestFailed,
                  ),
          );
          return;
        }
        if (
          receipt &&
          (response.data.release_id !== releaseId ||
            response.data.position_id !== positionId ||
            response.data.mode !== "ordinary" ||
            (response.data.selected_source &&
              response.data.selected_source.media_variant_id !==
                receipt.mediaVariantId) ||
            response.data.source_candidates?.some(
              (source) => source.media_variant_id !== receipt.mediaVariantId,
            ))
        ) {
          setError(copy.sourceUnavailable);
          return;
        }
        setPreview(response.data);
        setSourceCandidates(response.data.source_candidates ?? []);
        setPreviewChoiceKey(requestIdentity.choiceKey);
        setPreviewWorkflow(requestIdentity.workflow);
      } catch (reason) {
        if (!isCurrentRequest(request, requestIdentity, true)) return;
        setError(
          reason instanceof Error && reason.message
            ? reason.message
            : copy.previewFailed,
        );
      } finally {
        if (isCurrentRequest(request, requestIdentity, true)) setLoading(false);
      }
    },
    [
      copy,
      releaseId,
      positionId,
      mode,
      workflow,
      matchingReceipt,
      refreshToken,
      isCurrentRequest,
    ],
  );

  const prepare = useCallback(
    (input: PublicationPreviewPrepare) => {
      if (
        confirmationPending.current ||
        admissionLocked.current ||
        confirming ||
        admissionUncertain ||
        admittedOperationID
      )
        return;
      const receiptRequest = typeof input === "string" ? null : input;
      if (
        receiptRequest &&
        (receiptRequest.mode !== "ordinary" ||
          !validMatchingReceipt(receiptRequest.receipt))
      )
        return;
      const requestedMode: PublicationMode =
        typeof input === "string" ? input : "ordinary";
      const nextWorkflow: PreviewWorkflow = receiptRequest
        ? "matching_receipt"
        : comparisonOnly
          ? "retained_same_path"
          : "first";
      const nextMode =
        receiptRequest || nextWorkflow === "first" ? "ordinary" : requestedMode;
      const nextChoice =
        nextWorkflow === "first" ||
        (nextWorkflow === "matching_receipt" &&
          matchingReceiptKey(receiptRequest?.receipt) ===
            matchingReceiptKey(matchingReceipt))
          ? selectedChoice
          : null;
      const nextReceipt = receiptRequest?.receipt ?? null;
      ++requestSequence.current;
      currentIdentity.current = {
        releaseId,
        positionId,
        choiceKey: choiceKey(nextChoice),
        mode: nextMode,
        workflow: nextWorkflow,
        receiptKey: matchingReceiptKey(nextReceipt),
        refreshToken,
      };
      setMatchingReceipt(nextReceipt);
      setMode(nextMode);
      setWorkflow(nextWorkflow);
      setSelectedChoice(nextChoice);
      setPreview(undefined);
      setSourceCandidates([]);
      setPreviewChoiceKey("");
      setPreviewWorkflow(undefined);
      setLoading(false);
      setError("");
      void loadPreview(nextChoice, nextMode, nextWorkflow, nextReceipt);
    },
    [
      admittedOperationID,
      admissionUncertain,
      comparisonOnly,
      confirming,
      loadPreview,
      matchingReceipt,
      positionId,
      refreshToken,
      releaseId,
      selectedChoice,
    ],
  );

  useEffect(() => {
    if (Object.is(previousRefreshToken.current, refreshToken)) return;
    previousRefreshToken.current = refreshToken;
    // A submitted admission is frozen at its captured digest. Never invalidate
    // its request sequence or reset the uncertainty/admission fence.
    if (
      confirmationPending.current ||
      admissionLocked.current ||
      confirming ||
      admissionUncertain ||
      admittedOperationID
    )
      return;
    const hadPreviewRequest = loading || Boolean(preview);
    const choice = selectedChoice;
    ++requestSequence.current;
    setPreview(undefined);
    setSourceCandidates([]);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setLoading(false);
    setError("");
    if (hadPreviewRequest)
      void loadPreview(choice, mode, workflow, matchingReceipt);
  }, [
    refreshToken,
    confirming,
    admissionUncertain,
    admittedOperationID,
    loading,
    preview,
    selectedChoice,
    matchingReceipt,
    mode,
    workflow,
    loadPreview,
  ]);

  function changeChoice(choice: SourceChoice | null) {
    if (
      confirmationPending.current ||
      admissionLocked.current ||
      confirming ||
      admissionUncertain ||
      admittedOperationID
    )
      return;
    const nextKey = choiceKey(choice);
    ++requestSequence.current;
    currentIdentity.current = {
      releaseId,
      positionId,
      choiceKey: nextKey,
      mode,
      workflow,
      receiptKey: matchingReceiptKey(matchingReceipt),
      refreshToken,
    };
    setSelectedChoice(choice);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setLoading(false);
    setError("");
    setAdmissionUncertain(false);
    setAdmittedOperationID("");
    void loadPreview(choice);
  }
  changeChoiceRef.current = changeChoice;

  function changeMode(nextMode: "ordinary" | "forced") {
    if (
      nextMode === mode ||
      (workflow === "matching_receipt" && nextMode !== "ordinary") ||
      confirmationPending.current ||
      admissionLocked.current ||
      confirming ||
      admissionUncertain ||
      admittedOperationID
    )
      return;
    ++requestSequence.current;
    currentIdentity.current = {
      releaseId,
      positionId,
      choiceKey: choiceKey(selectedChoice),
      mode: nextMode,
      workflow,
      receiptKey: matchingReceiptKey(matchingReceipt),
      refreshToken,
    };
    setMode(nextMode);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setError("");
    void loadPreview(selectedChoice, nextMode, workflow);
  }

  function changeWorkflow(nextWorkflow: PreviewWorkflow) {
    // A receipt-scoped workflow requires an exact receipt; it is entered only
    // through prepare(), never through the comparison workflow switch.
    if (nextWorkflow === "matching_receipt") return;
    if (
      nextWorkflow === workflow ||
      confirmationPending.current ||
      admissionLocked.current ||
      confirming ||
      admissionUncertain ||
      admittedOperationID
    )
      return;
    const nextChoice = selectedChoice;
    const nextReceipt = null;
    ++requestSequence.current;
    currentIdentity.current = {
      releaseId,
      positionId,
      choiceKey: choiceKey(nextChoice),
      mode,
      workflow: nextWorkflow,
      receiptKey: "",
      refreshToken,
    };
    setMatchingReceipt(nextReceipt);
    setWorkflow(nextWorkflow);
    setSelectedChoice(nextChoice);
    setPreview(undefined);
    setSourceCandidates([]);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setLoading(false);
    setError("");
    void loadPreview(nextChoice, mode, nextWorkflow, nextReceipt);
  }

  async function confirmPreview() {
    if (
      !preview ||
      !canConfirm ||
      confirmationPending.current ||
      admissionLocked.current ||
      confirming ||
      admissionUncertain
    )
      return;
    const request = ++requestSequence.current;
    const confirmedChoice = selectedChoice;
    const confirmedReceipt =
      workflow === "matching_receipt" ? matchingReceipt : null;
    const confirmedAction =
      workflow === "matching_receipt"
        ? receiptConfirmationAction(preview)
        : undefined;
    const confirmedGeneration =
      confirmedAction === "update"
        ? receiptExpectedGeneration(preview)
        : undefined;
    if (workflow === "matching_receipt") {
      // The receipt is the only source authority for this branch. Never fall
      // back to the generic first/retained request when its proof is incomplete.
      if (
        !confirmedReceipt ||
        !receiptProofMatches(preview, confirmedReceipt) ||
        !preview.selected_source ||
        mode !== "ordinary" ||
        preview.mode !== "ordinary" ||
        (confirmedAction !== "create" && confirmedAction !== "update") ||
        (confirmedAction === "update" && !confirmedGeneration)
      )
        return;
    }
    const requestIdentity = {
      releaseId,
      positionId,
      choiceKey: choiceKey(confirmedChoice),
      mode,
      workflow,
      receiptKey: matchingReceiptKey(confirmedReceipt),
    };
    const confirmedPreview = preview;
    if (previewChoiceKey !== requestIdentity.choiceKey) return;
    confirmationPending.current = true;
    setConfirming(true);
    setError("");
    try {
      let body: LibraryFirstPublicationAdmissionInputBody;
      if (requestIdentity.workflow === "retained_same_path") {
        body = {
          workflow: "retained_same_path",
          mode,
          preview_digest: confirmedPreview.context_digest,
          expected_epoch: confirmedPreview.epoch,
          expected_generation:
            confirmedPreview.confirmation_capability?.current_generation,
        };
      } else if (requestIdentity.workflow === "matching_receipt") {
        if (!confirmedReceipt || !confirmedPreview.selected_source) return;
        body = {
          workflow: "matching_receipt",
          mode: "ordinary",
          preview_digest: confirmedPreview.context_digest,
          expected_epoch: confirmedPreview.epoch,
          ...(confirmedAction === "update" && confirmedGeneration
            ? { expected_generation: confirmedGeneration }
            : {}),
          receipt: {
            kind: confirmedReceipt.kind,
            request_id: confirmedReceipt.requestId,
            track_source_id: confirmedReceipt.trackSourceId,
          },
          // Echo the server-selected triple exactly. A resolved selection is
          // authoritative even when the host did not make an explicit choice,
          // and stream index zero is a valid exact value.
          location_id: confirmedPreview.selected_source.location_id,
          media_variant_id: confirmedPreview.selected_source.media_variant_id,
          stream_index: confirmedPreview.selected_source.stream_index,
        };
      } else {
        body = {
          preview_digest: confirmedPreview.context_digest,
          expected_epoch: confirmedPreview.epoch,
          ...(confirmedChoice && confirmedPreview.selected_source
            ? {
                location_id: confirmedPreview.selected_source.location_id,
                media_variant_id:
                  confirmedPreview.selected_source.media_variant_id,
                stream_index: confirmedPreview.selected_source.stream_index,
              }
            : {}),
        };
      }
      const response = confirmedReceipt
        ? await onSubmitReceipt?.(confirmedReceipt, body)
        : await startLibraryFirstPublication(releaseId, positionId, body);
      if (!response || !isCurrentRequest(request, requestIdentity)) return;
      if (response.status === 202) {
        admissionLocked.current = true;
        setAdmittedOperationID(response.data.id);
        setPreview(undefined);
        setSourceCandidates([]);
        onAdmitted(response.data.id);
        return;
      }
      if (response.status === 409) {
        admissionLocked.current = false;
        setPreview(undefined);
        setSourceCandidates([]);
        setError(confirmedChoice ? copy.sourceUnavailable : copy.staleConfirm);
        if (!confirmedReceipt) onRefresh();
        return;
      }
      admissionLocked.current = true;
      setAdmissionUncertain(true);
      setError(
        `${responseError(response.data, response.status, copy.requestFailed)} ${copy.unknownStart}`,
      );
    } catch (reason) {
      if (!isCurrentRequest(request, requestIdentity)) return;
      admissionLocked.current = true;
      setAdmissionUncertain(true);
      setError(
        `${reason instanceof Error && reason.message ? reason.message : copy.confirmFailed} ${copy.unknownStart}`,
      );
    } finally {
      confirmationPending.current = false;
      if (isCurrentRequest(request, requestIdentity)) setConfirming(false);
    }
  }

  aggregateConfirmRef.current = (expected) => {
    const current = aggregateStateRef.current;
    if (
      !current.canConfirm ||
      !current.identity ||
      JSON.stringify(current.identity) !== JSON.stringify(expected)
    )
      return;
    void confirmPreview();
  };

  const aggregateIdentity = useMemo<
    PublicationPreviewIdentity | undefined
  >(() => {
    if (!preview || previewWorkflow !== workflow || workflow === "comparison")
      return undefined;
    return {
      digest: preview.context_digest,
      epoch: preview.epoch,
      ...(workflow === "retained_same_path" || workflow === "matching_receipt"
        ? { generation: preview.confirmation_capability?.current_generation }
        : {}),
      workflow,
      mode,
      sourceChoice:
        workflow === "matching_receipt"
          ? choiceKey(preview.selected_source ?? null)
          : previewChoiceKey,
      ...(workflow === "matching_receipt" && matchingReceipt
        ? { receipt: matchingReceipt }
        : {}),
      refreshToken,
    };
  }, [
    preview,
    previewWorkflow,
    workflow,
    mode,
    previewChoiceKey,
    matchingReceipt,
    refreshToken,
  ]);

  const prepareRef = useRef(prepare);
  prepareRef.current = prepare;
  const controller = useMemo<PublicationPreviewController>(
    () => ({
      prepare: (requestedMode) => prepareRef.current(requestedMode),
      confirm: (expected) => aggregateConfirmRef.current(expected),
      selectSource: (choice) => changeChoiceRef.current(choice),
      refresh: () => onRefreshRef.current(),
    }),
    [],
  );

  const candidates = sourceCandidates;
  const needsExplicitChoice =
    (candidates.length > 0 && !preview?.selected_source) ||
    candidates.length > 1 ||
    candidates.some(
      (candidate) =>
        candidate.requires_explicit_stream_selection ||
        candidate.audio_stream_count !== 1,
    );
  const choiceMatchesPreview = Boolean(
    selectedChoice &&
      preview?.selected_source &&
      sameChoice(preview.selected_source, selectedChoice),
  );
  const retainedCapability = preview?.confirmation_capability;
  const retainedReady = Boolean(
    retainedCapability?.kind === "retained_same_path" &&
      retainedCapability.status === "ready" &&
      retainedCapability.current_generation !==
        "00000000-0000-0000-0000-000000000000" &&
      retainedCapability.current_generation ===
        preview?.current_comparison?.generation &&
      preview?.mode === mode,
  );
  const receiptAction =
    workflow === "matching_receipt" && preview
      ? receiptConfirmationAction(preview)
      : undefined;
  const receiptReady = Boolean(
    onSubmitReceipt &&
      preview &&
      receiptProofMatches(preview, matchingReceipt) &&
      preview.mode === "ordinary" &&
      mode === "ordinary" &&
      (!selectedChoice || choiceMatchesPreview) &&
      (receiptAction === "create" ||
        (receiptAction === "update" && receiptExpectedGeneration(preview))),
  );
  const canConfirm = Boolean(
    (workflow === "retained_same_path"
      ? retainedReady
      : workflow === "matching_receipt"
        ? receiptReady
        : !comparisonOnly && workflow === "first") &&
      preview &&
      previewWorkflow === workflow &&
      previewChoiceKey === choiceKey(selectedChoice) &&
      preview.blockers?.length === 0 &&
      (workflow === "retained_same_path" ||
        (preview.selected_source &&
          (workflow === "matching_receipt" ||
            !needsExplicitChoice ||
            choiceMatchesPreview) &&
          preview.transform &&
          preview.resolved_path_components?.length)),
  );
  const aggregateState = useMemo<PublicationPreviewState>(
    () => ({
      identity: aggregateIdentity,
      canConfirm,
      loading,
      confirming,
      error,
      admittedOperationID,
      admissionUncertain,
      blockers: preview?.blockers ?? [],
      confirmationCapabilityStatus: preview?.confirmation_capability?.status,
      confirmationCapabilityReasonCodes:
        preview?.confirmation_capability?.reason_codes ?? [],
      receipt: matchingReceipt ?? undefined,
      receiptAction,
      inspection: {
        preview,
        workflow,
        sourceCandidates,
        selectedChoice,
        needsExplicitChoice,
        sourceChoiceDisabled: Boolean(
          confirming ||
            admissionUncertain ||
            admittedOperationID ||
            confirmationPending.current ||
            admissionLocked.current,
        ),
      },
    }),
    [
      aggregateIdentity,
      canConfirm,
      loading,
      confirming,
      error,
      admittedOperationID,
      admissionUncertain,
      preview?.blockers,
      preview?.confirmation_capability?.status,
      preview?.confirmation_capability?.reason_codes,
      preview,
      workflow,
      matchingReceipt,
      receiptAction,
      sourceCandidates,
      selectedChoice,
      needsExplicitChoice,
    ],
  );
  aggregateStateRef.current = aggregateState;

  useEffect(() => {
    onStateChange?.(aggregateState);
  }, [onStateChange, aggregateState]);

  useEffect(() => {
    onController?.(controller);
    return () => onController?.(null);
  }, [onController, controller]);

  if (aggregateManaged) return null;

  return (
    <section className="min-w-0 space-y-3" aria-label={copy.section}>
      {comparisonOnly && (
        <>
          <fieldset
            className={`${releaseControlFieldsetClass} space-y-2`}
            disabled={loading}
          >
            <legend className="font-medium">{copy.currentWorkflow}</legend>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`current-workflow-${releaseId}-${positionId}`}
                checked={workflow === "comparison"}
                onChange={() => changeWorkflow("comparison")}
              />{" "}
              {copy.currentCompare}
            </label>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`current-workflow-${releaseId}-${positionId}`}
                checked={workflow === "retained_same_path"}
                onChange={() => changeWorkflow("retained_same_path")}
              />{" "}
              {copy.retainedRepublish}
            </label>
          </fieldset>
          {workflow === "comparison" && (
            <p role="note">{copy.comparisonReadOnly}</p>
          )}
          <fieldset
            className={`${releaseControlFieldsetClass} space-y-2`}
            disabled={loading}
          >
            <legend className="font-medium">{copy.mode}</legend>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`comparison-mode-${releaseId}-${positionId}`}
                checked={mode === "ordinary"}
                onChange={() => changeMode("ordinary")}
              />{" "}
              {copy.ordinary}
            </label>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`comparison-mode-${releaseId}-${positionId}`}
                checked={mode === "forced"}
                onChange={() => changeMode("forced")}
              />{" "}
              {copy.forced}
            </label>
          </fieldset>
        </>
      )}
      <LibraryPositionPreviewButton
        locale={locale}
        pending={loading}
        disabled={
          confirming ||
          Boolean(admittedOperationID) ||
          admissionUncertain ||
          confirmationPending.current ||
          admissionLocked.current
        }
        onPreview={() => void loadPreview(selectedChoice)}
      />
      <PublicationPreviewDetails
        state={aggregateState}
        sourceGroupName={`publication-source-${releaseId}-${positionId}`}
        onSourceChoice={changeChoice}
        onRefresh={onRefresh}
      />
      {!comparisonOnly && workflow === "first" && (
        <LibraryPositionConfirmButton
          locale={locale}
          label="publish"
          pending={confirming}
          disabled={
            !canConfirm ||
            admissionUncertain ||
            confirmationPending.current ||
            admissionLocked.current
          }
          onConfirm={() => void confirmPreview()}
        />
      )}
      {workflow === "retained_same_path" && (
        <LibraryPositionConfirmButton
          locale={locale}
          label="retainedRepublish"
          pending={confirming}
          disabled={
            !canConfirm ||
            admissionUncertain ||
            confirmationPending.current ||
            admissionLocked.current
          }
          onConfirm={() => void confirmPreview()}
        />
      )}
    </section>
  );
}
