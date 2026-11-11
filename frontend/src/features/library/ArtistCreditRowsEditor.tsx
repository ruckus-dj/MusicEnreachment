import { useId } from "react";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type ArtistCreditDraft,
  type ArtistCreditProviderFact,
  type ArtistCreditResolutionStatus,
  type ArtistCreditRow,
  type ArtistCreditSearchResult,
  createArtistCreditRow,
} from "./artistCreditDraft";
import {
  type ArtistCreditMessages,
  artistCreditMessages,
} from "./artistCreditMessages";

export type ArtistCreditRowsEditorProps = {
  draft: ArtistCreditDraft;
  /** Original saved-key presence; controls whether undoing to inherited is legal. */
  savedHasDesiredOverride: boolean;
  baseline: readonly ArtistCreditRow[];
  effective: {
    status: ArtistCreditResolutionStatus;
    origin: string | null;
  };
  searchQuery: string;
  searchResults: readonly ArtistCreditSearchResult[];
  searchLoading?: boolean;
  searchError?: boolean;
  editing?: boolean;
  disabled?: boolean;
  onDraftChange: (draft: ArtistCreditDraft) => void;
  onSearchQueryChange: (query: string) => void;
  onRetrySearch?: () => void;
};

function FactList({ facts }: { facts?: readonly ArtistCreditProviderFact[] }) {
  if (!facts?.length) return null;
  return (
    <dl className="m-0 grid min-w-0 gap-x-3 gap-y-1 text-xs text-[var(--muted)] sm:grid-cols-2">
      {facts.map((fact) => (
        <div
          key={JSON.stringify([fact.label, fact.value])}
          className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-2"
        >
          <dt className="font-medium">{fact.label}</dt>
          <dd className="m-0 min-w-0 break-words">{fact.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function CreditIdentity({
  row,
  text,
}: {
  row: ArtistCreditRow;
  text: ArtistCreditMessages;
}) {
  return (
    <div className="grid min-w-0 gap-1">
      <span className="break-words font-medium">{row.name}</span>
      {row.disambiguation ? (
        <span className="break-words text-xs text-[var(--muted)]">
          {row.disambiguation}
        </span>
      ) : null}
      {row.sortName ? (
        <span className="break-words text-xs text-[var(--muted)]">
          {text.sortName}: {row.sortName}
        </span>
      ) : null}
      {row.hasMbid ? (
        <span className="text-xs text-[var(--muted)]">{text.mbidLinked}</span>
      ) : null}
      <FactList facts={row.providerFacts} />
      <span className="sr-only">{text.name}</span>
    </div>
  );
}

function BaselineList({
  rows,
  text,
}: {
  rows: readonly ArtistCreditRow[];
  text: ArtistCreditMessages;
}) {
  return (
    <section className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-3">
      <h3 className="m-0 text-sm font-medium">{text.baseline}</h3>
      {rows.length ? (
        <ol className="m-0 grid min-w-0 list-decimal gap-2 pl-5">
          {rows.map((row) => (
            <li key={row.clientKey} className="min-w-0 break-words text-sm">
              <span>{row.credit_name}</span>
              {row.join_phrase ? <span>{row.join_phrase}</span> : null}
              {row.disambiguation ? (
                <span className="text-xs text-[var(--muted)]">
                  {` (${row.disambiguation})`}
                </span>
              ) : null}
              {row.sortName ? (
                <span className="text-xs text-[var(--muted)]">
                  {` · ${text.sortName}: ${row.sortName}`}
                </span>
              ) : null}
              {row.hasMbid ? (
                <span className="text-xs text-[var(--muted)]">
                  {` · ${text.mbidLinked}`}
                </span>
              ) : null}
              <FactList facts={row.providerFacts} />
            </li>
          ))}
        </ol>
      ) : (
        <p className="m-0 text-sm text-[var(--muted)]">{text.baselineEmpty}</p>
      )}
    </section>
  );
}

function CreditRow({
  row,
  index,
  count,
  disabled,
  text,
  onChange,
  onMove,
  onRemove,
}: {
  row: ArtistCreditRow;
  index: number;
  count: number;
  disabled: boolean;
  text: ArtistCreditMessages;
  onChange: (next: ArtistCreditRow) => void;
  onMove: (direction: -1 | 1) => void;
  onRemove: () => void;
}) {
  const identityId = useId();
  const creditNameId = useId();
  const joinPhraseId = useId();
  return (
    <li className="grid min-w-0 gap-3 rounded border border-[var(--line)] bg-[var(--surface)] p-3">
      <div className="flex min-w-0 flex-wrap items-start justify-between gap-2">
        <div id={identityId} className="min-w-0 flex-1">
          <CreditIdentity row={row} text={text} />
        </div>
        <div className="grid grid-cols-[repeat(3,minmax(0,1fr))] gap-2">
          <AppButton
            type="button"
            isDisabled={disabled || index === 0}
            aria-label={`${text.reorder}: ${text.moveUp} — ${text.creditNumber(index + 1)}`}
            onPress={() => onMove(-1)}
            className="min-w-0"
          >
            {text.moveUp}
          </AppButton>
          <AppButton
            type="button"
            isDisabled={disabled || index === count - 1}
            aria-label={`${text.reorder}: ${text.moveDown} — ${text.creditNumber(index + 1)}`}
            onPress={() => onMove(1)}
            className="min-w-0"
          >
            {text.moveDown}
          </AppButton>
          <AppButton
            type="button"
            isDisabled={disabled}
            aria-label={`${text.remove}: ${text.creditNumber(index + 1)}`}
            onPress={onRemove}
            className="min-w-0"
          >
            {text.remove}
          </AppButton>
        </div>
      </div>
      <label htmlFor={creditNameId} className="grid min-w-0 gap-1 text-xs">
        <span>{text.creditName}</span>
        <input
          id={creditNameId}
          className="min-h-10 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
          value={row.credit_name}
          disabled={disabled}
          aria-describedby={identityId}
          onChange={(event) =>
            onChange({ ...row, credit_name: event.currentTarget.value })
          }
        />
      </label>
      <label htmlFor={joinPhraseId} className="grid min-w-0 gap-1 text-xs">
        <span>{text.joinPhrase}</span>
        <input
          id={joinPhraseId}
          className="min-h-10 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
          value={row.join_phrase}
          disabled={disabled}
          aria-describedby={identityId}
          onChange={(event) =>
            onChange({ ...row, join_phrase: event.currentTarget.value })
          }
        />
      </label>
    </li>
  );
}

function moveRow(rows: ArtistCreditRow[], index: number, direction: -1 | 1) {
  const target = index + direction;
  if (target < 0 || target >= rows.length) return rows;
  const reordered = [...rows];
  [reordered[index], reordered[target]] = [reordered[target], reordered[index]];
  return reordered;
}

export function ArtistCreditRowsEditor({
  draft,
  savedHasDesiredOverride,
  baseline,
  effective,
  searchQuery,
  searchResults,
  searchLoading = false,
  searchError = false,
  editing = true,
  disabled = false,
  onDraftChange,
  onSearchQueryChange,
  onRetrySearch,
}: ArtistCreditRowsEditorProps) {
  const { locale } = useI18n();
  const text = artistCreditMessages[locale];
  const queryId = useId();
  const headingId = useId();

  function setState(state: ArtistCreditDraft["state"] | "inherited") {
    if (state === "inherited") {
      if (savedHasDesiredOverride) return;
      onDraftChange({ hasDesiredOverride: false, state: "absent", rows: [] });
      return;
    }
    onDraftChange({
      hasDesiredOverride: true,
      state,
      rows: state === "value" ? draft.rows : [],
    });
  }

  function updateRows(rows: ArtistCreditRow[]) {
    onDraftChange({ hasDesiredOverride: true, state: "value", rows });
  }

  return (
    <section
      aria-labelledby={headingId}
      className="grid min-w-0 gap-3 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
    >
      <h2 id={headingId} className="m-0 text-sm font-semibold">
        {text.heading}
      </h2>

      <BaselineList rows={baseline} text={text} />

      <section
        className="grid min-w-0 gap-2"
        aria-labelledby={`${queryId}-effective`}
      >
        <h3 id={`${queryId}-effective`} className="m-0 text-sm font-medium">
          {text.effective}
        </h3>
        <p className="m-0 min-w-0 break-words text-sm">
          {text.status[effective.status]}
          {effective.origin
            ? ` · ${text.origin}: ${
                text.origins[effective.origin as keyof typeof text.origins] ??
                effective.origin
              }`
            : ""}
        </p>
      </section>

      <label className="grid min-w-0 gap-1 text-sm">
        <span>{text.override}</span>
        <select
          className="min-h-10 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
          value={draft.hasDesiredOverride ? draft.state : "inherited"}
          disabled={disabled}
          onChange={(event) =>
            setState(
              event.currentTarget.value as
                | ArtistCreditDraft["state"]
                | "inherited",
            )
          }
        >
          {!savedHasDesiredOverride ? (
            <option value="inherited">{text.inherit}</option>
          ) : null}
          <option value="absent">{text.explicitAbsent}</option>
          <option value="value">{text.useOverride}</option>
          <option value="explicit_empty">{text.clear}</option>
        </select>
      </label>

      {!draft.hasDesiredOverride ? (
        <p className="m-0 text-sm text-[var(--muted)]">{text.inheritedNote}</p>
      ) : null}
      {draft.hasDesiredOverride && draft.state === "absent" ? (
        <p className="m-0 text-sm text-[var(--muted)]">{text.absentNote}</p>
      ) : null}
      {draft.state === "explicit_empty" ? (
        <p className="m-0 text-sm text-[var(--muted)]">{text.clearedNote}</p>
      ) : null}

      {draft.state === "value" ? (
        <div className="grid min-w-0 gap-3">
          <h3 className="m-0 text-sm font-medium">{text.selected}</h3>
          {draft.rows.length ? (
            <ol className="m-0 grid min-w-0 list-none gap-2 p-0">
              {draft.rows.map((row, index) => (
                <CreditRow
                  key={row.clientKey}
                  row={row}
                  index={index}
                  count={draft.rows.length}
                  disabled={disabled}
                  text={text}
                  onChange={(updated) =>
                    updateRows(
                      draft.rows.map((current, i) =>
                        i === index ? updated : current,
                      ),
                    )
                  }
                  onMove={(direction) =>
                    updateRows(moveRow(draft.rows, index, direction))
                  }
                  onRemove={() =>
                    updateRows(draft.rows.filter((_, i) => i !== index))
                  }
                />
              ))}
            </ol>
          ) : (
            <p className="m-0 text-sm text-[var(--muted)]">{text.empty}</p>
          )}

          {editing ? (
            <div className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-3">
              <label htmlFor={queryId} className="grid min-w-0 gap-1 text-sm">
                <span>{text.search}</span>
                <input
                  id={queryId}
                  className="min-h-10 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                  type="search"
                  value={searchQuery}
                  disabled={disabled}
                  placeholder={text.searchPlaceholder}
                  onChange={(event) =>
                    onSearchQueryChange(event.currentTarget.value)
                  }
                />
              </label>
              {searchLoading ? (
                <p role="status" className="m-0 text-sm text-[var(--muted)]">
                  {text.loading}
                </p>
              ) : null}
              {searchError ? (
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <p
                    role="alert"
                    className="m-0 min-w-0 text-sm text-[var(--error)]"
                  >
                    {text.searchError}
                  </p>
                  {onRetrySearch ? (
                    <AppButton
                      type="button"
                      isDisabled={disabled}
                      onPress={onRetrySearch}
                    >
                      {text.search}
                    </AppButton>
                  ) : null}
                </div>
              ) : null}
              {searchQuery && !searchLoading && !searchError ? (
                searchResults.length ? (
                  <ul
                    aria-label={text.results}
                    className="m-0 grid min-w-0 gap-2 p-0"
                  >
                    {searchResults.map((artist) => (
                      <li
                        key={artist.id}
                        className="flex min-w-0 flex-wrap items-start justify-between gap-2 rounded border border-[var(--line)] p-2"
                      >
                        <div className="grid min-w-0 flex-1 gap-1 text-sm">
                          <span className="break-words font-medium">
                            {artist.name}
                          </span>
                          {artist.disambiguation ? (
                            <span className="break-words text-xs text-[var(--muted)]">
                              {artist.disambiguation}
                            </span>
                          ) : null}
                          <FactList facts={artist.providerFacts} />
                        </div>
                        <AppButton
                          type="button"
                          isDisabled={disabled}
                          aria-label={`${text.add}: ${artist.name}${artist.disambiguation ? ` (${artist.disambiguation})` : ""}`}
                          onPress={() =>
                            updateRows([
                              ...draft.rows,
                              createArtistCreditRow(artist),
                            ])
                          }
                        >
                          {text.add}
                        </AppButton>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="m-0 text-sm text-[var(--muted)]">
                    {text.noResults}
                  </p>
                )
              ) : !searchQuery ? (
                <p className="m-0 text-sm text-[var(--muted)]">
                  {text.searchPrompt}
                </p>
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
