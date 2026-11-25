import { AppButton } from "../../../components/AppButton";
import type { Locale } from "../../../i18n/types";
import { releaseMetadataEditorMessages } from "./releaseMetadataEditorMessages";

export type MetadataEditorNoticesViewProps = {
  locale: Locale;
  loading: boolean;
  saving: boolean;
  readError: string;
  saveError: string;
  refreshConflict: boolean;
  onRetry: () => void;
};

/** Read/save/conflict notices for a scoped metadata editor card. */
export function MetadataEditorNoticesView({
  locale,
  loading,
  saving,
  readError,
  saveError,
  refreshConflict,
  onRetry,
}: MetadataEditorNoticesViewProps) {
  const text = releaseMetadataEditorMessages[locale];
  return (
    <>
      {loading ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {text.loading}
        </p>
      ) : null}
      {saving ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {text.saving}
        </p>
      ) : null}
      {readError ? (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="m-0 text-sm text-[var(--error)]">
            {text.readError} {readError}
          </p>
          <AppButton
            type="button"
            isDisabled={loading || saving}
            onPress={onRetry}
            className="min-w-0 justify-center"
          >
            {text.retry}
          </AppButton>
        </div>
      ) : null}
      {saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {refreshConflict ? text.conflict : text.saveError} {saveError}{" "}
          {text.draftRetained}
        </p>
      ) : null}
      {refreshConflict && !saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {text.conflict}
        </p>
      ) : null}
    </>
  );
}
