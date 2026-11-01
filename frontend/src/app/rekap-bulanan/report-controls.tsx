"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Download, Printer, RefreshCw } from "lucide-react";

export default function ReportControls({ query }: { query: string }) {
  const router = useRouter();
  const [updatedAt, setUpdatedAt] = useState("");
  useEffect(() => {
    const update = () => setUpdatedAt(new Intl.DateTimeFormat("id-ID", { timeZone: "Asia/Jakarta", hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date()));
    update();
    const timer = window.setInterval(() => { update(); router.refresh(); }, 60_000);
    return () => window.clearInterval(timer);
  }, [router]);
  return <div className="report-toolbar">
    <span className="report-live"><i/> Live {updatedAt ? "· " + updatedAt + " WIB" : ""}</span>
    <a href={"/api/reports/monthly/export?" + query + "&format=csv"}><Download size={14}/> CSV</a>
    <a href={"/api/reports/monthly/export?" + query + "&format=xlsx"}><Download size={14}/> Excel</a>
    <a href={"/api/reports/monthly/export?" + query + "&format=pdf"}><Download size={14}/> PDF</a>
    <button type="button" onClick={() => window.print()}><Printer size={14}/> Cetak</button>
    <button type="button" onClick={() => router.refresh()} aria-label="Muat ulang data"><RefreshCw size={14}/></button>
  </div>;
}
