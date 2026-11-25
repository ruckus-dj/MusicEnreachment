import { Button } from "react-aria-components";
import { muted, panel, primary, rows } from "../local-release/localReleaseUi";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";

type RecordFact = { label: string; value: string };
type RecordTrack = {
  id: string;
  number: string;
  title: string;
  description: string;
};

export type OwnCatalogDetailsViewProps = {
  title: string;
  eyebrow: string;
  backLabel: string;
  editLabel: string;
  onBack: () => void;
  onEdit?: () => void;
  body:
    | { kind: "loading"; label: string }
    | {
        kind: "error";
        message: string;
        retryLabel: string;
        onRetry: () => void;
      }
    | {
        kind: "record";
        facts: readonly RecordFact[];
        tracklist?: {
          title: string;
          positions: string;
          empty: string;
          tracks: readonly RecordTrack[];
        };
      }
    | null;
};

export function OwnCatalogDetailsView({
  title,
  eyebrow,
  backLabel,
  editLabel,
  onBack,
  onEdit,
  body,
}: OwnCatalogDetailsViewProps) {
  return (
    <OwnCatalogEditorShell
      eyebrow={eyebrow}
      title={title}
      backLabel={backLabel}
      onBack={onBack}
      actions={
        onEdit ? (
          <Button className={primary} onPress={onEdit}>
            {editLabel}
          </Button>
        ) : undefined
      }
    >
      {body?.kind === "loading" && (
        <p className={muted} role="status">
          {body.label}
        </p>
      )}
      {body?.kind === "error" && (
        <div className="grid justify-items-start gap-2">
          <p
            className="m-0 text-xs font-medium text-[var(--error)]"
            role="alert"
          >
            {body.message}
          </p>
          <Button className={primary} onPress={body.onRetry}>
            {body.retryLabel}
          </Button>
        </div>
      )}
      {body?.kind === "record" && (
        <section className={`${panel} grid gap-3`}>
          <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-3">
            {body.facts.map((fact) => (
              <div
                key={fact.label}
                className="border-b border-[var(--line)] py-[9px]"
              >
                <dt className="text-[var(--muted)]">{fact.label}</dt>
                <dd className="m-0 wrap-anywhere">{fact.value || "—"}</dd>
              </div>
            ))}
          </dl>
          {body.tracklist && (
            <div>
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] pb-2">
                <h2 className="m-0 text-base font-semibold">
                  {body.tracklist.title}
                </h2>
                <small className={muted}>{body.tracklist.positions}</small>
              </div>
              {body.tracklist.tracks.length ? (
                <ol className={`${rows} mt-2`}>
                  {body.tracklist.tracks.map((track) => (
                    <li
                      key={track.id}
                      className="flex min-w-0 items-baseline gap-2 py-2"
                    >
                      <span className="w-6 shrink-0 text-right text-xs text-[var(--muted)] tabular-nums">
                        {track.number}.
                      </span>
                      <div className="min-w-0">
                        <strong>{track.title}</strong>
                        <p className={muted}>{track.description}</p>
                      </div>
                    </li>
                  ))}
                </ol>
              ) : (
                <p className={muted}>{body.tracklist.empty}</p>
              )}
            </div>
          )}
        </section>
      )}
    </OwnCatalogEditorShell>
  );
}
