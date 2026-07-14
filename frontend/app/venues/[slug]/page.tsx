'use client';

// Venue page (WO-VENUES Gate 1c-minimal) — the player-facing detail page keyed
// by venue slug. COLLAPSE RULE: a single-pitch venue renders exactly the pitch
// page experience (no selector, no venue vocabulary). Multi-pitch venues add a
// segmented pitch selector inside the booking card, above the date bar; the
// hero/info cards reflect the SELECTED pitch, the venue name stays the title.
// Reuses the pitch page's components (PitchDiagram, PitchMap, ReviewSection,
// BookingForm) — no forks.

import { useState, useEffect, useMemo, Suspense } from 'react';
import dynamic from 'next/dynamic';
import Link from 'next/link';
import { useParams, useSearchParams } from 'next/navigation';
import api from '@/lib/api';
import Navbar from '@/components/Navbar';
import PitchDiagram from '@/components/PitchDiagram';
import BookingForm, { type PitchOption } from '@/app/pitches/[id]/BookingForm';
import ReviewSection from '@/components/reviews/ReviewSection';
import { MapPin, Star, Users, ArrowRight } from 'lucide-react';

const PitchMap = dynamic(() => import('@/components/PitchMap'), { ssr: false });

// ─── Wire types — GET /venues/:slug (Gate 1b PublicVenue payload) ─────────────

interface VenuePitch {
  id:            number;
  label:         string;
  name:          string;
  surface:       string;
  format:        string;
  pricePerHour:  number;
  amenities:     string[];
  pitchHue:      string;
  rating:        number | null;
  reviewsCount:  number;
  image_url:     string;
}

interface VenueData {
  id:              number;
  name:            string;
  slug:            string;
  neighborhood:    string;
  maps_url:        string;
  lat:             number;
  lng:             number;
  description:     string;
  cover_image_url: string;
  rating:          number | null;
  reviewsCount:    number;
  pitches:         VenuePitch[];
}

const SURFACE_LABEL: Record<string, string> = {
  artificial_grass: 'عشبية صناعية',
  artificial_turf:  'عشبية صناعية',
  natural_grass:    'عشبية طبيعية',
  futsal_court:     'ملعب فوتسال',
};

// ─── Page ─────────────────────────────────────────────────────────────────────

function VenuePageContent() {
  const { slug } = useParams<{ slug: string }>();
  const searchParams = useSearchParams();

  const [venue,     setVenue]     = useState<VenueData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error,     setError]     = useState<string | null>(null);
  const [selId,     setSelId]     = useState<number | null>(null);
  const [heroImgError, setHeroImgError] = useState(false);

  useEffect(() => {
    api.get(`/venues/${slug}`)
      .then(res => {
        const v: VenueData = res.data.data;
        setVenue(v);
        // Default selection: ?pitch= when present AND one of this venue's
        // pitches; otherwise the first pitch.
        const wanted = Number(searchParams.get('pitch'));
        const valid  = v.pitches.find(p => p.id === wanted);
        setSelId(valid ? valid.id : v.pitches[0]?.id ?? null);
      })
      .catch(()  => setError('تعذّر تحميل بيانات الملعب'))
      .finally(() => setIsLoading(false));
    // searchParams is read once at load for the initial selection only —
    // switching pitches never navigates.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug]);

  const pitch = useMemo(
    () => venue?.pitches.find(p => p.id === selId) ?? venue?.pitches[0] ?? null,
    [venue, selId],
  );

  // Selector options — sub-line only when format or price differ across pitches.
  const pitchOptions = useMemo<PitchOption[]>(() => {
    if (!venue) return [];
    const differs =
      new Set(venue.pitches.map(p => p.format)).size > 1 ||
      new Set(venue.pitches.map(p => p.pricePerHour)).size > 1;
    return venue.pitches.map(p => ({
      id:    p.id,
      label: p.label || p.name,
      ...(differs ? { subline: `${p.format} · ${p.pricePerHour} د.أ` } : {}),
    }));
  }, [venue]);

  if (isLoading) {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  if (error || !venue || !pitch) {
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

  const heroImage = pitch.image_url || venue.cover_image_url;
  const hasCoords = venue.lat !== 0 || venue.lng !== 0;

  return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">

      <Navbar />

      {/* ── Hero Banner ───────────────────────────────────────────────────── */}
      <div className="relative w-full h-64 sm:h-80 overflow-hidden">
        {heroImage && !heroImgError ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={heroImage}
            alt={venue.name}
            className="absolute inset-0 w-full h-full object-cover"
            onError={() => setHeroImgError(true)}
          />
        ) : (
          <PitchDiagram hue={pitch.pitchHue} featured={false} />
        )}
        <div className="absolute inset-0 bg-gradient-to-t from-[#0d0f0e] via-[#0d0f0e]/50 to-transparent" />

        <div className="absolute bottom-0 inset-x-0 px-6 pb-7 max-w-7xl mx-auto">
          <div className="flex items-center gap-1.5 text-[11px] text-white/35 mb-2.5">
            <Link href="/pitches" className="hover:text-emerald-400 transition-colors duration-150">
              الملاعب
            </Link>
            <ArrowRight size={10} className="rotate-180" aria-hidden="true" />
            <span className="text-white/55">{venue.name}</span>
          </div>
          <h1 className="text-3xl sm:text-[40px] font-bold text-[#f0efe8] tracking-tight leading-tight">
            {venue.name}
          </h1>
          <div className="flex items-center gap-1.5 mt-2 text-white/40">
            <MapPin size={12} aria-hidden="true" />
            <span className="text-[12px]">{venue.neighborhood}، عمّان</span>
          </div>
        </div>
      </div>

      {/* ── Main Content ─────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 pt-8 pb-20">
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-8 items-start">

          {/* ── Details column ── */}
          <section className="lg:col-span-2 flex flex-col gap-5" aria-label="تفاصيل الملعب">

            {/* Stats grid — per-pitch values follow the SELECTED pitch */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">

              {/* Rating card — null-safe, per-pitch (venue aggregate is a followup) */}
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

              {/* Price — always the selected pitch's price, never a range */}
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

            {/* Amenities — per selected pitch */}
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

            {/* Description — place-level */}
            {venue.description && (
              <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
                <h2 className="text-[13px] font-bold text-[#f0efe8] mb-3">عن الملعب</h2>
                <p className="text-[13px] text-white/50 leading-relaxed">{venue.description}</p>
              </div>
            )}

            {/* Location + interactive map — place-level */}
            <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
              <h2 className="text-[13px] font-bold text-[#f0efe8] mb-3.5">الموقع</h2>
              <div className="flex items-center gap-2 text-white/40 mb-4">
                <MapPin size={13} className="text-emerald-500 shrink-0" aria-hidden="true" />
                <span className="text-[12px]">{venue.neighborhood}، عمّان، الأردن</span>
              </div>
              {venue.maps_url && (
                <a
                  href={venue.maps_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1.5 mb-4 text-[12px] font-semibold text-emerald-400 hover:text-emerald-300 transition-colors duration-150"
                >
                  <MapPin size={12} aria-hidden="true" />
                  افتح في خرائط Google
                </a>
              )}
              {hasCoords && process.env.NEXT_PUBLIC_GOOGLE_MAPS_API_KEY && (
                <div className="h-56 rounded-xl overflow-hidden">
                  <PitchMap lat={venue.lat} lng={venue.lng} zoom={15} />
                </div>
              )}
            </div>

            {/* Verified reviews — per selected pitch (aggregate view: logged followup) */}
            <div className="p-5 rounded-xl bg-[#141715] border border-white/[0.07]">
              <ReviewSection pitchId={pitch.id} />
            </div>
          </section>

          {/* ── Booking form column — sticky on desktop ── */}
          <aside className="lg:col-span-1 lg:sticky lg:top-24" aria-label="نموذج الحجز">
            <BookingForm
              pitchId={pitch.id}
              pricePerHour={pitch.pricePerHour}
              pitchOptions={pitchOptions}
              onPitchChange={setSelId}
            />
          </aside>

        </div>
      </main>

      {/* ── Footer ───────────────────────────────────────────────────────── */}
      <footer className="border-t border-white/[0.05] py-8">
        <div className="max-w-7xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            {['الخصوصية'].map((item, i) => (
              <Link key={item}
                href={`/${(['privacy'] as const)[i]}`}
                className="text-[11px] text-white/20 hover:text-white/45 transition-colors duration-150">
                {item}
              </Link>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-white/20">© 2026 مرمى. جميع الحقوق محفوظة.</span>
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500/50" />
          </div>
        </div>
      </footer>

    </div>
  );
}

// useSearchParams requires a Suspense boundary at prerender time.
export default function VenuePage() {
  return (
    <Suspense fallback={
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    }>
      <VenuePageContent />
    </Suspense>
  );
}
