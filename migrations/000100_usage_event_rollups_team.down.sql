BEGIN;

-- 1. Drop team indices
DROP INDEX IF EXISTS idx_usage_event_rollups_tenant_team_hour;
DROP INDEX IF EXISTS idx_usage_event_rollups_unique;

-- 2. Clear table to prevent collision when rolling back to non-team unique index
DELETE FROM usage_event_rollups;

-- 3. Rebuild rollups aggregated without team_id from raw events
INSERT INTO usage_event_rollups (
    id, tenant_id, bucket_hour, event_type, resource_type, resource_name, source,
    agent_id, channel, provider, model, status,
    input_tokens, output_tokens, total_tokens,
    cache_read_tokens, cache_create_tokens, thinking_tokens, cost_usd,
    duration_ms, call_count, error_count, created_at, updated_at
)
SELECT
    gen_random_uuid(),
    tenant_id,
    bucket_hour,
    event_type,
    resource_type,
    resource_name,
    source,
    agent_id,
    channel,
    provider,
    model,
    status,
    COALESCE(SUM(input_tokens), 0),
    COALESCE(SUM(output_tokens), 0),
    COALESCE(SUM(total_tokens), 0),
    COALESCE(SUM(cache_read_tokens), 0),
    COALESCE(SUM(cache_create_tokens), 0),
    COALESCE(SUM(thinking_tokens), 0),
    COALESCE(SUM(cost_usd), 0),
    CASE WHEN COALESCE(SUM(call_count), 0) > 0
        THEN COALESCE(SUM(duration_ms * call_count), 0) / SUM(call_count)
        ELSE 0 END,
    COALESCE(SUM(call_count), 0),
    COALESCE(SUM(error_count), 0),
    NOW(),
    NOW()
FROM usage_events
WHERE bucket_hour < (date_trunc('hour', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
GROUP BY tenant_id, bucket_hour, event_type, resource_type, resource_name, source, agent_id, channel, provider, model, status;

-- 4. Recreate legacy unique index
CREATE UNIQUE INDEX idx_usage_event_rollups_unique
    ON usage_event_rollups (
        tenant_id,
        bucket_hour,
        event_type,
        resource_type,
        resource_name,
        source,
        COALESCE(agent_id, '00000000-0000-0000-0000-000000000000'::uuid),
        channel,
        provider,
        model,
        status
    );

-- 5. Drop team_id column
ALTER TABLE usage_event_rollups DROP COLUMN IF EXISTS team_id;

COMMIT;
