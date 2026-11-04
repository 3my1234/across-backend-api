import hashlib,io,json,subprocess,tarfile,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
from image_identity import identities,resolve

class ImageIdentityTests(unittest.TestCase):
    def archive(self,path,nested=False,foreign=None):
        config=json.dumps(dict(os='linux',architecture='amd64')).encode()
        config_id='sha256:'+hashlib.sha256(config).hexdigest()
        manifest=json.dumps(dict(config=dict(digest=config_id))).encode()
        manifest_id='sha256:'+hashlib.sha256(manifest).hexdigest()
        entries=[dict(digest=manifest_id)]
        if foreign:entries.append(dict(digest='sha256:'+'f'*64,platform=dict(os='linux',architecture=foreign)))
        index=json.dumps(dict(manifests=entries)).encode()
        extra=[]
        if nested:
            digest=hashlib.sha256(index).hexdigest()
            extra=[('blobs/sha256/'+digest,index)]
            index=json.dumps(dict(manifests=[dict(digest='sha256:'+digest)])).encode()
        with tarfile.open(path,'w:gz') as stream:
            for name,data in [('index.json',index),('blobs/sha256/'+config_id[7:],config),('blobs/sha256/'+manifest_id[7:],manifest)]+extra:
                item=tarfile.TarInfo(name);item.size=len(data);stream.addfile(item,io.BytesIO(data))
        return config_id,manifest_id,hashlib.sha256(path.read_bytes()).hexdigest()
    def test_nested_oci_index_keeps_exact_config_and_manifest_binding(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'images.tar.gz';config,manifest,sha=self.archive(path,nested=True)
            self.assertEqual(identities(path,sha,{config})[config][:2],[config,manifest])
    def test_partial_multiarch_archive_skips_only_unrequested_platform(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'images.tar.gz';config,manifest,sha=self.archive(path,nested=True,foreign='arm64')
            self.assertEqual(identities(path,sha,{config})[config][:2],[config,manifest])
            config,manifest,sha=self.archive(path,nested=True,foreign='amd64')
            with self.assertRaises(ValueError):identities(path,sha,{config})
    def test_only_verified_same_content_descriptors_accepted(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'images.tar.gz';config,manifest,sha=self.archive(path)
            self.assertEqual(identities(path,sha,{config})[config][:2],[config,manifest])
            with self.assertRaises(ValueError):identities(path,'0'*64,{config})
            with self.assertRaises(ValueError):identities(path,sha,{'sha256:'+'f'*64})
    def test_both_docker_stores_supported_without_tags(self):
        config='sha256:'+'a'*64;manifest='sha256:'+'b'*64
        for chosen in (config,manifest):
            def run(args,**kwargs):
                candidate=args[-1]
                return subprocess.CompletedProcess(args,0,json.dumps([dict(Id=candidate,Os='linux',Architecture='amd64')]),'') if candidate==chosen else subprocess.CompletedProcess(args,1,'','not found')
            with patch('image_identity.subprocess.run',side_effect=run):self.assertEqual(resolve([config,manifest]),chosen)
        with patch('image_identity.subprocess.run',return_value=subprocess.CompletedProcess([],1,'','missing')):self.assertIsNone(resolve([config,manifest]))
    def test_wrong_loaded_descriptor_rejected(self):
        with patch('image_identity.subprocess.run',return_value=subprocess.CompletedProcess([],0,json.dumps([dict(Id='wrong',Os='linux',Architecture='amd64')]),'')):
            with self.assertRaises(ValueError):resolve(['sha256:'+'a'*64])

if __name__=='__main__':unittest.main()

