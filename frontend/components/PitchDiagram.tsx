// PitchDiagram — the branded top-view pitch SVG used as the hero fallback when
// no image exists. Extracted from app/pitches/[id]/page.tsx (unchanged) so the
// venue page (WO-VENUES Gate 1c) reuses it instead of forking.
export default function PitchDiagram({ hue, featured }: { hue: string; featured: boolean }) {
  const lines = featured ? 'rgba(52,211,153,0.55)' : 'rgba(255,255,255,0.12)';
  return (
    <svg viewBox="0 0 320 160" fill="none" xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true" className="absolute inset-0 w-full h-full">
      <rect width="320" height="160" fill={hue} />
      <rect x="24" y="14" width="272" height="132" stroke={lines} strokeWidth="1.2" />
      <line x1="160" y1="14" x2="160" y2="146" stroke={lines} strokeWidth="1" />
      <circle cx="160" cy="80" r="28" stroke={lines} strokeWidth="1" />
      <circle cx="160" cy="80" r="2.5" fill={lines} />
      <rect x="24" y="46" width="50" height="68" stroke={lines} strokeWidth="1" />
      <rect x="24" y="62" width="20" height="36" stroke={lines} strokeWidth="1" />
      <rect x="14" y="68" width="10" height="24" stroke={lines} strokeWidth="1" />
      <rect x="246" y="46" width="50" height="68" stroke={lines} strokeWidth="1" />
      <rect x="276" y="62" width="20" height="36" stroke={lines} strokeWidth="1" />
      <rect x="296" y="68" width="10" height="24" stroke={lines} strokeWidth="1" />
      <path d="M24 22 A8 8 0 0 1 32 14"     stroke={lines} strokeWidth="1" />
      <path d="M288 14 A8 8 0 0 0 296 22"   stroke={lines} strokeWidth="1" />
      <path d="M32 146 A8 8 0 0 1 24 138"   stroke={lines} strokeWidth="1" />
      <path d="M296 138 A8 8 0 0 1 288 146" stroke={lines} strokeWidth="1" />
      <circle cx="68"  cy="80" r="2" fill={lines} />
      <circle cx="252" cy="80" r="2" fill={lines} />
      {featured && <rect width="320" height="160" fill="rgba(16,185,129,0.04)" />}
    </svg>
  );
}
