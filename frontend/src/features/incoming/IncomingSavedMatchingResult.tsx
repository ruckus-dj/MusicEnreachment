import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { MatchingLinkResultView } from "../matchingWorkspace/MatchingLinkResultView";
import { incomingSourceFileName } from "./apiMatchingSources";
import type { IncomingConfirmReceipt } from "./confirmReceipts";
import {
  IncomingReceiptPublication,
  type IncomingReceiptPublicationPosition,
} from "./IncomingReceiptPublication";
import { incomingMessages } from "./incomingMessages";

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

  for (const source of sourceRows) {
    const name = names.get(source.file_member_id) ?? text.file;
    const label = labels.get(source.file_member_id) ?? text.unknown;
    const existing = positions.get(source.position_id);
    const duplicateName = sourceRows.some(
      (other) =>
        other.position_id === source.position_id &&
        other.track_source_id !== source.track_source_id &&
        (names.get(other.file_member_id) ?? text.file) === name,
    );
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

export function IncomingSavedMatchingResult({
  receipt,
  locale,
  onContinue,
}: {
  receipt: IncomingConfirmReceipt;
  locale: "en" | "ru";
  onContinue?: () => void;
}) {
  const [publicationOpen, setPublicationOpen] = useState(false);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const publicationButtonRef = useRef<HTMLButtonElement>(null);
  const restorePublicationFocus = useRef(false);
  const model = useMemo(
    () => receiptPresentation(receipt, locale),
    [receipt, locale],
  );
  useLayoutEffect(() => {
    if (publicationOpen) return;
    if (restorePublicationFocus.current) {
      restorePublicationFocus.current = false;
      publicationButtonRef.current?.focus();
    } else {
      headingRef.current?.focus();
    }
  }, [publicationOpen]);

  if (publicationOpen && model.releaseId)
    return (
      <IncomingReceiptPublication
        locale={locale}
        releaseId={model.releaseId}
        releaseTitle={model.releaseTitle}
        positions={model.positions}
        onBack={() => {
          restorePublicationFocus.current = true;
          setPublicationOpen(false);
        }}
      />
    );

  return (
    <MatchingLinkResultView
      locale={locale}
      releaseTitle={model.releaseTitle}
      links={model.links}
      remaining={model.remaining}
      headingRef={headingRef}
      publicationButtonRef={publicationButtonRef}
      publicationDisabled={!model.releaseId || !model.positions.length}
      onOpenPublication={() => setPublicationOpen(true)}
      onContinue={onContinue}
    />
  );
}
