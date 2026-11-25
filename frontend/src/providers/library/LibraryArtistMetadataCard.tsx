import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getLibraryArtistDetails,
  getLibraryArtistMetadata,
  patchLibraryArtistMetadata,
} from "../../api/generated/client";
import type {
  LibraryArtistMetadataBody,
  LibraryMetadataResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { AppButton } from "../../ui/components/AppButton";
import {
  ArtistMetadataCard,
  type ArtistMetadataValues,
} from "../../ui/features/library/ArtistMetadataCard";
import type { GenreReference } from "../../ui/features/library/GenrePicker";
import { useProviderGenreCatalog } from "../local-release/ProviderCatalogGenreField";

type Baseline = {
  values: ArtistMetadataValues;
  revision: number;
  providerGenres: GenreReference[] | null;
};

const copy = {
  en: {
    loading: "Loading artist metadata…",
    readError: "Could not load artist metadata.",
    retry: "Retry",
    saveError: "Could not save artist metadata.",
    conflict:
      "Artist metadata changed. Your draft is preserved. Cancel and reopen the editor to use the current revision.",
  },
  ru: {
    loading: "Загрузка метаданных артиста…",
    readError: "Не удалось загрузить метаданные артиста.",
    retry: "Повторить",
    saveError: "Не удалось сохранить метаданные артиста.",
    conflict:
      "Метаданные артиста изменились. Черновик сохранён. Отмените правки и откройте редактор заново, чтобы использовать актуальную версию.",
  },
} as const;

function desired(metadata: LibraryMetadataResponse) {
  const { sort_name: sortName, genre } = metadata.envelope.desired;
  const value = genre?.value;
  const genres: GenreReference[] = Array.isArray(value)
    ? value.filter(
        (item): item is GenreReference =>
          !!item &&
          typeof item === "object" &&
          typeof item.id === "string" &&
          typeof item.name === "string" &&
          typeof item.source_name === "string",
      )
    : [];
  const state =
    genre?.state === "value"
      ? "value"
      : genre?.state === "explicit_empty"
        ? "explicit_empty"
        : "absent";
  return {
    sortName:
      sortName?.state === "value" && typeof sortName.value === "string"
        ? sortName.value
        : null,
    genre: { state, value: genres } satisfies ArtistMetadataValues["genre"],
  };
}

export function LibraryArtistMetadataCard({
  artistId,
  onSaved,
}: {
  artistId: string;
  onSaved: () => void;
}) {
  const { locale } = useI18n();
  const text = copy[locale];
  const genreCatalog = useProviderGenreCatalog(locale);
  const client = useQueryClient();
  const queryKey = [
    `/api/library/artists/${artistId}/metadata`,
    "card",
  ] as const;
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }): Promise<Baseline> => {
      const options = { cache: "no-store", signal } as const;
      const [details, metadata] = await Promise.all([
        getLibraryArtistDetails(artistId, options),
        getLibraryArtistMetadata(artistId, options),
      ]);
      if (details.status !== 200) throw apiResponseError(details);
      if (metadata.status !== 200) throw apiResponseError(metadata);
      if (details.data.revision !== metadata.data.revision) {
        throw new Error(text.conflict);
      }
      const provider = metadata.data.provider_genre_baseline;
      const current = client.getQueryData<Baseline>(queryKey);
      if (current && current.revision > metadata.data.revision) return current;
      return {
        revision: metadata.data.revision,
        providerGenres: provider ? (provider.genres ?? []) : null,
        values: {
          name: details.data.name,
          disambiguation: details.data.disambiguation ?? null,
          ...desired(metadata.data),
        },
      };
    },
  });
  const baseline = query.data;

  async function save(
    changes: Partial<ArtistMetadataValues>,
    expectedRevision: number | string,
  ) {
    if (!baseline || typeof expectedRevision !== "number")
      throw new Error(text.saveError);
    if (expectedRevision !== client.getQueryData<Baseline>(queryKey)?.revision)
      throw new Error(text.conflict);
    const fields: Record<string, unknown> = {};
    if (changes.sortName !== undefined) {
      fields.sort_name =
        changes.sortName === null
          ? { state: "absent" }
          : { state: "value", value: changes.sortName };
    }
    if (changes.genre) {
      fields.genre =
        changes.genre.state === "value"
          ? { state: "value", value: changes.genre.value }
          : { state: changes.genre.state };
    }
    const body: LibraryArtistMetadataBody = {
      expected_revision: expectedRevision,
      ...(Object.keys(fields).length ? { changes: fields } : {}),
      ...(changes.name !== undefined ? { name: changes.name } : {}),
      ...(changes.disambiguation !== undefined
        ? { disambiguation: changes.disambiguation ?? "" }
        : {}),
    };
    const response = await patchLibraryArtistMetadata(artistId, body);
    if (response.status === 409) {
      void client.invalidateQueries({ queryKey, exact: true });
      throw new Error(text.conflict);
    }
    if (response.status !== 200)
      throw apiResponseError(response, text.saveError);

    await client.cancelQueries({ queryKey, exact: true });
    client.setQueryData<Baseline>(queryKey, (current) =>
      current && current.revision > response.data.revision
        ? current
        : {
            revision: response.data.revision,
            providerGenres: current?.providerGenres ?? baseline.providerGenres,
            values: {
              name: changes.name ?? baseline.values.name,
              disambiguation:
                changes.disambiguation ?? baseline.values.disambiguation,
              ...desired(response.data),
            },
          },
    );
    onSaved();
  }

  return (
    <div className="grid gap-2">
      {baseline && (
        <ArtistMetadataCard
          locale={locale}
          values={baseline.values}
          revision={baseline.revision}
          providerGenres={baseline.providerGenres}
          genreCatalog={genreCatalog}
          onSave={save}
          disabled={query.isFetching || query.isError}
        />
      )}
      {query.isFetching && <p role="status">{text.loading}</p>}
      {query.isError && (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert">
            {text.readError} {errorMessage(query.error, "")}
          </p>
          <AppButton onPress={() => void query.refetch()}>
            {text.retry}
          </AppButton>
        </div>
      )}
    </div>
  );
}
