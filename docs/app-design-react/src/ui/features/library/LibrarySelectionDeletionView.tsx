import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  LibraryDeletionDialogView,
  LibraryDeletionOutputsView,
} from "./LibraryDeletionDialogView";
import {
  type LibraryRowsMessage,
  libraryRowsMessage,
} from "./libraryRowsMessages";

export function LibrarySelectionDeletionView({
  locale,
  open,
  onOpenChange,
  selected,
  published,
  rows,
  simulated,
  onConfirm,
}: {
  locale: Locale;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  selected: number;
  published: number;
  rows: readonly {
    id: string;
    release?: string;
    position: number;
    title: string;
    path: string;
  }[];
  simulated?: boolean;
  onConfirm: () => void;
}) {
  const m = (
    key: LibraryRowsMessage,
    values?: Record<string, string | number>,
  ) => libraryRowsMessage(locale, key, values);
  return (
    <LibraryDeletionDialogView
      locale={locale}
      kind="selection"
      targetLabel={m("frozenDeletionCounts", {
        selected,
        published,
        skipped: selected - published,
      })}
      open={open}
      onOpenChange={onOpenChange}
      dismissable
      actions={
        <>
          <AppButton onPress={() => onOpenChange(false)}>
            {m("cancel")}
          </AppButton>
          <AppButton
            className="border-[var(--error)]! bg-[var(--error)]! text-white!"
            isDisabled={!rows.length}
            onPress={onConfirm}
          >
            {m("confirmDeletion")}
          </AppButton>
        </>
      }
    >
      <p>{m("selectedPublicationScope")}</p>
      {published > rows.length && (
        <p role="status">{m("changedPublications")}</p>
      )}
      <LibraryDeletionOutputsView
        locale={locale}
        rows={rows.map((row) => ({
          id: row.id,
          path: row.path,
          label: [row.release, String(row.position).padStart(2, "0"), row.title]
            .filter(Boolean)
            .join(" · "),
        }))}
      />
      {simulated && <p>{m("deletionDemoNotice")}</p>}
    </LibraryDeletionDialogView>
  );
}
