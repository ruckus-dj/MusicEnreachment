import { useEffect, useRef, useState } from "react";
import {
  confirmIncomingGroups,
  getIncomingGroups,
  previewIncomingGroups,
} from "../../api/generated/client";
import type {
  IncomingGroupingActionInput,
  IncomingGroupingEditBody,
  IncomingGroupsBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type IncomingGroupingMessage,
  incomingGroupingFileCount,
  incomingGroupingMessage,
  incomingGroupingMessages,
  incomingGroupingNumber,
  incomingGroupingPathCount,
} from "./incomingGroupingMessages";

export interface IncomingGroupingEditorProps {
  snapshot: IncomingGroupsBody;
  onConfirmed?: (groups: IncomingGroupsBody) => void | Promise<void>;
}

function responseMessage(
  data: { detail?: string },
  action: "previewError" | "confirmError" | "refreshError",
  status: number,
): IncomingGroupingMessage {
  if (data.detail) return { key: "raw", message: data.detail };
  return { key: "httpError", action, status };
}

export function IncomingGroupingEditor({
  snapshot,
  onConfirmed,
}: IncomingGroupingEditorProps) {
  const { locale } = useI18n();
  const copy = incomingGroupingMessages[locale];
  const [actions, setActions] = useState<IncomingGroupingActionInput[]>([]);
  const [groupSelection, setGroupSelection] = useState<string[]>([]);
  const [memberSelection, setMemberSelection] = useState<string[]>([]);
  const [actionRowIDs, setActionRowIDs] = useState<number[]>([]);
  const [moveTarget, setMoveTarget] = useState("");
  const [preview, setPreview] = useState<IncomingGroupsBody>();
  const [effectiveBoard, setEffectiveBoard] = useState(snapshot);
  const [needsPreview, setNeedsPreview] = useState(false);
  const [error, setError] = useState<IncomingGroupingMessage | null>(null);
  const [notice, setNotice] = useState<IncomingGroupingMessage | null>(null);
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState(false);
  const [currentSnapshot, setCurrentSnapshot] = useState(snapshot);
  const sequence = useRef(0);
  const nextActionRowID = useRef(0);
  const previousPropSnapshot = useRef(snapshot);

  // A new server snapshot invalidates any preview but deliberately leaves the
  // page-local action list visible for review/revision.
  useEffect(() => {
    if (previousPropSnapshot.current === snapshot) return;
    previousPropSnapshot.current = snapshot;
    setCurrentSnapshot(snapshot);
    setPreview(undefined);
    setEffectiveBoard(snapshot);
    setNeedsPreview(actions.length > 0);
    setStale(false);
    setGroupSelection([]);
    setMemberSelection([]);
    setMoveTarget("");
    setNotice({ key: "snapshotChanged" });
    setError(null);
    setBusy(false);
    sequence.current += 1;
  }, [actions.length, snapshot]);

  const groups = effectiveBoard.groups ?? [];
  const fileById = new Map(
    (effectiveBoard.files ?? []).map((file) => [file.variant_id, file]),
  );

  function addAction(action: IncomingGroupingActionInput) {
    setActions((existing) => [...existing, action]);
    setActionRowIDs((existing) => [...existing, nextActionRowID.current++]);
    setNeedsPreview(true);
    setStale(false);
    setError(null);
    setNotice({ key: "actionAdded" });
    sequence.current += 1;
  }

  function toggle(
    values: string[],
    id: string,
    update: (next: string[]) => void,
  ) {
    update(
      values.includes(id)
        ? values.filter((value) => value !== id)
        : [...values, id],
    );
  }

  function editBody(draftRevision?: string): IncomingGroupingEditBody {
    return {
      epoch: currentSnapshot.epoch,
      base_revision: currentSnapshot.revision,
      actions,
      ...(draftRevision ? { draft_revision: draftRevision } : {}),
    };
  }

  async function requestPreview() {
    const request = ++sequence.current;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const response = await previewIncomingGroups(editBody());
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        if (response.status === 409) setStale(true);
        setPreview(undefined);
        setError(
          responseMessage(response.data, "previewError", response.status),
        );
        return;
      }
      setPreview(response.data);
      setEffectiveBoard(response.data);
      setNeedsPreview(false);
      setStale(false);
    } catch (reason) {
      if (request === sequence.current)
        setError(
          reason instanceof Error
            ? { key: "raw", message: reason.message }
            : { key: "previewError" },
        );
    } finally {
      if (request === sequence.current) setBusy(false);
    }
  }

  async function confirm() {
    if (!preview?.draft_revision || busy || stale || needsPreview) return;
    const request = ++sequence.current;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const response = await confirmIncomingGroups(
        editBody(preview.draft_revision),
      );
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        if (response.status === 409) setStale(true);
        setError(
          responseMessage(response.data, "confirmError", response.status),
        );
        return;
      }
      setCurrentSnapshot(response.data);
      setPreview(undefined);
      setEffectiveBoard(response.data);
      setNeedsPreview(false);
      setActions([]);
      setActionRowIDs([]);
      setStale(false);
      setNotice({ key: "saved" });
      await onConfirmed?.(response.data);
    } catch (reason) {
      if (request === sequence.current)
        setError(
          reason instanceof Error
            ? { key: "raw", message: reason.message }
            : { key: "confirmError" },
        );
    } finally {
      if (request === sequence.current) setBusy(false);
    }
  }

  async function refreshSnapshot() {
    const request = ++sequence.current;
    setBusy(true);
    setError(null);
    try {
      const response = await getIncomingGroups();
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        setError(
          responseMessage(response.data, "refreshError", response.status),
        );
        return;
      }
      setCurrentSnapshot(response.data);
      setPreview(undefined);
      setEffectiveBoard(response.data);
      setNeedsPreview(actions.length > 0);
      setStale(false);
      setNotice({ key: "snapshotRefreshed" });
    } catch (reason) {
      if (request === sequence.current)
        setError(
          reason instanceof Error
            ? { key: "raw", message: reason.message }
            : { key: "refreshError" },
        );
    } finally {
      if (request === sequence.current) setBusy(false);
    }
  }

  function cancelDraft() {
    sequence.current += 1;
    setActions([]);
    setActionRowIDs([]);
    setPreview(undefined);
    setEffectiveBoard(currentSnapshot);
    setNeedsPreview(false);
    setGroupSelection([]);
    setMemberSelection([]);
    setStale(false);
    setError(null);
    setNotice({ key: "draftCancelled" });
  }

  return (
    <section aria-labelledby="incoming-grouping-title" className="space-y-4">
      <header>
        <h2 id="incoming-grouping-title" className="text-xl font-semibold">
          {copy.heading}
        </h2>
        <p>{copy.intro}</p>
      </header>

      <div className="space-y-3">
        {groups.map((group, index) => {
          const members = group.members ?? [];
          const paths = new Set(
            members.flatMap(
              (id) =>
                fileById
                  .get(id)
                  ?.locations?.map(
                    (location) =>
                      `${location.root_id}:${location.relative_path}`,
                  ) ?? [],
            ),
          );
          const unready = new Set(group.unready_members ?? []);
          return (
            <fieldset
              key={group.id}
              className="rounded border border-stone-300 p-3"
            >
              <legend className="font-semibold">{copy.group(index)}</legend>
              <label className="mr-4 inline-flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={copy.selectGroup(copy.group(index))}
                  checked={groupSelection.includes(group.id)}
                  disabled={busy || needsPreview}
                  onChange={() =>
                    toggle(groupSelection, group.id, setGroupSelection)
                  }
                />
                {copy.merge}
              </label>
              <span>
                {incomingGroupingFileCount(members.length, locale)} ·{" "}
                {incomingGroupingPathCount(paths.size, locale)}
              </span>
              <ul className="mt-2 space-y-1">
                {members.map((id) => (
                  <li key={id} className="flex flex-wrap items-center gap-2">
                    <label className="inline-flex items-center gap-2">
                      <input
                        type="checkbox"
                        aria-label={copy.selectFile(id)}
                        checked={memberSelection.includes(id)}
                        disabled={busy || needsPreview}
                        onChange={() =>
                          toggle(memberSelection, id, setMemberSelection)
                        }
                      />
                      {fileById
                        .get(id)
                        ?.locations?.map((location) => location.relative_path)
                        .join(" · ") || id}
                    </label>
                    <span>{unready.has(id) ? copy.notReady : copy.ready}</span>
                  </li>
                ))}
              </ul>
              <button
                type="button"
                className="mt-2 rounded border px-2 py-1"
                disabled={
                  busy ||
                  needsPreview ||
                  members.length < 2 ||
                  memberSelection.length === 0 ||
                  memberSelection.length >= members.length ||
                  memberSelection.some((id) => !members.includes(id))
                }
                onClick={() => {
                  addAction({
                    kind: "split",
                    group_id: group.id,
                    member_ids: memberSelection,
                  });
                  setMemberSelection([]);
                }}
              >
                {copy.split}
              </button>
            </fieldset>
          );
        })}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="rounded border px-3 py-2"
          disabled={busy || needsPreview || groupSelection.length < 2}
          onClick={() => {
            addAction({ kind: "merge", group_ids: groupSelection });
            setGroupSelection([]);
          }}
        >
          {copy.mergeSelected}
        </button>
        <label className="flex items-center gap-2">
          {copy.moveLabel}
          <select
            aria-label={copy.targetGroup}
            value={moveTarget}
            disabled={busy || needsPreview}
            onChange={(event) => setMoveTarget(event.target.value)}
          >
            <option value="">{copy.chooseGroup}</option>
            {groups.map((group, index) => (
              <option key={group.id} value={group.id}>
                {copy.group(index)}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          className="rounded border px-3 py-2"
          disabled={
            busy || needsPreview || memberSelection.length === 0 || !moveTarget
          }
          onClick={() => {
            addAction({
              kind: "move",
              member_ids: memberSelection,
              target_group_id: moveTarget,
            });
            setMemberSelection([]);
          }}
        >
          {copy.move}
        </button>
      </div>

      <section aria-label={copy.draftActionsLabel}>
        <h3 className="font-semibold">
          {copy.draftActions(incomingGroupingNumber(actions.length, locale))}
        </h3>
        <ol className="list-inside list-decimal">
          {actions.map((action, index) => (
            <li key={actionRowIDs[index]}>
              {action.kind}:{" "}
              {action.group_id ??
                action.group_ids?.join(", ") ??
                action.target_group_id}
            </li>
          ))}
        </ol>
        <div className="mt-2 flex flex-wrap gap-2">
          <button
            type="button"
            className="rounded border px-3 py-2"
            disabled={busy}
            onClick={cancelDraft}
          >
            {copy.cancelDraft}
          </button>
          <button
            type="button"
            className="rounded border px-3 py-2"
            disabled={busy || actions.length === 0}
            onClick={requestPreview}
          >
            {copy.preview}
          </button>
          <button
            type="button"
            className="rounded border px-3 py-2"
            disabled={busy || needsPreview || !preview?.draft_revision || stale}
            onClick={confirm}
          >
            {copy.confirm}
          </button>
          {stale && (
            <button
              type="button"
              className="rounded border px-3 py-2"
              disabled={busy}
              onClick={refreshSnapshot}
            >
              {copy.refresh}
            </button>
          )}
        </div>
      </section>

      {error && <p role="alert">{incomingGroupingMessage(locale, error)}</p>}
      {notice && <p role="status">{incomingGroupingMessage(locale, notice)}</p>}
      {preview && !needsPreview && (
        <p role="status">
          {incomingGroupingMessage(locale, { key: "previewReady" })}
        </p>
      )}
      {needsPreview && actions.length > 0 && (
        <p role="status">
          {incomingGroupingMessage(locale, { key: "previewRequired" })}
        </p>
      )}
    </section>
  );
}
