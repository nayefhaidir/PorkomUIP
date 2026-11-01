"use client";
import { type FormEvent, useState } from "react";
import { LockKeyhole } from "lucide-react";
export default function LoginPage() {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    const form = new FormData(event.currentTarget);
    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: form.get("email"), password: form.get("password") }),
      });
      const data = await response.json().catch(() => ({})) as { error?: string };
      if (!response.ok) {
        setError(data.error ?? "Login gagal");
        return;
      }
      window.location.assign("/beranda");
    } catch {
      setError("Tidak dapat menghubungi server. Periksa koneksi lalu coba lagi.");
    } finally {
      setBusy(false);
    }
  }

  return <main className="login-page"><div><form className="login-card" onSubmit={submit}><img className="login-logo" src="/pln-indonesia-power.png" alt="PLN Indonesia Power"/><p className="login-brand">UNIT INDUK PROYEK</p><h1>Portal Komunikasi UIP</h1><p className="login-caption">Masuk untuk mengelola kegiatan rapat Unit Induk Proyek.</p><label>Email<input name="email" type="email" autoComplete="username" required placeholder="nama@plnindonesiapower.co.id"/></label><label>Kata sandi<input name="password" type="password" autoComplete="current-password" required/></label>{error&&<p className="form-error" role="alert">{error}</p>}<button className="primary-button" disabled={busy}>{busy?"Memeriksa…":<><LockKeyhole size={16}/> Masuk</>}</button></form><a className="login-public-link" href="/layar-publik">Buka tampilan informasi publik <span>→</span></a></div></main>;
}
