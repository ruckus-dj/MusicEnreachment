import { useState } from "react";
import {
  createLocalProviderRecording,
  createLocalProviderRelease,
  listLocalProviderArtists,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { LocalReleaseEditor } from "./LocalReleaseEditor";
import {
  checkLocalReleaseEditions,
  findLocalReleaseRecordings,
} from "./localReleaseLookups";

function uniqueTag(files: readonly IncomingFileResponse[], key: string) {
  const distinct = new Map<string, string>();
  for (const file of files) {
    for (const [tag, values] of Object.entries(file.tags ?? {})) {
      if (tag.trim().toUpperCase() !== key) continue;
      for (const raw of values ?? []) {
        const value = raw.trim();
        if (value) distinct.set(value.normalize("NFC").toLowerCase(), value);
      }
    }
  }
  return distinct.size === 1 ? (distinct.values().next().value ?? "") : "";
}

function formRecord(item: LocalProviderItem) {
  return {
    id: item.id,
    key: item.provider_key,
    provider: item.provider_code === "local" ? "Local" : "MusicBrainz",
    name: item.name,
    title: item.title,
    duration_ms: item.duration_ms,
  };
}

const options = (signal?: AbortSignal): RequestInit => ({
  cache: "no-store",
  signal,
});

// Keep uncertain or still-pending writes fenced across picker/route remounts.
// The API assigns a fresh ID to each create, so a lost reply is not retryable.
const unresolvedWrites = new Set<string>();

async function writeCatalogRecord(
  identity: string,
  request: () => Promise<{ status: number; data: unknown }>,
) {
  const uncertain = () =>
    Object.assign(new Error("catalog write outcome unknown"), {
      outcomeUncertain: true,
    });
  if (unresolvedWrites.has(identity)) throw uncertain();
  unresolvedWrites.add(identity);
  let response: Awaited<ReturnType<typeof request>>;
  try {
    response = await request();
  } catch {
    throw uncertain();
  }
  if (response.status >= 500) throw uncertain();
  if (response.status !== 200) {
    unresolvedWrites.delete(identity);
    throw new Error("catalog write rejected");
  }
  const item = response.data as Partial<LocalProviderItem>;
  if (!item?.id || item.provider_code !== "local") throw uncertain();
  unresolvedWrites.delete(identity);
  return item as LocalProviderItem;
}

export function IncomingLocalReleaseCreation({
  group,
  files,
  label,
  onCancel,
  onSelect,
}: {
  group: IncomingGroupResponse;
  files: readonly IncomingFileResponse[];
  label: string;
  onCancel: () => void;
  onSelect: (id: string, kind: "local" | "musicbrainz") => void;
}) {
  const { locale } = useI18n();
  const [artists, setArtists] = useState<ReturnType<typeof formRecord>[]>([]);
  const [localEditions, setLocalEditions] = useState<ReadonlySet<string>>(
    new Set(),
  );
  const suggestedArtist = uniqueTag(files, "ALBUMARTIST");

  return (
    <LocalReleaseEditor
      locale={locale}
      incomingLabel={label}
      suggestedArtist={suggestedArtist}
      suggestedTitle={uniqueTag(files, "ALBUM")}
      artists={artists}
      recordings={[]}
      checkRelease={async (artist, title, signal) => {
        const [editions, artistPage] = await Promise.all([
          checkLocalReleaseEditions(
            group.id,
            group.revision,
            artist,
            title,
            signal,
          ),
          listLocalProviderArtists(
            { provider: artist ? "all" : "local", q: artist, limit: 50 },
            options(signal),
          ),
        ]);
        signal?.throwIfAborted();
        if (artistPage.status !== 200) throw new Error("artist lookup failed");
        setArtists((current) => [
          ...new Map(
            [...current, ...(artistPage.data.items ?? []).map(formRecord)].map(
              (item) => [item.id, item] as const,
            ),
          ).values(),
        ]);
        setLocalEditions(
          new Set(
            editions
              .filter((hit) => hit.provider === "Local")
              .map((hit) => hit.id),
          ),
        );
        return editions;
      }}
      findRecordings={findLocalReleaseRecordings}
      saveRecording={async (data) => {
        return formRecord(
          await writeCatalogRecord(
            `recording:${data.title.trim().normalize("NFC").toLowerCase()}`,
            () => createLocalProviderRecording(data),
          ),
        );
      }}
      onUseRelease={(id) =>
        onSelect(id, localEditions.has(id) ? "local" : "musicbrainz")
      }
      onSave={async (record) => {
        const body = {
          title: record.title,
          release_group_title: record.release_group_title,
          credits: record.artistIds?.map((id) => {
            const artist = artists.find((item) => item.id === id);
            if (!artist?.name) throw new Error("selected artist not found");
            return { artist_id: id, credit_name: artist.name, join_phrase: "" };
          }),
          tracks: record.tracks?.map((position) => ({
            id: position.id,
            recording_id: position.recordingId,
            title_override: position.titleOverride || undefined,
            duration_override_ms:
              typeof position.durationOverrideMs === "number"
                ? position.durationOverrideMs
                : undefined,
          })),
          release_group_primary_type: record.primaryType,
          language: record.language,
          script: record.script,
        };
        const result = await writeCatalogRecord(
          `release:${record.release_group_title.trim().normalize("NFC").toLowerCase()}`,
          () => createLocalProviderRelease(body),
        );
        onSelect(result.id, "local");
      }}
      onCancel={onCancel}
    />
  );
}
