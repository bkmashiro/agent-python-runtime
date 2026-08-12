# pyright: reportUndefinedVariable=false

items = sources.demo_catalog()
ranked = sorted(
    (item for item in items if item["score"] >= inputs["minimum_score"]),
    key=lambda item: (-item["score"], item["id"]),
)
result = {
    "count": len(ranked),
    "ids": [item["id"] for item in ranked],
    "top": ranked[0]["id"] if ranked else None,
}
