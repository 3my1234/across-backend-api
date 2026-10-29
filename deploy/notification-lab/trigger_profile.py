"""Generate fixed synthetic SQL from the actual controller constant, never operator SQL."""
import hashlib
import json
from pathlib import Path
import re
import sys

def generate(source, room, actor):
    for value in (room, actor):
        assert re.fullmatch(r'[a-f0-9-]{36}', value), 'Synthetic UUID required'
    matches = re.findall(r'const communityNotificationBatchSQL = `([^`]+)`', source)
    assert len(matches) == 1
    query = matches[0]
    values = {1: "'"+room+"'::uuid", 2: "'"+actor+"'::uuid",
              3: 'gen_random_uuid()', 4: "'New chat message'",
              5: "'"+json.dumps({'view':'messages','chat_room_id':room,'sender_user_id':actor})+"'::jsonb",
              6: 'NULL::uuid', 7: '64'}
    sql = re.sub(r'\$(\d+)\b', lambda m: values[int(m[1])], query)
    return query, sql

if __name__ == '__main__':
    destination = Path(sys.argv[1])
    room, actor = (destination/'actors.txt').read_text().strip().split('|')
    original, sql = generate(Path('internal/controllers/community_controller.go').read_text(), room, actor)
    (destination/'query.sql').write_text(sql+';\n')
    (destination/'pgbench.sql').write_text('BEGIN;\n'+sql+';\nROLLBACK;\n')
    (destination/'plan.sql').write_text('BEGIN;\nEXPLAIN(ANALYZE,BUFFERS,WAL,FORMAT JSON) '+sql+';\nROLLBACK;\n')
    (destination/'source-query.sha256').write_text(hashlib.sha256(original.encode()).hexdigest()+'\n')
