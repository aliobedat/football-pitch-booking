'use client';

import React, { createContext, useContext, ReactNode } from 'react';
import { useUserLocation, type UserLocationState } from '@/hooks/useUserLocation';

const LocationContext = createContext<UserLocationState | undefined>(undefined);

/**
 * Provides geolocation state ONCE at the listing-page level.
 * PitchCard components consume distanceKm as a prop — they must NOT call
 * useUserLocation themselves (which would fire N separate geolocation requests).
 */
export function LocationProvider({ children }: { children: ReactNode }) {
  const location = useUserLocation();
  return (
    <LocationContext.Provider value={location}>
      {children}
    </LocationContext.Provider>
  );
}

export function useLocation(): UserLocationState {
  const ctx = useContext(LocationContext);
  if (!ctx) throw new Error('useLocation must be used within a LocationProvider');
  return ctx;
}
