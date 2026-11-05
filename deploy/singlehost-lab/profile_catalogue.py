"""Catalogue-only SQL/index comparison in disposable CI; original SQL unchanged."""
import json
import re
import statistics


def exercise(root, base, output):
    from runtime import sql
    if not str(base).startswith('/root/atlxpres-singlehost/'):
        raise ValueError('disposable fixture required')
    sql(base, "INSERT INTO product_delivery_areas(product_id,country_code,delivered_price,currency_code) SELECT p.id,'NG',1000,'NGN' FROM products p WHERE p.provider_id IN (SELECT provider FROM capacity_lab_actors) AND NOT EXISTS(SELECT 1 FROM product_delivery_areas a WHERE a.product_id=p.id AND a.country_code='NG'); ANALYZE;")
    population = json.loads(sql(base, "SELECT json_build_object('products',(SELECT count(*) FROM products),'delivery_areas',(SELECT count(*) FROM product_delivery_areas))"))
    if population != {'products': 5000, 'delivery_areas': 5000}:
        raise ValueError('complete matched catalogue population required')
    source = (root / 'internal/controllers/catalog_controller.go').read_text()
    body = source[source.index('func (cc *CatalogController) ListProducts('):]
    query = re.search(r'\.db.Query\(c.Context\(\), `(.+?)`', body, re.S)[1].strip()
    cases = {'all': ['false','0','0','true',"'NG'","''","''","'all'"], 'local': ['false','0','0','true',"'NG'","''","''","'local'"], 'international': ['false','0','0','true',"'NG'","''","''","'international'"], 'nearby': ['true','9.08','7.45','true',"'NG'","''","''","'all'"]}
    report = {'population': population, 'original_sql_unchanged': True, 'capacity_qualified': False, 'cases': {}}
    sql(base, 'CREATE TABLE capacity_lab_catalogue_baselines(name text PRIMARY KEY,rows jsonb);')
    for phase in ('original','indexed'):
        if phase == 'indexed':
            sql(base, "CREATE INDEX capacity_profile_product_visible_slot ON products(provider_id,plan_slot DESC) WHERE moderation_status='approved' AND published_at IS NOT NULL; CREATE INDEX capacity_profile_service_visible_slot ON provider_listings(provider_id,plan_slot DESC) WHERE status='approved' AND published_at IS NOT NULL; ANALYZE;")
        for name, values in cases.items():
            literal = re.sub(r'\$(\d+)', lambda m: values[int(m[1])-1], query)
            wrapped = "SELECT COALESCE(jsonb_agg(to_jsonb(q)),'[]'::jsonb) FROM (" + literal + ') q'
            if phase == 'original':
                sql(base, "INSERT INTO capacity_lab_catalogue_baselines SELECT '"+name+"',("+wrapped+');')
            equal = sql(base, "SELECT rows=("+wrapped+") FROM capacity_lab_catalogue_baselines WHERE name='"+name+"';").strip() == 't'
            for mode in ('force_custom_plan','force_generic_plan'):
                samples=[]
                for iteration in range(4):
                    raw=sql(base,'BEGIN; SET LOCAL plan_cache_mode='+mode+'; PREPARE catalogue AS '+query+'; EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) EXECUTE catalogue('+','.join(values)+'); ROLLBACK;')
                    plan=json.JSONDecoder().raw_decode(raw[raw.index('['):])[0][0]
                    samples.append(plan['Execution Time'])
                key=phase+'-'+name+'-'+mode
                report['cases'][key]={'median_ms':statistics.median(samples),'execution_ms':samples,'ordered_full_rows_equal':equal}
                (output/(key+'-plan.json')).write_text(json.dumps(plan,indent=2))
                print('CATALOGUE_PROFILE '+key+' '+json.dumps(report['cases'][key]),flush=True)
                (output/'catalogue-profile.json').write_text(json.dumps(report,indent=2))
    sql(base,'DROP INDEX capacity_profile_product_visible_slot; DROP INDEX capacity_profile_service_visible_slot; DROP TABLE capacity_lab_catalogue_baselines;')
