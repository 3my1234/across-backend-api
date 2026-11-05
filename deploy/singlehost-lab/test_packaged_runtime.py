import struct,tempfile,unittest
from pathlib import Path
from runner import SingleHostRunner
from verifier_build import validate_binary,INTERPRETER

class PackagedRuntimeTests(unittest.TestCase):
    def test_relative_coordinator_paths_are_absolute_for_docker_and_ssh(self):
        runner=SingleHostRunner(Path('.'),Path('relative-config'),{},Path('relative-evidence'))
        self.assertTrue(runner.root.is_absolute())
        self.assertTrue(runner.config.is_absolute())
        self.assertTrue(runner.output.is_absolute())
        self.assertTrue((runner.output.parent/'private-run'/'baseline-20'/'suite').is_absolute())

    def test_wrong_loader_is_rejected_before_upload(self):
        for loader,valid in ((INTERPRETER,True),(b'/lib64/ld-linux-x86-64.so.2\0',False)):
            with self.subTest(loader=loader),tempfile.TemporaryDirectory() as folder:
                data=bytearray(120+len(loader));data[:6]=b'\x7fELF\x02\x01'
                struct.pack_into('<H',data,18,62);struct.pack_into('<Q',data,32,64)
                struct.pack_into('<HH',data,54,56,1);struct.pack_into('<I',data,64,3)
                struct.pack_into('<Q',data,72,120);struct.pack_into('<Q',data,96,len(loader));data[120:]=loader
                path=Path(folder)/'verifier';path.write_bytes(data)
                if valid:validate_binary(path)
                else:
                    with self.assertRaisesRegex(ValueError,'loader'):validate_binary(path)
