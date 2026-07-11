'use client';

// Pitch management — ported (copy-adapt) from the legacy B2C dashboard
// (frontend/app/dashboard/page.tsx). Wires to the existing pitch CRUD endpoints,
// the Cloudinary signed-upload dropzone, and mandatory maps_url validation.
// Owner sees only their pitches (server-scoped); admin global.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Plus, Pencil, Trash2, X, ChevronDown, MapPin, Loader2, Ban, Clock, AlertTriangle } from 'lucide-react';
import api from '@/lib/api';
import PitchImageDropzone, { type PitchImageValue } from '@/components/PitchImageDropzone';
import BlocksModal from '@/components/BlocksModal';
import OperatingHoursModal from '@/components/OperatingHoursModal';

interface OwnerPitch {
  id: number;
  owner_id: number;
  name: string;
  neighborhood: string;
  surface: string;
  format: string;
  pricePerHour: number;
  description: string;
  isActive: boolean;
  image_url: string;
  image_public_id: string;
  maps_url: string;
  // WO-VENUES Gate 1d-minimal: grouping identity + in-venue label.
  venue_slug: string;
  venue_name: string;
  label: string;
}

// OwnerVenue — the subset of GET /owner/venues used by the «المجمع» dropdown.
interface OwnerVenue {
  id: number;
  name: string;
  neighborhood: string;
  maps_url: string;
  pitchCount: number;
}

interface PitchForm {
  name: string;
  neighborhood: string;
  surface: string;
  format: string;
  price_per_hour: string;
  description: string;
  image_url: string;
  image_public_id: string;
  maps_url: string;
}

const EMPTY_FORM: PitchForm = {
  name: '', neighborhood: '', surface: 'artificial_grass',
  format: 'خماسي', price_per_hour: '', description: '', image_url: '', image_public_id: '', maps_url: '',
};

const SURFACE_LABEL: Record<string, string> = {
  artificial_grass: 'عشبية صناعية', natural_grass: 'عشبية طبيعية', futsal_court: 'ملعب فوتسال',
};

const inputCls = [
  'w-full bg-white/[0.04] border border-white/[0.13] rounded-xl px-4 py-3',
  'text-[13px] text-[#f0efe8] placeholder:text-white/25',
  'hover:border-white/[0.22] focus:outline-none',
  'focus:border-emerald-500/60 focus:ring-2 focus:ring-emerald-500/20 transition-all duration-150',
].join(' ');
const labelCls = 'block text-[11px] font-semibold text-white/40 tracking-wide mb-1.5';

function pitchToForm(p: OwnerPitch): PitchForm {
  return {
    name: p.name, neighborhood: p.neighborhood, surface: p.surface, format: p.format,
    price_per_hour: String(p.pricePerHour), description: p.description ?? '',
    image_url: p.image_url ?? '', image_public_id: p.image_public_id ?? '', maps_url: p.maps_url ?? '',
  };
}

function AddPitchForm({ editing, onSuccess, onCancel }: {
  editing?: OwnerPitch | null;
  onSuccess: (p: OwnerPitch) => void;
  onCancel: () => void;
}) {
  const isEdit = !!editing;
  const [form, setForm] = useState<PitchForm>(editing ? pitchToForm(editing) : EMPTY_FORM);
  const [isSubmitting, setSubmit] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [mapsUrlError, setMapsUrlError] = useState<string | null>(null);

  // «المجمع» (Gate 1d-minimal, create only): '' = «مستقل» (standalone — today's
  // request, auto-1:1 venue). Selecting a venue sends venue_id, reveals the
  // optional label field, and prefills neighborhood/maps_url (still editable).
  const [venues, setVenues] = useState<OwnerVenue[]>([]);
  const [venueId, setVenueId] = useState<string>('');
  const [label, setLabel] = useState('');

  useEffect(() => {
    if (isEdit) return;
    api.get('/owner/venues')
      .then((res) => setVenues(res.data.data ?? []))
      .catch(() => setVenues([])); // dropdown degrades to «مستقل» only
  }, [isEdit]);

  const onVenueChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const next = e.target.value;
    setVenueId(next);
    if (next === '') return; // back to standalone: keep whatever the user typed
    const v = venues.find((x) => String(x.id) === next);
    if (!v) return;
    setMapsUrlError(null);
    setForm((prev) => ({ ...prev, neighborhood: v.neighborhood, maps_url: v.maps_url }));
  };

  const set = (field: keyof PitchForm) =>
    (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
      if (field === 'maps_url') setMapsUrlError(null);
      setForm((prev) => ({ ...prev, [field]: e.target.value }));
    };

  const isGoogleMapsURL = (raw: string) =>
    /^https:\/\/([a-z0-9-]+\.)*(google\.com|goo\.gl)(\/|$)/i.test(raw.trim());

  const handleImageChange = async (next: PitchImageValue) => {
    setForm((prev) => ({ ...prev, image_url: next.image_url, image_public_id: next.image_public_id }));
    if (!isEdit) return;
    try {
      await api.patch(`/pitches/${editing!.id}/image`, { image_url: next.image_url, public_id: next.image_public_id });
    } catch (err: any) {
      setError(err?.response?.data?.message ?? 'تعذّر حفظ صورة الملعب، يرجى المحاولة مجدداً');
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setMapsUrlError(null);
    const url = form.maps_url.trim();
    if (!isEdit && !url) { setMapsUrlError('رابط الموقع على خرائط جوجل مطلوب'); return; }
    if (url && !isGoogleMapsURL(url)) {
      setMapsUrlError('الرابط يجب أن يكون رابط مشاركة من خرائط جوجل (google.com أو goo.gl)');
      return;
    }
    setSubmit(true);
    try {
      const payload: Record<string, unknown> = { ...form, price_per_hour: Number(form.price_per_hour) };
      // Standalone («مستقل»): request unchanged — no venue_id/label keys at all.
      if (!isEdit && venueId !== '') {
        payload.venue_id = Number(venueId);
        if (label.trim()) payload.label = label.trim();
      }
      const res = isEdit ? await api.patch(`/pitches/${editing!.id}`, payload) : await api.post('/pitches', payload);
      onSuccess(res.data.data as OwnerPitch);
    } catch (err: any) {
      const data = err?.response?.data;
      if (data?.field === 'maps_url') setMapsUrlError(data?.message ?? 'رابط الموقع غير صالح');
      else setError(data?.message ?? (isEdit ? 'تعذّر تحديث الملعب' : 'تعذّر إنشاء الملعب'));
      setSubmit(false);
    }
  };

  return (
    <div className="rounded-2xl bg-[#141715] border border-white/[0.10] mb-6 overflow-hidden">
      <div className="px-6 py-4 border-b border-white/[0.06] flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
            {isEdit ? <Pencil size={13} className="text-emerald-400" aria-hidden /> : <Plus size={13} className="text-emerald-400" aria-hidden />}
          </div>
          <span className="text-[13px] font-semibold">{isEdit ? 'تعديل الملعب' : 'إضافة ملعب جديد'}</span>
        </div>
        <button type="button" onClick={onCancel} className="text-white/25 hover:text-white/55" aria-label="إغلاق"><X size={16} /></button>
      </div>
      <form onSubmit={handleSubmit} className="p-6">
        {!isEdit && venues.length > 0 && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 mb-5">
            <div className="relative">
              <label className={labelCls}>المجمع</label>
              <select value={venueId} onChange={onVenueChange} className={`${inputCls} appearance-none pe-9`}>
                <option value="">مستقل</option>
                {venues.map((v) => (
                  <option key={v.id} value={v.id}>{v.name}</option>
                ))}
              </select>
              <ChevronDown size={13} className="absolute end-3 bottom-[13px] text-white/25 pointer-events-none" aria-hidden />
            </div>
            {venueId !== '' && (
              <div>
                <label className={labelCls}>تسمية الملعب</label>
                <input type="text" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="ملعب ١" className={inputCls} />
                <p className="text-[11px] text-white/35 mt-1.5">اسم قصير يميّز الملعب داخل المجمع — اختياري.</p>
              </div>
            )}
          </div>
        )}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 mb-5">
          <div>
            <label className={labelCls}>اسم الملعب <span className="text-red-400/60">*</span></label>
            <input type="text" value={form.name} onChange={set('name')} required placeholder="مثال: ملعب الحسين" className={inputCls} />
          </div>
          <div>
            <label className={labelCls}>الحي / الموقع <span className="text-red-400/60">*</span></label>
            <input type="text" value={form.neighborhood} onChange={set('neighborhood')} required placeholder="مثال: خلدا" className={inputCls} />
          </div>
          <div>
            <label className={labelCls}>رابط الموقع على خرائط Google <span className="text-red-400/60">*</span></label>
            <input type="url" dir="ltr" value={form.maps_url} onChange={set('maps_url')} placeholder="https://maps.app.goo.gl/..." required={!isEdit}
              aria-invalid={!!mapsUrlError} className={`${inputCls} ${mapsUrlError ? '!border-red-500/60 focus:!ring-red-500/20' : ''}`} />
            {mapsUrlError ? <p className="text-[11px] text-red-400 mt-1.5">{mapsUrlError}</p>
              : <p className="text-[11px] text-white/35 mt-1.5">افتح الملعب على خرائط جوجل، «مشاركة» ثم «نسخ الرابط»، والصقه هنا.</p>}
          </div>
          <div className="relative">
            <label className={labelCls}>نوع الأرضية <span className="text-red-400/60">*</span></label>
            <select value={form.surface} onChange={set('surface')} className={`${inputCls} appearance-none pe-9`}>
              <option value="artificial_grass">عشبية صناعية</option>
              <option value="natural_grass">عشبية طبيعية</option>
              <option value="futsal_court">ملعب فوتسال</option>
            </select>
            <ChevronDown size={13} className="absolute end-3 bottom-[13px] text-white/25 pointer-events-none" aria-hidden />
          </div>
          <div className="relative">
            <label className={labelCls}>صيغة اللعب <span className="text-red-400/60">*</span></label>
            <select value={form.format} onChange={set('format')} className={`${inputCls} appearance-none pe-9`}>
              <option value="خماسي">خماسي (5v5)</option>
              <option value="سباعي">سباعي (7v7)</option>
            </select>
            <ChevronDown size={13} className="absolute end-3 bottom-[13px] text-white/25 pointer-events-none" aria-hidden />
          </div>
          <div>
            <label className={labelCls}>السعر بالساعة (دينار) <span className="text-red-400/60">*</span></label>
            <div className="relative">
              <input type="number" min="1" lang="en" inputMode="numeric" value={form.price_per_hour} onChange={set('price_per_hour')} required placeholder="25" className={`${inputCls} pe-12`} />
              <span className="absolute end-4 top-1/2 -translate-y-1/2 text-[11px] text-emerald-500 font-semibold pointer-events-none">د.أ</span>
            </div>
          </div>
          <div className="sm:col-span-2">
            <label className={labelCls}>صورة الملعب</label>
            <PitchImageDropzone value={{ image_url: form.image_url, image_public_id: form.image_public_id }} onChange={handleImageChange} />
          </div>
        </div>
        <div className="mb-6">
          <label className={labelCls}>الوصف</label>
          <textarea value={form.description} onChange={set('description')} rows={3} placeholder="صف الملعب: المرافق، الإضاءة، موقف السيارات..." className={`${inputCls} resize-none`} />
        </div>
        {error && <p className="text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-xl px-4 py-3 mb-4">{error}</p>}
        <div className="flex items-center justify-end gap-3">
          <button type="button" onClick={onCancel} className="px-5 py-2.5 rounded-xl text-[12px] font-semibold text-white/40 hover:text-white/65 border border-white/[0.07] hover:border-white/[0.14] transition-all">إلغاء</button>
          <button type="submit" disabled={isSubmitting} className="flex items-center gap-2 px-6 py-2.5 rounded-xl text-[12px] font-bold bg-[#0f4c3a] text-emerald-400 border border-emerald-500/20 hover:bg-[#1a6b52] hover:text-emerald-300 hover:border-emerald-500/40 disabled:opacity-50 transition-all">
            {isSubmitting ? <><span className="w-3.5 h-3.5 rounded-full border-2 border-emerald-400/50 border-t-transparent animate-spin" aria-hidden />{isEdit ? 'جاري الحفظ...' : 'جاري الإضافة...'}</> : <>{isEdit ? 'حفظ التعديلات' : 'إضافة الملعب'}</>}
          </button>
        </div>
      </form>
    </div>
  );
}

export default function PitchesPage() {
  const [pitches, setPitches] = useState<OwnerPitch[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [editTarget, setEditTarget] = useState<OwnerPitch | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<OwnerPitch | null>(null);
  const [isDeleting, setIsDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [togglingId, setTogglingId] = useState<number | null>(null);
  const [blocksTarget, setBlocksTarget] = useState<OwnerPitch | null>(null);
  const [hoursTarget, setHoursTarget] = useState<OwnerPitch | null>(null);
  // Lightweight inline toast (copy-adapted from the legacy dashboard, zero deps) —
  // surfaces the activate/deactivate rollback that was previously silent.
  const [toast, setToast] = useState<string | null>(null);

  useEffect(() => {
    api.get('/owner/pitches')
      .then((res) => setPitches(res.data.data ?? []))
      .catch(() => setError('تعذّر تحميل الملاعب.'))
      .finally(() => setLoading(false));
  }, []);

  const onAdded = useCallback((p: OwnerPitch) => { setPitches((prev) => [p, ...prev]); setShowAdd(false); }, []);
  const onUpdated = useCallback((u: OwnerPitch) => { setPitches((prev) => prev.map((p) => (p.id === u.id ? u : p))); setEditTarget(null); }, []);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return;
    setIsDeleting(true); setDeleteError(null);
    try {
      await api.delete(`/pitches/${deleteTarget.id}`);
      setPitches((prev) => prev.filter((p) => p.id !== deleteTarget.id));
      setDeleteTarget(null);
    } catch (err: any) {
      setDeleteError(err?.response?.data?.message ?? 'تعذّر حذف الملعب.');
    } finally {
      setIsDeleting(false);
    }
  }, [deleteTarget]);

  const toggleActive = useCallback(async (p: OwnerPitch) => {
    const next = !p.isActive;
    setToast(null);
    setTogglingId(p.id);
    setPitches((prev) => prev.map((x) => (x.id === p.id ? { ...x, isActive: next } : x)));
    try {
      await api.patch(`/pitches/${p.id}/active`, { is_active: next });
    } catch (err: any) {
      // Roll back to the previous state and surface the failure (was silent).
      setPitches((prev) => prev.map((x) => (x.id === p.id ? { ...x, isActive: p.isActive } : x)));
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

  // Gate 1d-minimal grouping: a venue header appears ONLY when a venue holds
  // more than one pitch (collapse rule — a lone pitch stays a flat card with
  // zero venue vocabulary). Consecutive flat cards share one grid so the
  // no-multi-venue owner sees today's layout unchanged.
  const cardBlocks = useMemo(() => {
    const bySlug = new Map<string, OwnerPitch[]>();
    for (const p of pitches) {
      if (!p.venue_slug) continue;
      bySlug.set(p.venue_slug, [...(bySlug.get(p.venue_slug) ?? []), p]);
    }
    const blocks: Array<{ key: string; venueName?: string; items: OwnerPitch[] }> = [];
    const grouped = new Set<string>();
    let flat: OwnerPitch[] = [];
    const flushFlat = () => {
      if (flat.length) { blocks.push({ key: `flat-${blocks.length}`, items: flat }); flat = []; }
    };
    for (const p of pitches) {
      const group = p.venue_slug ? bySlug.get(p.venue_slug) ?? [] : [];
      if (group.length > 1) {
        if (grouped.has(p.venue_slug)) continue;
        grouped.add(p.venue_slug);
        flushFlat();
        blocks.push({ key: `venue-${p.venue_slug}`, venueName: p.venue_name, items: group });
      } else {
        flat.push(p);
      }
    }
    flushFlat();
    return blocks;
  }, [pitches]);

  // One card, identical markup in both contexts; inside a venue group the
  // title is the in-venue label (name fallback).
  const renderCard = (p: OwnerPitch, inGroup: boolean) => (
    <div key={p.id} className="rounded-2xl bg-[#141715] border border-white/[0.08] overflow-hidden flex flex-col">
      <div className="h-32 bg-white/[0.03]">
        {p.image_url
          // eslint-disable-next-line @next/next/no-img-element
          ? <img src={p.image_url} alt={p.name} className="w-full h-full object-cover" />
          : <div className="w-full h-full flex items-center justify-center"><MapPin size={22} className="text-white/15" aria-hidden /></div>}
      </div>
      <div className="p-4 flex flex-col gap-2 flex-1">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-[14px] font-bold truncate">{inGroup ? (p.label || p.name) : p.name}</h3>
          <span className={`text-[10px] font-bold rounded-full border px-2 py-0.5 ${p.isActive ? 'text-emerald-300 border-emerald-500/30 bg-emerald-500/15' : 'text-white/40 border-white/10 bg-white/[0.04]'}`}>
            {p.isActive ? 'نشط' : 'معطّل'}
          </span>
        </div>
        <p className="text-[11.5px] text-white/45">{p.neighborhood} · {SURFACE_LABEL[p.surface] ?? p.surface} · {p.format}</p>
        <p className="text-[12px] text-emerald-300/90 font-bold">{p.pricePerHour} د.أ / ساعة</p>
        <div className="mt-auto pt-2 flex flex-wrap items-center gap-2">
          <button type="button" onClick={() => { setShowAdd(false); setEditTarget(p); }} className="inline-flex items-center gap-1 rounded-lg border border-white/10 px-2.5 py-1.5 text-[11px] font-semibold text-white/60 hover:text-white/90 transition-colors">
            <Pencil size={12} aria-hidden /> تعديل
          </button>
          <button type="button" onClick={() => setHoursTarget(p)} className="inline-flex items-center gap-1 rounded-lg border border-white/10 px-2.5 py-1.5 text-[11px] font-semibold text-white/60 hover:text-emerald-300 hover:border-emerald-500/30 transition-colors" title="أوقات العمل">
            <Clock size={12} aria-hidden /> الأوقات
          </button>
          <button type="button" onClick={() => setBlocksTarget(p)} className="inline-flex items-center gap-1 rounded-lg border border-amber-500/20 px-2.5 py-1.5 text-[11px] font-semibold text-amber-400/80 hover:text-amber-300 transition-colors">
            <Ban size={12} aria-hidden /> الحجب
          </button>
          <button type="button" onClick={() => { setDeleteError(null); setDeleteTarget(p); }} className="inline-flex items-center gap-1 rounded-lg border border-red-500/15 px-2.5 py-1.5 text-[11px] font-semibold text-red-400/80 hover:text-red-400 transition-colors">
            <Trash2 size={12} aria-hidden /> حذف
          </button>
          <button type="button" disabled={togglingId === p.id} onClick={() => toggleActive(p)} className="ms-auto text-[11px] font-semibold text-white/45 hover:text-white/75 disabled:opacity-50 transition-colors">
            {p.isActive ? 'تعطيل' : 'تفعيل'}
          </button>
        </div>
      </div>
    </div>
  );

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-[20px] font-bold tracking-tight">الملاعب</h1>
        {!showAdd && !editTarget && (
          <button type="button" onClick={() => setShowAdd(true)} className="inline-flex items-center gap-1.5 rounded-xl bg-[#0f4c3a] text-emerald-300 border border-emerald-500/30 px-4 py-2 text-[12.5px] font-bold hover:bg-[#1a6b52] transition-colors">
            <Plus size={14} aria-hidden /> إضافة ملعب
          </button>
        )}
      </div>

      {showAdd && <AddPitchForm onSuccess={onAdded} onCancel={() => setShowAdd(false)} />}
      {editTarget && <AddPitchForm editing={editTarget} onSuccess={onUpdated} onCancel={() => setEditTarget(null)} />}

      {loading ? (
        <div className="flex justify-center py-24"><Loader2 className="animate-spin text-white/30" /></div>
      ) : error ? (
        <div className="rounded-xl border border-red-500/15 bg-red-500/[0.06] px-4 py-3 text-[12.5px] text-red-400">{error}</div>
      ) : pitches.length === 0 ? (
        <div className="rounded-2xl bg-[#141715] border border-white/[0.08] p-12 text-center text-[13px] text-white/35">لا ملاعب بعد.</div>
      ) : (
        cardBlocks.map((block) => (
          <div key={block.key} className="flex flex-col gap-3">
            {block.venueName && (
              <div className="flex items-center gap-2 px-1">
                <MapPin size={13} className="text-emerald-400/70 flex-shrink-0" aria-hidden />
                <h2 className="text-[13.5px] font-bold truncate">{block.venueName}</h2>
                <span className="text-[10.5px] font-semibold text-white/40 bg-white/[0.05] border border-white/[0.08] rounded-full px-2 py-0.5 flex-shrink-0">
                  {block.items.length} ملاعب
                </span>
              </div>
            )}
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {block.items.map((p) => renderCard(p, !!block.venueName))}
            </div>
          </div>
        ))
      )}

      {blocksTarget && (
        <BlocksModal pitchId={blocksTarget.id} pitchName={blocksTarget.name} onClose={() => setBlocksTarget(null)} />
      )}

      {hoursTarget && (
        <OperatingHoursModal pitchId={hoursTarget.id} pitchName={hoursTarget.name} onClose={() => setHoursTarget(null)} />
      )}

      {toast && (
        <div role="alert" className="fixed bottom-6 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2.5 px-4 py-3 rounded-xl bg-[#1a1110] border border-red-500/25 shadow-2xl">
          <AlertTriangle size={15} className="text-red-400 flex-shrink-0" aria-hidden />
          <span className="text-[12.5px] font-semibold text-red-300">{toast}</span>
          <button type="button" onClick={() => setToast(null)} className="text-white/30 hover:text-white/60 transition-colors duration-150 ms-1" aria-label="إغلاق التنبيه">
            <X size={14} aria-hidden />
          </button>
        </div>
      )}

      {deleteTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4" onClick={() => !isDeleting && setDeleteTarget(null)}>
          <div className="w-full max-w-sm rounded-2xl bg-[#1a1c1b] border border-white/[0.08] p-6" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-[15px] font-bold mb-2">حذف الملعب</h2>
            <p className="text-[12.5px] text-white/55 mb-4">هل تريد حذف «{deleteTarget.name}»؟ لا يمكن الحذف إن كان عليه حجوزات قادمة.</p>
            {deleteError && <p className="text-[12px] text-red-400 bg-red-500/[0.06] border border-red-500/15 rounded-lg px-3 py-2 mb-3">{deleteError}</p>}
            <div className="flex items-center justify-end gap-3">
              <button type="button" disabled={isDeleting} onClick={() => setDeleteTarget(null)} className="px-4 py-2 rounded-lg text-[12px] font-semibold text-white/45 hover:text-white/70 border border-white/10">إلغاء</button>
              <button type="button" disabled={isDeleting} onClick={confirmDelete} className="px-4 py-2 rounded-lg text-[12px] font-bold bg-red-500/15 text-red-300 border border-red-500/30 hover:bg-red-500/25 disabled:opacity-50">
                {isDeleting ? 'جاري الحذف...' : 'حذف'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
