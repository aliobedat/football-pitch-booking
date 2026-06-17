'use client';

// Staff management — owner-scoped (Dashboard PR2 UI). An owner provisions a guard
// by PHONE and binds them to a pitch they own; the backend promotes that already-
// registered player to the `staff` role. Revoke unbinds + demotes back to player.
// All scoping (own pitches, own staff) is enforced server-side; this is UX only —
// the route guard + backend RequireRole("owner","admin") are the real boundary.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { UserCog, Phone as PhoneIcon, Loader2, UserPlus, Trash2, ShieldAlert } from 'lucide-react';
import api from '@/lib/api';

interface OwnerPitch { id: number; name: string }
interface StaffBinding {
  id: number; user_id: number; pitch_id: number; owner_id: number;
  phone: string; full_name: string;
}

// Map an InviteStaff error code to an Arabic message for the owner.
const INVITE_ERRORS: Record<string, string> = {
  staff_user_not_found: 'هذا الرقم لم يسجّل في التطبيق بعد — اطلب من الموظف تسجيل الدخول مرة واحدة أولاً.',
  staff_already_assigned: 'هذا المستخدم معيّن بالفعل كموظف على ملعب.',
  not_pitch_owner: 'لا يمكنك تعيين موظف على ملعب لا تملكه.',
  cannot_assign_user: 'لا يمكن تعيين هذا المستخدم كموظف.',
  invalid_phone: 'رقم الهاتف غير صالح.',
};

export default function StaffPage() {
  const [pitches, setPitches] = useState<OwnerPitch[]>([]);
  const [staff, setStaff]     = useState<StaffBinding[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  // Invite form state.
  const [phone, setPhone]   = useState('');
  const [pitchId, setPitchId] = useState<number | ''>('');
  const [inviting, setInviting] = useState(false);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [inviteOK, setInviteOK] = useState<string | null>(null);

  const [revoking, setRevoking] = useState<number | null>(null);

  const pitchName = useMemo(() => {
    const m = new Map(pitches.map(p => [p.id, p.name]));
    return (id: number) => m.get(id) ?? `#${id}`;
  }, [pitches]);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([api.get('/owner/pitches'), api.get('/owner/staff')])
      .then(([p, s]) => {
        const list = (p.data.data ?? p.data ?? []) as OwnerPitch[];
        setPitches(list);
        setStaff((s.data.data ?? []) as StaffBinding[]);
        setPitchId(prev => (prev === '' && list.length > 0 ? list[0].id : prev));
      })
      .catch(() => setError('تعذّر تحميل البيانات. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  const invite = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();
    setInviteError(null);
    setInviteOK(null);
    if (pitchId === '') { setInviteError('اختر الملعب.'); return; }
    if (!phone.trim()) { setInviteError('رقم الهاتف مطلوب.'); return; }
    setInviting(true);
    try {
      await api.post(`/pitches/${pitchId}/staff`, { phone: phone.trim() });
      setPhone('');
      setInviteOK('تم تعيين الموظف.');
      load();
    } catch (err: any) {
      const code = err?.response?.data?.error;
      setInviteError(INVITE_ERRORS[code] ?? err?.response?.data?.message ?? 'تعذّر تعيين الموظف.');
    } finally {
      setInviting(false);
    }
  }, [phone, pitchId, load]);

  const revoke = useCallback(async (b: StaffBinding) => {
    if (!confirm(`تسريح ${b.full_name || b.phone}؟ سيعود حسابه إلى مستخدم عادي.`)) return;
    setRevoking(b.user_id);
    try {
      await api.delete(`/owner/staff/${b.user_id}`);
      setStaff(prev => prev.filter(x => x.user_id !== b.user_id));
    } catch {
      setError('تعذّر تسريح الموظف. حاول مرة أخرى.');
    } finally {
      setRevoking(null);
    }
  }, []);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-[20px] font-bold tracking-tight flex items-center gap-2">
          <UserCog size={20} className="text-emerald-400" aria-hidden />
          الموظفون
        </h1>
      </div>

      {/* Invite form */}
      <form onSubmit={invite} className="rounded-2xl border border-white/[0.07] bg-[#141715] p-5 flex flex-col gap-3.5">
        <p className="text-[12px] text-white/45 leading-relaxed">
          عيّن موظفاً (حارس ملعب) عبر رقم هاتفه. يجب أن يكون قد سجّل دخوله للتطبيق مرة واحدة على الأقل. سيُمنح صلاحية الوصول إلى جدول الملعب المحدد فقط — دون الوصول للتحليلات أو المالية.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <label className="flex flex-col gap-1.5">
            <span className="text-[12px] text-white/45">رقم الهاتف *</span>
            <input value={phone} onChange={e => setPhone(e.target.value)} dir="ltr"
              className="staff-input font-mono" placeholder="+9627…" />
          </label>
          <label className="flex flex-col gap-1.5">
            <span className="text-[12px] text-white/45">الملعب *</span>
            <select value={pitchId} onChange={e => setPitchId(Number(e.target.value))} className="staff-input">
              {pitches.length === 0 && <option value="" className="bg-[#0f1110]">لا توجد ملاعب</option>}
              {pitches.map(p => <option key={p.id} value={p.id} className="bg-[#0f1110]">{p.name}</option>)}
            </select>
          </label>
        </div>

        {inviteError && <p className="text-[12px] text-amber-400">{inviteError}</p>}
        {inviteOK && <p className="text-[12px] text-emerald-400">{inviteOK}</p>}

        <div className="flex justify-end">
          <button type="submit" disabled={inviting || pitches.length === 0}
            className="inline-flex items-center gap-2 px-5 py-2 rounded-xl text-[12px] font-bold bg-emerald-500/[0.12] text-emerald-400 border border-emerald-500/25 hover:bg-emerald-500/[0.18] disabled:opacity-50 transition-all">
            {inviting ? <Loader2 size={13} className="animate-spin" aria-hidden /> : <UserPlus size={13} aria-hidden />}
            تعيين موظف
          </button>
        </div>
      </form>

      {/* Staff list */}
      {loading ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center">
          <Loader2 size={22} className="inline text-emerald-500 animate-spin" aria-hidden />
        </div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400 flex items-center gap-2">
          <ShieldAlert size={15} aria-hidden /> {error}
        </div>
      ) : staff.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 gap-3">
          <UserCog size={26} className="text-white/15" aria-hidden />
          <p className="text-[13.5px] text-white/40">لا يوجد موظفون بعد</p>
        </div>
      ) : (
        <div className="rounded-2xl border border-white/[0.07] bg-[#141715] overflow-hidden divide-y divide-white/[0.05]">
          {staff.map(b => (
            <div key={b.id} className="flex items-center justify-between gap-4 px-5 py-3.5">
              <div className="min-w-0">
                <p className="text-[14px] font-bold text-[#f0efe8] truncate">{b.full_name || 'بدون اسم'}</p>
                <div className="flex items-center gap-3 mt-0.5 text-[12px] text-white/45">
                  <span className="inline-flex items-center gap-1 font-mono" dir="ltr"><PhoneIcon size={11} aria-hidden /> {b.phone}</span>
                  <span className="inline-flex items-center gap-1">· {pitchName(b.pitch_id)}</span>
                </div>
              </div>
              <button onClick={() => revoke(b)} disabled={revoking === b.user_id}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-semibold text-red-300 bg-red-500/[0.08] border border-red-500/20 hover:bg-red-500/[0.14] disabled:opacity-50 transition-all shrink-0">
                {revoking === b.user_id ? <Loader2 size={12} className="animate-spin" aria-hidden /> : <Trash2 size={12} aria-hidden />}
                تسريح
              </button>
            </div>
          ))}
        </div>
      )}

      <style jsx>{`
        .staff-input {
          width: 100%; background: #0f1110; border: 1px solid rgba(255,255,255,0.09);
          border-radius: 12px; padding: 10px 12px; font-size: 13px; color: rgba(255,255,255,0.85);
          outline: none;
        }
        .staff-input:focus { border-color: rgba(16,185,129,0.4); }
      `}</style>
    </div>
  );
}
