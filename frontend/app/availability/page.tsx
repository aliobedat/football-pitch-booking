'use client';

import { useState, useCallback, useMemo } from 'react';
import Link from 'next/link';
import api from '@/lib/api';
import Navbar from '@/components/Navbar';
import { useUserLocation } from '@/hooks/useUserLocation';
import { formatTime } from '@/lib/format';
import { Search, MapPin, Navigation, Loader2, Clock, CalendarDays, AlertTriangle } from 'lucide-react';

// ─────────────────────────────────────────────────────────────────────────────
// Availability search — date + start time (+ optional location) → pitches open
// at that start and free from it for ≥ 60 min, nearest-first when location is
// shared. Backend: GET /pitches/availability (Cache-Control: no-store).
// ─────────────────────────────────────────────────────────────────────────────

const AMMAN_OFFSET = '+03:00'; // Jordan is permanent UTC+3 (no DST)

interface AvailabilityResult {
  id:                number;
  name:              string;
  area:              string;
  image_url:         string;
  available_until:   string;  // ISO
  available_minutes: number;
  distance_km:       number | null;
}

// Today's Amman civil date as YYYY-MM-DD (for the date input default + min).
function ammanToday(): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Amman', year: 'numeric', month: '2-digit', day: '2-digit',
  }).format(new Date());
}

// "ساعة ونصف" / "ساعتان" / "90 دقيقة" — compact Arabic duration, Latin digits.
function formatDuration(mins: number): string {
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  const hPart = h === 1 ? 'ساعة' : h === 2 ? 'ساعتان' : h > 2 ? `${h} ساعات` : '';
  const mPart = m > 0 ? `${m} دقيقة` : '';
  if (hPart && mPart) return `${hPart} و${mPart}`;
  return hPart || mPart || '0 دقيقة';
}

export default function AvailabilityPage() {
  const [date, setDate] = useState<string>(ammanToday());
  const [time, setTime] = useState<string>('18:00');
  const { coords, status, request } = useUserLocation();

  const [results, setResults] = useState<AvailabilityResult[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError]     = useState<string | null>(null);

  // Display label for "free from {time}" built from the chosen Amman wall-clock.
  const startLabel = useMemo(() => {
    if (!date || !time) return '';
    return formatTime(`${date}T${time}:00${AMMAN_OFFSET}`);
  }, [date, time]);

  const runSearch = useCallback(async () => {
    if (!date || !time) { setError('اختر التاريخ والوقت'); return; }
    setLoading(true);
    setError(null);
    try {
      const params: Record<string, string> = { date, start_time: time };
      // Include coordinates only when the user explicitly granted location.
      if (status === 'granted' && coords) {
        params.lat = String(coords.lat);
        params.lng = String(coords.lng);
      }
      const res = await api.get('/pitches/availability', { params });
      setResults(res.data.results ?? []);
    } catch (err: any) {
      const data = err?.response?.data;
      setError(data?.message ?? 'تعذّر البحث، حاول مجدداً');
      setResults(null);
    } finally {
      setLoading(false);
    }
  }, [date, time, status, coords]);

  const inputCls = [
    'w-full bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-3',
    'text-[13px] text-[#f0efe8] [color-scheme:dark]',
    'focus:outline-none focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20',
    'transition-all duration-150',
  ].join(' ');

  return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">
      <Navbar />

      <section className="max-w-5xl mx-auto px-6 pt-12 pb-6">
        <div className="flex items-center gap-2.5 mb-4">
          <div className="w-8 h-8 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
            <Search size={14} className="text-emerald-400" aria-hidden />
          </div>
          <span className="text-[11px] font-bold tracking-widest text-emerald-400 uppercase">بحث فوري</span>
        </div>
        <h1 className="text-3xl sm:text-4xl font-bold text-[#f0efe8] tracking-tight leading-tight">ابحث عن ملعب متاح</h1>
        <p className="text-[13px] text-white/35 mt-1.5">
          اختر التاريخ ووقت البداية، وفعّل موقعك لترتيب الملاعب الأقرب أولاً.
        </p>

        {/* Search controls */}
        <div className="mt-7 grid grid-cols-1 sm:grid-cols-[1fr_1fr_auto] gap-3 items-end">
          <div>
            <label className="block text-[11px] font-semibold text-white/40 mb-1.5">التاريخ</label>
            <div className="relative">
              <CalendarDays size={14} className="absolute start-3 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none" aria-hidden />
              <input type="date" value={date} min={ammanToday()} onChange={e => setDate(e.target.value)}
                className={`${inputCls} ps-9`} />
            </div>
          </div>
          <div>
            <label className="block text-[11px] font-semibold text-white/40 mb-1.5">وقت البداية</label>
            <div className="relative">
              <Clock size={14} className="absolute start-3 top-1/2 -translate-y-1/2 text-white/25 pointer-events-none" aria-hidden />
              <input type="time" value={time} onChange={e => setTime(e.target.value)}
                className={`${inputCls} ps-9`} />
            </div>
          </div>
          <button
            type="button"
            onClick={runSearch}
            disabled={loading}
            className="flex items-center justify-center gap-2 px-6 py-3 rounded-xl text-[12px] font-bold bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20 hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40 disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-200 active:scale-[0.97]"
          >
            {loading ? <Loader2 size={14} className="animate-spin" aria-hidden /> : <Search size={14} aria-hidden />}
            ابحث
          </button>
        </div>

        {/* Location control + permission-denied path */}
        <div className="mt-3 flex items-center gap-2 flex-wrap">
          <button
            type="button"
            onClick={request}
            disabled={status === 'loading'}
            className={[
              'inline-flex items-center gap-1.5 px-3.5 py-2 rounded-lg text-[11.5px] font-semibold border transition-all duration-150',
              status === 'granted'
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : 'bg-white/[0.03] border-white/[0.10] text-white/55 hover:text-white/80 hover:border-white/[0.20]',
            ].join(' ')}
          >
            {status === 'loading'
              ? <Loader2 size={13} className="animate-spin" aria-hidden />
              : <Navigation size={13} aria-hidden />}
            {status === 'granted' ? 'يتم الترتيب حسب موقعك' : 'الأقرب إليّ'}
          </button>
          {status === 'denied' && (
            <span className="inline-flex items-center gap-1.5 text-[11px] text-amber-300/80">
              <AlertTriangle size={12} aria-hidden />
              تعذّر الوصول إلى الموقع — النتائج بالترتيب الافتراضي.
            </span>
          )}
          {status === 'unavailable' && (
            <span className="text-[11px] text-white/35">المتصفح لا يدعم تحديد الموقع.</span>
          )}
        </div>
      </section>

      {/* Results */}
      <main className="max-w-5xl mx-auto px-6 pb-20">
        {error && (
          <div className="flex items-center gap-2 text-[12.5px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-3">
            <AlertTriangle size={14} aria-hidden />
            {error}
          </div>
        )}

        {results !== null && !error && (
          results.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-24 gap-4">
              <div className="w-16 h-16 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
                <CalendarDays size={26} className="text-white/15" aria-hidden />
              </div>
              <p className="text-[14px] text-white/40">لا توجد ملاعب متاحة في هذا الوقت — جرّب وقتاً آخر.</p>
            </div>
          ) : (
            <>
              <p className="text-[12px] text-white/35 mb-4">{results.length} ملعب متاح</p>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
                {results.map(r => (
                  <Link
                    key={r.id}
                    href={`/pitches/${r.id}`}
                    className="group flex flex-col rounded-2xl overflow-hidden bg-[#141715] border border-white/[0.08] hover:border-white/[0.18] hover:-translate-y-0.5 transition-all duration-300"
                  >
                    <div className="relative h-36 bg-white/[0.03]">
                      {r.image_url
                        // eslint-disable-next-line @next/next/no-img-element
                        ? <img src={r.image_url} alt={r.name} className="w-full h-full object-cover" />
                        : <div className="w-full h-full flex items-center justify-center"><MapPin size={24} className="text-white/15" aria-hidden /></div>}
                      {r.distance_km !== null && (
                        <span className="absolute top-2 end-2 inline-flex items-center gap-1 px-2 py-1 rounded-lg text-[10px] font-bold bg-black/60 backdrop-blur-sm text-emerald-300 border border-emerald-500/30">
                          <Navigation size={10} aria-hidden />
                          {r.distance_km.toFixed(1)} كم
                        </span>
                      )}
                    </div>
                    <div className="p-4 flex flex-col gap-2 flex-1">
                      <h3 className="text-[14px] font-bold text-[#f0efe8] leading-snug">{r.name}</h3>
                      <div className="flex items-center gap-1.5 text-white/40">
                        <MapPin size={11} aria-hidden />
                        <span className="text-[11px]">{r.area}، عمّان</span>
                      </div>
                      <div className="mt-auto pt-2 flex items-center gap-1.5 text-[12px] text-emerald-300/90">
                        <Clock size={12} aria-hidden />
                        <span>
                          متاح من <span className="font-semibold">{startLabel}</span>
                          {' '}— لمدة تصل إلى <span className="font-semibold">{formatDuration(r.available_minutes)}</span>
                        </span>
                      </div>
                    </div>
                  </Link>
                ))}
              </div>
            </>
          )
        )}
      </main>
    </div>
  );
}
