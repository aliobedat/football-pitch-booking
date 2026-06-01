'use client';

// PitchMap — used on the pitch DETAILS page only (lazy-loaded via next/dynamic).
// Never import this inside PitchCard; Maps JS is heavy and client-only.
//
// Required env vars:
//   NEXT_PUBLIC_GOOGLE_MAPS_API_KEY  — your Maps JavaScript API key
//   NEXT_PUBLIC_GOOGLE_MAPS_MAP_ID   — required for AdvancedMarker
//
// Without a valid API key this component renders a silent placeholder so
// the rest of the page still works — no crashes, no console errors.

import { APIProvider, Map, AdvancedMarker } from '@vis.gl/react-google-maps';
import { MapPin } from 'lucide-react';

// Read at module level so Next.js can inline them at build time.
const API_KEY = process.env.NEXT_PUBLIC_GOOGLE_MAPS_API_KEY ?? '';
const MAP_ID  = process.env.NEXT_PUBLIC_GOOGLE_MAPS_MAP_ID  ?? '';

// Amman city-centre fallback — used to CENTRE THE MAP ONLY when the pitch
// coordinates are 0,0 (unset). This is NOT a user location.
const AMMAN_CENTER = { lat: 31.9539, lng: 35.9106 };

interface Props {
  lat: number;
  lng: number;
  zoom?: number;
}

export default function PitchMap({ lat, lng, zoom = 15 }: Props) {
  if (!API_KEY) {
    return (
      <div className="w-full h-full rounded-xl bg-[#141715] border border-white/[0.07] flex flex-col items-center justify-center gap-2">
        <MapPin size={22} className="text-white/15" aria-hidden />
        <p className="text-[11px] text-white/25">خريطة الموقع غير متاحة</p>
        <p className="text-[9px] text-white/15">أضف NEXT_PUBLIC_GOOGLE_MAPS_API_KEY إلى .env.local</p>
      </div>
    );
  }

  // Use pitch coordinates when set; fall back to Amman centre.
  const center = (lat !== 0 || lng !== 0) ? { lat, lng } : AMMAN_CENTER;

  return (
    <APIProvider apiKey={API_KEY}>
      <Map
        defaultCenter={center}
        defaultZoom={zoom}
        // mapId is required by AdvancedMarker. Omit it (undefined) when not
        // configured to render the plain map without a marker.
        mapId={MAP_ID || undefined}
        style={{ width: '100%', height: '100%' }}
        gestureHandling="cooperative"
        disableDefaultUI
      >
        {/* AdvancedMarker requires mapId on the Map — only rendered when set */}
        {MAP_ID && lat !== 0 && lng !== 0 && (
          <AdvancedMarker position={{ lat, lng }} />
        )}
      </Map>
    </APIProvider>
  );
}
