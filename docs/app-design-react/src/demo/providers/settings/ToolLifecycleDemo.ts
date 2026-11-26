export type ManagedToolPackage = "ffmpeg" | "fpcalc";
export type InstallationStatus = "active" | "installed" | "available";

export interface DemoInstallation {
  id: string;
  package: ManagedToolPackage;
  release: string;
  status: InstallationStatus;
  occupied: boolean;
}

export const initialDemoInstallations: DemoInstallation[] = [
  {
    id: "ffmpeg-8.0",
    package: "ffmpeg",
    release: "8.0",
    status: "active",
    occupied: false,
  },
  {
    id: "fpcalc-1.5.1",
    package: "fpcalc",
    release: "1.5.1",
    status: "active",
    occupied: false,
  },
  {
    id: "ffmpeg-7.1",
    package: "ffmpeg",
    release: "7.1",
    status: "installed",
    occupied: false,
  },
];

export const availableToolReleases: Record<ManagedToolPackage, string[]> = {
  ffmpeg: ["8.0.1", "8.0"],
  fpcalc: ["1.5.1"],
};

export type DemoInstallPreflight = {
  kind: ManagedToolPackage;
  release: string;
  /** Exact tools root frozen for this admitted preflight. */
  toolsRoot: string;
  targets: string[];
  conflicts: string[];
  token: string;
};

/** Executables a managed package publishes, matching the demo server platform. */
function demoExecutables(kind: ManagedToolPackage): string[] {
  return kind === "ffmpeg" ? ["ffmpeg", "ffprobe"] : ["fpcalc"];
}

function demoInstallTarget(
  toolsDirectory: string,
  kind: ManagedToolPackage,
  release: string,
  executable: string,
): string {
  const root = toolsDirectory.trim().replace(/\\/g, "/").replace(/\/+$/, "");
  return `${root}/${kind}/${release}/${executable}`;
}

/**
 * Files that already exist in the demo tools directory but are not owned by a
 * managed installation. A real preflight reports exactly such targets as
 * conflicts: the path is occupied, yet no ready installation manages it. The
 * data is in-memory only and never touches a real filesystem.
 */
export const initialDemoUnmanagedToolFiles = [
  "/var/lib/melotrove/tools/ffmpeg/8.0.1/ffmpeg",
];

/**
 * In-memory mirror of the installation preflight. It computes the same target
 * list and conflict list the server derives from the tools directory, so the
 * demo host can start a clean install directly and review exact conflicts
 * without inventing filesystem behavior.
 */
export function installPreflightDemo(
  toolsDirectory: string,
  kind: ManagedToolPackage,
  release: string,
  installations: readonly DemoInstallation[],
  unmanagedFiles: readonly string[],
): DemoInstallPreflight {
  const toolsRoot = toolsDirectory.trim();
  const targets = demoExecutables(kind).map((executable) =>
    demoInstallTarget(toolsRoot, kind, release, executable),
  );
  // Only verified installations can account for occupied paths.
  const known = new Set(
    installations
      .filter((item) => item.status !== "available")
      .flatMap((item) =>
        demoExecutables(item.package).map((executable) =>
          demoInstallTarget(toolsRoot, item.package, item.release, executable),
        ),
      ),
  );
  const conflicts = targets.filter(
    (target) => unmanagedFiles.includes(target) && !known.has(target),
  );
  return {
    kind,
    release,
    toolsRoot,
    targets,
    conflicts,
    token: [
      "demo-install-preflight",
      "tools-root",
      toolsRoot,
      "kind",
      kind,
      "release",
      release,
      ...targets,
      ...conflicts,
    ].join("|"),
  };
}

export function activateDemoInstallation(
  items: DemoInstallation[],
  id: string,
): DemoInstallation[] {
  const target = items.find(
    (item) => item.id === id && item.status === "installed" && !item.occupied,
  );
  if (!target) return items;
  return items.map((item) =>
    item.package !== target.package
      ? item
      : { ...item, status: item.id === id ? "active" : "installed" },
  );
}

export function canDeleteDemoInstallation(item: DemoInstallation): boolean {
  return item.status === "installed" && !item.occupied;
}

export function createDemoInstallation(
  items: DemoInstallation[],
  packageKind: ManagedToolPackage,
  release: string,
): DemoInstallation[] {
  const id = `${packageKind}-${release}`;
  if (items.some((item) => item.id === id)) return items;
  return [
    ...items,
    { id, package: packageKind, release, status: "installed", occupied: false },
  ];
}

export function activateOrCreateDemoInstallation(
  items: DemoInstallation[],
  packageKind: ManagedToolPackage,
  release: string,
): DemoInstallation[] {
  const id = `${packageKind}-${release}`;
  const hasTarget = items.some((item) => item.id === id);
  const next: DemoInstallation[] = hasTarget
    ? items.map((item) =>
        item.package !== packageKind
          ? item
          : {
              ...item,
              status: item.id === id ? "active" : "installed",
            },
      )
    : [
        ...items.map((item) =>
          item.package === packageKind && item.status === "active"
            ? { ...item, status: "installed" as const }
            : item,
        ),
        {
          id,
          package: packageKind,
          release,
          status: "active",
          occupied: false,
        },
      ];
  return next;
}

export function isAbsoluteDemoServerPath(value: string): boolean {
  return (
    value.startsWith("/") ||
    /^[a-z]:[\\/]/i.test(value) ||
    /^\\\\[^\\]+\\[^\\]+/.test(value)
  );
}

export function managedDemoFileCount(
  installations: readonly DemoInstallation[],
) {
  return installations.reduce(
    (count, item) => count + (item.package === "ffmpeg" ? 2 : 1),
    0,
  );
}

export function movePreflightDemo(
  path: string,
  managedCount: number,
  conflicts: string[] = [],
) {
  const normalized = path.trim();
  if (!isAbsoluteDemoServerPath(normalized))
    throw new Error("Выберите абсолютный путь на сервере.");
  return {
    path: normalized,
    managedCount,
    conflicts,
    token: `demo-preflight-${normalized}-${managedCount}-${conflicts.join("|")}`,
  };
}
