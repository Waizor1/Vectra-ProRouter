"use client";

import { usePathname } from "next/navigation";

import { OperatorShellV2 } from "~/features/shell/operator-shell-v2";

export interface AppShellFrameProps {
  children: React.ReactNode;
}

export function AppShellFrame({ children }: Readonly<AppShellFrameProps>) {
  const pathname = usePathname();
  const isPublicInstallPage = pathname === "/install";

  if (isPublicInstallPage) {
    return (
      <div className="mx-auto flex min-h-screen w-full max-w-5xl items-start px-4 py-6 sm:px-6 lg:px-8 lg:py-10">
        <main className="w-full">{children}</main>
      </div>
    );
  }

  return <OperatorShellV2>{children}</OperatorShellV2>;
}
