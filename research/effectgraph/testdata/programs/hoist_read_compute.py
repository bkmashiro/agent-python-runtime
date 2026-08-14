def load_remote():
    return sources.read("slow")

def compute_local(value):
    return (((value * value) + value) * 17) - 3

remote = load_remote()
local = compute_local(inputs["value"])
result = [remote, local]
