'use client';

import { useState } from 'react';
import Link from 'next/link';
import Image from 'next/image';
import { MapPin, Star } from 'lucide-react';
import type { Pitch } from '@/lib/types';

// Re-export so existing `import PitchCard, { type Pitch }` imports keep resolving.
// New code should import from @/lib/types directly.
export type { Pitch };

// ─── Helpers ─────────────────────────────────────────────────────────────────

const SURFACE_LABEL: Record<string, string> = {
  artificial_grass: 'عشب صناعي',
  artificial_turf:  'عشب صناعي',
  natural_grass:    'عشب طبيعي',
  futsal_court:     'فوتسال',
};
function surfaceDisplay(s: string): string { return SURFACE_LABEL[s] ?? s; }

function formatSlot12(slot: string): string {
  const [hStr = '0', mStr = '0'] = slot.split(':');
  const h    = parseInt(hStr, 10);
  const m    = parseInt(mStr, 10);
  const hr12 = (h === 0 || h === 12) ? 12 : h > 12 ? h - 12 : h;
  return `${String(hr12).padStart(2, '0')}:${String(m).padStart(2, '0')} ${h < 12 ? 'ص' : 'م'}`;
}

// ─── Availability config ──────────────────────────────────────────────────────

const AVAIL_STYLE = {
  available: { pill: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400', dot: 'bg-emerald-400', label: 'متاح اليوم'      },
  limited:   { pill: 'bg-amber-500/15 border-amber-500/30 text-amber-400',       dot: 'bg-amber-400',   label: 'مواعيد محدودة' },
  full:      { pill: 'bg-red-500/15 border-red-500/30 text-red-400',             dot: 'bg-red-500',     label: 'ممتلئ اليوم'  },
} as const;

// ─── Pitch silhouette — branded fallback ─────────────────────────────────────

function PitchSilhouette() {
  return (
    <svg viewBox="0 0 240 140" fill="none" xmlns="http://www.w3.org/2000/svg"
      className="w-24 h-14" aria-hidden>
      <rect x="8" y="8" width="224" height="124" stroke="white" strokeOpacity="0.12" strokeWidth="1.5" rx="3" />
      <line x1="120" y1="8" x2="120" y2="132" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <circle cx="120" cy="70" r="26" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <circle cx="120" cy="70" r="2.5" fill="white" fillOpacity="0.12" />
      <rect x="8"   y="38" width="38" height="64" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <rect x="8"   y="51" width="16" height="38" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <rect x="194" y="38" width="38" height="64" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <rect x="216" y="51" width="16" height="38" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <circle cx="52"  cy="70" r="2" fill="white" fillOpacity="0.12" />
      <circle cx="188" cy="70" r="2" fill="white" fillOpacity="0.12" />
    </svg>
  );
}

// ─── Component ────────────────────────────────────────────────────────────────
//
// Single <Link> wraps the whole card. CTA "احجز الآن" is a <span> — not a
// nested <a>/<button> — zero nested interactive elements.

// WO-1C-PAYLOAD: the listing now feeds venue-aggregated rows through this same
// card. pitchCount > 1 adds the «N ملاعب» chip; priceFrom renders «من {price}»
// (price varies across the venue). Both absent → byte-identical to the classic
// pitch card (collapse rule).
export default function PitchCard({ pitch, pitchCount, priceFrom }: {
  pitch: Pitch;
  pitchCount?: number;
  priceFrom?: boolean;
}) {
  const [imgLoaded, setImgLoaded] = useState(false);
  const [imgError,  setImgError]  = useState(false);

  const displayArea  = pitch.area ?? pitch.neighborhood;
  // The backend serializes the uploaded Cloudinary URL as snake_case `image_url`
  // (see backend/internal/data/pitches.go). `imageUrl` is a legacy camelCase
  // alias the API never populates — prefer image_url, fall back for safety.
  const displayImage = pitch.image_url ?? pitch.imageUrl ?? null;
  const avail        = pitch.availabilityToday ? AVAIL_STYLE[pitch.availabilityToday] : null;
  const showImage    = !!displayImage && !imgError;

  return (
    <Link
      // WO-VENUES Gate 1c: pitch links point at the venue URL (pre-selected).
      // WO-1C-PAYLOAD: a venue-aggregated row (pitchCount prop present) links
      // to the bare venue URL — its `id` is the VENUE id, so no ?pitch= param
      // (the venue page defaults sensibly). venue_slug is always present
      // post-034; the /pitches/:id redirect covers any row without one.
      href={pitchCount !== undefined
        ? `/venues/${pitch.venue_slug}`
        : pitch.venue_slug ? `/venues/${pitch.venue_slug}?pitch=${pitch.id}` : `/pitches/${pitch.id}`}
      className={[
        'group block rounded-2xl overflow-hidden',
        'bg-[#141715] border border-gray-800',
        'hover:border-gray-700 hover:-translate-y-0.5 hover:shadow-lg hover:shadow-black/40',
        'transition-all duration-300 ease-out',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
        'focus-visible:ring-offset-2 focus-visible:ring-offset-[#0d0f0e]',
        'active:scale-[0.98]',
      ].join(' ')}
    >
      <article>

        {/* ── Image container ───────────────────────────────────────────── */}
        <div className="relative aspect-video overflow-hidden rounded-t-2xl bg-[#0a0c0b]">

          {showImage && !imgLoaded && (
            <div className="absolute inset-0 animate-pulse bg-gradient-to-br from-white/[0.03] via-white/[0.07] to-white/[0.03]" />
          )}

          {!showImage && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-[#0a0c0b]">
              <PitchSilhouette />
              <span className="text-[10px] text-white/15 font-medium tracking-wide px-4 truncate max-w-full">
                {pitch.name}
              </span>
            </div>
          )}

          {displayImage && !imgError && (
            <Image
              src={displayImage}
              alt={`${pitch.name} — ${displayArea}`}
              fill
              className={[
                'object-cover transition-all duration-300 group-hover:scale-105',
                imgLoaded ? 'opacity-100' : 'opacity-0',
              ].join(' ')}
              sizes="(max-width: 768px) 100vw, (max-width: 1024px) 50vw, 33vw"
              onLoad={() => setImgLoaded(true)}
              onError={() => setImgError(true)}
            />
          )}

          {showImage && imgLoaded && (
            <div className="absolute inset-0 bg-gradient-to-t from-black/55 via-black/10 to-transparent" />
          )}

          {/* Availability badge */}
          {avail && (
            <div className="absolute top-3 end-3 z-10 flex flex-col items-end gap-1.5">
              <span className={[
                'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full',
                'text-[10px] font-bold border backdrop-blur-sm',
                avail.pill,
              ].join(' ')}>
                <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${avail.dot}`} aria-hidden />
                {avail.label}
              </span>
              {pitch.nextAvailableSlot && pitch.availabilityToday !== 'full' && (
                <span className="text-[9px] text-white/55 bg-black/50 backdrop-blur-sm px-2 py-0.5 rounded-full border border-white/10">
                  أقرب موعد {formatSlot12(pitch.nextAvailableSlot)}
                </span>
              )}
            </div>
          )}

          {/* Rating badge — real data; NEVER shows "0.0" */}
          <div className="absolute top-3 start-3 z-10">
            {pitch.rating !== null ? (
              <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-semibold bg-black/55 backdrop-blur-sm border border-white/10 text-amber-400">
                <Star size={9} fill="currentColor" aria-hidden />
                {pitch.rating.toFixed(1)}
                <span className="text-white/35 font-normal">({pitch.reviewsCount})</span>
              </span>
            ) : (
              <span className="inline-flex items-center px-2.5 py-1 rounded-full text-[10px] font-semibold bg-black/55 backdrop-blur-sm border border-white/10 text-white/35">
                جديد
              </span>
            )}
          </div>
        </div>

        {/* ── Card body ─────────────────────────────────────────────────── */}
        <div className="p-4 flex flex-col gap-3">

          <h3 className="text-[15px] font-bold text-[#f0efe8] tracking-tight leading-snug truncate">
            {pitch.name}
          </h3>

          {/* Location */}
          <div className="flex items-center gap-1.5 text-white/40 min-w-0">
            <MapPin size={11} className="shrink-0" aria-hidden />
            <span className="text-[11px] truncate">
              {pitch.city ? `${pitch.city} — ` : ''}{displayArea}
            </span>
          </div>

          {/* Spec pills — format/surface render only when known (uniform across
              a venue's pitches, or a plain pitch row; mixed venues omit them). */}
          <div className="flex flex-wrap gap-1.5" aria-label="مواصفات الملعب">
            {pitchCount !== undefined && pitchCount > 1 && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold text-white/40 bg-white/[0.05] border border-white/[0.08]">
                {pitchCount} ملاعب
              </span>
            )}
            {(pitch.size || pitch.format) && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
                {pitch.size ? pitch.size.replace('x', '×') : pitch.format}
              </span>
            )}
            {pitch.surface && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
                {surfaceDisplay(pitch.surface)}
              </span>
            )}
            {pitch.isIndoor !== undefined && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
                {pitch.isIndoor ? 'مغطّى' : 'مكشوف'}
              </span>
            )}
            {pitch.hasLighting && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-amber-500/10 border border-amber-500/20 text-amber-500/80">
                إنارة
              </span>
            )}
            {pitch.hasParking && (
              <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
                موقف
              </span>
            )}
          </div>

          <div className="h-px bg-white/[0.05]" />

          {/* Price + CTA */}
          <div className="flex items-center justify-between">
            <div>
              <div className="flex items-baseline gap-1">
                {priceFrom && <span className="text-[11px] font-semibold text-white/40">من</span>}
                <span className="text-[22px] font-bold text-[#f0efe8] leading-none tracking-tight">
                  {pitch.pricePerHour}
                </span>
                <span className="text-[12px] font-bold text-emerald-500">د.أ</span>
              </div>
              <p className="text-[10px] text-white/25 mt-0.5">للساعة</p>
            </div>
            <span className={[
              'flex items-center px-4 py-2 rounded-xl text-[11px] font-bold tracking-wide',
              'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
              'group-hover:bg-[#1a6b52] group-hover:text-emerald-300 group-hover:border-emerald-500/40',
              'group-hover:shadow-[0_0_20px_rgba(16,185,129,0.12)] transition-all duration-200',
            ].join(' ')}>
              احجز الآن
            </span>
          </div>
        </div>
      </article>
    </Link>
  );
}
