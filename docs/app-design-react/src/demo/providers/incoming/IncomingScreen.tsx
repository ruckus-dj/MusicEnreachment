import { useEffect, useRef, useState } from "react";
import {
  ApplicationIncomingListBinder,
  type ApplicationIncomingListModel,
  ApplicationRouteOutlet,
} from "../../../ui/app/ApplicationRouteOutlet";
import { navigateHash } from "../../../ui/app/hashLocation";
import { focusView } from "../../../ui/components/useViewFocus";
import type { IncomingGroupViewRow } from "../../../ui/features/incoming/IncomingGroupsOverviewView";
import { incomingGroupsEmpty } from "../../../ui/features/incoming/IncomingGroupsPanel";
import {
  type IncomingMessage,
  incomingMessage,
} from "../../../ui/features/incoming/incomingOverviewMessages";
import { incomingMessages } from "../../../ui/features/incoming/incomingReviewMessages";
import {
  compareSourceFiles,
  readSourceTrackOrder,
} from "../../../ui/features/matching/sourceFileOrder";
import { useI18n } from "../demoPreferences";
import type { CatalogArtist } from "../library/catalogData";
import { formatUiNotice } from "../notices/uiNotice";
import {
  incomingGroupMatchingHref,
  incomingGroupsListHref,
  parseIncomingGroupsRoute,
} from "./incomingGroupsRoute";
import {
  artistName,
  createIncomingState,
  type IncomingFiles,
  type IncomingGroup,
  type IncomingReleases,
  type IncomingState,
  incomingFixtures,
  usableFile,
} from "./incomingModel";
import { incomingOverviewRows } from "./incomingOverview";
import { canSplitGroup, splitFileIntoNewGroup } from "./incomingSplitModel";

export type IncomingScreenProps = {
  destination?: import("../publication/publicationOutput").PublicationDestination;
  files?: IncomingFiles;
  releases?: IncomingReleases;
  providers?: IncomingReleases;
  roots?: readonly { path: string }[];
  artists?: CatalogArtist[];
  state?: IncomingState;
  onState?: (state: IncomingState) => void;
  onFiles?: (files: IncomingFiles) => void;
  onReleases?: (releases: IncomingReleases) => void;
  /** Shared publication scheduler owned above the Incoming workspace. */
  publication?: import("../matching/matchingReceiptPublicationQueue").MatchingReceiptPublicationQueuePort;
};

export function useIncomingScreenDemoProvider({
  files: givenFiles,
  roots = [],
  artists = incomingFixtures.artists,
  state: givenState,
  onState,
}: IncomingScreenProps): ApplicationIncomingListModel {
  const { locale } = useI18n();
  const msg = (
    key: IncomingMessage,
    values?: Record<string, string | number>,
  ) => incomingMessage(locale, key, values);
  const renderReason = (reason: IncomingGroup["reason"]) => {
    if (typeof reason === "string") return reason;
    switch (reason.code) {
      case "incomplete":
        return msg("incompleteReason");
      case "confident":
        return msg("confidentReason", reason.params);
      case "compilation":
        return msg("compilationReason");
    }
  };
  const [localState, setLocalState] = useState(createIncomingState);
  const [localFiles] = useState<IncomingFiles>(() =>
    structuredClone(incomingFixtures.files),
  );
  const state = givenState ?? localState;
  const [hash, setHash] = useState(() => window.location.hash);
  const groupDisclosureRefs = useRef(new Map<string, HTMLButtonElement>());
  const pendingGroupFocus = useRef<{
    groupId: string;
    preventScroll: boolean;
  } | null>(null);
  const incomingRoute = parseIncomingGroupsRoute(hash);
  const expanded =
    incomingRoute?.view === "list" ? incomingRoute.expandedGroupIds : [];
  useEffect(() => {
    const onHashChange = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  const files = givenFiles ?? localFiles;
  const fileNames = new Intl.Collator(locale, {
    numeric: true,
    sensitivity: "base",
  });
  function sortedMembers(fileIds: readonly string[]) {
    return fileIds
      .map((id) => {
        const file = files[id];
        return {
          id,
          name: file?.name ?? "",
          ...readSourceTrackOrder(file?.tags),
        };
      })
      .sort((a, b) => compareSourceFiles(a, b, "track", fileNames));
  }
  const updateState = () => {
    const next = structuredClone(state);
    givenState ? onState?.(next) : setLocalState(next);
  };
  function toggleGroup(id: string) {
    const ids = expanded.includes(id)
      ? expanded.filter((openId) => openId !== id)
      : [...expanded, id];
    navigateHash(incomingGroupsListHref(ids));
  }
  function moveFile(sourceId: string, fileId: string, targetId: string) {
    const source = state.groups.find((group) => group.id === sourceId);
    const target = state.groups.find((group) => group.id === targetId);
    if (!source?.fileIds.includes(fileId) || !target || source === target)
      return false;
    const invalidate = (group: IncomingGroup, fileIds: string[]) => ({
      ...group,
      fileIds,
      autoEligible: false,
      processed: false,
      drafts: {},
      reason: { code: "incomplete" as const },
    });
    const next = {
      ...state,
      plan: null,
      groups: state.groups
        .map((group) =>
          group === source
            ? invalidate(
                group,
                group.fileIds.filter((id) => id !== fileId),
              )
            : group === target
              ? invalidate(group, [...new Set([...group.fileIds, fileId])])
              : group,
        )
        .filter((group) => group.fileIds.length),
    };
    givenState ? onState?.(next) : setLocalState(next);
    return true;
  }
  function splitFile(sourceId: string, fileId: string) {
    const next = splitFileIntoNewGroup(state, files, sourceId, fileId);
    if (!next) return false;
    givenState ? onState?.(next) : setLocalState(next);
    return true;
  }
  function commitMove(sourceId: string, fileId: string, targetId: string) {
    const sourceRemains = Boolean(
      state.groups
        .find((group) => group.id === sourceId)
        ?.fileIds.some((id) => id !== fileId),
    );
    queueGroupFocus(sourceRemains ? sourceId : targetId);
    if (!moveFile(sourceId, fileId, targetId)) pendingGroupFocus.current = null;
  }
  function commitSplit(sourceId: string, fileId: string) {
    queueGroupFocus(sourceId);
    if (!splitFile(sourceId, fileId)) pendingGroupFocus.current = null;
  }
  function queueGroupFocus(groupId: string) {
    pendingGroupFocus.current = {
      groupId,
      preventScroll: !document.activeElement?.matches(":focus-visible"),
    };
  }
  const groups = state.groups;
  useEffect(() => {
    const pending = pendingGroupFocus.current;
    if (!pending) return;
    pendingGroupFocus.current = null;
    focusView(groupDisclosureRefs.current.get(pending.groupId), {
      preventScroll: pending.preventScroll,
    });
  }, [groups]);
  const rows = incomingOverviewRows(
    groups,
    files,
    locale,
    roots.map((root) => root.path),
  );
  const viewRows = rows.map(
    ({
      group,
      name,
      memberCount,
      pathCount,
      readyCount,
      paths,
    }): IncomingGroupViewRow => {
      const opened = expanded.includes(group.id);
      const splitEnabled = canSplitGroup(group, files);
      const moveDestinations = rows
        .filter((destinationRow) => destinationRow.group.id !== group.id)
        .map((destinationRow) => ({
          id: destinationRow.group.id,
          name: destinationRow.name,
          paths: destinationRow.folderPaths,
        }));
      const fileRows = opened
        ? sortedMembers(group.fileIds).map(({ id, trackNumber }) => {
            const file = files[id];
            const tag = (key: string) => file?.tags?.[key]?.join(" · ");
            return {
              id,
              trackNumber: trackNumber ?? tag("TRACKNUMBER"),
              title: tag("TITLE") || msg("missingFileTitle"),
              path: file?.path,
              artist: tag("ARTIST") || tag("ALBUMARTIST") || msg("noValue"),
              album: tag("ALBUM") || msg("noValue"),
              canMove: moveDestinations.length > 0,
              canCreateGroup: splitEnabled && usableFile(file),
            };
          })
        : [];
      return {
        id: group.id,
        name,
        secondary:
          artistName(group.artistId, artists) || msg("artistNotSpecified"),
        status: renderReason(group.reason),
        members: memberCount,
        paths: pathCount,
        ready: readyCount,
        matchingHref: incomingGroupMatchingHref(group.id),
        opened,
        files: fileRows,
        pathEvidence: paths,
        destinations: moveDestinations,
      };
    },
  );
  const notice =
    state.notice ||
    (state.uiNotice
      ? formatUiNotice(locale, state.uiNotice)
      : state.noticeDescriptor?.code === "automaticPublished"
        ? msg("automaticNotice", state.noticeDescriptor.params)
        : state.noticeDescriptor
          ? msg("noAutomaticNotice")
          : "");
  const labels = {
    caption: msg("overviewCaption"),
    group: msg("group"),
    filesPaths: msg("uniqueFilesPaths"),
    ready: msg("readyTotal"),
    status: msg("evaluationState"),
    empty: incomingGroupsEmpty(locale),
    number: msg("columnNumber"),
    title: msg("columnTitle"),
    artist: msg("columnArtist"),
    album: msg("columnAlbum"),
    actions: msg("columnActions"),
    noTitle: msg("missingFileTitle"),
    noValue: msg("noValue"),
    expand: (name: string) => msg("filesIn", { name }),
    openGroup: (name: string) => msg("openGroup", { name }),
    openGroupAction: msg("openGroupAction"),
  };
  const listModel: ApplicationIncomingListModel = {
    locale,
    headingAction: {
      label: incomingMessages[locale].refreshGroups,
      onPress: updateState,
    },
    notice,
    panel: {
      labels,
      rows: viewRows,
      onToggle: toggleGroup,
      onMove: (groupId, memberId, targetGroupId, focus) => {
        if (focus) commitMove(groupId, memberId, targetGroupId);
        else moveFile(groupId, memberId, targetGroupId);
      },
      onSplit: (groupId, memberId) => commitSplit(groupId, memberId),
      disclosureRef: (groupId, node) => {
        if (node) groupDisclosureRefs.current.set(groupId, node);
        else groupDisclosureRefs.current.delete(groupId);
      },
    },
  };
  return listModel;
}

export function IncomingScreen(props: IncomingScreenProps) {
  const model = useIncomingScreenDemoProvider(props);
  return (
    <ApplicationRouteOutlet
      route="incoming-list"
      provider={ApplicationIncomingListBinder}
      providerProps={{ model }}
    />
  );
}
