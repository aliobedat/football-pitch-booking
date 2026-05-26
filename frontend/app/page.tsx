import { redirect } from 'next/navigation';

export default function Home() {
  // تحويل المستخدم تلقائياً لصفحة الملاعب
  redirect('/pitches');
}