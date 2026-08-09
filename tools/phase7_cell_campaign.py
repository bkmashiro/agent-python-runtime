#!/usr/bin/env /Users/yuzhe/.local/bin/python3.11
import argparse
import hashlib
import json
import os
import re
import shlex
import stat
import subprocess
import tarfile
import time
from pathlib import Path, PurePosixPath

CONTROL = '/tmp/pysolate-p7-gpucluster2-%C'
HOST = 'gpucluster2'
SLOTS = [1, 2, 4, 8, 16, 32, 64]
MAX_ARCHIVE = 2 << 20
EXPECTED_MEMBERS = {
    'ENVIRONMENT.txt', 'RUN_COMPLETE', 'result', 'result/SHA256SUMS',
    'result/cell.json', 'result/cell.json.validation.json',
}


def unique_json(data: bytes):
    def hook(pairs):
        out = {}
        for key, value in pairs:
            if key in out:
                raise ValueError(f'duplicate JSON key: {key}')
            out[key] = value
        return out
    text = data.decode('utf-8')
    decoder = json.JSONDecoder(object_pairs_hook=hook)
    value, end = decoder.raw_decode(text)
    if text[end:].strip():
        raise ValueError('trailing JSON')
    return value


def remote(command: str, *, capture=True):
    argv = ['ssh', '-o', f'ControlPath={CONTROL}', '-o', 'ControlMaster=auto', '-o', 'ControlPersist=70m',
            '-o', 'ServerAliveInterval=30', '-o', 'ServerAliveCountMax=3', '-o', 'BatchMode=yes', HOST, command]
    for attempt in range(4):
        result = subprocess.run(argv, check=False, stdout=subprocess.PIPE if capture else None,
                                stderr=subprocess.PIPE if capture else None)
        if result.returncode == 0:
            return result
        if result.returncode != 255 or attempt == 3:
            raise subprocess.CalledProcessError(result.returncode, argv, output=result.stdout, stderr=result.stderr)
        time.sleep(60)
    raise AssertionError('unreachable SSH retry state')


def pull_regular(path: str, maximum: int) -> bytes:
    code = ('import os,stat,sys; p=sys.argv[1]; m=int(sys.argv[2]); '
            'f=os.open(p,os.O_RDONLY|os.O_NONBLOCK|os.O_NOFOLLOW); s=os.fstat(f); '
            'assert stat.S_ISREG(s.st_mode) and 0<s.st_size<=m; d=b""; '
            '\nwhile len(d)<s.st_size:\n c=os.read(f,min(1048576,s.st_size-len(d))); assert c; d+=c\n'
            'assert os.read(f,1)==b""; os.close(f); sys.stdout.buffer.write(d)')
    command = 'python3 -c ' + shlex.quote(code) + ' ' + shlex.quote(path) + ' ' + str(maximum)
    result = remote(command)
    if len(result.stdout) > maximum:
        raise RuntimeError('remote pull exceeded bound')
    return result.stdout


def read_local_regular(path: Path, maximum: int) -> bytes:
    fd = os.open(path, os.O_RDONLY | os.O_NONBLOCK | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= maximum:
            raise RuntimeError('local input must be a bounded regular file')
        data = b''
        while len(data) < info.st_size:
            chunk = os.read(fd, min(1 << 20, info.st_size - len(data)))
            if not chunk:
                raise RuntimeError('local input was truncated')
            data += chunk
        if os.read(fd, 1):
            raise RuntimeError('local input grew while reading')
        return data
    finally:
        os.close(fd)


def remote_snapshot(root: str, task_jobs):
    code = '''import json,os,stat,sys
root,mapping_text=sys.argv[1],sys.argv[2]; mapping=json.loads(mapping_text)
out=[]
for task_text,job in sorted(mapping.items(),key=lambda item:int(item[0])):
 i=int(task_text)
 tag=f"{job}_{i}"
 row={"task":i}
 for kind,path in (("ready",f"{root}/outbox/READY-{tag}"),("failed",f"{root}/outbox/FAILED-{tag}"),("acked",f"{root}/outbox/ACKED-{tag}")):
  try:
   s=os.lstat(path); row[kind]=stat.S_ISREG(s.st_mode) and not stat.S_ISLNK(s.st_mode) and s.st_size
  except FileNotFoundError: row[kind]=0
 out.append(row)
print(json.dumps(out,separators=(",",":")))'''
    mapping = json.dumps({str(task): job for task, job in sorted(task_jobs.items())}, sort_keys=True, separators=(',', ':'))
    result = remote('python3 -c ' + shlex.quote(code) + ' ' + shlex.quote(root) + ' ' + shlex.quote(mapping))
    return unique_json(result.stdout)


def wait_for(root, task_jobs, field, timeout=7200):
    deadline = time.monotonic() + timeout
    count = len(task_jobs)
    while True:
        rows = remote_snapshot(root, task_jobs)
        failures = [row for row in rows if row['failed']]
        if failures:
            raise RuntimeError(f'campaign failures: {failures}')
        ready = sum(bool(row[field]) for row in rows)
        print(json.dumps({'waiting_for': field, 'ready': ready, 'total': count}, sort_keys=True), flush=True)
        if ready == count:
            return rows
        if time.monotonic() >= deadline:
            raise TimeoutError(f'timeout waiting for {field}')
        time.sleep(60)


def safe_extract(archive: Path, destination: Path):
    destination.mkdir(mode=0o700)
    with tarfile.open(archive, 'r:gz') as tar:
        members = tar.getmembers()
        names = {m.name.rstrip('/') for m in members}
        if names != EXPECTED_MEMBERS or len(members) != len(EXPECTED_MEMBERS):
            raise RuntimeError(f'archive topology drift: {sorted(names)}')
        for member in members:
            name = member.name.rstrip('/')
            pure = PurePosixPath(name)
            if pure.is_absolute() or '..' in pure.parts or member.issym() or member.islnk() or member.isdev() or member.uid != 0 or member.gid != 0 or int(member.mtime) != 0:
                raise RuntimeError('unsafe archive member')
            target = destination.joinpath(*pure.parts)
            if member.isdir():
                target.mkdir(mode=0o700, exist_ok=False)
            elif member.isfile():
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                source = tar.extractfile(member)
                if source is None:
                    raise RuntimeError('missing archive file body')
                fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                try:
                    remaining = member.size
                    while remaining:
                        chunk = source.read(min(1 << 20, remaining))
                        if not chunk:
                            raise RuntimeError('truncated archive member')
                        os.write(fd, chunk)
                        remaining -= len(chunk)
                finally:
                    os.close(fd)
            else:
                raise RuntimeError('unsupported archive member')


def parse_env(path: Path):
    env = {}
    for line in path.read_text().splitlines():
        if line.count('=') != 1:
            raise RuntimeError('invalid environment line')
        key, value = line.split('=', 1)
        if key in env:
            raise RuntimeError('duplicate environment key')
        env[key] = value
    return env


def load_array_job_map(args):
    if getattr(args, 'bound_task_jobs', None) is not None:
        return dict(args.bound_task_jobs)
    if args.array_job_map is None:
        return {task: (args.array_job_ids[0] if len(args.array_job_ids) == 1 else args.array_job_ids[task // 7]) for task in range(args.count)}
    path = args.array_job_map
    try:
        mapping = unique_json(read_local_regular(path, 16384))
    except (OSError, RuntimeError) as error:
        raise RuntimeError('array job map must be a bounded regular file') from error
    if not isinstance(mapping, dict):
        raise RuntimeError('array job map must be an object')
    result = {}
    for task_text, array_job in mapping.items():
        if not isinstance(task_text, str) or not re.fullmatch(r'(0|[1-9][0-9]*)', task_text) or not isinstance(array_job, str) or not re.fullmatch(r'[1-9][0-9]*', array_job):
            raise RuntimeError('array job map entry is not canonical')
        task = int(task_text)
        if task >= args.count:
            raise RuntimeError('array job map task is out of range')
        result[task] = array_job
    return result


def array_job_for_task(args, task):
    mapping = load_array_job_map(args)
    if task not in mapping:
        raise RuntimeError(f'array job is not assigned for task {task}')
    return mapping[task]


def validate_cell(args, task, campaign_root: Path, array_job=None):
    if array_job is None:
        array_job = array_job_for_task(args, task)
    tag = f'{array_job}_{task}'
    ready_path = f'{args.remote_root}/outbox/READY-{tag}'
    ready = pull_regular(ready_path, 256).decode('ascii')
    match = re.fullmatch(r'([0-9a-f]{64})  (cell-(\d+)-result-([0-9]+_[0-9]+)\.tar\.gz)\n', ready)
    if not match or int(match.group(3)) != task or match.group(4) != tag:
        raise RuntimeError(f'READY identity drift for task {task}')
    digest, filename = match.group(1), match.group(2)
    archive_data = pull_regular(f'{args.remote_root}/outbox/{filename}', MAX_ARCHIVE)
    if hashlib.sha256(archive_data).hexdigest() != digest:
        raise RuntimeError('archive digest mismatch')
    cell_root = campaign_root / f'cell-{task:02d}'
    cell_root.mkdir(mode=0o700)
    archive = cell_root / filename
    archive.write_bytes(archive_data)
    (campaign_root / filename).write_bytes(archive_data)
    (campaign_root / f'READY-{tag}').write_bytes(ready.encode('ascii'))
    extracted = cell_root / 'extracted'
    safe_extract(archive, extracted)
    if (extracted / 'RUN_COMPLETE').read_text() != args.source + '\n':
        raise RuntimeError('RUN_COMPLETE drift')
    repeats = args.repeats
    slot = SLOTS[task // repeats]
    repeat = task % repeats
    env = parse_env(extracted / 'ENVIRONMENT.txt')
    required = {'array_job_id': array_job, 'array_task_id': str(task), 'tier': args.tier, 'arm_order': args.order,
                'requested_slots': str(slot), 'repeat_index': str(repeat), 'source_commit': args.source,
                'cpus_per_task': '4', 'gpus_on_node': '1', 'job_partition': 't4'}
    for key, value in required.items():
        if env.get(key) != value:
            raise RuntimeError(f'environment drift task={task} {key}={env.get(key)!r}')
    environment_job_id = env.get('job_id')
    if not isinstance(environment_job_id, str) or not re.fullmatch(r'[1-9][0-9]*', environment_job_id):
        raise RuntimeError(f'environment JobId is not canonical task={task}')
    if env.get('memory_per_node') not in {'16384', '16G'}:
        raise RuntimeError(f'environment memory drift task={task}: {env.get("memory_per_node")!r}')
    result = extracted / 'result'
    sums = (result / 'SHA256SUMS').read_text('ascii').splitlines()
    if len(sums) != 2:
        raise RuntimeError('internal checksum topology drift')
    for line, expected_name in zip(sums, ['cell.json', 'cell.json.validation.json'], strict=True):
        value, name = line.split('  ', 1)
        if name != expected_name or hashlib.sha256((result / name).read_bytes()).hexdigest() != value:
            raise RuntimeError('internal checksum mismatch')
    validation = subprocess.run([args.validator, '-kind', 'validate-phase7-density-cell', '-input', str(result/'cell.json'),
        '-schema', args.schema, '-artifact', args.artifact, '-manifest', args.manifest], check=True, stdout=subprocess.PIPE)
    local_verdict = unique_json(validation.stdout)
    remote_verdict = unique_json((result/'cell.json.validation.json').read_bytes())
    if local_verdict != remote_verdict or not local_verdict.get('valid'):
        raise RuntimeError('validator verdict drift')
    fragment = unique_json((result/'cell.json').read_bytes())
    allocation = fragment['allocation']; cell = fragment['cell']
    if not isinstance(allocation.get('job_id'), str) or not re.fullmatch(r'[1-9][0-9]*', allocation['job_id']) or allocation['job_id'] != environment_job_id or allocation['array_job_id'] != array_job or allocation['array_task_id'] != task or allocation['arm_order'] != args.order or cell != {'sample_index': task, 'repeat_index': repeat, 'requested_slots': slot}:
        raise RuntimeError('fragment scheduler/cell identity drift')
    canonical = campaign_root / f'cell-{task:02d}.json'
    canonical.write_bytes((result/'cell.json').read_bytes())
    return {'sample_index': task, 'fragment_filename': canonical.name, 'fragment_sha256': hashlib.sha256(canonical.read_bytes()).hexdigest(),
        'archive_filename': filename, 'archive_sha256': digest, 'ready_filename': f'READY-{tag}',
        'acked_filename': f'ACKED-{tag}', 'slurm_filename': f'slurm-{task:02d}.json',
        'job_id': allocation['job_id'], 'array_job_id': allocation['array_job_id'],
        'array_task_id': task, 'cgroup_sha256': allocation['cgroup_path_sha256']}, digest


def scontrol(job: str):
    text = remote('scontrol show job ' + shlex.quote(job) + ' -o').stdout.decode()
    values = {}
    for token in text.strip().split():
        if '=' in token:
            key, value = token.split('=', 1); values[key] = value
    return values, text


def assert_scheduler_shape(job: str, expected_job_id: str, expected_array_job: str, expected_task: int, state: str):
    values, text = scontrol(job)
    required = {
        'JobId': expected_job_id, 'ArrayJobId': expected_array_job, 'ArrayTaskId': str(expected_task),
        'JobState': state, 'Partition': 't4', 'NumCPUs': '4', 'NumNodes': '1',
        'MinMemoryNode': '16G', 'Restarts': '0', 'TresPerNode': 'gres/gpu:tesla_t4:1',
    }
    if state == 'COMPLETED':
        required['ExitCode'] = '0:0'
    for key, value in required.items():
        if values.get(key) != value:
            raise RuntimeError(f'Slurm identity/resource drift {job} {key}={values.get(key)!r}: {text}')
    return values, text


def assert_running_shape(job: str, expected_job_id: str, expected_array_job: str, expected_task: int):
    assert_scheduler_shape(job, expected_job_id, expected_array_job, expected_task, 'RUNNING')


def write_ack(path: str, digest: str):
    code = ('import os,sys; p=sys.argv[1]; d=(sys.argv[2]+"\\n").encode(); '
            'f=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o400); '
            'assert os.write(f,d)==len(d); os.fsync(f); os.close(f)')
    command = 'python3 -c ' + shlex.quote(code) + ' ' + shlex.quote(path) + ' ' + digest
    try:
        remote(command)
    except subprocess.CalledProcessError:
        if pull_regular(path, 128) != (digest + '\n').encode('ascii'):
            raise


def assert_completed(job: str, expected_job_id: str, expected_array_job: str, expected_task: int):
    return assert_scheduler_shape(job, expected_job_id, expected_array_job, expected_task, 'COMPLETED')


def validate_and_ack_cells(args, count):
    entries=[None]*count; digests=[None]*count; accepted_task_jobs=[None]*count; terminal_receipts=[None]*count
    deadline=time.monotonic()+7200
    while any(entry is None for entry in entries) or any(receipt is None for receipt in terminal_receipts):
        task_jobs=load_array_job_map(args)
        for task, accepted_array_job in enumerate(accepted_task_jobs):
            if accepted_array_job is not None and task_jobs.get(task) != accepted_array_job:
                raise RuntimeError(f'accepted array job mapping drifted task={task}')
        rows=remote_snapshot(args.remote_root,task_jobs)
        failures=[row for row in rows if row['failed']]
        if failures: raise RuntimeError(f'campaign failures: {failures}')
        for row in rows:
            task=row['task']
            if entries[task] is not None or not row['ready']:
                continue
            array_job=task_jobs[task]
            entry,digest=validate_cell(args,task,args.local_root,array_job)
            prior=[value for value in entries if value is not None]
            if entry['job_id'] in {value['job_id'] for value in prior} or entry['cgroup_sha256'] in {value['cgroup_sha256'] for value in prior}:
                raise RuntimeError('allocation or cgroup identity was reused')
            assert_running_shape(f'{array_job}_{task}',entry['job_id'],array_job,task)
            write_ack(f'{args.remote_root}/ACK-{array_job}_{task}',digest)
            entries[task]=entry; digests[task]=digest; accepted_task_jobs[task]=array_job
            print(json.dumps({'acked_after_validation':task,'accepted_ready':sum(value is not None for value in entries),'total':count},sort_keys=True),flush=True)
        for row in rows:
            task=row['task']
            if entries[task] is None or terminal_receipts[task] is not None or not row['acked']:
                continue
            try:
                array_job=accepted_task_jobs[task]
                values,text=assert_completed(f'{array_job}_{task}',entries[task]['job_id'],array_job,task)
            except (RuntimeError, subprocess.CalledProcessError):
                continue
            if values.get('JobId') != entries[task]['job_id'] or 'gres/gpu:tesla_t4:1' not in text:
                raise RuntimeError(f'completed scheduler identity drift task={task}')
            if not 0 < len(text.encode('utf-8')) <= 16384:
                raise RuntimeError(f'completed scheduler snapshot exceeds bound task={task}')
            terminal_receipts[task]=(values,text)
        if all(entry is not None for entry in entries) and all(receipt is not None for receipt in terminal_receipts):
            final_observed_jobs=load_array_job_map(args)
            for task, accepted_array_job in enumerate(accepted_task_jobs):
                if final_observed_jobs.get(task) != accepted_array_job:
                    raise RuntimeError(f'accepted array job mapping drifted task={task}')
            args.bound_task_jobs={task: accepted_task_jobs[task] for task in range(count)}
            break
        if time.monotonic()>=deadline: raise TimeoutError('timeout waiting for validated READY cells')
        time.sleep(30)
    return entries,digests,accepted_task_jobs,terminal_receipts


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--remote-root', required=True)
    jobs = parser.add_mutually_exclusive_group(required=True)
    jobs.add_argument('--array-job'); jobs.add_argument('--array-jobs'); jobs.add_argument('--array-job-map', type=Path)
    parser.add_argument('--tier', choices=['canary','formal'], required=True); parser.add_argument('--order', choices=['cow-first','non-cow-first'], required=True)
    parser.add_argument('--source', required=True); parser.add_argument('--local-root', type=Path, required=True)
    parser.add_argument('--validator', required=True); parser.add_argument('--schema', required=True); parser.add_argument('--artifact', required=True); parser.add_argument('--manifest', required=True)
    parser.add_argument('--repo', type=Path, required=True)
    args = parser.parse_args(); args.repeats = 1 if args.tier == 'canary' else 3
    args.array_job_ids = [] if args.array_job_map is not None else (args.array_jobs or args.array_job).split(',')
    if args.array_job_map is None and (any(not re.fullmatch(r'[1-9][0-9]*', value) for value in args.array_job_ids) or len(set(args.array_job_ids)) != len(args.array_job_ids) or len(args.array_job_ids) not in ({1} if args.repeats == 1 else {1,3})):
        raise SystemExit('array job mapping is invalid')
    count = 7 * args.repeats; args.count = count; args.bound_task_jobs = None
    if args.local_root.exists():
        raise SystemExit('local acceptance root already exists')
    args.local_root.mkdir(mode=0o700)
    entries,digests,accepted_task_jobs,terminal_receipts=validate_and_ack_cells(args,count)
    final_task_jobs={task: accepted_task_jobs[task] for task in range(count)}
    if any(value is None for value in final_task_jobs.values()):
        raise RuntimeError('accepted array job map did not reach exact campaign coverage')
    args.bound_task_jobs=final_task_jobs
    wait_for(args.remote_root,final_task_jobs,'acked')

    for task, entry in enumerate(entries):
        array_job=array_job_for_task(args,task)
        tag=f'{array_job}_{task}'
        acked=pull_regular(f'{args.remote_root}/outbox/ACKED-{tag}',256)
        if not re.fullmatch((entry['archive_sha256']+r'  \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z\n').encode(),acked):
            raise RuntimeError(f'ACKED receipt drift task={task}')
        (args.local_root/entry['acked_filename']).write_bytes(acked)
        values,text=terminal_receipts[task]
        snapshot_filename=f'slurm-{task:02d}.scontrol.txt'
        snapshot_bytes=text.encode('utf-8')
        (args.local_root/snapshot_filename).write_bytes(snapshot_bytes)
        slurm={'job_id':entry['job_id'],'array_job_id':array_job,'array_task_id':task,'state':'COMPLETED','exit_code':'0:0',
               'partition':'t4','cpus':4,'memory_per_node_mib':16384,'gpu_type':'tesla_t4','gpus':1,'restarts':0,
               'snapshot_filename':snapshot_filename,'snapshot_sha256':hashlib.sha256(snapshot_bytes).hexdigest()}
        (args.local_root/entry['slurm_filename']).write_text(json.dumps(slurm,sort_keys=True,separators=(',',':'))+'\n')
    campaign={'schema_version':1,'evidence_class':'phase7-cell-campaign','arm_order':args.order,'repeats':args.repeats,'entries':entries}
    campaign_path=args.local_root/'campaign-manifest.json'; campaign_path.write_text(json.dumps(campaign,sort_keys=True,separators=(',',':'))+'\n')
    for strategy,name in [('cow-ready-single-use','cow.json'),('single-use-preinitialized','non-cow.json')]:
        subprocess.run([args.validator,'-kind','aggregate-phase7-density-cells','-input',str(campaign_path),'-schema',args.schema,'-artifact',args.artifact,'-manifest',args.manifest,'-class','profile-candidate','-prepared-warmup-profile','numpy-ready-v1','-strategy',strategy,'-samples',str(args.repeats),'-output',str(args.local_root/name)],check=True)
        subprocess.run([args.validator,'-kind','validate-lifecycle-density','-input',str(args.local_root/name),'-schema',str(args.repo/'benchmark/v1/lifecycle-density.schema.json'),'-artifact',args.artifact,'-manifest',args.manifest],check=True,stdout=(args.local_root/(name+'.validation')).open('wb'))
    paired=args.local_root/'paired-summary.json'
    command=['/Users/yuzhe/.local/bin/python3.11',str(args.repo/'tools/phase7_density.py'),'--benchmark',args.validator,'--schema',str(args.repo/'benchmark/v1/lifecycle-density.schema.json'),'--artifact',args.artifact,'--manifest',args.manifest,'--cow',str(args.local_root/'cow.json'),'--non-cow',str(args.local_root/'non-cow.json'),'--output',str(paired)]
    subprocess.run(command,check=True)
    subprocess.run([args.validator,'-kind','validate-phase7-paired-density','-input',str(paired),'-schema',str(args.repo/'benchmark/v1/phase7-paired-density.schema.json')],check=True,stdout=(args.local_root/'paired-summary.validation').open('wb'))
    rederived=args.local_root/'paired-summary.rederived.json'; subprocess.run(command[:-1]+[str(rederived)],check=True)
    if paired.read_bytes()!=rederived.read_bytes(): raise RuntimeError('paired aggregate is not byte-identical')
    array_jobs=[]
    for task in range(count):
        value=final_task_jobs[task]
        if value not in array_jobs: array_jobs.append(value)
    receipt={'status':'ACCEPTED','array_jobs':array_jobs,'tier':args.tier,'arm_order':args.order,'cells':count,'source_commit':args.source,'unique_jobs':count,'unique_cgroups':count,'paired_sha256':hashlib.sha256(paired.read_bytes()).hexdigest()}
    (args.local_root/'ACCEPTED.json').write_text(json.dumps(receipt,sort_keys=True,indent=2)+'\n')
    print(json.dumps(receipt,sort_keys=True))

if __name__ == '__main__': main()
