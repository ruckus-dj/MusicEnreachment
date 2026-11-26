import type { SVGProps } from "react";

export type AppIconName =
  | "inbox"
  | "library"
  | "catalog"
  | "sources"
  | "operations"
  | "settings"
  | "music"
  | "search"
  | "chevronDown"
  | "chevronRight"
  | "plus"
  | "more"
  | "arrowLeft"
  | "close"
  | "globe"
  | "sun"
  | "moon"
  | "monitor"
  | "check"
  | "refresh"
  | "trash"
  | "folder"
  | "file"
  | "alert"
  | "external";

const paths: Record<AppIconName, React.ReactNode> = {
  inbox: (
    <>
      <path d="M4 4.75h16L20 14H4L4 4.75Z" />
      <path d="M4 14v4.25h16V14" />
      <path d="M8.5 17.25h7" />
    </>
  ),
  library: (
    <>
      <circle cx="12" cy="12" r="8.25" />
      <circle cx="12" cy="12" r="2.25" />
    </>
  ),
  catalog: (
    <>
      <path d="M5 5.5A2.5 2.5 0 0 1 7.5 3H19v18H7.5A2.5 2.5 0 0 1 5 18.5v-13Z" />
      <path d="M5 18.5A2.5 2.5 0 0 1 7.5 16H19" />
      <path d="M9 7h6" />
    </>
  ),
  sources: (
    <>
      <path d="M3.5 6.75A1.75 1.75 0 0 1 5.25 5h4l2 2.25h7.5a1.75 1.75 0 0 1 1.75 1.75v8.75a1.75 1.75 0 0 1-1.75 1.75H5.25A1.75 1.75 0 0 1 3.5 17.75V6.75Z" />
    </>
  ),
  operations: (
    <>
      <path d="M3 12h4l2.5-6 4 12 2.5-6H21" />
    </>
  ),
  settings: (
    <>
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09a1.7 1.7 0 0 0-1.11-1.56 1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09A1.7 1.7 0 0 0 4.6 8.9a1.7 1.7 0 0 0-.34-1.87l-.06-.06A2 2 0 1 1 7.03 4.14l.06.06A1.7 1.7 0 0 0 8.96 4.54 1.7 1.7 0 0 0 10 2.98V3a2 2 0 1 1 4 0v.09a1.7 1.7 0 0 0 1.03 1.56 1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.7 1.7 0 0 0 19.4 8.9a1.7 1.7 0 0 0 1.56 1.03H21a2 2 0 1 1 0 4h-.09A1.7 1.7 0 0 0 19.4 15Z" />
    </>
  ),
  music: (
    <>
      <path d="M9 18V6l10-2v12" />
      <circle cx="6.5" cy="18" r="2.5" />
      <circle cx="16.5" cy="16" r="2.5" />
    </>
  ),
  search: (
    <>
      <circle cx="10.5" cy="10.5" r="6.25" />
      <path d="m15.5 15.5 4 4" />
    </>
  ),
  chevronDown: <path d="m6 9 6 6 6-6" />,
  chevronRight: <path d="m9 6 6 6-6 6" />,
  plus: <path d="M12 5v14M5 12h14" />,
  more: (
    <>
      <circle cx="5" cy="12" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="12" cy="12" r="1.25" fill="currentColor" stroke="none" />
      <circle cx="19" cy="12" r="1.25" fill="currentColor" stroke="none" />
    </>
  ),
  arrowLeft: <path d="M19 12H5m6-6-6 6 6 6" />,
  close: <path d="m6 6 12 12M18 6 6 18" />,
  globe: (
    <>
      <circle cx="12" cy="12" r="8.25" />
      <path d="M3.75 12h16.5M12 3.75c2.2 2.3 3.25 5.05 3.25 8.25S14.2 18 12 20.25C9.8 18 8.75 15.3 8.75 12S9.8 6.05 12 3.75Z" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.65 17.65l1.42 1.42M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.65 6.35l1.42-1.42" />
    </>
  ),
  moon: <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />,
  monitor: (
    <>
      <rect x="3" y="4" width="18" height="13" rx="1.5" />
      <path d="M8 21h8M12 17v4" />
    </>
  ),
  check: <path d="m4.5 12.5 4.5 4.5L19.5 6.5" />,
  refresh: (
    <>
      <path d="M20 11a8 8 0 0 0-13.9-5.2L3 9" />
      <path d="M3 4v5h5" />
      <path d="M4 13a8 8 0 0 0 13.9 5.2L21 15" />
      <path d="M21 20v-5h-5" />
    </>
  ),
  trash: (
    <>
      <path d="M4 7h16M9 7V5h6v2M6.5 7l.75 12h9.5l.75-12" />
      <path d="M10 11v4M14 11v4" />
    </>
  ),
  folder: (
    <path d="M3.5 7A1.5 1.5 0 0 1 5 5.5h4L11 8h8a1.5 1.5 0 0 1 1.5 1.5v8A1.5 1.5 0 0 1 19 19H5a1.5 1.5 0 0 1-1.5-1.5V7Z" />
  ),
  file: (
    <>
      <path d="M6 3.5h7l5 5v12H6v-17Z" />
      <path d="M13 3.5v5h5" />
    </>
  ),
  alert: (
    <>
      <path d="M12 3 2.8 20h18.4L12 3Z" />
      <path d="M12 9v5M12 17.5v.5" />
    </>
  ),
  external: (
    <>
      <path d="M14 4h6v6M20 4l-9 9" />
      <path d="M18 14v5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5" />
    </>
  ),
};

export function AppIcon({
  name,
  className = "size-4",
  ...props
}: { name: AppIconName; className?: string } & Omit<
  SVGProps<SVGSVGElement>,
  "name" | "className"
>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className={className}
      {...props}
    >
      {paths[name]}
    </svg>
  );
}
