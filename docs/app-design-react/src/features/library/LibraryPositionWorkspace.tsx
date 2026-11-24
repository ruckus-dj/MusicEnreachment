import { type ReactNode, useId } from "react";
import { AppButton } from "../../components/AppButton";
import { firstPublicationPreviewMessages } from "./firstPublicationPreviewMessages";

const title = { en: "Publication", ru: "Публикация" } as const;

export function LibraryPositionPreviewButton({
  locale,
  pending,
  disabled,
  onPreview,
}: {
  locale: "en" | "ru";
  pending?: boolean;
  disabled?: boolean;
  onPreview: () => void;
}) {
  const text = firstPublicationPreviewMessages[locale];
  return (
    <AppButton
      type="button"
      data-position-preview-button
      isDisabled={pending || disabled}
      onPress={onPreview}
      className="min-h-8! rounded-[4px]! pointer-coarse:min-h-10! max-[599px]:min-h-10!"
    >
      {pending ? text.loading : text.preview}
    </AppButton>
  );
}

export function LibraryPositionWorkspace({
  locale,
  editors,
  sources,
  publication,
  actions,
  technicalDetails,
}: {
  locale: "en" | "ru";
  editors?: ReactNode;
  sources?: ReactNode;
  publication?: ReactNode;
  actions?: ReactNode;
  technicalDetails?: ReactNode;
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
          {actions}
          {technicalDetails}
        </section>
      </div>
    </>
  );
}
