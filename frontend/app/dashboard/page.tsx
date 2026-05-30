'use client';

import { useState, useEffect, useCallback, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import Navbar from '@/components/Navbar';
import {
  BookOpen, CheckCircle2, XCircle,
  X, CalendarDays, LayoutDashboard,
  MapPin, DollarSign, Plus, ChevronDown,
} from 'lucide-react';

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type BookingStatus = 'pending' | 'confirmed' | 'cancelled';
type ActiveTab     = 'bookings' | 'pitches';

interface AdminBooking {
  id:          number;
  pitch_id:    number;
  pitch_name:  string;
  user_id:     number;
  user_name:   string;
  user_email:  string;
  start_time:  string;
  end_time:    string;
  status:      BookingStatus;
  total_price: number;
  created_at:  string;
}

interface OwnerPitch {
  id:           number;
  owner_id:     number;
  name:         string;
  neighborhood: string;
  surface:      string;
  format:       string;
  pricePerHour: number;
  description:  string;
  image_url:    string;
  rating:       number;
  reviewCount:  number;
  isFeatured:   boolean;
  amenities:    string[];
  pitchHue:     string;
}

interface PitchForm {
  name:          string;
  neighborhood:  string;
  surface:       string;
  format:        string;
  price_per_hour: string;
  description:   string;
  image_url:     string;
}

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const EMPTY_FORM: PitchForm = {
  name: '', neighborhood: '', surface: 'artificial_grass',
  format: 'خماسي', price_per_hour: '', description: '', image_url: '',
};

const STATUS_CONFIG: Record<BookingStatus, { label: string; badge: string }> = {
  confirmed: { label: 'مؤكد',          badge: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400' },
  pending:   { label: 'قيد الانتظار',  badge: 'bg-amber-500/15 border-amber-500/30 text-amber-400'       },
  cancelled: { label: 'مرفوض',         badge: 'bg-red-500/15 border-red-500/30 text-red-400'             },
};

const SURFACE_LABEL: Record<string, string> = {
  artificial_grass: 'عشبية صناعية',
  natural_grass:    'عشبية طبيعية',
  futsal_court:     'ملعب فوتسال',
};

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

function fmtDate(iso: string) {
  return new Date(iso).toLocaleDateString('ar-JO', { weekday: 'short', month: 'short', day: 'numeric' });
}
function fmtTime(iso: string) {
  return new Date(iso).toLocaleTimeString('ar-JO', { hour: '2-digit', minute: '2-digit', hour12: true });
}

// ─────────────────────────────────────────────────────────────────────────────
// Shared input / label styles
// ─────────────────────────────────────────────────────────────────────────────

const inputCls = [
  'w-full bg-white/[0.04] border border-white/[0.09] rounded-xl px-4 py-3',
  'text-[13px] text-[#f0efe8] placeholder:text-white/25',
  'hover:border-white/[0.15] focus:outline-none',
  'focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/[0.12]',
  'transition-all duration-150',
].join(' ');

const labelCls = 'block text-[11px] font-semibold text-white/40 tracking-wide mb-1.5';

// ─────────────────────────────────────────────────────────────────────────────
// Stat card
// ─────────────────────────────────────────────────────────────────────────────

function StatCard({ icon: Icon, value, label, iconBg, iconColor, valueColor }: {
  icon: React.ElementType; value: number; label: string;
  iconBg: string; iconColor: string; valueColor: string;
}) {
  return (
    <div className="p-5 rounded-2xl bg-[#141715] border border-white/[0.07]">
      <div className={`w-9 h-9 rounded-xl flex items-center justify-center mb-4 ${iconBg}`}>
        <Icon size={16} className={iconColor} aria-hidden />
      </div>
      <p className={`text-[30px] font-bold tracking-tight leading-none mb-1 ${valueColor}`}>{value}</p>
      <p className="text-[12px] text-white/35">{label}</p>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Booking row
// ─────────────────────────────────────────────────────────────────────────────

function BookingRow({ booking }: { booking: AdminBooking }) {
  const { label, badge } = STATUS_CONFIG[booking.status] ?? STATUS_CONFIG.confirmed;

  return (
    <tr className="border-b border-white/[0.04] hover:bg-white/[0.018] transition-colors duration-150">
      <td className="px-5 py-4 text-start">
        <span className="text-[12px] font-bold text-white/45 font-mono">
          #{String(booking.id).padStart(4, '0')}
        </span>
      </td>
      <td className="px-5 py-4 text-start">
        <p className="text-[13px] font-semibold text-[#f0efe8] leading-snug">{booking.user_name || '—'}</p>
        <p className="text-[11px] text-white/30 mt-0.5 truncate max-w-[160px]">{booking.user_email}</p>
      </td>
      <td className="px-5 py-4 text-start">
        <span className="text-[13px] text-white/65">{booking.pitch_name || `ملعب #${booking.pitch_id}`}</span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[12px] text-white/50">{fmtDate(booking.start_time)}</span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[12px] text-white/50">
          {fmtTime(booking.start_time)}
          <span className="mx-1 text-white/20">—</span>
          {fmtTime(booking.end_time)}
        </span>
      </td>
      <td className="px-5 py-4 text-start whitespace-nowrap">
        <span className="text-[13px] font-semibold text-[#f0efe8]">{booking.total_price.toFixed(2)}</span>
        <span className="text-[10px] text-emerald-500 ms-1">د.أ</span>
      </td>
      <td className="px-5 py-4 text-start">
        <span className={`inline-flex items-center px-2.5 py-1 rounded-full text-[10px] font-bold tracking-wide border ${badge}`}>
          {label}
        </span>
      </td>
    </tr>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Owner pitch card
// ─────────────────────────────────────────────────────────────────────────────

function PitchCard({ pitch }: { pitch: OwnerPitch }) {
  return (
    <article className="flex flex-col rounded-2xl overflow-hidden bg-[#141715] border border-white/[0.07] hover:border-white/[0.14] transition-all duration-300 hover:-translate-y-0.5">
      {/* Colour strip */}
      <div className="h-1.5 w-full" style={{ background: pitch.pitchHue.replace(/[\d.]+\)$/, '0.8)') }} />

      <div className="p-5 flex flex-col gap-4 flex-1">
        {/* Name + featured badge */}
        <div className="flex items-start justify-between gap-2">
          <h3 className="text-[15px] font-bold text-[#f0efe8] tracking-tight leading-snug">{pitch.name}</h3>
          {pitch.isFeatured && (
            <span className="flex-shrink-0 px-2 py-0.5 rounded-md text-[9px] font-bold bg-emerald-500/15 border border-emerald-500/25 text-emerald-400">
              مميّز
            </span>
          )}
        </div>

        {/* Location */}
        <div className="flex items-center gap-1.5 text-white/40">
          <MapPin size={11} aria-hidden />
          <span className="text-[11px]">{pitch.neighborhood}، عمّان</span>
        </div>

        {/* Tags */}
        <div className="flex flex-wrap gap-1.5">
          <span className="px-2.5 py-1 rounded-full text-[10px] bg-white/[0.04] border border-white/[0.07] text-white/45">
            {pitch.format}
          </span>
          <span className="px-2.5 py-1 rounded-full text-[10px] bg-white/[0.04] border border-white/[0.07] text-white/45">
            {SURFACE_LABEL[pitch.surface] ?? pitch.surface}
          </span>
        </div>

        {pitch.description && (
          <p className="text-[12px] text-white/35 leading-relaxed line-clamp-2">{pitch.description}</p>
        )}
      </div>

      {/* Footer */}
      <div className="px-5 pb-5">
        <div className="h-px bg-white/[0.05] mb-4" />
        <div className="flex items-baseline gap-1">
          <span className="text-[22px] font-bold text-[#f0efe8] tracking-tight">{pitch.pricePerHour}</span>
          <span className="text-[12px] font-semibold text-emerald-500">دينار / ساعة</span>
        </div>
      </div>
    </article>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Add Pitch form
// ─────────────────────────────────────────────────────────────────────────────

function AddPitchForm({
  onSuccess,
  onCancel,
}: {
  onSuccess: (pitch: OwnerPitch) => void;
  onCancel:  () => void;
}) {
  const [form, setForm]           = useState<PitchForm>(EMPTY_FORM);
  const [isSubmitting, setSubmit] = useState(false);
  const [error, setError]         = useState<string | null>(null);

  const set = (field: keyof PitchForm) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
      setForm(prev => ({ ...prev, [field]: e.target.value }));

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmit(true);
    try {
      const res = await api.post('/pitches', {
        ...form,
        price_per_hour: Number(form.price_per_hour),
      });
      onSuccess(res.data.data as OwnerPitch);
    } catch (err: any) {
      setError(err?.response?.data?.message ?? 'تعذّر إنشاء الملعب، يرجى المحاولة مجدداً');
      setSubmit(false);
    }
  };

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.07] mb-6 overflow-hidden">
      {/* Form header */}
      <div className="px-6 py-4 border-b border-white/[0.06] flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
            <Plus size={13} className="text-emerald-400" aria-hidden />
          </div>
          <span className="text-[13px] font-semibold text-[#f0efe8]">إضافة ملعب جديد</span>
        </div>
        <button
          type="button"
          onClick={onCancel}
          className="text-white/25 hover:text-white/55 transition-colors duration-150"
          aria-label="إغلاق النموذج"
        >
          <X size={16} aria-hidden />
        </button>
      </div>

      <form onSubmit={handleSubmit} className="p-6">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 mb-5">
          {/* Name */}
          <div>
            <label className={labelCls}>اسم الملعب <span className="text-red-400/60">*</span></label>
            <input
              type="text"
              value={form.name}
              onChange={set('name')}
              required
              placeholder="مثال: ملعب الحسين"
              className={inputCls}
            />
          </div>

          {/* Neighborhood */}
          <div>
            <label className={labelCls}>الحي / الموقع <span className="text-red-400/60">*</span></label>
            <input
              type="text"
              value={form.neighborhood}
              onChange={set('neighborhood')}
              required
              placeholder="مثال: خلدا"
              className={inputCls}
            />
          </div>

          {/* Surface */}
          <div className="relative">
            <label className={labelCls}>نوع الأرضية <span className="text-red-400/60">*</span></label>
            <select value={form.surface} onChange={set('surface')} className={`${inputCls} appearance-none pe-9`}>
              <option value="artificial_grass">عشبية صناعية</option>
              <option value="natural_grass">عشبية طبيعية</option>
              <option value="futsal_court">ملعب فوتسال</option>
            </select>
            <ChevronDown size={13} className="absolute end-3 bottom-[13px] text-white/25 pointer-events-none" aria-hidden />
          </div>

          {/* Format */}
          <div className="relative">
            <label className={labelCls}>صيغة اللعب <span className="text-red-400/60">*</span></label>
            <select value={form.format} onChange={set('format')} className={`${inputCls} appearance-none pe-9`}>
              <option value="خماسي">خماسي (5v5)</option>
              <option value="سباعي">سباعي (7v7)</option>
            </select>
            <ChevronDown size={13} className="absolute end-3 bottom-[13px] text-white/25 pointer-events-none" aria-hidden />
          </div>

          {/* Price */}
          <div>
            <label className={labelCls}>السعر بالساعة (دينار) <span className="text-red-400/60">*</span></label>
            <div className="relative">
              <input
                type="number"
                min="1"
                value={form.price_per_hour}
                onChange={set('price_per_hour')}
                required
                placeholder="25"
                className={`${inputCls} pe-12`}
              />
              <span className="absolute end-4 top-1/2 -translate-y-1/2 text-[11px] text-emerald-500 font-semibold pointer-events-none">
                د.أ
              </span>
            </div>
          </div>

          {/* Image URL */}
          <div>
            <label className={labelCls}>رابط الصورة</label>
            <input
              type="url"
              value={form.image_url}
              onChange={set('image_url')}
              placeholder="https://..."
              className={inputCls}
            />
          </div>
        </div>

        {/* Description — full width */}
        <div className="mb-6">
          <label className={labelCls}>الوصف</label>
          <textarea
            value={form.description}
            onChange={set('description')}
            rows={3}
            placeholder="صف الملعب: المرافق، الإضاءة، موقف السيارات..."
            className={`${inputCls} resize-none`}
          />
        </div>

        {error && (
          <p className="text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-3 mb-4">
            {error}
          </p>
        )}

        {/* Actions */}
        <div className="flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/40 hover:text-white/65 border border-white/[0.07] hover:border-white/[0.14] transition-all duration-150"
          >
            إلغاء
          </button>
          <button
            type="submit"
            disabled={isSubmitting}
            className={[
              'flex items-center gap-2 px-6 py-2.5 rounded-xl',
              'text-[12px] font-bold',
              'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
              'hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40',
              'hover:shadow-[0_0_20px_rgba(16,185,129,0.12)]',
              'disabled:opacity-50 disabled:cursor-not-allowed',
              'transition-all duration-200 active:scale-[0.97]',
            ].join(' ')}
          >
            {isSubmitting ? (
              <>
                <span className="w-3.5 h-3.5 rounded-full border-2 border-emerald-400/50 border-t-transparent animate-spin" aria-hidden />
                جاري الإضافة...
              </>
            ) : (
              <>إضافة الملعب</>
            )}
          </button>
        </div>
      </form>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Page
// ─────────────────────────────────────────────────────────────────────────────

export default function DashboardPage() {
  const router = useRouter();
  const { user, isLoading: authLoading } = useAuth();

  // Client-side role guard (mirrors the edge middleware in middleware.ts).
  // Guard: only 'owner' and 'admin' may access the dashboard
  useEffect(() => {
    if (!authLoading && (!user || (user.role !== 'owner' && user.role !== 'admin'))) {
      router.replace(user ? '/pitches' : '/login');
    }
  }, [user, authLoading, router]);

  const [activeTab, setActiveTab] = useState<ActiveTab>('bookings');

  // ── bookings state ──────────────────────────────────────────────────────
  const [bookings,       setBookings]       = useState<AdminBooking[]>([]);
  const [bookingsLoading, setBookingsLoading] = useState(true);
  const [bookingsError,  setBookingsError]  = useState<string | null>(null);

  // ── pitches state ───────────────────────────────────────────────────────
  const [pitches,        setPitches]        = useState<OwnerPitch[]>([]);
  const [pitchesLoading, setPitchesLoading] = useState(false);
  const [pitchesLoaded,  setPitchesLoaded]  = useState(false);
  const [pitchesError,   setPitchesError]   = useState<string | null>(null);
  const [showAddForm,    setShowAddForm]    = useState(false);

  // ── fetch bookings on mount ─────────────────────────────────────────────
  useEffect(() => {
    api.get('/admin/bookings')
      .then(res  => setBookings(res.data.data ?? []))
      .catch(()  => setBookingsError('تعذّر تحميل البيانات. تأكد من صلاحيات الحساب.'))
      .finally(() => setBookingsLoading(false));
  }, []);

  // ── fetch pitches on first visit to the tab ─────────────────────────────
  useEffect(() => {
    if (activeTab !== 'pitches' || pitchesLoaded) return;
    setPitchesLoading(true);
    api.get('/owner/pitches')
      .then(res  => { setPitches(res.data.data ?? []); setPitchesLoaded(true); })
      .catch(()  => setPitchesError('تعذّر تحميل الملاعب.'))
      .finally(() => setPitchesLoading(false));
  }, [activeTab, pitchesLoaded]);

  const handlePitchAdded = useCallback((pitch: OwnerPitch) => {
    setPitches(prev => [pitch, ...prev]);
    setShowAddForm(false);
  }, []);

  const stats = useMemo(() => ({
    total:     bookings.length,
    confirmed: bookings.filter(b => b.status === 'confirmed').length,
    cancelled: bookings.filter(b => b.status === 'cancelled').length,
  }), [bookings]);

  // ── global loading / access guard ──────────────────────────────────────
  if (authLoading || !user || (user.role !== 'owner' && user.role !== 'admin')) {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  if (bookingsLoading && activeTab === 'bookings') {
    return (
      <div className="min-h-screen bg-[#0d0f0e] flex items-center justify-center">
        <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
      </div>
    );
  }

  return (
    <div dir="rtl" className="min-h-screen bg-[#0d0f0e]">

      <Navbar />

      {/* ── Page hero + tab switcher ──────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 pt-14 pb-0">
        <div className="flex items-center gap-2.5 mb-5">
          <div className="w-8 h-8 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
            <LayoutDashboard size={14} className="text-emerald-400" aria-hidden />
          </div>
          <span className="text-[11px] font-bold tracking-widest text-emerald-400 uppercase">لوحة التحكم</span>
        </div>

        <div className="flex items-end justify-between gap-4 flex-wrap">
          <div>
            <h1 className="text-3xl sm:text-4xl font-bold text-[#f0efe8] tracking-tight leading-tight">
              {activeTab === 'bookings' ? 'إدارة الحجوزات' : 'ملاعبي'}
            </h1>
            <p className="text-[13px] text-white/35 mt-1.5">
              {activeTab === 'bookings'
                ? 'استعراض الحجوزات الواردة على ملاعبك وإجراء التأكيد أو الرفض'
                : 'إدارة ملاعبك وإضافة ملاعب جديدة للمنصة'}
            </p>
          </div>

          {/* Tab pills */}
          <div className="flex items-center gap-1 p-1 rounded-xl bg-white/[0.04] border border-white/[0.07] mb-1">
            {([
              { key: 'bookings', label: 'الحجوزات'  },
              { key: 'pitches',  label: 'ملاعبي'    },
            ] as const).map(tab => (
              <button
                key={tab.key}
                type="button"
                onClick={() => setActiveTab(tab.key)}
                className={[
                  'px-5 py-2 rounded-lg text-[12px] font-semibold transition-all duration-150',
                  activeTab === tab.key
                    ? 'bg-emerald-500/15 border border-emerald-500/30 text-emerald-400'
                    : 'text-white/40 hover:text-white/65',
                ].join(' ')}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>

        <div className="h-px bg-white/[0.05] mt-6" />
      </section>

      {/* ── Content ──────────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 py-8 pb-20">

        {/* ═══════════════════════════════════════════════════════════ BOOKINGS */}
        {activeTab === 'bookings' && (
          <>
            {bookingsError ? (
              <div className="flex flex-col items-center justify-center py-24 gap-4">
                <div className="w-16 h-16 rounded-2xl bg-red-500/[0.06] border border-red-500/15 flex items-center justify-center">
                  <span className="text-red-400 text-xl font-bold">!</span>
                </div>
                <p className="text-[14px] text-white/40">{bookingsError}</p>
              </div>
            ) : (
              <>
                {/* Stats row */}
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-8">
                  <StatCard icon={BookOpen}     value={stats.total}     label="إجمالي الحجوزات" iconBg="bg-white/[0.05]"    iconColor="text-white/50"    valueColor="text-[#f0efe8]"   />
                  <StatCard icon={CheckCircle2} value={stats.confirmed} label="مؤكدة"            iconBg="bg-emerald-500/10"  iconColor="text-emerald-400" valueColor="text-emerald-400" />
                  <StatCard icon={XCircle}      value={stats.cancelled} label="مرفوضة / ملغاة"  iconBg="bg-red-500/[0.08]" iconColor="text-red-400"     valueColor="text-red-400"     />
                </div>

                {bookings.length === 0 ? (
                  <div className="flex flex-col items-center justify-center py-24 gap-5">
                    <div className="w-20 h-20 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
                      <CalendarDays size={28} className="text-white/15" aria-hidden />
                    </div>
                    <div className="text-center">
                      <p className="text-[16px] font-semibold text-white/45 mb-1">لا توجد حجوزات بعد</p>
                      <p className="text-[13px] text-white/25">ستظهر هنا الحجوزات الواردة على ملاعبك</p>
                    </div>
                  </div>
                ) : (
                  <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
                    <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110] flex items-center justify-between">
                      <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">الحجوزات الواردة</p>
                      <p className="text-[11px] text-white/25">{bookings.length} {bookings.length === 1 ? 'حجز' : 'حجوزات'}</p>
                    </div>
                    <div className="overflow-x-auto">
                      <table className="w-full min-w-[860px] bg-[#141715]">
                        <thead>
                          <tr className="border-b border-white/[0.06] bg-[#111312]">
                            {['# الحجز', 'اللاعب', 'الملعب', 'التاريخ', 'الوقت', 'المبلغ', 'الحالة'].map(col => (
                              <th key={col} className="px-5 py-3.5 text-start text-[10px] font-semibold text-white/30 tracking-widest uppercase whitespace-nowrap">
                                {col}
                              </th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {bookings.map(b => (
                            <BookingRow key={b.id} booking={b} />
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
              </>
            )}
          </>
        )}

        {/* ════════════════════════════════════════════════════════════ PITCHES */}
        {activeTab === 'pitches' && (
          <>
            {/* Pitches header */}
            {!showAddForm && (
              <div className="flex items-center justify-between mb-6">
                <p className="text-[13px] text-white/35">
                  {pitchesLoaded
                    ? `${pitches.length} ${pitches.length === 1 ? 'ملعب' : 'ملاعب'}`
                    : ''}
                </p>
                <button
                  type="button"
                  onClick={() => setShowAddForm(true)}
                  className={[
                    'flex items-center gap-2 px-5 py-2.5 rounded-xl',
                    'text-[12px] font-bold',
                    'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
                    'hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40',
                    'hover:shadow-[0_0_20px_rgba(16,185,129,0.12)]',
                    'transition-all duration-200 active:scale-[0.97]',
                  ].join(' ')}
                >
                  <Plus size={14} aria-hidden />
                  إضافة ملعب جديد
                </button>
              </div>
            )}

            {/* Add form */}
            {showAddForm && (
              <AddPitchForm onSuccess={handlePitchAdded} onCancel={() => setShowAddForm(false)} />
            )}

            {/* Loading */}
            {pitchesLoading && (
              <div className="flex items-center justify-center py-24">
                <div className="w-6 h-6 rounded-full border-2 border-emerald-500 border-t-transparent animate-spin" />
              </div>
            )}

            {/* Error */}
            {pitchesError && !pitchesLoading && (
              <div className="flex flex-col items-center py-20 gap-4">
                <p className="text-[14px] text-white/40">{pitchesError}</p>
              </div>
            )}

            {/* Empty */}
            {!pitchesLoading && !pitchesError && pitches.length === 0 && (
              <div className="flex flex-col items-center justify-center py-24 gap-6">
                <div className="w-20 h-20 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
                  <MapPin size={28} className="text-white/15" aria-hidden />
                </div>
                <div className="text-center">
                  <p className="text-[16px] font-semibold text-white/45 mb-2">لا توجد ملاعب مضافة بعد</p>
                  <p className="text-[13px] text-white/25">أضف ملعبك الأول لتبدأ باستقبال الحجوزات</p>
                </div>
                {!showAddForm && (
                  <button
                    type="button"
                    onClick={() => setShowAddForm(true)}
                    className="flex items-center gap-2 px-6 py-3 rounded-xl text-[13px] font-bold bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20 hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40 transition-all duration-200 active:scale-[0.97]"
                  >
                    <Plus size={14} aria-hidden />
                    إضافة ملعب جديد
                  </button>
                )}
              </div>
            )}

            {/* Grid */}
            {!pitchesLoading && pitches.length > 0 && (
              <div className="grid gap-5 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
                {pitches.map(p => <PitchCard key={p.id} pitch={p} />)}
              </div>
            )}
          </>
        )}

      </main>

      {/* ── Footer ───────────────────────────────────────────────────────── */}
      <footer className="border-t border-white/[0.05] py-8">
        <div className="max-w-7xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-6">
            {['الخصوصية', 'الشروط', 'تواصل معنا'].map((item, i) => (
              <Link key={item} href={`/${(['privacy', 'terms', 'contact'] as const)[i]}`}
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
