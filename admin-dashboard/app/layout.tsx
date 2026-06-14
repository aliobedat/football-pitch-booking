import type { Metadata } from 'next';
import { Cairo } from 'next/font/google';
import { AuthProvider } from '@/context/AuthContext';
import './globals.css';

const cairo = Cairo({ subsets: ['arabic', 'latin'], display: 'swap' });

export const metadata: Metadata = {
  title: { default: 'لوحة التحكم | ملاعب', template: '%s | لوحة التحكم' },
  description: 'لوحة تحكم أصحاب الملاعب والمشرفين.',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ar" dir="rtl">
      <body className={`bg-[#121413] text-[#f0efe8] antialiased ${cairo.className}`}>
        <AuthProvider>{children}</AuthProvider>
      </body>
    </html>
  );
}
