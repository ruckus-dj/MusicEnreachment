/**
 * Presentation model for the Incoming publication queue.
 *
 * The queue is read-only: it groups already matched editions by whether a
 * publication can start, and the view links back to the existing release,
 * position or matching workflow instead of performing writes.
 */

export type IncomingPublicationReason =
  | { code: "missingPrimaryArtist" }
  | {
      code: "unsafeOutputFilename";
      trackId: string;
      trackTitle: string;
      filename: string;
    }
  | {
      code: "sourceNeedsSelection";
      trackId: string;
      trackTitle: string;
    }
  | { code: "automaticSkip" }
  | { code: "manualReady" };

export type IncomingPublicationTrackAttention =
  | "unsafeOutputFilename"
  | "sourceNeedsSelection";

export type IncomingPublicationNextStepKind =
  | "release"
  | "track"
  | "matching"
  | "primaryArtist";

export type IncomingPublicationNextStep = {
  kind: IncomingPublicationNextStepKind;
  href: string;
};

export type IncomingPublicationTrack = {
  id: string;
  number: string;
  title: string;
  /** Existing position address; omitted when the host has no address. */
  href?: string;
  /** Selected source file name; absent when no source is selected. */
  source?: string;
  attention?: IncomingPublicationTrackAttention;
};

export type IncomingPublicationEdition = {
  id: string;
  releaseId: string;
  title: string;
  /** Release primary artist; empty when the edition has no primary artist. */
  artist: string;
  year: string;
  reason: IncomingPublicationReason;
  nextStep: IncomingPublicationNextStep;
  tracks: readonly IncomingPublicationTrack[];
};

export type IncomingPublicationModel = {
  editions: readonly IncomingPublicationEdition[];
  initialStatus?: "attention" | "ready";
};

export function incomingPublicationNeedsAttention(
  edition: IncomingPublicationEdition,
) {
  return !["automaticSkip", "manualReady"].includes(edition.reason.code);
}

export function incomingPublicationCounts(
  editions: readonly IncomingPublicationEdition[],
) {
  const attention = editions.filter(incomingPublicationNeedsAttention).length;
  return {
    attention,
    ready: editions.length - attention,
    total: editions.length,
  };
}
