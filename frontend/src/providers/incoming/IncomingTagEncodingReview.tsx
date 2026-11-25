import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import {
  confirmIncomingGroupTagEncoding,
  getGetIncomingGroupsQueryKey,
  getGetIncomingGroupTagEncodingQueryKey,
  getIncomingGroupTagEncoding,
} from "../../api/generated/client";
import type {
  IncomingTagEncodingBody,
  IncomingTagEncodingCorrectionBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  type IncomingTagEncodingReviewEntry,
  type IncomingTagEncodingReviewFailure,
  IncomingTagEncodingReviewView,
} from "../../ui/features/incoming/IncomingTagEncodingReviewView";

interface Props {
  groupId: string;
  disabled?: boolean;
}

type CorrectionAction = "apply" | "undo";

function groupCorrection(
  data: IncomingTagEncodingBody,
  action: CorrectionAction,
) {
  const members: IncomingTagEncodingCorrectionBody[] = [];
  for (const member of data.members ?? []) {
    const target = {
      variant_id: member.variant_id,
      metadata_result_id: member.metadata_result_id,
    };
    const automatic = member.automatic ?? [];
    const candidates = member.candidates ?? [];
    if (action === "undo") {
      if (member.stored && automatic.length > 0) {
        members.push({ ...target, undo: true });
      }
    } else if (
      candidates.length > 0 ||
      (!member.stored && automatic.length > 0)
    ) {
      members.push({
        ...target,
        candidate_indexes: candidates.map((_, index) => index),
      });
    }
  }
  return {
    generation: data.generation,
    group_revision: data.group_revision,
    digest: data.digest,
    correction_revision: data.correction_revision,
    members,
  };
}

function tagEncodingEntries(
  data?: IncomingTagEncodingBody,
): IncomingTagEncodingReviewEntry[] {
  if (!data) return [];
  return (data.members ?? []).flatMap((member) =>
    [
      ...(member.automatic ?? []).map((entry) => ({
        entry,
        saved: member.stored,
      })),
      ...(member.candidates ?? []).map((entry) => ({
        entry,
        saved: false,
      })),
    ].map(({ entry, saved }) => ({
      id: `${member.variant_id}:${entry.key}:${entry.index}`,
      title: member.tags?.TITLE?.[0] || member.variant_id,
      key: entry.key,
      saved,
      original: entry.original,
      replacement: entry.replacement,
    })),
  );
}

export function IncomingTagEncodingReview({ groupId, disabled }: Props) {
  const { locale } = useI18n();
  const queryClient = useQueryClient();
  const queryKey = getGetIncomingGroupTagEncodingQueryKey(groupId);
  const [open, setOpen] = useState(false);
  const preview = useQuery({
    enabled: open,
    queryKey,
    queryFn: async ({ signal }) => {
      const response = await getIncomingGroupTagEncoding(groupId, {
        signal,
        cache: "no-store",
      });
      if (response.status !== 200) throw new Error("tag encoding unavailable");
      return response.data;
    },
  });
  const correction = useMutation({
    mutationFn: async (action: CorrectionAction) => {
      if (!preview.data) return;
      const body = groupCorrection(preview.data, action);
      if (body.members.length === 0) return;
      const response = await confirmIncomingGroupTagEncoding(groupId, body);
      if (response.status === 409) throw new Error("stale");
      if (response.status !== 200) throw new Error("save failed");
      queryClient.setQueryData(queryKey, response.data);
      await queryClient.invalidateQueries({
        queryKey: getGetIncomingGroupsQueryKey(),
      });
    },
  });
  const data = preview.data;
  const stale = correction.error?.message === "stale";
  const failure: IncomingTagEncodingReviewFailure | undefined =
    preview.isError || correction.isError
      ? {
          reason: stale ? "stale" : preview.isError ? "load" : "apply",
          canReload: stale || preview.isError,
          reloading: preview.isFetching,
        }
      : undefined;
  return (
    <IncomingTagEncodingReviewView
      locale={locale}
      open={open}
      loading={preview.isPending}
      failure={failure}
      done={correction.isSuccess}
      available={Boolean(data)}
      entries={tagEncodingEntries(data)}
      canApply={
        data ? groupCorrection(data, "apply").members.length > 0 : false
      }
      canUndo={data ? groupCorrection(data, "undo").members.length > 0 : false}
      blocked={disabled || correction.isPending || preview.isFetching || stale}
      onToggle={setOpen}
      onReload={() => {
        void preview.refetch().then((result) => {
          if (result.isSuccess) correction.reset();
        });
      }}
      onApply={() => correction.mutate("apply")}
      onUndo={() => correction.mutate("undo")}
    />
  );
}
