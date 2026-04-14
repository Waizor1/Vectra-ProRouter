import { NextResponse, type NextRequest } from "next/server";

import {
  getOperatorCookieName,
  verifyOperatorSession,
} from "~/server/operator-session";

function unauthorizedApiRequest() {
  return NextResponse.json({ error: "Operator session required." }, { status: 401 });
}

export async function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;

  if (pathname.startsWith("/api/router")) {
    return NextResponse.next();
  }

  if (pathname === "/login") {
    return NextResponse.next();
  }

  const session = await verifyOperatorSession(
    request.cookies.get(getOperatorCookieName())?.value
  );

  if (session) {
    if (pathname === "/") {
      return NextResponse.redirect(new URL("/fleet", request.url));
    }
    return NextResponse.next();
  }

  if (pathname.startsWith("/api/trpc")) {
    return unauthorizedApiRequest();
  }

  return NextResponse.redirect(new URL("/login", request.url));
}

export const config = {
  matcher: [
    "/",
    "/login",
    "/fleet/:path*",
    "/routers/:path*",
    "/drafts/:path*",
    "/updates/:path*",
    "/rescue/:path*",
    "/enrollment/:path*",
    "/downloads/:path*",
    "/api/trpc/:path*",
  ],
};
