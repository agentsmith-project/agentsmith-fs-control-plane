DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'export_sessions_terminal_zero_counts_check'
            AND conrelid = 'export_sessions'::regclass
    ) THEN
        ALTER TABLE export_sessions
            ADD CONSTRAINT export_sessions_terminal_zero_counts_check CHECK (
                status NOT IN ('revoked', 'expired', 'failed')
                OR (
                    active_request_count = 0
                    AND active_write_count = 0
                )
            );
    END IF;
END $$;
