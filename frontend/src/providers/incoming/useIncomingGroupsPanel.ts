import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  confirmIncomingGroups,
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  previewIncomingGroups,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupingActionInput,
  IncomingGroupingEditBody,
  IncomingGroupsBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { focusView } from "../../ui/components/useViewFocus";
import type { IncomingGroupViewRow } from "../../ui/features/incoming/IncomingGroupsOverviewView";
import { incomingGroupsEmpty } from "../../ui/features/incoming/IncomingGroupsPanel";
import type { IncomingScreenViewProps } from "../../ui/features/incoming/IncomingScreenView";
import {
  incomingGroupMatchingHref,
  incomingGroupsListHref,
  parseIncomingGroupsRoute,
} from "../../ui/features/incoming/incomingGroupsRoute";
import { incomingMessage } from "../../ui/features/incoming/incomingOverviewMessages";
import {
  incomingMessages,
  incomingOverviewCopy as overviewCopy,
} from "../../ui/features/incoming/incomingReviewMessages";
import { compareSourceFiles } from "../../ui/features/matching/sourceFileOrder";
import {
  incomingSourceFileName,
  readIncomingSourceTrackOrder,
} from "./apiMatchingSources";
import {
  type IncomingGroupName,
  incomingGroupNameText,
} from "./incomingGroupNames";
import { incomingGroupCountQueryKey } from "./incomingGroupsQueryKeys";
import { incomingOverviewOrder } from "./incomingOverviewOrder";

function tag(file: IncomingFileResponse | undefined, ...keys: string[]) {
  for (const key of keys) {
    const value = file?.tags?.[key]?.filter(Boolean).join(" · ");
    if (value) return value;
  }
  return "";
}

function displayTag(file: IncomingFileResponse | undefined, ...keys: string[]) {
  for (const key of keys) {
    const value = (file?.display_tags?.[key] ?? file?.tags?.[key])
      ?.filter(Boolean)
      .join(" · ");
    if (value) return value;
  }
  return "";
}

function groupContaining(
  snapshot: IncomingGroupsBody,
  memberIds: readonly string[],
) {
  const wanted = new Set(memberIds);
  return snapshot.groups?.find((group) =>
    (group.members ?? []).some((id) => wanted.has(id)),
  );
}

/**
 * Incoming groups model and regrouping workflow, without markup.
 *
 * The hook is called unconditionally even when no snapshot is loaded or the
 * list view is hidden behind matching. It returns the overview table props
 * once a snapshot exists; preview, confirmation, cache reconciliation and
 * focus restoration remain in this module.
 */
export function useIncomingGroupsPanel({
  snapshot,
  groupNames,
  expandedGroupIds = [],
}: {
  snapshot?: IncomingGroupsBody;
  groupNames: ReadonlyMap<string, IncomingGroupName>;
  expandedGroupIds?: readonly string[];
}): IncomingScreenViewProps["panel"] {
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const ui = overviewCopy[locale];
  const queryClient = useQueryClient();
  const busy = useRef(false);
  const groupDisclosureRefs = useRef(new Map<string, HTMLButtonElement>());
  const pendingGroupFocus = useRef<{
    groupId: string;
    preventScroll: boolean;
  } | null>(null);
  const [feedback, setFeedback] = useState<
    "moving" | "moved" | "split" | "stale" | "failed"
  >();
  const files = useMemo(
    () =>
      new Map((snapshot?.files ?? []).map((file) => [file.variant_id, file])),
    [snapshot?.files],
  );
  const { groups, folderPaths } = useMemo(
    () => incomingOverviewOrder(snapshot?.groups ?? [], files),
    [snapshot?.groups, files],
  );
  const labels = useMemo(() => {
    const base = new Map(
      groups.map(
        (group) =>
          [
            group.id,
            incomingGroupNameText(groupNames.get(group.id), locale),
          ] as const,
      ),
    );
    const counts = new Map<string, number>();
    for (const text of base.values())
      counts.set(text, (counts.get(text) ?? 0) + 1);
    const ordinals = new Map<string, number>();
    return new Map(
      [...base].map(([id, text]) => {
        if ((counts.get(text) ?? 0) < 2) return [id, text] as const;
        const number = (ordinals.get(text) ?? 0) + 1;
        ordinals.set(text, number);
        return [id, ui.repeatedGroupName(text, number)] as const;
      }),
    );
  }, [groups, groupNames, locale, ui]);
  const label = (id: string) => labels.get(id) ?? "";
  const collator = useMemo(
    () => new Intl.Collator(locale, { numeric: true, sensitivity: "base" }),
    [locale],
  );
  function toggle(id: string) {
    window.location.hash = incomingGroupsListHref(
      expandedGroupIds.includes(id)
        ? expandedGroupIds.filter((openId) => openId !== id)
        : [...expandedGroupIds, id],
    );
  }

  useEffect(() => {
    const pending = pendingGroupFocus.current;
    if (!pending) return;
    // Wait for the confirmed snapshot that actually renders the target row.
    if (!snapshot?.groups?.some((group) => group.id === pending.groupId))
      return;
    pendingGroupFocus.current = null;
    focusView(groupDisclosureRefs.current.get(pending.groupId), {
      preventScroll: pending.preventScroll,
    });
  }, [snapshot]);

  async function previewAndConfirm(actions: IncomingGroupingActionInput[]) {
    if (!snapshot) return { status: "failed" as const };
    const request = {
      epoch: snapshot.epoch,
      base_revision: snapshot.revision,
      actions,
    } satisfies IncomingGroupingEditBody;
    const key = getGetIncomingGroupsQueryKey();
    const preview = await previewIncomingGroups(request);
    if (preview.status !== 200 || !preview.data.draft_revision) {
      await queryClient.invalidateQueries({ queryKey: key });
      return {
        status:
          preview.status === 409 ? ("stale" as const) : ("failed" as const),
      };
    }
    const confirmed = await confirmIncomingGroups({
      ...request,
      draft_revision: preview.data.draft_revision,
    });
    if (confirmed.status !== 200) {
      await queryClient.invalidateQueries({ queryKey: key });
      return {
        status:
          confirmed.status === 409 ? ("stale" as const) : ("failed" as const),
      };
    }
    return { status: "ok" as const, snapshot: confirmed.data };
  }

  async function applyConfirmed(
    next: IncomingGroupsBody,
    outcome: "moved" | "split",
    afterConfirm: (
      accepted: IncomingGroupsBody,
    ) => ReadonlyMap<string, string | undefined>,
  ) {
    const key = getGetIncomingGroupsQueryKey();
    await queryClient.cancelQueries({ queryKey: key, exact: true });
    const cached = queryClient.getQueryData<IncomingGroupsBody>(key);
    const accepted = cached && cached.epoch > next.epoch ? cached : next;
    const remapped = afterConfirm(accepted);
    if (accepted !== cached) queryClient.setQueryData(key, accepted);
    const route = parseIncomingGroupsRoute(window.location.hash);
    if (route?.view === "list") {
      const ids = new Set(
        route.expandedGroupIds.flatMap((id) => {
          const replacement = remapped.has(id) ? remapped.get(id) : id;
          return replacement ? [replacement] : [];
        }),
      );
      const href = incomingGroupsListHref([...ids]);
      if (href !== window.location.hash) window.location.replace(href);
    }
    const matchingIds = [...remapped].flatMap(([prior, current]) =>
      current ? [prior, current] : [prior],
    );
    setFeedback(outcome);
    await Promise.all([
      queryClient.invalidateQueries({
        queryKey: incomingGroupCountQueryKey(),
        exact: true,
      }),
      ...[...new Set(matchingIds)].map((id) =>
        queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupMatchingQueryKey(id),
        }),
      ),
    ]);
  }

  async function move(
    from: string,
    member: string,
    target: string,
    focus: boolean,
  ) {
    if (busy.current || from === target) return;
    const source = groups.find((group) => group.id === from);
    if (
      !source?.members?.includes(member) ||
      !groups.some((group) => group.id === target)
    )
      return;
    /*
     * The menu closes before the confirm response arrives, so keyboard focus
     * must be captured now. Drag and drop never moves focus on its own.
     */
    const preventScroll = !(
      document.activeElement?.matches(":focus-visible") ?? false
    );
    busy.current = true;
    setFeedback("moving");
    try {
      const result = await previewAndConfirm([
        { kind: "move", member_ids: [member], target_group_id: target },
      ]);
      if (result.status !== "ok") {
        setFeedback(result.status);
        return;
      }
      /*
       * Manual regrouping can re-key the destination, so it is resolved by the
       * confirmed member rather than the pre-confirmation target ID. Unready
       * files keep the previous group's hidden identity, so the remainder is
       * looked up through ready members only.
       */
      await applyConfirmed(result.snapshot, "moved", (accepted) => {
        const movedGroup = groupContaining(accepted, [member]);
        const remainingGroup = groupContaining(
          accepted,
          (source.members ?? []).filter((id) => id !== member),
        );
        const destination = remainingGroup ?? movedGroup;
        pendingGroupFocus.current =
          focus && destination && accepted === result.snapshot
            ? { groupId: destination.id, preventScroll }
            : null;
        return new Map([
          [from, remainingGroup?.id],
          [target, movedGroup?.id],
        ]);
      });
    } catch {
      setFeedback("failed");
      await queryClient.invalidateQueries({
        queryKey: getGetIncomingGroupsQueryKey(),
      });
    } finally {
      busy.current = false;
    }
  }

  async function split(from: string, member: string) {
    if (busy.current) return;
    const source = groups.find((group) => group.id === from);
    const ready = source?.members ?? [];
    const unready = new Set(source?.unready_members ?? []);
    if (
      !source ||
      unready.has(member) ||
      !ready.includes(member) ||
      ready.length < 2
    )
      return;
    const preventScroll = !(
      document.activeElement?.matches(":focus-visible") ?? false
    );
    busy.current = true;
    setFeedback("moving");
    try {
      const result = await previewAndConfirm([
        { kind: "split", group_id: from, member_ids: [member] },
      ]);
      if (result.status !== "ok") {
        setFeedback(result.status);
        return;
      }
      await applyConfirmed(result.snapshot, "split", (accepted) => {
        const remainingGroup = groupContaining(
          accepted,
          ready.filter((id) => id !== member),
        );
        pendingGroupFocus.current =
          remainingGroup && accepted === result.snapshot
            ? { groupId: remainingGroup.id, preventScroll }
            : null;
        return new Map([[from, remainingGroup?.id]]);
      });
    } catch {
      setFeedback("failed");
      await queryClient.invalidateQueries({
        queryKey: getGetIncomingGroupsQueryKey(),
      });
    } finally {
      busy.current = false;
    }
  }

  const rows = groups.map((group): IncomingGroupViewRow => {
    const opened = expandedGroupIds.includes(group.id);
    const groupName = label(group.id);
    const unready = new Set(group.unready_members ?? []);
    const members = new Set([...(group.members ?? []), ...unready]);
    const readyCount = members.size - unready.size;
    /*
     * Sort only this visual copy: the member set, stable row keys, move
     * targets and the drag payload keep the server's original composition
     * and ordering of actions unchanged.
     */
    const orderedMembers = opened
      ? [...members]
          .map((id) => {
            const file = files.get(id);
            return {
              id,
              ...readIncomingSourceTrackOrder(file?.tags),
              name:
                incomingSourceFileName(file?.locations?.[0]?.relative_path) ??
                "",
            };
          })
          .sort((left, right) =>
            compareSourceFiles(left, right, "track", collator),
          )
      : [];
    const paths = new Set(
      [...members].flatMap((id) =>
        (files.get(id)?.locations ?? []).map(
          (location) => `${location.root_id}:${location.relative_path}`,
        ),
      ),
    );
    const destinations =
      opened && groups.length > 1
        ? groups
            .filter((other) => other.id !== group.id)
            .map((other) => ({
              id: other.id,
              name: label(other.id),
              paths: folderPaths.get(other.id) ?? [],
            }))
        : [];
    return {
      id: group.id,
      name: groupName,
      secondary: group.manual ? copy.manualGrouping : copy.automaticGrouping,
      members: members.size,
      paths: paths.size,
      ready: readyCount,
      matchingHref: incomingGroupMatchingHref(group.id),
      opened,
      files: orderedMembers.map(({ id, trackNumber }) => {
        const file = files.get(id);
        return {
          id,
          trackNumber:
            trackNumber ?? (tag(file, "TRACKNUMBER", "TRACK") || undefined),
          title: displayTag(file, "TITLE") || ui.noTitle,
          path: file?.locations?.[0]?.relative_path,
          artist: displayTag(file, "ARTIST", "ALBUMARTIST") || copy.noValue,
          album: displayTag(file, "ALBUM") || copy.noValue,
          canMove: !unready.has(id) && groups.length > 1,
          canCreateGroup: !unready.has(id) && readyCount > 1,
        };
      }),
      destinations,
    };
  });

  if (!snapshot) return undefined;

  return {
    labels: {
      caption: copy.overviewCaption,
      group: copy.group,
      filesPaths: copy.uniqueFilesPaths,
      ready: copy.readyTotal,
      status: copy.evaluationState,
      number: ui.number,
      title: ui.title,
      artist: ui.artist,
      album: ui.album,
      actions: ui.actions,
      noTitle: ui.noTitle,
      noValue: copy.noValue,
      empty: incomingGroupsEmpty(locale),
      expand: ui.expand,
      openGroup: (name) => incomingMessage(locale, "openGroup", { name }),
      openGroupAction: incomingMessage(locale, "openGroupAction"),
    },
    rows,
    onToggle: toggle,
    onMove: (groupId, memberId, targetGroupId, focus) =>
      void move(groupId, memberId, targetGroupId, focus),
    onSplit: (groupId, memberId) => void split(groupId, memberId),
    disclosureRef: (groupId, node) => {
      if (node) groupDisclosureRefs.current.set(groupId, node);
      else groupDisclosureRefs.current.delete(groupId);
    },
    disabled: feedback === "moving",
    status: feedback ? ui[feedback] : "",
  };
}
