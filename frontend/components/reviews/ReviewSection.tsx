'use client';

// Verified-review zone for the pitch detail page. Owns three concerns:
//   1. Summary — driven by the dedicated aggregate endpoint (COUNT < 3 hides the
//      numeric score behind "جديد / لا تقييمات بعد").
//   2. Interaction — eligibility decides Hidden / Write / Edit (nulls are safe).
//   3. Listing + moderation — masked names, Report (any logged-in user), Delete
//      (admin only).
// All IDs are numbers (int64). The reviewer_name is already masked server-side.

import { useState, useEffect, useCallback } from 'react';
import axios from 'axios';
import { Star, Flag, Trash2, Loader2 } from 'lucide-react';
import api from '@/lib/api';
import { useAuth } from '@/context/AuthContext';
import type { Review, RatingAggregate, ReviewEligibility } from '@/lib/types';
import FullNameField, { isValidFullName, saveFullName } from '@/components/FullNameField';

interface Props {
  pitchId: number;
}

// ─── Star primitives ──────────────────────────────────────────────────────────

function StarRow({ value }: { value: number }) {
  return (
    <div className="flex items-center gap-0.5" aria-label={`${value} من 5`}>
      {[1, 2, 3, 4, 5].map((n) => (
        <Star
          key={n}
          size={13}
          className={n <= value ? 'text-amber-400' : 'text-white/15'}
          fill={n <= value ? 'currentColor' : 'none'}
          aria-hidden
        />
      ))}
    </div>
  );
}

function StarPicker({
  value,
  onChange,
  disabled,
}: {
  value: number;
  onChange: (v: number) => void;
  disabled?: boolean;
}) {
  return (
    <div className="flex items-center gap-1" role="radiogroup" aria-label="التقييم">
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          disabled={disabled}
          onClick={() => onChange(n)}
          aria-label={`${n} نجوم`}
          aria-checked={value === n}
          role="radio"
          className="p-0.5 disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500 rounded"
        >
          <Star
            size={22}
            className={n <= value ? 'text-amber-400' : 'text-white/20 hover:text-white/40'}
            fill={n <= value ? 'currentColor' : 'none'}
          />
        </button>
      ))}
    </div>
  );
}

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString('ar-JO', { year: 'numeric', month: 'long', day: 'numeric' });
  } catch {
    return '';
  }
}

// ─── Write / Edit form ────────────────────────────────────────────────────────

function ReviewForm({
  pitchId,
  existing,
  qualifyingBookingId,
  onSaved,
}: {
  pitchId: number;
  existing: Review | null;
  qualifyingBookingId: number | null;
  onSaved: () => void;
}) {
  const { user, refreshUser } = useAuth();
  const [rating, setRating] = useState(existing?.rating ?? 0);
  const [comment, setComment] = useState(existing?.comment ?? '');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Defensive JIT name fallback — mirrors the booking checkout.
  const needsName = !!user && !user.full_name?.trim();
  const [nameInput, setNameInput] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const nameOK = !needsName || isValidFullName(nameInput);

  const isEdit = !!existing;
  const canSubmit = rating >= 1 && rating <= 5 && nameOK && !submitting;

  async function handleSubmit() {
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);

    if (needsName) {
      try {
        await saveFullName(nameInput);
        await refreshUser();
      } catch {
        setError('تعذّر حفظ الاسم، حاول مجدداً');
        setSubmitting(false);
        return;
      }
    }

    // The qualifying booking is derived server-side from the authoritative
    // eligibility re-check — the client strictly sends only rating + comment.
    const body = { rating, comment: comment.trim() || null };
    try {
      if (isEdit) {
        await api.put(`/reviews/${existing!.id}`, body);
      } else {
        await api.post(`/pitches/${pitchId}/reviews`, body);
      }
      onSaved();
    } catch (err) {
      if (axios.isAxiosError(err)) {
        const code = err.response?.data?.error as string | undefined;
        if (code === 'already_reviewed') setError('لقد قمت بتقييم هذا الملعب مسبقاً');
        else if (code === 'invalid_booking') setError('لا يوجد حجز مؤهل لتقييم هذا الملعب');
        else setError(err.response?.data?.message ?? 'تعذّر حفظ التقييم، حاول مجدداً');
      } else {
        setError('تعذّر الاتصال بالخادم');
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="rounded-xl border border-white/[0.07] bg-[#141715] p-5 flex flex-col gap-4">
      <h3 className="text-[14px] font-bold text-[#f0efe8]">
        {isEdit ? 'تعديل تقييمك' : 'اكتب تقييمك'}
      </h3>

      <StarPicker value={rating} onChange={setRating} disabled={submitting} />

      <textarea
        dir="rtl"
        value={comment}
        maxLength={1000}
        disabled={submitting}
        onChange={(e) => setComment(e.target.value)}
        placeholder="شاركنا تجربتك في هذا الملعب (اختياري)"
        rows={3}
        className="w-full rounded-xl px-4 py-2.5 bg-[#0d0f0e] text-[13px] text-[#f0efe8] border border-white/[0.09] placeholder:text-white/20 focus:outline-none focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/15 transition-all duration-150 resize-none disabled:opacity-50"
      />

      {needsName && (
        <FullNameField
          value={nameInput}
          onChange={(v) => { setNameInput(v); setNameTouched(true); }}
          disabled={submitting}
          showError={nameTouched}
          id="review-full-name"
        />
      )}

      {error && (
        <p role="alert" className="text-[11px] text-red-400 bg-red-500/[0.07] border border-red-500/[0.14] rounded-lg px-3 py-2">
          {error}
        </p>
      )}

      <button
        type="button"
        onClick={handleSubmit}
        disabled={!canSubmit}
        className={[
          'flex items-center justify-center gap-2 w-full py-3 rounded-xl text-[13px] font-bold transition-all duration-200',
          canSubmit
            ? 'bg-gradient-to-r from-green-600 to-emerald-500 text-white'
            : 'bg-white/[0.04] text-white/20 border border-white/[0.05] cursor-not-allowed',
        ].join(' ')}
      >
        {submitting ? <Loader2 size={15} className="animate-spin" /> : isEdit ? 'حفظ التعديل' : 'إرسال التقييم'}
      </button>
    </div>
  );
}

// ─── Single review row ────────────────────────────────────────────────────────

function ReviewRow({
  review,
  onFlag,
  onDelete,
}: {
  review: Review;
  onFlag: (id: number) => void;
  onDelete: (id: number) => void;
}) {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';

  return (
    <div className="rounded-xl border border-white/[0.06] bg-[#141715] p-4 flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <div className="w-8 h-8 rounded-full bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center text-[12px] font-bold text-emerald-400">
            {review.reviewer_name?.[0] ?? '؟'}
          </div>
          <div>
            <p className="text-[12px] font-bold text-[#f0efe8] leading-tight">
              {review.reviewer_name || 'لاعب'}
            </p>
            <p className="text-[10px] text-white/30">{fmtDate(review.created_at)}</p>
          </div>
        </div>
        <StarRow value={review.rating} />
      </div>

      {review.comment && (
        <p className="text-[12px] text-white/55 leading-relaxed">{review.comment}</p>
      )}

      {user && (
        <div className="flex items-center gap-3 pt-1">
          <button
            type="button"
            onClick={() => onFlag(review.id)}
            className="flex items-center gap-1 text-[10px] text-white/30 hover:text-amber-400 transition-colors"
          >
            <Flag size={11} aria-hidden /> إبلاغ
          </button>
          {isAdmin && (
            <button
              type="button"
              onClick={() => onDelete(review.id)}
              className="flex items-center gap-1 text-[10px] text-white/30 hover:text-red-400 transition-colors"
            >
              <Trash2 size={11} aria-hidden /> حذف
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// ─── Section ──────────────────────────────────────────────────────────────────

const MIN_REVIEWS_FOR_SCORE = 3;

export default function ReviewSection({ pitchId }: Props) {
  const { user } = useAuth();
  const [reviews, setReviews] = useState<Review[]>([]);
  const [aggregate, setAggregate] = useState<RatingAggregate>({ average: 0, count: 0 });
  const [eligibility, setEligibility] = useState<ReviewEligibility | null>(null);
  const [loading, setLoading] = useState(true);

  const loadReviews = useCallback(async () => {
    try {
      const { data } = await api.get(`/pitches/${pitchId}/reviews`);
      setReviews(data.data ?? []);
      setAggregate(data.aggregate ?? { average: 0, count: 0 });
    } catch {
      setReviews([]);
      setAggregate({ average: 0, count: 0 });
    }
  }, [pitchId]);

  const loadEligibility = useCallback(async () => {
    // Only players have this endpoint (owner/admin would 403). Guard before call.
    if (!user || user.role !== 'player') {
      setEligibility(null);
      return;
    }
    try {
      const { data } = await api.get<ReviewEligibility>(
        `/pitches/${pitchId}/review-eligibility`,
        { _silent: true },
      );
      setEligibility(data);
    } catch {
      setEligibility(null);
    }
  }, [pitchId, user]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      setLoading(true);
      await Promise.all([loadReviews(), loadEligibility()]);
      if (!cancelled) setLoading(false);
    })();
    return () => { cancelled = true; };
  }, [loadReviews, loadEligibility]);

  const handleFlag = useCallback(async (id: number) => {
    try {
      await api.post(`/reviews/${id}/flag`);
    } catch {
      /* best-effort; flagged reviews stay visible by policy */
    }
  }, []);

  const handleDelete = useCallback(async (id: number) => {
    try {
      await api.delete(`/reviews/${id}`);
      setReviews((prev) => prev.filter((r) => r.id !== id));
      await loadReviews();
    } catch {
      /* swallow — non-admins never see the button */
    }
  }, [loadReviews]);

  const onSaved = useCallback(async () => {
    await Promise.all([loadReviews(), loadEligibility()]);
  }, [loadReviews, loadEligibility]);

  // Summary: hide the numeric score until there are enough reviews.
  const hasScore = aggregate.count >= MIN_REVIEWS_FOR_SCORE;

  return (
    <section className="flex flex-col gap-5 pt-4" aria-label="التقييمات">
      <div className="flex items-center justify-between">
        <h2 className="text-[15px] font-bold text-[#f0efe8]">التقييمات</h2>
        {hasScore ? (
          <div className="flex items-center gap-2">
            <Star size={15} className="text-amber-400" fill="currentColor" aria-hidden />
            <span className="text-[15px] font-bold text-[#f0efe8]">{aggregate.average.toFixed(1)}</span>
            <span className="text-[11px] text-white/30">({aggregate.count} تقييم)</span>
          </div>
        ) : (
          <span className="text-[12px] font-bold text-white/30">جديد / لا تقييمات بعد</span>
        )}
      </div>

      {/* Interaction zone — eligibility decides Hidden / Write / Edit. */}
      {eligibility?.existing_review ? (
        <ReviewForm
          pitchId={pitchId}
          existing={eligibility.existing_review}
          qualifyingBookingId={eligibility.qualifying_booking_id}
          onSaved={onSaved}
        />
      ) : eligibility?.eligible ? (
        <ReviewForm
          pitchId={pitchId}
          existing={null}
          qualifyingBookingId={eligibility.qualifying_booking_id}
          onSaved={onSaved}
        />
      ) : null}

      {/* Listing */}
      {loading ? (
        <div className="flex justify-center py-8">
          <Loader2 size={18} className="animate-spin text-white/30" />
        </div>
      ) : reviews.length > 0 ? (
        <div className="flex flex-col gap-3">
          {reviews.map((r) => (
            <ReviewRow key={r.id} review={r} onFlag={handleFlag} onDelete={handleDelete} />
          ))}
        </div>
      ) : (
        <p className="text-[12px] text-white/25 py-4 text-center">كن أول من يقيّم هذا الملعب</p>
      )}
    </section>
  );
}
