'use client';

// WO-MONITORING-V1 Gate 1: the smallest read-only admin monitoring page.
// Admin-only (backend: RequireRole("admin"), no owner/staff access — enforced
// both at the route and re-asserted in the handler). Polls GET /admin/monitoring
// every 20s; never starts an overlapping request.

import { useState, useEffect, useEffectEvent, useRef } from 'react';
import { AlertTriangle, CalendarDays } from 'lucide-react';
import api from '@/lib/api';
import { formatDate, formatTime } from '@/lib/format';

// ─────────────────────────────────────────────────────────────────────────────
// Types (mirror the backend-owned DTO exactly — no raw DB model, no raw phone)
// ─────────────────────────────────────────────────────────────────────────────

interface BookingSummary {
  total: number; pending: number; confirmed: number; rejected: number;
  completed: number; cancelled: number; no_show: number;
}

interface RecentBooking {
  id: number; created_at: string; contact_name: string; contact_phone_masked: string;
  venue_id: number; venue_name: string; pitch_id: number; pitch_name: string;
  start_time: string; end_time: string; status: string;
}

interface WhatsAppUsage {
  count: number; cap: number; remaining: number; warning: boolean; blocked: boolean;
}

interface FailedJob {
  kind: string; status: string; attempts: number; failure_category: string;
  recipient_masked: string; updated_at: string;
}

interface NotificationJobs {
  pending: number; retrying: number; processing: number;
  succeeded: number; dead_letter: number; blocked: number;
  recent_failures: FailedJob[];
}

interface MonitoringData {
  selected_date: string;
  booking_summary: BookingSummary;
  recent_bookings: RecentBooking[];
  whatsapp_usage: WhatsAppUsage;
  notification_jobs: NotificationJobs;
}

interface Venue { id: number; name: string; }

const STATUS_LABEL: Record<string, string> = {
  pending: 'قيد الانتظار', confirmed: 'مؤكد', rejected: 'مرفوض',
  completed: 'مكتمل', cancelled: 'ملغى', no_show: 'لم يحضر',
};

const FAILURE_CATEGORY_LABEL: Record<string, string> = {
  quota_exhausted: 'تجاوز الحد اليومي', quota_unavailable: 'تعذّر التحقق من الحصة',
  paid_whatsapp_disabled: 'واتساب المدفوع معطّل', delivery_failed: 'فشل التسليم',
  invalid_recipient: 'رقم غير صالح', unknown: 'غير معروف',
};

const POLL_INTERVAL_MS = 20_000;

const MONITORING_LOAD_ERROR_MESSAGE = 'تعذّر تحميل بيانات المراقبة. تأكد من صلاحيات الحساب.';

// ─────────────────────────────────────────────────────────────────────────────
// Small stat card
// ─────────────────────────────────────────────────────────────────────────────

function StatCard({ value, label, valueColor = 'text-[#f0efe8]' }: { value: number; label: string; valueColor?: string }) {
  return (
    <div className="p-4 rounded-2xl bg-[#141715] border border-white/[0.07]">
      <p className={`text-[24px] font-bold tracking-tight leading-none mb-1 ${valueColor}`}>{value}</p>
      <p className="text-[11px] text-white/35">{label}</p>
    </div>
  );
}

export default function MonitoringPage() {
  const [data, setData] = useState<MonitoringData | null>(null);
  const [venues, setVenues] = useState<Venue[]>([]);
  const [error, setError] = useState<string | null>(null);
  // Loading is DERIVED (data === null && no error yet) rather than tracked with
  // its own setState — nothing needs to set it synchronously inside an effect.
  // Existing results stay on screen during a background poll/filter refresh
  // (data is never cleared before the next response arrives).
  const loading = data === null && error === null;

  const [dateFilter, setDateFilter] = useState('');
  const [venueFilter, setVenueFilter] = useState('');
  const [statusFilter, setStatusFilter] = useState('');

  // true while ANY request (filter-triggered or polled) is in flight — lets a
  // poll tick skip itself rather than overlap. A filter change does NOT
  // consult this; it always cancels whatever is in flight and starts fresh.
  const inFlight = useRef(false);
  // The AbortController for the currently-active request, so a superseded
  // request's own late resolution can tell it is no longer the current one.
  const abortRef = useRef<AbortController | null>(null);
  // Guards against a stale response (unmount) writing state after the fact.
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; };
  }, []);

  // An Effect Event: non-reactive, always sees the latest filters, and is the
  // React-endorsed escape hatch for calling setState from inside an effect
  // (https://react.dev/learn/separating-events-from-effects) — it is
  // deliberately omitted from every dependency array below.
  const onFetchMonitoring = useEffectEvent(async (controller: AbortController) => {
    inFlight.current = true;
    const params: Record<string, string> = {};
    if (dateFilter) params.date = dateFilter;
    if (venueFilter) params.venue_id = venueFilter;
    if (statusFilter) params.status = statusFilter;
    try {
      const res = await api.get('/admin/monitoring', { params, signal: controller.signal });
      if (controller.signal.aborted || !mountedRef.current) return;
      setData(res.data.data ?? null);
      setError(null);
    } catch {
      if (controller.signal.aborted || !mountedRef.current) return;
      setError(MONITORING_LOAD_ERROR_MESSAGE);
    } finally {
      // Only clear the flag if no newer request has since taken over —
      // otherwise a late-resolving aborted request would wrongly let a poll
      // tick start a second, truly overlapping request.
      if (abortRef.current === controller) {
        inFlight.current = false;
      }
    }
  });

  // Venues fetched ONCE on load (existing owner/admin-unscoped-for-admin
  // endpoint reused verbatim — no new backend route for the selector).
  useEffect(() => {
    let cancelled = false;
    api.get('/owner/venues')
      .then(res => { if (!cancelled) setVenues(res.data.data ?? []); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, []);

  // Initial load + every filter change. Each run gets its own AbortController;
  // the cleanup (which fires before the NEXT filter change's effect body, and
  // on unmount) aborts it — so a filter change never leaves a stale in-flight
  // request able to overwrite fresher results.
  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;
    const id = setTimeout(() => { void onFetchMonitoring(controller); }, 0);
    return () => {
      clearTimeout(id);
      controller.abort();
    };
  }, [dateFilter, venueFilter, statusFilter]);

  // Poll every 20s; cleared on unmount. A tick skips itself (no new request,
  // no abort of the current one) when a request is already running.
  useEffect(() => {
    const id = setInterval(() => {
      if (inFlight.current) return;
      const controller = new AbortController();
      abortRef.current = controller;
      void onFetchMonitoring(controller);
    }, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, []);

  const summary = data?.booking_summary;
  const usage = data?.whatsapp_usage;
  const jobs = data?.notification_jobs;

  const usageBarColor = usage?.blocked ? 'bg-red-500' : usage?.warning ? 'bg-amber-500' : 'bg-emerald-500';
  const usagePct = usage ? Math.min(100, (usage.count / usage.cap) * 100) : 0;

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">المراقبة</h1>

      {/* Filters */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-4 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-white/40">التاريخ</span>
          <input
            type="date" value={dateFilter} dir="ltr"
            onChange={e => setDateFilter(e.target.value)}
            className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-white/40">المجمع</span>
          <select
            value={venueFilter}
            onChange={e => setVenueFilter(e.target.value)}
            className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40 min-w-[140px]"
          >
            <option value="" className="bg-[#0f1110]">كل المجمعات</option>
            {venues.map(v => <option key={v.id} value={v.id} className="bg-[#0f1110]">{v.name}</option>)}
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-[11px] text-white/40">الحالة</span>
          <select
            value={statusFilter}
            onChange={e => setStatusFilter(e.target.value)}
            className="bg-[#0f1110] border border-white/[0.09] rounded-lg px-3 py-2 text-[12px] text-white/80 focus:outline-none focus:border-emerald-500/40 min-w-[140px]"
          >
            <option value="" className="bg-[#0f1110]">كل الحالات</option>
            {Object.entries(STATUS_LABEL).map(([value, label]) => (
              <option key={value} value={value} className="bg-[#0f1110]">{label}</option>
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
      ) : !data ? null : (
        <>
          {/* A. Booking cards */}
          <div>
            <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase mb-2">
              الحجوزات المنشأة بتاريخ {data.selected_date}
            </p>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              <StatCard value={summary!.total} label="الإجمالي" />
              <StatCard value={summary!.confirmed} label="مؤكدة" valueColor="text-emerald-400" />
              <StatCard value={summary!.pending} label="قيد الانتظار" valueColor="text-amber-400" />
              <StatCard value={summary!.cancelled} label="ملغاة" valueColor="text-red-400" />
              <StatCard value={summary!.rejected} label="مرفوضة" />
              <StatCard value={summary!.completed} label="مكتملة" />
              <StatCard value={summary!.no_show} label="لم يحضر" />
            </div>
          </div>

          {/* B. WhatsApp usage */}
          <div className="rounded-2xl bg-[#141715] border border-white/[0.07] p-4">
            <div className="flex items-center justify-between mb-2">
              <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">استخدام واتساب اليومي</p>
              <p className="text-[13px] font-semibold text-white/70" dir="ltr">{usage!.count} / {usage!.cap}</p>
            </div>
            <div className="w-full h-2 rounded-full bg-white/[0.06] overflow-hidden">
              <div className={`h-full ${usageBarColor} transition-all`} style={{ width: `${usagePct}%` }} />
            </div>
            {usage!.blocked && (
              <p className="mt-2 text-[11px] text-red-400 flex items-center gap-1">
                <AlertTriangle size={12} aria-hidden /> تم تجاوز الحد اليومي — لا يمكن إرسال رسائل واتساب مدفوعة إضافية اليوم
              </p>
            )}
            {!usage!.blocked && usage!.warning && (
              <p className="mt-2 text-[11px] text-amber-400">الاقتراب من الحد اليومي</p>
            )}
          </div>

          {/* C. Notification jobs */}
          <div>
            <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase mb-2">مهام الإشعارات</p>
            <div className="grid grid-cols-2 sm:grid-cols-5 gap-3">
              <StatCard value={jobs!.pending} label="قيد الانتظار" />
              <StatCard value={jobs!.retrying} label="إعادة المحاولة" valueColor="text-amber-400" />
              <StatCard value={jobs!.processing} label="قيد المعالجة" />
              <StatCard value={jobs!.dead_letter} label="فشل نهائي" valueColor="text-red-400" />
              <StatCard value={jobs!.blocked} label="محظورة" valueColor="text-red-400" />
            </div>
          </div>

          {/* D. Recent bookings */}
          <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
            <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110]">
              <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">الحجوزات الأخيرة</p>
            </div>
            {data.recent_bookings.length === 0 ? (
              <div className="flex flex-col items-center justify-center py-16 gap-3">
                <CalendarDays size={24} className="text-white/15" aria-hidden />
                <p className="text-[13px] text-white/30">لا توجد حجوزات</p>
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[760px] bg-[#141715]">
                  <thead>
                    <tr className="border-b border-white/[0.06] bg-[#111312]">
                      {['الإنشاء', 'اللاعب', 'الهاتف', 'المجمع', 'الملعب', 'الموعد', 'الحالة'].map(col => (
                        <th key={col} className="px-4 py-3 text-start text-[10px] font-semibold text-white/30 tracking-widest uppercase whitespace-nowrap">{col}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {data.recent_bookings.map(b => (
                      <tr key={b.id} className="border-b border-white/[0.04]">
                        <td className="px-4 py-3 text-[12px] text-white/50 whitespace-nowrap">{formatDate(b.created_at)} {formatTime(b.created_at)}</td>
                        <td className="px-4 py-3 text-[13px] text-[#f0efe8]">{b.contact_name || '—'}</td>
                        <td className="px-4 py-3 text-[12px] text-white/50 font-mono" dir="ltr">{b.contact_phone_masked || '—'}</td>
                        <td className="px-4 py-3 text-[13px] text-white/65">{b.venue_name || '—'}</td>
                        <td className="px-4 py-3 text-[13px] text-white/65">{b.pitch_name || '—'}</td>
                        <td className="px-4 py-3 text-[12px] text-white/50 whitespace-nowrap">{formatTime(b.start_time)} – {formatTime(b.end_time)}</td>
                        <td className="px-4 py-3 text-[11px] text-white/50">{STATUS_LABEL[b.status] ?? b.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>

          {/* E. Recent failed jobs */}
          <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
            <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110]">
              <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase">آخر مهام الإشعارات الفاشلة</p>
            </div>
            {jobs!.recent_failures.length === 0 ? (
              <div className="py-10 text-center text-[13px] text-white/30">لا توجد مهام فاشلة</div>
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[640px] bg-[#141715]">
                  <thead>
                    <tr className="border-b border-white/[0.06] bg-[#111312]">
                      {['الوقت', 'النوع', 'الحالة', 'المحاولات', 'السبب', 'المستلم'].map(col => (
                        <th key={col} className="px-4 py-3 text-start text-[10px] font-semibold text-white/30 tracking-widest uppercase whitespace-nowrap">{col}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {jobs!.recent_failures.map((f, i) => (
                      <tr key={i} className="border-b border-white/[0.04]">
                        <td className="px-4 py-3 text-[12px] text-white/50 whitespace-nowrap">{formatDate(f.updated_at)} {formatTime(f.updated_at)}</td>
                        <td className="px-4 py-3 text-[12px] text-white/65">{f.kind}</td>
                        <td className="px-4 py-3 text-[11px] text-red-400">{f.status}</td>
                        <td className="px-4 py-3 text-[12px] text-white/50">{f.attempts}</td>
                        <td className="px-4 py-3 text-[12px] text-white/65">{FAILURE_CATEGORY_LABEL[f.failure_category] ?? f.failure_category}</td>
                        <td className="px-4 py-3 text-[12px] text-white/50 font-mono" dir="ltr">{f.recipient_masked || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
