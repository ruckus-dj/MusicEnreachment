import type {
  FirstPublicationPreviewSource,
  LibraryFirstPublicationPreviewResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import type {
  PublicationPreviewState,
  SourceChoice,
} from "./FirstPublicationPreview";
import { firstPublicationPreviewMessages } from "./firstPublicationPreviewMessages";

type Inspection = PublicationPreviewState["inspection"];
type PreviewCopy =
  (typeof firstPublicationPreviewMessages)[keyof typeof firstPublicationPreviewMessages];

function jsonValue(value: unknown) {
  return JSON.stringify(value);
}

function candidateChoice(
  candidate: FirstPublicationPreviewSource,
): SourceChoice {
  return {
    location_id: candidate.location_id,
    media_variant_id: candidate.media_variant_id,
    stream_index: candidate.stream_index,
  };
}

function CandidateLabel({
  candidate,
  stream,
  channels,
  unsupportedStreams,
}: {
  candidate: FirstPublicationPreviewSource;
  stream: string;
  channels: string;
  unsupportedStreams: string;
}) {
  return (
    <>
      {candidate.title || candidate.codec} · {candidate.codec}, {stream}{" "}
      {candidate.stream_index}
      {candidate.language ? ` · ${candidate.language}` : ""}
      {candidate.channels ? ` · ${candidate.channels} ${channels}` : ""}
      {candidate.unsupported_audio_stream_count > 0
        ? ` · ${unsupportedStreams}: ${candidate.unsupported_audio_stream_count}`
        : ""}
    </>
  );
}

function TagValues({
  present,
  values,
  absent,
}: {
  present: boolean;
  values: string[] | null | undefined;
  absent: string;
}) {
  return (
    <pre className="m-0 whitespace-pre-wrap break-words font-mono text-xs">
      {present ? jsonValue(values) : absent}
    </pre>
  );
}

function TagComparison({
  preview,
  workflow,
  copy,
}: {
  preview: LibraryFirstPublicationPreviewResponse;
  workflow: Inspection["workflow"];
  copy: PreviewCopy;
}) {
  const differences = preview.current_comparison?.tag_differences ?? [];
  const firstPublication = workflow === "first";
  const planned = Object.entries(preview.desired_tags ?? {});
  if (!firstPublication && differences.length === 0) {
    return <p>{copy.noTagDifferences}</p>;
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full table-fixed border-collapse text-left text-xs">
        <thead>
          <tr className="border-b border-stone-300 text-stone-600">
            <th scope="col" className="w-1/4 px-2 py-2 font-medium">
              {copy.tagField}
            </th>
            <th scope="col" className="w-[37.5%] px-2 py-2 font-medium">
              {copy.tagNow}
            </th>
            <th scope="col" className="w-[37.5%] px-2 py-2 font-medium">
              {copy.tagAfter}
            </th>
          </tr>
        </thead>
        <tbody>
          {firstPublication
            ? planned.map(([key, values]) => (
                <tr className="border-b border-stone-200 align-top" key={key}>
                  <th scope="row" className="break-words px-2 py-2 font-medium">
                    {key}
                  </th>
                  <td className="px-2 py-2 text-stone-500">{copy.noCurrent}</td>
                  <td className="px-2 py-2">
                    <TagValues present values={values} absent={copy.absent} />
                  </td>
                </tr>
              ))
            : differences.map((difference) => (
                <tr
                  className="border-b border-stone-200 align-top"
                  key={difference.key}
                >
                  <th scope="row" className="break-words px-2 py-2 font-medium">
                    {difference.key}
                  </th>
                  <td className="px-2 py-2">
                    <TagValues
                      present={difference.current_present}
                      values={difference.current}
                      absent={copy.absent}
                    />
                  </td>
                  <td className="px-2 py-2">
                    <TagValues
                      present={difference.desired_present}
                      values={difference.desired}
                      absent={copy.absent}
                    />
                  </td>
                </tr>
              ))}
        </tbody>
      </table>
    </div>
  );
}

function CoverDetails({
  preview,
  copy,
}: {
  preview: LibraryFirstPublicationPreviewResponse;
  copy: PreviewCopy;
}) {
  const plan = preview.cover_plan;
  if (!plan) return null;
  return (
    <section
      className="space-y-2 rounded border border-stone-200 p-3"
      aria-label={copy.coverPlanAria}
    >
      <h4 className="font-medium">{copy.coverPlan}</h4>
      <p>
        {copy.coverAuthority}:{" "}
        {plan.authority_mode === "automatic"
          ? copy.coverModeAutomatic
          : plan.authority_mode === "manual"
            ? copy.coverModeManual
            : plan.authority_mode}{" "}
        · {copy.revision} {plan.authority_revision}
      </p>
      {plan.original ? (
        <p>
          {copy.coverOriginal}: {plan.original.mime} · {plan.original.size}{" "}
          {copy.coverBytes}
        </p>
      ) : (
        <p>{copy.coverDerivationAbsent}</p>
      )}
      {plan.derivative ? (
        <p>
          {copy.coverDerivative}: {plan.derivative.mime} ·{" "}
          {plan.derivative.size} {copy.coverBytes}
        </p>
      ) : plan.derivation === "optional_failure" ? (
        <p>
          {copy.coverDerivativeUnavailable}
          {plan.failure_reason ? ` · ${plan.failure_reason}` : ""}
        </p>
      ) : null}
      {plan.derivation !== "absent" && (
        <p>
          {copy.coverDerivation}:{" "}
          {plan.derivation === "ready"
            ? copy.coverDerivationReady
            : copy.coverDerivationOptionalFailure}
        </p>
      )}
      <p>{copy.coverPlanNotGuarantee}</p>
    </section>
  );
}

function LyricsDetails({
  preview,
  copy,
}: {
  preview: LibraryFirstPublicationPreviewResponse;
  copy: PreviewCopy;
}) {
  const plan = preview.lyrics_plan;
  if (!plan) return null;
  const currentFields = plan.current_lyrics_fields ?? {};
  const comparison = !plan.current_comparison_available
    ? copy.lyricsComparisonUnavailable
    : (preview.current_comparison?.lyrics_status ?? plan.current_status);
  return (
    <section
      className="space-y-2 rounded border border-stone-200 p-3"
      aria-label={copy.lyricsPlanAria}
    >
      <h4 className="font-medium">{copy.lyricsPlan}</h4>
      <p>
        {copy.lyricsState}:{" "}
        {plan.state === "value"
          ? copy.lyricsStateValue
          : plan.state === "absent"
            ? copy.lyricsStateAbsent
            : plan.state === "unknown"
              ? copy.lyricsStateUnknown
              : copy.lyricsStateUnprocessable}
      </p>
      <p>
        {copy.lyricsOrigin}:{" "}
        {plan.origin === "none"
          ? copy.lyricsOriginNone
          : plan.origin === "manual"
            ? copy.lyricsOriginManual
            : plan.origin === "source"
              ? copy.lyricsOriginSource
              : plan.origin === "lrclib"
                ? copy.lyricsOriginLrclib
                : plan.origin}
      </p>
      {plan.state === "value" && plan.lyrics !== undefined && (
        <div>
          <p className="font-medium">{copy.lyricsPlannedText}</p>
          <pre className="whitespace-pre-wrap break-words">{plan.lyrics}</pre>
        </div>
      )}
      {plan.warning_code && (
        <p>
          {copy.lyricsWarning}:{" "}
          {copy.lyricsWarnings[
            plan.warning_code as keyof typeof copy.lyricsWarnings
          ] ?? plan.warning_code}
        </p>
      )}
      <div>
        <h5 className="font-medium">{copy.lyricsCurrentFields}</h5>
        {!plan.current_present ? (
          <p>{copy.lyricsCurrentAbsent}</p>
        ) : (
          <>
            {plan.current_lyrics === null && <p>{copy.lyricsCurrentNil}</p>}
            {plan.current_lyrics?.length === 0 && (
              <p>{copy.lyricsCurrentEmpty}</p>
            )}
            {Object.keys(currentFields).length ? (
              <ul className="space-y-1">
                {Object.entries(currentFields).map(([field, values]) => (
                  <li key={field}>
                    <p className="break-all font-medium">{field}</p>
                    <TagValues present values={values} absent={copy.absent} />
                  </li>
                ))}
              </ul>
            ) : (
              <p>{copy.lyricsNoFields}</p>
            )}
          </>
        )}
      </div>
      <p>
        {copy.lyricsComparison}: {comparison}
      </p>
      <p>{copy.lyricsComparisonBasis}</p>
    </section>
  );
}

export function PublicationPreviewDetails({
  state,
  sourceGroupName,
  onSourceChoice,
  onRefresh,
}: {
  state: PublicationPreviewState;
  sourceGroupName: string;
  onSourceChoice: (choice: SourceChoice | null) => void;
  onRefresh: () => void;
}) {
  const { locale } = useI18n();
  const copy = firstPublicationPreviewMessages[locale];
  const {
    inspection,
    error,
    loading,
    admittedOperationID,
    admissionUncertain,
  } = state;
  const {
    preview,
    workflow,
    sourceCandidates,
    selectedChoice,
    needsExplicitChoice,
    sourceChoiceDisabled,
  } = inspection;
  const choiceKey = (choice: SourceChoice | null) =>
    choice
      ? `${choice.location_id}:${choice.media_variant_id}:${choice.stream_index}`
      : "unresolved";

  return (
    <div className="min-w-0 space-y-3">
      {error && <p role="alert">{error}</p>}
      {workflow !== "retained_same_path" && needsExplicitChoice && (
        <fieldset className="space-y-2" disabled={sourceChoiceDisabled}>
          <legend className="font-medium">{copy.sourceChoice}</legend>
          <label className="block">
            <input
              type="radio"
              name={sourceGroupName}
              checked={selectedChoice === null}
              onChange={() => onSourceChoice(null)}
            />{" "}
            {copy.noSource}
          </label>
          {sourceCandidates.map((candidate) => {
            const choice = candidateChoice(candidate);
            return (
              <label className="block" key={choiceKey(choice)}>
                <input
                  type="radio"
                  name={sourceGroupName}
                  checked={choiceKey(selectedChoice) === choiceKey(choice)}
                  onChange={() => onSourceChoice(choice)}
                />{" "}
                <CandidateLabel
                  candidate={candidate}
                  stream={copy.stream}
                  channels={copy.channels}
                  unsupportedStreams={copy.unsupportedStreams}
                />
              </label>
            );
          })}
        </fieldset>
      )}
      {loading && !preview && <p role="status">{copy.loading}</p>}
      {preview && (
        <div className="space-y-3">
          {workflow === "retained_same_path" &&
            preview.confirmation_capability && (
              <p role="status">
                {preview.confirmation_capability.status === "ready"
                  ? copy.retainedCapabilityReady
                  : preview.confirmation_capability.status === "unchanged"
                    ? copy.retainedUnchanged
                    : copy.retainedBlocked}
              </p>
            )}
          <div>
            <h3 className="font-semibold">
              {workflow === "comparison"
                ? copy.currentComparison
                : workflow === "retained_same_path"
                  ? copy.retainedPlan
                  : copy.firstPlan}
            </h3>
            <p>
              {copy.source}:{" "}
              {preview.selected_source
                ? `${preview.selected_source.codec}, ${copy.stream} ${preview.selected_source.stream_index}`
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
            {workflow !== "first" && preview.current_comparison && (
              <>
                <p>
                  {copy.currentPath}:{" "}
                  {preview.current_comparison.current_components?.length
                    ? preview.current_comparison.current_components.join("/")
                    : copy.pathUnknown}
                </p>
                <p>
                  {copy.renderedPath}:{" "}
                  {preview.current_comparison.rendered_components?.length
                    ? preview.current_comparison.rendered_components.join("/")
                    : copy.pathUnknown}
                </p>
                <p>
                  {copy.resolvedPath}:{" "}
                  {preview.current_comparison.resolved_components?.length
                    ? preview.current_comparison.resolved_components.join("/")
                    : copy.pathUnknown}
                </p>
                <p>
                  {copy.comparisonStatus}: {preview.current_comparison.status}
                </p>
                <p>
                  {copy.audioComparison}:{" "}
                  {preview.current_comparison.audio_status === "matches"
                    ? copy.audioStatusMatches
                    : preview.current_comparison.audio_status === "differs"
                      ? copy.audioStatusDiffers
                      : copy.audioStatusUnavailable}
                </p>
                <p>
                  {copy.coverComparison}:{" "}
                  {preview.current_comparison.cover_status === "matches"
                    ? copy.coverStatusMatches
                    : preview.current_comparison.cover_status === "differs"
                      ? copy.coverStatusDiffers
                      : copy.coverStatusUnavailable}
                </p>
              </>
            )}
            {preview.blockers?.length ? (
              <ul aria-label={copy.blockers}>
                {preview.blockers.map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
            ) : null}
            {preview.warnings?.length ? (
              <ul aria-label={copy.warnings}>
                {preview.warnings.map((item) => (
                  <li key={item}>
                    {item === "output_quality_would_downgrade"
                      ? copy.outputQualityDowngradeWarning
                      : item}
                  </li>
                ))}
              </ul>
            ) : null}
            <section>
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
            </section>
          </div>
          <section
            className="space-y-2 rounded border border-stone-200 p-3"
            aria-label={copy.tagDifferences}
          >
            <h4 className="font-medium">
              {workflow === "first" ? copy.plannedTags : copy.tagDifferences}
            </h4>
            <TagComparison preview={preview} workflow={workflow} copy={copy} />
          </section>
          <CoverDetails preview={preview} copy={copy} />
          <LyricsDetails preview={preview} copy={copy} />
          {workflow !== "first" &&
            preview.current_comparison?.audio_evidence && (
              <p>
                {copy.audioAuthorityStatus}:{" "}
                {preview.current_comparison.audio_evidence
                  .source_authority_status === "present"
                  ? copy.audioAuthorityPresent
                  : preview.current_comparison.audio_evidence
                        .source_authority_status === "stale"
                    ? copy.audioAuthorityStale
                    : preview.current_comparison.audio_evidence
                          .source_authority_status === "missing"
                      ? copy.audioAuthorityMissing
                      : copy.audioAuthorityUnknown}
              </p>
            )}
          {workflow !== "first" && <p>{copy.audioComparisonBasis}</p>}
          {workflow !== "first" && <p>{copy.readOnlyBoundary}</p>}
          <p>
            {copy.namespaceCheck}: {preview.native_status}.{" "}
            {workflow === "retained_same_path"
              ? copy.retainedPrestartRevalidation
              : copy.previewNotPublicationCheck}
          </p>
        </div>
      )}
      {admittedOperationID && <p role="status">{copy.queued}.</p>}
      {admissionUncertain && <p role="status">{copy.unknownStart}</p>}
      {(error || admittedOperationID || admissionUncertain) && (
        <button type="button" onClick={onRefresh}>
          {copy.refreshPublication}
        </button>
      )}
    </div>
  );
}
