import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import {
  getGetLibraryReleaseCoverImageUrl,
  useGetLibraryArtist,
  useListLibraryPositions,
  useListLibraryReleases,
} from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { useHashLocation } from "../../navigation/hashLocation";
import { LibraryArtistHeadingView } from "./LibraryArtistHeadingView";
import { LibraryArtistMetadataCard } from "./LibraryArtistMetadataCard";
import { LibraryArtistReleaseTracksView } from "./LibraryArtistReleaseTracksView";
import { LibraryDeletionControl } from "./LibraryDeletionControl";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";
import {
  LibraryEditionCard,
  libraryEditionCardStyles,
} from "./LibraryEditionCard";
import { LibraryPaginationControls } from "./LibraryPaginationView";
import {
  libraryArtistRouteHref,
  libraryRouteHref,
  parseLibraryArtistRoute,
} from "./libraryRoute";
import { MissingReleaseGroupRow } from "./MissingReleaseGroupRow";

const PAGE_SIZE = 50;

const statePanelClass =
  "grid justify-items-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 text-sm [&>h2]:m-0 [&>h2]:text-base [&>p]:m-0";
const screenLinkClass =
  "text-[var(--ink)] no-underline underline-offset-2 hover:text-[var(--accent)] hover:underline";
const artistScreenCopy = {
  en: {
    backToLibrary: "← Back to library",
    loadingArtist: "Loading artist…",
    artistLoadFailed: (status: number) =>
      `Could not load artist (HTTP ${status}).`,
    retry: "Retry",
    loadingReleases: "Loading releases…",
    releaseLoadFailed: (status: number) =>
      `Could not load releases (HTTP ${status}).`,
    artistReleases: "Artist releases",
    untitledRelease: "Untitled release",
    dateNotSpecified: "Date not specified",
    withAudio: "with audio",
    published: "published",
    hidePositions: "Hide positions",
    showPositions: "Show positions",
    noReleasesFound: "No releases found",
    changeFilters: "Try changing the filters.",
    artistHasNoReleases: "This artist has no releases yet.",
    releasePages: "Release pages",
    of: "of",
    previous: "Previous",
    next: "Next",
    loadingPositions: "Loading positions…",
    positionLoadFailed: (status: number) =>
      `Could not load positions (HTTP ${status}).`,
    positionPages: "Position pages",
  },
  ru: {
    backToLibrary: "← К медиатеке",
    loadingArtist: "Загрузка артиста…",
    artistLoadFailed: (status: number) =>
      `Не удалось загрузить артиста (HTTP ${status}).`,
    retry: "Повторить",
    loadingReleases: "Загрузка релизов…",
    releaseLoadFailed: (status: number) =>
      `Не удалось загрузить релизы (HTTP ${status}).`,
    artistReleases: "Релизы артиста",
    untitledRelease: "Релиз без названия",
    dateNotSpecified: "Дата не указана",
    withAudio: "с аудио",
    published: "опубликовано",
    hidePositions: "Скрыть позиции",
    showPositions: "Показать позиции",
    noReleasesFound: "Релизы не найдены",
    changeFilters: "Попробуйте изменить фильтры.",
    artistHasNoReleases: "У этого артиста пока нет релизов.",
    releasePages: "Страницы релизов",
    of: "из",
    previous: "Назад",
    next: "Далее",
    loadingPositions: "Загрузка позиций…",
    positionLoadFailed: (status: number) =>
      `Не удалось загрузить позиции (HTTP ${status}).`,
    positionPages: "Страницы позиций",
  },
} as const;

function countLabel(
  count: number,
  locale: "en" | "ru",
  forms: {
    en: readonly [string, string];
    ru: readonly [string, string, string];
  },
) {
  const formatted = new Intl.NumberFormat(
    locale === "ru" ? "ru-RU" : "en-US",
  ).format(count);
  if (locale === "en") return `${formatted} ${forms.en[count === 1 ? 0 : 1]}`;
  const plural = new Intl.PluralRules("ru-RU").select(count);
  return `${formatted} ${forms.ru[plural === "one" ? 0 : plural === "few" ? 1 : 2]}`;
}

export function LibraryArtistScreen({ artistId }: { artistId: string }) {
  const { locale } = useI18n();
  const copy = artistScreenCopy[locale];
  const queryClient = useQueryClient();
  const hash = useHashLocation();
  const route = parseLibraryArtistRoute(hash) ?? {
    artistId,
    view: "artists" as const,
    query: "",
    publication: "all" as const,
    availability: "all" as const,
    offset: 0,
  };
  const artistQuery = useGetLibraryArtist(artistId);
  const releasesQuery = useListLibraryReleases({
    artist_id: artistId,
    limit: PAGE_SIZE,
    offset: route.offset,
    q: route.query || undefined,
    publication: route.publication,
    availability: route.availability,
  });
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  function invalidateArtistMetadata() {
    void queryClient.invalidateQueries({
      predicate: ({ queryKey }) => {
        const key = queryKey[0];
        return (
          typeof key === "string" &&
          (key.startsWith("/api/library/artists") ||
            key.startsWith("/api/library/releases") ||
            key.includes("/publication/"))
        );
      },
    });
  }
  function invalidateDeletionCatalog() {
    void queryClient.invalidateQueries({
      predicate: ({ queryKey }) => {
        const key = queryKey[0];
        if (typeof key !== "string") return false;
        return (
          key === "/api/library/artists" ||
          key.startsWith("/api/library/artists?") ||
          key.startsWith("/api/library/releases") ||
          key.startsWith("/api/library/positions") ||
          key.includes("/publication/") ||
          key.includes("/deletion/preview")
        );
      },
    });
  }
  function handleArtistDeletionSnapshot(snapshot: { state: string }) {
    if (snapshot.state !== "failed" && snapshot.state !== "succeeded") return;
    invalidateDeletionCatalog();
    if (snapshot.state === "succeeded") {
      // Do not refetch the now-hidden artist detail: leave this route directly.
      window.location.hash = libraryRouteHref(route).slice(1);
    }
  }
  const error =
    artistQuery.data?.status !== undefined && artistQuery.data.status !== 200
      ? apiResponseError(
          artistQuery.data,
          copy.artistLoadFailed(artistQuery.data.status),
        ).message
      : artistQuery.error instanceof Error
        ? artistQuery.error.message
        : "";
  const artist =
    artistQuery.data?.status === 200 ? artistQuery.data.data : undefined;
  const releasesError =
    releasesQuery.data?.status !== undefined &&
    releasesQuery.data.status !== 200
      ? apiResponseError(
          releasesQuery.data,
          copy.releaseLoadFailed(releasesQuery.data.status),
        ).message
      : releasesQuery.error instanceof Error
        ? releasesQuery.error.message
        : "";
  const releases =
    releasesQuery.data?.status === 200 ? releasesQuery.data.data : undefined;

  return (
    <section
      className="w-[min(100%,76rem)] text-[13px] leading-[18px] text-[var(--ink)]"
      aria-labelledby="artist-title"
    >
      <a
        className={`inline-block mb-3 ${screenLinkClass}`}
        href={libraryRouteHref(route)}
      >
        {copy.backToLibrary}
      </a>
      {artistQuery.isLoading ? (
        <p role="status">{copy.loadingArtist}</p>
      ) : error ? (
        <section
          className={`${statePanelClass} text-[var(--error)]`}
          role="alert"
        >
          <p>{error}</p>
          <AppButton onPress={() => void artistQuery.refetch()}>
            {copy.retry}
          </AppButton>
        </section>
      ) : artist ? (
        <>
          <LibraryArtistHeadingView
            locale={locale}
            name={artist.name}
            disambiguation={artist.disambiguation}
            genres={artist.genres}
            count={{
              releases: artist.release_count,
              positions: artist.position_count,
              published: artist.published_position_count,
              available: artist.available_position_count,
              missing: artist.missing_release_groups ?? 0,
              local:
                artist.release_count - (artist.missing_release_groups ?? 0),
            }}
            includesCachedGroups={!artist.discography_complete}
            discographyComplete={artist.discography_complete}
          />
          <LibraryDeletionControl
            target={{ type: "artist", artistId }}
            releaseTitle={artist.name}
            onSnapshot={handleArtistDeletionSnapshot}
          />
          <LibraryArtistMetadataCard
            key={artistId}
            artistId={artistId}
            onSaved={invalidateArtistMetadata}
          />
          {releasesQuery.isLoading ? (
            <p role="status">{copy.loadingReleases}</p>
          ) : releasesError ? (
            <section
              className={`${statePanelClass} text-[var(--error)]`}
              role="alert"
            >
              <p>{releasesError}</p>
              <AppButton onPress={() => void releasesQuery.refetch()}>
                {copy.retry}
              </AppButton>
            </section>
          ) : releases?.items.length ? (
            <section
              className={`${libraryEditionCardStyles.list} mt-3`}
              aria-label={copy.artistReleases}
            >
              {releases.items.map((item) => {
                if (
                  item.kind === "missing_release_group" &&
                  item.missing_release_group
                ) {
                  return (
                    <MissingReleaseGroupRow
                      key={`missing_release_group:${item.missing_release_group.provider_key}`}
                      group={item.missing_release_group}
                    />
                  );
                }
                const release = item.library_release;
                if (!release) return null;
                const title = release.title.trim()
                  ? release.title
                  : copy.untitledRelease;
                const generation = release.cover?.selected_generation;
                const isExpanded = Boolean(expanded[release.id]);
                return (
                  <LibraryEditionCard
                    key={`library_release:${release.id}`}
                    expanded={isExpanded}
                    header={
                      <div className={libraryEditionCardStyles.headerGrid}>
                        <LibraryEditionArtwork
                          title={title}
                          imageSrc={
                            generation
                              ? getGetLibraryReleaseCoverImageUrl(
                                  release.id,
                                  generation,
                                )
                              : undefined
                          }
                        />
                        <div className={libraryEditionCardStyles.copy}>
                          <h2 data-library-edition-title>
                            <a
                              href={`#/library/releases/${encodeURIComponent(release.id)}`}
                            >
                              {title}
                            </a>
                          </h2>
                          <span className={libraryEditionCardStyles.meta}>
                            {release.edition.release_date ??
                              release.edition.original_release_date ??
                              copy.dateNotSpecified}{" "}
                            ·{" "}
                            {countLabel(release.counts.position_count, locale, {
                              en: ["position", "positions"],
                              ru: ["позиция", "позиции", "позиций"],
                            })}{" "}
                            · {release.counts.available_position_count}{" "}
                            {copy.withAudio} ·{" "}
                            {release.counts.published_position_count}{" "}
                            {copy.published}
                          </span>
                        </div>
                        <button
                          type="button"
                          aria-expanded={isExpanded}
                          onClick={() =>
                            setExpanded((value) => ({
                              ...value,
                              [release.id]: !value[release.id],
                            }))
                          }
                        >
                          {isExpanded ? copy.hidePositions : copy.showPositions}
                        </button>
                      </div>
                    }
                    panel={
                      isExpanded ? (
                        <div
                          className={`${libraryEditionCardStyles.panel} px-[15px] pb-3 max-[600px]:px-[9px]`}
                        >
                          <ReleasePositions
                            releaseId={release.id}
                            releaseTitle={title}
                            locale={locale}
                          />
                        </div>
                      ) : undefined
                    }
                  />
                );
              })}
            </section>
          ) : (
            <section className={statePanelClass}>
              <h2>{copy.noReleasesFound}</h2>
              <p>
                {route.query ||
                route.publication !== "all" ||
                route.availability !== "all"
                  ? copy.changeFilters
                  : copy.artistHasNoReleases}
              </p>
            </section>
          )}
          {releases && releases.total > PAGE_SIZE && (
            <LibraryPaginationControls
              label={copy.releasePages}
              status={`${route.offset + 1}–${Math.min(route.offset + PAGE_SIZE, releases.total)} ${copy.of} ${releases.total}`}
              previous={{
                label: copy.previous,
                disabled: route.offset === 0,
                href: libraryArtistRouteHref(artistId, {
                  ...route,
                  offset: Math.max(0, route.offset - PAGE_SIZE),
                }),
              }}
              next={{
                label: copy.next,
                disabled: route.offset + PAGE_SIZE >= releases.total,
                href: libraryArtistRouteHref(artistId, {
                  ...route,
                  offset: route.offset + PAGE_SIZE,
                }),
              }}
            />
          )}
        </>
      ) : null}
    </section>
  );
}

function ReleasePositions({
  releaseId,
  releaseTitle,
  locale,
}: {
  releaseId: string;
  releaseTitle: string;
  locale: "en" | "ru";
}) {
  const copy = artistScreenCopy[locale];
  const [offset, setOffset] = useState(0);
  const query = useListLibraryPositions({
    release_id: releaseId,
    limit: PAGE_SIZE,
    offset,
    publication: "all",
    availability: "all",
  });
  const failure =
    query.data?.status !== undefined && query.data.status !== 200
      ? apiResponseError(query.data, copy.positionLoadFailed(query.data.status))
          .message
      : query.error instanceof Error
        ? query.error.message
        : "";
  if (query.isLoading) return <p role="status">{copy.loadingPositions}</p>;
  if (failure)
    return (
      <section
        className={`${statePanelClass} text-[var(--error)]`}
        role="alert"
      >
        <p>{failure}</p>
        <AppButton onPress={() => void query.refetch()}>{copy.retry}</AppButton>
      </section>
    );
  const page = query.data?.status === 200 ? query.data.data : undefined;
  const positions = page?.items ?? [];
  // An incomplete page cannot prove that this is a single-medium edition.
  const showMedium =
    (page?.total ?? 0) > positions.length ||
    new Set(positions.map(({ medium }) => medium)).size > 1;
  return (
    <div className="mt-2.5 min-w-0">
      <LibraryArtistReleaseTracksView
        locale={locale}
        releaseTitle={releaseTitle}
        tracks={positions.map((position) => {
          const display =
            position.displayed_number.trim() ||
            String(position.position).padStart(2, "0");
          // Numeric labels may repeat across media. Qualified labels (A1,
          // DVD1-3, etc.) are already the intended display value.
          const numberLabel =
            showMedium && position.medium && /^\d+$/.test(display)
              ? `${position.medium}.${display}`
              : display;
          return {
            id: position.id,
            numberLabel,
            title: position.title,
            href: `#/library/releases/${encodeURIComponent(releaseId)}/positions/${encodeURIComponent(position.id)}`,
            durationMs: position.provider_duration_ms,
            hasAudio: position.audio_available,
            sourceCount: position.source_variant_count,
            isNewRecording: !position.recording_id,
            publication: position.publication
              ? {
                  state: "published",
                  codec: position.publication.audio?.codec,
                  container: position.publication.audio?.container,
                }
              : { state: "not-published" },
          };
        })}
      />
      {positions.length > 0 && page && page.total > PAGE_SIZE && (
        <LibraryPaginationControls
          label={copy.positionPages}
          status={`${offset + 1}–${Math.min(offset + PAGE_SIZE, page.total)} ${copy.of} ${page.total}`}
          previous={{
            label: copy.previous,
            disabled: offset === 0,
            onPress: () => setOffset(Math.max(0, offset - PAGE_SIZE)),
          }}
          next={{
            label: copy.next,
            disabled: offset + PAGE_SIZE >= page.total,
            onPress: () => setOffset(offset + PAGE_SIZE),
          }}
        />
      )}
    </div>
  );
}
