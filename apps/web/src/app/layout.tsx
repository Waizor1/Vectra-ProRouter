import "~/styles/globals.css";

import { type Metadata } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";

import { AppShellFrame } from "~/components/app-shell-frame";
import { isUiV2 } from "~/lib/feature-flag";
import { TRPCReactProvider } from "~/trpc/react";

export const metadata: Metadata = {
  title: "Панель Vectra",
  description:
    "Русскоязычная router-centric панель управления PassWall2 и сертифицированными OpenWrt-роутерами.",
  icons: [{ rel: "icon", url: "/favicon.ico" }],
};

const plexSans = IBM_Plex_Sans({
  subsets: ["latin", "cyrillic"],
  variable: "--font-plex-sans",
  weight: ["400", "500", "600"],
  display: "swap",
});

const plexMono = IBM_Plex_Mono({
  subsets: ["latin", "cyrillic"],
  variable: "--font-plex-mono",
  weight: ["400", "500"],
  display: "swap",
});

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const uiV2 = await isUiV2();

  return (
    <html
      lang="ru"
      className={`dark ${plexSans.variable} ${plexMono.variable}`}
    >
      <body className="min-h-screen bg-[var(--vectra-bg)] font-sans text-slate-100 antialiased">
        <TRPCReactProvider>
          <AppShellFrame uiV2={uiV2}>{children}</AppShellFrame>
        </TRPCReactProvider>
      </body>
    </html>
  );
}
