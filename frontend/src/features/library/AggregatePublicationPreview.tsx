import { useEffect, useRef, useState } from "react";
import { useI18n } from "../../i18n/I18nProvider";
import type {
  PublicationPreviewController,
  PublicationPreviewIdentity,
  PublicationPreviewState,
} from "./FirstPublicationPreview";
import { PublicationPreviewDetails } from "./PublicationPreviewDetails";

type AggregatePosition = {
  id: string;
  label: string;
  current: boolean;
};

type AggregateDisposition =
  | "create"
  | "update"
  | "unchanged"
  | "skipped"
  | "unprocessable"
  | "blocked";

const aggregateDispositions: AggregateDisposition[] = [
  "create",
  "update",
  "unchanged",
  "skipped",
  "unprocessable",
  "blocked",
];

function dispositionFor(
  position: AggregatePosition,
  included: boolean,
  state: PublicationPreviewState | undefined,
  controllerAvailable: boolean,
  mode: "ordinary" | "forced",
  preparedMode: "ordinary" | "forced" | null,
): AggregateDisposition {
  if (!included) return "skipped";
  if (!controllerAvailable || !state || state.loading || preparedMode !== mode)
    return "blocked";
  if (
    state.blockers.some((blocker) =>
      blocker.startsWith("desired tags are incomplete:"),
    )
  )
    return "unprocessable";
  if (state.confirmationCapabilityStatus === "unchanged") return "unchanged";
  const expectedWorkflow = position.current ? "retained_same_path" : "first";
  const expectedMode = position.current ? mode : "ordinary";
  if (
    state.canConfirm &&
    state.identity?.workflow === expectedWorkflow &&
    state.identity.mode === expectedMode &&
    !state.confirming &&
    !state.admittedOperationID &&
    !state.admissionUncertain
  )
    return position.current ? "update" : "create";
  return "blocked";
}

export function AggregatePublicationPreview({
  positions,
  controllers,
  states,
}: {
  positions: AggregatePosition[];
  controllers: ReadonlyMap<string, PublicationPreviewController>;
  states: Readonly<Record<string, PublicationPreviewState | undefined>>;
}) {
  const { locale } = useI18n();
  const [mode, setMode] = useState<"ordinary" | "forced">("ordinary");
  const [preparedMode, setPreparedMode] = useState<
    "ordinary" | "forced" | null
  >(null);
  const [included, setIncluded] = useState<Set<string>>(
    () => new Set(positions.map((position) => position.id)),
  );
  const [selectedPositionID, setSelectedPositionID] = useState(
    positions[0]?.id ?? "",
  );
  const [mobileInspectorOpen, setMobileInspectorOpen] = useState(false);
  const mobileDialog = useRef<HTMLDialogElement>(null);
  const copy =
    locale === "ru"
      ? {
          title: "Общий предпросмотр публикации",
          independentAdmissions:
            "Подтверждение запускает отдельные операции по позициям; общей атомарной публикации нет.",
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
        }
      : {
          title: "Aggregate publication preview",
          independentAdmissions:
            "Confirmation starts independent operations per position; there is no atomic batch publication.",
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
        };
  const selected = positions.filter((position) => included.has(position.id));
  const selectedStates = selected.map((position) => states[position.id]);
  const allControllersAvailable = selected.every((position) =>
    controllers.has(position.id),
  );
  const dispositions = new Map(
    positions.map((position) => [
      position.id,
      dispositionFor(
        position,
        included.has(position.id),
        states[position.id],
        controllers.has(position.id),
        mode,
        preparedMode,
      ),
    ]),
  );
  const counts = aggregateDispositions.reduce(
    (result, disposition) => {
      result[disposition] = 0;
      return result;
    },
    {} as Record<AggregateDisposition, number>,
  );
  for (const disposition of dispositions.values()) counts[disposition] += 1;
  const hasBlockingPositions = [...dispositions.entries()].some(
    ([positionID, disposition]) =>
      included.has(positionID) &&
      (disposition === "blocked" || disposition === "unprocessable"),
  );
  const readyCount = counts.create + counts.update;
  const preparing = selectedStates.some((state) => state?.loading);
  const canConfirm =
    readyCount > 0 &&
    !hasBlockingPositions &&
    !preparing &&
    preparedMode === mode;
  const selectedPosition =
    positions.find((position) => position.id === selectedPositionID) ??
    positions[0];
  const selectedState = selectedPosition
    ? states[selectedPosition.id]
    : undefined;
  const selectedController = selectedPosition
    ? controllers.get(selectedPosition.id)
    : undefined;

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

  function prepare() {
    if (!selected.length || !allControllersAvailable) return;
    const selectedControllers: PublicationPreviewController[] = [];
    for (const position of selected) {
      const controller = controllers.get(position.id);
      if (!controller) return;
      selectedControllers.push(controller);
    }
    setPreparedMode(mode);
    for (const controller of selectedControllers) controller.prepare(mode);
  }

  function confirm() {
    if (!canConfirm) return;
    const admissions: {
      controller: PublicationPreviewController;
      identity: PublicationPreviewIdentity;
    }[] = [];
    for (const position of selected) {
      const disposition = dispositions.get(position.id);
      if (disposition !== "create" && disposition !== "update") continue;
      const controller = controllers.get(position.id);
      const identity = states[position.id]?.identity;
      if (!controller || !identity) return;
      admissions.push({ controller, identity });
    }
    for (const { controller, identity } of admissions)
      controller.confirm(identity);
  }

  function selectPosition(positionID: string) {
    setSelectedPositionID(positionID);
    const isNarrow = window.matchMedia
      ? window.matchMedia("(max-width: 899px)").matches
      : window.innerWidth < 900;
    if (isNarrow) setMobileInspectorOpen(true);
  }

  const inspector = selectedState ? (
    <PublicationPreviewDetails
      state={selectedState}
      sourceGroupName={`aggregate-publication-source-${selectedPosition?.id ?? "none"}`}
      onSourceChoice={(choice) => selectedController?.selectSource(choice)}
      onRefresh={() => selectedController?.refresh()}
    />
  ) : (
    <p>{selectedPosition ? copy.notPrepared : copy.noPosition}</p>
  );

  return (
    <section
      className="space-y-3 rounded border border-stone-300 p-4"
      aria-label={copy.title}
    >
      <h2 className="font-semibold">{copy.title}</h2>
      <p role="note">{copy.independentAdmissions}</p>
      <dl className="grid gap-x-4 gap-y-1 sm:grid-cols-[max-content_1fr]">
        {aggregateDispositions.map((disposition) => (
          <div className="contents" key={disposition}>
            <dt>{copy.dispositions[disposition]}</dt>
            <dd>{counts[disposition]}</dd>
          </div>
        ))}
      </dl>
      <fieldset className="flex flex-wrap gap-4">
        <legend className="font-medium">
          {locale === "ru"
            ? "Режим повторной публикации CURRENT"
            : "CURRENT republish mode"}
        </legend>
        <label>
          <input
            type="radio"
            name="aggregate-publication-mode"
            checked={mode === "ordinary"}
            onChange={() => {
              setMode("ordinary");
              setPreparedMode(null);
            }}
          />{" "}
          {copy.ordinary}
        </label>
        <label>
          <input
            type="radio"
            name="aggregate-publication-mode"
            checked={mode === "forced"}
            onChange={() => {
              setMode("forced");
              setPreparedMode(null);
            }}
          />{" "}
          {copy.forced}
        </label>
      </fieldset>
      {!positions.length ? (
        <p>{copy.none}</p>
      ) : (
        <div className="grid min-w-0 gap-3 min-[900px]:grid-cols-1 min-[1400px]:grid-cols-[minmax(220px,0.38fr)_minmax(0,1fr)]">
          <section aria-label={copy.select} className="min-w-0">
            <h3 className="sr-only">{copy.select}</h3>
            <ul className="max-h-80 space-y-1 overflow-y-auto min-[1400px]:max-h-[34rem]">
              {positions.map((position) => {
                const isIncluded = included.has(position.id);
                const state = states[position.id];
                const disposition = dispositions.get(position.id) ?? "blocked";
                const status = state?.loading
                  ? copy.loading
                  : preparedMode !== mode
                    ? copy.notPrepared
                    : copy.dispositions[disposition];
                const reason =
                  state?.error ||
                  state?.blockers[0] ||
                  state?.confirmationCapabilityReasonCodes?.[0] ||
                  (!controllers.has(position.id)
                    ? copy.noController
                    : disposition === "blocked"
                      ? copy.notPrepared
                      : "");
                const isSelected = position.id === selectedPosition?.id;
                return (
                  <li className="min-w-0" key={position.id}>
                    <div
                      className={`grid min-h-12 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded border px-2 py-1 ${isSelected ? "border-stone-500 bg-stone-100" : "border-stone-200"}`}
                    >
                      <button
                        type="button"
                        aria-pressed={isSelected}
                        aria-label={position.label}
                        className="min-w-0 text-left focus-visible:outline-2 focus-visible:outline-offset-2"
                        onClick={() => selectPosition(position.id)}
                      >
                        <span className="block truncate font-medium">
                          {position.label}
                        </span>
                        <span className="block truncate text-xs text-stone-600">
                          {status}
                          {isIncluded && reason ? ` · ${reason}` : ""}
                        </span>
                      </button>
                      <label className="flex items-center gap-1 text-xs">
                        <input
                          type="checkbox"
                          checked={isIncluded}
                          aria-label={`${copy.include}: ${position.label}`}
                          onChange={() =>
                            setIncluded((current) => {
                              const next = new Set(current);
                              if (next.has(position.id))
                                next.delete(position.id);
                              else next.add(position.id);
                              return next;
                            })
                          }
                        />
                        {copy.include}
                      </label>
                    </div>
                  </li>
                );
              })}
            </ul>
          </section>
          <section
            aria-label={copy.inspector}
            className="min-w-0 rounded border border-stone-200 p-3 max-[899px]:hidden"
          >
            <h3 className="mb-2 font-medium">
              {selectedPosition?.label ?? copy.inspector}
            </h3>
            <div className="max-h-[34rem] min-w-0 overflow-y-auto">
              {!mobileInspectorOpen && inspector}
            </div>
          </section>
        </div>
      )}
      <dialog
        ref={mobileDialog}
        aria-label={copy.inspector}
        className="m-auto max-h-[90dvh] w-[min(96vw,46rem)] overflow-y-auto rounded border border-stone-300 p-4 backdrop:bg-black/40"
        onClose={() => setMobileInspectorOpen(false)}
      >
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="font-semibold">
            {selectedPosition?.label ?? copy.inspector}
          </h2>
          <button type="button" onClick={() => setMobileInspectorOpen(false)}>
            {copy.close}
          </button>
        </div>
        {mobileInspectorOpen && inspector}
      </dialog>
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          disabled={!selected.length || !allControllersAvailable || preparing}
          onClick={prepare}
        >
          {copy.prepare}
        </button>
        <button type="button" disabled={!canConfirm} onClick={confirm}>
          {copy.confirm}
        </button>
      </div>
    </section>
  );
}
