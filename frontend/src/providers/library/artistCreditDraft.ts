import type {
  LibraryMetadataArtistCredit,
  LibraryMetadataGetResponse,
} from "../../api/generated/client.schemas";
import {
  type ArtistCreditDraft,
  type ArtistCreditOverrideState,
  type ArtistCreditResolutionStatus,
  type ArtistCreditRow,
  createClientKey,
} from "../../ui/features/library/metadata/artistCreditDraft";

export type ArtistCreditMetadataDisplay = Pick<
  LibraryMetadataGetResponse,
  "envelope" | "artist_credit_baseline" | "effective_artist_credit"
>;

export type ArtistCreditMetadataAdapterResult = {
  draft: ArtistCreditDraft;
  /** Relational baseline remains display context, never an override source. */
  baseline: ArtistCreditRow[];
  effective: {
    status: ArtistCreditResolutionStatus;
    origin: string | null;
    rows: ArtistCreditRow[];
  };
};

type ArtistCreditPatchItem = {
  artist_id: string;
  artist_name: string;
  credit_name: string;
  join_phrase: string;
  role?: string;
};

export type ArtistCreditPatchChange =
  | { state: "absent" }
  | { state: "explicit_empty" }
  | { state: "value"; value: ArtistCreditPatchItem[] };

function adaptCredit(
  credit: LibraryMetadataArtistCredit,
  clientKey: string,
): ArtistCreditRow {
  return {
    clientKey,
    id: credit.artist_id,
    name: credit.artist_name,
    credit_name: credit.credit_name,
    join_phrase: credit.join_phrase,
    ...(Object.hasOwn(credit, "role") ? { role: credit.role ?? "" } : {}),
    ...(credit.disambiguation ? { disambiguation: credit.disambiguation } : {}),
    ...(credit.sort_name ? { sortName: credit.sort_name } : {}),
    ...(credit.mbid ? { hasMbid: true } : {}),
  };
}

function readCredits(
  value: readonly LibraryMetadataArtistCredit[] | null | undefined,
  nextClientKey: () => string,
): ArtistCreditRow[] {
  return (value ?? []).map((item) => adaptCredit(item, nextClientKey()));
}

function readDesiredCredits(
  value: unknown,
  nextClientKey: () => string,
): ArtistCreditRow[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item: unknown) => {
    if (typeof item !== "object" || item === null) return [];
    const credit = item as Record<string, unknown>;
    if (
      typeof credit.artist_id !== "string" ||
      typeof credit.artist_name !== "string" ||
      typeof credit.credit_name !== "string" ||
      typeof credit.join_phrase !== "string"
    )
      return [];
    return [
      {
        clientKey: nextClientKey(),
        id: credit.artist_id,
        name: credit.artist_name,
        credit_name: credit.credit_name,
        join_phrase: credit.join_phrase,
        ...(Object.hasOwn(credit, "role") && typeof credit.role === "string"
          ? { role: credit.role }
          : {}),
      },
    ];
  });
}

/**
 * Reads editable artist credits from desired metadata while keeping the
 * relational baseline and server-resolved display facts in separate channels.
 */
export function adaptArtistCreditMetadata(
  metadata: ArtistCreditMetadataDisplay,
  nextClientKey: () => string = createClientKey,
): ArtistCreditMetadataAdapterResult {
  const desired = metadata.envelope.desired.artist_credit;
  const hasDesiredOverride = Object.hasOwn(
    metadata.envelope.desired,
    "artist_credit",
  );
  const state: ArtistCreditOverrideState =
    desired?.state === "value" ||
    desired?.state === "explicit_empty" ||
    desired?.state === "absent"
      ? desired.state
      : "absent";
  const baseline = readCredits(metadata.artist_credit_baseline, nextClientKey);
  const effective = metadata.effective_artist_credit;
  const effectiveRows = readCredits(effective?.credits, nextClientKey);
  const identityByArtist = new Map<string, ArtistCreditRow>();
  for (const row of [...baseline, ...effectiveRows])
    identityByArtist.set(row.id, row);
  const rows =
    state === "value" ? readDesiredCredits(desired?.value, nextClientKey) : [];
  const hydratedRows = rows.map((row) => {
    const identity = identityByArtist.get(row.id);
    if (!identity) return row;
    return {
      ...row,
      name: identity.name,
      ...(identity.disambiguation
        ? { disambiguation: identity.disambiguation }
        : {}),
      ...(identity.sortName ? { sortName: identity.sortName } : {}),
      ...(identity.hasMbid ? { hasMbid: true } : {}),
    };
  });

  return {
    draft: { hasDesiredOverride, state, rows: hydratedRows },
    baseline,
    effective: {
      status: isResolutionStatus(effective?.status)
        ? effective.status
        : "unclassified",
      origin: effective?.origin ?? null,
      rows: effectiveRows,
    },
  };
}

function isResolutionStatus(
  value: string | undefined,
): value is ArtistCreditResolutionStatus {
  return (
    value === "resolved" || value === "unavailable" || value === "unclassified"
  );
}

/** Serialize only editable credit fields and a pre-existing opaque role. */
export function serializeArtistCreditChange(
  draft: ArtistCreditDraft,
): ArtistCreditPatchChange | undefined {
  if (!draft.hasDesiredOverride) return undefined;
  if (draft.state === "absent") return { state: "absent" };
  if (draft.state === "explicit_empty") return { state: "explicit_empty" };
  return {
    state: "value",
    value: draft.rows.map((row) => ({
      artist_id: row.id,
      artist_name: row.name,
      credit_name: row.credit_name,
      join_phrase: row.join_phrase,
      ...(Object.hasOwn(row, "role") ? { role: row.role } : {}),
    })),
  };
}
