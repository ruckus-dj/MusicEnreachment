import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  getLibraryFirstPublicationPreview,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type {
  FirstPublicationPreviewSource,
  LibraryFirstPublicationPreviewResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { firstPublicationPreviewMessages } from "./firstPublicationPreviewMessages";
import { PublicationPreviewDetails } from "./PublicationPreviewDetails";

export type SourceChoice = Pick<
  FirstPublicationPreviewSource,
  "location_id" | "media_variant_id" | "stream_index"
>;

type PreviewWorkflow = "first" | "comparison" | "retained_same_path";
type PublicationMode = "ordinary" | "forced";

export type PublicationPreviewIdentity = {
  digest: string;
  epoch: string;
  generation?: string;
  workflow: "first" | "retained_same_path";
  mode: PublicationMode;
  sourceChoice: string;
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
  prepare: (mode: PublicationMode) => void;
  confirm: (expected: PublicationPreviewIdentity) => void;
  selectSource: (choice: SourceChoice | null) => void;
  refresh: () => void;
};

function choiceKey(choice: SourceChoice | null) {
  return choice
    ? `${choice.location_id}:${choice.media_variant_id}:${choice.stream_index}`
    : "unresolved";
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
}) {
  const { locale } = useI18n();
  const copy = firstPublicationPreviewMessages[locale];
  const [preview, setPreview] =
    useState<LibraryFirstPublicationPreviewResponse>();
  const [selectedChoice, setSelectedChoice] = useState<SourceChoice | null>(
    null,
  );
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
    refreshToken,
  });
  currentIdentity.current = {
    releaseId,
    positionId,
    choiceKey: choiceKey(selectedChoice),
    mode,
    workflow,
    refreshToken,
  };

  useEffect(() => {
    currentIdentity.current = {
      ...currentIdentity.current,
      releaseId,
      positionId,
      choiceKey: "unresolved",
      workflow: comparisonOnly ? "comparison" : "first",
    };
    mounted.current = true;
    setSelectedChoice(null);
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
    ) => {
      const request = ++requestSequence.current;
      const requestIdentity = {
        releaseId,
        positionId,
        choiceKey: choiceKey(choice),
        mode: requestMode,
        workflow: requestWorkflow,
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
          requestWorkflow === "retained_same_path"
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
      refreshToken,
      isCurrentRequest,
    ],
  );

  const prepare = useCallback(
    (requestedMode: PublicationMode) => {
      if (
        confirmationPending.current ||
        admissionLocked.current ||
        confirming ||
        admissionUncertain ||
        admittedOperationID
      )
        return;
      const nextWorkflow: PreviewWorkflow = comparisonOnly
        ? "retained_same_path"
        : "first";
      const nextMode = nextWorkflow === "first" ? "ordinary" : requestedMode;
      const nextChoice = nextWorkflow === "first" ? selectedChoice : null;
      ++requestSequence.current;
      currentIdentity.current = {
        releaseId,
        positionId,
        choiceKey: choiceKey(nextChoice),
        mode: nextMode,
        workflow: nextWorkflow,
        refreshToken,
      };
      setMode(nextMode);
      setWorkflow(nextWorkflow);
      setSelectedChoice(nextChoice);
      setPreview(undefined);
      setSourceCandidates([]);
      setPreviewChoiceKey("");
      setPreviewWorkflow(undefined);
      setLoading(false);
      setError("");
      void loadPreview(nextChoice, nextMode, nextWorkflow);
    },
    [
      admittedOperationID,
      admissionUncertain,
      comparisonOnly,
      confirming,
      loadPreview,
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
    if (hadPreviewRequest) void loadPreview(choice, mode, workflow);
  }, [
    refreshToken,
    confirming,
    admissionUncertain,
    admittedOperationID,
    loading,
    preview,
    selectedChoice,
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
    ++requestSequence.current;
    currentIdentity.current = {
      releaseId,
      positionId,
      choiceKey: choiceKey(nextChoice),
      mode,
      workflow: nextWorkflow,
      refreshToken,
    };
    setWorkflow(nextWorkflow);
    setSelectedChoice(nextChoice);
    setPreview(undefined);
    setSourceCandidates([]);
    setPreviewChoiceKey("");
    setPreviewWorkflow(undefined);
    setLoading(false);
    setError("");
    void loadPreview(nextChoice, mode, nextWorkflow);
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
    const requestIdentity = {
      releaseId,
      positionId,
      choiceKey: choiceKey(confirmedChoice),
      mode,
      workflow,
    };
    const confirmedPreview = preview;
    if (previewChoiceKey !== requestIdentity.choiceKey) return;
    confirmationPending.current = true;
    setConfirming(true);
    setError("");
    try {
      const retained = requestIdentity.workflow === "retained_same_path";
      const body = retained
        ? {
            workflow: "retained_same_path",
            mode,
            preview_digest: confirmedPreview.context_digest,
            expected_epoch: confirmedPreview.epoch,
            expected_generation:
              confirmedPreview.confirmation_capability?.current_generation,
          }
        : {
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
      const response = await startLibraryFirstPublication(
        releaseId,
        positionId,
        body,
      );
      if (!isCurrentRequest(request, requestIdentity)) return;
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
        onRefresh();
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
      ...(workflow === "retained_same_path"
        ? { generation: preview.confirmation_capability?.current_generation }
        : {}),
      workflow,
      mode,
      sourceChoice: previewChoiceKey,
      refreshToken,
    };
  }, [
    preview,
    previewWorkflow,
    workflow,
    mode,
    previewChoiceKey,
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
  const canConfirm = Boolean(
    (workflow === "retained_same_path"
      ? retainedReady
      : !comparisonOnly && workflow === "first") &&
      preview &&
      previewWorkflow === workflow &&
      previewChoiceKey === choiceKey(selectedChoice) &&
      preview.blockers?.length === 0 &&
      (workflow === "retained_same_path" ||
        (preview.selected_source &&
          (!needsExplicitChoice || choiceMatchesPreview) &&
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
    <section
      className="space-y-3 rounded border border-stone-300 p-4"
      aria-label={copy.section}
    >
      {comparisonOnly && (
        <>
          <fieldset className="space-y-2" disabled={loading}>
            <legend className="font-medium">{copy.currentWorkflow}</legend>
            <label className="block">
              <input
                type="radio"
                name={`current-workflow-${releaseId}-${positionId}`}
                checked={workflow === "comparison"}
                onChange={() => changeWorkflow("comparison")}
              />{" "}
              {copy.currentCompare}
            </label>
            <label className="block">
              <input
                type="radio"
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
          <fieldset className="space-y-2" disabled={loading}>
            <legend className="font-medium">{copy.mode}</legend>
            <label className="block">
              <input
                type="radio"
                name={`comparison-mode-${releaseId}-${positionId}`}
                checked={mode === "ordinary"}
                onChange={() => changeMode("ordinary")}
              />{" "}
              {copy.ordinary}
            </label>
            <label className="block">
              <input
                type="radio"
                name={`comparison-mode-${releaseId}-${positionId}`}
                checked={mode === "forced"}
                onChange={() => changeMode("forced")}
              />{" "}
              {copy.forced}
            </label>
          </fieldset>
        </>
      )}
      <button
        type="button"
        disabled={
          loading ||
          confirming ||
          Boolean(admittedOperationID) ||
          admissionUncertain ||
          confirmationPending.current ||
          admissionLocked.current
        }
        onClick={() => void loadPreview(selectedChoice)}
      >
        {loading ? copy.loading : copy.preview}
      </button>
      <PublicationPreviewDetails
        state={aggregateState}
        sourceGroupName={`publication-source-${releaseId}-${positionId}`}
        onSourceChoice={changeChoice}
        onRefresh={onRefresh}
      />
      {!comparisonOnly && workflow === "first" && (
        <button
          type="button"
          disabled={
            !canConfirm ||
            confirming ||
            admissionUncertain ||
            confirmationPending.current ||
            admissionLocked.current
          }
          onClick={() => void confirmPreview()}
        >
          {confirming ? copy.publicationStart : copy.publish}
        </button>
      )}
      {workflow === "retained_same_path" && (
        <button
          type="button"
          disabled={
            !canConfirm ||
            confirming ||
            admissionUncertain ||
            confirmationPending.current ||
            admissionLocked.current
          }
          onClick={() => void confirmPreview()}
        >
          {confirming ? copy.publicationStart : copy.retainedRepublish}
        </button>
      )}
    </section>
  );
}
