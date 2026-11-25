import { type ReactNode, useMemo } from "react";
import { useI18n } from "../../app/i18n/host";
import { SourceCleanupDataBinder } from "../../ui/features/sources/SourceListPageView";
import { renderSourceError } from "./sourcesApi";
import { useSourceArtifactCleanup } from "./useSourceArtifactCleanup";

/**
 * Binds the cleanup hook to the portable list page; it never renders the view so
 * the page mounts this hook only while the loaded list is on screen.
 */
export function SourceArtifactCleanupPanel({
  children,
}: {
  children: ReactNode;
}) {
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
    <SourceCleanupDataBinder
      model={{
        locale,
        candidates: cleanup.candidates,
        batchCount: cleanup.batchCount,
        selected: cleanup.selected,
        submitted: cleanup.submitted,
        operation: cleanup.operation,
        results: cleanup.results,
        loading: cleanup.loading,
        busy: cleanup.busy,
        isPending: cleanup.isPending,
        error,
        streamError: cleanup.streamError,
        onLoad: () => void cleanup.load(),
        onToggle: cleanup.toggle,
        onStart: (ids) => void cleanup.start(ids),
      }}
    >
      {children}
    </SourceCleanupDataBinder>
  );
}
