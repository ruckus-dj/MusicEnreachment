import type { ReactNode, Ref } from "react";
import { ui } from "./uiStyles";

export function PageHeader({
  id,
  title,
  subtitle,
  actions,
  headingRef,
}: {
  id?: string;
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  headingRef?: Ref<HTMLHeadingElement>;
}) {
  return (
    <header className={ui.pageHeader}>
      <div className={ui.pageHeading}>
        <h1
          ref={headingRef}
          id={id}
          tabIndex={id ? -1 : undefined}
          className={ui.pageTitle}
        >
          {title}
        </h1>
        {subtitle ? <p className={ui.pageSubtitle}>{subtitle}</p> : null}
      </div>
      {actions ? <div className={ui.pageActions}>{actions}</div> : null}
    </header>
  );
}
