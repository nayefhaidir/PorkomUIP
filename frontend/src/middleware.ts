import { NextRequest, NextResponse } from "next/server";

function secure(response: NextResponse) {
  response.headers.set("X-Content-Type-Options", "nosniff");
  response.headers.set("X-Frame-Options", "DENY");
  response.headers.set("Referrer-Policy", "strict-origin-when-cross-origin");
  response.headers.set("Permissions-Policy", "camera=(), microphone=(), geolocation=()");
  response.headers.set("Content-Security-Policy", "frame-ancestors 'none'; object-src 'none'; base-uri 'self'; form-action 'self'");
  if (process.env.NODE_ENV === "production") response.headers.set("Strict-Transport-Security", "max-age=31536000; includeSubDomains");
  return response;
}

export async function middleware(request: NextRequest) {
  const pathname = request.nextUrl.pathname;
  if (pathname.startsWith("/api/")) {
    const forwardedHeaders = new Headers(request.headers);
    forwardedHeaders.set("x-forwarded-proto", request.nextUrl.protocol.replace(/:$/, ""));
    if (["POST", "PUT", "PATCH", "DELETE"].includes(request.method)) {
      const origin = request.headers.get("origin");
      if (origin) {
        try {
          if (new URL(origin).host !== request.headers.get("host")) return secure(NextResponse.json({ error: "Permintaan lintas situs ditolak" }, { status: 403 }));
        }
        catch { return secure(NextResponse.json({ error: "Header Origin tidak valid" }, { status: 403 })); }
      }
    }
    return secure(NextResponse.next({ request: { headers: forwardedHeaders } }));
  }
  if (["/", "/beranda", "/login", "/layar-publik", "/kalender-rapat", "/ketersediaan-ruangan", "/healthz"].includes(pathname)) return secure(NextResponse.next());
  const token = request.cookies.get("uip_session")?.value;
  if (!token) return secure(NextResponse.redirect(new URL("/login", request.url)));
  return secure(NextResponse.next());
}

export const config = { matcher: ["/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp)$).*)"] };
