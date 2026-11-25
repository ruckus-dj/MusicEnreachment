import type { ComponentProps, ReactElement, ReactNode } from "react";
import { OwnCatalogDetailsView } from "./OwnCatalogDetailsView";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import { OwnCatalogList, type OwnCatalogListProps } from "./OwnCatalogList";
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
 * An independent editor actor, already composed by the facade. The view keeps
 * the screen shell and the create/edit heading placement; the facade supplies
 * the localized labels for the shelled actors.
 */
export type LocalCatalogEditorSlot =
  | { kind: "releases"; actor: LocalCatalogReleaseEditorActor }
  | {
      kind: "recordings" | "artists";
      eyebrow: string;
      title: string;
      backLabel: string;
      backDisabled?: boolean;
      onBack: () => void;
      element: ReactElement;
    };

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
    <section
      className={inset ? "mx-auto max-w-none p-3" : "mx-auto max-w-none"}
    >
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
  if (editor) {
    return shell(
      <OwnCatalogEditorShell
        eyebrow={editor.eyebrow}
        title={editor.title}
        backLabel={editor.backLabel}
        backDisabled={editor.backDisabled}
        onBack={editor.onBack}
      >
        {editor.element}
      </OwnCatalogEditorShell>,
    );
  }
  return shell(<OwnCatalogList {...list} />, true);
}
