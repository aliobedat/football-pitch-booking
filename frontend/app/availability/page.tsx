import { redirect } from 'next/navigation';

// The availability search was lifted onto the discovery page (/pitches) as a
// two-state view (PR 4.3). This route is retired: it permanently redirects to the
// discovery page so any existing links / bookmarks keep working.
export default function AvailabilityRedirect() {
  redirect('/pitches');
}
