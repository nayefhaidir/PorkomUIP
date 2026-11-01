"use client";

import { useEffect, useRef, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { Home, CalendarDays, PlusCircle, ChartColumn, Building2, Search, Bell, ChevronDown, Menu, X, LogOut, UserRound, Settings, BellRing, FileText, AlertTriangle } from "lucide-react";
import { roleLabel } from "@/lib/roles";
import type { ToastKind } from "@/lib/client-notifications";

const links = [[Home,"Beranda","/beranda"],[CalendarDays,"Kalender Rapat","/kalender-rapat"],[PlusCircle,"Pengajuan Rapat","/pengajuan-rapat"],[ChartColumn,"Rekap Bulanan","/rekap-bulanan"],[Building2,"Ketersediaan Ruangan","/ketersediaan-ruangan"]] as const;

export function AppShell({ children, user, publicMode = false }: { children: React.ReactNode; user: { name: string; role: string; department: string | null; pageAccess?: string[] }; publicMode?: boolean }) {
  const pathname = usePathname();
  const router = useRouter();
  const accountRef = useRef<HTMLDivElement>(null);
  const [drawer, setDrawer] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [menu, setMenu] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);
  const [q, setQ] = useState("");
  const [unread, setUnread] = useState(0);
  const [apiUnavailable, setApiUnavailable] = useState(false);
  const [apiDiagnostic, setApiDiagnostic] = useState<{ reference: string; detail: string; checkedAt: string } | null>(null);
  const [toast, setToast] = useState<{ id: string; title: string; body: string; kind: ToastKind } | null>(null);
  const canRequest = !publicMode && ["ADMIN", "ORGANIZER"].includes(user.role);
  const canManage = ["ADMIN", "APPROVER"].includes(user.role);
  const accessByHref: Record<string, string> = { "/beranda": "beranda", "/kalender-rapat": "kalender", "/pengajuan-rapat": "pengajuan", "/rekap-bulanan": "rekap", "/ketersediaan-ruangan": "ruangan" };
  const customAccess = Boolean(user.pageAccess?.length);
  const hasAccess = (key: string, fallback: boolean) => user.role === "ADMIN" || customAccess ? user.role === "ADMIN" || Boolean(user.pageAccess?.includes(key)) : fallback;
  const visibleLinks = links.filter(([, , href]) => hasAccess(accessByHref[href], href === "/pengajuan-rapat" ? canRequest : href === "/rekap-bulanan" ? user.role !== "PARTICIPANT" : true));

  async function checkApiAvailability() {
    try {
      const response = await fetch("/api/health", { cache: "no-store", signal: AbortSignal.timeout(5000) });
      setApiUnavailable(!response.ok);
      if (response.ok) {
        setApiDiagnostic(null);
      } else {
        setApiDiagnostic(current => current ?? {
          reference: crypto.randomUUID(),
          detail: `Backend merespons HTTP ${response.status}.`,
          checkedAt: new Date().toISOString(),
        });
      }
    } catch {
      setApiUnavailable(true);
      setApiDiagnostic(current => current ?? {
        reference: crypto.randomUUID(),
        detail: "Pemeriksaan backend gagal: koneksi terputus atau melewati batas waktu 5 detik.",
        checkedAt: new Date().toISOString(),
      });
    }
  }

  useEffect(() => {
    void checkApiAvailability();
    const timer = setInterval(() => void checkApiAvailability(), 10000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    setDrawer(false);
    setMenu(false);
  }, [pathname]);

  useEffect(() => {
    if (!drawer) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setDrawer(false);
        setMenu(false);
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [drawer]);

  useEffect(() => {
    if (!menu) return;
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (event.target instanceof Node && !accountRef.current?.contains(event.target)) setMenu(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenu(false);
    };
    document.addEventListener("pointerdown", closeOnOutsideClick);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsideClick);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [menu]);

  useEffect(() => {
    if (publicMode) return;
    let live = true;
    let latestId: string | null = null;
    const load = async () => {
      try {
        const response = await fetch("/api/notifications?pageSize=1", { cache: "no-store" });
        if (response.ok) {
          const data = await response.json();
          if (!live) return;
          setUnread(data.unread ?? 0);
          const item = data.items?.[0];
          if (item && latestId && item.id !== latestId) {
            setToast({ id: item.id, title: item.title, body: item.body, kind: "info" });
            window.setTimeout(() => setToast(current => current?.id === item.id ? null : current), 6500);
          }
          if (item) latestId = item.id;
        }
      } catch {
        // Keep the last known unread count when notifications are unavailable.
      }
    };
    void load();
    const timer = setInterval(() => void load(), 12000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [publicMode]);

  useEffect(() => {
    let timer: number | undefined;
    const handle = (event: Event) => {
      const detail = (event as CustomEvent<{ title: string; body: string; kind?: ToastKind }>).detail;
      if (!detail) return;
      const id = crypto.randomUUID();
      setToast({ id, title: detail.title, body: detail.body, kind: detail.kind ?? "success" });
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(() => setToast(current => current?.id === id ? null : current), 6500);
    };
    window.addEventListener("app:toast", handle);
    return () => {
      window.removeEventListener("app:toast", handle);
      if (timer) window.clearTimeout(timer);
    };
  }, []);

  async function logout() {
    if (loggingOut) return;
    setLoggingOut(true);
    try {
      const response = await fetch("/api/auth/logout", { method: "POST" });
      if (!response.ok) throw new Error("Sesi gagal ditutup. Silakan coba lagi.");
      setMenu(false);
      setDrawer(false);
      router.replace("/login");
      router.refresh();
    } catch (error) {
      const id = crypto.randomUUID();
      setToast({
        id,
        title: "Gagal keluar",
        body: error instanceof Error ? error.message : "Sesi gagal ditutup. Silakan coba lagi.",
        kind: "error",
      });
      window.setTimeout(() => setToast(current => current?.id === id ? null : current), 6500);
    } finally {
      setLoggingOut(false);
    }
  }

  const toggleNav = () => {
    if (window.matchMedia("(max-width: 650px)").matches) {
      setDrawer(value => !value);
      setMenu(false);
    } else {
      setCollapsed(value => !value);
    }
  };
  const publicLinks = visibleLinks.filter(([, , href]) => href === "/beranda" || href === "/kalender-rapat");
  const navigationLinks = publicMode ? publicLinks : visibleLinks;

  return (
    <div className={`app role-${user.role.toLowerCase()} ${publicMode ? "app-public" : ""}`}>
      <aside className={`sidebar ${drawer ? "drawer-open" : ""} ${collapsed ? "collapsed" : ""}`} id="mobile-navigation">
        <a className="brand" href="/beranda" aria-label="PLN Indonesia Power">
          <img src="/pln-indonesia-power.png" alt="PLN Indonesia Power" />
        </a>
        <nav aria-label="Navigasi utama">
          {navigationLinks.map(([Icon, label, href]) => (
            <a className={(pathname === href || (href === "/beranda" && pathname === "/")) ? "active" : ""} href={href} key={href} onClick={() => setDrawer(false)} title={label}>
              <Icon size={17} /><span>{label}</span>
            </a>
          ))}
          {!publicMode && hasAccess("pengelolaan", user.role === "ADMIN") && (
            <a className={pathname === "/pengelolaan" ? "active" : ""} href="/pengelolaan" onClick={() => setDrawer(false)} title="Dokumen & Evidence">
              <FileText size={17} /><span>Dokumen & Evidence</span>
            </a>
          )}
          {!publicMode && hasAccess("pengaturan", user.role === "ADMIN" || user.role === "APPROVER") && (
            <a className={pathname === "/pengaturan" ? "active" : ""} href="/pengaturan" onClick={() => setDrawer(false)} title="Pengaturan Portal">
              <Settings size={17} /><span>Pengaturan Portal</span>
            </a>
          )}
        </nav>
        <div className="sidebar-actions">
          {publicMode ? (
            <a href="/login" onClick={() => setDrawer(false)}><UserRound size={17} /><span>Masuk / Login</span></a>
          ) : (
            <>
              <a href="/profil" onClick={() => setDrawer(false)}><UserRound size={17} /><span>Profil akun</span></a>
              <button type="button" onClick={() => void logout()} disabled={loggingOut}><LogOut size={17} /><span>{loggingOut ? "Keluar…" : "Keluar / Logout"}</span></button>
            </>
          )}
        </div>
        <div className="side-slogan"><strong>#ENERGY<br />Beyond<br />Today</strong><hr /></div>
      </aside>

      {drawer && <button className="drawer-backdrop" onClick={() => setDrawer(false)} aria-label="Tutup navigasi" />}

      <main className={`main ${collapsed ? "collapsed" : ""}`}>
        <header className="topbar">
          <button className="menu" onClick={toggleNav} aria-label={drawer ? "Tutup navigasi" : "Buka navigasi"} aria-expanded={drawer} aria-controls="mobile-navigation">
            {drawer ? <X /> : <Menu />}
          </button>
          <a className="mobile-brand" href="/beranda" aria-label="PLN Indonesia Power">
            <img src="/pln-indonesia-power.png" alt="PLN Indonesia Power" />
          </a>
          <div className="topbar-brand">
            <strong>UNIT INDUK PROYEK</strong>
            <span>Management Meeting Portal - Portal Komunikasi Unit Induk Proyek (Porkom UIP)</span>
          </div>
          <form className="search" role="search" onSubmit={event => { event.preventDefault(); setDrawer(false); router.push(`/kalender-rapat?q=${encodeURIComponent(q)}`); }}>
            <Search size={15} />
            <input value={q} onChange={event => setQ(event.target.value)} placeholder="Cari judul rapat, bidang, atau ruangan..." aria-label="Cari rapat" />
          </form>

          {publicMode ? (
            <a className="public-login-button" href="/login"><UserRound size={16} /> Masuk / Login</a>
          ) : (
            <>
              <a className="bell" href="/notifikasi" aria-label={unread ? `Notifikasi, ${unread} belum dibaca` : "Notifikasi"}>
                <Bell size={19} />{unread > 0 && <span className="bell-count">{unread > 99 ? "99+" : unread}</span>}
              </a>
              <div className="profile" ref={accountRef}>
                <span className="avatar">{user.name.split(/\s+/).map(name => name[0]).slice(0, 2).join("").toUpperCase()}</span>
                <div className="profile-details"><b>{user.name}</b><small>{roleLabel(user.role)}{user.department ? ` · ${user.department}` : ""}</small></div>
                <button className="profile-toggle" type="button" onClick={() => setMenu(value => !value)} aria-label={menu ? "Tutup menu akun" : "Buka menu akun"} aria-expanded={menu} aria-controls="profile-menu">
                  <ChevronDown size={15} />
                </button>
                {menu && (
                  <div className="profile-menu" id="profile-menu">
                    <a href="/profil" onClick={() => setMenu(false)}><UserRound /> Profil</a>
                    {canManage && <a href="/pengaturan" onClick={() => setMenu(false)}><Settings /> Pengaturan</a>}
                    <button type="button" onClick={() => void logout()} disabled={loggingOut}><LogOut /> {loggingOut ? "Keluar…" : "Keluar / Logout"}</button>
                  </div>
                )}
              </div>
            </>
          )}
        </header>

        {apiUnavailable && (
          <aside className="service-health-card" role="alert">
            <span className="service-health-icon"><AlertTriangle aria-hidden="true" /></span>
            <div className="service-health-content">
              <div className="service-health-heading"><strong>Layanan sedang tidak tersedia</strong><span>Fitur akan pulih otomatis setelah backend tersambung kembali.</span></div>
              <p>Butuh bantuan? <a href="tel:081310729415">Helpdesk UIP 0813-1072-9415</a> · <a href="mailto:admin.helpdeskuip@plnindonesiapower.co.id">admin.helpdeskuip@plnindonesiapower.co.id</a>.</p>
              <details className="service-health-diagnostic">
                <summary>Detail teknis untuk Helpdesk</summary>
                <dl>
                  <div><dt>Referensi gangguan</dt><dd><code>{apiDiagnostic?.reference ?? "Menyiapkan kode…"}</code></dd></div>
                  <div><dt>Diagnostik</dt><dd>{apiDiagnostic?.detail ?? "Pemeriksaan layanan sedang berlangsung."}</dd></div>
                  <div><dt>Pemeriksaan terakhir</dt><dd>{apiDiagnostic?.checkedAt ? new Date(apiDiagnostic.checkedAt).toLocaleString("id-ID") : "—"}</dd></div>
                </dl>
              </details>
            </div>
            <button type="button" onClick={() => void checkApiAvailability()}>Coba lagi</button>
          </aside>
        )}

        {children}

        {toast && !publicMode && (
          <aside className={`global-notification-toast toast-${toast.kind}`} role="status" aria-live={toast.kind === "error" ? "assertive" : "polite"}>
            <span className="toast-icon"><BellRing size={19} /></span>
            <div>
              <strong>{toast.kind === "error" ? "Perlu perhatian" : toast.kind === "info" ? "Informasi" : "Berhasil diperbarui"}</strong>
              <b>{toast.title}</b><p>{toast.body}</p><a href="/notifikasi">Lihat notifikasi</a>
            </div>
            <button aria-label="Tutup notifikasi" onClick={() => setToast(null)}><X size={16} /></button>
          </aside>
        )}
      </main>
    </div>
  );
}
