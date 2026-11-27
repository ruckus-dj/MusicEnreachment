export const setupToolReleases = {
  ffmpeg: ["8.0.1", "8.0"],
  fpcalc: ["1.5.1"],
} as const;

export type SetupToolKind = keyof typeof setupToolReleases;

export function setupToolExecutables(kind: SetupToolKind): string[] {
  return kind === "ffmpeg" ? ["ffmpeg", "ffprobe"] : ["fpcalc"];
}
