import { Checkbox } from "react-aria-components";
import { firstPublicationPreviewMessages } from "../library/firstPublicationPreviewMessages";
import {
  LibraryPositionConfirmButton,
  LibraryPositionPreviewButton,
} from "../library/LibraryPositionWorkspace";
import { releaseControlFieldsetClass } from "../library/libraryReleaseControlStyles";
import {
  type PublicationPreviewDetailsState,
  PublicationPreviewDetailsView,
  type PublicationPreviewSourceChoice,
} from "../library/PublicationPreviewDetailsView";

export type ContextPublicationPreviewWorkflow =
  | "first"
  | "comparison"
  | "matching_receipt"
  | "retained_same_path";

export type ContextPublicationPreviewMode = "ordinary" | "forced";

export interface ContextPublicationPreviewWorkflowControl {
  disabled: boolean;
  mode: ContextPublicationPreviewMode;
  namePrefix: string;
  onChange: (workflow: "comparison" | "retained_same_path") => void;
  onModeChange: (mode: ContextPublicationPreviewMode) => void;
  workflow: ContextPublicationPreviewWorkflow;
}

export interface ContextPublicationPreviewPlanControl {
  disabled: boolean;
  pending?: boolean;
  onPreview: () => void;
}

export interface ContextPublicationPreviewDowngradeControl {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onChange: (checked: boolean) => void;
}

export interface ContextPublicationPreviewConfirmControl {
  disabled: boolean;
  label: "publish" | "retainedRepublish";
  pending?: boolean;
  onConfirm: () => void;
}

export interface ContextPublicationPreviewViewProps {
  locale: "en" | "ru";
  error?: string;
  readOnly?: boolean;
  reason?: string;
  notices?: readonly string[];
  details?: PublicationPreviewDetailsState;
  sourceGroupName: string;
  onSourceChoice?: (choice: PublicationPreviewSourceChoice | null) => void;
  onRefresh?: () => void;
  workflowControl?: ContextPublicationPreviewWorkflowControl;
  planControl?: ContextPublicationPreviewPlanControl;
  downgradeControl?: ContextPublicationPreviewDowngradeControl;
  confirmControl?: ContextPublicationPreviewConfirmControl;
}

export function ContextPublicationPreviewView({
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
  planControl,
  downgradeControl,
  confirmControl,
}: ContextPublicationPreviewViewProps) {
  const copy = firstPublicationPreviewMessages[locale];

  return (
    <section className="min-w-0 space-y-3" aria-label={copy.section}>
      {workflowControl && (
        <>
          <fieldset
            className={`${releaseControlFieldsetClass} space-y-2`}
            disabled={workflowControl.disabled}
          >
            <legend className="font-medium">{copy.currentWorkflow}</legend>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`current-workflow-${workflowControl.namePrefix}`}
                checked={workflowControl.workflow === "comparison"}
                onChange={() => workflowControl.onChange("comparison")}
              />{" "}
              {copy.currentCompare}
            </label>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`current-workflow-${workflowControl.namePrefix}`}
                checked={workflowControl.workflow === "retained_same_path"}
                onChange={() => workflowControl.onChange("retained_same_path")}
              />{" "}
              {copy.retainedRepublish}
            </label>
          </fieldset>
          {workflowControl.workflow === "comparison" && (
            <p role="note">{copy.comparisonReadOnly}</p>
          )}
          <fieldset
            className={`${releaseControlFieldsetClass} space-y-2`}
            disabled={workflowControl.disabled}
          >
            <legend className="font-medium">{copy.mode}</legend>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`comparison-mode-${workflowControl.namePrefix}`}
                checked={workflowControl.mode === "ordinary"}
                onChange={() => workflowControl.onModeChange("ordinary")}
              />{" "}
              {copy.ordinary}
            </label>
            <label className="block">
              <input
                type="radio"
                className="accent-[var(--accent)]"
                name={`comparison-mode-${workflowControl.namePrefix}`}
                checked={workflowControl.mode === "forced"}
                onChange={() => workflowControl.onModeChange("forced")}
              />{" "}
              {copy.forced}
            </label>
          </fieldset>
        </>
      )}
      {planControl && (
        <LibraryPositionPreviewButton
          locale={locale}
          pending={planControl.pending}
          disabled={planControl.disabled}
          onPreview={planControl.onPreview}
        />
      )}
      {error && (
        <p role="alert" className="text-[var(--error)]">
          {error}
        </p>
      )}
      {readOnly && <p role="note">{copy.comparisonReadOnly}</p>}
      {(reason || notices?.length) && (
        <p>{[reason, ...(notices ?? [])].join(" · ")}</p>
      )}
      {details && (
        <PublicationPreviewDetailsView
          locale={locale}
          state={details}
          sourceGroupName={sourceGroupName}
          onSourceChoice={onSourceChoice}
          onRefresh={onRefresh}
        />
      )}
      {!readOnly && downgradeControl && (
        <Checkbox
          className="react-aria-Checkbox ui-checkbox inline-flex min-h-8 pointer-coarse:min-h-[40px] cursor-pointer items-center gap-[7px] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]"
          isSelected={downgradeControl.checked}
          isDisabled={Boolean(downgradeControl.disabled)}
          onChange={downgradeControl.onChange}
        >
          <span className="checkbox" />
          {downgradeControl.label}
        </Checkbox>
      )}
      {!readOnly && confirmControl && (
        <LibraryPositionConfirmButton
          locale={locale}
          label={confirmControl.label}
          pending={confirmControl.pending}
          disabled={confirmControl.disabled}
          onConfirm={confirmControl.onConfirm}
        />
      )}
    </section>
  );
}
