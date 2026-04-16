import "~/styles/globals.css";

import { type Metadata } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";

import { OperatorShellHeader } from "~/components/operator-shell-header";
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

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html
      lang="ru"
      className={`${plexSans.variable} ${plexMono.variable}`}
    >
      <body className="min-h-screen bg-[var(--vectra-bg)] font-sans text-slate-100 antialiased">
        <TRPCReactProvider>
          <div className="vectra-shell overflow-hidden">
            <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(60,112,156,0.12),transparent_22%),radial-gradient(circle_at_bottom_right,rgba(174,95,42,0.12),transparent_20%)]" />
            <div className="vectra-shell-frame">
              <OperatorShellHeader />
              <main className="vectra-page-stack flex-1">{children}</main>
            </div>
          </div>
        </TRPCReactProvider>
      </body>
    </html>
  );
}
