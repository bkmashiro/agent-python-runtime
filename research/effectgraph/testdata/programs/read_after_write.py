def write_value():
    return workspace.write_text("value.txt", "new")

def read_value():
    return sources.read("value.txt")

write_value()
result = read_value()
