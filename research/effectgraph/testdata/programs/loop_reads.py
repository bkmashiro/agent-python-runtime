values = []
for key in inputs["keys"]:
    values.append(sources.read(key))
result = values
