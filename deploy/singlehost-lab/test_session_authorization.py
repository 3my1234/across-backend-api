import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from rehearse import session_authorization


class SessionAuthorizationTests(unittest.TestCase):
    def test_both_profiles_load_only_existing_files_and_keep_original_policy(self):
        policy = Path(__file__).resolve().parents[1] / 'session-lab/policy.mjs'
        program = """
import fs from 'node:fs';
const {plan,requireApproval}=await import(process.argv[1]);
const [profile,path]=process.argv.slice(2);
const approval=path?JSON.parse(fs.readFileSync(path,'utf8')):null;
const configuration=plan(profile,20);
requireApproval(configuration,approval);
if(configuration.p95_ms!==500||configuration.p99_ms!==1500)throw Error('changed thresholds');
if(profile==='stage'){
  let rejected=false;
  try{requireApproval(configuration,{...approval,authorized:false});}catch{rejected=true;}
  if(!rejected)throw Error('unauthorized stage allowed');
}
"""
        for launch in (False, True):
            with self.subTest(launch=launch), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                profile, advertised = session_authorization(directory, launch)
                # This reproduces k6's eager conditional open, before policy runs.
                local = directory / Path(advertised).name if advertised else None
                if local:
                    data = json.loads(local.read_text())
                    self.assertEqual(data['max_duration_seconds'], 180)
                    self.assertEqual(data['max_sessions'], 20)
                else:
                    self.assertFalse((directory / 'approval.json').exists())
                subprocess.run(['node', '--input-type=module', '-e', program,
                                policy.as_uri(), profile, str(local) if local else ''],
                               check=True, capture_output=True, text=True, timeout=15)
