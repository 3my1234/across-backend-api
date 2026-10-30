"""Narrow synthetic-evidence exchange for the restricted CI key. No tar extraction."""
import json,os
from pathlib import Path

UPLOADS={'summary.json':10*2**20,'k6.log':256*2**20,'k6-exit-code.txt':16}
DOWNLOADS={'run.json':2*2**20,'fixtures.json':50*2**20,'provider-journal.jsonl':256*2**20}

def path_for(base,name,upload=False):
    limits=UPLOADS if upload else DOWNLOADS
    if name not in limits:raise ValueError('exchange filename denied')
    directory=base/'exchange'; path=directory/name
    if directory.is_symlink() or path.is_symlink():raise ValueError('exchange symlink denied')
    return path,limits[name]

def put(base,name,source):
    manifest=json.loads((base/'manifest.json').read_text())
    if manifest.get('role')!='main' or manifest.get('load_authorized') is not True:raise ValueError('approved main trial required')
    path,limit=path_for(base,name,True)
    temporary=path.with_name(name+'.incoming')
    if temporary.exists():raise ValueError('concurrent/interrupted upload; stop')
    try:
        with temporary.open('xb') as out:
            total=0
            while block:=source.read(65536):
                total+=len(block)
                if total>limit:raise ValueError('exchange upload too large')
                out.write(block)
            out.flush();os.fsync(out.fileno())
        if name=='summary.json':
            doc=json.loads(temporary.read_text())
            if not isinstance(doc,dict) or not doc.get('run_id'):raise ValueError('summary identity missing')
        if name=='k6-exit-code.txt' and temporary.read_text().strip() not in ('0','1','2','99','255','130','143'):raise ValueError('invalid k6 outcome')
        temporary.replace(path)
    finally:temporary.unlink(missing_ok=True)

def get(base,name,destination):
    path,limit=path_for(base,name)
    if path.stat().st_size>limit:raise ValueError('exchange download too large')
    with path.open('rb') as source:
        while block:=source.read(65536):destination.write(block)
