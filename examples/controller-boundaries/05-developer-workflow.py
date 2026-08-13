# pyright: reportUndefinedVariable=false

import json
from pathlib import Path
import shutil

status = git.status()
commits = git.log(1)
readme = git.show("HEAD", "README.md")
matches = []
for candidate in sorted(Path("/workspace/src").rglob("*.py")):
    for line_number, line in enumerate(candidate.read_text(encoding="utf-8").splitlines(), 1):
        column = line.find("TODO")
        if column >= 0:
            matches.append({"path": candidate.as_posix(), "line": line_number, "column": column + 1})

report = {
    "clean": status["clean"],
    "commit_message": commits["commits"][0]["message"].strip(),
    "readme": readme.strip(),
    "todo_locations": [f"{match['path']}:{match['line']}" for match in matches],
}
temporary = Path("/tmp/reports/inspection.json")
temporary.parent.mkdir(parents=True, exist_ok=True)
temporary.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
durable = Path("/workspace/reports/inspection.json")
durable.parent.mkdir(parents=True, exist_ok=True)
shutil.copyfile(temporary, durable)
result = report
