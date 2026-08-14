def load_first():
    return sources.read("first")

def load_second():
    return sources.read("second")

first = load_first()
second = load_second()
result = [first, second]
