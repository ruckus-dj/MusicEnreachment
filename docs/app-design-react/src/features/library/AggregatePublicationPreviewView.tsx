import { type Ref, useEffect, useId, useRef, useState } from "react";
import { firstPublicationPreviewMessages } from "./firstPublicationPreviewMessages";
import {
  type PublicationPreviewDetailsState,
  PublicationPreviewDetailsView,
  type PublicationPreviewSourceChoice,
} from "./PublicationPreviewDetailsView";

export type AggregatePublicationPreviewLocale = "en" | "ru";
export type AggregatePublicationMode = "ordinary" | "forced";

export type AggregateDisposition =
  | "create"
  | "update"
  | "unchanged"
  | "skipped"
  | "unprocessable"
  | "blocked";

/** Display order of the disposition totals. */
export const aggregateDispositions: readonly AggregateDisposition[] = [
  "create",
  "update",
  "unchanged",
  "skipped",
  "unprocessable",
  "blocked",
];

/**
 * Preview state of one position as the aggregate row shows it. "not-prepared"
 * means the current mode has no prepared snapshot yet; it is not a failure.
 */
export type AggregatePositionStatus =
  | { kind: "disposition"; disposition: AggregateDisposition }
  | { kind: "loading" }
  | { kind: "not-prepared" };

/**
 * Row detail shown after the status when the position is included. The host
 * supplies only text it owns; labels for the remaining causes stay here so the
 * view never receives host copy.
 */
export type AggregatePositionDiagnostic =
  | { kind: "text"; text: string }
  | { kind: "quality-downgrade" }
  | { kind: "no-controller" }
  | { kind: "not-prepared" };

export interface AggregatePositionView {
  id: string;
  /** Position title already formatted by the host, e.g. "1.03. Title". */
  label: string;
  status: AggregatePositionStatus;
  diagnostic?: AggregatePositionDiagnostic;
}

/**
 * Optional exact source-file choice shown above the publication details. The
 * aggregate release workflow uses the audio-stream choice in
 * `PublicationPreviewDetailsView`; a saved matching result chooses among its
 * exact linked files instead and supplies localized copy here.
 */
export interface AggregateInspectorFileChoice {
  legend: string;
  emptyLabel: string;
  options: readonly { id: string; label: string }[];
  selectedId: string | null;
  disabled?: boolean;
  onSelect: (id: string | null) => void;
}

/**
 * Selected-position inspector. Publication details are a portable inspection
 * model owned by `PublicationPreviewDetailsView`; the aggregate view only
 * places them in the desktop panel or the mobile dialog.
 */
export type AggregateInspectorPresentation =
  | { kind: "empty" }
  | { kind: "not-prepared" }
  | {
      kind: "publication";
      details: PublicationPreviewDetailsState;
      fileChoice?: AggregateInspectorFileChoice;
      onSourceChoice?: (choice: PublicationPreviewSourceChoice | null) => void;
      onRefresh: () => void;
    };

export interface AggregatePublicationPreviewViewProps {
  locale: AggregatePublicationPreviewLocale;
  /** REQUIRED only when `onModeChange` is supplied. */
  mode?: AggregatePublicationMode;
  counts: Readonly<Record<AggregateDisposition, number>>;
  positions: readonly AggregatePositionView[];
  included: ReadonlySet<string>;
  /** Requested selection; falls back to the first position like the host. */
  selectedPositionId: string;
  /** False when no included position has a controller or while preparing. */
  canPrepare: boolean;
  canConfirm: boolean;
  preparing: boolean;
  inspector: AggregateInspectorPresentation;
  title?: string;
  headingRef?: Ref<HTMLHeadingElement>;
  prepareLabel?: string;
  confirmLabel?: string;
  /** Omit to hide the CURRENT republish mode selector. */
  onModeChange?: (mode: AggregatePublicationMode) => void;
  onToggleInclude: (positionId: string) => void;
  onSelectPosition: (positionId: string) => void;
  onPrepare: () => void;
  onConfirm: () => void;
}

const copy = {
  en: {
    title: "Aggregate publication preview",
    independentAdmissions:
      "Confirmation starts independent operations per position; there is no atomic batch publication.",
    modeLegend: "CURRENT republish mode",
    include: "Include position",
    ordinary: "Ordinary",
    forced: "Forced",
    prepare: "Prepare preview",
    confirm: "Confirm ready publications",
    loading: "Preview is loading",
    notPrepared: "Preview has not been prepared",
    noController: "No publication controller for this position",
    none: "No selected positions.",
    select: "Position inspector",
    inspector: "Publication inspector",
    noPosition: "Select a position to inspect its details.",
    close: "Close",
    dispositions: {
      create: "Create",
      update: "Update",
      unchanged: "Unchanged",
      skipped: "Skipped",
      unprocessable: "Unprocessable metadata",
      blocked: "Blocked",
    } satisfies Record<AggregateDisposition, string>,
  },
  ru: {
    title: "Общий предпросмотр публикации",
    independentAdmissions:
      "Подтверждение запускает отдельные операции по позициям; общей атомарной публикации нет.",
    modeLegend: "Режим повторной публикации CURRENT",
    include: "Включить позицию",
    ordinary: "Обычная",
    forced: "Принудительная",
    prepare: "Подготовить предпросмотр",
    confirm: "Подтвердить готовые публикации",
    loading: "Предпросмотр загружается",
    notPrepared: "Предпросмотр не подготовлен",
    noController: "Нет контроллера публикации для этой позиции",
    none: "Нет выбранных позиций.",
    select: "Позиция для инспектора",
    inspector: "Инспектор публикации",
    noPosition: "Выберите позицию, чтобы посмотреть подробности.",
    close: "Закрыть",
    dispositions: {
      create: "Создать",
      update: "Обновить",
      unchanged: "Без изменений",
      skipped: "Пропущено",
      unprocessable: "Невозможно обработать метаданные",
      blocked: "Заблокировано",
    } satisfies Record<AggregateDisposition, string>,
  },
} as const;

type Text = (typeof copy)[AggregatePublicationPreviewLocale];

const styles = {
  root: "min-w-0 space-y-3 rounded border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)]",
  title: "font-semibold",
  summary: "grid gap-x-4 gap-y-1 sm:grid-cols-[max-content_1fr]",
  summaryRow: "contents",
  modes: "flex flex-wrap gap-4",
  modesLegend: "font-medium",
  layout:
    "grid min-w-0 gap-3 min-[900px]:grid-cols-1 min-[1400px]:grid-cols-[minmax(220px,0.38fr)_minmax(0,1fr)]",
  positions: "min-w-0",
  positionsList:
    "max-h-80 space-y-1 overflow-y-auto min-[1400px]:max-h-[34rem]",
  positionItem: "min-w-0",
  positionRow:
    "grid min-h-12 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded border px-2 py-1",
  positionRowSelected: "border-[var(--accent)] bg-[var(--accent-soft)]",
  positionRowIdle: "border-[var(--line)]",
  positionButton:
    "min-w-0 text-left focus-visible:outline-2 focus-visible:outline-offset-2",
  positionLabel: "block truncate font-medium",
  positionStatus: "block truncate text-xs text-[var(--muted)]",
  includeLabel: "flex items-center gap-1 text-xs",
  inspectorPanel:
    "min-w-0 rounded border border-[var(--line)] p-3 max-[899px]:hidden",
  inspectorHeading: "mb-2 font-medium",
  inspectorBody: "max-h-[34rem] min-w-0 overflow-y-auto",
  dialog:
    "m-auto max-h-[90dvh] w-[min(96vw,46rem)] overflow-y-auto rounded border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] backdrop:bg-black/40",
  dialogHeader: "mb-3 flex items-center justify-between gap-3",
  dialogTitle: "font-semibold",
  actions: "flex flex-wrap gap-2",
} as const;

function statusLabel(status: AggregatePositionStatus, text: Text) {
  if (status.kind === "loading") return text.loading;
  if (status.kind === "not-prepared") return text.notPrepared;
  return text.dispositions[status.disposition];
}

function diagnosticLabel(
  diagnostic: AggregatePositionDiagnostic | undefined,
  text: Text,
  locale: AggregatePublicationPreviewLocale,
) {
  if (!diagnostic) return "";
  if (diagnostic.kind === "text") return diagnostic.text;
  if (diagnostic.kind === "quality-downgrade")
    return firstPublicationPreviewMessages[locale]
      .outputQualityDowngradeWarning;
  return diagnostic.kind === "no-controller"
    ? text.noController
    : text.notPrepared;
}

export function AggregatePublicationPreviewView({
  locale,
  mode = "ordinary",
  counts,
  positions,
  included,
  selectedPositionId,
  canPrepare,
  canConfirm,
  preparing,
  inspector,
  title,
  headingRef,
  prepareLabel,
  confirmLabel,
  onModeChange,
  onToggleInclude,
  onSelectPosition,
  onPrepare,
  onConfirm,
}: AggregatePublicationPreviewViewProps) {
  const text = copy[locale];
  const modeName = useId();
  const fileChoiceName = useId();
  const [mobileInspectorOpen, setMobileInspectorOpen] = useState(false);
  const mobileDialog = useRef<HTMLDialogElement>(null);
  const selectedPosition =
    positions.find((position) => position.id === selectedPositionId) ??
    positions[0];

  useEffect(() => {
    if (
      mobileInspectorOpen &&
      mobileDialog.current &&
      !mobileDialog.current.open
    )
      mobileDialog.current.showModal();
    if (!mobileInspectorOpen && mobileDialog.current?.open)
      mobileDialog.current.close();
  }, [mobileInspectorOpen]);

  function selectPosition(positionId: string) {
    onSelectPosition(positionId);
    const isNarrow = window.matchMedia
      ? window.matchMedia("(max-width: 899px)").matches
      : window.innerWidth < 900;
    if (isNarrow) setMobileInspectorOpen(true);
  }

  const fileChoice =
    inspector.kind === "publication" ? inspector.fileChoice : undefined;
  const inspectorContent =
    inspector.kind === "publication" ? (
      <>
        {fileChoice ? (
          <fieldset className="space-y-2" disabled={fileChoice.disabled}>
            <legend className="font-medium">{fileChoice.legend}</legend>
            <label className="block">
              <input
                type="radio"
                name={fileChoiceName}
                checked={fileChoice.selectedId === null}
                onChange={() => fileChoice.onSelect(null)}
              />{" "}
              {fileChoice.emptyLabel}
            </label>
            {fileChoice.options.map((option) => (
              <label className="block" key={option.id}>
                <input
                  type="radio"
                  name={fileChoiceName}
                  checked={fileChoice.selectedId === option.id}
                  onChange={() => fileChoice.onSelect(option.id)}
                />{" "}
                <span className="wrap-anywhere">{option.label}</span>
              </label>
            ))}
          </fieldset>
        ) : null}
        <PublicationPreviewDetailsView
          locale={locale}
          state={inspector.details}
          sourceGroupName={`aggregate-publication-source-${selectedPosition?.id ?? "none"}`}
          onSourceChoice={inspector.onSourceChoice}
          onRefresh={inspector.onRefresh}
        />
      </>
    ) : (
      <p>
        {inspector.kind === "not-prepared" ? text.notPrepared : text.noPosition}
      </p>
    );

  return (
    <section className={styles.root} aria-label={title ?? text.title}>
      <h2 className={styles.title} ref={headingRef} tabIndex={-1}>
        {title ?? text.title}
      </h2>
      <p role="note">{text.independentAdmissions}</p>
      <dl className={styles.summary}>
        {aggregateDispositions.map((disposition) => (
          <div className={styles.summaryRow} key={disposition}>
            <dt>{text.dispositions[disposition]}</dt>
            <dd>{counts[disposition]}</dd>
          </div>
        ))}
      </dl>
      {onModeChange ? (
        <fieldset className={styles.modes}>
          <legend className={styles.modesLegend}>{text.modeLegend}</legend>
          <label>
            <input
              type="radio"
              name={modeName}
              checked={mode === "ordinary"}
              onChange={() => onModeChange("ordinary")}
            />{" "}
            {text.ordinary}
          </label>
          <label>
            <input
              type="radio"
              name={modeName}
              checked={mode === "forced"}
              onChange={() => onModeChange("forced")}
            />{" "}
            {text.forced}
          </label>
        </fieldset>
      ) : null}
      {!positions.length ? (
        <p>{text.none}</p>
      ) : (
        <div className={styles.layout}>
          <section aria-label={text.select} className={styles.positions}>
            <h3 className="sr-only">{text.select}</h3>
            <ul className={styles.positionsList}>
              {positions.map((position) => {
                const isIncluded = included.has(position.id);
                const status = statusLabel(position.status, text);
                const diagnostic = diagnosticLabel(
                  position.diagnostic,
                  text,
                  locale,
                );
                const isSelected = position.id === selectedPosition?.id;
                return (
                  <li className={styles.positionItem} key={position.id}>
                    <div
                      className={`${styles.positionRow} ${isSelected ? styles.positionRowSelected : styles.positionRowIdle}`}
                    >
                      <button
                        type="button"
                        aria-pressed={isSelected}
                        aria-label={position.label}
                        className={styles.positionButton}
                        onClick={() => selectPosition(position.id)}
                      >
                        <span className={styles.positionLabel}>
                          {position.label}
                        </span>
                        <span
                          className={
                            position.diagnostic?.kind === "quality-downgrade"
                              ? "block text-xs wrap-anywhere"
                              : styles.positionStatus
                          }
                        >
                          {status}
                          {isIncluded && diagnostic ? ` · ${diagnostic}` : ""}
                        </span>
                      </button>
                      <label className={styles.includeLabel}>
                        <input
                          type="checkbox"
                          checked={isIncluded}
                          aria-label={`${text.include}: ${position.label}`}
                          onChange={() => onToggleInclude(position.id)}
                        />
                        {text.include}
                      </label>
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
          <section
            aria-label={text.inspector}
            className={styles.inspectorPanel}
          >
            <h3 className={styles.inspectorHeading}>
              {selectedPosition?.label ?? text.inspector}
            </h3>
            <div className={styles.inspectorBody}>
              {!mobileInspectorOpen && inspectorContent}
            </div>
          </section>
        </div>
      )}
      <dialog
        ref={mobileDialog}
        aria-label={text.inspector}
        className={styles.dialog}
        onClose={() => setMobileInspectorOpen(false)}
      >
        <div className={styles.dialogHeader}>
          <h2 className={styles.dialogTitle}>
            {selectedPosition?.label ?? text.inspector}
          </h2>
          <button type="button" onClick={() => setMobileInspectorOpen(false)}>
            {text.close}
          </button>
        </div>
        {mobileInspectorOpen && inspectorContent}
      </dialog>
      <div className={styles.actions}>
        <button
          type="button"
          disabled={!canPrepare || preparing}
          onClick={onPrepare}
        >
          {prepareLabel ?? text.prepare}
        </button>
        <button type="button" disabled={!canConfirm} onClick={onConfirm}>
          {confirmLabel ?? text.confirm}
        </button>
      </div>
    </section>
  );
}
