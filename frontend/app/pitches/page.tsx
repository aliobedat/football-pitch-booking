'use client';

import { useState, useEffect, useMemo, useCallback } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import {
  Search,
  MapPin,
  Star,
  Clock,
  Users,
  Zap,
  ChevronDown,
  SlidersHorizontal,
} from 'lucide-react';
import Navbar from '@/components/Navbar';
// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type SurfaceType = 'artificial_grass' | 'natural_grass' | 'futsal_court';
type Format      = 'خماسي' | 'سباعي';
type SortKey     = 'price_asc' | 'price_desc' | 'rating';

interface Pitch {
  id:           number;
  name:         string;
  neighborhood: string;
  surface:      SurfaceType;
  format:       Format;
  pricePerHour: number;
  rating:       number;
  reviewCount:  number;
  isFeatured:   boolean;
  amenities:    string[];
  pitchHue:     string;
}

type FilterValue =
  | 'all'
  | 'عين الباشا'
  | 'خلدا'
  | 'الجبيهة'
  | 'شفا بدران'
  | 'خماسي'
  | 'سباعي';

interface FilterChip {
  label: string;
  value: FilterValue;
  type:  'all' | 'neighborhood' | 'format';
}

// ─────────────────────────────────────────────────────────────────────────────
// Static data  (replace PITCHES with an API call when the Go backend is wired)
// ─────────────────────────────────────────────────────────────────────────────


const FILTER_CHIPS: FilterChip[] = [
  { label: 'جميع الملاعب', value: 'all',          type: 'all'          },
  { label: 'عين الباشا',   value: 'عين الباشا',   type: 'neighborhood' },
  { label: 'خلدا',         value: 'خلدا',          type: 'neighborhood' },
  { label: 'الجبيهة',      value: 'الجبيهة',       type: 'neighborhood' },
  { label: 'شفا بدران',    value: 'شفا بدران',     type: 'neighborhood' },
  { label: 'خماسي',        value: 'خماسي',          type: 'format'       },
  { label: 'سباعي',        value: 'سباعي',          type: 'format'       },
];

const SORT_OPTIONS: { label: string; value: SortKey }[] = [
  { label: 'السعر: من الأقل',    value: 'price_asc'  },
  { label: 'السعر: من الأعلى',   value: 'price_desc' },
  { label: 'الأعلى تقييماً',     value: 'rating'     },
];

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const SURFACE_LABEL: Record<SurfaceType, string> = {
  artificial_grass: 'عشبية صناعية',
  natural_grass:    'عشبية طبيعية',
  futsal_court:     'ملعب فوتسال',
};

// Arabic ordinal for result count display
function pitchCountLabel(n: number): string {
  if (n === 0) return 'لا توجد ملاعب';
  if (n === 1) return 'ملعب واحد';
  if (n === 2) return 'ملعبان';
  if (n <= 10) return `${n} ملاعب`;
  return `${n} ملعبًا`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Sub-components
// ─────────────────────────────────────────────────────────────────────────────

/** Inline SVG pitch diagram — keeps each card visually alive without images */
function PitchDiagram({
  hue,
  featured,
}: {
  hue: string;
  featured: boolean;
}) {
  const lines = featured
    ? 'rgba(52,211,153,0.55)'
    : 'rgba(255,255,255,0.12)';

  return (
    <svg
      viewBox="0 0 320 160"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      className="absolute inset-0 w-full h-full"
    >
      <rect width="320" height="160" fill={hue} />

      {/* Pitch outline */}
      <rect x="24" y="14" width="272" height="132" stroke={lines} strokeWidth="1.2" />

      {/* Centre line */}
      <line x1="160" y1="14" x2="160" y2="146" stroke={lines} strokeWidth="1" />

      {/* Centre circle */}
      <circle cx="160" cy="80" r="28" stroke={lines} strokeWidth="1" />
      <circle cx="160" cy="80" r="2.5" fill={lines} />

      {/* Left penalty area */}
      <rect x="24" y="46" width="50" height="68" stroke={lines} strokeWidth="1" />
      {/* Left goal area */}
      <rect x="24" y="62" width="20" height="36" stroke={lines} strokeWidth="1" />
      {/* Left goal post */}
      <rect x="14" y="68" width="10" height="24" stroke={lines} strokeWidth="1" />

      {/* Right penalty area */}
      <rect x="246" y="46" width="50" height="68" stroke={lines} strokeWidth="1" />
      {/* Right goal area */}
      <rect x="276" y="62" width="20" height="36" stroke={lines} strokeWidth="1" />
      {/* Right goal post */}
      <rect x="296" y="68" width="10" height="24" stroke={lines} strokeWidth="1" />

      {/* Corner arcs */}
      <path d="M24 22 A8 8 0 0 1 32 14"   stroke={lines} strokeWidth="1" />
      <path d="M288 14 A8 8 0 0 0 296 22" stroke={lines} strokeWidth="1" />
      <path d="M32 146 A8 8 0 0 1 24 138" stroke={lines} strokeWidth="1" />
      <path d="M296 138 A8 8 0 0 1 288 146" stroke={lines} strokeWidth="1" />

      {/* Penalty spots */}
      <circle cx="68"  cy="80" r="2" fill={lines} />
      <circle cx="252" cy="80" r="2" fill={lines} />

      {featured && (
        <rect width="320" height="160" fill="rgba(16,185,129,0.04)" />
      )}
    </svg>
  );
}

/** Individual pitch card */
function PitchCard({ pitch }: { pitch: Pitch }) {
  return (
    <article
      className={[
        'group relative flex flex-col rounded-2xl overflow-hidden',
        'bg-[#141715]',
        'border transition-all duration-300 ease-out',
        pitch.isFeatured
          ? 'border-emerald-500/25 hover:border-emerald-500/50'
          : 'border-white/[0.07] hover:border-white/[0.16]',
        'hover:-translate-y-0.5',
      ].join(' ')}
    >
      {/* ── Diagram area ── */}
      <div className="relative h-44 overflow-hidden">
        <PitchDiagram hue={pitch.pitchHue} featured={pitch.isFeatured} />

        {/* Featured badge — top-right in RTL = inline-start */}
        {pitch.isFeatured && (
          <div className="absolute top-3 end-3 z-10">
            <span className="flex items-center gap-1 px-2 py-1 rounded-md text-[9px] font-bold tracking-wide bg-emerald-500/20 border border-emerald-500/30 text-emerald-400">
              <Zap size={8} aria-hidden="true" />
              مميّز
            </span>
          </div>
        )}

        {/* Rating — top-start in RTL */}
        <div className="absolute top-3 start-3 z-10">
          <span className="flex items-center gap-1 px-2.5 py-1 rounded-md text-[10px] font-semibold bg-black/60 backdrop-blur-sm border border-white/10 text-amber-400">
            <Star size={9} fill="currentColor" aria-hidden="true" />
            {pitch.rating.toFixed(1)}
            <span className="text-white/30 font-normal">
              ({pitch.reviewCount})
            </span>
          </span>
        </div>

        {/* Surface label — bottom-end */}
        <div className="absolute bottom-3 end-3 z-10">
          <span className="px-2.5 py-1 rounded-md text-[9px] font-bold tracking-wide bg-emerald-500/15 border border-emerald-500/20 text-emerald-400">
            {SURFACE_LABEL[pitch.surface]}
          </span>
        </div>
      </div>

      {/* ── Body ── */}
      <div className="flex flex-col flex-1 p-5">
        {/* Name & location */}
        <div className="mb-3">
          <h3 className="text-[15px] font-bold text-[#f0efe8] tracking-tight leading-snug mb-1.5 text-start">
            {pitch.name}
          </h3>
          <div className="flex items-center justify-start gap-1.5 text-white/40">
            <MapPin size={11} aria-hidden="true" />
            <span className="text-[11px]">
              {pitch.neighborhood}، عمّان
            </span>
          </div>
        </div>

        {/* Format + duration chips */}
        <div className="flex items-center justify-start gap-2 mb-4">
          <span className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.04] border border-white/[0.07] text-[10px] text-white/45">
            <Users size={9} aria-hidden="true" />
            {pitch.format}
          </span>
          <span className="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.04] border border-white/[0.07] text-[10px] text-white/45">
            <Clock size={9} aria-hidden="true" />
            للساعة
          </span>
        </div>

        {/* Amenities */}
        <div className="flex flex-wrap justify-start gap-1.5 mb-4">
          {pitch.amenities.map((a) => (
            <span
              key={a}
              className="px-2 py-0.5 rounded-full text-[10px] text-white/28 bg-white/[0.03] border border-white/[0.05]"
            >
              {a}
            </span>
          ))}
        </div>

        {/* Divider */}
        <div className="h-px bg-white/[0.05] mb-4" />

        {/* Price + Book button */}
        <div className="flex items-center justify-between mt-auto">

          {/* Book button */}
          <Link
            href={`/pitches/${pitch.id}`}
            className={[
              'flex items-center gap-2 px-5 py-2.5 rounded-xl',
              'text-[11px] font-bold tracking-wide',
              'bg-[#0f4c3a] text-emerald-400',
              'border border-emerald-500/20',
              'transition-all duration-200 ease-out',
              'hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40',
              'hover:shadow-[0_0_20px_rgba(16,185,129,0.12)]',
              'active:scale-[0.97]',
              'focus-visible:outline-none focus-visible:ring-2',
              'focus-visible:ring-emerald-500 focus-visible:ring-offset-2',
              'focus-visible:ring-offset-[#141715]',
            ].join(' ')}
          >
            احجز الآن
          </Link>

          {/* Price */}
          <div className="text-end">
            <div className="flex items-baseline justify-end gap-1">
              <span className="text-[22px] font-bold text-[#f0efe8] tracking-tight leading-none">
                {pitch.pricePerHour}
              </span>
              <span className="text-sm font-semibold text-emerald-500">دينار</span>
            </div>
            <p className="text-[10px] text-white/25 mt-0.5">للساعة الواحدة</p>
          </div>

        </div>
      </div>
    </article>
  );
}

/** Sort dropdown — fully RTL-aware */
function SortDropdown({
  value,
  onChange,
}: {
  value: SortKey;
  onChange: (v: SortKey) => void;
}) {
  return (
    <div className="relative">
      <label htmlFor="sort-select" className="sr-only">
        ترتيب النتائج
      </label>
      {/* Leading icon on the end side because of RTL */}
      <div className="flex items-center pointer-events-none absolute start-3 top-1/2 -translate-y-1/2 text-white/25">
        <SlidersHorizontal size={10} aria-hidden="true" />
      </div>
      <select
        id="sort-select"
        value={value}
        onChange={(e) => onChange(e.target.value as SortKey)}
        dir="rtl"
        className={[
          'appearance-none ps-8 pe-4 py-2',
          'bg-white/[0.04] border border-white/[0.08] rounded-lg',
          'text-[11px] text-white/50',
          'hover:border-white/[0.15] transition-colors duration-150',
          'focus:outline-none focus:ring-1 focus:ring-emerald-500/40',
          'cursor-pointer',
          '[&>option]:bg-[#1a1c1b]',
        ].join(' ')}
      >
        {SORT_OPTIONS.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
      <ChevronDown
        size={10}
        aria-hidden="true"
        className="absolute end-2.5 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none"
      />
    </div>
  );
}

/** Empty state */
function EmptyState({ onReset }: { onReset: () => void }) {
  return (
    <div className="col-span-full flex flex-col items-center justify-center py-24 gap-4">
      <div className="w-16 h-16 rounded-full bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
        <Search size={22} className="text-white/20" aria-hidden="true" />
      </div>
      <div className="text-center">
        <p className="text-[15px] font-semibold text-white/50 mb-1">لا توجد ملاعب</p>
        <p className="text-[12px] text-white/25">جرّب تغيير الفلاتر أو البحث بكلمة مختلفة</p>
      </div>
      <button
        type="button"
        onClick={onReset}
        className="text-[11px] text-emerald-500 hover:text-emerald-400 transition-colors underline underline-offset-2"
      >
        إعادة تعيين الفلاتر
      </button>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Page
// ─────────────────────────────────────────────────────────────────────────────

export default function PitchesPage() {
  const { user, isLoading: authLoading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!authLoading && user?.role === 'owner') {
      router.replace('/dashboard');
    }
  }, [user, authLoading, router]);

  const [pitches, setPitches] = useState<Pitch[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  const [query, setQuery] = useState('');
  const [activeFilter, setActiveFilter] = useState<FilterValue>('all');
  const [sortKey, setSortKey] = useState<SortKey>('price_asc');

  useEffect(() => {
    api.get('/pitches')
      .then((res) => {
        setPitches(res.data.data);
        setIsLoading(false);
      })
      .catch((err) => {
        console.error('Failed to fetch pitches:', err);
        setIsLoading(false);
      });
  }, []);

  const filteredPitches = useMemo<Pitch[]>(() => {
    const q = query.trim();

    const filtered = pitches.filter((pitch) => {
      const matchesQuery =
        !q ||
        pitch.name.includes(q) ||
        pitch.neighborhood.includes(q);

      const chip = FILTER_CHIPS.find((c) => c.value === activeFilter);
      const matchesFilter =
        !chip || chip.type === 'all'
          ? true
          : chip.type === 'neighborhood'
          ? pitch.neighborhood === activeFilter
          : pitch.format === activeFilter;

      return matchesQuery && matchesFilter;
    });

    return [...filtered].sort((a, b) => {
      if (sortKey === 'price_asc') return a.pricePerHour - b.pricePerHour;
      if (sortKey === 'price_desc') return b.pricePerHour - a.pricePerHour;
      return b.rating - a.rating;
    });
  }, [query, activeFilter, sortKey, pitches]);

  const handleReset = useCallback(() => {
    setQuery('');
    setActiveFilter('all');
    setSortKey('price_asc');
  }, []);

  if (isLoading || authLoading || user?.role === 'owner') {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">

      <Navbar />

      {/* ── Hero ─────────────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 pt-16 pb-12 text-center">
        {/* Eyebrow pill */}
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

      {/* ── Search & filters ─────────────────────────────────────────────── */}
      <section
        className="max-w-7xl mx-auto px-6 mb-10"
        aria-label="البحث وتصفية الملاعب"
      >
        {/* Search bar */}
        <div className="relative mb-4">
          <label htmlFor="pitch-search" className="sr-only">
            ابحث عن ملعب
          </label>
          <Search
            size={16}
            aria-hidden="true"
            className="absolute start-4 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none"
          />
          <input
            id="pitch-search"
            type="search"
            placeholder="ابحث بالاسم أو الحي..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            dir="rtl"
            className={[
              'w-full h-12 pe-4 ps-11',
              'bg-white/[0.03] border border-white/[0.08] rounded-xl',
              'text-sm text-[#f0efe8] placeholder:text-white/20 text-start',
              'transition-all duration-150',
              'hover:border-white/[0.14]',
              'focus:outline-none focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15',
            ].join(' ')}
          />
        </div>

        {/* Filter chips */}
        <div
          role="group"
          aria-label="فلترة حسب الحي أو نوع الملعب"
          className="flex flex-wrap gap-2 justify-start"
        >
          {FILTER_CHIPS.map((chip) => {
            const isActive = activeFilter === chip.value;
            return (
              <button
                key={chip.value}
                type="button"
                onClick={() => setActiveFilter(chip.value)}
                aria-pressed={isActive}
                className={[
                  'px-4 py-1.5 rounded-full text-[12px] font-semibold',
                  'border transition-all duration-150',
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
        </div>
      </section>

      {/* ── Results bar ──────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 mb-6">
        <div className="flex items-center justify-between">
          <p className="text-[11px] font-semibold tracking-wide text-white/30 uppercase">
            {pitchCountLabel(filteredPitches.length)} متاح
          </p>
          <SortDropdown value={sortKey} onChange={setSortKey} />
        </div>
      </section>

      {/* ── Pitches grid ─────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 pb-20">
        <div
          className="grid gap-5 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
          aria-label="الملاعب المتاحة"
          aria-live="polite"
          aria-atomic="true"
        >
          {filteredPitches.length > 0 ? (
            filteredPitches.map((pitch) => (
              <PitchCard key={pitch.id} pitch={pitch} />
            ))
          ) : (
            <EmptyState onReset={handleReset} />
          )}
        </div>
      </main>

      {/* ── Footer ───────────────────────────────────────────────────────── */}
      <footer className="border-t border-white/[0.05] py-8">
        <div className="max-w-7xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            {['الخصوصية', 'الشروط', 'تواصل معنا'].map((item, i) => (
              <Link
                key={item}
                href={`/${['privacy', 'terms', 'contact'][i]!}`}
                className="text-[11px] text-white/20 hover:text-white/45 transition-colors duration-150"
              >
                {item}
              </Link>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-white/20">
              © 2026 ملاعب. جميع الحقوق محفوظة.
            </span>
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-500/50" />
          </div>
        </div>
      </footer>
    </div>
  );
}