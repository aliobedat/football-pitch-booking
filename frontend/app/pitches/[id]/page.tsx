'use client';

import { useState, useEffect } from 'react';
import dynamic from 'next/dynamic';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import api from '@/lib/api';
import type { Pitch } from '@/lib/types';
import Navbar from '@/components/Navbar';
import BookingForm from './BookingForm';
import { MapPin, Star, Users, Zap, ArrowRight } from 'lucide-react';

// ── Lazy-load the map — Maps JS is client-only and heavy ─────────────────────
const PitchMap = dynamic(() => import('@/components/PitchMap'), { ssr: false });

// ─── Helpers ──────────────────────────────────────────────────────────────────

const SURFACE_LABEL: Record<string, string> = {
  artificial_grass: 'عشبية صناعية',
  artificial_turf:  'عشبية صناعية',
  natural_grass:    'عشبية طبيعية',
  futsal_court:     'ملعب فوتسال',
};

// ─── Sub-components ───────────────────────────────────────────────────────────

function PitchDiagram({ hue, featured }: { hue: string; featured: boolean }) {
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

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function PitchDetailPage() {
  const { id } = useParams<{ id: string }>();

  const [pitch,     setPitch]     = useState<Pitch | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error,     setError]     = useState<string | null>(null);

  useEffect(() => {
    api.get(`/pitches/${id}`)
      .then(res  => setPitch(res.data.data))
      .catch(()  => setError('تعذّر تحميل بيانات الملعب'))
      .finally(() => setIsLoading(false));
  }, [id]);

  if (isLoading) {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  if (error || !pitch) {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex flex-col items-center justify-center gap-4">
        <p className="text-white/50 text-[15px]">{error ?? 'الملعب غير موجود'}</p>
        <Link href="/pitches"
          className="text-[12px] text-emerald-500 hover:text-emerald-400 transition-colors underline underline-offset-2">
          العودة إلى الملاعب
        </Link>
      </div>
    );
  }

  const hasCoords = pitch.lat !== 0 || pitch.lng !== 0;

  return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">

      <Navbar />

      {/* ── Hero Banner ───────────────────────────────────────────────────── */}
      <div className="relative w-full h-64 sm:h-80 overflow-hidden">
        <PitchDiagram hue={pitch.pitchHue} featured={pitch.isFeatured} />
        <div className="absolute inset-0 bg-gradient-to-t from-[#0d0f0e] via-[#0d0f0e]/50 to-transparent" />

        {pitch.isFeatured && (
          <div className="absolute top-5 end-5 z-10">
            <span className="flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[10px] font-bold tracking-wide bg-emerald-500/20 border border-emerald-500/30 text-emerald-400">
              <Zap size={9} aria-hidden="true" />
              ملعب مميّز
            </span>
          </div>
        )}

        <div className="absolute bottom-0 inset-x-0 px-6 pb-7 max-w-7xl mx-auto">
          <div className="flex items-center gap-1.5 text-[11px] text-white/35 mb-2.5">
            <Link href="/pitches" className="hover:text-emerald-400 transition-colors duration-150">
              الملاعب
            </Link>
            <ArrowRight size={10} className="rotate-180" aria-hidden="true" />
            <span className="text-white/55">{pitch.name}</span>
          </div>
          <h1 className="text-3xl sm:text-[40px] font-bold text-[#f0efe8] tracking-tight leading-tight">
            {pitch.name}
          </h1>
          <div className="flex items-center gap-1.5 mt-2 text-white/40">
            <MapPin size={12} aria-hidden="true" />
            <span className="text-[12px]">{pitch.neighborhood}، عمّان</span>
          </div>
        </div>
      </div>

      {/* ── Main Content ─────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 pt-8 pb-20">
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-8 items-start">

          {/* ── Details column ── */}
          <section className="lg:col-span-2 flex flex-col gap-5" aria-label="تفاصيل الملعب">

            {/* Stats grid */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">

              {/* Rating card — null-safe */}
              <div className="flex flex-col gap-1.5 p-4 rounded-xl bg-[#141715] border border-white/[0.07]">
                <Star
                  size={13}
                  className={pitch.rating !== null ? 'text-amber-400' : 'text-white/20'}
                  fill={pitch.rating !== null ? 'currentColor' : 'none'}
                  aria-hidden="true"
                />
                {pitch.rating !== null ? (
                  <>
                    <span className="text-[22px] font-bold text-[#f0efe8] leading-none">
                      {pitch.rating.toFixed(1)}
                    </span>
                    <span className="text-[10px] text-white/30">{pitch.reviewsCount} تقييم</span>
                  </>
                ) : (
                  <>
                    <span className="text-[15px] font-bold text-white/30 leading-none">جديد</span>
                    <span className="text-[10px] text-white/20">لا تقييمات بعد</span>
                  </>
                )}
              </div>

              {/* Price */}
              <div className="flex flex-col gap-1.5 p-4 rounded-xl bg-[#141715] border border-white/[0.07]">
                <span className="text-[10px] font-semibold text-emerald-500 uppercase tracking-wide">
                  السعر
                </span>
                <div className="flex items-baseline gap-1">
                  <span className="text-[22px] font-bold text-[#f0efe8] leading-none">
                    {pitch.pricePerHour}
                  </span>
                  <span className="text-[11px] font-semibold text-emerald-500">د.أ</span>
                </div>
                <span className="text-[10px] text-white/30">للساعة الواحدة</span>
              </div>

              {/* Format */}
              <div className="flex flex-col gap-1.5 p-4 rounded-xl bg-[#141715] border border-white/[0.07]">
                <Users size={13} className="text-emerald-500" aria-hidden="true" />
                <span className="text-[19px] font-bold text-[#f0efe8] leading-none">
                  {pitch.format}
                </span>
                <span className="text-[10px] text-white/30">نوع اللعب</span>
              </div>

              {/* Surface */}
              <div className="flex flex-col gap-1.5 p-4 rounded-xl bg-[#141715] border border-white/[0.07]">
                <span className="text-[10px] font-semibold text-emerald-500 uppercase tracking-wide">
                  الأرضية
                </span>
                <span className="text-[13px] font-bold text-[#f0efe8] leading-snug">
                  {SURFACE_LABEL[pitch.surface] ?? pitch.surface}
                </span>
                <span className="text-[10px] text-white/30">نوع السطح</span>
              </div>
            </div>

            {/* Amenities */}
            {pitch.amenities.length > 0 && (
              <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
                <h2 className="text-[13px] font-bold text-[#f0efe8] mb-3.5">المرافق والخدمات</h2>
                <div className="flex flex-wrap gap-2">
                  {pitch.amenities.map(a => (
                    <span key={a}
                      className="px-3 py-1.5 rounded-full text-[11px] font-medium text-emerald-400 bg-emerald-500/[0.07] border border-emerald-500/20">
                      {a}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {/* Description */}
            {pitch.description && (
              <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
                <h2 className="text-[13px] font-bold text-[#f0efe8] mb-3">عن الملعب</h2>
                <p className="text-[13px] text-white/50 leading-relaxed">{pitch.description}</p>
              </div>
            )}

            {/* Location + interactive map */}
            <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
              <h2 className="text-[13px] font-bold text-[#f0efe8] mb-3.5">الموقع</h2>
              <div className="flex items-center gap-2 text-white/40 mb-4">
                <MapPin size={13} className="text-emerald-500 shrink-0" aria-hidden="true" />
                <span className="text-[12px]">{pitch.neighborhood}، عمّان، الأردن</span>
              </div>
              {hasCoords && (
                <div className="h-56 rounded-xl overflow-hidden">
                  <PitchMap lat={pitch.lat} lng={pitch.lng} zoom={15} />
                </div>
              )}
            </div>
          </section>

          {/* ── Booking form column — sticky on desktop ── */}
          <aside className="lg:col-span-1 lg:sticky lg:top-24" aria-label="نموذج الحجز">
            <BookingForm pitchId={pitch.id} pricePerHour={pitch.pricePerHour} />
          </aside>

        </div>
      </main>

      {/* ── Footer ───────────────────────────────────────────────────────── */}
      <footer className="border-t border-white/[0.05] py-8">
        <div className="max-w-7xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            {['الخصوصية', 'الشروط', 'تواصل معنا'].map((item, i) => (
              <Link key={item}
                href={`/${(['privacy', 'terms', 'contact'] as const)[i]}`}
                className="text-[11px] text-white/20 hover:text-white/45 transition-colors duration-150">
                {item}
              </Link>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-white/20">© 2026 ملاعب. جميع الحقوق محفوظة.</span>
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500/50" />
          </div>
        </div>
      </footer>

    </div>
  );
}
