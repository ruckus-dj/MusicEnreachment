import { useState } from "react";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type AggregateDisposition,
  type AggregateInspectorPresentation,
  type AggregatePositionDiagnostic,
  type AggregatePositionView,
  AggregatePublicationPreviewView,
  aggregateDispositions,
} from "./AggregatePublicationPreviewView";
import type {
  PublicationPreviewController,
  PublicationPreviewIdentity,
  PublicationPreviewState,
} from "./FirstPublicationPreview";

type AggregatePosition = {
  id: string;
  label: string;
  current: boolean;
};

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

function diagnosticFor(
  state: PublicationPreviewState | undefined,
  controllerAvailable: boolean,
  disposition: AggregateDisposition,
): AggregatePositionDiagnostic | undefined {
  const text =
    state?.error ||
    state?.blockers[0] ||
    state?.confirmationCapabilityReasonCodes?.[0];
  if (text) return { kind: "text", text };
  if (!controllerAvailable) return { kind: "no-controller" };
  if (disposition === "blocked") return { kind: "not-prepared" };
  return undefined;
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
  const positionViews: AggregatePositionView[] = positions.map((position) => {
    const state = states[position.id];
    const disposition = dispositions.get(position.id) ?? "blocked";
    return {
      id: position.id,
      label: position.label,
      status: state?.loading
        ? { kind: "loading" }
        : preparedMode !== mode
          ? { kind: "not-prepared" }
          : { kind: "disposition", disposition },
      diagnostic: diagnosticFor(
        state,
        controllers.has(position.id),
        disposition,
      ),
    };
  });
  const inspector: AggregateInspectorPresentation = selectedState
    ? {
        kind: "publication",
        details: selectedState,
        onSourceChoice: (choice) => selectedController?.selectSource(choice),
        onRefresh: () => selectedController?.refresh(),
      }
    : selectedPosition
      ? { kind: "not-prepared" }
      : { kind: "empty" };

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

  return (
    <AggregatePublicationPreviewView
      locale={locale}
      mode={mode}
      counts={counts}
      positions={positionViews}
      included={included}
      selectedPositionId={selectedPositionID}
      canPrepare={selected.length > 0 && allControllersAvailable}
      canConfirm={canConfirm}
      preparing={preparing}
      inspector={inspector}
      onModeChange={(nextMode) => {
        setMode(nextMode);
        setPreparedMode(null);
      }}
      onToggleInclude={(positionID) =>
        setIncluded((current) => {
          const next = new Set(current);
          if (next.has(positionID)) next.delete(positionID);
          else next.add(positionID);
          return next;
        })
      }
      onSelectPosition={setSelectedPositionID}
      onPrepare={prepare}
      onConfirm={confirm}
    />
  );
}
