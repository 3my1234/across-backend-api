import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('restore_drill', Path(__file__).with_name('restore-drill.py'))
drill = importlib.util.module_from_spec(spec)
spec.loader.exec_module(drill)


class RestoreSafety(unittest.TestCase):
    def test_invalid_archive_rejected_before_docker(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'archive.dmp'
            archive.write_bytes(b'not an archive')
            with patch.object(drill, 'docker') as command:
                with self.assertRaises(ValueError):
                    drill.inspect_archive(archive)
                command.assert_not_called()

    def test_owned_isolated_target_required(self):
        good = {'Config': {'Labels': {'atl.restore-drill': 'nonce'}, 'User': 'postgres'},
                'HostConfig': {'NetworkMode': 'none', 'PortBindings': {}}}
        with patch.object(drill, 'docker', return_value=json.dumps([good])):
            drill.verify_target('generated', 'nonce')
            with self.assertRaises(RuntimeError):
                drill.verify_target('generated', 'wrong-owner')
        good['HostConfig']['NetworkMode'] = 'host'
        with patch.object(drill, 'docker', return_value=json.dumps([good])):
            with self.assertRaises(RuntimeError):
                drill.verify_target('generated', 'nonce')


if __name__ == '__main__':
    unittest.main()
