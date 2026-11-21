import type { Ref } from "react";
import { AppButton } from "../../components/AppButton";

type SourceHeadingLink = { href: string; label: string };

export function SourceRootHeadingView({
  title,
  headingId,
  headingRef,
  back,
  incoming,
  path,
}: {
  title: string;
  headingId?: string;
  headingRef?: Ref<HTMLHeadingElement>;
  back: SourceHeadingLink | { label: string; onPress: () => void };
  incoming?: SourceHeadingLink;
  path?: string;
}) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-3 pb-2">
      <div className="grid min-w-0 gap-1">
        <h1
          ref={headingRef}
          id={headingId}
          tabIndex={headingRef ? -1 : undefined}
          className="m-0 text-2xl font-semibold wrap-anywhere"
        >
          {title}
        </h1>
        {path && (
          <p className="m-0 wrap-anywhere text-sm text-[var(--muted)]">
            <code>{path}</code>
          </p>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {"href" in back ? (
          <a
            className="text-[var(--accent)] underline underline-offset-2"
            href={back.href}
          >
            {back.label}
          </a>
        ) : (
          <AppButton onPress={back.onPress}>{back.label}</AppButton>
        )}
        {incoming && (
          <a
            className="text-[var(--accent)] underline underline-offset-2"
            href={incoming.href}
          >
            {incoming.label}
          </a>
        )}
      </div>
    </header>
  );
}
