"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { showToast } from "@/lib/client-notifications";

export function VerificationAction({ id, verified }: { id: string; verified: boolean }) {
  const [busy, setBusy] = useState(false);
  const router = useRouter();
  if (verified) return <span className="form-success">Terverifikasi</span>;

  async function verify() {
    if (busy) return;
    setBusy(true);
    try {
      const response = await fetch(`/api/documents/${encodeURIComponent(id)}/verification`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: "{}" });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error ?? "Verifikasi evidence gagal.");
      showToast("Evidence diverifikasi", "Penyelenggara rapat telah diberi notifikasi.");
      router.refresh();
    } catch (cause) {
      showToast("Verifikasi gagal", cause instanceof Error ? cause.message : "Verifikasi evidence gagal.", "error");
    } finally {
      setBusy(false);
    }
  }

  return <button type="button" className="secondary-button" disabled={busy} onClick={() => void verify()}>{busy ? "Memproses…" : "Tandai diverifikasi"}</button>;
}
