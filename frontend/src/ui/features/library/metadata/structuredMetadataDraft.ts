let nextMetadataDraftRowKey = 0;

export function createMetadataDraftRowKey() {
  nextMetadataDraftRowKey += 1;
  return `metadata-draft-row-${nextMetadataDraftRowKey}`;
}

export type MetadataWorkDraft = {
  uiKey: string;
  name: string;
  mbid?: string;
};
export type MetadataMovementDraft = {
  name: string;
  number: string;
  count: string;
};

export function readMetadataWorkDrafts(value: unknown): MetadataWorkDraft[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const work = item as Record<string, unknown>;
    if (typeof work.name !== "string") return [];
    return [
      {
        uiKey: createMetadataDraftRowKey(),
        name: work.name,
        ...(typeof work.mbid === "string" ? { mbid: work.mbid } : {}),
      },
    ];
  });
}

export function readMetadataMovementDraft(
  value: unknown,
): MetadataMovementDraft {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { name: "", number: "", count: "" };
  }
  const movement = value as Record<string, unknown>;
  return {
    name: typeof movement.name === "string" ? movement.name : "",
    number:
      typeof movement.number === "number" || typeof movement.number === "string"
        ? String(movement.number)
        : "",
    count:
      typeof movement.count === "number" || typeof movement.count === "string"
        ? String(movement.count)
        : "",
  };
}
