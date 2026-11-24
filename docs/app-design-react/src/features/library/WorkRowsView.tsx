import { useId, useLayoutEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";

export type WorkRowDraft = {
  uiKey: string;
  title: string;
  mbid?: string;
};

type WorkRowsViewProps = {
  locale: "en" | "ru";
  rows: readonly WorkRowDraft[];
  editing: boolean;
  disabled?: boolean;
  onChange: (rows: WorkRowDraft[]) => void;
  onAdd: () => void;
  /** Only providers that support editing the work identity opt in. */
  editableMbid?: boolean;
  titleDataKey?: string;
  mbidDataKey?: string;
};

const copy = {
  en: {
    title: "Work title",
    mbid: "Work MusicBrainz ID",
    empty: "Empty value",
    add: "Add work",
    remove: "Remove",
    removeWork: (position: number) => `Remove work ${position}`,
    moveUp: (position: number) => `Move work up ${position}`,
    moveDown: (position: number) => `Move work down ${position}`,
  },
  ru: {
    title: "Название произведения",
    mbid: "MusicBrainz ID произведения",
    empty: "Пустое значение",
    add: "Добавить произведение",
    remove: "Убрать",
    removeWork: (position: number) => `Удалить произведение ${position}`,
    moveUp: (position: number) => `Переместить произведение вверх ${position}`,
    moveDown: (position: number) => `Переместить произведение вниз ${position}`,
  },
};

const inputClassName =
  "min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]";

const marker = (key?: string) => (key ? { "data-metadata-keys": key } : {});

export function WorkRowsView({
  locale,
  rows,
  editing,
  disabled = false,
  onChange,
  onAdd,
  editableMbid,
  titleDataKey,
  mbidDataKey,
}: WorkRowsViewProps) {
  const controlPrefix = useId();
  const text = copy[locale];
  const pendingMove = useRef<{ key: string; action: "up" | "down" }>(null);
  useLayoutEffect(() => {
    const move = pendingMove.current;
    if (!move || !rows.some((row) => row.uiKey === move.key)) return;
    pendingMove.current = null;
    const prefix = `${controlPrefix}-${move.key}`;
    const button = document.getElementById(`${prefix}-${move.action}`);
    if (button instanceof HTMLButtonElement && !button.disabled) button.focus();
    else document.getElementById(`${prefix}-title`)?.focus();
  }, [controlPrefix, rows]);

  const replaceRow = (index: number, patch: Partial<WorkRowDraft>) => {
    onChange(
      rows.map((row, rowIndex) =>
        rowIndex === index ? { ...row, ...patch } : row,
      ),
    );
  };
  const moveRow = (index: number, delta: number) => {
    const next = [...rows];
    const target = index + delta;
    pendingMove.current = {
      key: rows[index].uiKey,
      action: delta < 0 ? "up" : "down",
    };
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  };
  const removeRow = (index: number) => {
    onChange(rows.filter((_, rowIndex) => rowIndex !== index));
  };

  return (
    <div className="grid min-w-0 gap-2">
      {rows.length ? (
        rows.map((row, index) => (
          <div
            key={row.uiKey}
            className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
          >
            {editing ? (
              <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                <label htmlFor={`${controlPrefix}-${row.uiKey}-title`}>
                  {`${text.title} ${index + 1}`}
                </label>
                <input
                  {...marker(titleDataKey)}
                  id={`${controlPrefix}-${row.uiKey}-title`}
                  className={inputClassName}
                  value={row.title}
                  disabled={disabled}
                  onChange={(event) =>
                    replaceRow(index, { title: event.currentTarget.value })
                  }
                />
              </div>
            ) : (
              <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                <span>{`${text.title} ${index + 1}`}</span>
                <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
                  {row.title || text.empty}
                </span>
              </div>
            )}
            {editing && editableMbid ? (
              <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                <label htmlFor={`${controlPrefix}-${row.uiKey}-mbid`}>
                  {text.mbid}
                </label>
                <input
                  {...marker(mbidDataKey)}
                  id={`${controlPrefix}-${row.uiKey}-mbid`}
                  className={inputClassName}
                  value={row.mbid ?? ""}
                  disabled={disabled}
                  onChange={(event) =>
                    replaceRow(index, { mbid: event.currentTarget.value })
                  }
                />
              </div>
            ) : row.mbid ? (
              <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                <span>{text.mbid}</span>
                <output
                  {...marker(mbidDataKey)}
                  className="min-h-9 break-all py-2 text-sm text-[var(--ink)]"
                >
                  {row.mbid}
                </output>
              </div>
            ) : null}
            {editing ? (
              <div className="flex flex-wrap items-end gap-1">
                <AppButton
                  id={`${controlPrefix}-${row.uiKey}-up`}
                  type="button"
                  aria-label={text.moveUp(index + 1)}
                  isDisabled={disabled || index === 0}
                  onPress={() => moveRow(index, -1)}
                  className="min-w-0 justify-center"
                >
                  ↑
                </AppButton>
                <AppButton
                  id={`${controlPrefix}-${row.uiKey}-down`}
                  type="button"
                  aria-label={text.moveDown(index + 1)}
                  isDisabled={disabled || index === rows.length - 1}
                  onPress={() => moveRow(index, 1)}
                  className="min-w-0 justify-center"
                >
                  ↓
                </AppButton>
                <AppButton
                  type="button"
                  aria-label={text.removeWork(index + 1)}
                  isDisabled={disabled}
                  onPress={() => removeRow(index)}
                  className="min-w-0 justify-center"
                >
                  {text.remove}
                </AppButton>
              </div>
            ) : null}
          </div>
        ))
      ) : (
        <span className="text-sm text-[var(--muted)]">{text.empty}</span>
      )}
      {editing ? (
        <AppButton
          type="button"
          isDisabled={disabled}
          onPress={onAdd}
          className="min-w-0 justify-center justify-self-start"
        >
          {text.add}
        </AppButton>
      ) : null}
    </div>
  );
}
