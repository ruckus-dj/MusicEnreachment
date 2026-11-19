import { subscribeToOperation } from "../../api/client/operations";
import {
  getIncomingGroupManualPickerSearch,
  getLibraryRecordingSearch,
  getOperation,
  listLocalProviderRecordings,
  listLocalProviderReleases,
  startIncomingGroupManualPickerSearch,
  startLibraryRecordingSearch,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import type { ReleaseCheckHit } from "./LocalReleaseEditor";
import type { LocalReleaseRecording } from "./localReleaseFormModel";

const limit = 25;
const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
const requestOptions = (signal?: AbortSignal) =>
  ({ cache: "no-store", signal }) satisfies RequestInit;

function awaitSearch(id: string, signal?: AbortSignal) {
  signal?.throwIfAborted();
  if (!id) throw new Error("missing search operation");
  return new Promise<void>((resolve, reject) => {
    const initial = new AbortController();
    let stop: (() => void) | undefined;
    let done = false;
    const timeout = window.setTimeout(
      () => finish(new Error("search timed out")),
      90_000,
    );
    function finish(error?: Error) {
      if (done) return;
      done = true;
      window.clearTimeout(timeout);
      signal?.removeEventListener("abort", cancel);
      initial.abort();
      stop?.();
      if (error) reject(error);
      else resolve();
    }
    function cancel() {
      finish(new DOMException("Search abandoned", "AbortError"));
    }
    function observe(snapshot: OperationResponse) {
      if (snapshot.id !== id || done) return;
      if (snapshot.state === "succeeded") finish();
      else if (snapshot.state !== "queued" && snapshot.state !== "running")
        finish(new Error("search operation failed"));
    }
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      // EventSource reconnects by itself; on open the shared transport reads
      // the operation again. A transient disconnect is not a search failure.
      stop = subscribeToOperation(id, observe, () => {}, finish);
    } catch (error) {
      finish(
        error instanceof Error ? error : new Error("search stream unavailable"),
      );
      return;
    }
    void getOperation(id, requestOptions(initial.signal))
      .then((response) => {
        if (response.status === 200) observe(response.data);
        else finish(new Error("search operation unavailable"));
      })
      .catch((error: unknown) => {
        finish(
          error instanceof Error
            ? error
            : new Error("search operation unavailable"),
        );
      });
  });
}

function object(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

const text = (value: unknown) => (typeof value === "string" ? value : "");

function externalEditions(payload: unknown): ReleaseCheckHit[] {
  const releases = object(payload)?.releases;
  if (!Array.isArray(releases))
    throw new Error("invalid edition search results");
  return releases.map((item) => {
    const entry = object(item);
    const id = text(entry?.id);
    const title = text(entry?.title);
    if (!uuid.test(id) || !title.trim())
      throw new Error("invalid edition search result");
    const credits = entry?.["artist-credit"];
    const artist = Array.isArray(credits)
      ? credits
          .map((value) => {
            const credit = object(value);
            return (
              (text(credit?.name) || text(object(credit?.artist)?.name)) +
              text(credit?.joinphrase)
            );
          })
          .join("")
      : "";
    return {
      id,
      title,
      artist,
      provider: "MusicBrainz",
      details: [entry?.date, entry?.country, entry?.disambiguation]
        .map(text)
        .filter(Boolean)
        .join(" · "),
    };
  });
}

export async function checkLocalReleaseEditions(
  groupId: string,
  groupRevision: string,
  artist: string,
  title: string,
  signal?: AbortSignal,
): Promise<ReleaseCheckHit[]> {
  const opts = requestOptions(signal);
  const localPage = listLocalProviderReleases(
    { provider: "local", q: title, limit },
    opts,
  );
  const musicBrainz = async () => {
    const start = await startIncomingGroupManualPickerSearch(
      groupId,
      {
        entity: "release",
        query: `${artist} ${title}`.trim(),
        expected_group_revision: groupRevision,
        request_id: crypto.randomUUID(),
        limit,
        offset: 0,
      },
      opts,
    );
    if (start.status !== 202) throw new Error("edition search not admitted");
    await awaitSearch(start.data.operation_id, signal);
    const result = await getIncomingGroupManualPickerSearch(
      groupId,
      start.data.operation_id,
      opts,
    );
    if (
      result.status !== 200 ||
      result.data.operation_id !== start.data.operation_id
    )
      throw new Error("edition search results unavailable");
    return externalEditions(result.data.payload);
  };
  const [external, local] = await Promise.all([musicBrainz(), localPage]);
  if (local.status !== 200) throw new Error("Local editions unavailable");
  return [
    ...(local.data.items ?? []).map((item) => ({
      id: item.id,
      title: item.title ?? item.release_group_title ?? "",
      artist: "",
      provider: "Local",
    })),
    ...external,
  ];
}

export async function findLocalReleaseRecordings(
  query: string,
  signal?: AbortSignal,
): Promise<LocalReleaseRecording[]> {
  const opts = requestOptions(signal);
  const localPage = listLocalProviderRecordings(
    { provider: "local", q: query, limit },
    opts,
  );
  const musicBrainz = async () => {
    const start = await startLibraryRecordingSearch(
      { query, limit, offset: 0 },
      opts,
    );
    if (start.status !== 202) throw new Error("recording search not admitted");
    const id = start.data.id;
    await awaitSearch(id, signal);
    const result = await getLibraryRecordingSearch(id, opts);
    if (result.status !== 200 || result.data.operation_id !== id)
      throw new Error("recording search results unavailable");
    return result.data.items.map((item) => {
      if (!item.provider_recording_id || !item.title.trim())
        throw new Error("recording search result cannot be linked");
      return {
        id: item.provider_recording_id,
        provider: "MusicBrainz",
        title: item.title,
        duration_ms: item.duration_ms,
      };
    });
  };
  const [external, local] = await Promise.all([musicBrainz(), localPage]);
  if (local.status !== 200) throw new Error("Local recordings unavailable");
  return [
    ...(local.data.items ?? []).map((item) => ({
      id: item.id,
      provider: "Local",
      title: item.title ?? "",
      duration_ms: item.duration_ms,
    })),
    ...external,
  ];
}
