"""Extract the exact repository-owned Inbox SQL, including its static helper.
Plans run after the measured window; no synthetic query rewrite is substituted.
"""
from pathlib import Path
import re

source = Path('internal/controllers/community_controller.go').read_text()
query = source.split('rows, err := s.db.Query(c.Context(), `WITH inbox AS (', 1)[1].split('`, actor, provider,', 1)[0]
query = 'WITH inbox AS (' + query
helper = Path('internal/controllers/provider_threads.go').read_text().split('func businessThreadMembers', 1)[1].split('\n}', 1)[0]
parts = re.findall(r'`([^`]+)`', helper)
assert len(parts) == 2
query = re.sub(r'`\+businessThreadMembers\("([^"]+)"\)\+`', lambda m: parts[0] + m[1] + parts[1], query)
assert '`' not in query and 'businessThreadMembers' not in query
print("SELECT buyer::text AS buyer,provider::text AS provider FROM capacity_lab_actors ORDER BY slot LIMIT 1 \\gset")
print('PREPARE inbox_plan(uuid,text,timestamptz,uuid,int,text) AS ' + query + ';')
for mode in ('force_custom_plan', 'force_generic_plan'):
    print('SET plan_cache_mode=' + mode + ';')
    for _ in range(2):
        print("EXPLAIN(ANALYZE,BUFFERS,FORMAT JSON) EXECUTE inbox_plan(:'buyer',:'provider',NULL,NULL,26,'');")
