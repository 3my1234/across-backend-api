DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_enum value
    JOIN pg_type type ON type.oid = value.enumtypid
    WHERE type.typname = 'tracking_stage'
      AND value.enumlabel = 'Arrived at China Hub'
  ) AND NOT EXISTS (
    SELECT 1
    FROM pg_enum value
    JOIN pg_type type ON type.oid = value.enumtypid
    WHERE type.typname = 'tracking_stage'
      AND value.enumlabel = 'Arrived at International Hub'
  ) THEN
    ALTER TYPE tracking_stage RENAME VALUE 'Arrived at China Hub' TO 'Arrived at International Hub';
  END IF;
END
$$;
