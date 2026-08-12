# pyright: reportUndefinedVariable=false

values = inputs["values"]
kept = [value for value in values if value >= inputs["minimum"]]
result = {
    "count": len(kept),
    "descending": sorted(kept, reverse=True),
    "total": sum(kept),
}
