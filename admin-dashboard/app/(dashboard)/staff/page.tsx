'use client';

// Staff management — owner-scoped (Dashboard PR2 UI, 1:N). An owner provisions a
// guard by PHONE and binds them to ONE OR MORE pitches they own; the backend
// promotes that already-registered player to the `staff` role. Revoke unbinds ALL
// of their pitches + demotes back to player. All scoping (own pitches, own staff)
// is enforced server-side; this is UX only — the route guard + backend
// RequireRole("owner","admin") are the real boundary.

import { useCallback, useEffect, useState } from 'react';
import { UserCog, Phone as PhoneIcon, Loader2, UserPlus, Trash2, ShieldAlert, MapPin } from 'lucide-react';
import api from '@/lib/api';

interface OwnerPitch { id: number; name: string }
interface StaffPitch { pitch_id: number; pitch_name: string }
interface StaffMember {
  user_id: number; owner_id: number;
  phone: string; full_name: string;
  pitches: StaffPitch[];
}

// Map an InviteStaff error code to an Arabic message for the owner.
const INVITE_ERRORS: Record<string, string> = {
  password_required: 'كلمة المرور مطلوبة لإضافة هذا الموظف.',
  weak_password: 'كلمة المرور يجب أن تكون 8 أحرف على الأقل.',
  staff_foreign_owner: 'هذا الموظف مُسجَّل لدى مالك آخر ولا يمكن إعادة تعيينه.',
  not_pitch_owner: 'لا يمكنك تعيين موظف إلا على ملاعبك.',
  cannot_assign_user: 'لا يمكن إضافة هذا الرقم كموظف.',
  invalid_phone: 'رقم الهاتف غير صالح.',
  no_pitches: 'اختر ملعباً واحداً على الأقل.',
};

// Client-side minimum mirrors the backend (minStaffPasswordLen).
const MIN_PASSWORD_LEN = 8;

export default function StaffPage() {
  const [pitches, setPitches] = useState<OwnerPitch[]>([]);
  const [staff, setStaff]     = useState<StaffMember[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError]     = useState<string | null>(null);

  // Invite form state — multi-pitch (1:N).
  const [phone, setPhone]   = useState('');
  const [fullName, setFullName] = useState('');
  const [password, setPassword] = useState('');
  const [selectedPitches, setSelectedPitches] = useState<number[]>([]);
  const [inviting, setInviting] = useState(false);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [inviteOK, setInviteOK] = useState<string | null>(null);

  const [revoking, setRevoking] = useState<number | null>(null);

  const togglePitch = (id: number) =>
    setSelectedPitches(s => (s.includes(id) ? s.filter(x => x !== id) : [...s, id]));

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([api.get('/owner/pitches'), api.get('/owner/staff')])
      .then(([p, s]) => {
        setPitches((p.data.data ?? p.data ?? []) as OwnerPitch[]);
        setStaff((s.data.data ?? []) as StaffMember[]);
      })
      .catch(() => setError('تعذّر تحميل البيانات. تأكد من صلاحيات الحساب.'))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  const invite = useCallback(async (e: React.FormEvent) => {
    e.preventDefault();
    setInviteError(null);
    setInviteOK(null);
    if (!phone.trim()) { setInviteError('رقم الهاتف مطلوب.'); return; }
    if (selectedPitches.length === 0) { setInviteError('اختر ملعباً واحداً على الأقل.'); return; }
    // A password is required for a brand-new staff member; the backend is the
    // authority (it knows the phone's current state) and returns password_required
    // when one is actually needed. We only block an obviously-too-short password.
    if (password && password.length < MIN_PASSWORD_LEN) {
      setInviteError(INVITE_ERRORS.weak_password); return;
    }
    setInviting(true);
    try {
      await api.post('/owner/staff', {
        phone: phone.trim(),
        full_name: fullName.trim(),
        password,
        pitch_ids: selectedPitches,
      });
      setPhone('');
      setFullName('');
      setPassword('');
      setSelectedPitches([]);
      setInviteOK('تم تعيين الموظف.');
      load();
    } catch (err: any) {
      const code = err?.response?.data?.error;
      setInviteError(INVITE_ERRORS[code] ?? err?.response?.data?.message ?? 'تعذّر تعيين الموظف.');
    } finally {
      setInviting(false);
    }
  }, [phone, selectedPitches, load]);

  const revoke = useCallback(async (m: StaffMember) => {
    if (!confirm(`تسريح ${m.full_name || m.phone}؟ سيُلغى وصوله لكل الملاعب ويعود حسابه إلى مستخدم عادي.`)) return;
    setRevoking(m.user_id);
    try {
      await api.delete(`/owner/staff/${m.user_id}`);
      setStaff(prev => prev.filter(x => x.user_id !== m.user_id));
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
          أضِف موظفاً (حارس ملعب) عبر رقم هاتفه واسمه وكلمة مرور. سيسجّل الدخول بنفس صفحة الدخول عبر رقمه وكلمة المرور — دون الحاجة لتسجيل مسبق. سيُمنح صلاحية الوصول إلى جداول الملاعب المحدّدة فقط — دون الوصول للتحليلات أو المالية.
        </p>
        <label className="flex flex-col gap-1.5">
          <span className="text-[12px] text-white/45">رقم الهاتف *</span>
          <input value={phone} onChange={e => setPhone(e.target.value)} dir="ltr"
            className="staff-input font-mono max-w-xs" placeholder="+9627…" />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className="text-[12px] text-white/45">اسم الموظف</span>
          <input value={fullName} onChange={e => setFullName(e.target.value)}
            className="staff-input max-w-xs" placeholder="مثال: أحمد محمد" />
        </label>
        <label className="flex flex-col gap-1.5">
          <span className="text-[12px] text-white/45">كلمة المرور <span className="text-white/30">(مطلوبة لموظف جديد، 8 أحرف على الأقل)</span></span>
          <input value={password} onChange={e => setPassword(e.target.value)} type="password" dir="ltr"
            autoComplete="new-password"
            className="staff-input max-w-xs" placeholder="••••••••" />
        </label>
        <div className="flex flex-col gap-1.5">
          <span className="text-[12px] text-white/45">الملاعب * <span className="text-white/30">(يمكن اختيار أكثر من ملعب)</span></span>
          {pitches.length === 0 ? (
            <p className="text-[12px] text-white/35">لا توجد ملاعب</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {pitches.map(p => {
                const on = selectedPitches.includes(p.id);
                return (
                  <button key={p.id} type="button" onClick={() => togglePitch(p.id)}
                    className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-semibold border transition-all ${
                      on ? 'bg-emerald-500/20 text-emerald-200 border-emerald-500/40'
                         : 'bg-white/[0.03] text-white/55 border-white/[0.08] hover:text-white/85'
                    }`}>
                    <MapPin size={12} aria-hidden /> {p.name}
                  </button>
                );
              })}
            </div>
          )}
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
          {staff.map(m => (
            <div key={m.user_id} className="flex items-start justify-between gap-4 px-5 py-3.5">
              <div className="min-w-0">
                <p className="text-[14px] font-bold text-[#f0efe8] truncate">{m.full_name || 'بدون اسم'}</p>
                <div className="flex items-center gap-1 mt-0.5 text-[12px] text-white/45 font-mono" dir="ltr">
                  <PhoneIcon size={11} aria-hidden /> {m.phone}
                </div>
                <div className="flex flex-wrap gap-1.5 mt-2">
                  {m.pitches.map(p => (
                    <span key={p.pitch_id} className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-emerald-500/10 text-[11px] text-emerald-200/90">
                      <MapPin size={10} aria-hidden /> {p.pitch_name}
                    </span>
                  ))}
                </div>
              </div>
              <button onClick={() => revoke(m)} disabled={revoking === m.user_id}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] font-semibold text-red-300 bg-red-500/[0.08] border border-red-500/20 hover:bg-red-500/[0.14] disabled:opacity-50 transition-all shrink-0">
                {revoking === m.user_id ? <Loader2 size={12} className="animate-spin" aria-hidden /> : <Trash2 size={12} aria-hidden />}
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
