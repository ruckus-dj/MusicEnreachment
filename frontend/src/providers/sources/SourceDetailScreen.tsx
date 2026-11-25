import {
  useIsMutating,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  deleteSource,
  getGetSourceQueryKey,
  getSource,
  updateSource,
} from "../../api/generated/client";
import { useI18n } from "../../app/i18n/host";
import { useViewFocus } from "../../ui/components/useViewFocus";
import type { SourceRootChange } from "../../ui/features/sources/SourceRootSettingsView";
import { sourceMessage } from "../../ui/features/sources/sourceMessages";
import { browseServerDirectory } from "../components/DirectoryPicker";
import { incomingGroupCountQueryKey } from "../incoming/incomingGroupsQueryKeys";
import { SourceLocations } from "./SourceLocations";
import { useSourceScanPanel } from "./SourceScanControl";
import { useSourceScans } from "./sourceScans";
import {
  inventoryLabel,
  lastScanLabel,
  message,
  renderSourceError,
  sourceError,
  sourceFailure,
  sourceResponseError,
  sourceSettingsMutationKey,
} from "./sourcesApi";
import { SourcesBinder, SourcesOutlet } from "./sourcesOutlet";

export function SourceDetailScreen({ sourceId }: { sourceId: string }) {
  const { locale } = useI18n();
  const queryClient = useQueryClient();
  const scanRevision = useSourceScans().rootRevision(sourceId);
  const previousScanRevision = useRef(scanRevision);
  const queryKey = getGetSourceQueryKey(sourceId);
  const mutationFilter = {
    mutationKey: [...sourceSettingsMutationKey, sourceId],
    exact: true,
  };
  const saving = useIsMutating(mutationFilter) !== 0;
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const [removal, setRemoval] = useState<{
    sourceId: string;
    path: string;
    locationCount: number;
  } | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<ReturnType<
    typeof message
  > | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  function refreshIncomingGroupCount() {
    void queryClient.invalidateQueries({
      queryKey: incomingGroupCountQueryKey(),
      exact: true,
    });
  }

  const rootQuery = useQuery({
    queryKey,
    // This read and the write fence outlive a card mount: back/forward during a
    // write must not start a stale GET or lose the authoritative PATCH result.
    enabled: !saving,
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async ({ signal }) => {
      const response = await getSource(sourceId, {
        cache: "no-store",
        signal,
      });
      if (response.status !== 200) throw sourceFailure("read", response);
      return response.data;
    },
  });
  const root = rootQuery.data;
  const { error, isFetching: reading } = rootQuery;
  const busy = reading || saving || deleting;
  const scanModel = useSourceScanPanel(root, saving);

  const settings = useMutation({
    mutationKey: mutationFilter.mutationKey,
    networkMode: "always",
    retry: false,
    mutationFn: async (change: SourceRootChange) => {
      await queryClient.cancelQueries({ queryKey, exact: true });
      const body =
        change.field === "name"
          ? { display_name: change.value }
          : change.field === "path"
            ? { configured_path: change.value }
            : { processing_mode: change.value };
      const response = await updateSource(sourceId, body);
      if (response.status === 409)
        throw sourceResponseError(response, sourceError("sourceEditConflict"));
      if (response.status === 400 && change.field !== "path")
        throw sourceResponseError(response, sourceError("editUnavailable"));
      if (response.status !== 200) throw sourceFailure("edit", response);
      return response.data;
    },
    onSuccess: (next) => {
      queryClient.setQueryData(queryKey, next);
      refreshIncomingGroupCount();
    },
    onSettled: () => {
      // Covers a scan that completed while the query was disabled and a write
      // with a missing reply. Re-enable then reread; never retry the write.
      void queryClient.invalidateQueries({
        queryKey,
        exact: true,
        refetchType: "none",
      });
    },
  });

  useViewFocus(heading, true);
  useEffect(() => {
    if (scanRevision === previousScanRevision.current) return;
    previousScanRevision.current = scanRevision;
    void queryClient.invalidateQueries({
      queryKey: getGetSourceQueryKey(sourceId),
      exact: true,
    });
  }, [queryClient, scanRevision, sourceId]);
  function backToList() {
    window.location.hash = "/sources";
  }

  async function save(change: SourceRootChange): Promise<boolean> {
    if (removal || queryClient.isMutating(mutationFilter)) return false;
    try {
      await settings.mutateAsync(change);
      return true;
    } catch {
      return false;
    }
  }

  function closeDelete() {
    setDeleteError(null);
    setRemoval(null);
  }

  async function confirmDelete() {
    if (!removal || busy || queryClient.isMutating(mutationFilter)) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      const response = await deleteSource(removal.sourceId, {
        confirmed_path: removal.path,
        confirmed_location_count: removal.locationCount,
      });
      if (response.status !== 204) throw sourceFailure("delete", response);
      refreshIncomingGroupCount();
      queryClient.removeQueries({ queryKey, exact: true });
      setRemoval(null);
      backToList();
    } catch (reason) {
      setDeleteError(message(reason));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <SourcesBinder
      page={{
        view: "detail",
        detail: "root",
        locale,
        headingId: "source-title",
        heading: {
          title: root ? root.display_name : t("sourceTitle"),
          path: root?.configured_path,
          headingRef: heading,
          headingId: "source-title",
          sourcesHref: "#/sources",
          incomingHref:
            root?.status === "available" ? "#/incoming-groups" : undefined,
        },
        feedback: {
          requestRunning: Boolean(root) && (reading || deleting),
          loading: !root && !error,
          error: error ? renderSourceError(message(error), locale) : undefined,
          retryDisabled: saving,
          onRetry: () => void rootQuery.refetch(),
        },
        scan: scanModel,
        inventory: root
          ? {
              Component: SourceLocations,
              props: {
                sourceId,
                root,
                refreshToken: rootQuery.dataUpdatedAt,
              },
            }
          : undefined,
        overview: root
          ? {
              titleId: "source-summary-title",
              name: root.display_name,
              path: root.configured_path,
              mode: root.processing_mode,
              availability: root.status,
              availabilityNote:
                root.status === "unavailable" ? root.safe_error : undefined,
              lastScan: {
                label: lastScanLabel(root, locale),
                dateTime: root.last_successful_scan_at,
              },
              inventory: {
                label: inventoryLabel(root, locale),
                path: root.inventory_path,
                stale: root.stale,
              },
              onRemove: () => {
                setDeleteError(null);
                setRemoval({
                  sourceId,
                  path: root.configured_path,
                  locationCount: root.location_count,
                });
              },
            }
          : undefined,
        settings: root
          ? {
              name: root.display_name,
              path: root.configured_path,
              mode: root.processing_mode,
              browse: browseServerDirectory,
              disabled: removal !== null || (saving && !settings.isPending),
              error: settings.error
                ? renderSourceError(message(settings.error), locale)
                : undefined,
              onSave: save,
            }
          : undefined,
        removal: removal
          ? {
              path: removal.path,
              count: removal.locationCount,
              busy,
              error: deleteError
                ? renderSourceError(deleteError, locale)
                : undefined,
              onCancel: closeDelete,
              onConfirm: () => void confirmDelete(),
            }
          : undefined,
      }}
    >
      <SourcesOutlet />
    </SourcesBinder>
  );
}
