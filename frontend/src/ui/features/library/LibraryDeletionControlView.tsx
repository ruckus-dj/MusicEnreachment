import { Fragment } from "react";
import { Tooltip, TooltipTrigger } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  LibraryDeletionDialogView,
  LibraryDeletionOutputsView,
  libraryDeletionDialogTitle,
} from "./LibraryDeletionDialogView";
import { releaseControlAlertClass } from "./libraryReleaseControlStyles";

export type LibraryDeletionKind = "artist" | "release" | "publication";

export type LibraryDeletionViewAction = {
  id: string;
  label: string;
  onPress: () => void;
  isDisabled?: boolean;
  danger?: boolean;
};

export type LibraryDeletionOutputRowView = {
  id: string;
  path: string;
  label?: string;
  status?: string;
};

export type LibraryDeletionFactView = {
  id: string;
  label: string;
  value: string;
};

export type LibraryDeletionArtistReleaseView = {
  id: string;
  label: string;
};

export type LibraryDeletionCoartistWarningView = {
  id: string;
  releaseTitle: string;
  artistNames?: readonly string[];
};

export type LibraryDeletionBlockerView = {
  id: string;
  text: string;
};

export type LibraryDeletionPreviewModel = {
  title: string;
  artistReleases?: {
    title: string;
    items: readonly LibraryDeletionArtistReleaseView[];
  };
  positions?: {
    title: string;
    items: readonly { id: string; label: string }[];
  };
  coartistWarnings?: {
    title: string;
    namesLabel: string;
    items: readonly LibraryDeletionCoartistWarningView[];
  };
  facts: readonly LibraryDeletionFactView[];
  outputs: readonly LibraryDeletionOutputRowView[];
  blockers?: {
    title: string;
    items: readonly LibraryDeletionBlockerView[];
  };
  frozen?: string;
};

export type LibraryDeletionOperationModel = {
  status: string;
  partial?: string;
  progress?: {
    stageLabel: string;
    stageValue: string;
    progressLabel: string;
    progressValue: string;
    progressMax?: number;
    progressNow?: number;
  };
  safeError?: string;
  actionError?: string;
  retry?: {
    label: string;
    onPress: () => void;
    isDisabled: boolean;
  };
};

export type LibraryDeletionDialogModel = {
  targetLabel: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dismissable: boolean;
  actions: readonly LibraryDeletionViewAction[];
  scope: string;
  uncertain?: string;
  preview?: LibraryDeletionPreviewModel;
  previewError?: string;
  actionError?: string;
  blockedNotice?: string;
};

export type LibraryDeletionControlViewProps = {
  locale: Locale;
  kind: LibraryDeletionKind;
  trigger?: {
    label: string;
    iconLabel?: string;
    onPress: () => void;
  };
  operation?: LibraryDeletionOperationModel;
  dialog: LibraryDeletionDialogModel;
};

const dangerActionClassName =
  "border-[var(--error)]! text-[var(--error)]! hover:bg-[var(--error-soft)]!";

export function LibraryDeletionControlView({
  locale,
  kind,
  trigger,
  operation,
  dialog,
}: LibraryDeletionControlViewProps) {
  const progress = operation?.progress;
  const preview = dialog.preview;
  const artistReleases = preview?.artistReleases;
  const positions = preview?.positions;
  const coartistWarnings = preview?.coartistWarnings;
  const blockers = preview?.blockers;
  return (
    <>
      {trigger?.iconLabel ? (
        <TooltipTrigger>
          <AppButton
            aria-label={trigger.iconLabel}
            className="artist-album-delete text-[var(--error)]"
            onPress={trigger.onPress}
          >
            <svg
              viewBox="0 0 24 24"
              width="18"
              height="18"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M3 6h18M8 6V4h8v2m3 0-1 14H6L5 6m4 4v6m6-6v6" />
            </svg>
          </AppButton>
          <Tooltip className="rounded border border-[var(--line)] bg-[var(--surface)] p-2">
            {trigger.label}
          </Tooltip>
        </TooltipTrigger>
      ) : trigger ? (
        <AppButton className={dangerActionClassName} onPress={trigger.onPress}>
          {trigger.label}
        </AppButton>
      ) : null}
      {operation ? (
        <section
          className="mt-3 space-y-2 rounded border border-[var(--line)] p-3"
          aria-label={libraryDeletionDialogTitle(locale, kind)}
        >
          <p role="status">{operation.status}</p>
          {operation.partial ? <p>{operation.partial}</p> : null}
          {progress ? (
            <dl className="grid gap-x-3 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
              <dt className="text-[var(--muted)]">{progress.stageLabel}</dt>
              <dd className="break-words">{progress.stageValue}</dd>
              <dt className="text-[var(--muted)]">{progress.progressLabel}</dt>
              <dd className="space-y-1">
                <span>{progress.progressValue}</span>
                {progress.progressMax != null && progress.progressMax > 0 ? (
                  <progress
                    className="block w-full"
                    aria-label={progress.progressLabel}
                    max={progress.progressMax}
                    value={Math.min(
                      progress.progressNow ?? 0,
                      progress.progressMax,
                    )}
                  />
                ) : null}
              </dd>
            </dl>
          ) : null}
          {operation.safeError ? (
            <p role="alert" className={releaseControlAlertClass}>
              {operation.safeError}
            </p>
          ) : null}
          {operation.retry ? (
            <AppButton
              isDisabled={operation.retry.isDisabled}
              onPress={operation.retry.onPress}
            >
              {operation.retry.label}
            </AppButton>
          ) : null}
          {operation.actionError ? (
            <p role="alert" className={releaseControlAlertClass}>
              {operation.actionError}
            </p>
          ) : null}
        </section>
      ) : null}
      <LibraryDeletionDialogView
        locale={locale}
        kind={kind}
        targetLabel={dialog.targetLabel}
        open={dialog.open}
        onOpenChange={dialog.onOpenChange}
        dismissable={dialog.dismissable}
        actions={dialog.actions.map((action) => (
          <AppButton
            key={action.id}
            className={action.danger ? dangerActionClassName : undefined}
            isDisabled={action.isDisabled}
            onPress={action.onPress}
          >
            {action.label}
          </AppButton>
        ))}
      >
        <p>{dialog.scope}</p>
        {dialog.uncertain ? (
          <p role="alert" className={releaseControlAlertClass}>
            {dialog.uncertain}
          </p>
        ) : null}
        {preview ? (
          <div className="space-y-3 rounded border border-[var(--line)] p-3">
            <p className="wrap-anywhere">{preview.title}</p>
            {artistReleases ? (
              <section aria-label={artistReleases.title}>
                <h3 className="font-semibold">{artistReleases.title}</h3>
                <ul className="list-disc pl-5">
                  {artistReleases.items.map((release) => (
                    <li key={release.id}>{release.label}</li>
                  ))}
                </ul>
              </section>
            ) : null}
            {coartistWarnings ? (
              <section aria-label={coartistWarnings.title} role="note">
                <h3 className="font-semibold">{coartistWarnings.title}</h3>
                <ul className="list-disc space-y-1 pl-5">
                  {coartistWarnings.items.map((warning) => (
                    <li key={warning.id}>
                      <span className="font-medium">
                        {warning.releaseTitle}
                      </span>
                      {warning.artistNames?.length ? (
                        <span>
                          {` — ${coartistWarnings.namesLabel}: ${warning.artistNames.join(", ")}`}
                        </span>
                      ) : null}
                    </li>
                  ))}
                </ul>
              </section>
            ) : null}
            <dl className="grid gap-x-3 gap-y-1 text-sm sm:grid-cols-[max-content_1fr]">
              {preview.facts.map((fact) => (
                <Fragment key={fact.id}>
                  <dt>{fact.label}</dt>
                  <dd>{fact.value}</dd>
                </Fragment>
              ))}
            </dl>
            {positions && (
              <section aria-label={positions.title} className="min-w-0">
                <h3 className="text-sm font-semibold">{positions.title}</h3>
                <ul className="max-h-48 overflow-y-auto break-words">
                  {positions.items.map((item) => (
                    <li key={item.id}>{item.label}</li>
                  ))}
                </ul>
              </section>
            )}
            <LibraryDeletionOutputsView
              locale={locale}
              rows={preview.outputs}
            />
            {blockers ? (
              <div role="alert" className={releaseControlAlertClass}>
                <h3 className="font-semibold">{blockers.title}</h3>
                <ul className="list-disc pl-5">
                  {blockers.items.map((blocker) => (
                    <li key={blocker.id}>{blocker.text}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {preview.frozen ? <p>{preview.frozen}</p> : null}
          </div>
        ) : null}
        {dialog.previewError ? (
          <p role="alert" className={releaseControlAlertClass}>
            {dialog.previewError}
          </p>
        ) : null}
        {dialog.actionError ? (
          <p role="alert" className={releaseControlAlertClass}>
            {dialog.actionError}
          </p>
        ) : null}
        {dialog.blockedNotice ? (
          <p role="status">{dialog.blockedNotice}</p>
        ) : null}
      </LibraryDeletionDialogView>
    </>
  );
}
