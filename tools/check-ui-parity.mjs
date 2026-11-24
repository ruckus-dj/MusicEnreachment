import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const prototype = new URL("../docs/app-design-react/src/", import.meta.url);
const application = new URL("../frontend/src/", import.meta.url);

// Register each portable UI module after its first port. A prototype-first
// revision may lead the port only with exact bytes recorded in the receipt.
const pairs = [
  ["routes", "routes", ["ShellView.tsx"]],
  [
    "components",
    "components",
    [
      "AppButton.tsx",
      "DirectoryPathFieldView.tsx",
      "DirectoryPickerMessages.ts",
      "DirectoryPickerView.tsx",
      "LanguageControl.tsx",
      "ThemeControl.tsx",
      "themePreference.ts",
    ],
  ],
  ["i18n", "i18n", ["I18nProvider.tsx", "createNamespace.ts", "locale.ts", "translator.ts", "types.ts"]],
  ["i18n/namespaces", "i18n/namespaces", ["shell.ts"]],
  ["features/incoming", "features/incoming", ["IncomingFileActions.tsx", "IncomingGroupsOverviewView.tsx"]],
  ["features/operations", "features/operations", ["MatchingRecheckAbandonDialog.tsx", "OperationsList.tsx", "OperationsScreenView.tsx", "operationsScreenCopy.ts"]],
  [
    "features/sources",
    "features/sources",
    [
      "SourceAnalysisSteps.tsx",
      "SourceArtifactCleanupView.tsx",
      "SourceInspectorView.tsx",
      "SourceInventoryPanel.tsx",
      "SourceInventoryView.tsx",
      "SourceRegistrationView.tsx",
      "SourceRootDetailView.tsx",
      "SourceRootSettingsView.tsx",
      "SourceRootHeadingView.tsx",
      "SourceRootList.tsx",
      "SourceRootOverviewView.tsx",
      "SourceRootRemovalDialog.tsx",
      "SourceScanPanel.tsx",
      "SourceTechnicalResult.tsx",
      "sourceInspectorMessages.ts",
      "sourceInspectorModel.ts",
    ],
  ],
  [
    "features/settings",
    "features/settings",
    [
      "GenreCatalogView.tsx",
      "SettingsConflictDialog.tsx",
      "SettingsFoldersSection.tsx",
      "SettingsGeneralSection.tsx",
      "SettingsLeaveDialog.tsx",
      "SettingsManagedPackageCards.tsx",
      "SettingsOutputResetDialog.tsx",
      "SettingsPageFrame.tsx",
      "SettingsProvidersSection.tsx",
      "SettingsTabs.tsx",
      "SettingsToolInstallDialog.tsx",
      "SettingsToolsMoveReview.tsx",
      "SettingsToolsMoveReviewDialog.tsx",
      "SettingsToolsMoveEditor.tsx",
      "SettingsToolsOverview.tsx",
    ],
  ],
  ["features/setup", "features/setup", ["SetupDirectoryFields.tsx", "SetupFeedbackView.tsx", "SetupGenreCatalogView.tsx", "SetupMusicBrainzFields.tsx", "SetupProvidersView.tsx", "SetupPublicationOptions.tsx", "SetupSummaryView.tsx", "SetupToolCardsView.tsx", "SetupToolReleasePicker.tsx", "SetupWizard.tsx"]],
  ["features/tools", "features/tools", ["GitHubTokenFormView.tsx", "toolMessages.ts"]],
  [
    "features/library",
    "features/library",
    [
      "AggregatePublicationPreviewView.tsx",
      "ArtistMetadataCard.tsx",
      "GenrePicker.tsx",
      "LibraryAlbumListView.tsx",
      "LibraryAlbumPositionsView.tsx",
      "LibraryArtistCards.tsx",
      "LibraryArtistHeadingView.tsx",
      "LibraryArtistReleaseTracksView.tsx",
      "LibraryCatalogFilterView.tsx",
      "LibraryCatalogControls.tsx",
      "LibraryMissingEditionView.tsx",
      "LibraryPaginationView.tsx",
      "LibraryPositionHeadingView.tsx",
      "LibraryPositionIdentityView.tsx",
      "LibraryPositionSourcesView.tsx",
      "LibraryPositionWorkspace.tsx",
      "LibraryPositionTechnicalDetailsView.tsx",
      "LibrarySelectionDockView.tsx",
      "LibrarySelectionPreviewView.tsx",
      "LibrarySelectionReviewView.tsx",
      "LibraryTrackListView.tsx",
      "MetadataCardActions.tsx",
      "MetadataFieldPresentation.tsx",
      "MovementFieldsView.tsx",
      "WorksMovementSectionView.tsx",
      "WorkRowsView.tsx",
      "PublicationAudioMeasurementsView.tsx",
      "PublicationCurrentSummaryView.tsx",
      "ProviderGenreBaselineRow.tsx",
      "PublicationPreviewDetailsView.tsx",
      "PublicationTagSnapshotView.tsx",
      "TagSnapshotView.tsx",
      "firstPublicationPreviewMessages.ts",
      "genreMessages.ts",
      "libraryArtistCardStyles.ts",
    ],
  ],
  [
    "features/matching",
    "features/matchingWorkspace",
    [
      "ManualReleasePreparationView.tsx",
      "ManualSearchView.tsx",
      "MatchingBoard.tsx",
      "MatchingDnD.tsx",
      "MatchingEvidenceInspector.tsx",
      "MatchingLinkResultView.tsx",
      "MatchingPreview.tsx",
      "MatchingReceiptPublicationFrame.tsx",
      "MatchingReceiptPublicationOutcomeView.tsx",
      "MatchingReviewSearchView.tsx",
      "MatchingWorkspaceLayout.tsx",
      "evidenceOverrideMessages.ts",
      "index.ts",
      "manualReleasePreparationContract.ts",
      "manualSearchContract.ts",
      "matchingBoardMessages.ts",
      "matchingBoardStyles.ts",
      "matchingLinksCopy.ts",
      "matchingSourcePath.ts",
      "scoring.ts",
      "sourceFileOrder.ts",
      "types.ts",
    ],
  ],
  [
    "features/local-release",
    "features/local-release",
    [
      "LocalRecordingArtistField.tsx",
      "LocalReleaseCancelDialog.tsx",
      "LocalReleaseEditor.tsx",
      "LocalReleaseForm.tsx",
      "LocalReleaseRecordingPicker.tsx",
      "localReleaseFormModel.ts",
      "localReleaseMessages.ts",
      "localReleaseUi.tsx",
      "useLocalReleaseForm.ts",
    ],
  ],
  [
    "features/local-catalog",
    "features/own-catalog",
    ["OwnCatalogDetailsView.tsx", "OwnCatalogEditorShell.tsx", "OwnCatalogList.tsx", "OwnCatalogPagination.tsx", "OwnCatalogRecordFieldsView.tsx"],
  ],
];

const sourcePairs = [
  ["app/tailwind.css", "styles/index.css"],
  ...pairs.flatMap(([from, to, files]) =>
    files.map((file) => [`${from}/${file}`, `${to}/${file}`]),
  ),
];

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");

export function compareUiSources(pairs, receipts, readSource, readCopy) {
  const leads = new Map();
  const errors = [];
  for (const receipt of receipts) {
    if (
      typeof receipt?.from !== "string" ||
      typeof receipt.reason !== "string" ||
      !receipt.reason.trim() ||
      !/^[a-f0-9]{64}$/.test(receipt.prototypeSHA256) ||
      (receipt.applicationSHA256 !== null &&
        !/^[a-f0-9]{64}$/.test(receipt.applicationSHA256)) ||
      leads.has(receipt.from)
    ) {
      errors.push(`Invalid or duplicate prototype lead: ${receipt?.from}`);
      continue;
    }
    leads.set(receipt.from, receipt);
  }

  let identical = 0;
  const pending = [];
  for (const [from, to] of pairs) {
    const label = `docs/app-design-react/src/${from} ↔ frontend/src/${to}`;
    const lead = leads.get(from);
    leads.delete(from);
    try {
      const source = readSource(from);
      let copy;
      try {
        copy = readCopy(to);
      } catch (error) {
        if (error.code !== "ENOENT" || lead?.applicationSHA256 !== null) throw error;
      }
      if (copy && source.equals(copy)) {
        identical++;
        if (lead) errors.push(`Remove the completed prototype lead: ${from}`);
      } else if (
        lead && digest(source) === lead.prototypeSHA256 &&
        (copy ? digest(copy) : null) === lead.applicationSHA256
      ) {
        pending.push(`${label}: ${lead.reason}`);
      } else {
        errors.push(`Different source bytes: ${label}`);
      }
    } catch (error) {
      errors.push(`Cannot compare ${label}: ${error.code ?? error.message}`);
    }
  }

  for (const from of leads.keys()) {
    errors.push(`Prototype lead is not a registered pair: ${from}`);
  }
  return { errors, identical, pending };
}

function unpairedSources(root, paired, label) {
  const src = fileURLToPath(root);
  return readdirSync(src, { recursive: true, withFileTypes: true })
    .filter((entry) => !entry.isDirectory())
    .map((entry) =>
      `${relative(src, entry.parentPath)}/${entry.name}`
        .replaceAll("\\", "/")
        .replace(/^\//, ""),
    )
    .filter((path) =>
      !/\.(?:test|spec|d)\./.test(path) &&
      !/(?:^|\/)\.DS_Store$|\.md$/.test(path) &&
      !/^(?:test|api\/generated)\//.test(path) &&
      !paired.has(path),
    )
    .map((path) => `${label}/${path}`);
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const receipts = JSON.parse(
    readFileSync(new URL("./ui-parity-prototype-leads.json", import.meta.url), "utf8"),
  );
  const result = compareUiSources(
    sourcePairs,
    receipts,
    (path) => readFileSync(new URL(path, prototype)),
    (path) => readFileSync(new URL(path, application)),
  );
  const prototypeEntry = readFileSync(new URL("../index.html", prototype), "utf8");
  const applicationEntry = readFileSync(new URL("../index.html", application), "utf8");
  if (prototypeEntry
    .replace("<title>Медиатека · стенд MeloTrove</title>", "<title>MeloTrove</title>")
    .replace('src="/src/app/main.tsx"', 'src="/src/main.tsx"') !== applicationEntry) {
    result.errors.push("Different application entry presentation: index.html");
  }
  for (const error of result.errors) console.error(error);
  for (const lead of result.pending) console.log(`Prototype awaiting port: ${lead}`);
  console.log(`Byte-identical UI source pairs: ${result.identical}/${sourcePairs.length}`);
  const actualUnpaired = new Set([
    ...unpairedSources(prototype, new Set(sourcePairs.map(([from]) => from)), "docs/app-design-react/src"),
    ...unpairedSources(application, new Set(sourcePairs.map(([, to]) => to)), "frontend/src"),
  ].sort());
  const unpaired = new Set(
    readFileSync(new URL("./ui-parity-unpaired.txt", import.meta.url), "utf8")
      .split(/\r?\n/)
      .filter((line) => line && !line.startsWith("#")),
  );
  console.log(`Unpaired runtime source files (providers and outstanding ports): ${actualUnpaired.size}`);
  if (process.argv.includes("--inventory")) {
    for (const path of actualUnpaired) console.log(path);
  }
  const inventoryErrors = [
    ...[...actualUnpaired].filter((path) => !unpaired.has(path))
      .map((path) => `Unclassified runtime source: ${path}`),
    ...[...unpaired].filter((path) => !actualUnpaired.has(path))
      .map((path) => `Remove stale unpaired entry: ${path}`),
  ];
  for (const error of inventoryErrors) console.error(error);
  if (result.errors.length > 0 || inventoryErrors.length > 0) process.exitCode = 1;
}
