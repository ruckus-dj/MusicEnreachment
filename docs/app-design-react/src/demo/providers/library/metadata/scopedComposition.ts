import type { MetadataFieldState } from "../../../../ui/features/library/MetadataFieldPresentation";
import type { MetadataMovementDraft } from "../../../../ui/features/library/metadata/structuredMetadataDraft";
import { type MetadataWork, readMetadataWorks } from "./model";
import type { MetadataValues } from "./projection";

type Override<T> = { state: "value"; value: T } | { state: "explicit_empty" };

export type ScopedComposition = {
  works?: Override<MetadataWork[]>;
  movement?: Override<MetadataMovementDraft>;
};

export const compositionKeys = [
  "work",
  "musicbrainz_workid",
  "movementname",
  "movementtotal",
  "movement",
] as const;

export function compositionStates(composition?: ScopedComposition) {
  return {
    work: composition?.works?.state ?? "absent",
    movement: composition?.movement?.state ?? "absent",
  } as const;
}

export function compositionDraftValues(
  composition?: ScopedComposition,
): MetadataValues {
  const works =
    composition?.works?.state === "value" ? composition.works.value : [];
  const movement =
    composition?.movement?.state === "value"
      ? composition.movement.value
      : undefined;
  return {
    work: works.map((row) => row.title),
    musicbrainz_workid: works.map((row) => row.mbid ?? ""),
    movementname: movement ? [movement.name] : [],
    movementtotal: movement ? [movement.count] : [],
    movement: movement ? [movement.number] : [],
  };
}

export function updateComposition(
  current: ScopedComposition | undefined,
  values: MetadataValues,
  states?: Partial<Readonly<Record<string, MetadataFieldState>>>,
): ScopedComposition {
  const next = { ...current };
  if (states?.work === "absent") delete next.works;
  else if (states?.work === "explicit_empty")
    next.works = { state: "explicit_empty" };
  else if (states?.work === "value")
    next.works = { state: "value", value: readMetadataWorks(values) };

  if (states?.movement === "absent") delete next.movement;
  else if (states?.movement === "explicit_empty")
    next.movement = { state: "explicit_empty" };
  else if (states?.movement === "value")
    next.movement = {
      state: "value",
      value: {
        name: values.movementname?.[0] ?? "",
        number: String(Number(values.movement?.[0])),
        count: String(Number(values.movementtotal?.[0])),
      },
    };
  return next;
}

export function projectCompositionTags(composition?: ScopedComposition) {
  const tags: MetadataValues = {};
  const works = composition?.works;
  if (works?.state === "explicit_empty") {
    tags.WORK = [];
    tags.MUSICBRAINZ_WORKID = [];
  } else if (works?.state === "value" && works.value.length) {
    tags.WORK = works.value.map((work) => work.title);
    const ids = works.value.map((work) => work.mbid ?? "");
    if (ids.length && ids.every(Boolean)) tags.MUSICBRAINZ_WORKID = ids;
  }
  const movement = composition?.movement;
  if (movement?.state === "explicit_empty") {
    tags.MOVEMENTNAME = [];
    tags.MOVEMENTNUMBER = [];
    tags.MOVEMENTCOUNT = [];
  } else if (movement?.state === "value") {
    tags.MOVEMENTNAME = [movement.value.name];
    tags.MOVEMENTNUMBER = [movement.value.number];
    tags.MOVEMENTCOUNT = [movement.value.count];
  }
  return tags;
}

export function resolvePublicationComposition(
  position?: ScopedComposition,
  recording?: ScopedComposition,
): ScopedComposition {
  const hasWorks = (works: ScopedComposition["works"]) =>
    works?.state === "explicit_empty" ||
    (works?.state === "value" && works.value.length > 0);
  return {
    works: hasWorks(position?.works)
      ? position?.works
      : hasWorks(recording?.works)
        ? recording?.works
        : undefined,
    movement: position?.movement ?? recording?.movement,
  };
}
