'use client';

import { useState, useEffect, useMemo, useCallback } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useAuth } from '@/context/AuthContext';
import { Search, SlidersHorizontal, ChevronDown } from 'lucide-react';
import Navbar from '@/components/Navbar';
import PitchCard, { type Pitch } from '@/components/PitchCard';

// ─── Dummy data ───────────────────────────────────────────────────────────────
// Typed against the Pitch contract in PitchCard.tsx.
// To switch to the real API, replace the useEffect below with:
//   api.get('/pitches').then(r => setPitches(r.data.data)).catch(...)

const DUMMY_PITCHES: Pitch[] = [
  {
    id: '1', name: 'ملعب النجمة الذهبية', city: 'عمان', area: 'عين الباشا',
    imageUrl: null, pricePerHour: 10, rating: 4.8, reviewsCount: 124,
    size: '5x5', surface: 'عشب صناعي', isIndoor: false,
    hasLighting: true, hasParking: true,
    availabilityToday: 'available', nextAvailableSlot: '18:00', distanceKm: 2.3,
  },
  {
    id: '2', name: 'ملعب الفيصل', city: 'عمان', area: 'خلدا',
    imageUrl: null, pricePerHour: 15, rating: 4.5, reviewsCount: 87,
    size: '7x7', surface: 'عشب طبيعي', isIndoor: true,
    hasLighting: true, hasParking: false,
    availabilityToday: 'limited', nextAvailableSlot: '20:00', distanceKm: 5.1,
  },
  {
    id: '3', name: 'ملعب الوحدات', city: 'عمان', area: 'الجبيهة',
    imageUrl: null, pricePerHour: 8, rating: 4.2, reviewsCount: 56,
    size: '5x5', surface: 'عشب صناعي', isIndoor: false,
    hasLighting: false, hasParking: true,
    availabilityToday: 'full', distanceKm: 7.8,
  },
  {
    id: '4', name: 'ملعب البطولة', city: 'عمان', area: 'شفا بدران',
    imageUrl: null, pricePerHour: 12, rating: 4.9, reviewsCount: 203,
    size: '11x11', surface: 'عشب طبيعي', isIndoor: false,
    hasLighting: true, hasParking: true,
    availabilityToday: 'available', nextAvailableSlot: '19:00', distanceKm: 3.6,
  },
  {
    id: '5', name: 'ملعب الأولمبي', city: 'عمان', area: 'عين الباشا',
    imageUrl: null, pricePerHour: 20, rating: 4.7, reviewsCount: 341,
    size: '7x7', surface: 'عشب صناعي', isIndoor: true,
    hasLighting: true, hasParking: true,
    availabilityToday: 'limited', nextAvailableSlot: '21:00', distanceKm: 1.9,
  },
  {
    id: '6', name: 'ملعب الهلال', city: 'عمان', area: 'خلدا',
    imageUrl: null, pricePerHour: 10, rating: 4.3, reviewsCount: 67,
    size: '5x5', surface: 'عشب صناعي', isIndoor: false,
    hasLighting: true, hasParking: false,
    availabilityToday: 'available', nextAvailableSlot: '17:00', distanceKm: 4.4,
  },
  {
    id: '7', name: 'ملعب الاتحاد', city: 'عمان', area: 'الجبيهة',
    imageUrl: null, pricePerHour: 18, rating: 4.6, reviewsCount: 152,
    size: '7x7', surface: 'عشب صناعي', isIndoor: true,
    hasLighting: true, hasParking: true,
    availabilityToday: 'available', nextAvailableSlot: '18:30', distanceKm: 6.2,
  },
  {
    id: '8', name: 'ملعب المدينة', city: 'عمان', area: 'شفا بدران',
    imageUrl: null, pricePerHour: 9, rating: 4.0, reviewsCount: 38,
    size: '5x5', surface: 'عشب طبيعي', isIndoor: false,
    hasLighting: false, hasParking: true,
    availabilityToday: 'limited', nextAvailableSlot: '22:00', distanceKm: 9.1,
  },
];

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
      {/* Pitch illustration */}
      <svg
        viewBox="0 0 240 140"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        className="w-28 h-16 opacity-[0.12]"
        aria-hidden
      >
        <rect x="8" y="8" width="224" height="124" stroke="white" strokeWidth="2" rx="3" />
        <line x1="120" y1="8" x2="120" y2="132" stroke="white" strokeWidth="1.5" />
        <circle cx="120" cy="70" r="28" stroke="white" strokeWidth="1.5" />
        <circle cx="120" cy="70" r="3" fill="white" />
        <rect x="8"   y="40" width="36"  height="60" stroke="white" strokeWidth="1.5" />
        <rect x="196" y="40" width="36"  height="60" stroke="white" strokeWidth="1.5" />
        <circle cx="50"  cy="70" r="2.5" fill="white" />
        <circle cx="190" cy="70" r="2.5" fill="white" />
      </svg>
      <div>
        <p className="text-[16px] font-bold text-white/40 mb-1.5">لا توجد ملاعب متاحة</p>
        <p className="text-[13px] text-white/22">جرّب تغيير الفلاتر أو البحث بكلمة مختلفة</p>
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

// ─── Page ─────────────────────────────────────────────────────────────────────

export default function PitchesPage() {
  const { user, isLoading: authLoading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!authLoading && user?.role === 'owner') {
      router.replace('/dashboard');
    }
  }, [user, authLoading, router]);

  const [pitches,   setPitches]   = useState<Pitch[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  const [query,        setQuery]        = useState('');
  const [activeFilter, setActiveFilter] = useState<FilterValue>('all');
  const [sortKey,      setSortKey]      = useState<SortKey>('price_asc');

  useEffect(() => {
    // Swap this block for an api.get('/pitches') call when the backend is ready
    setPitches(DUMMY_PITCHES);
    setIsLoading(false);
  }, []);

  const filteredPitches = useMemo<Pitch[]>(() => {
    const q = query.trim();
    const chip = FILTER_CHIPS.find(c => c.value === activeFilter);

    const filtered = pitches.filter(pitch => {
      const matchesQuery =
        !q ||
        pitch.name.includes(q) ||
        pitch.area.includes(q) ||
        pitch.city.includes(q);

      const matchesFilter = !chip || chip.type === 'all'
        ? true
        : chip.type === 'area'
          ? pitch.area === activeFilter
          : chip.type === 'size'
            ? pitch.size === activeFilter
            : pitch.availabilityToday !== 'full'; // availability_now

      return matchesQuery && matchesFilter;
    });

    return [...filtered].sort((a, b) => {
      if (sortKey === 'price_asc')  return a.pricePerHour - b.pricePerHour;
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

        <div
          role="group"
          aria-label="فلترة حسب الحي أو نوع الملعب أو التوفر"
          className="flex flex-wrap gap-2 justify-start"
        >
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
        </div>
      </section>

      {/* ── Results bar ───────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 mb-6">
        <div className="flex items-center justify-between">
          <p className="text-[11px] font-semibold tracking-wide text-white/30 uppercase">
            {pitchCountLabel(filteredPitches.length)} متاح
          </p>
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
        >
          {filteredPitches.length > 0
            ? filteredPitches.map(pitch => <PitchCard key={pitch.id} pitch={pitch} />)
            : <EmptyState onReset={handleReset} />
          }
        </div>
      </main>

      {/* ── Footer ────────────────────────────────────────────────────────── */}
      <footer className="border-t border-white/[0.05] py-8">
        <div className="max-w-7xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            {['الخصوصية', 'الشروط', 'تواصل معنا'].map((item, i) => (
              <Link
                key={item}
                href={`/${(['privacy', 'terms', 'contact'] as const)[i]}`}
                className="text-[11px] text-white/20 hover:text-white/45 transition-colors duration-150"
              >
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
