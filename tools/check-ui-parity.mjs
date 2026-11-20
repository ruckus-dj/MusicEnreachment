import { readFileSync } from "node:fs";

const prototype = new URL("../docs/app-design-react/src/", import.meta.url);
const application = new URL("../frontend/src/", import.meta.url);

// Only files mounted as the same presentation in both applications belong here.
// A new prototype revision may lead the port; compare after integrating it.
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
    ],
  ],
  ["features/incoming", "features/incoming", ["IncomingFileActions.tsx"]],
  ["features/operations", "features/operations", ["OperationsList.tsx"]],
  ["features/sources", "features/sources", ["SourceRootList.tsx", "SourceScanPanel.tsx"]],
  [
    "features/settings",
    "features/settings",
    [
      "GenreCatalogView.tsx",
      "SettingsFoldersSection.tsx",
      "SettingsGeneralSection.tsx",
      "SettingsManagedPackageCards.tsx",
      "SettingsOutputResetDialog.tsx",
      "SettingsProvidersSection.tsx",
      "SettingsTabs.tsx",
      "SettingsToolInstallDialog.tsx",
      "SettingsToolsMoveReview.tsx",
      "SettingsToolsMoveEditor.tsx",
      "SettingsToolsOverview.tsx",
    ],
  ],
  ["features/setup", "features/setup", ["SetupDirectoryFields.tsx", "SetupToolReleasePicker.tsx", "SetupWizard.tsx"]],
  ["features/tools", "features/tools", ["GitHubTokenFormView.tsx", "toolMessages.ts"]],
  [
    "features/library",
    "features/library",
    [
      "LibraryAlbumPositionsView.tsx",
      "LibraryArtistCards.tsx",
      "LibraryMissingEditionView.tsx",
      "LibrarySelectionReviewView.tsx",
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
      "MatchingPreview.tsx",
      "MatchingWorkspaceLayout.tsx",
      "evidenceOverrideMessages.ts",
      "index.ts",
      "manualReleasePreparationContract.ts",
      "manualSearchContract.ts",
      "matchingBoardMessages.ts",
      "matchingBoardStyles.ts",
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
    ["OwnCatalogEditorShell.tsx", "OwnCatalogList.tsx", "OwnCatalogPagination.tsx"],
  ],
];

let total = 0;
let identical = 0;
for (const [prototypeDir, applicationDir, files] of pairs) {
  for (const file of files) {
    const from = `${prototypeDir}/${file}`;
    const to = `${applicationDir}/${file}`;
    const label = `docs/app-design-react/src/${from} ↔ frontend/src/${to}`;
    total++;
    try {
      const source = readFileSync(new URL(from, prototype));
      const copy = readFileSync(new URL(to, application));
      if (source.equals(copy)) {
        identical++;
      } else {
        console.error(`Different source bytes: ${label}`);
      }
    } catch (error) {
      console.error(`Cannot compare ${label}: ${error.code ?? error.message}`);
    }
  }
}
console.log(`Byte-identical UI source pairs: ${identical}/${total}`);
if (identical !== total) process.exitCode = 1;
