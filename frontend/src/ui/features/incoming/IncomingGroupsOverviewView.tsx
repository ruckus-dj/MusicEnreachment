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
  openGroupAction: string;
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
const headCell =
  "border-b border-[var(--line)] bg-[var(--raised)] px-4! py-2.5! text-left align-middle! text-xs font-semibold text-[var(--muted)]";
const cell =
  "border-b border-[var(--line)] px-4! py-3! align-middle! wrap-anywhere";
const innerHeadCell =
  "border-b border-[var(--line)] bg-[var(--raised)] px-3! py-2! text-left align-middle! text-xs font-semibold text-[var(--muted)]";
const innerCell =
  "border-b border-[var(--line)] px-3! py-2.5! align-top! wrap-anywhere";

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
    <div
      className={`min-w-0 overflow-x-auto overscroll-x-contain ${className}`}
    >
      <table
        className={`w-full table-fixed border-collapse text-left text-sm! ${rows.length ? "min-w-[36rem]" : ""}`}
      >
        <caption className="sr-only">{labels.caption}</caption>
        <colgroup>
          <col className="w-[35%]" />
          <col className="w-[22%]" />
          <col className="w-[16%]" />
          <col className="w-[27%]" />
        </colgroup>
        <thead>
          <tr>
            <th scope="col" className={headCell}>
              {labels.group}
            </th>
            <th scope="col" className={headCell}>
              {labels.filesPaths}
            </th>
            <th scope="col" className={headCell}>
              {labels.ready}
            </th>
            <th scope="col" className={headCell}>
              {labels.status}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 && labels.empty ? (
            <tr>
              <td
                className="px-4! py-12! text-center align-middle! text-sm text-[var(--muted)]"
                colSpan={4}
              >
                {labels.empty}
              </td>
            </tr>
          ) : null}
          {rows.map((row) => {
            const detailsId = `incoming-files-${row.id}`;
            return (
              <Fragment key={row.id}>
                <tr
                  className={`cursor-pointer transition-colors motion-safe:duration-100 ${
                    dropTarget === row.id
                      ? "bg-[var(--accent-soft)] outline-2 -outline-offset-2 outline-[var(--accent)]"
                      : row.opened
                        ? "bg-[var(--accent-soft)]"
                        : "hover:bg-[var(--raised)]"
                  }`}
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
                  <td className="border-b border-[var(--line)] p-0! align-middle!">
                    <button
                      type="button"
                      aria-expanded={row.opened}
                      aria-controls={row.opened ? detailsId : undefined}
                      aria-label={labels.expand(row.name)}
                      aria-describedby={`incoming-artist-${row.id}`}
                      className="flex min-h-14 w-full min-w-0 cursor-pointer items-center gap-3 rounded-none border-0! bg-transparent! px-4 py-3 text-left text-[var(--ink)] outline-none hover:bg-transparent! focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-[var(--focus)]"
                      ref={(node) => disclosureRef?.(row.id, node)}
                      onClick={() => onToggle(row.id)}
                    >
                      <span className="grid size-7 flex-none place-items-center rounded-md border border-[var(--line)] bg-[var(--surface)] text-[var(--muted)]">
                        <svg
                          viewBox="0 0 16 16"
                          className={`size-4 transition-transform motion-safe:duration-150 ${row.opened ? "rotate-90" : ""}`}
                          fill="none"
                          stroke="currentColor"
                          strokeWidth="1.5"
                          aria-hidden="true"
                          focusable="false"
                        >
                          <path d="m6 4 4 4-4 4" />
                        </svg>
                      </span>
                      <span className="min-w-0">
                        <span className="block text-sm font-semibold wrap-anywhere">
                          {row.name}
                        </span>
                        <span
                          id={`incoming-artist-${row.id}`}
                          className="mt-0.5 block text-[13px] leading-5 text-[var(--muted)] wrap-anywhere"
                        >
                          {row.secondary}
                        </span>
                      </span>
                    </button>
                  </td>
                  <td className={`${cell} tabular-nums`}>
                    {row.members} / {row.paths}
                  </td>
                  <td className={`${cell} tabular-nums`}>
                    {row.ready} / {row.members}
                  </td>
                  <td className={cell}>
                    <a
                      className="inline-flex min-h-9 items-center justify-center gap-1.5 rounded-[var(--control-radius)] bg-[var(--button)] px-3 py-1 text-center text-sm font-medium text-[var(--button-text)] no-underline hover:bg-[var(--button-hover)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 pointer-coarse:min-h-10"
                      href={row.matchingHref}
                      aria-label={labels.openGroup(row.name)}
                    >
                      {labels.openGroupAction}
                      <svg
                        viewBox="0 0 12 12"
                        className="size-3 flex-none"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="1.5"
                        aria-hidden="true"
                        focusable="false"
                      >
                        <path
                          d="M2.5 6h7M6.5 3 9.5 6l-3 3"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    </a>
                    {row.status ? (
                      <p className="mt-1 mb-0 text-[13px] leading-5 text-[var(--muted)] wrap-anywhere">
                        {row.status}
                      </p>
                    ) : null}
                  </td>
                </tr>
                {row.opened && (
                  <tr id={detailsId} className="bg-[var(--raised)]">
                    <td className="p-0!" colSpan={4}>
                      <div className="px-4 py-3">
                        <div className="overflow-x-auto overscroll-x-contain rounded-md border border-[var(--line)] bg-[var(--surface)]">
                          <table
                            className="w-full table-fixed text-left text-sm!"
                            aria-label={labels.expand(row.name)}
                          >
                            <thead>
                              <tr>
                                <th
                                  scope="col"
                                  className={`${innerHeadCell} w-[8%]`}
                                >
                                  {labels.number}
                                </th>
                                <th
                                  scope="col"
                                  className={`${innerHeadCell} w-[29%]`}
                                >
                                  {labels.title}
                                </th>
                                <th
                                  scope="col"
                                  className={`${innerHeadCell} w-[19%]`}
                                >
                                  {labels.artist}
                                </th>
                                <th
                                  scope="col"
                                  className={`${innerHeadCell} w-[20%]`}
                                >
                                  {labels.album}
                                </th>
                                <th
                                  scope="col"
                                  className={`${innerHeadCell} w-[24%]`}
                                >
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
                                  className={`transition-colors motion-safe:duration-100 hover:bg-[var(--raised)] ${
                                    file.canMove
                                      ? "cursor-grab active:cursor-grabbing"
                                      : ""
                                  }`}
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
                                  <td
                                    className={`${innerCell} tabular-nums text-[var(--muted)]`}
                                  >
                                    {file.trackNumber ?? labels.noValue}
                                  </td>
                                  <td className={innerCell}>
                                    <span className="block text-sm font-medium text-[var(--ink)]">
                                      {file.title}
                                    </span>
                                    {file.path && (
                                      <p className="mt-0.5 mb-0 text-[13px] leading-5 text-[var(--muted)] wrap-anywhere">
                                        {file.path}
                                      </p>
                                    )}
                                  </td>
                                  <td className={innerCell}>{file.artist}</td>
                                  <td className={innerCell}>{file.album}</td>
                                  <td className={innerCell}>
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
                        </div>
                        {row.pathEvidence?.length ? (
                          <ul className="m-0 mt-3 flex list-none flex-wrap gap-1.5 p-0">
                            {row.pathEvidence.map((path) => (
                              <li
                                className="wrap-anywhere rounded-md border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-[13px] leading-5 text-[var(--muted)]"
                                key={path.key}
                              >
                                {path.label}
                              </li>
                            ))}
                          </ul>
                        ) : null}
                      </div>
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
