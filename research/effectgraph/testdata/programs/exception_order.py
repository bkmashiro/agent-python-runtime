def require_positive(value):
    if value < 0:
        raise ValueError("negative")
    return value

checked = require_positive(inputs["value"])
result = sources.read(str(checked))
