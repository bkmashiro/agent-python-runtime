def write_first():
    return workspace.write_text("same.txt", "first")

def write_second():
    return workspace.write_text("same.txt", "second")

write_first()
write_second()
result = True
