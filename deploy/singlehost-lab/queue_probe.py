"""Isolated pre-load retry fixture; never fake acknowledgements for workload jobs."""
import re,uuid

def exercise(sql,run):
    if sql('SELECT marker FROM capacity_lab_identity').strip()!='disposable-compose-only':raise ValueError('synthetic database marker required')
    if int(sql('SELECT count(*) FROM notification_push_deliveries'))!=0:raise ValueError('queue preflight requires an empty pre-workload native queue')
    eligible=sql("SELECT count(*) FROM notifications n JOIN user_push_tokens t ON t.user_id=n.user_id AND t.disabled_at IS NULL WHERE n.created_at>=now()-interval '48 hours' AND NOT n.is_read")
    if int(eligible)!=0:raise ValueError('queue preflight must not backfill unrelated eligible notifications')
    tag='singlehost-queue-probe:'+uuid.uuid4().hex
    ident=sql("INSERT INTO notifications(user_id,type,title,body,data,event_key) SELECT buyer,'ticket_reply','Isolated queue recovery probe','Synthetic preflight only','{}'::jsonb,'"+tag+"' FROM capacity_lab_actors ORDER BY buyer LIMIT 1 RETURNING id::text").strip().splitlines()[0]
    if not re.fullmatch('[a-f0-9-]{36}',ident):raise ValueError('probe notification UUID missing')
    try:
        if int(sql('SELECT count(*) FROM notification_push_deliveries'))!=1:raise ValueError('probe did not enqueue exactly one native job')
        run()
    finally:
        # Exact fixture only; cannot delete, reclassify or acknowledge workload rows.
        removed=sql("WITH web AS(DELETE FROM web_push_deliveries WHERE source_key='notification:"+ident+"' RETURNING id), note AS(DELETE FROM notifications WHERE id='"+ident+"'::uuid AND event_key='"+tag+"' RETURNING id) SELECT count(*) FROM note")
        if int(removed)!=1:raise ValueError('probe cleanup identity mismatch')
        if int(sql('SELECT count(*) FROM notification_push_deliveries'))!=0:raise ValueError('probe queue cleanup incomplete')
