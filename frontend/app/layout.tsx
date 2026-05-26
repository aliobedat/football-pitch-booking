import type { Metadata } from 'next';
import { Cairo } from 'next/font/google';
import { AuthProvider } from '@/context/AuthContext';
import './globals.css';

// سحب خط القاهرة ليدعم العربي بلمسة فخمة
const cairo = Cairo({
  subsets: ['arabic', 'latin'],
  display: 'swap',
});

export const metadata: Metadata = {
  title: {
    default: 'ملاعب — احجز ملعبك',
    template: '%s | ملاعب',
  },
  description:
    'اكتشف واحجز أفضل ملاعب كرة القدم في الأردن. سرعة، سهولة، وموثوقية.',
  icons: { icon: '/favicon.ico' },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    // قلبنا اللغة عربي والاتجاه من اليمين لليسار
    <html lang="ar" dir="rtl">
      {/*
       * bg-[#121413] on the body ensures the dark canvas is set globally.
       * antialiased improves font rendering on the dark background.
       */}
      <body className={`bg-[#121413] text-[#f0efe8] antialiased ${cairo.className}`}>
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}