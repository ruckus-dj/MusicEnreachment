import { type ReactNode, type Ref, useId } from "react";
import { Button } from "react-aria-components";

type PreviewAction = { label: string; onPress: () => void; disabled?: boolean };

export type MatchingPreviewRow = {
  key: string;
  source: string;
  position?: string;
};

type MatchingPreviewProps = {
  locale: "en" | "ru";
  heading: string;
  headingRef?: Ref<HTMLHeadingElement>;
  releaseTitle?: string;
  summary: string;
  rows: readonly MatchingPreviewRow[];
  notice?: string;
  primary: PreviewAction;
  secondary?: PreviewAction;
  children?: ReactNode;
};

const actionClass =
  "inline-flex min-h-9 cursor-pointer items-center justify-center rounded-[var(--control-radius,4px)] border border-[var(--control)] px-3 py-1 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:min-h-10";

const labels = {
  en: {
    source: "File",
    outcome: "After confirmation",
    omitted: "Remains in Incoming",
  },
  ru: {
    source: "Файл",
    outcome: "После подтверждения",
    omitted: "Останется во входящих",
  },
};

export function MatchingPreview({
  locale,
  heading,
  headingRef,
  releaseTitle,
  summary,
  rows,
  notice,
  primary,
  secondary,
  children,
}: MatchingPreviewProps) {
  const headingId = useId();
  const copy = labels[locale];

  return (
    <section
      data-matching-preview
      aria-labelledby={headingId}
      className="space-y-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-4"
    >
      <header>
        <h2
          id={headingId}
          ref={headingRef}
          tabIndex={-1}
          className="scroll-mt-14 text-sm font-semibold focus:outline-none"
        >
          {heading}
        </h2>
        {releaseTitle ? (
          <p className="mt-1 wrap-anywhere font-medium">{releaseTitle}</p>
        ) : null}
        <p className="mt-1 text-sm text-[var(--muted)]">{summary}</p>
      </header>
      {notice ? (
        <p
          role="alert"
          className="rounded border border-[var(--warning)] p-2 text-sm text-[var(--warning)]"
        >
          {notice}
        </p>
      ) : null}
      {rows.length ? (
        <table className="w-full table-fixed border-collapse text-left text-sm">
          <thead className="bg-[var(--raised)] text-[var(--muted)]">
            <tr>
              <th
                scope="col"
                className="w-1/2 border-b border-[var(--line)] px-2 py-1.5"
              >
                {copy.source}
              </th>
              <th
                scope="col"
                className="border-b border-[var(--line)] px-2 py-1.5"
              >
                {copy.outcome}
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ key, source, position }) => (
              <tr key={key}>
                <th
                  scope="row"
                  className="wrap-anywhere border-b border-[var(--line)] px-2 py-1.5 align-top font-normal"
                >
                  {source}
                </th>
                <td className="wrap-anywhere border-b border-[var(--line)] px-2 py-1.5 align-top">
                  {position ?? copy.omitted}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {children}
      <div className="flex flex-wrap justify-end gap-2">
        {secondary ? (
          <Button
            className={`${actionClass} bg-[var(--surface)] text-[var(--ink)] enabled:hover:bg-[var(--raised)]`}
            onPress={secondary.onPress}
            isDisabled={secondary.disabled}
          >
            {secondary.label}
          </Button>
        ) : null}
        <Button
          className={`${actionClass} bg-[var(--button)] text-[var(--button-text)] enabled:hover:brightness-90`}
          onPress={primary.onPress}
          isDisabled={primary.disabled}
        >
          {primary.label}
        </Button>
      </div>
    </section>
  );
}
