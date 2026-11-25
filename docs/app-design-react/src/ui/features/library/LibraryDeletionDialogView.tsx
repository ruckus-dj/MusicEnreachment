import type { ReactNode } from "react";
import { useId } from "react";
import { Dialog, Heading, Modal, ModalOverlay } from "react-aria-components";

export type LibraryDeletionDialogViewProps = {
  locale: "ru" | "en";
  kind: keyof (typeof dialogTitles)["en"];
  targetLabel: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dismissable: boolean;
  children: ReactNode;
  actions: ReactNode;
};

const dialogTitles = {
  en: {
    artist: "Delete artist",
    release: "Delete release",
    publication: "Delete publication",
  },
  ru: {
    artist: "Удаление артиста",
    release: "Удаление релиза",
    publication: "Удаление публикации",
  },
} as const;

export function libraryDeletionDialogTitle(
  locale: LibraryDeletionDialogViewProps["locale"],
  kind: LibraryDeletionDialogViewProps["kind"],
) {
  return dialogTitles[locale][kind];
}

const outputCopy = {
  en: {
    title: "Files affected",
    empty: "There are no current publications to delete.",
  },
  ru: {
    title: "Будут затронуты файлы",
    empty: "Текущих публикаций для удаления нет.",
  },
} as const;

type LibraryDeletionOutputRow = {
  id: string;
  path: string;
  label?: string;
  status?: string;
};

export function LibraryDeletionOutputsView({
  locale,
  rows,
}: {
  locale: keyof typeof outputCopy;
  rows: readonly LibraryDeletionOutputRow[];
}) {
  const copy = outputCopy[locale];
  if (rows.length === 0) return <p>{copy.empty}</p>;

  return (
    <>
      <h3 className="font-semibold">{copy.title}</h3>
      <ul className="max-h-56 space-y-2 overflow-y-auto pl-5">
        {rows.map(({ id, path, label, status }) => (
          <li className="wrap-anywhere" key={id}>
            {label && <span className="font-medium">{label}</span>}
            <code className="block text-sm">{path}</code>
            {status && (
              <span className="text-sm text-[var(--muted)]">{status}</span>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

export function LibraryDeletionDialogView({
  locale,
  kind,
  targetLabel,
  open,
  onOpenChange,
  dismissable,
  children,
  actions,
}: LibraryDeletionDialogViewProps) {
  const titleID = useId();
  const targetID = useId();
  return (
    <ModalOverlay
      isOpen={open}
      onOpenChange={onOpenChange}
      isDismissable={dismissable}
      className="fixed inset-0 z-50 grid place-items-center overflow-y-auto bg-black/45 p-3 sm:p-6"
    >
      <Modal className="flex max-h-[min(70dvh,700px)] w-full max-w-xl flex-col overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_16px_48px_#0003]">
        <Dialog
          aria-labelledby={titleID}
          aria-describedby={targetID}
          className="flex min-h-0 min-w-0 flex-1 flex-col wrap-anywhere outline-none"
        >
          <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-6">
            <Heading
              slot="title"
              id={titleID}
              className="text-xl font-semibold"
            >
              {libraryDeletionDialogTitle(locale, kind)}
            </Heading>
            <p id={targetID} className="wrap-anywhere font-medium">
              {targetLabel}
            </p>
            {children}
          </div>
          <div className="sticky bottom-0 z-10 flex shrink-0 flex-col-reverse flex-wrap gap-2 border-t border-[var(--line)] bg-[var(--surface)] p-4 sm:flex-row sm:justify-end sm:px-6">
            {actions}
          </div>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}
