'use client';

import { useState, useCallback } from 'react';

export type LocationStatus = 'idle' | 'loading' | 'granted' | 'denied' | 'unavailable';
export type Coords = { lat: number; lng: number };

export interface UserLocationState {
  coords: Coords | null;   // ONLY non-null when status === 'granted'
  status: LocationStatus;
  request: () => void;
}

/**
 * One-shot geolocation hook.
 *
 * CRITICAL: coords is ALWAYS null unless the user explicitly grants permission
 * (status === 'granted').  It is NOT set to any fallback/Amman coordinate.
 * The Amman fallback is used ONLY to centre the map, never to compute distance.
 *
 * Does NOT auto-request on mount.  Call request() from a user-triggered action.
 */
export function useUserLocation(): UserLocationState {
  const [coords, setCoords] = useState<Coords | null>(null);
  const [status, setStatus] = useState<LocationStatus>('idle');

  const request = useCallback(() => {
    // Guard SSR
    if (typeof navigator === 'undefined' || !navigator.geolocation) {
      setStatus('unavailable');
      return;
    }
    setStatus('loading');
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setCoords({ lat: pos.coords.latitude, lng: pos.coords.longitude });
        setStatus('granted');
      },
      () => {
        // Permission denied or position unavailable
        setCoords(null);
        setStatus('denied');
      },
      { enableHighAccuracy: false, timeout: 8_000, maximumAge: 300_000 },
    );
  }, []);

  return { coords, status, request };
}
