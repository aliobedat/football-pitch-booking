import type { Metadata } from 'next';

// ── Print-route shell (WO-REPORTS-R4) ─────────────────────────────────────────
// A4 statement wrapper for /reports/print/*. Lives OUTSIDE the (dashboard)
// group: no Sidebar/Header on paper, while AuthProvider (root layout) and the
// httpOnly-cookie session are unaffected. The dark dashboard theme is
// hard-coded on <body> in the root layout; this wrapper fully covers it with a
// light, print-safe surface — the light theme is scoped here and leaks nowhere
// (dashboard files untouched). print-color-adjust: exact per ruling C1.

export const metadata: Metadata = { title: 'كشف للطباعة' };

export default function PrintLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="print-statement min-h-screen bg-white text-[#1a1a1a]">
      <style>{`
        @page { size: A4; margin: 14mm 12mm; }
        .print-statement { print-color-adjust: exact; -webkit-print-color-adjust: exact; }
        .print-statement table { width: 100%; border-collapse: collapse; }
        /* Repeat table headers across page breaks (locked G0 decision). */
        .print-statement thead { display: table-header-group; }
        .print-statement tr { break-inside: avoid; page-break-inside: avoid; }
        @media print {
          /* The root layout paints <body> dark; paper must be white. This rule
             mounts only with the print routes, so the dashboard is unaffected. */
          body { background: #fff !important; }
          .print-statement .no-print { display: none !important; }
        }
      `}</style>
      <div className="mx-auto max-w-[190mm] px-6 py-8 print:px-0 print:py-0 print:max-w-none">
        {children}
      </div>
    </div>
  );
}
