# pyright: reportUndefinedVariable=false

import json
from pathlib import Path
from pysolate import workspace

status = git.status()
commits = git.log(1)
readme = git.show("HEAD", "README.md")
matches = workspace.search("TODO", path="src", glob="*.py")
report = {
    "clean": status["clean"],
    "commit_message": commits["commits"][0]["message"].strip(),
    "readme": readme.strip(),
    "todo_locations": [f"{match['path']}:{match['line']}" for match in matches],
}
Path("/workspace/reports").mkdir(parents=True, exist_ok=True)
Path("/workspace/reports/inspection.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
result = report
