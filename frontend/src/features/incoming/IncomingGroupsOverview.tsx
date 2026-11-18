import { useQueryClient } from "@tanstack/react-query";
import { Fragment, useRef, useState } from "react";
import {
  confirmIncomingGroups,
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  previewIncomingGroups,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupingEditBody,
  IncomingGroupsBody,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type IncomingGroupName,
  incomingGroupNameText,
} from "./incomingGroupNames";
import {
  incomingGroupMatchingHref,
  incomingGroupsListHref,
} from "./incomingGroupsRoute";
import { incomingMessages } from "./incomingMessages";

const fileDragType = "application/x-melotrove-group-member";
const cell = "border-b p-2 align-top wrap-anywhere";
const overviewCopy = {
  en: {
    expand: (name: string) => `Files in ${name}`,
    number: "No.",
    title: "Title",
    artist: "Artist",
    album: "Album",
    noTitle: "No title tag",
    destination: "Destination group",
    move: "Move",
    moving: "Moving file…",
    moved: "File moved to the other group.",
    stale:
      "The groups changed on the server. The list was refreshed; try again.",
    failed:
      "Could not confirm the move. Check the refreshed list before retrying.",
  },
  ru: {
    expand: (name: string) => `Файлы группы «${name}»`,
    number: "№",
    title: "Название",
    artist: "Исполнитель",
    album: "Альбом",
    noTitle: "Нет тега названия",
    destination: "Куда переместить",
    move: "Переместить",
    moving: "Перемещаем файл…",
    moved: "Файл перемещён в другую группу.",
    stale: "Группы изменились на сервере. Список обновлён; повторите действие.",
    failed:
      "Не удалось подтвердить перенос. Проверьте обновлённый список перед повтором.",
  },
};

function tag(file: IncomingFileResponse | undefined, ...keys: string[]) {
  for (const key of keys) {
    const value = file?.tags?.[key]?.filter(Boolean).join(" · ");
    if (value) return value;
  }
  return "";
}

export function IncomingGroupsOverview({
  snapshot,
  groupNames,
  expandedGroupIds = [],
}: {
  snapshot: IncomingGroupsBody;
  groupNames: ReadonlyMap<string, IncomingGroupName>;
  expandedGroupIds?: readonly string[];
}) {
  const { locale } = useI18n();
  const copy = incomingMessages[locale];
  const ui = overviewCopy[locale];
  const queryClient = useQueryClient();
  const movingRef = useRef(false);
  const dragging = useRef<{ groupId: string; memberId: string } | null>(null);
  const [dropTarget, setDropTarget] = useState("");
  const [moveTarget, setMoveTarget] = useState<Record<string, string>>({});
  const [feedback, setFeedback] = useState<
    "moving" | "moved" | "stale" | "failed"
  >();
  const groups = snapshot.groups ?? [];
  const files = new Map(
    (snapshot.files ?? []).map((file) => [file.variant_id, file]),
  );
  const name = (id: string) =>
    incomingGroupNameText(groupNames.get(id), locale);
  function toggle(id: string) {
    window.location.hash = incomingGroupsListHref(
      expandedGroupIds.includes(id)
        ? expandedGroupIds.filter((openId) => openId !== id)
        : [...expandedGroupIds, id],
    );
  }

  async function move(from: string, member: string, target: string) {
    if (
      movingRef.current ||
      from === target ||
      !groups.find((group) => group.id === from)?.members?.includes(member) ||
      !groups.some((group) => group.id === target)
    )
      return;
    const request = {
      epoch: snapshot.epoch,
      base_revision: snapshot.revision,
      actions: [
        { kind: "move", member_ids: [member], target_group_id: target },
      ],
    } satisfies IncomingGroupingEditBody;
    const key = getGetIncomingGroupsQueryKey();
    movingRef.current = true;
    setFeedback("moving");
    try {
      const preview = await previewIncomingGroups(request);
      if (preview.status !== 200 || !preview.data.draft_revision) {
        setFeedback(preview.status === 409 ? "stale" : "failed");
        await queryClient.invalidateQueries({ queryKey: key });
        return;
      }
      const confirmed = await confirmIncomingGroups({
        ...request,
        draft_revision: preview.data.draft_revision,
      });
      if (confirmed.status !== 200) {
        setFeedback(confirmed.status === 409 ? "stale" : "failed");
        await queryClient.invalidateQueries({ queryKey: key });
        return;
      }
      await queryClient.cancelQueries({ queryKey: key });
      queryClient.setQueryData(key, confirmed.data);
      setFeedback("moved");
      setMoveTarget({});
      await Promise.all(
        [from, target].map((id) =>
          queryClient.invalidateQueries({
            queryKey: getGetIncomingGroupMatchingQueryKey(id),
          }),
        ),
      );
    } catch {
      setFeedback("failed");
      await queryClient.invalidateQueries({ queryKey: key });
    } finally {
      movingRef.current = false;
    }
  }

  return (
    <section aria-labelledby="incoming-overview-title">
      <h2 id="incoming-overview-title">{copy.overviewTitle}</h2>
      <p className="min-h-5 text-sm" role="status">
        {feedback && ui[feedback]}
      </p>
      <div className="mt-2 min-w-0 overflow-x-auto">
        <table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-sm">
          <caption className="sr-only">{copy.overviewCaption}</caption>
          <colgroup>
            <col className="w-[36%]" />
            <col className="w-[28%]" />
            <col className="w-[14%]" />
            <col className="w-[22%]" />
          </colgroup>
          <thead>
            <tr>
              <th scope="col" className={cell}>
                {copy.group}
              </th>
              <th scope="col" className={cell}>
                {copy.uniqueFilesPaths}
              </th>
              <th scope="col" className={cell}>
                {copy.readyTotal}
              </th>
              <th scope="col" className={cell}>
                {copy.evaluationState}
              </th>
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => {
              const opened = expandedGroupIds.includes(group.id);
              const groupName = name(group.id);
              const unready = new Set(group.unready_members ?? []);
              const members = new Set([...(group.members ?? []), ...unready]);
              const paths = new Set(
                [...members].flatMap((id) =>
                  (files.get(id)?.locations ?? []).map(
                    (location) =>
                      `${location.root_id}:${location.relative_path}`,
                  ),
                ),
              );
              const detailsId = `incoming-files-${group.id}`;
              return (
                <Fragment key={group.id}>
                  <tr
                    className={`cursor-pointer ${dropTarget === group.id || opened ? "bg-[var(--accent-soft)]" : "hover:bg-[var(--raised)]"}`}
                    data-incoming-group={group.id}
                    onClick={(event) => {
                      if (
                        !(event.target instanceof Element) ||
                        event.target.closest("button,a")
                      )
                        return;
                      toggle(group.id);
                    }}
                    onDragOver={(event) => {
                      if (
                        movingRef.current ||
                        !dragging.current ||
                        dragging.current.groupId === group.id
                      )
                        return;
                      event.preventDefault();
                      event.dataTransfer.dropEffect = "move";
                      setDropTarget(group.id);
                    }}
                    onDragLeave={(event) => {
                      if (
                        event.relatedTarget instanceof Node &&
                        event.currentTarget.contains(event.relatedTarget)
                      )
                        return;
                      setDropTarget((current) =>
                        current === group.id ? "" : current,
                      );
                    }}
                    onDrop={(event) => {
                      event.preventDefault();
                      const source = dragging.current;
                      const transfer = event.dataTransfer.getData(fileDragType);
                      dragging.current = null;
                      setDropTarget("");
                      if (source && transfer === source.memberId) {
                        void move(source.groupId, source.memberId, group.id);
                      }
                    }}
                  >
                    <td className={cell}>
                      <button
                        type="button"
                        aria-expanded={opened}
                        aria-controls={opened ? detailsId : undefined}
                        aria-label={ui.expand(groupName)}
                        className="font-semibold text-left wrap-anywhere"
                        onClick={() => toggle(group.id)}
                      >
                        <svg
                          viewBox="0 0 16 16"
                          className={`mr-1 inline size-4 ${opened ? "rotate-90" : ""}`}
                          fill="none"
                          stroke="currentColor"
                          strokeWidth="1.5"
                          aria-hidden="true"
                        >
                          <path d="m6 4 4 4-4 4" />
                        </svg>
                        {groupName}
                      </button>
                      <p className="text-xs text-[var(--muted)]">
                        {group.manual
                          ? copy.manualGrouping
                          : copy.automaticGrouping}
                      </p>
                    </td>
                    <td className={cell}>
                      {members.size} / {paths.size}
                    </td>
                    <td className={cell}>
                      {members.size - unready.size} / {members.size}
                    </td>
                    <td className={cell}>
                      <a
                        className="text-[var(--accent)] underline"
                        href={incomingGroupMatchingHref(group.id)}
                      >
                        {copy.openGroup(groupName)}
                      </a>
                    </td>
                  </tr>
                  {opened && (
                    <tr id={detailsId}>
                      <td colSpan={4} className="border-b p-2">
                        <table
                          className="w-full table-fixed text-left text-sm"
                          aria-label={ui.expand(groupName)}
                        >
                          <thead>
                            <tr className="bg-[var(--raised)]">
                              <th scope="col" className="w-[8%] p-2">
                                {ui.number}
                              </th>
                              <th scope="col" className="w-[29%] p-2">
                                {ui.title}
                              </th>
                              <th scope="col" className="w-[19%] p-2">
                                {ui.artist}
                              </th>
                              <th scope="col" className="w-[20%] p-2">
                                {ui.album}
                              </th>
                              <th scope="col" className="w-[24%] p-2">
                                {ui.move}
                              </th>
                            </tr>
                          </thead>
                          <tbody>
                            {[...members].map((id) => {
                              const file = files.get(id);
                              const title = tag(file, "TITLE") || ui.noTitle;
                              const path = file?.locations?.[0]?.relative_path;
                              const canMove =
                                !unready.has(id) && groups.length > 1;
                              const selectedTarget = groups.some(
                                (other) =>
                                  other.id !== group.id &&
                                  other.id === moveTarget[id],
                              )
                                ? moveTarget[id]
                                : "";
                              return (
                                <tr
                                  key={id}
                                  data-incoming-member={id}
                                  draggable={canMove}
                                  className={canMove ? "cursor-grab" : ""}
                                  onDragStart={(event) => {
                                    if (!canMove || movingRef.current) {
                                      event.preventDefault();
                                      return;
                                    }
                                    dragging.current = {
                                      groupId: group.id,
                                      memberId: id,
                                    };
                                    event.dataTransfer.setData(
                                      fileDragType,
                                      id,
                                    );
                                    event.dataTransfer.effectAllowed = "move";
                                  }}
                                  onDragEnd={() => {
                                    dragging.current = null;
                                    setDropTarget("");
                                  }}
                                >
                                  <td className={cell}>
                                    {tag(file, "TRACKNUMBER", "TRACK") ||
                                      copy.noValue}
                                  </td>
                                  <td className={cell}>
                                    <strong>{title}</strong>
                                    {path && (
                                      <p className="text-xs text-[var(--muted)]">
                                        {path}
                                      </p>
                                    )}
                                  </td>
                                  <td className={cell}>
                                    {tag(file, "ARTIST", "ALBUMARTIST") ||
                                      copy.noValue}
                                  </td>
                                  <td className={cell}>
                                    {tag(file, "ALBUM") || copy.noValue}
                                  </td>
                                  <td className={cell}>
                                    {canMove && (
                                      <div className="grid gap-1">
                                        <select
                                          className="w-full min-w-0"
                                          aria-label={`${ui.destination}: ${title}`}
                                          value={selectedTarget}
                                          disabled={feedback === "moving"}
                                          onChange={(event) => {
                                            const target = event.target.value;
                                            setMoveTarget((current) => ({
                                              ...current,
                                              [id]: target,
                                            }));
                                          }}
                                        >
                                          <option value="">
                                            {ui.destination}
                                          </option>
                                          {groups
                                            .filter(
                                              (other) => other.id !== group.id,
                                            )
                                            .map((other) => (
                                              <option
                                                key={other.id}
                                                value={other.id}
                                              >
                                                {name(other.id)}
                                              </option>
                                            ))}
                                        </select>
                                        <AppButton
                                          isDisabled={
                                            !selectedTarget ||
                                            feedback === "moving"
                                          }
                                          onPress={() =>
                                            void move(
                                              group.id,
                                              id,
                                              selectedTarget,
                                            )
                                          }
                                        >
                                          {ui.move}
                                        </AppButton>
                                      </div>
                                    )}
                                  </td>
                                </tr>
                              );
                            })}
                          </tbody>
                        </table>
                      </td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
