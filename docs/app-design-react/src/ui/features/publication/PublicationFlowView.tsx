import { useState } from "react";
import { Heading } from "react-aria-components";
import { ActionDialog, ActionModal } from "../../components/ActionModal";
import { AppButton as Button } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  type PublicationInspection,
  PublicationInspector,
} from "./PublicationInspector";
import { publicationMessage } from "./publicationMessages";

export function PublicationFlowView({
  locale,
  title,
  description,
  returnLabel,
  onBack,
  stale,
  error,
  counts,
  items,
  footer,
  primaryLabel,
  primaryDisabled,
  confirmationTitle,
  confirmationDetails,
  confirmationError,
  confirmLabel,
  onConfirm,
}: {
  locale: Locale;
  title: string;
  description?: string;
  returnLabel: string;
  onBack: () => void;
  stale: boolean;
  error?: string;
  counts: { label: string; count?: number }[];
  items: PublicationInspection[];
  footer: { unprocessed: string; policy: string };
  primaryLabel: string;
  primaryDisabled: boolean;
  confirmationTitle: string;
  confirmationDetails: readonly string[];
  confirmationError?: string;
  confirmLabel: string;
  onConfirm: () => void;
}) {
  const [confirming, setConfirming] = useState(false);
  return (
    <section className="min-w-0 px-3 py-3" aria-label={title}>
      <header className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0 [overflow-wrap:anywhere]">
          <h1 className="m-0 text-[20px]">{title}</h1>
          {description && (
            <p className="m-0 mt-1 text-xs text-[var(--muted)]">
              {description}
            </p>
          )}
        </div>
        <Button onPress={onBack}>{returnLabel}</Button>
      </header>
      {(stale || error) && (
        <p role="alert">
          {error || publicationMessage(locale, "inventoryChanged")}
        </p>
      )}
      <section
        className="flex flex-wrap gap-1.5 py-2"
        aria-label={publicationMessage(locale, "actionSummary")}
      >
        {counts.map(({ label, count }) => (
          <span
            key={label}
            className="rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[11px]"
          >
            {label}
            {count === undefined ? "" : `: ${count}`}
          </span>
        ))}
      </section>
      <PublicationInspector items={items} locale={locale} />
      <div className="mt-2 flex flex-wrap items-center justify-between gap-2 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] p-2">
        <div className="min-w-0 text-xs">
          <strong>{footer.unprocessed}</strong> {footer.policy}
        </div>
        <div className="my-2 flex flex-wrap items-center gap-2">
          <Button
            className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
            isDisabled={stale || primaryDisabled}
            onPress={() => setConfirming(true)}
          >
            {primaryLabel}
          </Button>
          <Button onPress={onBack}>
            {publicationMessage(locale, "cancel")}
          </Button>
        </div>
      </div>
      <ActionModal
        isOpen={confirming}
        onOpenChange={setConfirming}
        isDismissable
      >
        <ActionDialog
          aria-labelledby="publication-flow-confirm-title"
          className="grid gap-2"
        >
          <Heading slot="title" id="publication-flow-confirm-title">
            {confirmationTitle}
          </Heading>
          <div className="grid gap-2 text-xs text-[var(--muted)]">
            {confirmationDetails.map((detail) => (
              <p key={detail}>{detail}</p>
            ))}
          </div>
          {(stale || confirmationError) && (
            <p role="alert">
              {confirmationError ||
                publicationMessage(locale, "inventoryChanged")}
            </p>
          )}
          <div className="flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-2">
            <Button onPress={() => setConfirming(false)}>
              {publicationMessage(locale, "cancel")}
            </Button>
            <Button
              className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
              isDisabled={stale || primaryDisabled}
              onPress={() => {
                onConfirm();
                setConfirming(false);
              }}
            >
              {confirmLabel}
            </Button>
          </div>
        </ActionDialog>
      </ActionModal>
    </section>
  );
}
