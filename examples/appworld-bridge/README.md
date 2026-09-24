# AppWorld API bridge (development acceptance)

See [development acceptance results](../../docs/appworld-acceptance.md) for the
native baseline, the predeclared portable subset and limitations. These are
reference-program checks, not model benchmark scores.

This optional example runs Python in a fresh Pysolate Guest and keeps AppWorld's
applications, databases and evaluator in one trusted Python child process.
It is a benchmark adapter, not a complete AppWorld agent or a replacement for
AppWorld's interactive Python environment. It makes no model requests.

## Boundary

```text
provided Python source → Pysolate Guest → declared Host tools
                                             ↓
                               private JSON-lines protocol
                                             ↓
                         AppWorld public APIs and task DB
                                             ↓
                         original AppWorld task evaluator
```

There is no host `execute(source)` operation. The child exposes only public,
non-admin entries from `world.apis`. Bridge controls such as `track`, `client`,
`show`, `_system_datetime` and requester routing names cannot be injected through
API arguments. Business fields are not rejected merely because they resemble
configuration names. Native API response payloads remain values; ordinary API
exceptions are delivered at the Python tool call and can be caught. Malformed
protocol/control requests fail closed. All tools keep `AllowEarlyRead=false`.

The world is created with `load_ground_truth=False`. Only the trusted `finish`
control persists state and invokes the original evaluator. Ground truth and task
difficulty are not injected into the Guest. Evaluation results stay in the
private record. The evaluator, task DB and public API implementations are not
replaced with success stubs.

## Isolated setup

Tested with AppWorld **0.1.3.post1**, source tag commit
`66ad8099e12188ece0d3fe45e661dbc01880813b`, Python 3.11 and official data 0.1.0.
The child refuses other package versions because it uses the pinned environment's
state-persistence hook. This dependency is optional and is not added to Pysolate's
core dependencies.

Use a fresh directory outside this repository:

```sh
uv venv --python 3.11 /private/appworld-host/venv
uv pip install --python /private/appworld-host/venv/bin/python \
  -r examples/appworld-bridge/requirements.txt
export APPWORLD_ROOT=/private/appworld-host/world
mkdir -p "$APPWORLD_ROOT"
/private/appworld-host/venv/bin/appworld install
/private/appworld-host/venv/bin/appworld download data
```

The official data downloader can remove an existing data directory. Run it only
against a deliberately new root, not an existing experiment. The package installer
also uses AppWorld's user cache. Consult upstream instructions before reuse.

## Run one program

Choose an authorized **dev** task locally and write a program using its documented
`apis.<app>.<api>(**arguments)` calls. Keep task descriptions, reference solutions,
inputs and output paths outside Git.

```sh
APPWORLD_ROOT=/private/appworld-host/world \
go run ./examples/appworld-bridge \
  -python /private/appworld-host/venv/bin/python \
  -task DEV_TASK_ID -experiment new-private-experiment \
  -source /private/program.py -guest dist/pysolate.wasm \
  -record /private/guest.jsonl -host-trace /private/host.jsonl
```

`-inputs /private/inputs.json` supplies JSON inputs without rounding integers
through Go float64. The default Guest execution deadline is 30 seconds; the child
and preparation have a three-minute outer deadline. These are example policies,
not WASM limits. Blocked writes as well as response waits honor cancellation.
A stalled worker may be killed, in which case no completed grade is promised.

Files are created exclusively with mode 0600; existing task-output directories
are refused rather than reset. The Guest record contains source/inputs, artifact
identity, bindings, actual output/errors and raw Guest stdout/stderr. The host
record captures each request before dispatch and its result afterward, including
native version and adapter source identity. Native task state/logs are saved on
normal `finish`. Abrupt failure leaves partial records, not invented outcomes.
`finish.complete` indicates evaluation completed, **not that the task passed**.
An ordinary failed task grade is data; execution/protocol/evaluation errors make
the CLI fail. Check the private grade rather than only the process exit code.

AppWorld freezes simulated task time. Host service measurements use the original
performance clock; recording overhead is still part of the surrounding run.
This example is not a production-network latency benchmark.

## Compatibility and evaluation scope

- AppWorld normally retains an IPython namespace across interactions. This example
  executes one program in one fresh Guest. It does not emulate persistent Python
  globals or claim full multi-turn environment equivalence.
- AppWorld's Python package and helper libraries are not available inside the
  default Guest. A reference program importing them may fail before any tool call.
  Do not silently replace those imports with fake implementations.
- Reference-solution execution validates the adapter and environment, not an
  agent's task-solving ability. If source is adapted, retain the original failure,
  describe the edits, and independently run the adapted program against the
  original native evaluator before comparing it in Pysolate.
- Private captured-response playback checks request/output fidelity. It does not
  execute fresh APIs or independently re-run task grading, and is not a new score.
- Original task APIs and the evaluator remain upstream-owned. Local guards and
  Pysolate's existing budgets remain enabled. This bridge is not a submission to
  the official AppWorld leaderboard.

## Licensing and privacy

The upstream [license guidance](https://github.com/StonyBrookNLP/appworld/tree/v0.1.3.post1#license)
distinguishes ordinary source from encrypted benchmark bundles and restricts
plaintext public redistribution of protected contents and derivatives. This
repository contains only generic adapter code and fake-world tests. Never add
unpacked application/test code, task descriptions, solutions, DBs or raw task
trajectories to Git. Use train/dev for development; do not inspect test tasks or
use their detailed evaluations for tuning. Aggregate development checks are not
held-out benchmark scores.

## Checks

```sh
python3 -m unittest discover -s examples/appworld-bridge -p 'test_host.py'
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test ./examples/appworld-bridge
PYSOLATE_GUEST="$PWD/dist/pysolate.wasm" go test -race ./examples/appworld-bridge
```

Unit tests use a fake world and a temporary local protocol worker, not protected
benchmark contents. The real-Guest test checks the actual namespace bridge and
rejection of an unregistered tool. Native AppWorld acceptance requires the
separate installed environment and licensed data.
