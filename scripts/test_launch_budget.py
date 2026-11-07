import copy
import json
import unittest
from pathlib import Path
from launch_budget import connection_budget, media_budget
ROOT=Path(__file__).resolve().parents[1]
class BudgetTests(unittest.TestCase):
 def test_pool_totals_include_replicas_other_clients_reserve_and_read_pool(self):
  cfg=json.loads((ROOT/'deploy/launch/connection-budget.example.json').read_text())
  self.assertEqual(connection_budget(cfg)['required_connections'],42)
  cfg['services'][0].update(separate_read_pool=True,read_max=8)
  self.assertEqual(connection_budget(cfg)['required_connections'],58)
  cfg['postgres_max_connections']=50
  self.assertFalse(connection_budget(cfg)['fits'])
 def test_unverified_inventory_never_qualifies_production(self):
  cfg=json.loads((ROOT/'deploy/launch/connection-budget.example.json').read_text())
  self.assertFalse(connection_budget(cfg)['production_budget_verified'])
 def test_caching_reduces_origin_but_not_viewer_delivery(self):
  cfg=json.loads((ROOT/'deploy/launch/media-cost.example.json').read_text())
  result=media_budget(cfg)['scenarios'];before,after=result[:2]
  self.assertAlmostEqual(before['delivered_gb'],after['delivered_gb'])
  self.assertAlmostEqual(after['origin_gb'],(before['origin_gb']-before['video_delivered_gb'])*.15+before['video_delivered_gb'])
  self.assertEqual(after['video_cache_hit_rate'],0)
  self.assertEqual(before['components_usd']['cdn_egress'],after['components_usd']['cdn_egress'])
  self.assertLess(after['monthly_media_usd'],before['monthly_media_usd'])
 def test_all_variants_count_in_storage_and_puts_not_every_view(self):
  cfg=json.loads((ROOT/'deploy/launch/media-cost.example.json').read_text())
  before=media_budget(cfg)['scenarios'][0]
  cfg['usage']['image_variants']=1
  cfg['usage']['image_stored_bytes']=cfg['usage']['image_bytes']
  after=media_budget(cfg)['scenarios'][0]
  self.assertGreater(before['stored_gb'],after['stored_gb'])
  self.assertGreater(before['components_usd']['puts'],after['components_usd']['puts'])
  self.assertEqual(before['delivered_gb'],after['delivered_gb'])
 def test_bad_inputs_rejected_instead_of_silent_underestimate(self):
  cfg=json.loads((ROOT/'deploy/launch/media-cost.example.json').read_text())
  for key,value in [('gold',-.1),('free',.4)]:
   invalid=copy.deepcopy(cfg);invalid['tier_shares'][key]=value
   with self.assertRaises(ValueError):media_budget(invalid)
  cfg['rates_usd']['get_1000']=float('nan')
  with self.assertRaises(ValueError):media_budget(cfg)
if __name__=='__main__':unittest.main()
