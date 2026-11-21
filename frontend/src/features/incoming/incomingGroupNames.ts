import type {
  IncomingFileResponse,
  IncomingGroupResponse,
} from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";

interface GroupEvidence {
  album?: string;
  folder?: string;
  path?: string;
  qualifiedPath?: string;
  otherFiles: number;
}

export type IncomingGroupName = Pick<
  GroupEvidence,
  "album" | "path" | "otherFiles"
>;

function foldedTitle(title: string) {
  return title.normalize("NFC").toLowerCase();
}

function groupEvidence(
  group: IncomingGroupResponse,
  files: ReadonlyMap<string, IncomingFileResponse>,
): GroupEvidence {
  const members = new Set([
    ...(group.members ?? []),
    ...(group.unready_members ?? []),
  ]);
  const albums = new Map<string, string>();
  const folders = new Map<string, { label: string; members: number }>();
  const paths = [...members].flatMap((id) => {
    const file = files.get(id);
    for (const [tag, values] of Object.entries(
      file?.display_tags ?? file?.tags ?? {},
    )) {
      if (tag.trim().toUpperCase() !== "ALBUM") continue;
      for (const raw of values ?? []) {
        const title = raw.trim();
        const key = foldedTitle(title);
        if (key && !albums.has(key)) albums.set(key, title);
      }
    }
    const seen = new Set<string>();
    return (file?.locations ?? []).flatMap((location) => {
      const relative = location.relative_path;
      if (!relative) return [];
      const parent = relative.slice(0, Math.max(0, relative.lastIndexOf("/")));
      const key = JSON.stringify([location.root_id, parent]);
      if (!seen.has(key)) {
        seen.add(key);
        folders.set(key, {
          label: parent || location.configured_path,
          members: (folders.get(key)?.members ?? 0) + 1,
        });
      }
      return [
        {
          relative,
          qualified: `${location.configured_path.replace(/[\\/]+$/, "")}/${relative}`,
        },
      ];
    });
  });
  paths.sort(
    (a, b) =>
      a.relative.localeCompare(b.relative, "en") ||
      a.qualified.localeCompare(b.qualified, "en"),
  );
  const commonFolders = [...folders.values()].filter(
    (folder) => folder.members === members.size,
  );
  return {
    album: albums.size === 1 ? albums.values().next().value : undefined,
    folder:
      members.size > 1 && commonFolders.length === 1
        ? commonFolders[0].label
        : undefined,
    path: paths[0]?.relative,
    qualifiedPath: paths[0]?.qualified,
    otherFiles: Math.max(0, members.size - 1),
  };
}

export function incomingGroupNames(
  groups: readonly IncomingGroupResponse[],
  files: readonly IncomingFileResponse[] | null,
): Map<string, IncomingGroupName> {
  const byId = new Map((files ?? []).map((file) => [file.variant_id, file]));
  const evidence = groups.map((group) => groupEvidence(group, byId));
  const counts = new Map<string, number>();
  const primary = ({ album, folder, path }: GroupEvidence) =>
    album ?? folder ?? path;
  for (const item of evidence) {
    const text = primary(item);
    if (text) {
      const key = foldedTitle(text);
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
  }
  return new Map(
    groups.map(({ id }, index) => {
      const item = evidence[index];
      const text = primary(item);
      const duplicate = text && (counts.get(foldedTitle(text)) ?? 0) > 1;
      return [
        id,
        {
          album: item.album,
          path: duplicate
            ? item.qualifiedPath
            : item.album
              ? undefined
              : (item.folder ?? item.path),
          otherFiles:
            !item.album && (!item.folder || duplicate) ? item.otherFiles : 0,
        },
      ];
    }),
  );
}

export function incomingGroupNameText(
  name: IncomingGroupName | undefined,
  locale: Locale,
): string {
  if (name?.album) {
    return name.path ? `${name.album} · ${name.path}` : name.album;
  }
  if (!name?.path) {
    return locale === "ru"
      ? "Путь к файлам недоступен"
      : "Source path unavailable";
  }
  if (!name.otherFiles) return name.path;
  const count = new Intl.NumberFormat(locale).format(name.otherFiles);
  if (locale === "en") {
    return `${name.path} and ${count} more ${name.otherFiles === 1 ? "file" : "files"}`;
  }
  const noun = {
    one: "файл",
    few: "файла",
    many: "файлов",
  }[
    new Intl.PluralRules("ru").select(name.otherFiles) as "one" | "few" | "many"
  ];
  return `${name.path} и ещё ${count} ${noun}`;
}
