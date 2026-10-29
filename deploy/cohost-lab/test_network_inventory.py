import unittest
from stage_host import validate_networks

class NetworkInventoryTests(unittest.TestCase):
    def test_host_none_and_bridge_shapes(self):
        validate_networks([{},dict(IPAM=None),dict(IPAM={}),dict(IPAM=dict(Config=None)),
            dict(IPAM=dict(Config=[])),dict(IPAM=dict(Config=[dict(Subnet='172.17.0.0/16'),dict(Subnet='fd00::/64')]))])
    def test_existing_ipv4_overlap_always_refused_even_after_null_entry(self):
        for subnet in ('172.29.248.0/24','172.29.0.0/16','172.29.248.128/25'):
            with self.subTest(subnet=subnet),self.assertRaisesRegex(ValueError,'collision'):
                validate_networks([dict(IPAM=dict(Config=None)),dict(IPAM=dict(Config=[dict(Subnet=subnet)]))])
    def test_malformed_metadata_is_not_silently_ignored(self):
        for row in (dict(IPAM='bad'),dict(IPAM=dict(Config='bad')),dict(IPAM=dict(Config=[None])),dict(IPAM=dict(Config=[dict(Subnet='invalid')]))):
            with self.subTest(row=row),self.assertRaises(ValueError):validate_networks([row])

if __name__=='__main__':unittest.main()
