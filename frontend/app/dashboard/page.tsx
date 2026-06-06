'use client';

import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import Navbar from '@/components/Navbar';
import PitchImageDropzone, { type PitchImageValue } from '@/components/PitchImageDropzone';
import {
  BookOpen, CheckCircle2, XCircle,
  X, CalendarDays, LayoutDashboard,
  MapPin, Plus, ChevronDown,
  Ban, AlertTriangle, Pencil, Trash2, CalendarSearch, Users,
} from 'lucide-react';
import { formatNumber, formatCurrency, formatDate, formatTime } from '@/lib/format';

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

type BookingStatus = 'pending' | 'confirmed' | 'cancelled';
type ActiveTab     = 'bookings' | 'pitches';

interface AdminBooking {
  id:          number;
  pitch_id:    number;
  pitch_name:  string;
  player_id:   number;
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
  rating:       number;
  reviewCount:  number;
  isFeatured:   boolean;
  isActive:     boolean;
  amenities:    string[];
  pitchHue:     string;
  image_url:       string;
  image_public_id: string;
}

interface PitchForm {
  name:          string;
  neighborhood:  string;
  surface:       string;
  format:        string;
  price_per_hour: string;
  description:   string;
  image_url:       string;
  image_public_id: string;
}

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const EMPTY_FORM: PitchForm = {
  name: '', neighborhood: '', surface: 'artificial_grass',
  format: 'خماسي', price_per_hour: '', description: '', image_url: '', image_public_id: '',
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

// Date/time rendering goes through lib/format so digits stay Latin (0–9) while
// month/weekday names remain Arabic. See lib/format.ts.
const fmtDate = (iso: string) => formatDate(iso);
const fmtTime = (iso: string) => formatTime(iso);

// ─────────────────────────────────────────────────────────────────────────────
// Shared input / label styles
// ─────────────────────────────────────────────────────────────────────────────

const inputCls = [
  'w-full bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-3',
  'text-[13px] text-[#f0efe8] placeholder:text-white/25',
  'hover:border-white/[0.22] focus:outline-none',
  'focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20',
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

function BookingRow({
  booking,
  onRequestCancel,
}: {
  booking: AdminBooking;
  onRequestCancel: (booking: AdminBooking) => void;
}) {
  const { label, badge } = STATUS_CONFIG[booking.status] ?? STATUS_CONFIG.confirmed;

  // An owner may only cancel a booking that is still active (pending/confirmed).
  // Already-cancelled rows expose no action.
  const canCancel = booking.status === 'pending' || booking.status === 'confirmed';

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
        <span className="text-[13px] font-semibold text-[#f0efe8]">{formatCurrency(booking.total_price, { minimumFractionDigits: 2 })}</span>
        <span className="text-[10px] text-emerald-500 ms-1">د.أ</span>
      </td>
      <td className="px-5 py-4 text-start">
        <span className={`inline-flex items-center px-2.5 py-1 rounded-full text-[10px] font-bold tracking-wide border ${badge}`}>
          {label}
        </span>
      </td>
      {/* Actions — rendered last so, under dir="rtl", it sits left-most. */}
      <td className="px-5 py-4 text-start">
        {canCancel ? (
          <button
            type="button"
            onClick={() => onRequestCancel(booking)}
            className={[
              'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg',
              'text-[11px] font-semibold tracking-wide',
              'border border-red-500/[0.14] bg-red-500/[0.04] text-red-400/70',
              'hover:bg-red-500/[0.09] hover:border-red-500/30 hover:text-red-400',
              'transition-all duration-200 active:scale-[0.97]',
              'focus-visible:outline-none focus-visible:ring-1',
              'focus-visible:ring-red-500/40 focus-visible:ring-offset-1',
              'focus-visible:ring-offset-[#141715]',
            ].join(' ')}
            aria-label={`إلغاء الحجز رقم ${String(booking.id).padStart(4, '0')}`}
          >
            <Ban size={12} aria-hidden />
            إلغاء
          </button>
        ) : (
          <span className="text-[12px] text-white/20">—</span>
        )}
      </td>
    </tr>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Cancel-booking confirmation modal
//
// Accessibility: role="dialog" + aria-modal, labelled by its title/description.
// Handles Esc to dismiss, backdrop click to dismiss, and a focus trap that keeps
// Tab/Shift+Tab cycling within the dialog. Focus is moved to the dialog on open
// and restored to the trigger on close.
// ─────────────────────────────────────────────────────────────────────────────

function CancelModal({
  booking,
  isCancelling,
  error,
  onConfirm,
  onClose,
}: {
  booking: AdminBooking;
  isCancelling: boolean;
  error: string | null;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialogRef  = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  // Move focus into the dialog on open; restore it to the previously focused
  // element (the trigger button) on unmount.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    confirmRef.current?.focus();
    return () => previouslyFocused?.focus();
  }, []);

  // Esc to close + focus trap on Tab. A request in flight locks the dialog.
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (!isCancelling) onClose();
        return;
      }
      if (e.key !== 'Tab') return;

      const focusables = dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (!focusables || focusables.length === 0) return;

      const first = focusables[0];
      const last  = focusables[focusables.length - 1];

      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    },
    [isCancelling, onClose],
  );

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      onKeyDown={handleKeyDown}
    >
      {/* Backdrop — click to dismiss (disabled while a request is in flight). */}
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!isCancelling) onClose(); }}
        aria-hidden
      />

      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="cancel-modal-title"
        aria-describedby="cancel-modal-desc"
        className="relative w-full max-w-md rounded-2xl bg-[#141715] border border-white/[0.09] shadow-2xl overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
          <div className="w-10 h-10 rounded-xl bg-red-500/[0.08] border border-red-500/20 flex items-center justify-center flex-shrink-0">
            <AlertTriangle size={18} className="text-red-400" aria-hidden />
          </div>
          <div className="min-w-0">
            <h2 id="cancel-modal-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">
              تأكيد إلغاء الحجز
            </h2>
            <p id="cancel-modal-desc" className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
              سيتم إلغاء الحجز رقم{' '}
              <span className="font-mono font-bold text-white/60">
                #{String(booking.id).padStart(4, '0')}
              </span>{' '}
              لـ <span className="text-white/60 font-semibold">{booking.user_name || 'اللاعب'}</span>{' '}
              وإتاحة الموعد من جديد. لا يمكن التراجع عن هذا الإجراء.
            </p>
          </div>
        </div>

        {error && (
          <div className="mx-6 mb-1 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
            {error}
          </div>
        )}

        {/* Actions */}
        <div className="flex items-center justify-end gap-3 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
          <button
            type="button"
            onClick={onClose}
            disabled={isCancelling}
            className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
          >
            تراجع
          </button>
          <button
            ref={confirmRef}
            type="button"
            onClick={onConfirm}
            disabled={isCancelling}
            className={[
              'flex items-center gap-2 px-6 py-2.5 rounded-xl',
              'text-[12px] font-bold',
              'bg-red-500/[0.12] text-red-400 border border-red-500/25',
              'hover:bg-red-500/[0.18] hover:text-red-300 hover:border-red-500/40',
              'disabled:opacity-60 disabled:cursor-not-allowed',
              'transition-all duration-200 active:scale-[0.97]',
            ].join(' ')}
          >
            {isCancelling ? (
              <>
                <span className="w-3.5 h-3.5 rounded-full border-2 border-red-400/50 border-t-transparent animate-spin" aria-hidden />
                جاري الإلغاء...
              </>
            ) : (
              <>
                <Ban size={13} aria-hidden />
                تأكيد الإلغاء
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Delete-pitch confirmation modal
//
// Same a11y contract as CancelModal (role=dialog, Esc/backdrop dismiss, focus
// trap, focus restore). Deletion is irreversible, so it is always gated behind
// this dialog — never a single click.
// ─────────────────────────────────────────────────────────────────────────────

function DeletePitchModal({
  pitch,
  isDeleting,
  error,
  onConfirm,
  onClose,
}: {
  pitch: OwnerPitch;
  isDeleting: boolean;
  error: string | null;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const dialogRef  = useRef<HTMLDivElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    confirmRef.current?.focus();
    return () => previouslyFocused?.focus();
  }, []);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (!isDeleting) onClose();
        return;
      }
      if (e.key !== 'Tab') return;

      const focusables = dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      if (!focusables || focusables.length === 0) return;

      const first = focusables[0];
      const last  = focusables[focusables.length - 1];

      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    },
    [isDeleting, onClose],
  );

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4" onKeyDown={handleKeyDown}>
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={() => { if (!isDeleting) onClose(); }}
        aria-hidden
      />

      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="delete-pitch-title"
        aria-describedby="delete-pitch-desc"
        className="relative w-full max-w-md rounded-2xl bg-[#141715] border border-white/[0.10] shadow-2xl overflow-hidden"
      >
        <div className="flex items-start gap-3.5 px-6 pt-6 pb-4">
          <div className="w-10 h-10 rounded-xl bg-red-500/[0.08] border border-red-500/20 flex items-center justify-center flex-shrink-0">
            <Trash2 size={18} className="text-red-400" aria-hidden />
          </div>
          <div className="min-w-0">
            <h2 id="delete-pitch-title" className="text-[15px] font-bold text-[#f0efe8] leading-snug">
              تأكيد حذف الملعب
            </h2>
            <p id="delete-pitch-desc" className="text-[12.5px] text-white/40 mt-1 leading-relaxed">
              سيتم حذف ملعب{' '}
              <span className="text-white/60 font-semibold">{pitch.name}</span>{' '}
              نهائياً. لا يمكن التراجع عن هذا الإجراء.
            </p>
          </div>
        </div>

        {error && (
          <div className="mx-6 mb-1 text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-2.5">
            {error}
          </div>
        )}

        <div className="flex items-center justify-end gap-3 px-6 py-5 mt-1 border-t border-white/[0.05] bg-[#111312]">
          <button
            type="button"
            onClick={onClose}
            disabled={isDeleting}
            className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/[0.07] hover:border-white/[0.14] disabled:opacity-50 disabled:cursor-not-allowed transition-all duration-150"
          >
            تراجع
          </button>
          <button
            ref={confirmRef}
            type="button"
            onClick={onConfirm}
            disabled={isDeleting}
            className={[
              'flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold',
              'bg-red-500/[0.12] text-red-400 border border-red-500/25',
              'hover:bg-red-500/[0.18] hover:text-red-300 hover:border-red-500/40',
              'disabled:opacity-60 disabled:cursor-not-allowed',
              'transition-all duration-200 active:scale-[0.97]',
            ].join(' ')}
          >
            {isDeleting ? (
              <>
                <span className="w-3.5 h-3.5 rounded-full border-2 border-red-400/50 border-t-transparent animate-spin" aria-hidden />
                جاري الحذف...
              </>
            ) : (
              <>
                <Trash2 size={13} aria-hidden />
                تأكيد الحذف
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Owner pitch card
// ─────────────────────────────────────────────────────────────────────────────

function PitchCard({
  pitch,
  onEdit,
  onRequestDelete,
  onViewBookings,
  onToggleActive,
  isToggling,
}: {
  pitch: OwnerPitch;
  onEdit: (pitch: OwnerPitch) => void;
  onRequestDelete: (pitch: OwnerPitch) => void;
  onViewBookings: () => void;
  onToggleActive: (pitch: OwnerPitch) => void;
  isToggling: boolean;
}) {
  // The top border is bound to STATUS: emerald = متاح (active), grey = معطّل
  // (inactive). Featured is surfaced as a separate corner badge.
  const topBorder = pitch.isActive ? 'bg-emerald-500/70' : 'bg-white/[0.12]';

  const actionBtn = [
    'inline-flex items-center justify-center gap-1.5 flex-1 px-3 py-2 rounded-lg',
    'text-[11px] font-semibold tracking-wide border transition-all duration-150',
    'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-offset-1 focus-visible:ring-offset-[#141715]',
  ].join(' ');

  return (
    <article
      className={[
        'flex flex-col rounded-2xl overflow-hidden bg-[#141715] border transition-all duration-300',
        pitch.isActive
          ? 'border-white/[0.10] hover:border-white/[0.20] hover:-translate-y-0.5'
          : 'border-white/[0.06] opacity-60 hover:opacity-90', // معطّل → dimmed/muted
      ].join(' ')}
    >
      {/* Status-bound top border */}
      <div className={`h-1.5 w-full ${topBorder}`} />

      <div className="p-5 flex flex-col gap-4 flex-1">
        {/* Name + status / featured badges */}
        <div className="flex items-start justify-between gap-2">
          <h3 className="text-[15px] font-bold text-[#f0efe8] tracking-tight leading-snug">{pitch.name}</h3>
          <div className="flex items-center gap-1.5 flex-shrink-0">
            {pitch.isFeatured && (
              <span className="px-2 py-0.5 rounded-md text-[9px] font-bold bg-amber-500/15 border border-amber-500/25 text-amber-400">
                مميّز
              </span>
            )}
            {/* Status badge — متاح / معطّل */}
            <span
              className={[
                'px-2 py-0.5 rounded-md text-[9px] font-bold border',
                pitch.isActive
                  ? 'bg-emerald-500/15 border-emerald-500/25 text-emerald-400'
                  : 'bg-white/[0.05] border-white/[0.12] text-white/45',
              ].join(' ')}
            >
              {pitch.isActive ? 'متاح' : 'معطّل'}
            </span>
          </div>
        </div>

        {/* Location */}
        <div className="flex items-center gap-1.5 text-white/40">
          <MapPin size={11} aria-hidden />
          <span className="text-[11px]">{pitch.neighborhood}، عمّان</span>
        </div>

        {/* Tags — نوع الملعب (format) chip emphasised with an icon, beside surface */}
        <div className="flex flex-wrap gap-1.5">
          <span className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full text-[10px] font-semibold bg-emerald-500/10 border border-emerald-500/25 text-emerald-300/90">
            <Users size={10} aria-hidden />
            {pitch.format}
          </span>
          <span className="px-2.5 py-1 rounded-full text-[10px] bg-white/[0.04] border border-white/[0.09] text-white/45">
            {SURFACE_LABEL[pitch.surface] ?? pitch.surface}
          </span>
        </div>

        {pitch.description && (
          <p className="text-[12px] text-white/35 leading-relaxed line-clamp-2">{pitch.description}</p>
        )}
      </div>

      {/* Footer — price is the primary datum, so it's the largest/heaviest text */}
      <div className="px-5 pb-5 mt-auto">
        <div className="h-px bg-white/[0.06] mb-4" />
        <div className="flex items-baseline gap-1.5 mb-4">
          <span className="text-[28px] font-extrabold text-[#f0efe8] tracking-tight leading-none">
            {formatCurrency(pitch.pricePerHour)}
          </span>
          <span className="text-[12px] font-semibold text-emerald-500">دينار / ساعة</span>
        </div>

        {/* Activate / deactivate switch */}
        <div className="flex items-center justify-between mb-3 px-0.5">
          <span className="text-[11px] font-semibold text-white/45">
            {pitch.isActive ? 'الملعب متاح للحجز' : 'الملعب معطّل'}
          </span>
          <button
            type="button"
            role="switch"
            aria-checked={pitch.isActive}
            aria-label={pitch.isActive ? `تعطيل ملعب ${pitch.name}` : `تفعيل ملعب ${pitch.name}`}
            disabled={isToggling}
            onClick={() => onToggleActive(pitch)}
            dir="ltr"
            className={[
              'relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full px-0.5 transition-colors duration-200',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/40 focus-visible:ring-offset-1 focus-visible:ring-offset-[#141715]',
              'disabled:cursor-not-allowed disabled:opacity-60',
              pitch.isActive ? 'bg-emerald-500/80' : 'bg-white/[0.15]',
            ].join(' ')}
          >
            <span
              className={[
                'inline-flex items-center justify-center h-4 w-4 transform rounded-full bg-white shadow transition-transform duration-200',
                pitch.isActive ? 'translate-x-[16px]' : 'translate-x-0',
              ].join(' ')}
            >
              {isToggling && (
                <span className="h-2.5 w-2.5 rounded-full border-2 border-emerald-500/60 border-t-transparent animate-spin" aria-hidden />
              )}
            </span>
          </button>
        </div>

        {/* Management actions */}
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => onEdit(pitch)}
            className={`${actionBtn} bg-white/[0.03] border-white/[0.10] text-white/60 hover:bg-white/[0.07] hover:border-white/[0.20] hover:text-white/80 focus-visible:ring-white/30`}
            aria-label={`تعديل ملعب ${pitch.name}`}
          >
            <Pencil size={12} aria-hidden />
            تعديل
          </button>
          <button
            type="button"
            onClick={onViewBookings}
            className={`${actionBtn} bg-white/[0.03] border-white/[0.10] text-white/60 hover:bg-white/[0.07] hover:border-white/[0.20] hover:text-white/80 focus-visible:ring-white/30`}
            aria-label={`عرض حجوزات ملعب ${pitch.name}`}
          >
            <CalendarSearch size={12} aria-hidden />
            الحجوزات
          </button>
          <button
            type="button"
            onClick={() => onRequestDelete(pitch)}
            className={`${actionBtn} !flex-none px-2.5 bg-red-500/[0.04] border-red-500/[0.14] text-red-400/70 hover:bg-red-500/[0.09] hover:border-red-500/30 hover:text-red-400 focus-visible:ring-red-500/40`}
            aria-label={`حذف ملعب ${pitch.name}`}
          >
            <Trash2 size={12} aria-hidden />
          </button>
        </div>
      </div>
    </article>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Add Pitch form
// ─────────────────────────────────────────────────────────────────────────────

// Maps an existing pitch into the editable form shape. Fields the backend does
// not return on the Pitch payload (description / image_url) fall back to empty.
function pitchToForm(p: OwnerPitch): PitchForm {
  return {
    name:           p.name,
    neighborhood:   p.neighborhood,
    surface:        p.surface,
    format:         p.format,
    price_per_hour: String(p.pricePerHour),
    description:    p.description ?? '',
    image_url:       p.image_url ?? '',
    image_public_id: p.image_public_id ?? '',
  };
}

// The single pitch form, reused for both create and edit. When `editing` is set
// it prefills from that pitch and PATCHes /pitches/:id; otherwise it POSTs a new
// pitch. There is intentionally no second form.
function AddPitchForm({
  editing,
  onSuccess,
  onCancel,
}: {
  editing?: OwnerPitch | null;
  onSuccess: (pitch: OwnerPitch) => void;
  onCancel:  () => void;
}) {
  const isEdit = !!editing;
  const [form, setForm]           = useState<PitchForm>(editing ? pitchToForm(editing) : EMPTY_FORM);
  const [isSubmitting, setSubmit] = useState(false);
  const [error, setError]         = useState<string | null>(null);

  const set = (field: keyof PitchForm) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
      setForm(prev => ({ ...prev, [field]: e.target.value }));

  // Image change from the dropzone. In CREATE mode we just stash the values in
  // form state — they ride along in the POST /pitches payload on save. In EDIT
  // mode the image is persisted immediately via PATCH /pitches/:id/image (the
  // dedicated endpoint runs the cloud-origin guard + old-asset cleanup), so a
  // replace/remove takes effect without waiting for the main "save".
  const handleImageChange = async (next: PitchImageValue) => {
    setForm(prev => ({ ...prev, image_url: next.image_url, image_public_id: next.image_public_id }));
    if (!isEdit) return;
    try {
      await api.patch(`/pitches/${editing!.id}/image`, {
        image_url: next.image_url,
        public_id: next.image_public_id,
      });
    } catch (err: any) {
      setError(err?.response?.data?.message ?? 'تعذّر حفظ صورة الملعب، يرجى المحاولة مجدداً');
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmit(true);
    try {
      const payload = { ...form, price_per_hour: Number(form.price_per_hour) };
      const res = isEdit
        ? await api.patch(`/pitches/${editing!.id}`, payload)
        : await api.post('/pitches', payload);
      onSuccess(res.data.data as OwnerPitch);
    } catch (err: any) {
      setError(
        err?.response?.data?.message ??
          (isEdit ? 'تعذّر تحديث الملعب، يرجى المحاولة مجدداً' : 'تعذّر إنشاء الملعب، يرجى المحاولة مجدداً'),
      );
      setSubmit(false);
    }
  };

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.10] mb-6 overflow-hidden">
      {/* Form header */}
      <div className="px-6 py-4 border-b border-white/[0.06] flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
            {isEdit
              ? <Pencil size={13} className="text-emerald-400" aria-hidden />
              : <Plus size={13} className="text-emerald-400" aria-hidden />}
          </div>
          <span className="text-[13px] font-semibold text-[#f0efe8]">
            {isEdit ? 'تعديل الملعب' : 'إضافة ملعب جديد'}
          </span>
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
                lang="en"
                inputMode="numeric"
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

          {/* Image — drag-and-drop dropzone (Cloudinary signed direct upload) */}
          <div className="sm:col-span-2">
            <label className={labelCls}>صورة الملعب</label>
            <PitchImageDropzone
              value={{ image_url: form.image_url, image_public_id: form.image_public_id }}
              onChange={handleImageChange}
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
                {isEdit ? 'جاري الحفظ...' : 'جاري الإضافة...'}
              </>
            ) : (
              <>{isEdit ? 'حفظ التعديلات' : 'إضافة الملعب'}</>
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
  const [editTarget,     setEditTarget]     = useState<OwnerPitch | null>(null);

  // ── delete-pitch modal state ────────────────────────────────────────────
  const [deleteTarget, setDeleteTarget] = useState<OwnerPitch | null>(null);
  const [isDeleting,   setIsDeleting]   = useState(false);
  const [deleteError,  setDeleteError]  = useState<string | null>(null);

  // ── activate/deactivate toggle state ────────────────────────────────────
  const [togglingId, setTogglingId] = useState<number | null>(null);
  const [toast,      setToast]      = useState<string | null>(null);

  // ── cancel-booking modal state ──────────────────────────────────────────
  const [cancelTarget, setCancelTarget] = useState<AdminBooking | null>(null);
  const [isCancelling, setIsCancelling] = useState(false);
  const [cancelError,  setCancelError]  = useState<string | null>(null);

  const openCancelModal = useCallback((booking: AdminBooking) => {
    setCancelError(null);
    setCancelTarget(booking);
  }, []);

  const closeCancelModal = useCallback(() => {
    if (isCancelling) return; // don't dismiss mid-request
    setCancelTarget(null);
    setCancelError(null);
  }, [isCancelling]);

  const confirmCancel = useCallback(async () => {
    if (!cancelTarget) return;
    setIsCancelling(true);
    setCancelError(null);
    try {
      await api.patch(`/bookings/${cancelTarget.id}/cancel`);
      // Optimistic update: flip the row to 'cancelled'. The مؤكدة / ملغاة stat
      // cards are derived from `bookings` via useMemo, so they re-tally
      // automatically — confirmed −1, cancelled +1.
      setBookings(prev =>
        prev.map(b => (b.id === cancelTarget.id ? { ...b, status: 'cancelled' as BookingStatus } : b)),
      );
      setCancelTarget(null);
    } catch (err: any) {
      setCancelError(
        err?.response?.data?.message ?? 'تعذّر إلغاء الحجز، يرجى المحاولة مجدداً',
      );
    } finally {
      setIsCancelling(false);
    }
  }, [cancelTarget]);

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

  // Open the shared form in edit mode (and close the add form if it was open).
  const openEditForm = useCallback((pitch: OwnerPitch) => {
    setShowAddForm(false);
    setEditTarget(pitch);
  }, []);

  // Replace the edited pitch in place — no refetch, no full reload.
  const handlePitchUpdated = useCallback((updated: OwnerPitch) => {
    setPitches(prev => prev.map(p => (p.id === updated.id ? updated : p)));
    setEditTarget(null);
  }, []);

  const openDeleteModal = useCallback((pitch: OwnerPitch) => {
    setDeleteError(null);
    setDeleteTarget(pitch);
  }, []);

  const closeDeleteModal = useCallback(() => {
    if (isDeleting) return; // don't dismiss mid-request
    setDeleteTarget(null);
    setDeleteError(null);
  }, [isDeleting]);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return;
    setIsDeleting(true);
    setDeleteError(null);
    try {
      await api.delete(`/pitches/${deleteTarget.id}`);
      // Optimistic removal from the list — no refetch.
      setPitches(prev => prev.filter(p => p.id !== deleteTarget.id));
      setDeleteTarget(null);
    } catch (err: any) {
      setDeleteError(err?.response?.data?.message ?? 'تعذّر حذف الملعب، يرجى المحاولة مجدداً');
    } finally {
      setIsDeleting(false);
    }
  }, [deleteTarget]);

  // Optimistic activate/deactivate: flip the card immediately, PATCH the toggle
  // endpoint (the api interceptor attaches X-CSRF-Token), and roll back + toast
  // on failure. The switch is disabled while its own request is in flight.
  const handleToggleActive = useCallback(async (pitch: OwnerPitch) => {
    const next = !pitch.isActive;
    setToast(null);
    setTogglingId(pitch.id);
    setPitches(prev => prev.map(p => (p.id === pitch.id ? { ...p, isActive: next } : p)));
    try {
      await api.patch(`/pitches/${pitch.id}/active`, { is_active: next });
    } catch (err: any) {
      // Roll back to the previous state.
      setPitches(prev => prev.map(p => (p.id === pitch.id ? { ...p, isActive: pitch.isActive } : p)));
      setToast(
        err?.response?.data?.message ??
          (next ? 'تعذّر تفعيل الملعب، يرجى المحاولة مجدداً' : 'تعذّر تعطيل الملعب، يرجى المحاولة مجدداً'),
      );
    } finally {
      setTogglingId(null);
    }
  }, []);

  // Auto-dismiss the error toast.
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(t);
  }, [toast]);

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
                            {['# الحجز', 'اللاعب', 'الملعب', 'التاريخ', 'الوقت', 'المبلغ', 'الحالة', 'الإجراءات'].map(col => (
                              <th key={col} className="px-5 py-3.5 text-start text-[10px] font-semibold text-white/30 tracking-widest uppercase whitespace-nowrap">
                                {col}
                              </th>
                            ))}
                          </tr>
                        </thead>
                        <tbody>
                          {bookings.map(b => (
                            <BookingRow key={b.id} booking={b} onRequestCancel={openCancelModal} />
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
            {!showAddForm && !editTarget && (
              <div className="flex items-center justify-between mb-6">
                <p className="text-[13px] text-white/35">
                  {pitchesLoaded
                    ? `${formatNumber(pitches.length)} ${pitches.length === 1 ? 'ملعب' : 'ملاعب'}`
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

            {/* Shared add / edit form — reused, never duplicated */}
            {showAddForm && (
              <AddPitchForm onSuccess={handlePitchAdded} onCancel={() => setShowAddForm(false)} />
            )}
            {editTarget && (
              <AddPitchForm
                editing={editTarget}
                onSuccess={handlePitchUpdated}
                onCancel={() => setEditTarget(null)}
              />
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

            {/* Grid — auto-rows-fr keeps every card the same height so the last
                row never leaves a ragged / orphaned cell. Uniform gap-5. */}
            {!pitchesLoading && pitches.length > 0 && (
              <div className="grid gap-5 auto-rows-fr grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
                {pitches.map(p => (
                  <PitchCard
                    key={p.id}
                    pitch={p}
                    onEdit={openEditForm}
                    onRequestDelete={openDeleteModal}
                    onViewBookings={() => setActiveTab('bookings')}
                    onToggleActive={handleToggleActive}
                    isToggling={togglingId === p.id}
                  />
                ))}
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

      {/* ── Cancel confirmation modal ────────────────────────────────────── */}
      {cancelTarget && (
        <CancelModal
          booking={cancelTarget}
          isCancelling={isCancelling}
          error={cancelError}
          onConfirm={confirmCancel}
          onClose={closeCancelModal}
        />
      )}

      {/* ── Delete-pitch confirmation modal ──────────────────────────────── */}
      {deleteTarget && (
        <DeletePitchModal
          pitch={deleteTarget}
          isDeleting={isDeleting}
          error={deleteError}
          onConfirm={confirmDelete}
          onClose={closeDeleteModal}
        />
      )}

      {/* ── Error toast (toggle rollback, etc.) ───────────────────────────── */}
      {toast && (
        <div
          role="alert"
          className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2.5 px-4 py-3 rounded-xl bg-[#1a1110] border border-red-500/25 shadow-2xl"
        >
          <AlertTriangle size={15} className="text-red-400 flex-shrink-0" aria-hidden />
          <span className="text-[12.5px] font-semibold text-red-300">{toast}</span>
          <button
            type="button"
            onClick={() => setToast(null)}
            className="text-white/30 hover:text-white/60 transition-colors duration-150 ms-1"
            aria-label="إغلاق التنبيه"
          >
            <X size={14} aria-hidden />
          </button>
        </div>
      )}

    </div>
  );
}
