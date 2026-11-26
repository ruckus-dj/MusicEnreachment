import type { Ref } from "react";
import { Input, Label, TextField } from "react-aria-components";
import { AppBreadcrumbs } from "../../components/AppBreadcrumbs";
import { AppButton } from "../../components/AppButton";
import {
  IncomingTagEncodingReviewOutlet,
  type IncomingTagEncodingReviewSource,
} from "../incoming/IncomingTagEncodingReviewOutlet";
import type { ManualReleasePreparationSource } from "./ManualReleasePreparationOutlet";
import type { ManualSearchSource } from "./ManualSearchOutlet";
import { MatchingBoard, type MatchingBoardProps } from "./MatchingBoard";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";
import { MatchingPreview, type MatchingPreviewRow } from "./MatchingPreview";
import { MatchingReviewSearchView } from "./MatchingReviewSearchView";
import {
  type MatchingSavedReceiptModel,
  MatchingSavedReceiptView,
} from "./MatchingSavedReceiptView";
import {
  MatchingWorkspaceLayout,
  matchingInspectorActions,
  matchingInspectorContent,
  matchingInspectorPanel,
} from "./MatchingWorkspaceLayout";
import {
  matchingBoardClass,
  matchingBoardClasses,
} from "./matchingBoardStyles";
import type {
  MatchingWorkspaceCandidate,
  MatchingWorkspaceLocale,
  MatchingWorkspacePosition,
} from "./types";

export type MatchingScreenViewAction = {
  label: string;
  disabled?: boolean;
  onPress: () => void;
};

export type MatchingScreenViewHeading = {
  backHref: string;
  backLabel: string;
  sourceDirectoryLabel?: string;
  sourcePath?: string;
  title: string;
  hint?: string;
  id?: string;
  headingRef?: Ref<HTMLHeadingElement>;
};

export type MatchingScreenViewOption = {
  value: string;
  label: string;
};

export type MatchingScreenViewCandidateRow = {
  key: string;
  fileName: string;
  releaseTitle: string;
  evidence: string;
};

export type MatchingScreenViewBoard = MatchingBoardProps & {
  recordingSearch?: {
    label: string;
    trackIds: readonly string[];
    onOpen: (trackId: string) => void;
  } | null;
};

export type MatchingScreenViewRecordingResult = {
  key: string;
  title: string;
  releaseTitle: string;
  duration: string;
  linkLabel: string;
  onLink: () => void;
};

export type MatchingScreenViewRecording = {
  title: string;
  queryLabel: string;
  ariaSearchLabel: string;
  query: string;
  onQueryChange: (value: string) => void;
  onSubmit: () => void;
  searchLabel: string;
  closeLabel: string;
  onClose: () => void;
  hint: string;
  results: readonly MatchingScreenViewRecordingResult[];
};

export type MatchingScreenViewFooter = {
  assignedLabel: string;
  summary: string;
  previewLabel: string;
  previewDisabled: boolean;
  onPreview: () => void;
};

export type MatchingScreenViewPreviewDetail = {
  key: string;
  title: string;
  text: string;
};

export type MatchingScreenViewPreview = {
  headingRef?: Ref<HTMLHeadingElement>;
  heading: string;
  releaseTitle?: string;
  summary: string;
  rows: readonly MatchingPreviewRow[];
  notice?: string;
  primary: MatchingScreenViewAction;
  secondary?: MatchingScreenViewAction;
  details?: {
    heading: string;
    items: readonly MatchingScreenViewPreviewDetail[];
  };
};

export type MatchingScreenViewEvidence = {
  fileName?: string;
  position: MatchingWorkspacePosition;
  rawTags?: Record<string, string[] | null>;
  displayTags?: Record<string, string[] | null>;
  candidate?: MatchingWorkspaceCandidate;
};

export type MatchingScreenViewInspectorActions = {
  ariaLabel?: string;
  error?: string;
  notice?: string;
  stale?: string;
  buttons: readonly MatchingScreenViewAction[];
};

export type MatchingScreenViewInspector = {
  ariaLabel: string;
  title: string;
  context?: string;
  evidence: MatchingScreenViewEvidence | null;
  emptyText: string;
  actions?: MatchingScreenViewInspectorActions;
};

export type MatchingScreenViewEmptyBoard = {
  positions: {
    heading: string;
    loading?: string;
    error?: MatchingScreenViewAction;
    instruction: string;
  };
  sources: {
    heading: string;
    instruction: string;
    members: readonly { key: string; label: string }[];
    emptyText: string;
  };
};

export type MatchingScreenViewSummaryTable = {
  label: string;
  heading: string;
  columns: readonly [string, string, string, string];
  values: readonly [string, string, string, string];
  note: string;
};

export type MatchingScreenViewReview = {
  releaseSelection: {
    heading: string;
    label: string;
    disabled?: boolean;
    value: string;
    placeholder: string;
    options: readonly MatchingScreenViewOption[];
    onChange: (value: string) => void;
    savedText?: string;
  };
  candidates: {
    heading: string;
    rows: readonly MatchingScreenViewCandidateRow[];
    emptyText: string;
  };
};

export type MatchingScreenViewWorkspace = {
  kind: "workspace";
  locale: MatchingWorkspaceLocale;
  hidden?: boolean;
  heading: MatchingScreenViewHeading;
  feedback?: { text: string; visible: boolean };
  summary?: { assigned: string; free: string; detail: string };
  status?: { loading?: string; error?: string };
  draftHint?: string;
  groupSelect?: {
    label: string;
    value: string;
    options: readonly MatchingScreenViewOption[];
    onChange: (value: string) => void;
  };
  encoding?: IncomingTagEncodingReviewSource;
  evaluation?: {
    loading?: string;
    error?: { text: string; retry: MatchingScreenViewAction };
    notRun?: string;
    unavailable?: string;
    noReleases?: string;
  };
  authority?: {
    heading: string;
    modeLabel: string;
    mode: string;
    explanation: string;
    notice?: string;
    returnToAutomatic?: MatchingScreenViewAction;
    refresh?: MatchingScreenViewAction;
  };
  search: ManualSearchSource;
  preparation: ManualReleasePreparationSource;
  createLocalReleaseHref?: string;
  review?: MatchingScreenViewReview;
  board: MatchingScreenViewBoard | null;
  emptyBoard?: MatchingScreenViewEmptyBoard;
  summaryTable?: MatchingScreenViewSummaryTable;
  inspector: MatchingScreenViewInspector;
  recording?: MatchingScreenViewRecording | null;
  footer?: MatchingScreenViewFooter | null;
  preview?: MatchingScreenViewPreview | null;
  onEscape?: () => void;
};

export type MatchingScreenViewModel =
  | {
      kind: "receipt";
      locale: MatchingWorkspaceLocale;
      hidden?: boolean;
      heading: MatchingScreenViewHeading;
      body: MatchingSavedReceiptModel;
    }
  | {
      kind: "loading";
      locale: MatchingWorkspaceLocale;
      hidden?: boolean;
      heading: MatchingScreenViewHeading;
      message: string;
    }
  | {
      kind: "error";
      locale: MatchingWorkspaceLocale;
      hidden?: boolean;
      heading: MatchingScreenViewHeading;
      message: string;
      retry?: MatchingScreenViewAction;
    }
  | {
      kind: "missing";
      locale: MatchingWorkspaceLocale;
      hidden?: boolean;
      heading: MatchingScreenViewHeading;
      message?: string;
    }
  | MatchingScreenViewWorkspace;

const pageSectionClass =
  "relative mx-auto w-full max-w-[1500px] px-0 text-[var(--ink)]";

const workspaceSectionClass = [
  "relative mx-auto w-full max-w-[1500px] px-0 text-[var(--ink)]",
  "min-[1400px]:flex min-[1400px]:h-full min-[1400px]:min-h-0 min-[1400px]:flex-col",
].join(" ");

const pageTitleClass =
  "m-0 mt-2 text-[26px]/[1.2] font-semibold tracking-[-.02em] [text-wrap:balance]";
const workspaceTitleClass =
  "m-0 mt-2 text-[26px]/[1.2] font-semibold tracking-[-.02em] [text-wrap:balance]";
const sectionHeadingClass = "m-0 mb-3 text-base font-semibold";
const surfacePanel =
  "min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4";
const recordingFieldClass = "min-w-0";
const recordingInputClass =
  "h-9 min-w-0 w-full rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0";

function ScreenHeader({
  heading,
  workspace,
}: {
  heading: MatchingScreenViewHeading;
  workspace: boolean;
}) {
  return (
    <header className={workspace ? "mb-4 min-[1400px]:shrink-0" : undefined}>
      <AppBreadcrumbs
        label={heading.backLabel}
        items={[
          {
            id: "back",
            label: heading.backLabel,
            href: heading.backHref,
          },
          ...(workspace && heading.sourceDirectoryLabel && heading.sourcePath
            ? [
                {
                  id: "source",
                  label: `${heading.sourceDirectoryLabel}: ${heading.sourcePath}`,
                },
              ]
            : []),
          { id: "current", label: heading.title },
        ]}
      />
      <h1
        id={heading.id}
        ref={heading.headingRef}
        tabIndex={heading.headingRef || heading.id ? -1 : undefined}
        className={workspace ? workspaceTitleClass : pageTitleClass}
      >
        {heading.title}
      </h1>
      {heading.hint ? (
        <p className="mt-1 text-[var(--muted)]">{heading.hint}</p>
      ) : null}
    </header>
  );
}

function ReviewSelection({ review }: { review: MatchingScreenViewReview }) {
  const { releaseSelection, candidates } = review;
  return (
    <div className="mt-3 space-y-3">
      <h3 className="font-medium">{releaseSelection.heading}</h3>
      <label className="block text-sm font-medium">
        {releaseSelection.label}
        <select
          disabled={releaseSelection.disabled}
          className="mt-1 block w-full rounded border border-[var(--control)] bg-[var(--surface)] p-2"
          value={releaseSelection.value}
          onChange={(event) => releaseSelection.onChange(event.target.value)}
        >
          <option value="">{releaseSelection.placeholder}</option>
          {releaseSelection.options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </label>
      {releaseSelection.savedText ? (
        <p className="text-sm">{releaseSelection.savedText}</p>
      ) : null}
      <details className="rounded border border-[var(--line)] p-2">
        <summary className="cursor-pointer font-medium">
          {candidates.heading}
        </summary>
        {candidates.rows.length ? (
          <ul className="mt-2 max-h-[22rem] space-y-2 overflow-y-auto text-sm">
            {candidates.rows.map((row) => (
              <li
                key={row.key}
                className="rounded border border-[var(--line)] p-2"
              >
                <strong>{row.fileName}</strong>
                <p>{row.releaseTitle}</p>
                <p>{row.evidence}</p>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm">{candidates.emptyText}</p>
        )}
      </details>
    </div>
  );
}

function Inspector({
  locale,
  inspector,
}: {
  locale: MatchingWorkspaceLocale;
  inspector: MatchingScreenViewInspector;
}) {
  const { actions } = inspector;
  return (
    <section
      data-matching-inspector
      aria-label={inspector.ariaLabel}
      className={matchingInspectorPanel}
    >
      <div className={matchingInspectorContent}>
        <h2 className={sectionHeadingClass}>{inspector.title}</h2>
        {inspector.context ? (
          <p className="mt-1 text-sm wrap-anywhere">{inspector.context}</p>
        ) : inspector.evidence?.fileName ? (
          <p className="mt-1 text-sm wrap-anywhere">
            {inspector.evidence.fileName}
          </p>
        ) : null}
        {inspector.evidence ? (
          <MatchingEvidenceInspector
            position={inspector.evidence.position}
            rawTags={inspector.evidence.rawTags}
            displayTags={inspector.evidence.displayTags}
            candidate={inspector.evidence.candidate}
            locale={locale}
          />
        ) : (
          <p className="mt-2 text-sm text-[var(--muted)]">
            {inspector.emptyText}
          </p>
        )}
      </div>
      {actions ? (
        <aside
          data-matching-actions
          className={matchingInspectorActions}
          aria-label={actions.ariaLabel}
        >
          {actions.error ? (
            <p
              role="alert"
              className="w-full rounded-lg border border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] p-2.5 text-sm text-[var(--error)]"
            >
              {actions.error}
            </p>
          ) : null}
          {actions.notice ? (
            <p
              role="status"
              className="w-full rounded-lg border border-[color-mix(in_srgb,var(--success)_25%,transparent)] bg-[var(--success-soft)] p-2.5 text-sm text-[var(--success)]"
            >
              {actions.notice}
            </p>
          ) : null}
          {actions.stale ? <p role="status">{actions.stale}</p> : null}
          {actions.buttons.map((button) => (
            <AppButton
              key={button.label}
              isDisabled={button.disabled}
              onPress={button.onPress}
            >
              {button.label}
            </AppButton>
          ))}
        </aside>
      ) : null}
    </section>
  );
}

function EmptyBoard({ board }: { board: MatchingScreenViewEmptyBoard }) {
  return (
    <>
      <section
        data-matching-positions
        className={matchingBoardClass.panel}
        aria-labelledby="assignment-board-title"
      >
        <h2 id="assignment-board-title" className={matchingBoardClass.heading}>
          {board.positions.heading}
        </h2>
        {board.positions.loading ? (
          <p role="status" className={matchingBoardClass.instructions}>
            {board.positions.loading}
          </p>
        ) : board.positions.error ? (
          <AppButton
            isDisabled={board.positions.error.disabled}
            onPress={board.positions.error.onPress}
          >
            {board.positions.error.label}
          </AppButton>
        ) : (
          <p className={matchingBoardClass.instructions}>
            {board.positions.instruction}
          </p>
        )}
      </section>
      <section
        data-matching-sources
        className={matchingBoardClass.panel}
        aria-labelledby="matching-sources-title"
      >
        <h2 id="matching-sources-title" className={matchingBoardClass.heading}>
          {board.sources.heading}
        </h2>
        <p className={matchingBoardClass.instructions}>
          {board.sources.instruction}
        </p>
        {board.sources.members.length ? (
          <ul className={matchingBoardClass.list}>
            {board.sources.members.map((member) => (
              <li
                key={member.key}
                className={matchingBoardClasses(
                  matchingBoardClass.source,
                  matchingBoardClass.listItem,
                )}
              >
                <span className={matchingBoardClass.sourceSelect}>
                  <strong>{member.label}</strong>
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className={matchingBoardClass.empty}>{board.sources.emptyText}</p>
        )}
      </section>
    </>
  );
}

function SummaryTable({
  summaryTable,
}: {
  summaryTable: MatchingScreenViewSummaryTable;
}) {
  return (
    <section
      className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5"
      aria-label={summaryTable.label}
    >
      <h2 className="font-semibold">{summaryTable.heading}</h2>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full border-collapse text-left text-sm">
          <thead>
            <tr>
              {summaryTable.columns.map((column) => (
                <th key={column} className="border-b p-2">
                  {column}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              {summaryTable.values.map((value, index) => (
                <td key={summaryTable.columns[index]} className="border-b p-2">
                  {value}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>
      <p className="mt-2 text-sm">{summaryTable.note}</p>
    </section>
  );
}

export function MatchingScreenView({
  model,
}: {
  model: MatchingScreenViewModel;
}) {
  const workspace = model.kind === "workspace";
  const spacedSections =
    workspace && Boolean(model.status || model.groupSelect || model.review);
  return (
    <section
      data-matching-screen={workspace ? "" : undefined}
      hidden={model.hidden}
      aria-labelledby={model.heading.id}
      className={`${workspace ? workspaceSectionClass : pageSectionClass}${spacedSections ? " space-y-4" : ""}`}
      onKeyDown={
        workspace
          ? (event) => {
              if (event.key === "Escape") model.onEscape?.();
            }
          : undefined
      }
    >
      <ScreenHeader heading={model.heading} workspace={workspace} />
      <MatchingScreenContent model={model} />
    </section>
  );
}

function MatchingScreenContent({ model }: { model: MatchingScreenViewModel }) {
  if (model.kind === "loading" || model.kind === "error") {
    return (
      <>
        <p role={model.kind === "loading" ? "status" : "alert"}>
          {model.message}
        </p>
        {model.kind === "error" && model.retry ? (
          <AppButton
            isDisabled={model.retry.disabled}
            onPress={model.retry.onPress}
          >
            {model.retry.label}
          </AppButton>
        ) : null}
      </>
    );
  }

  if (model.kind === "missing") {
    return model.message ? <p role="alert">{model.message}</p> : null;
  }

  if (model.kind === "receipt") {
    return <MatchingSavedReceiptView key={model.body.key} model={model.body} />;
  }

  const { inspector, recording, footer, preview } = model;
  const board = model.board;
  const recordingSearch = board?.recordingSearch;

  return (
    <>
      {model.feedback ? (
        <div className="mb-3 min-w-0" data-matching-feedback>
          <div
            role="status"
            aria-live="polite"
            className={
              model.feedback.visible
                ? "m-0 rounded-lg border border-[var(--line)] bg-[var(--accent-soft)] px-3.5 py-2 text-sm text-[var(--ink)]"
                : "m-0 min-h-0 text-sm text-[var(--muted)]"
            }
          >
            {model.feedback.text}
          </div>
        </div>
      ) : null}
      {model.summary ? (
        <div className="incoming-summary mb-4 flex flex-wrap items-center gap-x-6 gap-y-2 rounded-lg border border-[var(--line)] bg-[var(--raised)] px-4 py-2.5 text-sm text-[var(--muted)] min-[1400px]:shrink-0">
          <strong className="text-[var(--ink)]">
            {model.summary.assigned}
          </strong>
          <span>{model.summary.free}</span>
          <span>{model.summary.detail}</span>
        </div>
      ) : null}
      {model.status?.loading ? (
        <p role="status">{model.status.loading}</p>
      ) : null}
      {model.status?.error ? <p role="alert">{model.status.error}</p> : null}
      {model.draftHint ? (
        <p className="m-0 text-sm text-[var(--muted)]">{model.draftHint}</p>
      ) : null}
      {model.groupSelect ? (
        <label className="block max-w-2xl text-sm font-medium">
          {model.groupSelect.label}
          <select
            className="mt-1 block w-full rounded border border-[var(--control)] bg-[var(--surface)] p-2"
            value={model.groupSelect.value}
            onChange={(event) =>
              model.groupSelect?.onChange(event.target.value)
            }
          >
            {model.groupSelect.options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      {model.encoding ? (
        <IncomingTagEncodingReviewOutlet
          source={model.encoding}
          locale={model.locale}
        />
      ) : null}
      {model.evaluation?.loading ? (
        <p role="status">{model.evaluation.loading}</p>
      ) : null}
      {model.evaluation?.error ? (
        <div role="alert" className="space-y-2">
          <p>{model.evaluation.error.text}</p>
          <AppButton
            isDisabled={model.evaluation.error.retry.disabled}
            onPress={model.evaluation.error.retry.onPress}
          >
            {model.evaluation.error.retry.label}
          </AppButton>
        </div>
      ) : null}
      {model.evaluation?.notRun ? (
        <p className="rounded-lg border border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] p-3 text-sm text-[var(--warning)]">
          {model.evaluation.notRun}
        </p>
      ) : null}
      {model.authority ? (
        <section
          className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5"
          aria-labelledby="authority-title"
        >
          <h2 id="authority-title" className="font-semibold">
            {model.authority.heading}
          </h2>
          <p className="mt-1 text-sm">
            {model.authority.modeLabel} <strong>{model.authority.mode}</strong>
          </p>
          <p className="mt-1 text-sm">{model.authority.explanation}</p>
          {model.authority.notice ? (
            <p
              role="status"
              className="mt-2 rounded-lg border border-[color-mix(in_srgb,var(--success)_25%,transparent)] bg-[var(--success-soft)] p-3 text-sm text-[var(--success)]"
            >
              {model.authority.notice}
            </p>
          ) : null}
          {model.authority.returnToAutomatic ? (
            <div className="mt-3">
              <AppButton
                isDisabled={model.authority.returnToAutomatic.disabled}
                onPress={model.authority.returnToAutomatic.onPress}
              >
                {model.authority.returnToAutomatic.label}
              </AppButton>
            </div>
          ) : null}
          {model.authority.refresh ? (
            <AppButton
              isDisabled={model.authority.refresh.disabled}
              onPress={model.authority.refresh.onPress}
            >
              {model.authority.refresh.label}
            </AppButton>
          ) : null}
        </section>
      ) : null}
      {model.evaluation?.unavailable ? (
        <p className="rounded-lg border border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] p-3 text-sm text-[var(--warning)]">
          {model.evaluation.unavailable}
        </p>
      ) : null}
      {model.evaluation?.noReleases ? (
        <p className="rounded-lg border border-[var(--line)] p-3 text-sm">
          {model.evaluation.noReleases}
        </p>
      ) : null}
      <MatchingReviewSearchView
        locale={model.locale}
        createLocalReleaseHref={model.createLocalReleaseHref}
        search={model.search}
        preparation={model.preparation}
      >
        {model.review ? <ReviewSelection review={model.review} /> : null}
      </MatchingReviewSearchView>
      <MatchingWorkspaceLayout>
        {board ? (
          <MatchingBoard
            {...board}
            hidden={model.hidden}
            renderPositionMeta={(context) => (
              <>
                {board.renderPositionMeta?.(context)}
                {recordingSearch?.trackIds.includes(
                  context.position.track_id,
                ) ? (
                  <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
                    <AppButton
                      onPress={() =>
                        recordingSearch.onOpen(context.position.track_id)
                      }
                    >
                      {recordingSearch.label}
                    </AppButton>
                  </div>
                ) : null}
              </>
            )}
          />
        ) : model.emptyBoard ? (
          <EmptyBoard board={model.emptyBoard} />
        ) : null}
        <Inspector locale={model.locale} inspector={inspector} />
      </MatchingWorkspaceLayout>
      {model.summaryTable ? (
        <SummaryTable summaryTable={model.summaryTable} />
      ) : null}
      {recording && (
        <section
          className={`matching-recording ${surfacePanel} mt-3 min-[1400px]:max-h-[25dvh] min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto`}
        >
          <h2 className={sectionHeadingClass}>{recording.title}</h2>
          <form
            className="flex flex-wrap items-end gap-[7px] [&_.ui-field]:min-w-[190px] [&_.ui-field]:flex-1"
            onSubmit={(event) => {
              event.preventDefault();
              recording.onSubmit();
            }}
          >
            <TextField
              className={recordingFieldClass}
              aria-label={recording.ariaSearchLabel}
              value={recording.query}
              onChange={recording.onQueryChange}
            >
              <Label className="ui-label mb-1 block text-xs font-medium text-[var(--muted)]">
                {recording.queryLabel}
              </Label>
              <Input id="recording-query" className={recordingInputClass} />
            </TextField>
            <AppButton type="submit">{recording.searchLabel}</AppButton>
            <AppButton onPress={recording.onClose}>
              {recording.closeLabel}
            </AppButton>
          </form>
          <p>{recording.hint}</p>
          <ul>
            {recording.results.map((result) => (
              <li key={result.key}>
                {result.title} · {result.releaseTitle} · {result.duration}{" "}
                <AppButton onPress={result.onLink}>
                  {result.linkLabel}
                </AppButton>
              </li>
            ))}
          </ul>
        </section>
      )}
      {footer && (
        <div className="matching-commit m-0 mt-4 flex flex-wrap items-center justify-between gap-x-5 gap-y-2 rounded-xl border border-[var(--line)] bg-[var(--surface)] px-4 py-3 shadow-[var(--shadow)] max-[520px]:items-stretch">
          <strong className="text-sm font-semibold text-[var(--ink)]">
            {footer.assignedLabel}
          </strong>
          <span className="min-w-[220px] flex-1 text-xs text-[var(--muted)] max-[800px]:hidden">
            {footer.summary}
          </span>
          <AppButton
            className="demo-button max-[520px]:w-full"
            isDisabled={footer.previewDisabled}
            onPress={footer.onPreview}
          >
            {footer.previewLabel}
          </AppButton>
        </div>
      )}
      {preview && (
        <MatchingPreview
          locale={model.locale}
          headingRef={preview.headingRef}
          heading={preview.heading}
          releaseTitle={preview.releaseTitle}
          summary={preview.summary}
          rows={preview.rows}
          notice={preview.notice}
          primary={preview.primary}
          secondary={preview.secondary}
        >
          {preview.details?.items.length ? (
            <div className="mt-2 text-sm">
              <h3 className="font-medium">{preview.details.heading}</h3>
              <ul className="list-inside list-disc">
                {preview.details.items.map((item) => (
                  <li key={item.key}>
                    {item.title}: {item.text}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </MatchingPreview>
      )}
    </>
  );
}
