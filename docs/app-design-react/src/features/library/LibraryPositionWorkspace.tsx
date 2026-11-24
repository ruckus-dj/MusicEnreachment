import { type ReactNode, useId } from "react";

const title = { en: "Publication", ru: "Публикация" } as const;

export function LibraryPositionWorkspace({
  locale,
  editors,
  sources,
  publication,
}: {
  locale: "en" | "ru";
  editors?: ReactNode;
  sources: ReactNode;
  publication: ReactNode;
}) {
  const publicationTitle = useId();
  return (
    <>
      {editors && <div className="grid min-w-0 gap-3">{editors}</div>}
      <div className="grid grid-cols-2 gap-3 max-[1100px]:grid-cols-1">
        {sources}
        <section
          aria-labelledby={publicationTitle}
          className="mb-[10px] min-w-0 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-[10px] wrap-anywhere [&_h2]:mb-[9px] [&_h2]:text-sm [&_h3]:mb-2 [&_h3]:text-sm [&_h3]:font-semibold [&_p]:my-2"
        >
          <h2 id={publicationTitle}>{title[locale]}</h2>
          {publication}
        </section>
      </div>
    </>
  );
}
