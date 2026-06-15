export default function OverviewPage() {
  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-[20px] font-bold tracking-tight">نظرة عامة</h1>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
        {[
          { label: 'حجوزات اليوم', value: '—' },
          { label: 'ملاعب نشطة', value: '—' },
          { label: 'إشغال الأسبوع', value: '—' },
        ].map((c) => (
          <div
            key={c.label}
            className="rounded-2xl bg-[#141715] border border-white/[0.08] p-5 flex flex-col gap-2"
          >
            <span className="text-[12px] text-white/40">{c.label}</span>
            <span className="text-[24px] font-bold">{c.value}</span>
          </div>
        ))}
      </div>
      <p className="text-[13px] text-white/35">
        هيكل لوحة التحكم — البيانات الحقيقية تُضاف بعد ربط الواجهة الخلفية (PR 2).
      </p>
    </div>
  );
}
