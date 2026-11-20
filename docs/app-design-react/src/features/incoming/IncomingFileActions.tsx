import { useEffect, useState } from "react";
import {
  Autocomplete,
  Input,
  Menu,
  MenuItem,
  MenuTrigger,
  Popover,
  SearchField,
  SubmenuTrigger,
  useFilter,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";

export type IncomingFileDestination = {
  id: string;
  name: string;
  paths: readonly string[];
};

export type IncomingFileActionsProps = {
  fileId: string;
  fileTitle: string;
  destinations: readonly IncomingFileDestination[];
  canCreateGroup: boolean;
  onMove: (fileId: string, destinationId: string) => void;
  onCreateGroup: (fileId: string) => void;
  isDisabled?: boolean;
};

const actionCopy = {
  en: {
    fileActions: (file: string) => `File actions: ${file}`,
    moveTo: "Move to…",
    moveDestinationForFile: (file: string) => `Destination group: ${file}`,
    searchGroup: "Find a group",
    noGroupsFound: "No groups found",
    createNewGroup: "Create new group",
  },
  ru: {
    fileActions: (file: string) => `Действия с файлом: ${file}`,
    moveTo: "Переместить в…",
    moveDestinationForFile: (file: string) => `Куда переместить: ${file}`,
    searchGroup: "Поиск группы",
    noGroupsFound: "Группы не найдены",
    createNewGroup: "Создать новую группу",
  },
} as const;

const itemClass =
  "flex min-h-8 cursor-pointer items-center gap-2 rounded-[var(--control-radius)] px-2 py-[7px] text-xs text-[var(--ink)] outline-none data-[disabled]:cursor-not-allowed data-[disabled]:text-[var(--muted)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)] data-[focused]:bg-[var(--raised)] data-[open]:bg-[var(--raised)] pointer-coarse:min-h-11";

const menuPopoverClass =
  "react-aria-Popover z-50 min-w-[12rem] max-w-[min(22rem,calc(100vw-1rem))] max-h-[min(20rem,calc(100dvh-1rem))] overflow-y-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]";

const searchPopoverClass =
  "react-aria-Popover z-50 w-72 min-w-0 max-w-[calc(100vw-1rem)] max-h-[min(20rem,calc(100dvh-1rem))] overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--ink)] shadow-[0_8px_24px_#0002]";

const triggerClass =
  "size-8 min-h-8 min-w-8 justify-center border-transparent! bg-transparent! p-0 text-[var(--muted)] hover:border-[var(--line)]! hover:bg-[var(--raised)]! hover:text-[var(--ink)] pointer-coarse:size-11 pointer-coarse:min-h-11 pointer-coarse:min-w-11";

const compactMenuQuery = "(max-width: 600px), (pointer: coarse)";

function useCompactMenuPlacement() {
  const [compact, setCompact] = useState(() =>
    typeof window.matchMedia === "function"
      ? window.matchMedia(compactMenuQuery).matches
      : false,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(compactMenuQuery);
    const update = () => setCompact(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return compact;
}

function EllipsisIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 16 16"
      className="size-4"
    >
      <circle cx="3.25" cy="8" r="1.15" fill="currentColor" />
      <circle cx="8" cy="8" r="1.15" fill="currentColor" />
      <circle cx="12.75" cy="8" r="1.15" fill="currentColor" />
    </svg>
  );
}

function ChevronRightIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 12 12"
      className="ml-auto size-3 shrink-0 text-[var(--muted)]"
    >
      <path
        d="M4.5 2.5 8 6l-3.5 3.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

type MoveDestinationMenuProps = {
  fileId: string;
  fileTitle: string;
  destinations: readonly IncomingFileDestination[];
  onMove: (fileId: string, destinationId: string) => void;
  copy: (typeof actionCopy)[keyof typeof actionCopy];
  isDisabled: boolean;
};

function MoveDestinationMenu({
  fileId,
  fileTitle,
  destinations,
  onMove,
  copy,
  isDisabled,
}: MoveDestinationMenuProps) {
  const destinationsById = new Map(
    destinations.map((destination) => [destination.id, destination]),
  );
  const { contains } = useFilter({ sensitivity: "base" });
  const filter = (
    textValue: string,
    inputValue: string,
    node: { key: string | number },
  ) => {
    const query = inputValue.trim();
    if (!query || contains(textValue, query)) return true;
    const destination = destinationsById.get(String(node.key));
    const pathQuery = query.replace(/\\/g, "/");
    return Boolean(
      destination?.paths.some((path) => contains(path, pathQuery)),
    );
  };
  return (
    <div className="flex max-h-[inherit] min-h-0 flex-col">
      <Autocomplete filter={filter} disableAutoFocusFirst>
        <SearchField
          autoFocus
          aria-label={copy.searchGroup}
          className="shrink-0 border-b border-[var(--line)] p-2"
        >
          <Input
            className="min-h-7 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-xs text-[var(--ink)] outline-none placeholder:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
            placeholder={copy.searchGroup}
          />
        </SearchField>
        <Menu
          aria-label={copy.moveDestinationForFile(fileTitle)}
          autoFocus={false}
          className="max-h-64 min-h-0 flex-1 overflow-y-auto p-1 outline-none"
          renderEmptyState={() => (
            <p className="m-0 px-2 py-3 text-center text-xs text-[var(--muted)]">
              {copy.noGroupsFound}
            </p>
          )}
        >
          {destinations.map((destination) => (
            <MenuItem
              key={destination.id}
              id={destination.id}
              isDisabled={isDisabled}
              className={itemClass}
              textValue={destination.name}
              onAction={() => onMove(fileId, destination.id)}
            >
              <span className="min-w-0 wrap-anywhere">{destination.name}</span>
            </MenuItem>
          ))}
        </Menu>
      </Autocomplete>
    </div>
  );
}

export function IncomingFileActions({
  fileId,
  fileTitle,
  destinations,
  canCreateGroup,
  onMove,
  onCreateGroup,
  isDisabled = false,
}: IncomingFileActionsProps) {
  const { locale } = useI18n();
  const copy = actionCopy[locale];
  const label = copy.fileActions(fileTitle);
  const compactMenu = useCompactMenuPlacement();
  if (!destinations.length && !canCreateGroup) return null;
  return (
    <MenuTrigger>
      <AppButton
        aria-label={label}
        isDisabled={isDisabled}
        className={triggerClass}
      >
        <EllipsisIcon />
      </AppButton>
      <Popover placement="bottom end" className={menuPopoverClass}>
        <Menu aria-label={label} className="outline-none">
          {destinations.length > 0 && (
            <SubmenuTrigger>
              <MenuItem
                className={itemClass}
                isDisabled={isDisabled}
                textValue={copy.moveTo}
              >
                <span className="min-w-0 wrap-anywhere">{copy.moveTo}</span>
                <ChevronRightIcon />
              </MenuItem>
              <Popover
                placement={compactMenu ? "bottom start" : "right top"}
                className={searchPopoverClass}
              >
                <MoveDestinationMenu
                  fileId={fileId}
                  fileTitle={fileTitle}
                  destinations={destinations}
                  onMove={onMove}
                  copy={copy}
                  isDisabled={isDisabled}
                />
              </Popover>
            </SubmenuTrigger>
          )}
          {canCreateGroup && (
            <MenuItem
              className={itemClass}
              isDisabled={isDisabled}
              textValue={copy.createNewGroup}
              onAction={() => onCreateGroup(fileId)}
            >
              <span className="min-w-0 wrap-anywhere">
                {copy.createNewGroup}
              </span>
            </MenuItem>
          )}
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}
