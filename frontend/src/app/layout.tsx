import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = { title: "UIP Meeting Portal", description: "Unit Induk Proyek Management Meeting Portal" };
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="id"><body>{children}</body></html>; }
