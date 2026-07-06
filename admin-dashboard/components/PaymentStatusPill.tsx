// Payment-status pill — the single source of truth for the colours + wording used
// to show a booking's cash-settlement state. It backs BOTH the interactive
// cash-settlement toggle in الحجوزات (which wraps these classes in a <button> and
// adds hover/disabled) and the read-only Day View pill. Values mirror the backend
// payment_status: 'paid_cash' | 'unpaid' (anything else is treated as unpaid).

export const PAYMENT_PILL_BASE =
  'inline-flex items-center px-2.5 py-1 rounded-lg text-[10px] font-bold border';

export function isPaidCash(status: string): boolean {
  return status === 'paid_cash';
}

/** Colour classes for a payment status (no hover — interactive callers add it). */
export function paymentPillColor(status: string): string {
  return isPaidCash(status)
    ? 'bg-emerald-500/15 border-emerald-500/30 text-emerald-300'
    : 'bg-amber-500/[0.08] border-amber-500/25 text-amber-300/80';
}

/** Arabic label for a payment status. */
export function paymentPillLabel(status: string): string {
  return isPaidCash(status) ? 'مدفوع نقداً' : 'غير مدفوع';
}

/** Read-only pill (Day View, and anywhere a static payment status is shown). */
export default function PaymentStatusPill({
  status,
  className = '',
}: {
  status: string;
  className?: string;
}) {
  return (
    <span className={`${PAYMENT_PILL_BASE} ${paymentPillColor(status)} ${className}`.trim()}>
      {paymentPillLabel(status)}
    </span>
  );
}
