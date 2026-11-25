import type { ReactNode } from "react";
import { catalogueStyles } from "./catalogueStyles";

export function LibrarySelectionScreenFrame({
  children,
  dockSpace = true,
  selectionDock,
  selectionPreview,
}: {
  children: ReactNode;
  dockSpace?: boolean;
  selectionDock?: ReactNode;
  selectionPreview?: ReactNode;
}) {
  return (
    <section className={dockSpace ? catalogueStyles.dockScreen : undefined}>
      {children}
      {selectionDock}
      {selectionPreview}
    </section>
  );
}
