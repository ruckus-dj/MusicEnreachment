import type { MatchingReviewCandidate } from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { MatchingEvidenceInspector as MatchingEvidenceInspectorView } from "../../ui/features/matching";
import type { MatchingWorkspacePosition } from "../../ui/features/matching/types";

type MatchingEvidenceInspectorProps = {
  candidate?: MatchingReviewCandidate;
  position: MatchingWorkspacePosition;
  rawTags?: Record<string, string[] | null>;
  displayTags?: Record<string, string[] | null>;
};

/**
 * Generated-API adapter for the portable evidence inspector. The view reads an
 * explicit locale; this adapter supplies the application's selected locale.
 */
export function MatchingEvidenceInspector({
  candidate,
  position,
  rawTags,
  displayTags,
}: MatchingEvidenceInspectorProps) {
  const { locale } = useI18n();
  return (
    <MatchingEvidenceInspectorView
      candidate={candidate}
      position={position}
      rawTags={rawTags}
      displayTags={displayTags}
      locale={locale}
    />
  );
}
