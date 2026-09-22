#!/usr/bin/env python3
"""Linux HTTP acceptance and paired loopback COW comparison."""
import json, os, socket, subprocess, sys, tempfile, time, urllib.request, urllib.error
from pathlib import Path
root=Path(sys.argv[1]).resolve()
out=root/("results-"+os.environ["SLURM_JOB_ID"]);out.mkdir()
affinity=sorted(getattr(os,"sched_getaffinity")(0));getattr(os,"sched_setaffinity")(0,affinity[:2]);os.environ["GOMAXPROCS"]="2"
for repeat in range(3):
 for enabled in ([False,True] if repeat%2==0 else [True,False]):
  with (out/f"http-{repeat}-{enabled}.jsonl").open("w") as f:
   subprocess.run([str(root/"bench"),"-guest",str(root/"pysolate.wasm"),"-iterations","50","-concurrency","1","-max-active","2","-cow-data-image="+str(enabled).lower()],stdout=f,check=True,timeout=120)
def request(base,path,body=None):
 data=None if body is None else json.dumps(body).encode()
 req=urllib.request.Request(base+path,data=data,headers={"Content-Type":"application/json"})
 try:
  with urllib.request.urlopen(req,timeout=10) as response:return response.status,json.load(response)
 except urllib.error.HTTPError as e:return e.code,json.load(e)
for kind in ["server","durable"]:
 with tempfile.TemporaryDirectory(prefix="pysolate-cli-") as tmp:
  with socket.socket() as s:s.bind(("127.0.0.1",0));port=s.getsockname()[1]
  base=f"http://127.0.0.1:{port}"
  flags=[str(root/kind),"-guest",str(root/"pysolate.wasm"),"-listen",f"127.0.0.1:{port}","-cow-data-image"]
  if kind=="durable":flags += ["-db",tmp+"/runs.db","-cow-data-image-seed","acceptance"]
  with (out/f"{kind}.log").open("w") as log:
   process=subprocess.Popen(flags,stdout=log,stderr=log)
   try:
    deadline=time.monotonic()+90
    while True:
     assert process.poll() is None,"server exited"
     try:
      if request(base,"/healthz")[0]==200:break
     except urllib.error.URLError:pass
     if time.monotonic()>deadline:raise TimeoutError("server readiness")
     time.sleep(.1)
    if kind=="server":
     status,result=request(base,"/v1/run",{"source":"result=42"});assert status==200 and result["value"]==42
    else:
     status,_=request(base,"/v1/durable/runs",{"id":"bad","source":"result=42","seed":"wrong"});assert status==400
     assert request(base,"/v1/durable/runs/bad")[0]==404
     assert request(base,"/v1/durable/runs",{"id":"good","source":"result=42","seed":"acceptance"})[0]==201
     status,result=request(base,"/v1/durable/runs/good/attempts",{});assert status==200 and result["value"]==42
     status,result=request(base,"/v1/durable/runs/good/history?limit=1");assert status==200 and result["calls"]==[]
    print(kind,"CLI HTTP acceptance passed",flush=True)
   finally:
    process.terminate()
    try:process.wait(timeout=35)
    except subprocess.TimeoutExpired:process.kill();process.wait();raise
(out/"COMPLETE").write_text("6 paired service processes; two CLI HTTP acceptance cases\n")
