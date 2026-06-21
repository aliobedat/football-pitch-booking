'use client';

// Customer profile (Cockpit WO1). Owner-scoped (GET /owner/customers/:id — the
// backend 404s a customer outside the owner's scope). The NAME is the dominant
// element of the header. Includes reliability stats, derived preferred slots,
// recent history, contact actions (tel: + WhatsApp deep-link), and the owner's
// private free-text notes (PATCH /owner/customers/:id/notes).

import { useCallback, useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import {
  ArrowRight, Phone, MessageCircle, CalendarDays, CheckCircle2, ShieldAlert,
  Clock, StickyNote, Loader2,
} from 'lucide-react';
import api from '@/lib/api';
import { formatNumber, formatDate, formatTime } from '@/lib/format';

interface PreferredSlot { weekday: number; hour: number; count: number; }
interface HistoryRow {
  id: number; pitch_name: string; start_time: string; end_time: string;
  status: string; attendance: string;
}
interface Profile {
  customer: {
    id: number; player_id: number | null; name: string; phone: string;
    notes: string; is_app_player: boolean;
  };
  booking_count: number;
  no_show_count: number;
  checked_in_count: number;
  last_booked: string | null;
  preferred_slots: PreferredSlot[];
  recent_bookings: HistoryRow[];
}

// Postgres DOW: 0=Sunday … 6=Saturday.
const WEEKDAYS_AR = ['الأحد', 'الإثنين', 'الثلاثاء', 'الأربعاء', 'الخميس', 'الجمعة', 'السبت'];
const pad2 = (n: number) => String(n).padStart(2, '0');

const ATTENDANCE_AR: Record<string, { label: string; cls: string }> = {
  checked_in: { label: 'حضر',     cls: 'bg-emerald-500/15 border-emerald-500/30 text-emerald-400' },
  no_show:    { label: 'لم يحضر', cls: 'bg-red-500/15 border-red-500/30 text-red-400' },
  pending:    { label: '—',       cls: 'bg-white/[0.04] border-white/[0.08] text-white/35' },
};

export default function CustomerProfilePage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();

  const [profile, setProfile] = useState<Profile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  const [notes, setNotes]         = useState('');
  const [notesSaving, setSaving]  = useState(false);
  const [notesSaved, setSaved]    = useState(false);

  useEffect(() => {
    api.get(`/owner/customers/${id}`)
      .then(res => {
        const p = res.data.data as Profile;
        setProfile(p);
        setNotes(p.customer.notes ?? '');
      })
      .catch(err => setError(err?.response?.status === 404
        ? 'العميل غير موجود'
        : 'تعذّر تحميل ملف الزبون.'))
      .finally(() => setLoading(false));
  }, [id]);

  const saveNotes = useCallback(async () => {
    setSaving(true);
    setSaved(false);
    try {
      const res = await api.patch(`/owner/customers/${id}/notes`, { notes });
      setNotes(res.data.data.notes ?? '');
      setSaved(true);
      setTimeout(() => setSaved(false), 2500);
    } catch {
      setError('تعذّر حفظ الملاحظات.');
    } finally {
      setSaving(false);
    }
  }, [id, notes]);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-24">
        <Loader2 size={24} className="text-emerald-500 animate-spin" aria-hidden />
      </div>
    );
  }
  if (error || !profile) {
    return (
      <div className="flex flex-col gap-4">
        <BackButton onClick={() => router.push('/customers')} />
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">
          {error ?? 'خطأ غير متوقع'}
        </div>
      </div>
    );
  }

  const { customer } = profile;
  const waNumber = customer.phone.replace(/[^0-9]/g, ''); // wa.me wants digits only
  const reliability = profile.booking_count > 0
    ? Math.round(((profile.booking_count - profile.no_show_count) / profile.booking_count) * 100)
    : null;

  return (
    <div className="flex flex-col gap-6">
      <BackButton onClick={() => router.push('/customers')} />

      {/* ── Header — NAME IS KING ── */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-6 flex flex-col sm:flex-row sm:items-center gap-5">
        <div className="w-16 h-16 rounded-2xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center flex-shrink-0">
          <span className="text-[26px] font-bold text-emerald-300">{(customer.name || '؟').trim().charAt(0)}</span>
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2.5 flex-wrap">
            <h1 className="text-[26px] font-bold tracking-tight text-[#f0efe8] leading-none">
              {customer.name || 'زبون بدون اسم'}
            </h1>
            <span className={[
              'inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold border',
              customer.is_app_player
                ? 'bg-sky-500/15 border-sky-500/30 text-sky-300'
                : 'bg-amber-500/15 border-amber-500/30 text-amber-300',
            ].join(' ')}>
              {customer.is_app_player ? 'لاعب مسجّل' : 'زبون يدوي'}
            </span>
          </div>
          <p className="text-[13px] text-white/50 mt-2 font-mono" dir="ltr">{customer.phone}</p>
        </div>

        {/* Contact actions */}
        <div className="flex items-center gap-2.5 flex-shrink-0">
          <a
            href={`tel:${customer.phone}`}
            className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl text-[12px] font-semibold border border-white/[0.09] bg-white/[0.03] text-white/75 hover:text-white hover:border-white/20 transition-all"
          >
            <Phone size={14} aria-hidden /> اتصال
          </a>
          <a
            href={`https://wa.me/${waNumber}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl text-[12px] font-semibold border border-emerald-500/25 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/15 transition-all"
          >
            <MessageCircle size={14} aria-hidden /> واتساب
          </a>
        </div>
      </div>

      {/* ── Reliability stats ── */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard icon={CalendarDays}  value={formatNumber(profile.booking_count)}    label="إجمالي الحجوزات" />
        <StatCard icon={CheckCircle2}  value={formatNumber(profile.checked_in_count)} label="مرات الحضور" accent="text-emerald-400" />
        <StatCard icon={ShieldAlert}   value={formatNumber(profile.no_show_count)}    label="مرات الغياب" accent={profile.no_show_count > 0 ? 'text-red-400' : undefined} />
        <StatCard icon={CheckCircle2}  value={reliability === null ? '—' : `${formatNumber(reliability)}%`} label="نسبة الالتزام" accent="text-emerald-400" />
      </div>

      {/* ── Preferred slots ── */}
      {profile.preferred_slots.length > 0 && (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
          <p className="text-[12px] text-white/40 mb-3 flex items-center gap-2"><Clock size={13} aria-hidden /> الأوقات المفضّلة</p>
          <div className="flex flex-wrap gap-2.5">
            {profile.preferred_slots.map((s, i) => (
              <span key={i} className="inline-flex items-center gap-2 px-3.5 py-2 rounded-xl bg-white/[0.03] border border-white/[0.07] text-[12.5px] text-white/70">
                <span className="font-semibold text-[#f0efe8]">{WEEKDAYS_AR[s.weekday] ?? '—'}</span>
                <span className="font-mono text-white/55" dir="ltr">{pad2(s.hour)}:00</span>
                <span className="text-[10px] text-white/30">×{formatNumber(s.count)}</span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* ── Notes ── */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-3 flex items-center gap-2"><StickyNote size={13} aria-hidden /> ملاحظات خاصة</p>
        <textarea
          value={notes}
          onChange={e => { setNotes(e.target.value); setSaved(false); }}
          rows={3}
          maxLength={2000}
          placeholder="أضف ملاحظة عن هذا الزبون (تفضيلاته، سلوكه، أي شيء مفيد)…"
          className="w-full bg-[#0f1110] border border-white/[0.09] rounded-xl px-3.5 py-3 text-[13px] text-white/80 placeholder:text-white/25 focus:outline-none focus:border-emerald-500/40 resize-y leading-relaxed"
        />
        <div className="flex items-center justify-end gap-3 mt-3">
          {notesSaved && <span className="text-[12px] text-emerald-400">تم الحفظ ✓</span>}
          <button
            type="button"
            onClick={saveNotes}
            disabled={notesSaving || notes === (customer.notes ?? '')}
            className="inline-flex items-center gap-2 px-5 py-2 rounded-xl text-[12px] font-bold bg-emerald-500/[0.12] text-emerald-400 border border-emerald-500/25 hover:bg-emerald-500/[0.18] disabled:opacity-40 disabled:cursor-not-allowed transition-all"
          >
            {notesSaving ? <Loader2 size={13} className="animate-spin" aria-hidden /> : null}
            حفظ الملاحظات
          </button>
        </div>
      </div>

      {/* ── Recent history ── */}
      <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
        <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110]">
          <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">سجل الحجوزات الأخيرة</p>
        </div>
        {profile.recent_bookings.length === 0 ? (
          <div className="bg-[#141715] py-12 text-center text-[13px] text-white/30">لا توجد حجوزات بعد</div>
        ) : (
          <div className="bg-[#141715] divide-y divide-white/[0.04]">
            {profile.recent_bookings.map(b => {
              const att = ATTENDANCE_AR[b.attendance] ?? ATTENDANCE_AR.pending;
              const cancelled = b.status === 'cancelled';
              return (
                <div key={b.id} className="px-5 py-3.5 flex items-center gap-4">
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-semibold text-[#f0efe8] truncate">{b.pitch_name || '—'}</p>
                    <p className="text-[11px] text-white/40 mt-0.5">
                      {formatDate(b.start_time)} · {formatTime(b.start_time)} — {formatTime(b.end_time)}
                    </p>
                  </div>
                  {cancelled
                    ? <span className="inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold border bg-white/[0.04] border-white/[0.08] text-white/35">ملغى</span>
                    : <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold border ${att.cls}`}>{att.label}</span>}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

function BackButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="inline-flex items-center gap-1.5 text-[12.5px] text-white/45 hover:text-white/75 transition-colors w-fit"
    >
      <ArrowRight size={15} aria-hidden /> العودة إلى الزبائن
    </button>
  );
}

function StatCard({ icon: Icon, value, label, accent }: {
  icon: React.ElementType; value: string; label: string; accent?: string;
}) {
  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-4 flex flex-col gap-2">
      <Icon size={15} className={accent ?? 'text-white/40'} aria-hidden />
      <p className={`text-[22px] font-bold leading-none ${accent ?? 'text-[#f0efe8]'}`}>{value}</p>
      <p className="text-[11px] text-white/35">{label}</p>
    </div>
  );
}
