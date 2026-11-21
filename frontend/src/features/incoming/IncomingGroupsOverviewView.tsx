import { Fragment, useRef, useState } from "react";
import {
  IncomingFileActions,
  type IncomingFileDestination,
} from "./IncomingFileActions";

export type IncomingGroupViewFile = {
  id: string;
  trackNumber?: number | string;
  title: string;
  path?: string;
  artist: string;
  album: string;
  canMove: boolean;
  canCreateGroup: boolean;
};

export type IncomingGroupViewPath = {
  key: string;
  label: string;
};

export type IncomingGroupViewRow = {
  id: string;
  name: string;
  secondary: string;
  status?: string;
  members: number;
  paths: number;
  ready: number;
  matchingHref: string;
  opened: boolean;
  files: readonly IncomingGroupViewFile[];
  pathEvidence?: readonly IncomingGroupViewPath[];
  destinations: readonly IncomingFileDestination[];
};

export type IncomingGroupsOverviewLabels = {
  caption: string;
  group: string;
  filesPaths: string;
  ready: string;
  status: string;
  number: string;
  title: string;
  artist: string;
  album: string;
  actions: string;
  noTitle: string;
  noValue: string;
  empty?: string;
  expand: (name: string) => string;
  openGroup: (name: string) => string;
};

export type IncomingGroupMoveHandler = (
  groupId: string,
  memberId: string,
  targetGroupId: string,
  focus: boolean,
) => void;

export type IncomingGroupsOverviewViewProps = {
  labels: IncomingGroupsOverviewLabels;
  rows: readonly IncomingGroupViewRow[];
  onToggle: (groupId: string) => void;
  onMove: IncomingGroupMoveHandler;
  onSplit: (groupId: string, memberId: string) => void;
  disclosureRef?: (groupId: string, node: HTMLButtonElement | null) => void;
  disabled?: boolean;
  className?: string;
};

const fileDragType = "application/x-melotrove-group-member";
const cell = "border-b p-2 align-top wrap-anywhere";

export function IncomingGroupsOverviewView({
  labels,
  rows,
  onToggle,
  onMove,
  onSplit,
  disclosureRef,
  disabled = false,
  className = "",
}: IncomingGroupsOverviewViewProps) {
  const dragging = useRef<{ groupId: string; memberId: string } | null>(null);
  const [dropTarget, setDropTarget] = useState("");

  return (
    <div className={`min-w-0 overflow-x-auto ${className}`}>
      <table className="w-full min-w-[36rem] table-fixed border-collapse text-left text-sm">
        <caption className="sr-only">{labels.caption}</caption>
        <colgroup>
          <col className="w-[36%]" />
          <col className="w-[28%]" />
          <col className="w-[14%]" />
          <col className="w-[22%]" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col" className={cell}>
              {labels.group}
            </th>
            <th scope="col" className={cell}>
              {labels.filesPaths}
            </th>
            <th scope="col" className={cell}>
              {labels.ready}
            </th>
            <th scope="col" className={cell}>
              {labels.status}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && labels.empty ? (
            <tr>
              <td className="p-6 text-center text-[var(--muted)]" colSpan={4}>
                {labels.empty}
              </td>
            </tr>
          ) : null}
          {rows.map((row) => {
            const detailsId = `incoming-files-${row.id}`;
            return (
              <Fragment key={row.id}>
                <tr
                  className={`cursor-pointer ${dropTarget === row.id || row.opened ? "bg-[var(--accent-soft)]" : "hover:bg-[var(--raised)]"}`}
                  data-incoming-group={row.id}
                  onClick={(event) => {
                    if (
                      !(event.target instanceof Element) ||
                      event.target.closest("button,a")
                    )
                      return;
                    onToggle(row.id);
                  }}
                  onDragOver={(event) => {
                    if (
                      disabled ||
                      !dragging.current ||
                      dragging.current.groupId === row.id
                    )
                      return;
                    event.preventDefault();
                    event.dataTransfer.dropEffect = "move";
                    setDropTarget(row.id);
                  }}
                  onDragLeave={(event) => {
                    if (
                      event.relatedTarget instanceof Node &&
                      event.currentTarget.contains(event.relatedTarget)
                    )
                      return;
                    setDropTarget((current) =>
                      current === row.id ? "" : current,
                    );
                  }}
                  onDrop={(event) => {
                    event.preventDefault();
                    const source = dragging.current;
                    const transfer = event.dataTransfer.getData(fileDragType);
                    dragging.current = null;
                    setDropTarget("");
                    if (source && transfer === source.memberId) {
                      onMove(source.groupId, source.memberId, row.id, false);
                    }
                  }}
                >
                  <td className={cell}>
                    <button
                      type="button"
                      aria-expanded={row.opened}
                      aria-controls={row.opened ? detailsId : undefined}
                      aria-label={labels.expand(row.name)}
                      className="font-semibold text-left wrap-anywhere"
                      ref={(node) => disclosureRef?.(row.id, node)}
                      onClick={() => onToggle(row.id)}
                    >
                      <svg
                        viewBox="0 0 16 16"
                        className={`mr-1 inline size-4 ${row.opened ? "rotate-90" : ""}`}
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.5"
                        aria-hidden="true"
                      >
                        <path d="m6 4 4 4-4 4" />
                      </svg>
                      {row.name}
                    </button>
                    <p className="text-xs text-[var(--muted)]">
                      {row.secondary}
                    </p>
                  </td>
                  <td className={cell}>
                    {row.members} / {row.paths}
                  </td>
                  <td className={cell}>
                    {row.ready} / {row.members}
                  </td>
                  <td className={cell}>
                    <a
                      className="text-[var(--accent)] underline"
                      href={row.matchingHref}
                    >
                      {labels.openGroup(row.name)}
                    </a>
                    {row.status ? (
                      <span className="mt-1 block text-xs text-[var(--muted)]">
                        {row.status}
                      </span>
                    ) : null}
                  </td>
                </tr>
                {row.opened && (
                  <tr id={detailsId}>
                    <td colSpan={4} className="border-b p-2">
                      <table
                        className="w-full table-fixed text-left text-sm"
                        aria-label={labels.expand(row.name)}
                      >
                        <thead>
                          <tr className="bg-[var(--raised)]">
                            <th scope="col" className="w-[8%] p-2">
                              {labels.number}
                            </th>
                            <th scope="col" className="w-[29%] p-2">
                              {labels.title}
                            </th>
                            <th scope="col" className="w-[19%] p-2">
                              {labels.artist}
                            </th>
                            <th scope="col" className="w-[20%] p-2">
                              {labels.album}
                            </th>
                            <th scope="col" className="w-[24%] p-2">
                              {labels.actions}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {row.files.map((file) => (
                            <tr
                              key={file.id}
                              data-incoming-member={file.id}
                              draggable={file.canMove}
                              className={file.canMove ? "cursor-grab" : ""}
                              onDragStart={(event) => {
                                if (!file.canMove || disabled) {
                                  event.preventDefault();
                                  return;
                                }
                                dragging.current = {
                                  groupId: row.id,
                                  memberId: file.id,
                                };
                                event.dataTransfer.setData(
                                  fileDragType,
                                  file.id,
                                );
                                event.dataTransfer.effectAllowed = "move";
                              }}
                              onDragEnd={() => {
                                dragging.current = null;
                                setDropTarget("");
                              }}
                            >
                              <td className={cell}>
                                {file.trackNumber ?? labels.noValue}
                              </td>
                              <td className={cell}>
                                <strong>{file.title}</strong>
                                {file.path && (
                                  <p className="text-xs text-[var(--muted)]">
                                    {file.path}
                                  </p>
                                )}
                              </td>
                              <td className={cell}>{file.artist}</td>
                              <td className={cell}>{file.album}</td>
                              <td className={cell}>
                                {(file.canMove || file.canCreateGroup) && (
                                  <IncomingFileActions
                                    fileId={file.id}
                                    fileTitle={file.title}
                                    destinations={
                                      file.canMove ? row.destinations : []
                                    }
                                    canCreateGroup={file.canCreateGroup}
                                    isDisabled={disabled}
                                    onMove={(fileId, destinationId) =>
                                      onMove(
                                        row.id,
                                        fileId,
                                        destinationId,
                                        true,
                                      )
                                    }
                                    onCreateGroup={(fileId) =>
                                      onSplit(row.id, fileId)
                                    }
                                  />
                                )}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                      {row.pathEvidence?.length ? (
                        <ul className="m-0 list-none p-2 text-xs text-[var(--muted)]">
                          {row.pathEvidence.map((path) => (
                            <li className="wrap-anywhere" key={path.key}>
                              {path.label}
                            </li>
                          ))}
                        </ul>
                      ) : null}
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
