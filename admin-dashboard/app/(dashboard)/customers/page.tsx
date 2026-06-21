'use client';

// The Regulars — owner-scoped CRM directory (Cockpit WO1). Searchable + sortable,
// server-side (GET /owner/customers?search=&sort=); owner scoping is enforced in
// SQL, never client-side. The CUSTOMER NAME is the dominant visual element of each
// row — the owner must instantly see WHO they're looking at, app player or walk-in.

import { useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { Users, Search, Phone as PhoneIcon, CalendarDays, ShieldAlert, ChevronLeft } from 'lucide-react';
import api from '@/lib/api';
import { formatNumber, formatDate } from '@/lib/format';

type SortKey = 'name' | 'last_booked' | 'booking_count' | 'no_show';

interface CustomerListItem {
  id:            number;
  player_id:     number | null;
  name:          string;
  phone:         string;
  notes:         string;
  is_app_player: boolean;
  booking_count: number;
  last_booked:   string | null;
  no_show_count: number;
}

const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: 'name',          label: 'الاسم' },
  { value: 'last_booked',   label: 'آخر حجز' },
  { value: 'booking_count', label: 'عدد الحجوزات' },
  { value: 'no_show',       label: 'عدم الحضور' },
];

export default function CustomersPage() {
  const [customers, setCustomers] = useState<CustomerListItem[]>([]);
  const [loading, setLoading]     = useState(true);
  const [error, setError]         = useState<string | null>(null);
  const [search, setSearch]       = useState('');
  const [sort, setSort]           = useState<SortKey>('name');

  // Debounce the search term so we don't fire a request per keystroke.
  const [debounced, setDebounced] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setDebounced(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  useEffect(() => {
    const params: Record<string, string> = { sort };
    if (debounced) params.search = debounced;
    setLoading(true);
    setError(null);
    api.get('/owner/customers', { params })
      .then(res => setCustomers(res.data.data ?? []))
      .catch(() => setError('تعذّر تحميل قائمة الزبائن. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, [debounced, sort]);

  const total = useMemo(() => customers.length, [customers]);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-[20px] font-bold tracking-tight flex items-center gap-2">
          <Users size={20} className="text-emerald-400" aria-hidden />
          الزبائن المنتظمون
        </h1>
      </div>

      {/* Controls */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[220px]">
          <Search size={15} className="absolute top-1/2 -translate-y-1/2 start-3 text-white/30" aria-hidden />
          <input
            type="search"
            value={search}
            onChange={e => setSearch(e.target.value)}
            placeholder="ابحث بالاسم أو رقم الهاتف…"
            className="w-full bg-[#141715] border border-white/[0.09] rounded-xl ps-9 pe-3 py-2.5 text-[13px] text-white/80 placeholder:text-white/25 focus:outline-none focus:border-emerald-500/40"
          />
        </div>
        <label className="flex items-center gap-2 text-[12px] text-white/40">
          ترتيب
          <select
            value={sort}
            onChange={e => setSort(e.target.value as SortKey)}
            className="bg-[#141715] border border-white/[0.09] rounded-xl px-3 py-2.5 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40"
          >
            {SORT_OPTIONS.map(o => (
              <option key={o.value} value={o.value} className="bg-[#0f1110]">{o.label}</option>
            ))}
          </select>
        </label>
      </div>

      {loading ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center">
          <div className="inline-block w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
        </div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : total === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 gap-5">
          <div className="w-20 h-20 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
            <Users size={28} className="text-white/15" aria-hidden />
          </div>
          <div className="text-center">
            <p className="text-[16px] font-semibold text-white/45 mb-1">
              {debounced ? 'لا يوجد زبائن مطابقون' : 'لا يوجد زبائن بعد'}
            </p>
            <p className="text-[13px] text-white/25">
              {debounced ? 'جرّب بحثاً آخر' : 'سيظهر هنا زبائنك مع تراكم الحجوزات'}
            </p>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-2.5">
          <p className="text-[11px] text-white/30">{formatNumber(total)} زبون</p>
          {customers.map(cust => (
            <Link
              key={cust.id}
              href={`/customers/${cust.id}`}
              className="group rounded-2xl bg-[#141715] border border-white/[0.07] hover:border-emerald-500/25 hover:bg-white/[0.015] transition-all px-5 py-4 flex items-center gap-4"
            >
              {/* Avatar initial */}
              <div className="w-11 h-11 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center flex-shrink-0">
                <span className="text-[16px] font-bold text-emerald-300">
                  {(cust.name || '؟').trim().charAt(0)}
                </span>
              </div>

              {/* Identity — NAME IS KING */}
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-[17px] font-bold text-[#f0efe8] truncate leading-tight">
                    {cust.name || 'زبون بدون اسم'}
                  </span>
                  <span className={[
                    'inline-flex items-center px-2 py-0.5 rounded-md text-[9px] font-bold border',
                    cust.is_app_player
                      ? 'bg-sky-500/15 border-sky-500/30 text-sky-300'
                      : 'bg-amber-500/15 border-amber-500/30 text-amber-300',
                  ].join(' ')}>
                    {cust.is_app_player ? 'لاعب مسجّل' : 'زبون يدوي'}
                  </span>
                </div>
                <p className="text-[12px] text-white/45 mt-0.5 font-mono" dir="ltr">{cust.phone}</p>
              </div>

              {/* Stats */}
              <div className="hidden sm:flex items-center gap-6 flex-shrink-0">
                <Stat icon={CalendarDays} value={formatNumber(cust.booking_count)} label="حجوزات" />
                <Stat
                  icon={ShieldAlert}
                  value={formatNumber(cust.no_show_count)}
                  label="غياب"
                  danger={cust.no_show_count > 0}
                />
                <div className="text-end min-w-[84px]">
                  <p className="text-[12px] text-white/55">{cust.last_booked ? formatDate(cust.last_booked) : '—'}</p>
                  <p className="text-[10px] text-white/25 mt-0.5">آخر حجز</p>
                </div>
              </div>

              <ChevronLeft size={16} className="text-white/20 group-hover:text-emerald-400/60 transition-colors flex-shrink-0" aria-hidden />
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}

function Stat({ icon: Icon, value, label, danger }: {
  icon: React.ElementType; value: string; label: string; danger?: boolean;
}) {
  return (
    <div className="text-center min-w-[52px]">
      <p className={`text-[15px] font-bold flex items-center justify-center gap-1 ${danger ? 'text-red-400' : 'text-[#f0efe8]'}`}>
        <Icon size={12} className={danger ? 'text-red-400/70' : 'text-white/35'} aria-hidden />
        {value}
      </p>
      <p className="text-[10px] text-white/25 mt-0.5">{label}</p>
    </div>
  );
}
