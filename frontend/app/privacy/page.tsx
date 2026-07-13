import Link from 'next/link';

export const metadata = {
  title: 'سياسة الخصوصية — مرمى',
  description: 'سياسة الخصوصية لمنصة مرمى لحجز الملاعب الرياضية في الأردن.',
};

/**
 * صفحة سياسة الخصوصية — مكوّن خادم ثابت (Server Component).
 * الخلفية الداكنة واتجاه RTL يُورَثان من الـ root layout، فلا نعيد ضبطهما هنا.
 */

// مكوّن مساعد: نص فرعي إنجليزي مكتوم تحت المحتوى العربي، يفصله خط باهت.
function EnglishNote({ children }: { children: React.ReactNode }) {
  return (
    <p
      dir="ltr"
      className="mt-3 pt-3 border-t border-white/[0.06] text-[12px] leading-relaxed text-white/35 text-left"
    >
      {children}
    </p>
  );
}

// مكوّن قسم: رقم عربي-هندي أخضر صغير فوق العنوان.
function Section({
  num,
  title,
  children,
}: {
  num: string;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-12">
      <span className="block text-[13px] font-bold tracking-widest text-emerald-500 mb-1">
        {num}
      </span>
      <h2 className="text-[18px] font-bold text-[#f0efe8] mb-3">{title}</h2>
      {children}
    </section>
  );
}

export default function PrivacyPage() {
  return (
    <div className="min-h-screen">
      {/* رأس بسيط مع رابط العودة */}
      <header className="border-b border-white/[0.05]">
        <div className="max-w-3xl mx-auto px-6 py-5">
          <Link
            href="/pitches"
            className="text-[12px] text-white/35 hover:text-white/60 transition-colors duration-150"
          >
            ← العودة إلى مرمى
          </Link>
        </div>
      </header>

      <main className="max-w-3xl mx-auto px-6 py-12">
        <h1 className="text-[28px] font-bold text-[#f0efe8] mb-2">سياسة الخصوصية</h1>
        <p className="text-[13px] text-white/35 mb-12">آخر تحديث: حزيران 2026</p>

        {/* §1 */}
        <Section num="٠١" title="مقدمة">
          <p className="text-[15px] leading-loose text-white/75">
            مرمى منصة إلكترونية تتيح للاعبين حجز ملاعب رياضية في الأردن، وتربطهم
            بأصحاب الملاعب. نحن نُقدّر ثقتك، ونلتزم بحماية بياناتك الشخصية والتعامل
            معها بشفافية تامة.
          </p>
          <EnglishNote>
            Marma is an online platform connecting players with sports pitch
            owners in Jordan. We value your trust and are committed to handling
            your personal data with full transparency.
          </EnglishNote>
        </Section>

        {/* §2 */}
        <Section num="٠٢" title="البيانات التي نجمعها">
          <ul className="space-y-3 text-[15px] leading-loose text-white/75 list-disc pr-5">
            <li>الاسم الكامل — لتعريفك إلى صاحب الملعب.</li>
            <li>
              رقم الجوال (أردني) — ليتمكّن صاحب الملعب من التواصل معك بعد تأكيد
              الحجز. لا تُرسَل رسائل تسويقية دون موافقتك.
            </li>
            <li>عنوان البريد الإلكتروني — يُستخدم فقط للتحقق من الحساب والمصادقة.</li>
            <li>سجل الحجوزات — الملاعب والأوقات التي حجزتها سابقاً.</li>
            <li>
              بيانات الجهاز والاتصال — عنوان IP، نوع المتصفح، وملفات ارتباط الجلسة
              الضرورية للخدمة. لا توجد ملفات ارتباط للتتبّع أو الإعلانات.
            </li>
          </ul>
          <EnglishNote>
            Five items collected at booking or account creation: Full name (to
            identify you to the pitch owner); Phone number, Jordanian (so the
            pitch owner can contact you after booking confirmation — no marketing
            messages sent without consent); Email address (used only for account
            verification and authentication); Booking history (pitches and time
            slots previously reserved); Device &amp; connection data (IP address,
            browser type, session cookies necessary for the service — no tracking
            or advertising cookies).
          </EnglishNote>
        </Section>

        {/* §3 */}
        <Section num="٠٣" title="كيف نستخدم بياناتك">
          <ul className="space-y-3 text-[15px] leading-loose text-white/75 list-disc pr-5">
            <li>تأكيد حجزك وإيصال بياناتك إلى صاحب الملعب المختار.</li>
            <li>التحقق من هويتك عبر رمز OTP يُرسَل على بريدك الإلكتروني.</li>
            <li>تشغيل جلستك وإبقائك مسجلاً داخل المنصة.</li>
            <li>تحسين أداء المنصة وحل أي مشكلات تقنية.</li>
          </ul>
          <EnglishNote>
            Four purposes only: Confirm your booking and share your contact
            details with the selected pitch owner; Verify your identity via an OTP
            sent to your email; Maintain your session while you are logged in;
            Improve platform performance and resolve technical issues.
          </EnglishNote>
        </Section>

        {/* §4 */}
        <Section num="٠٤" title="مشاركة البيانات مع أطراف أخرى">
          <ul className="space-y-3 text-[15px] leading-loose text-white/75 list-disc pr-5">
            <li>صاحب الملعب — الاسم ورقم الجوال عند تأكيد الحجز فقط.</li>
            <li>
              مزودو الخدمات التقنية — إرسال البريد الإلكتروني واستضافة قاعدة
              البيانات، ضمن اتفاقيات سرية صارمة.
            </li>
            <li>الجهات القانونية — عند طلبها بموجب القانون الأردني.</li>
          </ul>
          <EnglishNote>
            Three sharing cases: Pitch owner (name + phone at booking confirmation
            only); Technical service providers (email delivery, database hosting,
            under strict confidentiality agreements); Legal authorities (when
            required by Jordanian law).
          </EnglishNote>

          {/* ملاحظة مميّزة — صندوق أخضر */}
          <div className="mt-6 rounded-xl border border-emerald-500/25 bg-emerald-500/[0.06] px-5 py-4">
            <p className="text-[14px] leading-loose text-white/85">
              رقم جوالك يُحفظ في سجل الحجز ولا يتغير لاحقاً حتى لو عدّلت ملفك
              الشخصي — لضمان قدرة صاحب الملعب على الوصول إليك دائماً بالرقم الذي
              أدخلته وقت الحجز.
            </p>
            <p
              dir="ltr"
              className="mt-3 pt-3 border-t border-emerald-500/15 text-[12px] leading-relaxed text-emerald-200/55 text-left"
            >
              Your phone number is stored immutably on your booking record — it
              does not change if you later update your profile, ensuring the pitch
              owner always has the number you entered at booking time.
            </p>
          </div>
        </Section>

        {/* §5 */}
        <Section num="٠٥" title="ملفات الارتباط">
          <p className="text-[15px] leading-loose text-white/75">
            نستخدم نوعاً واحداً من ملفات الارتباط: ملف الجلسة الآمن (httpOnly)،
            وهو ضروري لتشغيل حسابك ولا يمكن الوصول إليه من طرف أي سكريبت خارجي. لا
            نستخدم ملفات ارتباط تتبعية أو تحليلية أو إعلانية.
          </p>
          <EnglishNote>
            We use a single cookie type: a secure httpOnly session cookie required
            to keep you logged in. It is inaccessible to any third-party script.
            No tracking, analytics, or advertising cookies are used.
          </EnglishNote>
        </Section>

        {/* §6 */}
        <Section num="٠٦" title="حقوقك">
          <ul className="space-y-3 text-[15px] leading-loose text-white/75 list-disc pr-5">
            <li>الاطلاع على البيانات التي نحتفظ بها عنك.</li>
            <li>تصحيح أي معلومة غير دقيقة.</li>
            <li>
              طلب حذف حسابك وبياناتك (مع مراعاة أن سجلات الحجوزات المكتملة قد
              تُحتفظ بها لأغراض تشغيلية وقانونية).
            </li>
          </ul>
          <EnglishNote>
            Three rights: Access the data we hold about you; Request correction of
            inaccurate information; Request deletion of your account and data
            (completed booking records may be retained for operational and legal
            purposes).
          </EnglishNote>
        </Section>

        {/* §7 */}
        <Section num="٠٧" title="الكيان القانوني والاتصال">
          <p className="text-[15px] leading-loose text-white/75">
            منصة مرمى تُدار حالياً كمشروع فردي في مرحلة الإطلاق المبكر. في حال
            وجود أي استفسار بشأن بياناتك، تواصل معنا عبر:
          </p>
          <div className="mt-4 space-y-1.5 text-[14px] text-white/75">
            <p>
              البريد الإلكتروني · Email:{' '}
              <span dir="ltr" className="text-emerald-400">
                privacy@marmajo.com
              </span>
            </p>
            <p>الموقع · Location: عمّان، المملكة الأردنية الهاشمية · Amman, Jordan</p>
          </div>
          <EnglishNote>
            Marma is currently operated as a sole-proprietor early-stage venture.
            For any data-related request, contact us at the email and location
            above.
          </EnglishNote>
        </Section>

        {/* §8 */}
        <Section num="٠٨" title="التعديلات على هذه السياسة">
          <p className="text-[15px] leading-loose text-white/75">
            قد نُحدّث هذه السياسة عند إضافة مزايا جديدة أو تغيير طريقة معالجة
            البيانات. ستُنشر التغييرات على هذه الصفحة مع تحديث تاريخ "آخر تحديث".
            الاستمرار في استخدام المنصة بعد نشر التغييرات يعني قبولك للسياسة
            المُعدَّلة.
          </p>
          <EnglishNote>
            We may update this policy when new features are added or our data
            practices change. Changes will be published on this page with an
            updated &quot;last updated&quot; date. Continued use of the platform
            after changes are posted constitutes acceptance of the revised policy.
          </EnglishNote>
        </Section>
      </main>
    </div>
  );
}
