# pyright: reportUndefinedVariable=false

items = sources.demo_catalog()
manifest = sources.benchmark_manifest()
case = manifest["cases"][0]
quality_metric = next(metric for metric in case["metrics"] if metric["id"] == "quality")
maximum = quality_metric["bounds"]["maximum"]
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
