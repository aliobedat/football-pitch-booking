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
import { MapPin, ArrowRight, ChevronDown } from 'lucide-react';

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

// ─── Accordion row (layout-only wrapper) ──────────────────────────────────────
// Collapsed-by-default disclosure for the lower informational sections. Pure
// presentation + one local boolean — content is the SAME JSX that previously
// rendered inline and stays MOUNTED while collapsed (CSS-hidden only), so
// children keep their pre-refactor lifecycle: ReviewSection and PitchMap still
// mount and fetch at page load exactly as before.

function AccordionRow({ title, preview, children }: {
  title:    string;
  preview:  string;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-xl bg-[#141715] border border-white/[0.07] overflow-hidden">
      <button
        type="button"
        onClick={() => setOpen(v => !v)}
        aria-expanded={open}
        className="w-full flex items-center gap-3 px-5 py-4 text-start cursor-pointer hover:bg-white/[0.02] transition-colors duration-150"
      >
        <div className="flex-1 min-w-0">
          <h2 className="text-[13px] font-bold text-[#f0efe8]">{title}</h2>
          {!open && (
            <p className="text-[11px] text-white/30 mt-0.5 truncate">{preview}</p>
          )}
        </div>
        <ChevronDown
          size={16}
          aria-hidden="true"
          className={`shrink-0 text-white/30 transition-transform duration-200 ${open ? 'rotate-180' : ''}`}
        />
      </button>
      <div className={open ? 'px-5 pb-5' : 'hidden'}>{children}</div>
    </div>
  );
}

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
      <main className="max-w-7xl mx-auto px-6 pt-5 pb-20">

        {/* ── Compact info chips — replace the old 4-card stats grid. Values
            follow the SELECTED pitch (same dynamic sources as before); each
            chip stays one-line internally, the row wraps on narrow screens. ── */}
        <div className="flex flex-wrap gap-2 mb-6">
          {/* Price — stronger green highlight */}
          <span className="whitespace-nowrap px-3 py-1.5 rounded-full text-[12px] font-bold text-emerald-300 bg-emerald-500/15 border border-emerald-500/30">
            {pitch.pricePerHour} د.أ/الساعة
          </span>
          {/* Format */}
          <span className="whitespace-nowrap px-3 py-1.5 rounded-full text-[12px] font-medium text-white/60 bg-white/[0.04] border border-white/[0.08]">
            {pitch.format}
          </span>
          {/* Surface */}
          <span className="whitespace-nowrap px-3 py-1.5 rounded-full text-[12px] font-medium text-white/60 bg-white/[0.04] border border-white/[0.08]">
            {SURFACE_LABEL[pitch.surface] ?? pitch.surface}
          </span>
          {/* Rating — null-safe, per-pitch (venue aggregate is a followup) */}
          <span className="whitespace-nowrap px-3 py-1.5 rounded-full text-[12px] font-medium text-amber-400/90 bg-white/[0.04] border border-white/[0.08]">
            {pitch.rating !== null
              ? `★ ${pitch.rating.toFixed(1)} · ${pitch.reviewsCount} تقييم`
              : '★ جديد'}
          </span>
        </div>

        {/* Mobile: booking card first (directly after the chips), accordions
            below. Desktop (lg+): unchanged two-column split — accordions in the
            wide column, booking sticky in the side column — via order utilities. */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 lg:gap-8 items-start">

          {/* ── Booking form — first on mobile, sticky side column on desktop ── */}
          <aside className="order-1 lg:order-2 lg:col-span-1 lg:sticky lg:top-24 w-full min-w-0" aria-label="نموذج الحجز">
            <BookingForm
              pitchId={pitch.id}
              pricePerHour={pitch.pricePerHour}
              pitchOptions={pitchOptions}
              onPitchChange={setSelId}
            />
          </aside>

          {/* ── Lower informational sections — collapsed accordions ── */}
          <section className="order-2 lg:order-1 lg:col-span-2 flex flex-col gap-3 w-full min-w-0" aria-label="تفاصيل الملعب">

            {/* About — place-level description + per-pitch amenities (moved in,
                content unchanged). Rendered only when there is anything to show. */}
            {(venue.description || pitch.amenities.length > 0) && (
              <AccordionRow
                title="عن الملعب"
                preview={venue.description || 'المرافق والخدمات'}
              >
                {venue.description && (
                  <p className="text-[13px] text-white/50 leading-relaxed">{venue.description}</p>
                )}
                {/* Amenities — per selected pitch */}
                {pitch.amenities.length > 0 && (
                  <div className={venue.description ? 'mt-4' : ''}>
                    <h3 className="text-[12px] font-bold text-[#f0efe8] mb-2.5">المرافق والخدمات</h3>
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
              </AccordionRow>
            )}

            {/* Location + interactive map — place-level */}
            <AccordionRow
              title="الموقع"
              preview={`${venue.neighborhood}، عمّان`}
            >
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
            </AccordionRow>

            {/* Verified reviews — per selected pitch (aggregate view: logged followup) */}
            <AccordionRow
              title="التقييمات"
              preview={pitch.rating !== null
                ? `★ ${pitch.rating.toFixed(1)} · ${pitch.reviewsCount} تقييم`
                : 'لا تقييمات بعد'}
            >
              <ReviewSection pitchId={pitch.id} />
            </AccordionRow>
          </section>

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
