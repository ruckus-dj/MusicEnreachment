export const libraryPositionTitleId = "library-position-title";
export const libraryPositionPageClassName =
  "min-w-0 p-[clamp(14px,2vw,26px)] max-[600px]:px-[10px] max-[600px]:py-[13px]";

const copy = {
  en: {
    position: "position in this edition",
    release: "Artist and release",
    library: "Library",
  },
  ru: {
    position: "позиция конкретного издания",
    release: "К артисту и изданию",
    library: "К медиатеке",
  },
} as const;

export function LibraryPositionHeadingView({
  locale,
  displayNumber,
  title,
  artist,
  releaseTitle,
  releaseHref,
  libraryHref,
}: {
  locale: "en" | "ru";
  displayNumber: string;
  title: string;
  artist?: string;
  releaseTitle: string;
  releaseHref?: string;
  libraryHref: string;
}) {
  const text = copy[locale];
  return (
    <header className="mb-[10px] flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] pb-[14px]">
      <div className="min-w-0 wrap-anywhere">
        <h1
          id={libraryPositionTitleId}
          className="my-[2px] text-[20px] leading-[1.3] tracking-[-.02em]"
        >
          {displayNumber} · {title}
        </h1>
        <p className="my-1 max-w-[850px] text-[var(--muted)]">
          {artist && `${artist} / `}
          {releaseTitle} · {text.position}
        </p>
      </div>
      <a
        className="justify-self-end text-[var(--accent)] hover:underline"
        href={releaseHref ?? libraryHref}
      >
        {releaseHref ? text.release : text.library}
      </a>
    </header>
  );
}
