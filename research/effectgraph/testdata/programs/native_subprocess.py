import subprocess

result = subprocess.run(["true"], check=True).returncode
