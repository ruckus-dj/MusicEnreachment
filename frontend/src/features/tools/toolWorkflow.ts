import { apiResponseError } from "../../api/client/errors";
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

function successful<T extends { status: number }, S extends T["status"]>(
  response: T,
  status: S,
): Extract<T, { status: S }> {
  if (response.status !== status) throw apiResponseError(response);
  return response as Extract<T, { status: S }>;
}

export async function loadManagedToolCatalogs(): Promise<
  Record<ManagedToolKind, CatalogBody>
> {
  const [ffmpeg, fpcalc] = await Promise.all([
    listToolCatalog({ package_kind: "ffmpeg" }),
    listToolCatalog({ package_kind: "fpcalc" }),
  ]);
  return {
    ffmpeg: successful(ffmpeg, 200).data,
    fpcalc: successful(fpcalc, 200).data,
  };
}

export async function preflightManagedToolInstall(
  kind: ManagedToolKind,
  release: string,
): Promise<InstallPreflightBody> {
  return successful(
    await preflightToolInstall({
      package_kind: kind,
      release_identity: release,
    }),
    200,
  ).data;
}

export async function startManagedToolInstall(
  plan: InstallPreflightBody,
  confirmedConflicts: string[] = plan.conflicts || [],
): Promise<OperationResponse> {
  return successful(
    await startToolInstall({
      preflight_token: plan.preflight_token,
      confirmed_conflicts: confirmedConflicts,
    }),
    200,
  ).data;
}
