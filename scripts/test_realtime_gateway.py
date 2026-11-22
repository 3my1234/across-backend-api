"""Static safeguards for the reviewed gateway candidate; not an HAProxy test."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


def section(text, name):
    match = re.search(rf'(?ms)^backend {re.escape(name)}\s*\n(.*?)(?=^(?:backend|frontend|listen|defaults|global|resolvers)\s|\Z)', text)
    if not match:
        raise AssertionError(f'missing backend {name}')
    return match.group(1)


class GatewayCandidate(unittest.TestCase):
    def test_websockets_have_separate_capacity_and_tunnel_timeout(self):
        config = (ROOT / 'deploy/scaling/haproxy.cfg').read_text(encoding='utf-8')
        self.assertRegex(config, r'(?m)^\s*acl realtime_path path -i /api/v1/realtime\s*$')
        self.assertRegex(config, r'(?m)^\s*use_backend realtime_sockets if realtime_path\s*$')
        realtime, ordinary = section(config, 'realtime_sockets'), section(config, 'replicas')
        self.assertRegex(realtime, r'(?m)^\s*timeout tunnel 90s\s*$')
        self.assertRegex(realtime, r'(?m)^\s*server-template\s+realtime\s+4\s+atlxpres-pilot-api:8080\b.*\bmaxconn 256\s*$')
        self.assertRegex(ordinary, r'(?m)^\s*server-template\s+api\s+4\s+atlxpres-pilot-api:8080\b.*\bmaxconn 64\s*$')


if __name__ == '__main__':
    unittest.main()
