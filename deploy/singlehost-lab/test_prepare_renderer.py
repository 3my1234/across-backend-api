import unittest
from pathlib import Path
from prepare import compatible_renderer

class RendererProofTests(unittest.TestCase):
    def test_current_renderer_proof_is_reusable_without_reapplying_changes(self):
        current=Path(__file__).with_name('render.cjs').read_text()
        self.assertTrue(compatible_renderer(current,current))

    def test_legacy_proof_allows_only_recorded_cpu_and_api_mount_change(self):
        current=Path(__file__).with_name('render.cjs').read_text()
        legacy=current.replace('doc.services.postgres=db;\n  doc.services.postgres.cpus=2;','doc.services.postgres=db;').replace("s.environment.LAB_DB_CPUS='2'","s.environment.LAB_DB_CPUS='1'")
        legacy=legacy.replace("\n  if (input.api_override===true) for (const name of ['api1','api2']) (doc.services[name].volumes??=[]).push(base+'/api.bin:/app/api:ro');",'')
        self.assertTrue(compatible_renderer(legacy,current))
        self.assertFalse(compatible_renderer(legacy,current+'\n// unexpected drift'))
        self.assertFalse(compatible_renderer(current,current.replace('cpus=2','cpus=3')))
