import type { ReactNode } from "react";

export function ScreenStatus({ children }: { children: ReactNode }) {
  return <p role="status">{children}</p>;
}
