import "~/styles/globals.css";

import { type Metadata } from "next";
import { IBM_Plex_Mono, IBM_Plex_Sans } from "next/font/google";

import { Navigation } from "~/components/navigation";
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
          <div className="relative min-h-screen overflow-hidden">
            <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(circle_at_top_left,rgba(60,112,156,0.12),transparent_22%),radial-gradient(circle_at_bottom_right,rgba(174,95,42,0.12),transparent_20%)]" />
            <div className="relative mx-auto flex min-h-screen w-full max-w-[1400px] flex-col px-2 py-2 sm:px-5 sm:py-4 lg:px-6">
              <header className="mb-4 rounded-md border border-white/10 bg-[rgba(9,12,18,0.9)] px-3 py-3 backdrop-blur sm:px-4">
                <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
                  <div className="min-w-0">
                    <p className="vectra-kicker text-[var(--vectra-accent)]">
                      Панель оператора Vectra
                    </p>
                    <h1 className="mt-1 text-lg font-semibold tracking-[-0.01em] text-white sm:text-xl">
                      Управление роутерами и PassWall2
                    </h1>
                  </div>
                  <Navigation />
                </div>
              </header>
              <main className="flex-1">{children}</main>
            </div>
          </div>
        </TRPCReactProvider>
      </body>
    </html>
  );
}
