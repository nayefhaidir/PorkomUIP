"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, Bug, Mail, Phone } from "lucide-react";

export default function ServiceError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const [occurredAt, setOccurredAt] = useState("");

  useEffect(() => {
    setOccurredAt(new Date().toLocaleString("id-ID"));
    const timer = window.setInterval(async () => {
      try {
        const response = await fetch("/api/health", { cache: "no-store", signal: AbortSignal.timeout(5000) });
        if (response.ok) window.location.reload();
      } catch {
        // The retry button remains available while the service is unreachable.
      }
    }, 10000);
    return () => window.clearInterval(timer);
  }, []);

  return (
    <main className="service-error" role="alert">
      <div className="service-error-card">
        <img className="service-error-logo" src="/pln-indonesia-power.png" alt="PLN Indonesia Power" />
        <div className="service-error-warning"><AlertTriangle aria-hidden="true" /><span>GANGGUAN LAYANAN</span></div>
        <h1>Layanan sedang tidak tersedia</h1>
        <p>Maaf, layanan sedang mengalami gangguan. Silakan coba kembali beberapa saat lagi atau hubungi Helpdesk UIP.</p>
        <section className="service-error-diagnostic" aria-label="Informasi diagnostik">
          <h2><Bug aria-hidden="true" /> Informasi diagnostik</h2>
          <p>Gunakan kode referensi berikut saat menghubungi Helpdesk. Detail internal dan stack trace tidak ditampilkan agar data sesi tetap terlindungi.</p>
          <dl>
            <div><dt>Kode kesalahan</dt><dd><code>{error.digest || "FRONTEND-ERROR"}</code></dd></div>
            <div><dt>Komponen</dt><dd>Aplikasi web UIP Meeting Portal</dd></div>
            <div><dt>Waktu terdeteksi</dt><dd>{occurredAt || "Mendeteksi waktu…"}</dd></div>
          </dl>
        </section>
        <div className="service-error-contacts">
          <a href="tel:081310729415"><Phone aria-hidden="true" /><span><small>Telepon Helpdesk UIP</small><strong>0813-1072-9415</strong></span></a>
          <a href="mailto:admin.helpdeskuip@plnindonesiapower.co.id"><Mail aria-hidden="true" /><span><small>Email Helpdesk UIP</small><strong>admin.helpdeskuip@plnindonesiapower.co.id</strong></span></a>
        </div>
        <button onClick={() => { reset(); window.location.reload(); }}>Coba lagi</button>
      </div>
    </main>
  );
}
