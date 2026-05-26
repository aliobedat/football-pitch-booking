-- HOW TO INSERT A BOOKING:
-- Pass timestamps directly; PostgreSQL constructs the range.

INSERT INTO bookings (pitch_id, player_id, booking_range, total_price)
VALUES (
    1,
    42,
    TSRANGE('2026-06-01 19:00', '2026-06-01 21:00', '[)'), -- [inclusive, exclusive)
    10.500  -- 2 hrs × 5.250 JOD/hr
);

-- ATTEMPTING A CONFLICTING BOOKING will throw:
-- ERROR: conflicting key value violates exclusion constraint "bookings_pitch_id_booking_range_excl"
-- No application-level lock or check required.


-- AVAILABILITY CHECK — find free slots for a pitch on a given day:
SELECT *
FROM   bookings
WHERE  pitch_id = 1
  AND  booking_range && TSRANGE('2026-06-01 00:00', '2026-06-02 00:00', '[)')
  AND  status <> 'cancelled'
ORDER BY lower(booking_range);