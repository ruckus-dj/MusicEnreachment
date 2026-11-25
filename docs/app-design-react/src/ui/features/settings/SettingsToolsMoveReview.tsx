import type { ReactNode } from "react";
import { AppButton } from "../../components/AppButton";

// Review of a frozen move preflight, rendered inside the owning dialog.
const messages = {
  en: {
    sourceDirectory: "Source directory:",
    destinationDirectory: "Destination directory:",
    removeOldFiles: "Remove old managed files:",
    yes: "Yes",
    no: "No",
    managedFileCount: "Managed files:",
    conflictCount: "Conflicts:",
    conflictsToConfirm: "Conflicts to confirm",
    noConflicts: "No conflicts.",
    confirmMove: "Confirm move",
    cancel: "Cancel",
  },
  ru: {
    sourceDirectory: "Исходный каталог:",
    destinationDirectory: "Каталог назначения:",
    removeOldFiles: "Удалить прежние управляемые файлы:",
    yes: "Да",
    no: "Нет",
    managedFileCount: "Управляемых файлов:",
    conflictCount: "Конфликтов:",
    conflictsToConfirm: "Конфликты для подтверждения",
    noConflicts: "Конфликтов нет.",
    confirmMove: "Подтвердить перенос",
    cancel: "Отмена",
  },
} as const;

export type SettingsToolsMoveReviewProps = {
  locale: "en" | "ru";
  /** Frozen preflight evidence; never the live input draft. */
  sourceDirectory: string;
  /** Frozen preflight evidence; never the live input draft. */
  destinationDirectory: string;
  removeOld: boolean;
  managedCount: number;
  conflicts: readonly string[];
  busy?: boolean;
  confirmDisabled?: boolean;
  onConfirm: () => void;
  onDismiss: () => void;
};

const reviewClass = "grid min-w-0 gap-3 text-sm text-[var(--ink)]";
const factsClass = "m-0 list-none divide-y divide-[var(--line)] p-0";
const factClass = "py-[9px]";
const factLabelClass = "text-[var(--muted)]";
const factValueClass = "m-0 wrap-anywhere";
const codeClass = "font-mono text-[11px]/[1.45] wrap-anywhere";
const sectionClass = "grid min-w-0 gap-1";
const headingClass = "m-0 text-sm font-semibold";
const listClass = "my-1 list-disc pl-5";
const itemClass = "wrap-anywhere";
const paragraphClass = "m-0 wrap-anywhere";
const actionsClass = "flex flex-wrap justify-end gap-2";

function ReviewFact({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className={factClass}>
      <dt className={factLabelClass}>{label}</dt>
      <dd className={factValueClass}>{children}</dd>
    </div>
  );
}

export function SettingsToolsMoveReview({
  locale,
  sourceDirectory,
  destinationDirectory,
  removeOld,
  managedCount,
  conflicts,
  busy = false,
  confirmDisabled = false,
  onConfirm,
  onDismiss,
}: SettingsToolsMoveReviewProps) {
  const copy = messages[locale];

  return (
    <div className={reviewClass}>
      <dl className={factsClass}>
        <ReviewFact label={copy.sourceDirectory}>
          <code className={codeClass}>{sourceDirectory}</code>
        </ReviewFact>
        <ReviewFact label={copy.destinationDirectory}>
          <code className={codeClass}>{destinationDirectory}</code>
        </ReviewFact>
        <ReviewFact label={copy.removeOldFiles}>
          {removeOld ? copy.yes : copy.no}
        </ReviewFact>
        <ReviewFact label={copy.managedFileCount}>{managedCount}</ReviewFact>
        <ReviewFact label={copy.conflictCount}>{conflicts.length}</ReviewFact>
      </dl>
      <section className={sectionClass}>
        <h3 className={headingClass}>{copy.conflictsToConfirm}</h3>
        {conflicts.length > 0 ? (
          <ul className={listClass}>
            {conflicts.map((path) => (
              <li key={path} className={itemClass}>
                <code className={codeClass}>{path}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className={paragraphClass}>{copy.noConflicts}</p>
        )}
      </section>
      <div className={actionsClass}>
        <AppButton
          isDisabled={busy || confirmDisabled}
          data-settings-move-confirm=""
          onPress={onConfirm}
        >
          {copy.confirmMove}
        </AppButton>
        <AppButton isDisabled={busy} onPress={onDismiss}>
          {copy.cancel}
        </AppButton>
      </div>
    </div>
  );
}
