import {
  type ReactNode,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
} from "react";
import { AppButton } from "../../components/AppButton";
import { firstPublicationPreviewMessages } from "../library/firstPublicationPreviewMessages";
import {
  PublicationPreviewDetailsView,
  type PublicationPreviewSourceChoice,
} from "../library/PublicationPreviewDetailsView";
import type {
  ContextPublicationPreviewViewProps,
  ContextPublicationPreviewWorkflow,
} from "./ContextPublicationPreviewView";
import { contextPublicationPreviewMessages } from "./contextPublicationPreviewMessages";

export type ContextPublicationPreviewDialogControl = {
  open: boolean;
  onOpen: (
    workflow: Extract<
      ContextPublicationPreviewWorkflow,
      "comparison" | "retained_same_path" | "first"
    >,
  ) => void;
  onDismiss: () => void;
};

type DialogViewProps = Omit<
  ContextPublicationPreviewViewProps,
  "dialogControl"
> & {
  dialogControl: ContextPublicationPreviewDialogControl;
};

const focusableSelector =
  'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], summary, [tabindex]:not([tabindex="-1"])';

function choiceKey(choice: PublicationPreviewSourceChoice | null) {
  return choice
    ? `${choice.location_id}:${choice.media_variant_id}:${choice.stream_index}`
    : "unresolved";
}

function SourceChoice({
  details,
  locale,
  sourceGroupName,
  onSourceChoice,
}: Pick<
  DialogViewProps,
  "details" | "locale" | "sourceGroupName" | "onSourceChoice"
>) {
  const copy = firstPublicationPreviewMessages[locale];
  if (
    !details?.inspection.needsExplicitChoice ||
    !onSourceChoice ||
    details.inspection.sourceCandidates.length === 0
  )
    return null;
  const { sourceCandidates, selectedChoice, sourceChoiceDisabled } =
    details.inspection;

  return (
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
        const choice = {
          location_id: candidate.location_id,
          media_variant_id: candidate.media_variant_id,
          stream_index: candidate.stream_index,
        };
        return (
          <label className="block" key={choiceKey(choice)}>
            <input
              type="radio"
              name={sourceGroupName}
              checked={choiceKey(selectedChoice) === choiceKey(choice)}
              onChange={() => onSourceChoice(choice)}
            />{" "}
            <span className="wrap-anywhere">
              {candidate.title || candidate.codec} · {candidate.codec},{" "}
              {copy.stream} {candidate.stream_index}
              {candidate.language ? ` · ${candidate.language}` : ""}
              {candidate.channels ? ` · ${candidate.channels}` : ""}
            </span>
          </label>
        );
      })}
    </fieldset>
  );
}

function SummaryStep({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="min-w-0 flex-1 rounded border border-[var(--line)] bg-[var(--raised)] p-2">
      <p className="m-0 text-xs font-medium text-[var(--muted)]">{label}</p>
      <div className="mt-1 min-w-0 text-sm">{children}</div>
    </div>
  );
}

function PublicationSummary({
  details,
  locale,
  reason,
  readOnly,
}: Pick<DialogViewProps, "details" | "locale" | "reason" | "readOnly">) {
  const copy = contextPublicationPreviewMessages[locale];
  const inspection = details?.inspection;
  const preview = inspection?.preview;
  const source = preview?.selected_source;
  const destination =
    preview?.resolved_path ??
    (preview?.resolved_path_components?.length
      ? preview.resolved_path_components.join("/")
      : preview?.resolved_path_components
        ? firstPublicationPreviewMessages[locale].namespaceUnconfirmed
        : firstPublicationPreviewMessages[locale].undefined);
  const extension =
    destination && /\.([^./\\]+)$/.exec(destination)?.[1].toUpperCase();
  const container =
    preview?.transform?.output_container?.toUpperCase() ?? extension;
  const comparison = preview?.current_comparison;
  const coverDiffers =
    Boolean(preview?.asset_changes?.cover) ||
    comparison?.cover_status === "differs";
  const lyricsDiffers =
    Boolean(preview?.asset_changes?.lyrics) ||
    comparison?.lyrics_status === "differs";
  const changeCount =
    (comparison?.tag_differences?.length ?? 0) +
    Number(coverDiffers) +
    Number(lyricsDiffers);
  const changes =
    !details?.loading && changeCount > 0
      ? copy.changesCount(changeCount)
      : !details?.loading &&
          comparison?.tag_differences &&
          comparison.cover_status === "matches" &&
          comparison.lyrics_status === "matches"
        ? copy.noChanges
        : !details?.loading && preview && inspection?.workflow === "first"
          ? copy.firstPublicationChanges
          : copy.changesUnavailable;

  return (
    <section className="space-y-2" aria-label={copy.summary}>
      <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-stretch">
        <SummaryStep label={copy.sourceStep}>
          {source ? (
            <>
              <p className="m-0">
                {source.codec}
                {source.stream_index !== undefined &&
                  ` · ${firstPublicationPreviewMessages[locale].stream} ${source.stream_index}`}
              </p>
              {source.path && (
                <code className="mt-1 block wrap-anywhere">{source.path}</code>
              )}
            </>
          ) : (
            <p className="m-0">
              {firstPublicationPreviewMessages[locale].unselected}
            </p>
          )}
        </SummaryStep>
        <span
          aria-hidden="true"
          className="self-center text-center text-[var(--muted)]"
        >
          →
        </span>
        <SummaryStep label={copy.destinationStep}>
          <code className="block wrap-anywhere">{destination}</code>
          {container && (
            <p className="m-0 mt-1 text-xs text-[var(--muted)]">
              {copy.formatLabel}: {container}
            </p>
          )}
        </SummaryStep>
        <span
          aria-hidden="true"
          className="self-center text-center text-[var(--muted)]"
        >
          →
        </span>
        <SummaryStep
          label={readOnly ? copy.potentialChanges : copy.changesStep}
        >
          {reason ? <p className="m-0">{reason}</p> : null}
          <p
            className={reason ? "m-0 mt-1 text-xs text-[var(--muted)]" : "m-0"}
          >
            {changes}
          </p>
        </SummaryStep>
      </div>
    </section>
  );
}

type PublicationAction = "compare" | "republish" | "preview";

function PreviewDialog({
  locale,
  error,
  readOnly,
  reason,
  notices,
  details,
  sourceGroupName,
  onSourceChoice,
  onRefresh,
  workflowControl,
  confirmControl,
  onDismiss,
}: Omit<DialogViewProps, "dialogControl"> & {
  onDismiss: () => void;
}) {
  const copy = firstPublicationPreviewMessages[locale];
  const dialogCopy = contextPublicationPreviewMessages[locale];
  const title = useId();
  const description = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const region = useRef<HTMLElement | null>(null);
  const workflow = details?.inspection.workflow;
  const returnFocusAction: PublicationAction = readOnly
    ? "compare"
    : workflowControl
      ? "republish"
      : "preview";

  useLayoutEffect(() => {
    const node = dialog.current;
    if (!node || node.open) return;
    region.current = node.closest<HTMLElement>("section[aria-labelledby]");
    node.showModal();
    heading.current?.focus();
    return () => {
      if (node.open) node.close();
    };
  }, []);

  useEffect(() => {
    return () => {
      const action = returnFocusAction;
      const scope = sourceGroupName;
      queueMicrotask(() => {
        const target = [
          ...document.querySelectorAll<HTMLElement>(
            `[data-publication-action="${action}"]`,
          ),
        ].find(
          (element) => element.getAttribute("data-publication-scope") === scope,
        );
        if (target?.isConnected && !target.closest("[hidden]")) {
          target.focus();
          return;
        }
        const container = region.current;
        if (!container?.isConnected || container.closest("[hidden]")) return;
        const hadTabIndex = container.hasAttribute("tabindex");
        if (!hadTabIndex) container.setAttribute("tabindex", "-1");
        container.focus();
        if (!hadTabIndex) container.removeAttribute("tabindex");
      });
    };
  }, [returnFocusAction, sourceGroupName]);

  useLayoutEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  useLayoutEffect(() => {
    if (dialog.current?.closest("[hidden]")) onDismiss();
  });

  const titleText = readOnly
    ? dialogCopy.comparisonTitle
    : workflowControl
      ? dialogCopy.republishTitle
      : dialogCopy.firstTitle;
  const republish =
    workflow === "retained_same_path" ||
    workflowControl?.workflow === "retained_same_path";
  const disclosureDetails = details
    ? {
        ...details,
        loading: false,
        inspection: {
          ...details.inspection,
          needsExplicitChoice: false,
          preview: details.inspection.preview
            ? {
                ...details.inspection.preview,
                confirmation_capability: undefined,
              }
            : undefined,
        },
      }
    : undefined;

  return (
    <dialog
      ref={dialog}
      aria-modal="true"
      aria-labelledby={title}
      aria-describedby={description}
      className="m-auto max-h-[calc(100dvh-24px)] w-[min(760px,calc(100vw-24px))] overflow-hidden rounded-md border border-[var(--line)] bg-[var(--surface)] p-0 text-sm text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)] backdrop:bg-[rgb(0_0_0/0.45)] open:flex open:flex-col"
      onCancel={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onDismiss();
      }}
      onKeyDown={(event) => {
        if (
          event.target instanceof Element &&
          event.target.closest("dialog") !== event.currentTarget
        )
          return;
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onDismiss();
          return;
        }
        if (event.key !== "Tab") return;
        const controls =
          event.currentTarget.querySelectorAll<HTMLElement>(focusableSelector);
        if (controls.length === 0) return;
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        } else if (!event.currentTarget.contains(document.activeElement)) {
          event.preventDefault();
          (event.shiftKey ? last : first).focus();
        }
      }}
    >
      <div className="min-h-0 flex-1 space-y-3 overflow-auto overscroll-contain p-4">
        <h2
          ref={heading}
          id={title}
          tabIndex={-1}
          className="m-0 wrap-anywhere text-base font-semibold"
        >
          {titleText}
        </h2>
        <p id={description} className="m-0">
          {readOnly
            ? copy.comparisonReadOnly
            : workflowControl
              ? copy.retainedPrestartRevalidation
              : dialogCopy.firstDescription}
        </p>
        {error && (
          <p
            ref={alert}
            role="alert"
            tabIndex={-1}
            className="m-0 wrap-anywhere text-[var(--error)]"
          >
            {error}
          </p>
        )}
        {(!details || details.loading) && (
          <p role="status" className="m-0">
            {copy.loading}
          </p>
        )}
        <PublicationSummary
          details={details}
          locale={locale}
          reason={reason}
          readOnly={readOnly}
        />
        {workflow === "retained_same_path" &&
          details?.inspection.preview?.confirmation_capability && (
            <p role="status" className="m-0">
              {details.inspection.preview.confirmation_capability.status ===
              "ready"
                ? copy.retainedCapabilityReady
                : details.inspection.preview.confirmation_capability.status ===
                    "unchanged"
                  ? copy.retainedUnchanged
                  : copy.retainedBlocked}
            </p>
          )}
        {notices?.length ? (
          <ul aria-label={copy.warnings} className="m-0 list-disc pl-5">
            {notices.map((notice) => (
              <li key={notice} className="wrap-anywhere">
                {notice}
              </li>
            ))}
          </ul>
        ) : null}
        <SourceChoice
          details={details}
          locale={locale}
          sourceGroupName={sourceGroupName}
          onSourceChoice={onSourceChoice}
        />
        {!readOnly && workflowControl && (
          <fieldset
            className="space-y-2 rounded border border-[var(--line)] p-3"
            disabled={workflowControl.disabled}
          >
            <legend className="font-medium">{dialogCopy.mode}</legend>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`republish-mode-${workflowControl.namePrefix}`}
                checked={workflowControl.mode === "ordinary"}
                onChange={() => workflowControl.onModeChange("ordinary")}
              />{" "}
              {copy.ordinary}
            </label>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`republish-mode-${workflowControl.namePrefix}`}
                checked={workflowControl.mode === "forced"}
                onChange={() => workflowControl.onModeChange("forced")}
              />{" "}
              {copy.forced}
            </label>
          </fieldset>
        )}
        {disclosureDetails && (
          <details className="min-w-0 rounded border border-[var(--line)] p-2">
            <summary className="cursor-pointer font-medium">
              {dialogCopy.details}
            </summary>
            <div className="mt-3 min-w-0">
              <PublicationPreviewDetailsView
                locale={locale}
                state={disclosureDetails}
                sourceGroupName={sourceGroupName}
                onSourceChoice={onSourceChoice}
                onRefresh={onRefresh}
              />
            </div>
          </details>
        )}
      </div>
      <div className="flex shrink-0 flex-wrap justify-end gap-2 border-t border-[var(--line)] bg-[var(--surface)] p-3">
        {!readOnly && confirmControl && (
          <AppButton
            type="button"
            data-publication-confirm
            variant="primary"
            isDisabled={confirmControl.pending || confirmControl.disabled}
            onPress={confirmControl.onConfirm}
          >
            {confirmControl.pending
              ? copy.publicationStart
              : republish
                ? dialogCopy.republishConfirm
                : copy.publish}
          </AppButton>
        )}
        <AppButton type="button" onPress={onDismiss}>
          {dialogCopy.close}
        </AppButton>
      </div>
    </dialog>
  );
}

export function ContextPublicationPreviewDialogView({
  dialogControl,
  ...props
}: DialogViewProps) {
  const dialogCopy = contextPublicationPreviewMessages[props.locale];
  const hasCurrent = Boolean(props.workflowControl);

  const open = (
    workflow: Parameters<ContextPublicationPreviewDialogControl["onOpen"]>[0],
  ) => {
    dialogControl.onOpen(workflow);
  };

  return (
    <section className="min-w-0 space-y-3" aria-label={dialogCopy.actions}>
      <div className="flex flex-wrap gap-2">
        {hasCurrent ? (
          <>
            <AppButton
              type="button"
              data-publication-action="compare"
              data-publication-scope={props.sourceGroupName}
              aria-haspopup="dialog"
              onPress={() => open("comparison")}
            >
              {dialogCopy.compareWithCurrent}
            </AppButton>
            <AppButton
              type="button"
              data-publication-action="republish"
              data-publication-scope={props.sourceGroupName}
              aria-haspopup="dialog"
              variant="primary"
              onPress={() => open("retained_same_path")}
            >
              {dialogCopy.prepareRepublish}
            </AppButton>
          </>
        ) : (
          <AppButton
            type="button"
            data-publication-action="preview"
            data-publication-scope={props.sourceGroupName}
            aria-haspopup="dialog"
            onPress={() => open("first")}
          >
            {dialogCopy.previewPublication}
          </AppButton>
        )}
      </div>
      {dialogControl.open && (
        <PreviewDialog {...props} onDismiss={dialogControl.onDismiss} />
      )}
    </section>
  );
}
