"use client";

import { usePathname } from "next/navigation";

import { OperatorShellHeader } from "~/components/operator-shell-header";
import { OperatorShellV2 } from "~/features/shell/operator-shell-v2";

export interface AppShellFrameProps {
  children: React.ReactNode;
  uiV2: boolean;
}

export function AppShellFrame({
  children,
  uiV2,
}: Readonly<AppShellFrameProps>) {
  const pathname = usePathname();
  const isPublicInstallPage = pathname === "/install";

  if (isPublicInstallPage) {
    return (
      <div className="mx-auto flex min-h-screen w-full max-w-5xl items-start px-4 py-6 sm:px-6 lg:px-8 lg:py-10">
        <main className="w-full">{children}</main>
      </div>
    );
  }

  if (uiV2) {
    return <OperatorShellV2>{children}</OperatorShellV2>;
  }

  return (
    <div className="vectra-shell">
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(60,112,156,0.12),transparent_22%),radial-gradient(circle_at_bottom_right,rgba(174,95,42,0.12),transparent_20%)]" />
      <div className="vectra-shell-frame">
        <OperatorShellHeader />
        <main className="vectra-page-stack min-w-0 flex-1">{children}</main>
      </div>
    </div>
  );
}
