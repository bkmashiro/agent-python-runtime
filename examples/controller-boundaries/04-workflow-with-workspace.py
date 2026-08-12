# pyright: reportUndefinedVariable=false

import json
from pathlib import Path

items = sources.demo_catalog()
manifest = sources.benchmark_manifest()
case = manifest["cases"][0]
quality = next(metric for metric in case["metrics"] if metric["id"] == "quality")
maximum = quality["bounds"]["maximum"]
ranked = sorted(
    (
        {"id": item["id"], "normalized_score": item["score"] / maximum}
        for item in items
    ),
    key=lambda item: (-item["normalized_score"], item["id"]),
)
result = {
    "case_id": case["id"],
    "ranked": ranked,
    "suite_id": manifest["suite"]["id"],
}
report_path = Path("/workspace/reports/ranking.json")
report_path.parent.mkdir(parents=True, exist_ok=True)
report_path.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
