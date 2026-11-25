import { incomingMessages } from "../../ui/features/incoming/incomingReviewMessages";
import type { MatchingSavedReceiptModel } from "../../ui/features/matching/MatchingSavedReceiptView";
import { incomingSourceFileName } from "./apiMatchingSources";
import type { IncomingConfirmReceipt } from "./confirmReceipts";
import {
  IncomingReceiptPublication,
  type IncomingReceiptPublicationPosition,
} from "./IncomingReceiptPublication";

function receiptPresentation(
  receipt: IncomingConfirmReceipt,
  locale: "en" | "ru",
) {
  const text = incomingMessages[locale];
  const names = new Map<string, string>();
  const labels = new Map<string, string>();
  const positions = new Map<string, IncomingReceiptPublicationPosition>();
  const links = [];
  const sourceRows = receipt.response.confirmed_sources ?? [];

  if (receipt.kind === "manual") {
    for (const member of receipt.response.members ?? []) {
      const path = member.locations?.find(Boolean);
      if (path)
        names.set(member.file_member_id, incomingSourceFileName(path) ?? path);
      const assignment = member.assignment;
      if (!assignment) continue;
      const track = receipt.response.selected_release.positions?.find(
        (item) => item.track_id === assignment.provider_track_id,
      );
      labels.set(
        member.file_member_id,
        `${track?.displayed_number || assignment.position} · ${assignment.title_override || track?.title || text.unknown}`,
      );
    }
  } else {
    for (const source of receipt.response.sources ?? []) {
      const path = source.locations?.find(Boolean);
      if (path)
        names.set(source.file_member_id, incomingSourceFileName(path) ?? path);
    }
    for (const assignment of receipt.assignments ?? []) {
      const track = receipt.response.tracks?.find(
        (item) => item.provider_track_id === assignment.provider_track_id,
      );
      if (track)
        labels.set(
          assignment.file_member_id,
          `${track.displayed_number || track.position + 1} · ${track.title}`,
        );
    }
  }

  const namesByPosition = new Map<string, Map<string, number>>();
  for (const source of sourceRows) {
    const name = names.get(source.file_member_id) ?? text.file;
    const counts =
      namesByPosition.get(source.position_id) ?? new Map<string, number>();
    counts.set(name, (counts.get(name) ?? 0) + 1);
    namesByPosition.set(source.position_id, counts);
  }

  for (const source of sourceRows) {
    const name = names.get(source.file_member_id) ?? text.file;
    const label = labels.get(source.file_member_id) ?? text.unknown;
    const existing = positions.get(source.position_id);
    const duplicateName =
      (namesByPosition.get(source.position_id)?.get(name) ?? 0) > 1;
    const file = {
      id: source.track_source_id,
      label: duplicateName ? `${name} · ${source.media_variant_id}` : name,
      receipt: {
        kind: receipt.kind,
        requestId: receipt.requestId,
        trackSourceId: source.track_source_id,
        mediaVariantId: source.media_variant_id,
      },
    };
    links.push({
      key: file.id,
      source: file.label,
      positionId: source.position_id,
      position: label,
    });
    positions.set(source.position_id, {
      id: source.position_id,
      label: existing?.label ?? label,
      sources: [...(existing?.sources ?? []), file],
    });
  }

  const omitted =
    receipt.kind === "manual"
      ? receipt.response.omitted_member_ids
      : receipt.response.omitted_file_member_ids;
  return {
    releaseId: receipt.response.local_release_id,
    releaseTitle:
      receipt.kind === "manual"
        ? receipt.response.selected_release.title
        : receipt.response.release_title,
    links,
    positions: [...positions.values()],
    remaining: (omitted ?? []).map((key) => ({
      key,
      name:
        names.get(key) ??
        (receipt.kind === "manual"
          ? receipt.omittedFileLabels[key]
          : undefined) ??
        text.file,
    })),
  };
}

/**
 * Projects one saved matching receipt onto the portable saved-result model.
 *
 * The model keeps the exact confirmed sources as links and positions. The
 * publication controller is mounted lazily by the portable view only while
 * its publication surface is open.
 */
export function incomingSavedReceiptModel({
  receipt,
  locale,
  onContinue,
}: {
  receipt: IncomingConfirmReceipt;
  locale: "en" | "ru";
  onContinue?: () => void;
}): MatchingSavedReceiptModel {
  const model = receiptPresentation(receipt, locale);
  const releaseId = model.releaseId;
  return {
    key: receipt.requestId,
    locale,
    releaseTitle: model.releaseTitle,
    links: model.links,
    remaining: model.remaining,
    focusIntent: receipt.requestId,
    onContinue,
    publicationDisabled: !model.positions.length,
    publication: releaseId
      ? {
          bind: (children) => (
            <IncomingReceiptPublication
              locale={locale}
              releaseId={releaseId}
              positions={model.positions}
            >
              {children}
            </IncomingReceiptPublication>
          ),
        }
      : undefined,
  };
}
