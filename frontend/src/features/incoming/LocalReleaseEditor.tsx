import { useEffect, useRef, useState } from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { createUUID } from "../../api/client/uuid";
import {
  confirmIncomingGroupLocalMatching,
  listLocalProviderReleases,
  previewIncomingGroupLocalMatching,
  readLocalProviderRelease,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type LocalMatchingMessages,
  localMatchingMessages,
} from "./localMatchingMessages";

type Assignment = { file_member_id: string; provider_track_id: string };
type PreviewBody = {
  provider_release_id: string;
  expected_group_revision: string;
  expected_authority_revision: number;
  assignments: Assignment[];
};
type ConfirmBody = PreviewBody & {
  preview_digest: string;
  request_id: string;
};
type PreviewState = {
  scope: string;
  body: PreviewBody;
  response: MatchingLocalPreview;
};
type PendingConfirmation = { scope: string; body: ConfirmBody };
type EditorState = {
  selectedReleaseID: string;
  release?: LocalProviderItem;
  assignments: Record<string, string>;
  preview?: PreviewState;
  pending?: PendingConfirmation;
  receipt?: MatchingLocalPreview;
  busy?: "preview" | "confirm";
  busyRequest?: number;
  error?: string;
  notice?: string;
  stale?: boolean;
};
type CatalogState = {
  items?: LocalProviderItem[];
  busy?: boolean;
  error?: string;
};

const emptyEditorState = (): EditorState => ({
  selectedReleaseID: "",
  assignments: {},
});

function message(reason: unknown, fallback: string) {
  return sharedErrorMessage(reason, fallback) || fallback;
}

function responseMessage(
  response: { data: { detail?: string }; status: number },
  fallback: string,
  copy: LocalMatchingMessages,
) {
  return apiResponseError(response, copy.httpError(fallback, response.status))
    .message;
}

function fileLabel(file: IncomingFileResponse) {
  return file.locations?.[0]?.relative_path || file.variant_id;
}

function trackLabel(track: NonNullable<LocalProviderItem["tracks"]>[number]) {
  return `${track.position}. ${track.title_override || track.recording_title}`;
}

export interface LocalReleaseEditorProps {
  group: IncomingGroupResponse;
  files: IncomingFileResponse[];
  authorityRevision: number | undefined;
  onConfirmed?: (receipt: MatchingLocalPreview) => void | Promise<void>;
}

export function LocalReleaseEditor({
  group,
  files,
  authorityRevision,
  onConfirmed,
}: LocalReleaseEditorProps) {
  const { locale } = useI18n();
  const copy = localMatchingMessages[locale];
  const [states, setStates] = useState<Record<string, EditorState>>({});
  const [catalogs, setCatalogs] = useState<Record<string, CatalogState>>({});
  const sequence = useRef(0);
  const catalogRequests = useRef<Record<string, number>>({});
  const detailRequest = useRef(0);
  const previousScopes = useRef<Record<string, string>>({});
  const selectedReleaseRef = useRef("");
  const currentScope = `${group.id}:${group.revision}:${authorityRevision === undefined ? "unknown" : authorityRevision}`;
  const currentScopeRef = useRef(currentScope);
  currentScopeRef.current = currentScope;
  const state = states[group.id] ?? emptyEditorState();
  selectedReleaseRef.current = state.selectedReleaseID;
  const scopedFiles = files.filter((file) =>
    group.members?.includes(file.variant_id),
  );
  const tracks = state.release?.tracks ?? [];
  const confirmationInFlight = state.busy === "confirm";

  function update(updateState: (current: EditorState) => EditorState) {
    setStates((current) => ({
      ...current,
      [group.id]: updateState(current[group.id] ?? emptyEditorState()),
    }));
  }

  useEffect(() => {
    const previous = previousScopes.current[group.id];
    previousScopes.current[group.id] = currentScope;
    if (previous === undefined || previous === currentScope) return;
    sequence.current += 1;
    setStates((current) => {
      const existing = current[group.id] ?? emptyEditorState();
      return {
        ...current,
        [group.id]: {
          ...existing,
          preview: undefined,
          stale: true,
          notice: existing.receipt
            ? copy.scopeChangedAfterConfirm
            : copy.scopeChangedBeforeConfirm,
        },
      };
    });
  }, [copy, currentScope, group.id]);

  async function loadCatalog() {
    const request = (catalogRequests.current[group.id] ?? 0) + 1;
    catalogRequests.current[group.id] = request;
    setCatalogs((current) => ({
      ...current,
      [group.id]: { ...current[group.id], busy: true, error: undefined },
    }));
    try {
      const items: LocalProviderItem[] = [];
      let offset = 0;
      let total = Number.POSITIVE_INFINITY;
      while (offset < total) {
        const response = await listLocalProviderReleases(
          { provider: "local", limit: 100, offset },
          { cache: "no-store" },
        );
        if (request !== catalogRequests.current[group.id]) return;
        if (response.status !== 200) {
          setCatalogs((current) => ({
            ...current,
            [group.id]: {
              ...current[group.id],
              error: responseMessage(response, copy.loadReleasesError, copy),
            },
          }));
          return;
        }
        const page = response.data.items ?? [];
        items.push(...page);
        total = response.data.total;
        offset += page.length;
        if (page.length === 0) break;
      }
      if (request !== catalogRequests.current[group.id]) return;
      setCatalogs((current) => ({
        ...current,
        [group.id]: { items },
      }));
    } catch (reason) {
      if (request === catalogRequests.current[group.id]) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: {
            ...current[group.id],
            error: message(reason, copy.loadReleasesError),
          },
        }));
      }
    } finally {
      if (request === catalogRequests.current[group.id]) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: { ...current[group.id], busy: false },
        }));
      }
    }
  }

  async function selectRelease(releaseID: string) {
    if (state.pending) return;
    const request = ++detailRequest.current;
    selectedReleaseRef.current = releaseID;
    sequence.current += 1;
    update((current) => ({
      ...current,
      selectedReleaseID: releaseID,
      release: undefined,
      assignments: {},
      preview: undefined,
      receipt: undefined,
      error: undefined,
      notice: undefined,
      stale: false,
    }));
    if (!releaseID) return;
    try {
      const response = await readLocalProviderRelease(releaseID, {
        cache: "no-store",
      });
      if (
        request !== detailRequest.current ||
        selectedReleaseRef.current !== releaseID
      )
        return;
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          error: responseMessage(response, copy.releaseReadError, copy),
        }));
        return;
      }
      update((current) => ({ ...current, release: response.data }));
    } catch (reason) {
      if (
        request === detailRequest.current &&
        selectedReleaseRef.current === releaseID
      ) {
        update((current) => ({
          ...current,
          error: message(reason, copy.releaseReadError),
        }));
      }
    }
  }

  function makePreviewBody(): PreviewBody | undefined {
    if (authorityRevision === undefined) {
      update((current) => ({
        ...current,
        error: copy.authorityNotReady,
      }));
      return undefined;
    }
    if (!state.release || !state.selectedReleaseID) {
      update((current) => ({
        ...current,
        error: copy.selectReleaseFirst,
      }));
      return undefined;
    }
    const allowedMembers = new Set(scopedFiles.map((file) => file.variant_id));
    const allowedTracks = new Set(tracks.map((track) => track.id));
    const assignments = Object.entries(state.assignments)
      .filter(
        ([fileID, trackID]) =>
          allowedMembers.has(fileID) && allowedTracks.has(trackID),
      )
      .map(([file_member_id, provider_track_id]) => ({
        file_member_id,
        provider_track_id,
      }));
    if (assignments.length === 0) {
      update((current) => ({
        ...current,
        error: copy.assignAtLeastOne,
      }));
      return undefined;
    }
    return {
      provider_release_id: state.selectedReleaseID,
      expected_group_revision: group.revision,
      expected_authority_revision: authorityRevision,
      assignments,
    };
  }

  async function preview() {
    if (state.pending) return;
    const body = makePreviewBody();
    if (!body) return;
    const request = ++sequence.current;
    const requestedScope = currentScope;
    update((current) => ({
      ...current,
      busy: "preview",
      busyRequest: request,
      error: undefined,
      notice: undefined,
      stale: false,
    }));
    try {
      const response = await previewIncomingGroupLocalMatching(group.id, body);
      if (
        request !== sequence.current ||
        currentScopeRef.current !== requestedScope
      )
        return;
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          busy: undefined,
          busyRequest: undefined,
          preview: undefined,
          pending: response.status === 409 ? undefined : current.pending,
          stale: response.status === 409,
          error:
            response.status === 409
              ? copy.previewConflict
              : responseMessage(response, copy.previewError, copy),
        }));
        return;
      }
      update((current) => ({
        ...current,
        busy: undefined,
        busyRequest: undefined,
        preview: { scope: requestedScope, body, response: response.data },
        pending: undefined,
        receipt: undefined,
      }));
    } catch (reason) {
      if (request === sequence.current) {
        update((current) => ({
          ...current,
          busy: undefined,
          busyRequest: undefined,
          error: message(reason, copy.previewError),
        }));
      }
    } finally {
      update((current) =>
        current.busyRequest === request
          ? { ...current, busy: undefined, busyRequest: undefined }
          : current,
      );
    }
  }

  async function confirm() {
    const pending =
      state.pending ??
      (state.preview?.scope === currentScope
        ? {
            scope: currentScope,
            body: {
              ...state.preview.body,
              preview_digest: state.preview.response.preview_digest,
              request_id: createUUID(),
            },
          }
        : undefined);
    if (!pending || state.busy) return;
    if (!state.pending) update((current) => ({ ...current, pending }));
    update((current) => ({ ...current, busy: "confirm", error: undefined }));
    try {
      const response = await confirmIncomingGroupLocalMatching(
        group.id,
        pending.body,
      );
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          busy: undefined,
          stale: response.status === 409,
          preview: response.status === 409 ? undefined : current.preview,
          pending: response.status === 409 ? undefined : current.pending,
          error:
            response.status === 409
              ? copy.confirmConflict
              : responseMessage(response, copy.confirmError, copy),
        }));
        return;
      }
      update((current) => ({
        ...current,
        busy: undefined,
        receipt: response.data,
        pending: undefined,
        preview: undefined,
        notice: copy.confirmed,
      }));
      try {
        await onConfirmed?.(response.data);
      } catch (reason) {
        update((current) => ({
          ...current,
          notice: copy.confirmedRefreshError(
            message(reason, copy.refreshFailure),
          ),
        }));
      }
    } catch (reason) {
      update((current) => ({
        ...current,
        busy: undefined,
        error: `${message(reason, copy.confirmError)} ${copy.confirmRetryNotice}`,
      }));
    }
  }

  const previewCurrent =
    state.preview?.scope === currentScope ? state.preview : undefined;
  const canPreview =
    authorityRevision !== undefined &&
    state.busy === undefined &&
    !confirmationInFlight &&
    state.pending === undefined;

  return (
    <section
      aria-labelledby={`local-release-${group.id}`}
      className="space-y-4 rounded border border-stone-300 p-4"
    >
      <h2 id={`local-release-${group.id}`} className="text-lg font-semibold">
        {copy.heading}
      </h2>
      <p>
        {copy.intro}{" "}
        {state.pending ? (
          copy.ownCatalog
        ) : (
          <a className="underline" href="#/own-catalog">
            {copy.ownCatalog}
          </a>
        )}
        .
      </p>

      <fieldset
        disabled={state.pending !== undefined || confirmationInFlight}
        className="space-y-3 rounded border border-stone-300 p-3"
      >
        <legend className="font-medium">{copy.releaseLegend}</legend>
        <AppButton
          type="button"
          isDisabled={catalogs[group.id]?.busy}
          onPress={loadCatalog}
        >
          {catalogs[group.id]?.busy ? copy.loadingReleases : copy.loadReleases}
        </AppButton>
        {catalogs[group.id]?.error && (
          <p role="alert">{catalogs[group.id].error}</p>
        )}
        {catalogs[group.id]?.items && (
          <label className="block">
            {copy.releaseLabel}
            <select
              className="block w-full rounded border p-2"
              value={state.selectedReleaseID}
              onChange={(event) => void selectRelease(event.target.value)}
            >
              <option value="">{copy.chooseRelease}</option>
              {catalogs[group.id]?.items?.map((release) => (
                <option key={release.id} value={release.id}>
                  {release.title || release.id}
                </option>
              ))}
            </select>
          </label>
        )}
        {state.selectedReleaseID && !state.release && !state.error && (
          <p role="status">{copy.loadingTracks}</p>
        )}
        {state.release && (
          <p role="status">
            {copy.releaseTrackCount(state.release.title, tracks.length)}
          </p>
        )}
      </fieldset>

      {state.release && (
        <fieldset
          disabled={state.pending !== undefined || confirmationInFlight}
          className="space-y-3 rounded border border-stone-300 p-3"
        >
          <legend className="font-medium">{copy.assignmentsLegend}</legend>
          {scopedFiles.map((file) => (
            <label className="block" key={file.variant_id}>
              {fileLabel(file)}
              <select
                className="block w-full rounded border p-2"
                aria-label={copy.trackForFile(fileLabel(file))}
                value={state.assignments[file.variant_id] ?? ""}
                onChange={(event) => {
                  if (state.pending) return;
                  sequence.current += 1;
                  const trackID = event.target.value;
                  update((current) => {
                    if (current.pending) return current;
                    const assignments = trackID
                      ? {
                          ...current.assignments,
                          [file.variant_id]: trackID,
                        }
                      : Object.fromEntries(
                          Object.entries(current.assignments).filter(
                            ([memberID]) => memberID !== file.variant_id,
                          ),
                        );
                    return {
                      ...current,
                      assignments,
                      preview: undefined,
                      receipt: undefined,
                      error: undefined,
                      notice: undefined,
                      stale: false,
                    };
                  });
                }}
              >
                <option value="">{copy.unassigned}</option>
                {tracks.map((track) => (
                  <option key={track.id} value={track.id}>
                    {trackLabel(track)}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </fieldset>
      )}

      {state.error && <p role="alert">{state.error}</p>}
      {state.notice && <p role="status">{state.notice}</p>}
      {state.stale && <p role="status">{copy.staleNotice}</p>}
      <div className="flex flex-wrap gap-2">
        <AppButton type="button" isDisabled={!canPreview} onPress={preview}>
          {state.busy === "preview" ? copy.previewing : copy.previewAction}
        </AppButton>
        {previewCurrent && (
          <AppButton
            type="button"
            isDisabled={state.busy !== undefined}
            onPress={confirm}
          >
            {state.pending ? copy.confirmRetry : copy.confirmAction}
          </AppButton>
        )}
        {state.pending && !previewCurrent && (
          <AppButton
            type="button"
            isDisabled={state.busy !== undefined}
            onPress={confirm}
          >
            {copy.confirmRetry}
          </AppButton>
        )}
      </div>
      {previewCurrent && (
        <section
          aria-label={copy.previewTitle}
          className="space-y-2 rounded bg-stone-100 p-3"
        >
          <h3 className="font-medium">{copy.previewTitle}</h3>
          <p>
            {copy.previewSummary(
              previewCurrent.response.release_title,
              previewCurrent.response.tracks?.length ?? 0,
            )}
          </p>
          <p>
            {copy.omittedFiles(
              previewCurrent.response.omitted_file_member_ids?.length ?? 0,
            )}
          </p>
        </section>
      )}
      {state.receipt?.local_release_id && (
        <p role="status">{copy.receipt(state.receipt.local_release_id)}</p>
      )}
    </section>
  );
}
