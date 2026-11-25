export type ArtistCreditOverrideState = "absent" | "value" | "explicit_empty";
export type ArtistCreditResolutionStatus =
  | "resolved"
  | "unavailable"
  | "unclassified";

export type ArtistCreditRow = {
  /** Stable identity for this occurrence in the editable list. */
  clientKey: string;
  /** Local artist identity and readonly identity snapshot. */
  id: string;
  name: string;
  /** Exact editable credit fields; empty strings are meaningful. */
  credit_name: string;
  join_phrase: string;
  /** Opaque legacy value. It is preserved but never exposed as an editor. */
  role?: string;
  disambiguation?: string;
  sortName?: string;
  hasMbid?: boolean;
  providerFacts?: readonly ArtistCreditProviderFact[];
};

export type ArtistCreditProviderFact = {
  label: string;
  value: string;
};

export type ArtistCreditDraft = {
  /** Whether desired.artist_credit was present in the server envelope. */
  hasDesiredOverride: boolean;
  state: ArtistCreditOverrideState;
  rows: ArtistCreditRow[];
};

export type ArtistCreditSearchResult = {
  id: string;
  name: string;
  disambiguation?: string;
  providerFacts?: readonly ArtistCreditProviderFact[];
};

export function createClientKey(): string {
  return globalThis.crypto.randomUUID();
}

export function createArtistCreditRow(
  artist: ArtistCreditSearchResult,
  clientKey: string = createClientKey(),
): ArtistCreditRow {
  return {
    clientKey,
    id: artist.id,
    name: artist.name,
    credit_name: artist.name,
    join_phrase: "",
    ...(artist.disambiguation ? { disambiguation: artist.disambiguation } : {}),
    ...(artist.providerFacts ? { providerFacts: artist.providerFacts } : {}),
  };
}
