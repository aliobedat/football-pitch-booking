'use client';

import { useState, useEffect, useCallback } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import api from '@/lib/api';
import Navbar from '@/components/Navbar';
import { useAuth } from '@/context/AuthContext';
import { CalendarDays, Clock, CreditCard, MapPin } from 'lucide-react';

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type BookingStatus = 'pending' | 'confirmed' | 'cancelled';

interface Booking {
  id: number;
  pitch_id: number;
  pitch_name: string;
  user_id: number;
  start_time: string;
  end_time: string;
  status: BookingStatus;
  total_price: number;
  created_at: string;
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

const STATUS_CONFIG: Record<BookingStatus, { label: string; classes: string }> = {
  confirmed: {
    label: 'مؤكد',
    classes: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400',
  },
  pending: {
    label: 'قيد الانتظار',
    classes: 'bg-amber-500/15 border-amber-500/30 text-amber-400',
  },
  cancelled: {
    label: 'ملغي',
    classes: 'bg-red-500/15 border-red-500/30 text-red-400',
  },
};

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('ar-JO', {
    weekday: 'long',
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  });
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('ar-JO', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: true,
  });
}

// ─────────────────────────────────────────────────────────────────────────────
// Booking card
// ─────────────────────────────────────────────────────────────────────────────

function BookingCard({
  booking,
  onCancel,
}: {
  booking: Booking;
  onCancel: (id: number) => void;
}) {
  const [isCancelling, setIsCancelling] = useState(false);

  const status = STATUS_CONFIG[booking.status] ?? STATUS_CONFIG.pending;

  // Only show the cancel button if the booking is active and hasn't started yet.
  const canCancel =
    (booking.status === 'pending' || booking.status === 'confirmed') &&
    new Date(booking.start_time) > new Date();

  const handleCancel = useCallback(async () => {
    setIsCancelling(true);
    try {
      await api.patch(`/bookings/${booking.id}/cancel`);
      onCancel(booking.id);
    } catch (err) {
      console.error('Cancel failed:', err);
      setIsCancelling(false);
    }
  }, [booking.id, onCancel]);

  return (
    <article
      className={[
        'group flex flex-col rounded-2xl overflow-hidden',
        'bg-[#141715]',
        'border border-white/[0.07] hover:border-white/[0.14]',
        'transition-all duration-300 ease-out hover:-translate-y-0.5',
      ].join(' ')}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-5 pt-5 pb-4 border-b border-white/[0.05]">
        <div>
          <p className="text-[10px] text-white/25 mb-0.5 tracking-wide">رقم الحجز</p>
          <p className="text-[13px] font-bold text-white/55 font-mono">
            #{String(booking.id).padStart(4, '0')}
          </p>
        </div>
        <span
          className={[
            'px-3 py-1 rounded-full text-[10px] font-bold tracking-wide border',
            status.classes,
          ].join(' ')}
        >
          {status.label}
        </span>
      </div>

      {/* Body */}
      <div className="flex flex-col gap-4 p-5 flex-1">
        {/* Pitch */}
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-emerald-500/10 border border-emerald-500/15 flex items-center justify-center flex-shrink-0">
            <MapPin size={13} className="text-emerald-400" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-white/25 mb-0.5">الملعب</p>
            <p className="text-[14px] font-semibold text-[#f0efe8] truncate">
              {booking.pitch_name || `ملعب #${booking.pitch_id}`}
            </p>
          </div>
        </div>

        {/* Date */}
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-white/[0.04] border border-white/[0.07] flex items-center justify-center flex-shrink-0">
            <CalendarDays size={13} className="text-white/35" aria-hidden="true" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-white/25 mb-0.5">التاريخ</p>
            <p className="text-[12px] font-medium text-white/60 truncate">
              {formatDate(booking.start_time)}
            </p>
          </div>
        </div>

        {/* Time range */}
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-white/[0.04] border border-white/[0.07] flex items-center justify-center flex-shrink-0">
            <Clock size={13} className="text-white/35" aria-hidden="true" />
          </div>
          <div>
            <p className="text-[10px] text-white/25 mb-0.5">الوقت</p>
            <p className="text-[12px] font-medium text-white/60">
              {formatTime(booking.start_time)}
              <span className="mx-1.5 text-white/20">—</span>
              {formatTime(booking.end_time)}
            </p>
          </div>
        </div>
      </div>

      {/* Footer */}
      <div className="px-5 pb-5">
        <div className="h-px bg-white/[0.05] mb-4" />

        {/* Price row */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1.5 text-white/25 text-[10px]">
            <CreditCard size={11} aria-hidden="true" />
            <span>إجمالي الحجز</span>
          </div>
          <div className="flex items-baseline gap-1">
            <span className="text-[20px] font-bold text-[#f0efe8] tracking-tight leading-none">
              {booking.total_price.toFixed(2)}
            </span>
            <span className="text-[11px] font-semibold text-emerald-500">دينار</span>
          </div>
        </div>

        {/* Cancel button — only for active, upcoming bookings */}
        {canCancel && (
          <button
            type="button"
            onClick={handleCancel}
            disabled={isCancelling}
            className={[
              'mt-3 w-full flex items-center justify-center gap-2',
              'py-2 rounded-xl',
              'text-[11px] font-semibold tracking-wide',
              'border border-red-500/[0.14] bg-red-500/[0.04] text-red-400/60',
              'hover:bg-red-500/[0.09] hover:border-red-500/30 hover:text-red-400',
              'disabled:opacity-40 disabled:cursor-not-allowed',
              'transition-all duration-200 active:scale-[0.98]',
              'focus-visible:outline-none focus-visible:ring-1',
              'focus-visible:ring-red-500/40 focus-visible:ring-offset-1',
              'focus-visible:ring-offset-[#141715]',
            ].join(' ')}
          >
            {isCancelling ? (
              <>
                <span
                  className="w-3 h-3 rounded-full border border-red-400/50 border-t-transparent animate-spin"
                  aria-hidden="true"
                />
                جاري الإلغاء...
              </>
            ) : (
              'إلغاء الحجز'
            )}
          </button>
        )}
      </div>
    </article>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Page
// ─────────────────────────────────────────────────────────────────────────────

export default function BookingsPage() {
  const { user, isLoading: authLoading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!authLoading && user?.role === 'owner') {
      router.replace('/dashboard');
    }
  }, [user, authLoading, router]);

  const [bookings, setBookings] = useState<Booking[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get('/bookings')
      .then((res) => {
        setBookings(res.data.data ?? []);
      })
      .catch((err) => {
        console.error('Failed to fetch bookings:', err);
        setError('تعذّر تحميل الحجوزات، يرجى المحاولة لاحقاً');
      })
      .finally(() => setIsLoading(false));
  }, []);

  // Flip a single booking's status to 'cancelled' in local state.
  const handleCancel = useCallback((id: number) => {
    setBookings((prev) =>
      prev.map((b) => (b.id === id ? { ...b, status: 'cancelled' as BookingStatus } : b))
    );
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

      {/* ── Page hero ────────────────────────────────────────────────────── */}
      <section className="max-w-7xl mx-auto px-6 pt-14 pb-10">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-emerald-500/20 bg-emerald-500/[0.06] mb-6">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
          <span className="text-[10px] font-bold tracking-wide text-emerald-400">
            سجل حجوزاتك
          </span>
        </div>
        <h1 className="text-3xl sm:text-4xl font-bold text-[#f0efe8] tracking-tight leading-tight">
          حجوزاتي
        </h1>
        {!error && bookings.length > 0 && (
          <p className="text-[13px] text-white/30 mt-2">
            {bookings.length === 1 ? 'حجز واحد نشط' : `${bookings.length} حجوزات`}
          </p>
        )}
      </section>

      {/* ── Main content ─────────────────────────────────────────────────── */}
      <main className="max-w-7xl mx-auto px-6 pb-20">

        {error ? (
          <div className="flex flex-col items-center justify-center py-28 gap-4">
            <div className="w-16 h-16 rounded-2xl bg-red-500/[0.06] border border-red-500/15 flex items-center justify-center">
              <span className="text-red-400 text-xl font-bold">!</span>
            </div>
            <p className="text-[14px] text-white/40">{error}</p>
          </div>
        ) : bookings.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-28 gap-6">
            <div className="w-20 h-20 rounded-2xl bg-white/[0.03] border border-white/[0.06] flex items-center justify-center">
              <CalendarDays size={28} className="text-white/15" aria-hidden="true" />
            </div>
            <div className="text-center">
              <p className="text-[16px] font-semibold text-white/45 mb-2">
                لا يوجد لديك حجوزات حالياً
              </p>
              <p className="text-[13px] text-white/22">
                ابدأ باستكشاف الملاعب المتاحة وقم بحجز ملعبك المفضل
              </p>
            </div>
            <Link
              href="/pitches"
              className={[
                'flex items-center gap-2 px-6 py-3 rounded-xl',
                'text-[13px] font-bold',
                'bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20',
                'hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40',
                'hover:shadow-[0_0_24px_rgba(16,185,129,0.12)]',
                'transition-all duration-200 active:scale-[0.97]',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500',
                'focus-visible:ring-offset-2 focus-visible:ring-offset-[#0d0f0e]',
              ].join(' ')}
            >
              تصفّح الملاعب
            </Link>
          </div>
        ) : (
          <div
            className="grid gap-5 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
            aria-label="قائمة الحجوزات"
          >
            {bookings.map((booking) => (
              <BookingCard key={booking.id} booking={booking} onCancel={handleCancel} />
            ))}
          </div>
        )}

      </main>

      {/* ── Footer ───────────────────────────────────────────────────────── */}
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
