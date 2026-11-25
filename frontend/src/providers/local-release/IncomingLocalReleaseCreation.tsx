import { type ReactNode, useState } from "react";
import {
  createLocalProviderArtist,
  createLocalProviderRecording,
  createLocalProviderRelease,
  listLocalProviderArtists,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderWrite,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { IncomingCreationDataBinder } from "../../ui/features/incoming/IncomingCreationOutlet";
import type { LocalReleaseEditorProps } from "../../ui/features/local-release/LocalReleaseEditor";
import {
  checkLocalReleaseEditions,
  findLocalReleaseArtists,
  findLocalReleaseRecordings,
  formProviderRecord,
} from "./localReleaseLookups";
import {
  catalogCreateIdentity,
  writeCatalogRecord,
} from "./localReleaseWrites";
import { ProviderCatalogReleaseGenreField } from "./ProviderCatalogReleaseGenreField";

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

const options = (signal?: AbortSignal): RequestInit => ({
  cache: "no-store",
  signal,
});

export function IncomingLocalReleaseCreation({
  group,
  files,
  label,
  onCancel,
  onSelect,
  children,
}: {
  group: IncomingGroupResponse;
  files: readonly IncomingFileResponse[];
  label: string;
  onCancel: () => void;
  onSelect: (id: string, kind: "local" | "musicbrainz") => void;
  children: ReactNode;
}) {
  const { locale } = useI18n();
  const [artists, setArtists] = useState<
    ReturnType<typeof formProviderRecord>[]
  >([]);
  const [localEditions, setLocalEditions] = useState<ReadonlySet<string>>(
    new Set(),
  );
  const suggestedArtist = uniqueTag(files, "ALBUMARTIST");

  const model: LocalReleaseEditorProps = {
    locale,
    incomingLabel: label,
    suggestedArtist,
    suggestedTitle: uniqueTag(files, "ALBUM"),
    artists,
    recordings: [],
    checkRelease: async (artist, title, signal) => {
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
          [
            ...current,
            ...(artistPage.data.items ?? []).map(formProviderRecord),
          ].map((item) => [item.id, item] as const),
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
    },
    findRecordings: findLocalReleaseRecordings,
    findArtists: findLocalReleaseArtists,
    renderGenreField: (props) => (
      <ProviderCatalogReleaseGenreField {...props} />
    ),
    saveArtist: async (data) => {
      const artist = formProviderRecord(
        await writeCatalogRecord(
          catalogCreateIdentity("artist", data.name),
          () => createLocalProviderArtist(data),
        ),
      );
      setArtists((current) => [...current, artist]);
      return artist;
    },
    saveRecording: async (data) => {
      return formProviderRecord(
        await writeCatalogRecord(
          catalogCreateIdentity("recording", data.title),
          () =>
            createLocalProviderRecording({
              title: data.title,
              duration_ms: data.duration_ms,
              credits: data.credits?.map((credit) => ({
                artist_id: credit.artistId,
                credit_name: credit.creditName,
                join_phrase: credit.joinPhrase,
              })),
            }),
        ),
      );
    },
    onUseRelease: (id) =>
      onSelect(id, localEditions.has(id) ? "local" : "musicbrainz"),
    onSave: async (record) => {
      const body: LocalProviderWrite = {
        title: record.title,
        release_group_title: record.release_group_title,
        credits: record.artistIds.map((id, index) => {
          const artist = artists.find((item) => item.id === id);
          if (!artist?.name) throw new Error("selected artist not found");
          return {
            artist_id: id,
            credit_name: artist.name,
            join_phrase: index + 1 < record.artistIds.length ? ", " : "",
          };
        }),
        tracks: record.tracks.map((position) => ({
          id: position.id,
          recording_id: position.recordingId,
          title_override: position.titleOverride || undefined,
          duration_override_ms:
            typeof position.durationOverrideMs === "number"
              ? position.durationOverrideMs
              : undefined,
        })),
        release_group_primary_type: record.primaryType,
        release_group_secondary_types: record.secondaryTypes,
        language: record.language,
        script: record.script,
        genres: record.genres,
        edition: record.edition,
      };
      const result = await writeCatalogRecord(
        catalogCreateIdentity("release", record.release_group_title),
        () => createLocalProviderRelease(body),
      );
      onSelect(result.id, "local");
    },
    onCancel,
  };

  return (
    <IncomingCreationDataBinder model={model}>
      {children}
    </IncomingCreationDataBinder>
  );
}
