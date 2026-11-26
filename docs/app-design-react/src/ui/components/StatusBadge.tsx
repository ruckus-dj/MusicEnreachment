import type { ReactNode } from "react";
import { type UiBadgeTone, ui } from "./uiStyles";

export function StatusBadge({
  children,
  tone = "neutral",
  className = "",
}: {
  children: ReactNode;
  tone?: UiBadgeTone;
  className?: string;
}) {
  return (
    <span
      className={`${ui.badge} ${ui.badgeTone[tone]}${className ? ` ${className}` : ""}`}
    >
      {children}
    </span>
  );
}
