import { useLayoutEffect, useRef } from "react";

/** A result gains focus only on a provider-requested transition. */
export function useMatchingLinkResultFocus({
  focusHeadingKey,
  publicationOpen,
}: {
  focusHeadingKey?: string;
  publicationOpen: boolean;
}) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const publicationButtonRef = useRef<HTMLButtonElement>(null);
  const restorePublicationFocus = useRef(false);

  useLayoutEffect(() => {
    if (focusHeadingKey && !restorePublicationFocus.current) {
      headingRef.current?.focus();
    }
  }, [focusHeadingKey]);

  useLayoutEffect(() => {
    if (!publicationOpen && restorePublicationFocus.current) {
      restorePublicationFocus.current = false;
      publicationButtonRef.current?.focus();
    }
  }, [publicationOpen]);

  return {
    headingRef,
    publicationButtonRef,
    restorePublicationButtonOnReturn: () => {
      restorePublicationFocus.current = true;
    },
  };
}
