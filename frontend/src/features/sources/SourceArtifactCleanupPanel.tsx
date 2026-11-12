import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { sourceMessage } from "./sourceMessages";
import { renderSourceError } from "./sourcesApi";
import { useSourceArtifactCleanup } from "./useSourceArtifactCleanup";

export function SourceArtifactCleanupPanel() {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const stateText = (state: string) =>
    t(
      state === "queued"
        ? "queued"
        : state === "running"
          ? "running"
          : state === "succeeded"
            ? "succeeded"
            : state === "failed"
              ? "failed"
              : "unknown",
    );
  const cleanup = useSourceArtifactCleanup();
  const [confirmation, setConfirmation] = useState<string[] | undefined>();
  const dialog = useRef<HTMLDialogElement>(null);
  const trigger = useRef<HTMLElement | null>(null);
  const alert = useRef<HTMLParagraphElement>(null);

  useLayoutEffect(() => {
    if (!confirmation || !dialog.current) return;
    if (!dialog.current.open) dialog.current.showModal();
    dialog.current.querySelector("button")?.focus();
  }, [confirmation]);

  useEffect(() => {
    if (cleanup.error) alert.current?.focus();
  }, [cleanup.error]);

  function closeConfirmation() {
    if (dialog.current?.open) dialog.current.close();
    else {
      setConfirmation(undefined);
      trigger.current?.focus();
      trigger.current = null;
    }
  }

  const operation = cleanup.operation;
  const visibleIds = new Set(
    cleanup.candidates.map(({ artifact_id }) => artifact_id),
  );
  const selectedNotInBatch = cleanup.selected.filter(
    (id) => !visibleIds.has(id),
  );

  return (
    <section aria-labelledby="artifact-cleanup-title" className="sources-panel">
      <h2 id="artifact-cleanup-title">{t("cleanupTitle")}</h2>
      <p>{t("cleanupDescription")}</p>

      {cleanup.loading && cleanup.batchCount === undefined && (
        <p role="status">{t("loadingCopies")}</p>
      )}
      {cleanup.error && (
        <>
          <p ref={alert} tabIndex={-1} role="alert">
            {renderSourceError(cleanup.error, locale)}
          </p>
          <AppButton onPress={() => void cleanup.load()}>
            {t("refreshCopies")}
          </AppButton>
        </>
      )}
      {cleanup.batchCount !== undefined && (
        <p role="status">
          {t("currentBatch", {
            count: new Intl.NumberFormat(
              locale === "ru" ? "ru-RU" : "en-US",
            ).format(cleanup.batchCount),
          })}
        </p>
      )}
      {cleanup.loading && cleanup.batchCount !== undefined && (
        <p role="status">{t("refreshingCopies")}</p>
      )}

      {cleanup.batchCount !== undefined && cleanup.candidates.length === 0 ? (
        <p>{t("noCopies")}</p>
      ) : (
        cleanup.candidates.length > 0 && (
          <>
            <ul aria-label={t("cleanupCandidates")}>
              {cleanup.candidates.map((candidate) => (
                <li key={candidate.artifact_id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={cleanup.selected.includes(candidate.artifact_id)}
                      disabled={cleanup.isPending || cleanup.busy}
                      onChange={() => cleanup.toggle(candidate.artifact_id)}
                    />{" "}
                    <code>{candidate.relative_path}</code> —{" "}
                    {t(
                      candidate.state === "cleanup_eligible"
                        ? "cleanupEligible"
                        : candidate.state === "cleanup_failed"
                          ? "cleanupFailed"
                          : candidate.state === "deleted"
                            ? "deleted"
                            : candidate.state === "missing"
                              ? "missing"
                              : candidate.state === "failed"
                                ? "deletionFailed"
                                : candidate.state === "claimed"
                                  ? "claimed"
                                  : "unknown",
                    )}
                  </label>
                </li>
              ))}
            </ul>
            <AppButton
              isDisabled={
                cleanup.selected.length === 0 ||
                cleanup.isPending ||
                cleanup.busy
              }
              onPress={(event) => {
                trigger.current = event.target as HTMLElement;
                setConfirmation([...cleanup.selected]);
              }}
            >
              {t("cleanupSelected", {
                count: new Intl.NumberFormat(
                  locale === "ru" ? "ru-RU" : "en-US",
                ).format(cleanup.selected.length),
              })}
            </AppButton>
          </>
        )
      )}

      {selectedNotInBatch.length > 0 && (
        <section aria-label={t("outsideBatch")}>
          <p>{t("outsideBatchHelp")}</p>
          <ul>
            {selectedNotInBatch.map((id) => (
              <li key={id}>
                <code>{id}</code>{" "}
                <AppButton
                  isDisabled={cleanup.isPending || cleanup.busy}
                  onPress={() => cleanup.toggle(id)}
                >
                  {t("deselect")}
                </AppButton>
              </li>
            ))}
          </ul>
        </section>
      )}

      {operation && (
        <section aria-labelledby="artifact-cleanup-operation-title">
          <h3 id="artifact-cleanup-operation-title">{t("cleanupOperation")}</h3>
          {cleanup.isPending ? (
            <p role="status">
              {t("operationRunning", {
                state: stateText(operation.state),
                stage: operation.stage,
              })}
            </p>
          ) : (
            <p role="status">
              {t("operationComplete", { state: stateText(operation.state) })}
              {operation.safe_error ? ` ${operation.safe_error}` : ""}
            </p>
          )}
          {cleanup.streamError && cleanup.isPending && (
            <p role="status">{t("cleanupStreamInterrupted")}</p>
          )}
          {cleanup.results && cleanup.results.length > 0 && (
            <ul aria-label={t("cleanupResults")}>
              {cleanup.results.map((result) => (
                <li key={result.artifact_id}>
                  <code>{result.artifact_id}</code>:{" "}
                  {t(
                    result.state === "cleanup_eligible"
                      ? "cleanupEligible"
                      : result.state === "cleanup_failed"
                        ? "cleanupFailed"
                        : result.state === "deleted"
                          ? "deleted"
                          : result.state === "missing"
                            ? "missing"
                            : result.state === "failed"
                              ? "deletionFailed"
                              : result.state === "claimed"
                                ? "claimed"
                                : "unknown",
                  )}
                  {result.safe_error ? ` — ${result.safe_error}` : ""}
                </li>
              ))}
            </ul>
          )}
          {!cleanup.isPending && !cleanup.results?.length && (
            <p>{t("unknownRemoval")}</p>
          )}
        </section>
      )}

      {confirmation && (
        <dialog
          ref={dialog}
          aria-modal="true"
          aria-labelledby="artifact-cleanup-confirm-title"
          className="sources-dialog"
          onClose={() => {
            setConfirmation(undefined);
            trigger.current?.focus();
            trigger.current = null;
          }}
          onCancel={(event) => {
            event.preventDefault();
            closeConfirmation();
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              closeConfirmation();
              return;
            }

            if (event.key !== "Tab") return;

            const focusable = Array.from(
              event.currentTarget.querySelectorAll<HTMLElement>(
                [
                  "a[href]",
                  "area[href]",
                  "button",
                  'input:not([type="hidden"])',
                  "select",
                  "textarea",
                  "iframe",
                  "object",
                  "embed",
                  '[contenteditable="true"]',
                  "[tabindex]",
                ].join(", "),
              ),
            ).filter(
              (element) =>
                !element.matches(":disabled") &&
                element.tabIndex >= 0 &&
                !element.closest('[hidden], [inert], [aria-hidden="true"]'),
            );

            const first = focusable[0];
            const last = focusable[focusable.length - 1];
            const active = event.currentTarget.ownerDocument.activeElement;

            if (event.shiftKey && active === first) {
              event.preventDefault();
              last?.focus();
            } else if (!event.shiftKey && active === last) {
              event.preventDefault();
              first?.focus();
            }
          }}
        >
          <h2 id="artifact-cleanup-confirm-title">{t("confirmCleanup")}</h2>
          <p>
            {t("confirmationIds", {
              count: new Intl.NumberFormat(
                locale === "ru" ? "ru-RU" : "en-US",
              ).format(confirmation.length),
            })}
          </p>
          <ul>
            {confirmation.map((id) => (
              <li key={id}>
                <code>{id}</code>
              </li>
            ))}
          </ul>
          <p>{t("sourceFilesNotDeleted")}</p>
          <AppButton onPress={closeConfirmation}>{t("cancel")}</AppButton>
          <AppButton
            isDisabled={cleanup.busy}
            onPress={() => {
              const ids = confirmation;
              setConfirmation(undefined);
              if (dialog.current?.open) dialog.current.close();
              void cleanup.start(ids);
            }}
          >
            {t("confirmCleanupAction")}
          </AppButton>
        </dialog>
      )}
    </section>
  );
}
