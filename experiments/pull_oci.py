"""Load official public OCI images through the session proxy, without changing Docker daemon settings."""
import concurrent.futures
import gzip
import hashlib
import json
import os
import subprocess
import sys
import tarfile
from pathlib import Path
import requests

CACHE = Path(os.environ.get('MAGPIE_OCI_CACHE', '/mnt/data2/weizhiwei/magpie-auto/bench-runtime/oci-cache'))
CACHE.mkdir(parents=True,exist_ok=True)
ACCEPT = ', '.join(['application/vnd.docker.distribution.manifest.list.v2+json', 'application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.v2+json', 'application/vnd.oci.image.manifest.v1+json'])

def load_image(image):
    if subprocess.run(['docker','image','inspect',image],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode == 0:
        print('Image already present:',image,flush=True); return
    repo, _, tag = image.partition(':'); tag = tag or 'latest'
    auth = requests.get('https://auth.docker.io/token',params={'service':'registry.docker.io','scope':f'repository:{repo}:pull'},timeout=30)
    auth.raise_for_status()
    headers = {'Authorization':'Bearer '+auth.json()['token'], 'Accept':ACCEPT}
    def manifest(reference):
        r=requests.get(f'https://registry-1.docker.io/v2/{repo}/manifests/{reference}',headers=headers,timeout=45)
        if not r.ok: raise RuntimeError(f'Official registry manifest HTTP {r.status_code} for {image}')
        return r.json()
    spec=manifest(tag)
    if 'manifests' in spec:
        selected=next(m for m in spec['manifests'] if m['platform']['os']=='linux' and m['platform']['architecture']=='amd64')
        spec=manifest(selected['digest'])
    def blob(entry):
        digest=entry['digest'].split(':',1)[1];p=CACHE/digest
        if p.exists() and hashlib.sha256(p.read_bytes()).hexdigest()==digest:return p
        r=requests.get(f'https://registry-1.docker.io/v2/{repo}/blobs/sha256:{digest}',headers=headers,stream=True,timeout=120)
        if not r.ok: raise RuntimeError(f'Official registry blob HTTP {r.status_code} for {image}')
        h=hashlib.sha256()
        with p.with_suffix('.tmp').open('wb') as out:
            for chunk in r.iter_content(1048576):h.update(chunk);out.write(chunk)
        if h.hexdigest()!=digest:raise RuntimeError('Official registry blob checksum mismatch')
        p.with_suffix('.tmp').replace(p)
        return p
    config=blob(spec['config'])
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool: layers=list(pool.map(blob,spec['layers']))
    archive=CACHE/'load.tar'
    with tarfile.open(archive,'w') as out:
        config_name=config.name+'.json';out.add(config,arcname=config_name)
        layer_names=[]
        for i,layer in enumerate(layers):
            name=layer.name+'/layer.tar';layer_names.append(name)
            raw=CACHE/(layer.name+'.tar')
            if not raw.exists():
                with layer.open('rb') as source:
                    magic=source.read(2)
                if magic==b'\x1f\x8b':
                    with gzip.open(layer,'rb') as source,raw.open('wb') as target:
                        while chunk:=source.read(1048576):target.write(chunk)
                else:
                    raw=layer
            out.add(raw,arcname=name)
        p=CACHE/'manifest.json';p.write_text(json.dumps([{'Config':config_name,'RepoTags':[image],'Layers':layer_names}]))
        out.add(p,arcname='manifest.json')
    with archive.open('rb') as data:subprocess.run(['docker','load'],stdin=data,check=True,stdout=subprocess.DEVNULL)
    archive.unlink()
    print('Loaded official image:',image,flush=True)

if __name__=='__main__':
    for image in sys.argv[1:]: load_image(image)
