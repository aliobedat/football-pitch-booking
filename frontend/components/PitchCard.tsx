'use client';

import { useState } from 'react';
import Link from 'next/link';
import Image from 'next/image';
import { MapPin, Star } from 'lucide-react';

// ─── Data contract ────────────────────────────────────────────────────────────
// Mirrors the expected Go backend response shape.
// Build dummy data against this; swapping to the real API is a one-line change.

export type Pitch = {
  id: string;
  name: string;
  city: string;
  area: string;
  imageUrl: string | null;
  pricePerHour: number;
  rating: number;
  reviewsCount: number;
  size: '5x5' | '7x7' | '11x11';
  surface: 'عشب صناعي' | 'عشب طبيعي';
  isIndoor: boolean;
  hasLighting: boolean;
  hasParking: boolean;
  availabilityToday: 'available' | 'limited' | 'full';
  nextAvailableSlot?: string;
  distanceKm?: number;
};

// ─── Availability config ──────────────────────────────────────────────────────

const AVAIL_STYLE = {
  available: {
    pill:  'bg-emerald-500/15 border-emerald-500/30 text-emerald-400',
    dot:   'bg-emerald-400',
    label: 'متاح اليوم',
  },
  limited: {
    pill:  'bg-amber-500/15 border-amber-500/30 text-amber-400',
    dot:   'bg-amber-400',
    label: 'مواعيد محدودة',
  },
  full: {
    pill:  'bg-red-500/15 border-red-500/30 text-red-400',
    dot:   'bg-red-500',
    label: 'ممتلئ اليوم',
  },
} as const;

// ─── Pitch silhouette — branded fallback when imageUrl is null / fails ────────

function PitchSilhouette() {
  return (
    <svg
      viewBox="0 0 240 140"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className="w-24 h-14"
      aria-hidden
    >
      <rect x="8"   y="8"   width="224" height="124" stroke="white" strokeOpacity="0.12" strokeWidth="1.5" rx="3" />
      <line x1="120" y1="8" x2="120" y2="132"        stroke="white" strokeOpacity="0.12" strokeWidth="1"   />
      <circle cx="120" cy="70" r="26"                 stroke="white" strokeOpacity="0.12" strokeWidth="1"   />
      <circle cx="120" cy="70" r="2.5" fill="white"   fillOpacity="0.12" />
      {/* Left box */}
      <rect x="8"   y="38"  width="38"  height="64" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <rect x="8"   y="51"  width="16"  height="38" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      {/* Right box */}
      <rect x="194" y="38"  width="38"  height="64" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <rect x="216" y="51"  width="16"  height="38" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      {/* Penalty spots */}
      <circle cx="52"  cy="70" r="2" fill="white" fillOpacity="0.12" />
      <circle cx="188" cy="70" r="2" fill="white" fillOpacity="0.12" />
      {/* Corner arcs */}
      <path d="M8  16 A8 8 0 0 1 16 8"   stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <path d="M224 8  A8 8 0 0 1 232 16" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <path d="M16 132 A8 8 0 0 1 8  124" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
      <path d="M232 124 A8 8 0 0 1 224 132" stroke="white" strokeOpacity="0.12" strokeWidth="1" />
    </svg>
  );
}

// ─── Component ────────────────────────────────────────────────────────────────
//
// The entire card is a single <Link> (renders as <a>).
// The "احجز الآن" CTA is a <span> — NOT a nested <button> or <a> — so there
// are zero nested interactive elements, satisfying HTML/a11y rules.
// Hover state is driven by the `group` class on the Link.

export default function PitchCard({ pitch }: { pitch: Pitch }) {
  const [imgLoaded, setImgLoaded] = useState(false);
  const [imgError,  setImgError]  = useState(false);

  const avail     = AVAIL_STYLE[pitch.availabilityToday];
  const showImage = !!pitch.imageUrl && !imgError;

  return (
    <Link
      href={`/pitches/${pitch.id}`}
      className={[
        'group block rounded-2xl overflow-hidden',
        'bg-[#141715] border border-gray-800',
        'hover:border-gray-700 hover:-translate-y-0.5',
        'hover:shadow-lg hover:shadow-black/40',
        'transition-all duration-300 ease-out',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
        'focus-visible:ring-offset-2 focus-visible:ring-offset-[#0d0f0e]',
        'active:scale-[0.98]',
      ].join(' ')}
    >
      <article>

        {/* ── Image container ─────────────────────────────────────────────── */}
        <div className="relative aspect-video overflow-hidden rounded-t-2xl bg-[#0a0c0b]">

          {/* Shimmer skeleton — visible while the real image loads */}
          {showImage && !imgLoaded && (
            <div className="absolute inset-0 animate-pulse bg-gradient-to-br from-white/[0.03] via-white/[0.07] to-white/[0.03]" />
          )}

          {/* Branded fallback — shown when imageUrl is null OR onError fires */}
          {!showImage && (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-2 bg-[#0a0c0b]">
              <PitchSilhouette />
              <span className="text-[10px] text-white/15 font-medium tracking-wide px-4 truncate max-w-full">
                {pitch.name}
              </span>
            </div>
          )}

          {/* Real image — fades in once loaded, scales on card hover */}
          {pitch.imageUrl && !imgError && (
            <Image
              src={pitch.imageUrl}
              alt={`${pitch.name} — ${pitch.city}`}
              fill
              className={[
                'object-cover transition-all duration-300',
                'group-hover:scale-105',
                imgLoaded ? 'opacity-100' : 'opacity-0',
              ].join(' ')}
              sizes="(max-width: 768px) 100vw, (max-width: 1024px) 50vw, 33vw"
              onLoad={() => setImgLoaded(true)}
              onError={() => setImgError(true)}
            />
          )}

          {/* Gradient scrim — keeps overlay badges legible over bright images */}
          {showImage && imgLoaded && (
            <div className="absolute inset-0 bg-gradient-to-t from-black/55 via-black/10 to-transparent" />
          )}

          {/* ── Availability badge — top-end (right in RTL layout) ── */}
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
                أقرب موعد {pitch.nextAvailableSlot}
              </span>
            )}
          </div>

          {/* ── Rating + review count — top-start (left in RTL layout) ── */}
          <div className="absolute top-3 start-3 z-10">
            <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-semibold bg-black/55 backdrop-blur-sm border border-white/10 text-amber-400">
              <Star size={9} fill="currentColor" aria-hidden />
              {pitch.rating.toFixed(1)}
              <span className="text-white/35 font-normal">({pitch.reviewsCount})</span>
            </span>
          </div>
        </div>

        {/* ── Card body ───────────────────────────────────────────────────── */}
        <div className="p-4 flex flex-col gap-3">

          {/* Name — single line, truncated */}
          <h3 className="text-[15px] font-bold text-[#f0efe8] tracking-tight leading-snug truncate">
            {pitch.name}
          </h3>

          {/* Location + distance */}
          <div className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 text-white/40 min-w-0">
              <MapPin size={11} className="shrink-0" aria-hidden />
              <span className="text-[11px] truncate">{pitch.city} — {pitch.area}</span>
            </div>
            {pitch.distanceKm !== undefined && (
              <span className="text-[10px] text-white/30 font-mono shrink-0">
                {pitch.distanceKm} كم
              </span>
            )}
          </div>

          {/* Spec pills */}
          <div className="flex flex-wrap gap-1.5" aria-label="مواصفات الملعب">
            <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
              {pitch.size.replace('x', '×')}
            </span>
            <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
              {pitch.surface}
            </span>
            <span className="px-2 py-0.5 rounded-full text-[10px] font-semibold bg-white/[0.05] border border-white/[0.09] text-white/50">
              {pitch.isIndoor ? 'مغطّى' : 'مكشوف'}
            </span>
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

            {/* Price */}
            <div>
              <div className="flex items-baseline gap-1">
                <span className="text-[22px] font-bold text-[#f0efe8] leading-none tracking-tight">
                  {pitch.pricePerHour}
                </span>
                <span className="text-[12px] font-bold text-emerald-500">د.أ</span>
              </div>
              <p className="text-[10px] text-white/25 mt-0.5">للساعة</p>
            </div>

            {/* CTA — styled <span>, NOT a nested <button> or <a> */}
            <span className={[
              'flex items-center px-4 py-2 rounded-xl',
              'text-[11px] font-bold tracking-wide',
              'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
              'group-hover:bg-[#1a6b52] group-hover:text-emerald-300',
              'group-hover:border-emerald-500/40',
              'group-hover:shadow-[0_0_20px_rgba(16,185,129,0.12)]',
              'transition-all duration-200',
            ].join(' ')}>
              احجز الآن
            </span>

          </div>
        </div>
      </article>
    </Link>
  );
}
