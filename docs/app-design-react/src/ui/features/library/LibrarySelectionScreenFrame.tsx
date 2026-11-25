import type { ReactNode } from "react";
import { catalogueStyles } from "./catalogueStyles";

export function LibrarySelectionScreenFrame({
  children,
  selectionDock,
  selectionPreview,
}: {
  children: ReactNode;
  selectionDock?: ReactNode;
  selectionPreview?: ReactNode;
}) {
  return (
    <section className={catalogueStyles.dockScreen}>
      {children}
      {selectionDock}
      {selectionPreview}
    </section>
  );
}
