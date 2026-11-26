import type { Ref } from "react";

const messages = {
  en: {
    back: "Back to sources",
    incoming: "Review groups",
  },
  ru: {
    back: "К списку источников",
    incoming: "Разобрать группы",
  },
} as const;

const linkClass =
  "text-sm font-medium text-[var(--accent-strong)] underline-offset-2 hover:underline";

export function SourceRootHeadingView({
  locale,
  title,
  headingId,
  headingRef,
  sourcesHref,
  incomingHref,
  path,
}: {
  locale: "en" | "ru";
  title: string;
  headingId?: string;
  headingRef?: Ref<HTMLHeadingElement>;
  sourcesHref: string;
  incomingHref?: string;
  path?: string;
}) {
  const copy = messages[locale];
  return (
    <header className="flex flex-wrap items-end justify-between gap-3 pb-2">
      <div className="grid min-w-0 gap-1">
        <h1
          ref={headingRef}
          id={headingId}
          tabIndex={headingRef ? -1 : undefined}
          className="m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em] wrap-anywhere"
        >
          {title}
        </h1>
        {path && (
          <p className="m-0 wrap-anywhere font-mono text-xs text-[var(--muted)]">
            <code>{path}</code>
          </p>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <a className={linkClass} href={sourcesHref}>
          {copy.back}
        </a>
        {incomingHref && (
          <a className={linkClass} href={incomingHref}>
            {copy.incoming}
          </a>
        )}
      </div>
    </header>
  );
}
