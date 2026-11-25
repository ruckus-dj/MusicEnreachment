import {
  createLocalProviderArtist,
  createLocalProviderRecording,
} from "../../api/generated/client";
import type { LocalProviderWrite } from "../../api/generated/client.schemas";
import type {
  LocalRecordingDraft,
  LocalReleaseArtist,
  LocalReleaseRecording,
} from "../../ui/features/local-release/localReleaseFormModel";
import { formProviderRecord } from "../local-release/localReleaseLookups";
import {
  catalogCreateIdentity,
  writeCatalogRecord,
} from "../local-release/localReleaseWrites";

export async function saveLocalArtist(data: {
  name: string;
}): Promise<LocalReleaseArtist> {
  const item = await writeCatalogRecord(
    catalogCreateIdentity("artist", data.name),
    () => createLocalProviderArtist({ name: data.name }),
  );
  return formProviderRecord(item);
}

export async function saveLocalRecording(
  data: LocalRecordingDraft,
): Promise<LocalReleaseRecording> {
  const item = await writeCatalogRecord(
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
      } satisfies LocalProviderWrite),
  );
  return formProviderRecord(item);
}
