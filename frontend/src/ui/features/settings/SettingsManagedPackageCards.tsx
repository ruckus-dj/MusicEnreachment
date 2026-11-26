import { AppButton } from "../../components/AppButton";
import { RowActionMenu } from "../../components/RowActionMenu";

export type ManagedPackageLocale = "en" | "ru";

export type ManagedPackageKind = "ffmpeg" | "fpcalc";

export type ManagedPackageLifeState =
  | "ready"
  | "preparing"
  | "failed"
  | "unknown";

export type ManagedPackageRelease = {
  identity: string;
  source?: string;
};

export type ManagedPackageAction = {
  disabled: boolean;
  onPress: (trigger: Element) => void;
};

export type ManagedPackageInstallation = {
  id: string;
  release: ManagedPackageRelease;
  active: boolean;
  lifeState: ManagedPackageLifeState;
  occupied?: boolean;
  activate?: ManagedPackageAction;
  remove?: ManagedPackageAction;
};

export type ManagedPackageCatalog =
  | { state: "notLoaded"; notice?: string }
  | { state: "loading"; notice?: string }
  | {
      state: "loaded";
      releases: readonly ManagedPackageRelease[];
      notice?: string;
    }
  | { state: "empty"; notice?: string };

export type ManagedPackageCardModel = {
  kind: ManagedPackageKind;
  installations: readonly ManagedPackageInstallation[];
  catalog: ManagedPackageCatalog;
  selectedRelease?: string;
  onReleaseChange: (identity: string) => void;
  downloadAndVerify: ManagedPackageAction;
};

const messages = {
  en: {
    ffmpeg: "FFmpeg · ffmpeg + ffprobe",
    fpcalc: "fpcalc",
    activeVersion: "Active version",
    noActiveVersion: "No active version",
    installedVersions: "Installed versions",
    noInstalledVersions: "No installed versions.",
    availableVersions: "Available versions",
    active: "active",
    occupied: "in use by an operation",
    ready: "ready",
    preparing: "preparing",
    failed: "failed",
    unknown: "unknown",
    activate: "Activate",
    remove: "Remove",
    moreActions: "More actions",
    versionToInstall: "Version to install",
    versionLabel: "Version {kind}",
    chooseVersion: "Choose a version",
    downloadAndVerify: "Download and verify without activation",
    loadingCatalog: "Loading catalog…",
    catalogNotLoaded: "Catalog has not been loaded yet.",
    emptyCatalog: "No releases are available for this package.",
  },
  ru: {
    ffmpeg: "FFmpeg · ffmpeg + ffprobe",
    fpcalc: "fpcalc",
    activeVersion: "Активная версия",
    noActiveVersion: "Нет активной версии",
    installedVersions: "Установленные версии",
    noInstalledVersions: "Нет установленных версий.",
    availableVersions: "Доступные версии",
    active: "активна",
    occupied: "используется операцией",
    ready: "готова",
    preparing: "подготовка",
    failed: "ошибка",
    unknown: "неизвестно",
    activate: "Активировать",
    remove: "Удалить",
    moreActions: "Действия",
    versionToInstall: "Версия для установки",
    versionLabel: "Версия {kind}",
    chooseVersion: "Выберите версию",
    downloadAndVerify: "Скачать и проверить без активации",
    loadingCatalog: "Загрузка каталога…",
    catalogNotLoaded: "Каталог ещё не загружен.",
    emptyCatalog: "Для этого пакета нет доступных выпусков.",
  },
} as const;

const cards =
  "grid grid-cols-[repeat(auto-fit,minmax(min(100%,320px),1fr))] gap-4";
const card =
  "grid min-w-0 content-start gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 text-sm text-[var(--ink)]";
const subheading = "m-0 text-xs font-medium text-[var(--muted)]";
const note = "m-0 text-xs text-[var(--muted)]";
const row =
  "flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-2";
const select =
  "h-9 w-full rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0";

function releaseText(release: ManagedPackageRelease) {
  return release.source
    ? `${release.identity} (${release.source})`
    : release.identity;
}

function lifeStateText(
  copy: (typeof messages)[ManagedPackageLocale],
  state: ManagedPackageLifeState,
) {
  return copy[state];
}

export function SettingsManagedPackageCards({
  locale,
  packages,
}: {
  locale: ManagedPackageLocale;
  packages: readonly ManagedPackageCardModel[];
}) {
  const copy = messages[locale];
  return (
    <div className={cards}>
      {packages.map((model) => {
        const active = model.installations.filter((item) => item.active);
        const { catalog } = model;
        const loaded = catalog.state === "loaded";
        const selection =
          loaded &&
          catalog.releases.some(
            (release) => release.identity === model.selectedRelease,
          )
            ? model.selectedRelease
            : undefined;
        return (
          <section
            key={model.kind}
            aria-labelledby={`${model.kind}-title`}
            className={card}
          >
            <h3
              id={`${model.kind}-title`}
              className="m-0 text-sm font-semibold"
            >
              {copy[model.kind]}
            </h3>
            <h4 className={subheading}>{copy.activeVersion}</h4>
            {active.length > 0 ? (
              <ul className="m-0 grid list-none gap-1 p-0">
                {active.map((item) => (
                  <li key={item.id} className="min-w-0 wrap-anywhere">
                    {releaseText(item.release)}
                  </li>
                ))}
              </ul>
            ) : (
              <p className={note}>{copy.noActiveVersion}</p>
            )}
            <h4 className={subheading}>{copy.installedVersions}</h4>
            {model.installations.length > 0 ? (
              <ul className="m-0 grid list-none gap-1.5 p-0">
                {model.installations.map((item) => (
                  <li key={item.id} className={row}>
                    <div className="grid min-w-0 gap-0.5">
                      <span className="font-medium wrap-anywhere">
                        {releaseText(item.release)}
                      </span>
                      <span
                        className={
                          item.lifeState === "failed"
                            ? "m-0 text-xs text-[var(--error)]"
                            : note
                        }
                      >
                        {lifeStateText(copy, item.lifeState)}
                        {item.active ? ` · ${copy.active}` : ""}
                        {item.occupied ? ` · ${copy.occupied}` : ""}
                      </span>
                    </div>
                    {(item.activate || item.remove) && (
                      <div className="flex flex-wrap items-center gap-1">
                        {item.activate && (
                          <AppButton
                            aria-label={`${copy.activate} ${item.release.identity}`}
                            isDisabled={item.activate.disabled}
                            onPress={(event) =>
                              item.activate?.onPress(event.target)
                            }
                          >
                            {copy.activate}
                          </AppButton>
                        )}
                        {item.remove && item.activate && (
                          <RowActionMenu
                            label={`${copy.moreActions}: ${item.release.identity}`}
                            actions={[
                              {
                                id: "remove",
                                label: `${copy.remove} ${item.release.identity}`,
                                isDisabled: item.remove.disabled,
                                isDestructive: true,
                                onAction: (trigger) =>
                                  item.remove?.onPress(trigger),
                              },
                            ]}
                          />
                        )}
                        {item.remove && !item.activate && (
                          <AppButton
                            aria-label={`${copy.remove} ${item.release.identity}`}
                            isDisabled={item.remove.disabled}
                            onPress={(event) =>
                              item.remove?.onPress(event.target)
                            }
                          >
                            {copy.remove}
                          </AppButton>
                        )}
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className={note}>{copy.noInstalledVersions}</p>
            )}
            <h4 className={subheading}>{copy.availableVersions}</h4>
            {catalog.notice && <p className={note}>{catalog.notice}</p>}
            {loaded && catalog.releases.length > 0 ? (
              <div className="grid gap-2">
                <label className="grid gap-1 text-xs font-medium">
                  {copy.versionToInstall}
                  <select
                    aria-label={copy.versionLabel.replace("{kind}", model.kind)}
                    className={select}
                    value={selection ?? ""}
                    onChange={(event) =>
                      model.onReleaseChange(event.target.value)
                    }
                  >
                    <option value="">{copy.chooseVersion}</option>
                    {catalog.releases.map((release) => (
                      <option key={release.identity} value={release.identity}>
                        {releaseText(release)}
                      </option>
                    ))}
                  </select>
                </label>
                <AppButton
                  className="justify-self-start"
                  isDisabled={model.downloadAndVerify.disabled || !selection}
                  onPress={(event) =>
                    model.downloadAndVerify.onPress(event.target)
                  }
                >
                  {copy.downloadAndVerify}
                </AppButton>
              </div>
            ) : (
              <p className={note}>
                {catalog.state === "loading"
                  ? copy.loadingCatalog
                  : catalog.state === "empty" ||
                      (loaded && catalog.releases.length === 0)
                    ? copy.emptyCatalog
                    : copy.catalogNotLoaded}
              </p>
            )}
          </section>
        );
      })}
    </div>
  );
}
