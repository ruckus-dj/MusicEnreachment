import type { ComponentProps, ReactNode } from "react";
import { OwnCatalogArtistEditor } from "./OwnCatalogArtistEditor";
import { OwnCatalogDetailsView } from "./OwnCatalogDetailsView";
import { OwnCatalogList, type OwnCatalogListProps } from "./OwnCatalogList";
import { OwnCatalogRecordingEditor } from "./OwnCatalogRecordingEditor";
import { OwnCatalogReleaseEditor } from "./OwnCatalogReleaseEditor";

/**
 * Host-ready data for the portable release actor: roster, initial projection,
 * typed genre catalog and actions only.
 */
export type LocalCatalogReleaseEditorActor = Omit<
  ComponentProps<typeof OwnCatalogReleaseEditor>,
  "locale"
>;

/**
 * Host-ready data for the portable artist actor: initial draft, typed genre
 * catalog and save/back actions only.
 */
export type LocalCatalogArtistEditorActor = Omit<
  ComponentProps<typeof OwnCatalogArtistEditor>,
  "locale"
>;

/**
 * Host-ready data for the portable recording actor: initial draft, artist
 * lookup actions, typed genre catalog and save/back actions only.
 */
export type LocalCatalogRecordingEditorActor = Omit<
  ComponentProps<typeof OwnCatalogRecordingEditor>,
  "locale"
>;

/**
 * An independent editor actor, already composed by the facade. The view keeps
 * the screen shell.
 */
export type LocalCatalogEditorSlot =
  | { kind: "releases"; actor: LocalCatalogReleaseEditorActor }
  | { kind: "recordings"; actor: LocalCatalogRecordingEditorActor }
  | { kind: "artists"; actor: LocalCatalogArtistEditorActor };

export type LocalCatalogScreenViewProps = {
  list: OwnCatalogListProps;
  /** Localized commit notice; an empty value leaves the status region hidden. */
  notice: string;
  details: ComponentProps<typeof OwnCatalogDetailsView> | null;
  editor: LocalCatalogEditorSlot | null;
};

/**
 * Copy-ready Local catalog composition. The provider owns state, lookups,
 * commits, routing, the details projection and the list model; independent
 * editor actors are supplied as narrow slots. This module owns the screen
 * shell and the list/details/edit branch markup.
 */
export function LocalCatalogScreenView({
  list,
  notice,
  details,
  editor,
}: LocalCatalogScreenViewProps) {
  const shell = (content: ReactNode, inset = false) => (
    <section className={inset ? "min-w-0" : "min-w-0"}>
      {content}
      {notice ? (
        <p className="mt-3 text-sm text-[var(--ink)]" role="status">
          {notice}
        </p>
      ) : null}
    </section>
  );
  if (details) {
    return shell(<OwnCatalogDetailsView {...details} />);
  }
  if (editor?.kind === "releases") {
    return shell(
      <OwnCatalogReleaseEditor locale={list.locale} {...editor.actor} />,
    );
  }
  if (editor?.kind === "recordings") {
    return shell(
      <OwnCatalogRecordingEditor locale={list.locale} {...editor.actor} />,
    );
  }
  if (editor?.kind === "artists") {
    return shell(
      <OwnCatalogArtistEditor locale={list.locale} {...editor.actor} />,
    );
  }
  return shell(<OwnCatalogList {...list} />, true);
}
