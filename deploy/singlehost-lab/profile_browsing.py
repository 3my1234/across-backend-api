"""Matched read-only query candidates on the disposable seeded stack only."""
import json,re,statistics

def exercise(root,base,output):
    from runtime import sql
    if not str(base).startswith('/root/atlxpres-singlehost/'):
        raise ValueError('disposable stack path required')
    cases=(('services','provider_marketplace_controller.go','listListings',"'','','public','','',NULL,NULL,21,true,0,0,'',''"),
           ('catalogue','catalog_controller.go','ListProducts',"false,0,0,true,'NG','','','all'"))
    report={'capacity_qualified':False,'application_modified':False,'comparisons':{}}
    for name,file,function,args in cases:
        source=(root/'internal/controllers'/file).read_text()
        source=source[source.index('func (',source.index('package controllers')):]
        match=re.search(r'func \([^\n]+\) '+function+r'\([^\n]+\{(.*?)(?=\nfunc |\Z)',source,re.S)
        if not match:raise ValueError('query function absent: '+function)
        query=re.search(r'\.db.Query\(c.Context\(\), `(.+?)`',match[1],re.S)[1].strip()
        candidate="WITH discovery_identity AS MATERIALIZED (SELECT provider_id,paid_active,listing_limit,badge_tier,verified FROM provider_public_identity) "+query.replace('FROM provider_public_identity','FROM discovery_identity').replace('JOIN provider_public_identity','JOIN discovery_identity')
        result={};samples={'original':[],'candidate':[]}
        # Same transaction timestamp, data, parameters and resource limits.
        for iteration in range(8):
            for label,statement in ((('original',query),('candidate',candidate)) if iteration%2==0 else (('candidate',candidate),('original',query))):
                prepared='BEGIN; PREPARE browsing AS '+statement+'; '
                raw=sql(base,prepared+'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE browsing('+args+'); ROLLBACK;')
                plan=json.JSONDecoder().raw_decode(raw[raw.index('['):])[0][0]
                samples[label].append(plan['Execution Time'])
                (output/(name+'-'+label+'-plan.json')).write_text(json.dumps(plan,indent=2))
        # Compare ordered full result rows, not just counts or selected IDs.
        def literal(statement):
            values=["''","''","'public'","''","''","NULL","NULL","21","true","0","0","''","''"] if name=='services' else ['false','0','0','true',"'NG'","''","''","'all'"]
            return re.sub(r'\$(\d+)',lambda m:values[int(m[1])-1],statement)
        equivalence=sql(base,"BEGIN; SELECT (SELECT COALESCE(jsonb_agg(to_jsonb(q)),'[]') FROM ("+literal(query)+") q)=(SELECT COALESCE(jsonb_agg(to_jsonb(q)),'[]') FROM ("+literal(candidate)+") q); ROLLBACK;")
        if 't' not in equivalence.splitlines():raise ValueError('ordered result mismatch: '+name)
        result['ordered_full_rows_equal']=True
        result['samples']={label:{'median_ms':statistics.median(times),'max_ms':max(times),'values_ms':times} for label,times in samples.items()}
        report['comparisons'][name]=result
    (output/'browsing-profile.json').write_text(json.dumps(report,indent=2))
    print('BROWSING_PROFILE '+json.dumps(report),flush=True)
