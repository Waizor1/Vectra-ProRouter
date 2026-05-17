import "server-only";

import { cookies } from "next/headers";

export const VECTRA_UI_COOKIE = "vectra-ui";
export const VECTRA_UI_V1_VALUE = "v1";
export const VECTRA_UI_V2_VALUE = "v2";

export async function isUiV2(): Promise<boolean> {
  const cookieStore = await cookies();
  return cookieStore.get(VECTRA_UI_COOKIE)?.value !== VECTRA_UI_V1_VALUE;
}
