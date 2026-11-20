import type { MatchingReviewCandidate } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { MatchingEvidenceInspector as MatchingEvidenceInspectorView } from "../matchingWorkspace";
import type { MatchingWorkspacePosition } from "../matchingWorkspace/types";

type MatchingEvidenceInspectorProps = {
  candidate?: MatchingReviewCandidate;
  position: MatchingWorkspacePosition;
  rawTags?: Record<string, string[] | null>;
};

/**
 * Generated-API adapter for the portable evidence inspector. The view reads an
 * explicit locale; this adapter supplies the application's selected locale.
 */
export function MatchingEvidenceInspector({
  candidate,
  position,
  rawTags,
}: MatchingEvidenceInspectorProps) {
  const { locale } = useI18n();
  return (
    <MatchingEvidenceInspectorView
      candidate={candidate}
      position={position}
      rawTags={rawTags}
      locale={locale}
    />
  );
}
