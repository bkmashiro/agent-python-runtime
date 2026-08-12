# pyright: reportUndefinedVariable=false

import json
from pysolate import fs

status = git.status()
commits = git.log(1)
readme = git.show("HEAD", "README.md")
matches = fs.search("TODO", path="/workspace/src", glob="*.py")
report = {
    "clean": status["clean"],
    "commit_message": commits["commits"][0]["message"].strip(),
    "readme": readme.strip(),
    "todo_locations": [f"{match['path']}:{match['line']}" for match in matches],
}
fs.mkdir("/tmp/reports")
fs.write_text("/tmp/reports/inspection.json", json.dumps(report, indent=2, sort_keys=True) + "\n")
fs.mkdir("/workspace/reports")
fs.copy("/tmp/reports/inspection.json", "/workspace/reports/inspection.json")
result = report
