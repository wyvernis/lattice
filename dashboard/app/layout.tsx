import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Lattice — Cluster Control",
  description: "Live distributed LLM inference control plane",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="antialiased">{children}</body>
    </html>
  );
}
