import { useEffect, useRef, useState } from "react";
import {
  confirmIncomingGroups,
  getIncomingGroups,
  previewIncomingGroups,
} from "../../api/generated/client";
import type {
  IncomingGroupingActionInput,
  IncomingGroupingEditBody,
  IncomingGroupResponse,
  IncomingGroupsBody,
} from "../../api/generated/client.schemas";

export interface IncomingGroupingEditorProps {
  snapshot: IncomingGroupsBody;
  onConfirmed?: (groups: IncomingGroupsBody) => void | Promise<void>;
}

function errorMessage(
  data: { detail?: string },
  fallback: string,
  status: number,
) {
  return data.detail || `${fallback} (HTTP ${status}).`;
}

function groupLabel(group: IncomingGroupResponse, index: number) {
  return `Группа ${index + 1} · ${group.id}`;
}

export function IncomingGroupingEditor({
  snapshot,
  onConfirmed,
}: IncomingGroupingEditorProps) {
  const [actions, setActions] = useState<IncomingGroupingActionInput[]>([]);
  const [groupSelection, setGroupSelection] = useState<string[]>([]);
  const [memberSelection, setMemberSelection] = useState<string[]>([]);
  const [actionRowIDs, setActionRowIDs] = useState<number[]>([]);
  const [moveTarget, setMoveTarget] = useState("");
  const [preview, setPreview] = useState<IncomingGroupsBody>();
  const [effectiveBoard, setEffectiveBoard] = useState(snapshot);
  const [needsPreview, setNeedsPreview] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
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
    setNotice(
      "Снимок изменился. Проверьте черновик и создайте новый предпросмотр.",
    );
    setError("");
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
    setError("");
    setNotice(
      "Действие добавлено. Создайте предпросмотр, прежде чем продолжать.",
    );
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
    setError("");
    setNotice("");
    try {
      const response = await previewIncomingGroups(editBody());
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        if (response.status === 409) setStale(true);
        setPreview(undefined);
        setError(
          errorMessage(
            response.data,
            "Предпросмотр не выполнен",
            response.status,
          ),
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
            ? reason.message
            : "Предпросмотр не выполнен.",
        );
    } finally {
      if (request === sequence.current) setBusy(false);
    }
  }

  async function confirm() {
    if (!preview?.draft_revision || busy || stale || needsPreview) return;
    const request = ++sequence.current;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const response = await confirmIncomingGroups(
        editBody(preview.draft_revision),
      );
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        if (response.status === 409) setStale(true);
        setError(
          errorMessage(
            response.data,
            "Подтверждение не выполнено",
            response.status,
          ),
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
      setNotice("Изменения сохранены.");
      await onConfirmed?.(response.data);
    } catch (reason) {
      if (request === sequence.current)
        setError(
          reason instanceof Error
            ? reason.message
            : "Подтверждение не выполнено.",
        );
    } finally {
      if (request === sequence.current) setBusy(false);
    }
  }

  async function refreshSnapshot() {
    const request = ++sequence.current;
    setBusy(true);
    setError("");
    try {
      const response = await getIncomingGroups();
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        setError(
          errorMessage(
            response.data,
            "Обновление не выполнено",
            response.status,
          ),
        );
        return;
      }
      setCurrentSnapshot(response.data);
      setPreview(undefined);
      setEffectiveBoard(response.data);
      setNeedsPreview(actions.length > 0);
      setStale(false);
      setNotice(
        "Снимок обновлён. Проверьте сохранённый список действий и создайте новый предпросмотр.",
      );
    } catch (reason) {
      if (request === sequence.current)
        setError(
          reason instanceof Error ? reason.message : "Обновление не выполнено.",
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
    setError("");
    setNotice("Черновик отменён.");
  }

  return (
    <section aria-labelledby="incoming-grouping-title" className="space-y-4">
      <header>
        <h2 id="incoming-grouping-title" className="text-xl font-semibold">
          Коррекция входящих групп
        </h2>
        <p>
          Изменения остаются черновиком страницы до отдельного подтверждения.
        </p>
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
              <legend className="font-semibold">
                {groupLabel(group, index)}
              </legend>
              <label className="mr-4 inline-flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={`Выбрать ${groupLabel(group, index)} для объединения`}
                  checked={groupSelection.includes(group.id)}
                  disabled={busy || needsPreview}
                  onChange={() =>
                    toggle(groupSelection, group.id, setGroupSelection)
                  }
                />
                Объединить
              </label>
              <span>
                {members.length} файлов · {paths.size} уникальных путей
              </span>
              <ul className="mt-2 space-y-1">
                {members.map((id) => (
                  <li key={id} className="flex flex-wrap items-center gap-2">
                    <label className="inline-flex items-center gap-2">
                      <input
                        type="checkbox"
                        aria-label={`Выбрать файл ${id}`}
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
                    <span>{unready.has(id) ? "Не готов" : "Готов"}</span>
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
                Отделить выбранные файлы
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
          Объединить выбранные группы
        </button>
        <label className="flex items-center gap-2">
          Переместить выбранные файлы в
          <select
            aria-label="Целевая группа"
            value={moveTarget}
            disabled={busy || needsPreview}
            onChange={(event) => setMoveTarget(event.target.value)}
          >
            <option value="">Выберите группу</option>
            {groups.map((group) => (
              <option key={group.id} value={group.id}>
                {group.id}
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
          Переместить файлы
        </button>
      </div>

      <section aria-label="Черновик действий">
        <h3 className="font-semibold">Действия черновика ({actions.length})</h3>
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
            Отменить черновик
          </button>
          <button
            type="button"
            className="rounded border px-3 py-2"
            disabled={busy || actions.length === 0}
            onClick={requestPreview}
          >
            Предпросмотр
          </button>
          <button
            type="button"
            className="rounded border px-3 py-2"
            disabled={busy || needsPreview || !preview?.draft_revision || stale}
            onClick={confirm}
          >
            Подтвердить изменения
          </button>
          {stale && (
            <button
              type="button"
              className="rounded border px-3 py-2"
              disabled={busy}
              onClick={refreshSnapshot}
            >
              Обновить снимок
            </button>
          )}
        </div>
      </section>

      {error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {preview && !needsPreview && (
        <p role="status">
          Предпросмотр готов; для сохранения требуется отдельное подтверждение.
        </p>
      )}
      {needsPreview && actions.length > 0 && (
        <p role="status">
          Создайте успешный предпросмотр, прежде чем добавлять следующее
          действие.
        </p>
      )}
    </section>
  );
}
