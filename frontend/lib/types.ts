// Thin re-export: the canonical API types/DTOs now live in @malaab/shared so the
// wire contract is shared with the admin dashboard. Existing `@/lib/types`
// imports across the player app keep resolving unchanged.
export type {
  Pitch,
  Review,
  RatingAggregate,
  ReviewEligibility,
} from '@malaab/shared/types';
