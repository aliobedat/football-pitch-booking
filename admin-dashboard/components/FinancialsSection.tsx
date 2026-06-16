'use client';

// Financials — Net Profit + Expense Ledger (Cockpit WO-F2). Mounted inside the
// existing Analytics tab (no new dashboard). The Net equation reads as one line:
// Collected − Expenses = Net (negative styled red). Below it, owner-scoped expense
// CRUD (add / edit / soft-delete) with per-category subtotals. RTL, Western
// numerals, admin theme tokens. Collected is the WO-F1 figure (server-reused).

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Wallet, Receipt, Plus, Pencil, Trash2, Loader2, X } from 'lucide-react';
import api from '@/lib/api';
import { formatCurrency, formatNumber, formatDate } from '@/lib/format';

type Granularity = 'day' | 'week' | 'month';

interface CategorySubtotal { category: string; total: number; }
interface NetSummary {
  from: string; to: string;
  collected: number; expenses: number; net: number;
  by_category: CategorySubtotal[] | null;
}
interface Expense {
  id: number; pitch_id: number | null; pitch_name?: string | null;
  category: string; amount: number; occurred_at: string; note?: string | null;
}
interface PitchOpt { id: number; name: string; }

const CATEGORIES = ['Electricity', 'Staff', 'Water', 'Maintenance', 'Marketing', 'Other'] as const;
const CAT_AR: Record<string, string> = {
  Electricity: 'كهرباء', Staff: 'موظفون', Water: 'مياه',
  Maintenance: 'صيانة', Marketing: 'تسويق', Other: 'أخرى',
};

const ammanToday = () => new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Amman' }).format(new Date());
const isoToDay = (iso: string) => new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Amman' }).format(new Date(iso));

const JOD = (v: number) => <>{formatCurrency(v, { minimumFractionDigits: 2 })}<span className="text-[11px] text-emerald-500/70 ms-1">د.أ</span></>;

interface FormState {
  id: number | null; category: string; amount: string; occurred_on: string;
  pitch_id: string; note: string;
}
const emptyForm = (): FormState => ({ id: null, category: 'Electricity', amount: '', occurred_on: ammanToday(), pitch_id: '', note: '' });

export default function FinancialsSection({ granularity }: { granularity: Granularity }) {
  const [summary, setSummary]   = useState<NetSummary | null>(null);
  const [expenses, setExpenses] = useState<Expense[]>([]);
  const [pitches, setPitches]   = useState<PitchOpt[]>([]);
  const [loading, setLoading]   = useState(true);
  const [error, setError]       = useState<string | null>(null);
  const [catFilter, setCatFilter] = useState('');

  const [form, setForm]     = useState<FormState>(emptyForm);
  const [saving, setSaving] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [sumRes, expRes] = await Promise.all([
        api.get('/owner/financials', { params: { granularity } }),
        api.get('/owner/expenses', { params: catFilter ? { category: catFilter } : {} }),
      ]);
      setSummary(sumRes.data.data as NetSummary);
      setExpenses(expRes.data.data ?? []);
    } catch {
      setError('تعذّر تحميل البيانات المالية.');
    } finally {
      setLoading(false);
    }
  }, [granularity, catFilter]);

  useEffect(() => { refresh(); }, [refresh]);
  // Pitch options for the optional tag (load once).
  useEffect(() => {
    api.get('/owner/pitches').then(r => setPitches((r.data.data ?? []).map((p: any) => ({ id: p.id, name: p.name }))))
      .catch(() => { /* optional — overhead expenses don't need a pitch */ });
  }, []);

  const submit = useCallback(async () => {
    const amount = Number(form.amount);
    if (!(amount > 0)) { setFormErr('أدخل مبلغاً صحيحاً أكبر من صفر'); return; }
    setSaving(true);
    setFormErr(null);
    const body = {
      category: form.category,
      amount,
      occurred_on: form.occurred_on,
      pitch_id: form.pitch_id ? Number(form.pitch_id) : null,
      note: form.note,
    };
    try {
      if (form.id) await api.patch(`/owner/expenses/${form.id}`, body);
      else await api.post('/owner/expenses', body);
      setForm(emptyForm());
      await refresh();
    } catch (e: any) {
      setFormErr(e?.response?.data?.message ?? 'تعذّر حفظ المصروف.');
    } finally {
      setSaving(false);
    }
  }, [form, refresh]);

  const startEdit = (e: Expense) => {
    setFormErr(null);
    setForm({
      id: e.id, category: e.category, amount: String(e.amount),
      occurred_on: isoToDay(e.occurred_at), pitch_id: e.pitch_id ? String(e.pitch_id) : '', note: e.note ?? '',
    });
  };

  const remove = async (e: Expense) => {
    if (!confirm(`حذف هذا المصروف (${CAT_AR[e.category]} — ${formatCurrency(e.amount, { minimumFractionDigits: 2 })} د.أ)؟`)) return;
    try { await api.delete(`/owner/expenses/${e.id}`); await refresh(); } catch { /* keep state on failure */ }
  };

  const net = summary?.net ?? 0;
  const netNegative = net < 0;

  return (
    <div className="flex flex-col gap-5">
      {/* ── Net equation ── */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-4">صافي الربح (نقدي) — {summary ? `${summary.from} ← ${summary.to}` : '—'}</p>
        {loading && !summary ? (
          <div className="h-16 flex items-center"><Loader2 size={20} className="text-emerald-500 animate-spin" /></div>
        ) : (
          <div className="flex items-center flex-wrap gap-x-3 gap-y-2 text-[15px]">
            <Leg label="المحصّل" value={summary?.collected ?? 0} tone="text-emerald-300" />
            <span className="text-white/30 text-[20px] font-light">−</span>
            <Leg label="المصروفات" value={summary?.expenses ?? 0} tone="text-amber-300" />
            <span className="text-white/30 text-[20px] font-light">=</span>
            <div className="flex flex-col">
              <span className="text-[10px] text-white/35">الصافي</span>
              <span className={`text-[24px] font-bold leading-none ${netNegative ? 'text-red-400' : 'text-[#f0efe8]'}`}>
                {netNegative && '−'}{JOD(Math.abs(net))}
              </span>
            </div>
          </div>
        )}
        {/* per-category subtotals */}
        {summary?.by_category && summary.by_category.length > 0 && (
          <div className="flex flex-wrap gap-2 mt-4 pt-4 border-t border-white/[0.05]">
            {summary.by_category.map(c => (
              <span key={c.category} className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg bg-white/[0.03] border border-white/[0.07] text-[11px] text-white/60">
                {CAT_AR[c.category] ?? c.category}
                <span className="font-mono text-amber-300/80">{formatCurrency(c.total, { minimumFractionDigits: 2 })}</span>
              </span>
            ))}
          </div>
        )}
      </div>

      {/* ── Add / edit expense ── */}
      <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5">
        <p className="text-[12px] text-white/40 mb-3 flex items-center gap-2">
          <Receipt size={13} aria-hidden /> {form.id ? 'تعديل مصروف' : 'إضافة مصروف'}
        </p>
        <div className="flex flex-wrap items-end gap-3">
          <Field label="الفئة">
            <select value={form.category} onChange={e => setForm(f => ({ ...f, category: e.target.value }))} className="fin-input min-w-[120px]">
              {CATEGORIES.map(c => <option key={c} value={c} className="bg-[#0f1110]">{CAT_AR[c]}</option>)}
            </select>
          </Field>
          <Field label="المبلغ (د.أ)">
            <input type="number" min="0" step="0.001" value={form.amount} dir="ltr"
              onChange={e => setForm(f => ({ ...f, amount: e.target.value }))} className="fin-input font-mono w-[110px]" placeholder="0.000" />
          </Field>
          <Field label="التاريخ">
            <input type="date" value={form.occurred_on} dir="ltr"
              onChange={e => setForm(f => ({ ...f, occurred_on: e.target.value }))} className="fin-input" />
          </Field>
          <Field label="الملعب (اختياري)">
            <select value={form.pitch_id} onChange={e => setForm(f => ({ ...f, pitch_id: e.target.value }))} className="fin-input min-w-[130px]">
              <option value="" className="bg-[#0f1110]">عام / تشغيلي</option>
              {pitches.map(p => <option key={p.id} value={p.id} className="bg-[#0f1110]">{p.name}</option>)}
            </select>
          </Field>
          <Field label="ملاحظة">
            <input value={form.note} onChange={e => setForm(f => ({ ...f, note: e.target.value }))} className="fin-input w-[160px]" placeholder="اختياري" />
          </Field>
          <button onClick={submit} disabled={saving}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl text-[12px] font-bold bg-emerald-500/[0.12] text-emerald-400 border border-emerald-500/25 hover:bg-emerald-500/[0.18] disabled:opacity-50 transition-all">
            {saving ? <Loader2 size={13} className="animate-spin" /> : form.id ? <Pencil size={13} /> : <Plus size={13} />}
            {form.id ? 'حفظ' : 'إضافة'}
          </button>
          {form.id && (
            <button onClick={() => { setForm(emptyForm()); setFormErr(null); }} className="inline-flex items-center gap-1 px-3 py-2 rounded-xl text-[12px] text-white/50 hover:text-white/80 border border-white/[0.08] transition-all">
              <X size={13} /> إلغاء
            </button>
          )}
        </div>
        {formErr && <p className="text-[12px] text-red-400 mt-2.5">{formErr}</p>}
      </div>

      {/* ── Ledger ── */}
      <div className="rounded-2xl border border-white/[0.07] overflow-hidden">
        <div className="px-5 py-3.5 border-b border-white/[0.06] bg-[#0f1110] flex items-center justify-between gap-3">
          <p className="text-[11px] font-semibold text-white/35 tracking-widest uppercase flex items-center gap-2"><Wallet size={13} /> سجل المصروفات</p>
          <select value={catFilter} onChange={e => setCatFilter(e.target.value)} className="fin-input !py-1.5 text-[11px]">
            <option value="" className="bg-[#0f1110]">كل الفئات</option>
            {CATEGORIES.map(c => <option key={c} value={c} className="bg-[#0f1110]">{CAT_AR[c]}</option>)}
          </select>
        </div>
        {error ? (
          <div className="bg-[#141715] px-5 py-4 text-[12.5px] text-red-400">{error}</div>
        ) : expenses.length === 0 ? (
          <div className="bg-[#141715] py-10 text-center text-[13px] text-white/30">لا مصروفات في هذه الفترة</div>
        ) : (
          <div className="bg-[#141715] divide-y divide-white/[0.04]">
            {expenses.map(e => (
              <div key={e.id} className="px-5 py-3 flex items-center gap-4">
                <span className="inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold bg-white/[0.04] border border-white/[0.08] text-white/60 min-w-[64px] justify-center">
                  {CAT_AR[e.category] ?? e.category}
                </span>
                <span className="text-[13px] font-bold text-amber-300/90 font-mono min-w-[90px]" dir="ltr">
                  {formatCurrency(e.amount, { minimumFractionDigits: 2 })}
                </span>
                <span className="text-[11px] text-white/45 min-w-[90px]">{formatDate(e.occurred_at)}</span>
                <span className="text-[11px] text-white/40 flex-1 truncate">
                  {e.pitch_name ? e.pitch_name : 'عام / تشغيلي'}{e.note ? ` · ${e.note}` : ''}
                </span>
                <button onClick={() => startEdit(e)} className="text-white/35 hover:text-emerald-300 transition-colors" aria-label="تعديل"><Pencil size={14} /></button>
                <button onClick={() => remove(e)} className="text-white/35 hover:text-red-400 transition-colors" aria-label="حذف"><Trash2 size={14} /></button>
              </div>
            ))}
          </div>
        )}
      </div>

      <style jsx>{`
        .fin-input {
          background: #0f1110; border: 1px solid rgba(255,255,255,0.09); border-radius: 10px;
          padding: 8px 12px; font-size: 12px; color: rgba(255,255,255,0.85); outline: none;
        }
        .fin-input:focus { border-color: rgba(16,185,129,0.4); }
      `}</style>
    </div>
  );
}

function Leg({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-[10px] text-white/35">{label}</span>
      <span className={`text-[18px] font-bold leading-none ${tone}`}>{formatNumber(value, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}</span>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-[11px] text-white/40">{label}</span>
      {children}
    </label>
  );
}
