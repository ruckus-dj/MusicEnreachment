import { successful } from "../../api/client/response";
import {
  listToolCatalog,
  preflightToolInstall,
  startToolInstall,
} from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationActionInputBodyPackageKind,
  InstallPreflightBody,
  OperationResponse,
} from "../../api/generated/client.schemas";

export type ManagedToolKind = InstallationActionInputBodyPackageKind;
type HttpErrorFactory = (response: { status: number; data?: unknown }) => Error;

function checked<T extends { status: number }, S extends T["status"]>(
  response: T,
  status: S,
  errorFactory?: HttpErrorFactory,
): Extract<T, { status: S }> {
  if (response.status !== status) {
    if (errorFactory) throw errorFactory(response);
    return successful(response, status);
  }
  return response as Extract<T, { status: S }>;
}

export async function loadManagedToolCatalogs(
  errorFactory?: HttpErrorFactory,
): Promise<Record<ManagedToolKind, CatalogBody>> {
  const [ffmpeg, fpcalc] = await Promise.all([
    listToolCatalog({ package_kind: "ffmpeg" }),
    listToolCatalog({ package_kind: "fpcalc" }),
  ]);
  return {
    ffmpeg: checked(ffmpeg, 200, errorFactory).data,
    fpcalc: checked(fpcalc, 200, errorFactory).data,
  };
}

export async function preflightManagedToolInstall(
  kind: ManagedToolKind,
  release: string,
  errorFactory?: HttpErrorFactory,
): Promise<InstallPreflightBody> {
  return checked(
    await preflightToolInstall({
      package_kind: kind,
      release_identity: release,
    }),
    200,
    errorFactory,
  ).data;
}

export async function startManagedToolInstall(
  plan: InstallPreflightBody,
  confirmedConflicts: string[] = plan.conflicts || [],
  errorFactory?: HttpErrorFactory,
): Promise<OperationResponse> {
  return checked(
    await startToolInstall({
      preflight_token: plan.preflight_token,
      confirmed_conflicts: confirmedConflicts,
    }),
    200,
    errorFactory,
  ).data;
}
