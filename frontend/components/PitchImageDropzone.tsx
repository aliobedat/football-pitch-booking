'use client';

import { useCallback, useState } from 'react';
import axios from 'axios';
import { useDropzone, type FileRejection } from 'react-dropzone';
import { UploadCloud, ImageIcon, RefreshCw, Trash2, Loader2, AlertCircle } from 'lucide-react';

import api from '@/lib/api';

// ─────────────────────────────────────────────────────────────────────────────
// Pitch image dropzone (RTL, Arabic) — backend-signed Cloudinary direct upload.
//
// Flow: pick/drop → POST /pitches/upload-signature (our API, CSRF cookie echoed
// by the api interceptor) → upload the bytes DIRECTLY to Cloudinary (a plain
// axios call with NO credentials, so our session cookies never reach a third
// party) → hand the resulting secure_url + public_id back to the parent via
// onChange. File bytes never transit our Go backend.
//
// Persistence is the parent's job: in CREATE mode it folds the value into the
// POST /pitches payload; in EDIT mode it PATCHes /pitches/:id/image immediately.
// ─────────────────────────────────────────────────────────────────────────────

export interface PitchImageValue {
  image_url: string;
  image_public_id: string;
}

// Mirrors the backend SignedUpload payload (internal/cloudinary).
interface SignedUpload {
  timestamp: number;
  signature: string;
  api_key: string;
  cloud_name: string;
  folder: string;
  upload_preset: string;
}

const MAX_BYTES = 8 * 1024 * 1024; // ~8MB — must match the Cloudinary preset cap.
const ACCEPT = {
  'image/jpeg': ['.jpg', '.jpeg'],
  'image/png': ['.png'],
  'image/webp': ['.webp'],
  'image/heic': ['.heic'],
};

type Status = 'idle' | 'uploading' | 'error';

export default function PitchImageDropzone({
  value,
  onChange,
  disabled = false,
}: {
  value: PitchImageValue;
  onChange: (next: PitchImageValue) => void;
  disabled?: boolean;
}) {
  const [status, setStatus] = useState<Status>('idle');
  const [progress, setProgress] = useState(0);
  const [error, setError] = useState<string | null>(null);
  // Local object URL for an in-flight file, so the preview shows instantly
  // before Cloudinary returns the delivered URL.
  const [localPreview, setLocalPreview] = useState<string | null>(null);

  const hasImage = !!value.image_url;
  const previewSrc = localPreview ?? (hasImage ? value.image_url : null);

  const upload = useCallback(
    async (file: File) => {
      setStatus('uploading');
      setProgress(0);
      setError(null);
      const objectURL = URL.createObjectURL(file);
      setLocalPreview(objectURL);

      try {
        // 1) Ask our backend to sign the upload (CSRF header added by the api interceptor).
        const sigRes = await api.post('/pitches/upload-signature');
        const sig: SignedUpload = sigRes.data.data;

        // 2) Upload the bytes straight to Cloudinary. Plain axios → no credentials,
        //    so our httpOnly session cookies are NOT sent to Cloudinary.
        const form = new FormData();
        form.append('file', file);
        form.append('api_key', sig.api_key);
        form.append('timestamp', String(sig.timestamp));
        form.append('signature', sig.signature);
        form.append('folder', sig.folder);
        form.append('upload_preset', sig.upload_preset);

        const cloudURL = `https://api.cloudinary.com/v1_1/${sig.cloud_name}/image/upload`;
        const upRes = await axios.post(cloudURL, form, {
          onUploadProgress: (e) => {
            if (e.total) setProgress(Math.round((e.loaded / e.total) * 100));
          },
        });

        const { secure_url, public_id } = upRes.data as { secure_url: string; public_id: string };
        if (!secure_url || !public_id) {
          throw new Error('missing url/public_id');
        }

        // 3) Report the persisted-able result to the parent.
        onChange({ image_url: secure_url, image_public_id: public_id });
        setStatus('idle');
      } catch {
        setError('تعذّر رفع الصورة، يرجى المحاولة مجدداً');
        setStatus('error');
      } finally {
        URL.revokeObjectURL(objectURL);
        setLocalPreview(null);
      }
    },
    [onChange],
  );

  const onDrop = useCallback(
    (accepted: File[], rejections: FileRejection[]) => {
      if (rejections.length > 0) {
        const code = rejections[0].errors[0]?.code;
        setError(
          code === 'file-too-large'
            ? 'حجم الصورة كبير جداً (الحد الأقصى 8 ميجابايت)'
            : 'نوع الملف غير مدعوم (المسموح: JPG، PNG، WEBP، HEIC)',
        );
        setStatus('error');
        return;
      }
      if (accepted[0]) void upload(accepted[0]);
    },
    [upload],
  );

  const { getRootProps, getInputProps, isDragActive, open } = useDropzone({
    onDrop,
    accept: ACCEPT,
    maxSize: MAX_BYTES,
    multiple: false,
    disabled: disabled || status === 'uploading',
    noClick: hasImage, // when an image is shown, clicking uses the explicit buttons
  });

  const remove = useCallback(() => {
    setError(null);
    setStatus('idle');
    onChange({ image_url: '', image_public_id: '' });
  }, [onChange]);

  // ── Uploading ──────────────────────────────────────────────────────────────
  if (status === 'uploading') {
    return (
      <div className="rounded-xl border border-white/[0.13] bg-white/[0.04] p-4" dir="rtl">
        <div className="flex items-center gap-3">
          {previewSrc ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={previewSrc} alt="معاينة" className="w-14 h-14 rounded-lg object-cover border border-white/10" />
          ) : (
            <div className="w-14 h-14 rounded-lg bg-white/5 flex items-center justify-center">
              <ImageIcon size={18} className="text-white/30" aria-hidden />
            </div>
          )}
          <div className="flex-1">
            <div className="flex items-center gap-2 mb-2 text-[12px] text-white/60">
              <Loader2 size={13} className="animate-spin text-emerald-400" aria-hidden />
              جاري الرفع... {progress}%
            </div>
            <div className="h-1.5 rounded-full bg-white/10 overflow-hidden">
              <div
                className="h-full bg-emerald-500 transition-[width] duration-150"
                style={{ width: `${progress}%` }}
              />
            </div>
          </div>
        </div>
      </div>
    );
  }

  // ── Success (image present) ──────────────────────────────────────────────────
  if (hasImage) {
    return (
      <div className="rounded-xl border border-white/[0.13] bg-white/[0.04] p-3" dir="rtl">
        <div className="flex items-center gap-3">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={value.image_url}
            alt="صورة الملعب"
            className="w-16 h-16 rounded-lg object-cover border border-white/10"
          />
          <div className="flex-1 min-w-0">
            <p className="text-[12px] text-emerald-400 font-semibold mb-2">تم رفع الصورة</p>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={open}
                disabled={disabled}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[11px] font-semibold text-white/65 border border-white/[0.10] hover:border-white/[0.20] hover:text-white/85 transition-all disabled:opacity-50"
              >
                <RefreshCw size={12} aria-hidden /> استبدال
              </button>
              <button
                type="button"
                onClick={remove}
                disabled={disabled}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[11px] font-semibold text-red-400/80 border border-red-500/15 hover:border-red-500/35 hover:text-red-400 transition-all disabled:opacity-50"
              >
                <Trash2 size={12} aria-hidden /> حذف
              </button>
            </div>
          </div>
        </div>
        {/* Hidden input so "استبدال" (open) can pick a new file. */}
        <input {...getInputProps()} />
      </div>
    );
  }

  // ── Idle / drag-over / error ─────────────────────────────────────────────────
  return (
    <div dir="rtl">
      <div
        {...getRootProps()}
        className={[
          'flex flex-col items-center justify-center gap-2 rounded-xl border border-dashed px-4 py-7 text-center cursor-pointer transition-all',
          isDragActive
            ? 'border-emerald-500/60 bg-emerald-500/[0.06]'
            : 'border-white/[0.15] bg-white/[0.03] hover:border-white/[0.28]',
          disabled ? 'opacity-50 cursor-not-allowed' : '',
        ].join(' ')}
      >
        <input {...getInputProps()} />
        <div className="w-10 h-10 rounded-xl bg-emerald-500/10 border border-emerald-500/20 flex items-center justify-center">
          <UploadCloud size={18} className="text-emerald-400" aria-hidden />
        </div>
        <p className="text-[12px] text-white/65 font-semibold">
          {isDragActive ? 'أفلت الصورة هنا' : 'اسحب صورة الملعب هنا أو اضغط للاختيار'}
        </p>
        <p className="text-[10px] text-white/30">JPG، PNG، WEBP، HEIC — حتى 8 ميجابايت</p>
      </div>

      {error && (
        <p className="mt-2 flex items-center gap-1.5 text-[11px] text-red-400">
          <AlertCircle size={12} aria-hidden /> {error}
        </p>
      )}
    </div>
  );
}
