import { useCallback, useEffect, useRef, useState } from "react";
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

type SourceChoice = Pick<
  FirstPublicationPreviewSource,
  "location_id" | "media_variant_id" | "stream_index"
>;

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

type PreviewCopy =
  (typeof firstPublicationPreviewMessages)[keyof typeof firstPublicationPreviewMessages];

function coverFactsText(
  facts: { mime: string; size: number; sha256: string },
  copy: PreviewCopy,
) {
  return `${facts.mime} · ${facts.size} ${copy.coverBytes} · ${copy.coverSHA256} ${facts.sha256}`;
}

function lyricsStateText(state: string, copy: PreviewCopy) {
  switch (state) {
    case "absent":
      return copy.lyricsStateAbsent;
    case "value":
      return copy.lyricsStateValue;
    case "unknown":
      return copy.lyricsStateUnknown;
    case "unprocessable":
      return copy.lyricsStateUnprocessable;
    default:
      return state;
  }
}

function lyricsOriginText(origin: string, copy: PreviewCopy) {
  switch (origin) {
    case "none":
      return copy.lyricsOriginNone;
    case "manual":
      return copy.lyricsOriginManual;
    case "source":
      return copy.lyricsOriginSource;
    case "lrclib":
      return copy.lyricsOriginLrclib;
    default:
      return origin;
  }
}

function lyricsComparisonText(status: string, copy: PreviewCopy) {
  switch (status) {
    case "matches":
      return copy.lyricsComparisonMatches;
    case "differs":
      return copy.lyricsComparisonDiffers;
    case "unavailable":
      return copy.lyricsComparisonUnavailable;
    default:
      return status;
  }
}

function lyricsWarningText(code: string, copy: PreviewCopy) {
  const warnings: Record<string, string> = copy.lyricsWarnings;
  return warnings[code] ?? code;
}

function audioStatusText(status: string, copy: PreviewCopy) {
  switch (status) {
    case "matches":
      return copy.audioStatusMatches;
    case "differs":
      return copy.audioStatusDiffers;
    default:
      return copy.audioStatusUnavailable;
  }
}

function audioAuthorityStatusText(status: string, copy: PreviewCopy) {
  switch (status) {
    case "present":
      return copy.audioAuthorityPresent;
    case "stale":
      return copy.audioAuthorityStale;
    case "missing":
      return copy.audioAuthorityMissing;
    default:
      return copy.audioAuthorityUnknown;
  }
}

function isZeroUUID(value: string | undefined) {
  return !value || value === "00000000-0000-0000-0000-000000000000";
}

export function FirstPublicationPreview({
  releaseId,
  positionId,
  refreshToken,
  comparisonOnly = false,
  onAdmitted,
  onRefresh,
}: {
  releaseId: string;
  positionId: string;
  refreshToken?: number | string;
  comparisonOnly?: boolean;
  onAdmitted: (operationID: string) => void;
  onRefresh: () => void;
}) {
  const { locale } = useI18n();
  const copy = firstPublicationPreviewMessages[locale];
  const [preview, setPreview] =
    useState<LibraryFirstPublicationPreviewResponse>();
  const [selectedChoice, setSelectedChoice] = useState<SourceChoice | null>(
    null,
  );
  const [mode, setMode] = useState<"ordinary" | "forced">("ordinary");
  const [sourceCandidates, setSourceCandidates] = useState<
    FirstPublicationPreviewSource[]
  >([]);
  const [previewChoiceKey, setPreviewChoiceKey] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState("");
  const [admittedOperationID, setAdmittedOperationID] = useState("");
  const [admissionUncertain, setAdmissionUncertain] = useState(false);
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
    refreshToken,
  });
  currentIdentity.current = {
    releaseId,
    positionId,
    choiceKey: choiceKey(selectedChoice),
    mode,
    refreshToken,
  };

  useEffect(() => {
    currentIdentity.current = {
      ...currentIdentity.current,
      releaseId,
      positionId,
      choiceKey: "unresolved",
    };
    mounted.current = true;
    setSelectedChoice(null);
    setSourceCandidates([]);
    setPreview(undefined);
    setPreviewChoiceKey("");
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
  }, [releaseId, positionId]);

  const isCurrentRequest = useCallback(
    (
      request: number,
      requestIdentity: {
        releaseId: string;
        positionId: string;
        choiceKey: string;
        mode: string;
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
    async (choice: SourceChoice | null, requestMode = mode) => {
      const request = ++requestSequence.current;
      const requestIdentity = {
        releaseId,
        positionId,
        choiceKey: choiceKey(choice),
        mode: requestMode,
        refreshToken,
      };
      currentIdentity.current = requestIdentity;
      setLoading(true);
      setError("");
      setPreview(undefined);
      setPreviewChoiceKey("");
      try {
        const response = await getLibraryFirstPublicationPreview(
          releaseId,
          positionId,
          choice
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
    [copy, releaseId, positionId, mode, refreshToken, isCurrentRequest],
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
    setLoading(false);
    setError("");
    if (hadPreviewRequest) void loadPreview(choice);
  }, [
    refreshToken,
    confirming,
    admissionUncertain,
    admittedOperationID,
    loading,
    preview,
    selectedChoice,
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
      refreshToken,
    };
    setSelectedChoice(choice);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setLoading(false);
    setError("");
    setAdmissionUncertain(false);
    setAdmittedOperationID("");
    void loadPreview(choice);
  }

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
      refreshToken,
    };
    setMode(nextMode);
    setPreview(undefined);
    setPreviewChoiceKey("");
    setError("");
    void loadPreview(selectedChoice, nextMode);
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
    };
    const confirmedPreview = preview;
    if (previewChoiceKey !== requestIdentity.choiceKey) return;
    confirmationPending.current = true;
    setConfirming(true);
    setError("");
    try {
      const response = await startLibraryFirstPublication(
        releaseId,
        positionId,
        {
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
        },
      );
      if (!isCurrentRequest(request, requestIdentity)) return;
      if (response.status === 202) {
        admissionLocked.current = true;
        setAdmittedOperationID(response.data.id);
        setPreview(undefined);
        onAdmitted(response.data.id);
        return;
      }
      if (response.status === 409) {
        admissionLocked.current = false;
        setPreview(undefined);
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
  const canConfirm = Boolean(
    !comparisonOnly &&
      preview &&
      previewChoiceKey === choiceKey(selectedChoice) &&
      preview.blockers?.length === 0 &&
      preview.selected_source &&
      (!needsExplicitChoice || choiceMatchesPreview) &&
      preview.transform &&
      preview.resolved_path_components?.length,
  );
  const audioEvidence = preview?.current_comparison?.audio_evidence;
  const audioAuthorityPresent =
    audioEvidence?.source_authority_status === "present";
  const lyricsPlan = preview?.lyrics_plan;
  const lyricsComparisonStatus = lyricsPlan
    ? !lyricsPlan.current_comparison_available
      ? "unavailable"
      : (preview?.current_comparison?.lyrics_status ??
        lyricsPlan.current_status)
    : "unavailable";

  return (
    <section
      className="space-y-3 rounded border border-stone-300 p-4"
      aria-label={copy.section}
    >
      {comparisonOnly && (
        <>
          <p role="note">{copy.comparisonReadOnly}</p>
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
      {error && <p role="alert">{error}</p>}
      {needsExplicitChoice && (
        <fieldset
          className="space-y-2"
          disabled={
            confirming ||
            admissionUncertain ||
            Boolean(admittedOperationID) ||
            confirmationPending.current ||
            admissionLocked.current
          }
        >
          <legend className="font-medium">{copy.sourceChoice}</legend>
          <label className="block">
            <input
              type="radio"
              name={`publication-source-${releaseId}-${positionId}`}
              checked={selectedChoice === null}
              onChange={() => changeChoice(null)}
            />{" "}
            {copy.noSource}
          </label>
          {candidates.map((candidate) => {
            const candidateChoice: SourceChoice = {
              location_id: candidate.location_id,
              media_variant_id: candidate.media_variant_id,
              stream_index: candidate.stream_index,
            };
            return (
              <label className="block" key={choiceKey(candidateChoice)}>
                <input
                  type="radio"
                  name={`publication-source-${releaseId}-${positionId}`}
                  checked={
                    choiceKey(selectedChoice) === choiceKey(candidateChoice)
                  }
                  onChange={() => changeChoice(candidateChoice)}
                />{" "}
                {candidate.title || candidate.codec} · {candidate.codec},{" "}
                {copy.stream} {candidate.stream_index}
                {candidate.language ? ` · ${candidate.language}` : ""}
                {candidate.channels
                  ? ` · ${candidate.channels} ${copy.channels}`
                  : ""}
                {candidate.unsupported_audio_stream_count > 0
                  ? ` · ${copy.unsupportedStreams}: ${candidate.unsupported_audio_stream_count}`
                  : ""}
                {` · ${copy.root} ${candidate.root_id}, ${copy.location} ${candidate.location_id}, ${copy.variant} ${candidate.media_variant_id}`}
              </label>
            );
          })}
        </fieldset>
      )}
      {preview && (
        <div className="space-y-2">
          <h3 className="font-semibold">
            {comparisonOnly ? copy.currentComparison : copy.firstPlan}
          </h3>
          <p>
            {copy.source}:{" "}
            {preview.selected_source
              ? `${preview.selected_source.codec}, ${copy.stream} ${preview.selected_source.stream_index} · ${copy.variant} ${preview.selected_source.media_variant_id}`
              : copy.unselected}
          </p>
          <p>
            {copy.transform}:{" "}
            {preview.transform
              ? `${preview.transform.action} → ${preview.transform.output_container}${preview.transform.extension ? ` (.${preview.transform.extension})` : ""}${preview.transform.bitrate ? `, ${preview.transform.bitrate} ${copy.bitrate}` : ""}`
              : copy.undefined}
          </p>
          <p>
            {copy.plannedPath}:{" "}
            {preview.resolved_path_components?.length
              ? preview.resolved_path_components.join("/")
              : copy.namespaceUnconfirmed}
          </p>
          <div className="rounded bg-amber-50 p-3">
            <h4 className="font-medium">{copy.plannedTags}</h4>
            {Object.entries(preview.desired_tags ?? {}).map(
              ([name, values]) => (
                <p key={name}>
                  {name}: {(values ?? []).join(", ")}
                </p>
              ),
            )}
          </div>
          <div>
            <h4 className="font-medium">{copy.metadataRevisions}</h4>
            {Object.keys(preview.metadata_revisions ?? {}).length ? (
              <ul>
                {Object.entries(preview.metadata_revisions).map(
                  ([field, revision]) => (
                    <li key={field}>
                      {field}: {copy.revision} {revision}
                    </li>
                  ),
                )}
              </ul>
            ) : (
              <p>{copy.noProjections}</p>
            )}
          </div>
          <div>
            <h4 className="font-medium">{copy.warnings}</h4>
            {preview.warnings?.length ? (
              <ul>
                {Array.from(new Set(preview.warnings ?? [])).map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : (
              <p>{copy.none}</p>
            )}
          </div>
          <div>
            <h4 className="font-medium">{copy.blockers}</h4>
            {preview.blockers?.length ? (
              <ul>
                {Array.from(new Set(preview.blockers ?? [])).map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : (
              <p>{copy.none}</p>
            )}
          </div>
          <p role="status">
            {copy.namespaceCheck}: {preview.native_status}.{" "}
            {copy.previewNotPublicationCheck}
          </p>
          {preview.cover_plan && (
            <section
              className="space-y-2 rounded border border-stone-200 p-3"
              aria-label={copy.coverPlanAria}
            >
              <h4 className="font-medium">{copy.coverPlan}</h4>
              <p>
                {copy.coverAuthority}:{" "}
                {preview.cover_plan.authority_mode === "automatic"
                  ? copy.coverModeAutomatic
                  : preview.cover_plan.authority_mode === "manual"
                    ? copy.coverModeManual
                    : preview.cover_plan.authority_mode}
                , {copy.revision} {preview.cover_plan.authority_revision}
              </p>
              {preview.cover_plan.authority_generation && (
                <p>
                  {copy.coverGeneration}:{" "}
                  {preview.cover_plan.authority_generation}
                </p>
              )}
              <p>
                {copy.coverRelease}: {preview.cover_plan.release_id}
              </p>
              {preview.cover_plan.original ? (
                <div>
                  <p className="font-medium">{copy.coverOriginal}</p>
                  <p className="break-all">
                    {coverFactsText(preview.cover_plan.original, copy)}
                  </p>
                </div>
              ) : (
                <p>{copy.coverDerivationAbsent}</p>
              )}
              {preview.cover_plan.derivative ? (
                <div>
                  <p className="font-medium">{copy.coverDerivative}</p>
                  <p className="break-all">
                    {coverFactsText(preview.cover_plan.derivative, copy)}
                  </p>
                </div>
              ) : (
                preview.cover_plan.derivation === "optional_failure" && (
                  <div>
                    <p className="font-medium">{copy.coverDerivative}</p>
                    <p>{copy.coverDerivativeUnavailable}</p>
                    {preview.cover_plan.failure_reason && (
                      <p>
                        {copy.coverFailureReason}:{" "}
                        {preview.cover_plan.failure_reason}
                      </p>
                    )}
                  </div>
                )
              )}
              {preview.cover_plan.derivation !== "absent" && (
                <p>
                  {copy.coverDerivation}:{" "}
                  {preview.cover_plan.derivation === "ready"
                    ? copy.coverDerivationReady
                    : copy.coverDerivationOptionalFailure}
                </p>
              )}
              <p>{copy.coverPlanNotGuarantee}</p>
            </section>
          )}
          {lyricsPlan && (
            <section
              className="space-y-2 rounded border border-stone-200 p-3"
              aria-label={copy.lyricsPlanAria}
            >
              <h4 className="font-medium">{copy.lyricsPlan}</h4>
              <p>
                {copy.lyricsState}: {lyricsStateText(lyricsPlan.state, copy)}
              </p>
              <p>
                {copy.lyricsOrigin}: {lyricsOriginText(lyricsPlan.origin, copy)}
              </p>
              {lyricsPlan.state === "value" &&
                lyricsPlan.lyrics !== undefined && (
                  <div>
                    <p className="font-medium">{copy.lyricsPlannedText}</p>
                    <pre className="whitespace-pre-wrap break-words">
                      {lyricsPlan.lyrics}
                    </pre>
                  </div>
                )}
              {lyricsPlan.sha256 && (
                <p className="break-all">
                  {copy.lyricsChecksum}: {lyricsPlan.sha256}
                </p>
              )}
              {lyricsPlan.warning_code && (
                <p>
                  {copy.lyricsWarning}:{" "}
                  {lyricsWarningText(lyricsPlan.warning_code, copy)}
                </p>
              )}
              <div>
                <h5 className="font-medium">{copy.lyricsCurrentFields}</h5>
                {lyricsPlan.current_present ? (
                  <>
                    {lyricsPlan.current_lyrics === null ? (
                      <p>{copy.lyricsCurrentNil}</p>
                    ) : lyricsPlan.current_lyrics.length === 0 ? (
                      <p>{copy.lyricsCurrentEmpty}</p>
                    ) : null}
                    {Object.keys(lyricsPlan.current_lyrics_fields ?? {})
                      .length ? (
                      <ul className="space-y-1">
                        {Object.entries(
                          lyricsPlan.current_lyrics_fields ?? {},
                        ).map(([field, values]) => (
                          <li key={field}>
                            <p className="break-all font-medium">{field}</p>
                            {values?.length ? (
                              <pre className="whitespace-pre-wrap break-words">
                                {JSON.stringify(values, null, 2)}
                              </pre>
                            ) : (
                              <p>{copy.lyricsFieldEmpty}</p>
                            )}
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <p>{copy.lyricsNoFields}</p>
                    )}
                  </>
                ) : (
                  <p>{copy.lyricsCurrentAbsent}</p>
                )}
              </div>
              <p>
                {copy.lyricsComparison}:{" "}
                {lyricsComparisonText(lyricsComparisonStatus, copy)}
              </p>
              <p>{copy.lyricsComparisonBasis}</p>
            </section>
          )}
          {comparisonOnly && preview.current_comparison && (
            <section
              className="space-y-2"
              aria-label={copy.currentComparisonAria}
            >
              <p>
                {copy.comparisonStatus}: {preview.current_comparison.status}
              </p>
              <div className="grid gap-2 sm:grid-cols-3">
                {[
                  [
                    copy.currentPath,
                    preview.current_comparison.current_components,
                  ],
                  [
                    copy.renderedPath,
                    preview.current_comparison.rendered_components,
                  ],
                  [
                    copy.resolvedPath,
                    preview.current_comparison.resolved_components,
                  ],
                ].map(([label, components]) => (
                  <p key={label as string}>
                    {label}:{" "}
                    {Array.isArray(components) && components.length
                      ? components.join("/")
                      : copy.pathUnknown}
                  </p>
                ))}
              </div>
              <p>
                {copy.audioComparison}:{" "}
                {audioStatusText(preview.current_comparison.audio_status, copy)}
              </p>
              {audioEvidence && (
                <div>
                  <p>
                    {copy.audioAuthorityStatus}:{" "}
                    {audioAuthorityStatusText(
                      audioEvidence.source_authority_status,
                      copy,
                    )}
                  </p>
                  {audioAuthorityPresent && (
                    <ul>
                      {!isZeroUUID(audioEvidence.media_variant_id) && (
                        <li>
                          {copy.audioCurrentVariant}:{" "}
                          {audioEvidence.media_variant_id}
                        </li>
                      )}
                      <li>
                        {copy.audioSelectedVariant}:{" "}
                        {isZeroUUID(audioEvidence.selected_media_variant_id)
                          ? copy.audioNotRecorded
                          : audioEvidence.selected_media_variant_id}
                      </li>
                      {audioEvidence.canonical_source_sha256 && (
                        <li className="break-all">
                          {copy.audioCanonicalSHA256}:{" "}
                          {audioEvidence.canonical_source_sha256}
                        </li>
                      )}
                    </ul>
                  )}
                </div>
              )}
              <p>{copy.audioComparisonBasis}</p>
              <p>
                {copy.coverComparison}:{" "}
                {preview.current_comparison.cover_status === "matches"
                  ? copy.coverStatusMatches
                  : preview.current_comparison.cover_status === "differs"
                    ? copy.coverStatusDiffers
                    : copy.coverStatusUnavailable}
              </p>
              <h4 className="font-medium">{copy.tagDifferences}</h4>
              {preview.current_comparison.tag_differences?.length ? (
                <ol className="list-decimal pl-5">
                  {preview.current_comparison.tag_differences.map(
                    (difference) => (
                      <li key={difference.key}>
                        <strong>{difference.key}</strong>: {copy.currentValue}{" "}
                        {difference.current_present
                          ? JSON.stringify(difference.current)
                          : copy.absent}
                        ; {copy.calculatedValue}{" "}
                        {difference.desired_present
                          ? JSON.stringify(difference.desired)
                          : copy.absent}
                      </li>
                    ),
                  )}
                </ol>
              ) : (
                <p>{copy.noTagDifferences}</p>
              )}
              <p>{copy.readOnlyBoundary}</p>
            </section>
          )}
          {!comparisonOnly && (
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
        </div>
      )}
      {admittedOperationID && (
        <div>
          <p role="status">
            {copy.queued}: {admittedOperationID}.
          </p>
          <button type="button" onClick={onRefresh}>
            {copy.refreshPublication}
          </button>
        </div>
      )}
      {admissionUncertain && (
        <button type="button" onClick={onRefresh}>
          {copy.refreshPublication}
        </button>
      )}
    </section>
  );
}
