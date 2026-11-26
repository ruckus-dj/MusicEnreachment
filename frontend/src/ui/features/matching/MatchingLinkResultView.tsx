import { type Ref, useId } from "react";
import { Button } from "react-aria-components";

export type MatchingLinkResultLocale = "en" | "ru";

export type MatchingLinkResultLink = {
  key: string;
  source: string;
  positionId: string;
  position: string;
};

export type MatchingLinkResultFile = {
  key: string;
  name: string;
};

export type MatchingLinkResultViewProps = {
  locale: MatchingLinkResultLocale;
  releaseTitle: string;
  links: readonly MatchingLinkResultLink[];
  remaining: readonly MatchingLinkResultFile[];
  onOpenPublication: () => void;
  publicationDisabled?: boolean;
  onContinue?: () => void;
  headingRef?: Ref<HTMLHeadingElement>;
  publicationButtonRef?: Ref<HTMLButtonElement>;
};

const copy = {
  en: {
    title: "Matching links saved",
    summary: "Links saved: {links} · Positions: {positions}",
    source: "File",
    position: "Position",
    remainingTitle: "Remains in Incoming",
    allMatched: "All files from this group are now matched.",
    publication: "Review publication",
    publicationHint: "No saved link is available for publication.",
    continueMatching: "Continue matching",
    filesUnchanged:
      "Source files and current publications were not changed by saving links.",
  },
  ru: {
    title: "Связи сопоставления сохранены",
    summary: "Сохранено связей: {links} · Позиций: {positions}",
    source: "Файл",
    position: "Позиция",
    remainingTitle: "Остались во входящих",
    allMatched: "Все файлы этой группы сопоставлены.",
    publication: "Проверить публикацию",
    publicationHint: "Нет сохранённых связей, доступных для публикации.",
    continueMatching: "Продолжить сопоставление",
    filesUnchanged:
      "Исходные файлы и действующие публикации при сохранении связей не изменены.",
  },
} as const;

const actionClass =
  "inline-flex min-h-9 cursor-pointer items-center justify-center rounded-[var(--control-radius,4px)] border border-[var(--control)] px-3 py-1 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] disabled:cursor-not-allowed disabled:opacity-60 pointer-coarse:min-h-10";

export function MatchingLinkResultView({
  locale,
  releaseTitle,
  links,
  remaining,
  onOpenPublication,
  publicationDisabled = false,
  onContinue,
  headingRef,
  publicationButtonRef,
}: MatchingLinkResultViewProps) {
  const text = copy[locale];
  const headingId = useId();
  const positions = new Set(links.map((link) => link.positionId)).size;
  return (
    <section
      data-matching-link-result
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
          {text.title}
        </h2>
        <p className="mt-1 wrap-anywhere font-medium">{releaseTitle}</p>
        <p className="mt-1 text-sm text-[var(--muted)]">
          {text.summary
            .replace("{links}", String(links.length))
            .replace("{positions}", String(positions))}
        </p>
      </header>
      {links.length ? (
        <table className="w-full table-fixed border-collapse text-left text-sm">
          <thead className="bg-[var(--raised)] text-[var(--muted)]">
            <tr>
              <th
                scope="col"
                className="w-1/2 border-b border-[var(--line)] px-2 py-1.5"
              >
                {text.source}
              </th>
              <th
                scope="col"
                className="border-b border-[var(--line)] px-2 py-1.5"
              >
                {text.position}
              </th>
            </tr>
          </thead>
          <tbody>
            {links.map(({ key, source, position }) => (
              <tr key={key}>
                <th
                  scope="row"
                  className="wrap-anywhere border-b border-[var(--line)] px-2 py-1.5 align-top font-normal"
                >
                  {source}
                </th>
                <td className="wrap-anywhere border-b border-[var(--line)] px-2 py-1.5 align-top">
                  {position}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      <section aria-label={text.remainingTitle}>
        <h3 className="text-xs font-semibold text-[var(--muted)]">
          {text.remainingTitle}
        </h3>
        {remaining.length ? (
          <ul className="mt-1 list-disc pl-5 text-sm">
            {remaining.map((file) => (
              <li key={file.key} className="wrap-anywhere">
                {file.name}
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-1 text-sm text-[var(--muted)]">{text.allMatched}</p>
        )}
      </section>
      <p className="text-xs text-[var(--muted)]">{text.filesUnchanged}</p>
      {publicationDisabled ? (
        <p className="text-xs text-[var(--muted)]">{text.publicationHint}</p>
      ) : null}
      <div className="flex flex-wrap justify-end gap-2">
        {onContinue ? (
          <Button
            className={`${actionClass} bg-[var(--surface)] text-[var(--ink)] enabled:hover:bg-[var(--raised)]`}
            onPress={onContinue}
          >
            {text.continueMatching}
          </Button>
        ) : null}
        <Button
          ref={publicationButtonRef}
          className={`${actionClass} bg-[var(--button)] text-[var(--button-text)] enabled:hover:brightness-90`}
          isDisabled={publicationDisabled}
          onPress={onOpenPublication}
        >
          {text.publication}
        </Button>
      </div>
    </section>
  );
}
