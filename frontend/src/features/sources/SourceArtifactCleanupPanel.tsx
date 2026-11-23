import { useMemo } from "react";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceArtifactCleanupView } from "./SourceArtifactCleanupView";
import { renderSourceError } from "./sourcesApi";
import { useSourceArtifactCleanup } from "./useSourceArtifactCleanup";

export function SourceArtifactCleanupPanel() {
  const { locale } = useI18n();
  const cleanup = useSourceArtifactCleanup();
  const error = useMemo(
    () =>
      cleanup.error
        ? { text: renderSourceError(cleanup.error, locale) }
        : undefined,
    [cleanup.error, locale],
  );

  return (
    <SourceArtifactCleanupView
      locale={locale}
      candidates={cleanup.candidates}
      batchCount={cleanup.batchCount}
      selected={cleanup.selected}
      submitted={cleanup.submitted}
      operation={cleanup.operation}
      results={cleanup.results}
      loading={cleanup.loading}
      busy={cleanup.busy}
      isPending={cleanup.isPending}
      error={error}
      streamError={cleanup.streamError}
      onLoad={() => void cleanup.load()}
      onToggle={cleanup.toggle}
      onStart={(ids) => void cleanup.start(ids)}
    />
  );
}
