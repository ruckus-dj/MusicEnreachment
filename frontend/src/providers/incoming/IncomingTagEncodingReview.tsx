import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";
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
import { AppButton } from "../../ui/components/AppButton";

const messages = {
  en: {
    title: "Tag encoding",
    note: "Changes are saved in MeloTrove. Source audio files are not changed.",
    loading: "Reading captured tags…",
    loadFailed: "Could not read encoding corrections.",
    applyFailed: "Could not save encoding corrections.",
    stale: "The group or its tags changed. Reload the preview before applying.",
    reload: "Reload preview",
    empty: "No encoding corrections for this group.",
    before: "Original",
    after: "Readable",
    saved: "Saved",
    proposed: "Proposed",
    apply: "Apply corrections to group",
    undo: "Undo saved corrections",
    done: "Tag encoding updated.",
  },
  ru: {
    title: "Кодировка тегов",
    note: "Поправки сохраняются в MeloTrove. Исходные аудиофайлы не меняются.",
    loading: "Читаем сохранённые теги…",
    loadFailed: "Не удалось прочитать поправки кодировки.",
    applyFailed: "Не удалось сохранить поправки кодировки.",
    stale:
      "Группа или теги изменились. Обновите предпросмотр перед применением.",
    reload: "Обновить предпросмотр",
    empty: "Для этой группы нет поправок кодировки.",
    before: "Исходное",
    after: "Читаемое",
    saved: "Сохранено",
    proposed: "Предложено",
    apply: "Применить исправления к группе",
    undo: "Отменить сохранённые исправления",
    done: "Кодировка тегов обновлена.",
  },
} as const;

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

export function IncomingTagEncodingReview({ groupId, disabled }: Props) {
  const { locale } = useI18n();
  const copy = messages[locale];
  const queryClient = useQueryClient();
  const queryKey = getGetIncomingGroupTagEncodingQueryKey(groupId);
  const [open, setOpen] = useState(false);
  const regionId = useId();
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
  const hasChanges = data?.members?.some(
    (member) =>
      Boolean(member.automatic?.length) || Boolean(member.candidates?.length),
  );
  const stale = correction.error?.message === "stale";
  const blocked =
    disabled || correction.isPending || preview.isFetching || stale;
  return (
    <details
      className="rounded border border-[var(--line)] px-3 py-2"
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary
        className="cursor-pointer text-sm font-medium"
        aria-controls={regionId}
      >
        {copy.title}
      </summary>
      {open ? (
        <div className="mt-2 space-y-3 text-sm" id={regionId}>
          <p className="text-[var(--muted)]">{copy.note}</p>
          {preview.isPending ? <p role="status">{copy.loading}</p> : null}
          {preview.isError || correction.isError ? (
            <p role="alert">
              {stale
                ? copy.stale
                : preview.isError
                  ? copy.loadFailed
                  : copy.applyFailed}
            </p>
          ) : null}
          {stale || preview.isError ? (
            <AppButton
              isDisabled={preview.isFetching}
              onPress={() => {
                void preview.refetch().then((result) => {
                  if (result.isSuccess) correction.reset();
                });
              }}
            >
              {copy.reload}
            </AppButton>
          ) : null}
          {correction.isSuccess ? <p role="status">{copy.done}</p> : null}
          {hasChanges && data ? (
            <>
              <ul className="max-h-72 space-y-2 overflow-y-auto overscroll-contain">
                {(data.members ?? []).flatMap((member) =>
                  [
                    ...(member.automatic ?? []).map((entry) => ({
                      entry,
                      saved: member.stored,
                    })),
                    ...(member.candidates ?? []).map((entry) => ({
                      entry,
                      saved: false,
                    })),
                  ].map(({ entry, saved }) => (
                    <li
                      className="border-b border-[var(--line)] pb-2"
                      key={`${member.variant_id}:${entry.key}:${entry.index}`}
                    >
                      <p className="wrap-anywhere font-medium">
                        {member.tags?.TITLE?.[0] || member.variant_id} ·{" "}
                        {entry.key}
                        <span className="ml-2 font-normal text-[var(--muted)]">
                          {saved ? copy.saved : copy.proposed}
                        </span>
                      </p>
                      <dl className="mt-1 grid gap-x-3 gap-y-1 wrap-anywhere sm:grid-cols-2">
                        <div>
                          <dt className="text-[var(--muted)]">{copy.before}</dt>
                          <dd>{entry.original}</dd>
                        </div>
                        <div>
                          <dt className="text-[var(--muted)]">{copy.after}</dt>
                          <dd>{entry.replacement}</dd>
                        </div>
                      </dl>
                    </li>
                  )),
                )}
              </ul>
              <div className="flex flex-wrap gap-2">
                <AppButton
                  isDisabled={
                    blocked || !groupCorrection(data, "apply").members.length
                  }
                  onPress={() => correction.mutate("apply")}
                >
                  {copy.apply}
                </AppButton>
                <AppButton
                  isDisabled={
                    blocked || !groupCorrection(data, "undo").members.length
                  }
                  onPress={() => correction.mutate("undo")}
                >
                  {copy.undo}
                </AppButton>
              </div>
            </>
          ) : data ? (
            <p>{copy.empty}</p>
          ) : null}
        </div>
      ) : null}
    </details>
  );
}
