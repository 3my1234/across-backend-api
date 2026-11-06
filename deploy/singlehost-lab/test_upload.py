import base64
import contextlib
import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import upload


class ResumeUploadTests(unittest.TestCase):
    def test_empty_partial_corrupt_and_complete_archives(self):
        original = subprocess.Popen
        run = 'cohost-' + 'e'*16
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            local = root/'local'
            data = b'archive bytes'*100000
            local.write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            remote = root/(run+'-singlehost.tar.gz')
            def transfer(args, stdin):
                encoded = args[-1].split("b64decode('")[1].split("')")[0]
                code = base64.b64decode(encoded).decode().replace("Path('/root')", 'Path('+repr(str(root))+')').replace("Path('/root/atlxpres-singlehost')", 'Path('+repr(str(root/'installed'))+')')
                return original([sys.executable, '-c', code], stdin=stdin,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            for prefix in [b'', data[:500000], b'x'*500000, data]:
                with self.subTest(prefix_bytes=len(prefix)):
                    remote.write_bytes(prefix)
                    status = json.dumps(dict(offset=len(prefix), sha=hashlib.sha256(prefix).hexdigest()))
                    with patch.object(sys,'argv',['upload',str(local),'--run',run,'--sha256',digest]),patch('upload.subprocess.check_output',return_value=status),patch('upload.subprocess.Popen',side_effect=transfer),contextlib.redirect_stdout(io.StringIO()):
                        upload.main()
                    self.assertEqual(remote.read_bytes(),data)

    def test_changed_local_archive_never_opens_ssh(self):
        with tempfile.TemporaryDirectory() as folder:
            local=Path(folder)/'local';local.write_bytes(b'changed')
            with patch.object(sys,'argv',['upload',str(local),'--run','cohost-'+'e'*16,'--sha256','0'*64]),patch('upload.subprocess.check_output') as ssh:
                with self.assertRaisesRegex(ValueError,'mismatch'):upload.main()
                ssh.assert_not_called()
