'use client';

import { useState, useEffect, useMemo, useCallback } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import { LocationProvider, useLocation } from '@/context/LocationContext';
import { haversineKm } from '@/lib/distance';
import type { Pitch } from '@/lib/types';
import { Search, SlidersHorizontal, ChevronDown, RefreshCw, MapPin, Loader2 } from 'lucide-react';
import Navbar from '@/components/Navbar';
import PitchCard from '@/components/PitchCard';
import { useAvailabilitySearch, AvailabilitySearchBar, AvailabilityResults } from '@/components/AvailabilitySearch';

// ─── Filter / sort types ──────────────────────────────────────────────────────

type FilterValue =
  | 'all'
  | 'عين الباشا' | 'خلدا' | 'الجبيهة' | 'شفا بدران'
  | '5x5' | '7x7' | '11x11'
  | 'available_now';

interface FilterChip {
  label: string;
  value: FilterValue;
  type: 'all' | 'area' | 'size' | 'availability';
}

// SortKey is defined as a union so that 'distance' (nearest) can be added
// next phase without any refactor — just add it to SORT_OPTIONS.
type SortKey = 'price_asc' | 'price_desc' | 'rating';

const FILTER_CHIPS: FilterChip[] = [
  { label: 'جميع الملاعب', value: 'all',          type: 'all'          },
  { label: 'عين الباشا',   value: 'عين الباشا',   type: 'area'         },
  { label: 'خلدا',          value: 'خلدا',          type: 'area'         },
  { label: 'الجبيهة',       value: 'الجبيهة',       type: 'area'         },
  { label: 'شفا بدران',     value: 'شفا بدران',     type: 'area'         },
  { label: '5×5',           value: '5x5',           type: 'size'         },
  { label: '7×7',           value: '7x7',           type: 'size'         },
  { label: 'متاح الآن',     value: 'available_now', type: 'availability' },
];

const SORT_OPTIONS: { label: string; value: SortKey }[] = [
  { label: 'السعر: من الأقل',  value: 'price_asc'  },
  { label: 'السعر: من الأعلى', value: 'price_desc' },
  { label: 'الأعلى تقييماً',   value: 'rating'     },
];

// ─── Helpers ──────────────────────────────────────────────────────────────────

function pitchCountLabel(n: number): string {
  if (n === 0) return 'لا توجد ملاعب';
  if (n === 1) return 'ملعب واحد';
  if (n === 2) return 'ملعبان';
  if (n <= 10) return `${n} ملاعب`;
  return `${n} ملعبًا`;
}

// ─── Sub-components ───────────────────────────────────────────────────────────

function PitchCardSkeleton() {
  return (
    <div className="rounded-2xl overflow-hidden bg-[#141715] border border-gray-800 animate-pulse">
      <div className="aspect-video bg-white/[0.05]" />
      <div className="p-4 flex flex-col gap-3">
        <div className="h-4 bg-white/[0.07] rounded-full w-3/4" />
        <div className="flex items-center justify-between gap-2">
          <div className="h-3 bg-white/[0.05] rounded-full w-2/5" />
          <div className="h-3 bg-white/[0.04] rounded-full w-10" />
        </div>
        <div className="flex gap-1.5">
          <div className="h-5 bg-white/[0.05] rounded-full w-10" />
          <div className="h-5 bg-white/[0.05] rounded-full w-16" />
          <div className="h-5 bg-white/[0.05] rounded-full w-12" />
        </div>
        <div className="h-px bg-white/[0.05]" />
        <div className="flex items-center justify-between">
          <div className="h-6 bg-white/[0.07] rounded-full w-14" />
          <div className="h-8 bg-white/[0.05] rounded-xl w-20" />
        </div>
      </div>
    </div>
  );
}

function SortDropdown({ value, onChange }: { value: SortKey; onChange: (v: SortKey) => void }) {
  return (
    <div className="relative">
      <label htmlFor="sort-select" className="sr-only">ترتيب النتائج</label>
      <div className="flex items-center pointer-events-none absolute start-3 top-1/2 -translate-y-1/2 text-white/25">
        <SlidersHorizontal size={10} aria-hidden />
      </div>
      <select
        id="sort-select"
        value={value}
        onChange={e => onChange(e.target.value as SortKey)}
        dir="rtl"
        className={[
          'appearance-none ps-8 pe-4 py-2',
          'bg-white/[0.04] border border-white/[0.08] rounded-lg',
          'text-[11px] text-white/50',
          'hover:border-white/[0.15] transition-colors duration-150',
          'focus:outline-none focus:ring-1 focus:ring-emerald-500/40',
          'cursor-pointer [&>option]:bg-[#1a1c1b]',
        ].join(' ')}
      >
        {SORT_OPTIONS.map(opt => (
          <option key={opt.value} value={opt.value}>{opt.label}</option>
        ))}
      </select>
      <ChevronDown size={10} aria-hidden className="absolute end-2.5 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none" />
    </div>
  );
}

function EmptyState({ onReset }: { onReset: () => void }) {
  return (
    <div className="col-span-full flex flex-col items-center justify-center py-28 gap-5 text-center">
      <svg viewBox="0 0 240 140" fill="none" xmlns="http://www.w3.org/2000/svg"
        className="w-28 h-16 opacity-[0.12]" aria-hidden>
        <rect x="8" y="8" width="224" height="124" stroke="white" strokeWidth="2" rx="3" />
        <line x1="120" y1="8" x2="120" y2="132" stroke="white" strokeWidth="1.5" />
        <circle cx="120" cy="70" r="28" stroke="white" strokeWidth="1.5" />
        <circle cx="120" cy="70" r="3" fill="white" />
        <rect x="8"   y="40" width="36" height="60" stroke="white" strokeWidth="1.5" />
        <rect x="196" y="40" width="36" height="60" stroke="white" strokeWidth="1.5" />
        <circle cx="50"  cy="70" r="2.5" fill="white" />
        <circle cx="190" cy="70" r="2.5" fill="white" />
      </svg>
      <div>
        <p className="text-[16px] font-bold text-white/40 mb-1.5">لا توجد ملاعب متاحة</p>
        <p className="text-[13px] text-white/25">جرّب تغيير الفلاتر أو البحث بكلمة مختلفة</p>
      </div>
      <button type="button" onClick={onReset}
        className="text-[11px] text-emerald-500 hover:text-emerald-400 transition-colors underline underline-offset-2">
        إعادة تعيين الفلاتر
      </button>
    </div>
  );
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="col-span-full flex flex-col items-center justify-center py-28 gap-5 text-center">
      <div className="w-14 h-14 rounded-full bg-red-500/[0.07] border border-red-500/20 flex items-center justify-center">
        <RefreshCw size={22} className="text-red-400/60" aria-hidden />
      </div>
      <div>
        <p className="text-[16px] font-bold text-white/40 mb-1.5">تعذّر تحميل الملاعب</p>
        <p className="text-[13px] text-white/25">تحقق من اتصالك بالإنترنت وحاول مجدداً</p>
      </div>
      <button type="button" onClick={onRetry}
        className={[
          'flex items-center gap-2 px-5 py-2.5 rounded-xl',
          'text-[12px] font-bold text-emerald-400',
          'bg-emerald-500/10 border border-emerald-500/20',
          'hover:bg-emerald-500/20 transition-all duration-150',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
        ].join(' ')}>
        <RefreshCw size={13} aria-hidden />
        إعادة المحاولة
      </button>
    </div>
  );
}

// ─── Inner page (consumes LocationContext) ────────────────────────────────────

function PitchesContent() {
  const { user, isLoading: authLoading } = useAuth();
  const router = useRouter();
  const { coords, status: locStatus, request: requestLocation } = useLocation();

  useEffect(() => {
    if (!authLoading && user?.role === 'owner') router.replace('/dashboard');
  }, [user, authLoading, router]);

  const [pitches,   setPitches]   = useState<Pitch[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error,     setError]     = useState<string | null>(null);
  const [fetchKey,  setFetchKey]  = useState(0);

  const [query,        setQuery]        = useState('');
  const [activeFilter, setActiveFilter] = useState<FilterValue>('all');
  const [sortKey,      setSortKey]      = useState<SortKey>('price_asc');

  // Date+time availability search (lifted from the former /availability route). It
  // fetches ONLY on submit; while a search is active it replaces the default
  // listing with availability cards. Clearing returns to the default listing.
  const search = useAvailabilitySearch();

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    setError(null);

    api.get('/pitches')
      .then(res => { if (!cancelled) setPitches(res.data.data ?? []); })
      .catch(() => { if (!cancelled) setError('fetch_failed'); })
      .finally(() => { if (!cancelled) setIsLoading(false); });

    return () => { cancelled = true; };
  }, [fetchKey]);

  const retry = useCallback(() => setFetchKey(k => k + 1), []);

  // Attach distanceKm to every pitch at this level.
  // When coords is null (location not granted) distanceKm is undefined —
  // PitchCard hides the distance row entirely.
  const pitchesWithDist = useMemo<Pitch[]>(() => {
    if (!coords) return pitches;
    return pitches.map(p => ({
      ...p,
      distanceKm: Math.round(haversineKm(coords, { lat: p.lat, lng: p.lng }) * 10) / 10,
    }));
  }, [pitches, coords]);

  const filteredPitches = useMemo<Pitch[]>(() => {
    if (isLoading || error) return [];
    const q    = query.trim();
    const chip = FILTER_CHIPS.find(c => c.value === activeFilter);

    const filtered = pitchesWithDist.filter(pitch => {
      const matchesQuery =
        !q ||
        pitch.name.includes(q) ||
        pitch.neighborhood.includes(q);

      const matchesFilter = !chip || chip.type === 'all'
        ? true
        : chip.type === 'area'
          ? pitch.neighborhood === activeFilter
          : chip.type === 'size'
            ? pitch.size === activeFilter
            : pitch.availabilityToday !== 'full';

      return matchesQuery && matchesFilter;
    });

    return [...filtered].sort((a, b) => {
      if (sortKey === 'price_asc')  return a.pricePerHour - b.pricePerHour;
      if (sortKey === 'price_desc') return b.pricePerHour - a.pricePerHour;
      // rating sort: nulls go last
      const ra = a.rating ?? -1;
      const rb = b.rating ?? -1;
      return rb - ra;
    });
  }, [pitchesWithDist, query, activeFilter, sortKey, isLoading, error]);

  const handleReset = useCallback(() => {
    setQuery('');
    setActiveFilter('all');
    setSortKey('price_asc');
  }, []);

  // Full-page spinner only while auth resolves (user role unknown)
  if (authLoading || user?.role === 'owner') {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">

      <Navbar />

      {/* ── Hero ──────────────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 pt-16 pb-12 text-center">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/[0.06] mb-7">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
          <span className="text-[10px] font-bold tracking-wide text-emerald-400">
            الشبكة الأولى للملاعب في الأردن
          </span>
        </div>
        <h1 className="text-4xl sm:text-5xl md:text-[56px] font-bold text-[#f0efe8] tracking-tight leading-[1.15] mb-5">
          ابحث عن{' '}
          <span className="text-emerald-500">ملعبك المثالي</span>
        </h1>
        <p className="text-[15px] text-white/40 max-w-md mx-auto leading-relaxed">
          أفضل ملاعب كرة القدم في عمّان. تصفّح، اختر، واحجز بلحظات — بدون اتصال.
        </p>
      </section>

      {/* ── Availability search (prominent, top of discovery) ─────────────── */}
      <section className="max-w-7xl mx-auto px-6 mb-10" aria-label="البحث عن ملعب متاح حسب الوقت">
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5 sm:p-6">
          <div className="flex items-center gap-2 mb-4">
            <Search size={14} className="text-emerald-400" aria-hidden />
            <span className="text-[12.5px] font-bold text-[#f0efe8]">ابحث عن ملعب متاح في وقت محدّد</span>
          </div>
          <AvailabilitySearchBar s={search} />
        </div>
      </section>

      {search.hasSearched ? (
        /* ═══ Searched state: availability result cards ═══ */
        <main className="max-w-7xl mx-auto px-6 pb-20">
          <AvailabilityResults s={search} />
        </main>
      ) : (
      <>

      {/* ── Search & filters ──────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 mb-10" aria-label="البحث وتصفية الملاعب">
        <div className="relative mb-4">
          <label htmlFor="pitch-search" className="sr-only">ابحث عن ملعب</label>
          <Search size={16} aria-hidden className="absolute start-4 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none" />
          <input
            id="pitch-search"
            type="search"
            placeholder="ابحث بالاسم أو الحي..."
            value={query}
            onChange={e => setQuery(e.target.value)}
            dir="rtl"
            className={[
              'w-full h-12 pe-4 ps-11',
              'bg-white/[0.03] border border-white/[0.08] rounded-xl',
              'text-sm text-[#f0efe8] placeholder:text-white/20',
              'hover:border-white/[0.14] transition-all duration-150',
              'focus:outline-none focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
            ].join(' ')}
          />
        </div>

        {/* Filter chips + "nearest" button */}
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="فلترة الملاعب">
          {FILTER_CHIPS.map(chip => {
            const isActive = activeFilter === chip.value;
            return (
              <button
                key={chip.value}
                type="button"
                onClick={() => setActiveFilter(chip.value)}
                aria-pressed={isActive}
                className={[
                  'px-4 py-1.5 rounded-full text-[12px] font-semibold border',
                  'transition-all duration-150',
                  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
                  isActive
                    ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-400'
                    : 'bg-transparent border-white/[0.08] text-white/40 hover:border-white/[0.18] hover:text-white/65',
                ].join(' ')}
              >
                {chip.label}
              </button>
            );
          })}

          {/* Location button — triggers geolocation on first click */}
          <button
            type="button"
            onClick={requestLocation}
            disabled={locStatus === 'loading' || locStatus === 'granted'}
            aria-label="الملاعب الأقرب لي"
            className={[
              'flex items-center gap-1.5 px-4 py-1.5 rounded-full text-[12px] font-semibold border',
              'transition-all duration-150',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
              locStatus === 'granted'
                ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-400'
                : 'bg-transparent border-white/[0.08] text-white/40 hover:border-white/[0.18] hover:text-white/65',
            ].join(' ')}
          >
            {locStatus === 'loading'
              ? <Loader2 size={11} className="animate-spin" aria-hidden />
              : <MapPin size={11} aria-hidden />
            }
            الملاعب الأقرب لي
          </button>

          {/* Denied note — inline, no layout shift */}
          {locStatus === 'denied' && (
            <span className="text-[11px] text-red-400/70 flex items-center gap-1">
              تعذّر تحديد موقعك
            </span>
          )}
        </div>
      </section>

      {/* ── Results bar ───────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 mb-6">
        <div className="flex items-center justify-between">
          {isLoading ? (
            <div className="h-3.5 w-28 bg-white/[0.05] rounded-full animate-pulse" />
          ) : !error ? (
            <p className="text-[11px] font-semibold tracking-wide text-white/30 uppercase">
              {pitchCountLabel(filteredPitches.length)} متاح
            </p>
          ) : (
            <span />
          )}
          <SortDropdown value={sortKey} onChange={setSortKey} />
        </div>
      </section>

      {/* ── Pitches grid ──────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 pb-20">
        <div
          className="grid gap-5 grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
          aria-label="الملاعب المتاحة"
          aria-live="polite"
          aria-atomic="true"
          aria-busy={isLoading}
        >
          {isLoading
            ? Array.from({ length: 6 }, (_, i) => <PitchCardSkeleton key={i} />)
            : error
              ? <ErrorState onRetry={retry} />
              : filteredPitches.length > 0
                ? filteredPitches.map(p => <PitchCard key={p.id} pitch={p} />)
                : <EmptyState onReset={handleReset} />
          }
        </div>
      </main>

      </>
      )}

      {/* ── Footer ────────────────────────────────────────────────────────── */}
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

// ─── Page wrapper — provides LocationContext once for the whole listing ───────

export default function PitchesPage() {
  return (
    <LocationProvider>
      <PitchesContent />
    </LocationProvider>
  );
}
