import { type MouseEvent, type ReactElement, useContext } from "react";
import {
  Checkbox,
  Disclosure,
  DisclosurePanel,
  DisclosureStateContext,
  Heading,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import type { Locale } from "../../i18n/types";
import { ArtistReleasePositionsView } from "./ArtistReleasePositionsView";
import type { ArtistEditionModel, ArtistTagFocus } from "./ArtistScreenView";
import { type CatalogueMessage, catalogueMessage } from "./catalogueMessages";
import { catalogueStyles } from "./catalogueStyles";
import { LibraryEditionArtwork } from "./LibraryEditionArtwork";
import { libraryRowsMessage } from "./libraryRowsMessages";

const headerClass =
  "flex min-h-[80px] min-w-0 cursor-pointer flex-wrap items-center gap-3 px-4 py-3 text-left transition-colors motion-safe:duration-100 hover:bg-[var(--raised)] group-data-[expanded]:bg-[var(--accent-soft)] max-[600px]:items-start max-[600px]:gap-3 max-[600px]:px-3";
const controlsClass = "flex flex-none items-center gap-2";
const actionsClass = "ml-auto flex flex-wrap items-center justify-end gap-2";
const chevronButton =
  "grid size-7 min-h-7 min-w-7 shrink-0 place-items-center rounded-md border border-[var(--line)] bg-[var(--surface)] p-0! text-[var(--muted)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2";
const openLinkClass =
  "inline-flex min-h-9 items-center justify-center gap-2 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 text-sm font-medium text-[var(--ink)] no-underline hover:border-[var(--control)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const cardCheckboxFocus =
  "data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]";

/** Defers mounting a lazy slot until its disclosure is actually expanded. */
function ExpandedOnly({ children }: { children: ReactElement }) {
  const state = useContext(DisclosureStateContext);
  return state?.isExpanded ? children : null;
}

function EditionCheckbox({
  locale,
  edition,
}: {
  locale: Locale;
  edition: ArtistEditionModel;
}) {
  const selection = edition.selection;
  if (!selection) return null;
  return (
    <Checkbox
      aria-label={`${catalogueMessage(locale, "selectAllEditionPositions")}: ${edition.ariaLabel}`}
      className={`${catalogueStyles.cardCheckbox} ${cardCheckboxFocus}`}
      isSelected={selection.isSelected}
      isIndeterminate={selection.isIndeterminate}
      isDisabled={selection.isDisabled}
      onChange={selection.onChange}
    >
      <span className={catalogueStyles.cardCheckboxBox} aria-hidden="true">
        {selection.isSelected ? (
          <AppIcon name="check" className="size-3" />
        ) : selection.isIndeterminate ? (
          <span className="h-0.5 w-2 rounded bg-current" />
        ) : null}
      </span>
    </Checkbox>
  );
}

function EditionCard({
  locale,
  edition,
  tagFocus,
}: {
  locale: Locale;
  edition: ArtistEditionModel;
  tagFocus?: ArtistTagFocus;
}) {
  const state = useContext(DisclosureStateContext);
  const expanded = state?.isExpanded ?? false;
  const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
    catalogueMessage(locale, key, values);

  function toggleFromRow(event: MouseEvent<HTMLDivElement>) {
    if (!(event.target instanceof Element)) return;
    if (
      event.target.closest(
        "button,a,input,label,select,textarea,[role='checkbox']",
      )
    )
      return;
    state?.toggle();
  }

  return (
    <>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: the row click is a pointer convenience; the chevron trigger provides keyboard access. */}
      {/* biome-ignore lint/a11y/useKeyWithClickEvents: the row click is a pointer convenience; the chevron trigger provides keyboard access. */}
      <div className={headerClass} onClick={toggleFromRow}>
        <div className={controlsClass}>
          <AppButton
            slot="trigger"
            aria-expanded={expanded}
            aria-label={`${expanded ? m("hidePositions") : m("showPositions")}: ${edition.ariaLabel}`}
            className={chevronButton}
          >
            <AppIcon
              name="chevronRight"
              className={`size-4 transition-transform motion-safe:duration-150 ${expanded ? "rotate-90" : ""}`}
            />
          </AppButton>
          <EditionCheckbox locale={locale} edition={edition} />
        </div>
        <LibraryEditionArtwork
          palette={edition.artwork.palette}
          title={edition.artwork.title}
          imageSrc={edition.artwork.imageSrc}
          imageAlt={edition.artwork.imageAlt}
        />
        <div className={`${catalogueStyles.albumCopy} flex-1`}>
          <Heading level={2} className="min-w-0" data-library-edition-title>
            <strong>{edition.title}</strong>
          </Heading>
          {edition.durationSeconds !== undefined && (
            <span className={catalogueStyles.albumMeta}>
              {edition.durationSeconds === null
                ? m("durationUnknown")
                : `${Math.floor(edition.durationSeconds / 60)}:${String(Math.round(edition.durationSeconds % 60)).padStart(2, "0")} ${m("total")}`}
            </span>
          )}
          <span className={catalogueStyles.albumMeta}>
            {edition.year} · {edition.positionCount} {m("positionsCount")}
            {edition.availableCount !== undefined &&
              ` · ${edition.availableCount} ${m("withAudio")}`}
            {edition.metadataChangedCount
              ? ` · ${m("metadataChangedFor", { count: edition.metadataChangedCount })}`
              : ""}
          </span>
          <span className={catalogueStyles.albumMeta}>
            {edition.country || m("countryUnspecified")} ·{" "}
            {edition.label || m("local")} · {edition.catalog || "—"}
            {edition.providerText ? ` · ${edition.providerText}` : ""}
          </span>
          <span className={catalogueStyles.albumMeta}>
            {edition.providerTrackCount !== null
              ? `${edition.publishedCount}/${edition.providerTrackCount} ${m("publishedCount")}`
              : `${edition.publishedCount} ${m("publishedCount")} · ${m("providerCompletenessUnknown")}`}
          </span>
        </div>
        {(edition.titleHref || edition.deletion) && (
          <div className={actionsClass}>
            {edition.titleHref && (
              <a
                href={edition.titleHref}
                className={openLinkClass}
                aria-label={`${libraryRowsMessage(locale, "openRelease")}: ${edition.ariaLabel}`}
              >
                {libraryRowsMessage(locale, "openRelease")}
              </a>
            )}
            {edition.deletion}
          </div>
        )}
      </div>
      <DisclosurePanel className={catalogueStyles.albumPanel}>
        {edition.editor && (
          <div
            ref={
              tagFocus?.scope === "release" &&
              tagFocus.releaseId === edition.key
                ? tagFocus.ref
                : undefined
            }
            tabIndex={
              tagFocus?.scope === "release" &&
              tagFocus.releaseId === edition.key
                ? -1
                : undefined
            }
            className="grid min-w-0 gap-3"
          >
            {edition.editor}
          </div>
        )}
        {edition.publication}
        {edition.positions ? (
          <ExpandedOnly>{edition.positions}</ExpandedOnly>
        ) : (
          <ArtistReleasePositionsView
            locale={locale}
            releaseTitle={edition.title}
            releaseSelection={edition.trackSelection}
            positions={{
              state: "ready",
              tracks: edition.tracks ?? [],
            }}
          />
        )}
      </DisclosurePanel>
    </>
  );
}

/** One artist edition row with an explicit chevron and full-width details. */
export function ArtistEditionRowView({
  locale,
  edition,
  tagFocus,
}: {
  locale: Locale;
  edition: ArtistEditionModel;
  tagFocus?: ArtistTagFocus;
}) {
  return (
    <div className={catalogueStyles.albumRow}>
      <Disclosure
        className={`group ${catalogueStyles.albumCard}`}
        id={edition.key}
      >
        <EditionCard locale={locale} edition={edition} tagFocus={tagFocus} />
      </Disclosure>
    </div>
  );
}
